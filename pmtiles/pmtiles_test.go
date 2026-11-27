package pmtiles

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestTileIDFollowsTheHilbertCurve: the spec's first ids, and every tile
// of a zoom on its own id after the lower zooms' tiles.
func TestTileIDFollowsTheHilbertCurve(t *testing.T) {
	for _, c := range []struct {
		z    uint8
		x, y uint32
		id   uint64
	}{{0, 0, 0, 0}, {1, 0, 0, 1}, {1, 0, 1, 2}, {1, 1, 1, 3}, {1, 1, 0, 4}, {2, 0, 0, 5}} {
		if got := TileID(c.z, c.x, c.y); got != c.id {
			t.Errorf("TileID(%d, %d, %d) = %d, want %d", c.z, c.x, c.y, got, c.id)
		}
	}
	for z := uint8(0); z <= 5; z++ {
		first := (uint64(1)<<(2*z) - 1) / 3
		seen := map[uint64]bool{}
		for x := uint32(0); x < 1<<z; x++ {
			for y := uint32(0); y < 1<<z; y++ {
				id := TileID(z, x, y)
				if id < first || id >= first+1<<(2*z) || seen[id] {
					t.Fatalf("zoom %d: tile %d/%d has id %d", z, x, y, id)
				}
				seen[id] = true
			}
		}
	}
}

// archive builds a small archive the way the spec lays one out: the
// header, the root directory, the leaf directories, then the tile data,
// with directories gzipped.
type archive struct {
	root   []entry
	leaves [][]entry
	data   []byte
	tiles  Compression
}

func (a archive) bytes(t *testing.T) []byte {
	t.Helper()
	gz := func(b []byte) []byte {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(b)
		_ = zw.Close()
		return buf.Bytes()
	}
	root := gz(encodeDirectory(a.root))
	var leaves []byte
	for _, l := range a.leaves {
		leaves = append(leaves, gz(encodeDirectory(l))...)
	}
	h := make([]byte, headerLength)
	copy(h, "PMTiles")
	h[7] = 3
	put := func(at int, v uint64) { binary.LittleEndian.PutUint64(h[at:], v) }
	put(8, headerLength)
	put(16, uint64(len(root)))
	put(40, headerLength+uint64(len(root)))
	put(48, uint64(len(leaves)))
	put(56, headerLength+uint64(len(root)+len(leaves)))
	put(64, uint64(len(a.data)))
	h[97] = byte(CompressionGzip)
	h[98] = byte(a.tiles)
	h[99] = byte(TileTypeMVT)
	out := append(h, root...)
	out = append(out, leaves...)
	return append(out, a.data...)
}

func encodeDirectory(es []entry) []byte {
	b := binary.AppendUvarint(nil, uint64(len(es)))
	var last uint64
	for _, e := range es {
		b = binary.AppendUvarint(b, e.tileID-last)
		last = e.tileID
	}
	for _, e := range es {
		b = binary.AppendUvarint(b, uint64(e.runLength))
	}
	for _, e := range es {
		b = binary.AppendUvarint(b, uint64(e.length))
	}
	for i, e := range es {
		if i > 0 && e.offset == es[i-1].offset+uint64(es[i-1].length) {
			b = binary.AppendUvarint(b, 0)
		} else {
			b = binary.AppendUvarint(b, e.offset+1)
		}
	}
	return b
}

// zoom2 is the zoom-2 tile with each id.
func zoom2() map[uint64][2]int {
	out := map[uint64][2]int{}
	for x := range 4 {
		for y := range 4 {
			out[TileID(2, uint32(x), uint32(y))] = [2]int{x, y}
		}
	}
	return out
}

func TestReaderFollowsLeavesRunsAndContiguousOffsets(t *testing.T) {
	// Tiles 5 ("aaa"), 6 and 7 (one run sharing "bbb") and 9 ("cc", right
	// after "bbb", so its offset is written as 0) behind two leaves.
	leaf1 := []entry{{tileID: 5, offset: 0, length: 3, runLength: 1}, {tileID: 6, offset: 3, length: 3, runLength: 2}}
	leaf2 := []entry{{tileID: 9, offset: 6, length: 2, runLength: 1}}
	l1 := len(gzipped(t, encodeDirectory(leaf1)))
	a := archive{
		root: []entry{
			{tileID: 5, offset: 0, length: uint32(l1)},
			{tileID: 9, offset: uint64(l1), length: uint32(len(gzipped(t, encodeDirectory(leaf2))))},
		},
		leaves: [][]entry{leaf1, leaf2},
		data:   []byte("aaabbbcc"),
		tiles:  CompressionNone,
	}
	r, err := New(bytes.NewReader(a.bytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	at := zoom2()
	want := map[uint64]string{5: "aaa", 6: "bbb", 7: "bbb", 9: "cc"}
	for id, data := range want {
		xy := at[id]
		got, err := r.Tile(t.Context(), 2, xy[0], xy[1])
		if err != nil || string(got) != data {
			t.Errorf("tile id %d = %q, %v, want %q", id, got, err, data)
		}
	}
	// A tile the archive does not hold is fs.ErrNotExist: between two
	// entries (8), past the last (10), at another zoom, or off the grid.
	missing := [][3]int{{2, at[8][0], at[8][1]}, {2, at[10][0], at[10][1]}, {0, 0, 0}, {2, 4, 0}, {2, -1, 0}, {32, 0, 0}}
	for _, c := range missing {
		if got, err := r.Tile(t.Context(), c[0], c[1], c[2]); got != nil || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("tile %v = %q, %v, want fs.ErrNotExist", c, got, err)
		}
	}
	if len(r.leaves) != 2 {
		t.Errorf("leaves kept = %d, want 2", len(r.leaves))
	}
	// A context that is done reads nothing, and says so in its own word.
	done, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.Tile(done, 2, at[5][0], at[5][1]); got != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("a tile read with a context already done = %q, %v", got, err)
	}
}

// TestAnEntryOfNoBytesIsATile: an entry of length zero is a tile the
// archive holds, with nothing in it, not a missing one.
func TestAnEntryOfNoBytesIsATile(t *testing.T) {
	a := archive{root: []entry{{tileID: 5, offset: 0, length: 0, runLength: 1}}, data: []byte{}, tiles: CompressionNone}
	r, err := New(bytes.NewReader(a.bytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	xy := zoom2()[5]
	if got, err := r.Tile(t.Context(), 2, xy[0], xy[1]); err != nil || len(got) != 0 {
		t.Errorf("an entry of no bytes = %q, %v, want no bytes and no error", got, err)
	}
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func TestNewRefusesWhatItCannotRead(t *testing.T) {
	if _, err := New(bytes.NewReader([]byte("SQLite format 3\x00" + string(make([]byte, 200))))); !errors.Is(err, ErrNotPMTiles) {
		t.Errorf("an SQLite file: %v", err)
	}
	v2 := archive{data: []byte{}, tiles: CompressionGzip}.bytes(t)
	v2[7] = 2
	if _, err := New(bytes.NewReader(v2)); !errors.Is(err, ErrNotPMTiles) {
		t.Errorf("version 2: %v", err)
	}
	if _, err := New(bytes.NewReader(archive{data: []byte{}, tiles: CompressionBrotli}.bytes(t))); err == nil {
		t.Error("brotli tiles were accepted")
	}
	if _, err := New(bytes.NewReader([]byte("PMT"))); err == nil {
		t.Error("a short file was accepted")
	}
}

func TestDecodeDirectoryRefusesWhatCannotBe(t *testing.T) {
	good := encodeDirectory([]entry{{tileID: 5, offset: 0, length: 3, runLength: 1}})
	if es, err := decodeDirectory(good); err != nil || len(es) != 1 || es[0].offset != 0 {
		t.Fatalf("a good directory: %v, %v", es, err)
	}
	// One entry whose offset column says "right after the previous
	// entry's bytes": there is no previous entry.
	noOffset := []byte{1, 5, 1, 3, 0}
	if _, err := decodeDirectory(noOffset); err == nil {
		t.Error("a first entry without an offset was accepted")
	}
	// More entries than the bytes can hold, at four bytes an entry.
	if _, err := decodeDirectory(append([]byte{3}, make([]byte, 8)...)); err == nil {
		t.Error("three entries in nine bytes were accepted")
	}
	// A column that ends early.
	if _, err := decodeDirectory(good[:len(good)-1]); err == nil {
		t.Error("a truncated directory was accepted")
	}
}

// TestDirectoriesAreBounded: a few kilobytes of gzip can unpack to a
// directory of millions of entries (a 65 KB archive kept 448 MB of them).
// The root must end within the first 16 KiB, as the spec lays it out; a
// directory is refused past maxDirectoryBytes, packed or unpacked; and
// the leaves kept are bounded by their entries, not only their number.
func TestDirectoriesAreBounded(t *testing.T) {
	good := archive{root: []entry{{tileID: 5, offset: 0, length: 3, runLength: 1}}, data: []byte("aaa"), tiles: CompressionNone}.bytes(t)
	if _, err := New(bytes.NewReader(good)); err != nil {
		t.Fatalf("a good archive: %v", err)
	}
	for _, c := range []struct {
		name           string
		offset, length uint64
	}{
		{"a root past the first 16 KiB", rootWithin, 10},
		{"a root that runs past it", 127, rootWithin},
		{"a root whose end wraps around", 1<<64 - 5, 10},
	} {
		b := slices.Clone(good)
		binary.LittleEndian.PutUint64(b[8:], c.offset)
		binary.LittleEndian.PutUint64(b[16:], c.length)
		if _, err := New(bytes.NewReader(b)); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}

	// A root that unpacks past the bound: zeros pack a thousandfold.
	bomb := slices.Clone(good[:headerLength])
	root := gzipped(t, make([]byte, maxDirectoryBytes+1))
	if len(root) > rootWithin-headerLength {
		t.Fatalf("the packed root takes %d bytes", len(root))
	}
	binary.LittleEndian.PutUint64(bomb[16:], uint64(len(root)))
	bomb = append(bomb, root...)
	if _, err := New(bytes.NewReader(bomb)); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("a root that unpacks to %d bytes: %v", maxDirectoryBytes+1, err)
	}
	r := &Reader{header: Header{InternalCompression: CompressionNone}, src: bytes.NewReader(nil)}
	if _, err := r.directory(0, maxDirectoryBytes+1); err == nil {
		t.Error("a directory larger than the bound was read")
	}

	// The leaves kept stay within their entries.
	r = &Reader{leaves: map[uint64][]entry{}}
	for i := range 8 {
		r.keep(uint64(i), make([]entry, maxCachedEntries/3))
		if total := r.cached; total > maxCachedEntries || total != len(r.leaves)*(maxCachedEntries/3) {
			t.Fatalf("after %d leaves: %d entries kept in %d leaves", i+1, total, len(r.leaves))
		}
	}
	r.keep(100, make([]entry, 1))
	r.keep(100, make([]entry, 1)) // read twice by two lookups at once: kept once
	if n := len(r.leaves[100]); n != 1 {
		t.Errorf("leaf kept with %d entries", n)
	}
}

// strictReaderAt is an io.ReaderAt over bytes that, like a naive one,
// slices them by the offset it is given and so panics on a negative one.
type strictReaderAt []byte

func (b strictReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// TestOffsetsPastAnyArchiveAreRefused: an entry's offset is added to its
// section's, and a sum that wraps around read bytes from elsewhere, or
// reached the io.ReaderAt as a negative offset.
func TestOffsetsPastAnyArchiveAreRefused(t *testing.T) {
	at := zoom2()
	for _, c := range []struct {
		name   string
		offset uint64
	}{
		{"an offset that wraps the sum around", 1<<64 - 100},
		{"an offset that is negative as an int64", 1 << 63},
	} {
		tile := archive{root: []entry{{tileID: 5, offset: c.offset, length: 3, runLength: 1}}, data: []byte("aaa"), tiles: CompressionNone}
		r, err := New(strictReaderAt(tile.bytes(t)))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := r.Tile(t.Context(), 2, at[5][0], at[5][1]); err == nil {
			t.Errorf("%s: a tile read: %q", c.name, got)
		}
		leaf := archive{root: []entry{{tileID: 5, offset: c.offset, length: 10}}, data: []byte{}, tiles: CompressionNone}
		r, err = New(strictReaderAt(leaf.bytes(t)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Tile(t.Context(), 2, at[5][0], at[5][1]); err == nil {
			t.Errorf("%s: a leaf directory read", c.name)
		}
	}
}
