// Package pmtiles reads tiles from a PMTiles version 3 archive
// (github.com/protomaps/PMTiles, spec/v3), the single-file tile format
// Planetiler writes with --output=<name>.pmtiles. It reads from any
// io.ReaderAt, so the archive can be a file (Open) or bytes in the binary:
//
//	//go:embed croatia-z14.pmtiles
//	var archive string
//
//	tiles, err := pmtiles.New(strings.NewReader(archive))
//
// A Reader is a birdseye.Tiles and is safe for concurrent use. It keeps
// the root directory and the leaf directories it has read in memory; tiles
// are read when asked for, and come back as stored (gzipped for
// Planetiler's archives, which birdseye reads as they are). A tile the
// archive does not hold is an error that is fs.ErrNotExist.
package pmtiles

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
)

// Compression is how the archive stores its directories or its tiles.
type Compression uint8

const (
	CompressionUnknown Compression = 0
	CompressionNone    Compression = 1
	CompressionGzip    Compression = 2
	CompressionBrotli  Compression = 3
	CompressionZstd    Compression = 4
)

// TileType is what the tiles are; birdseye reads TileTypeMVT.
type TileType uint8

const (
	TileTypeUnknown TileType = 0
	TileTypeMVT     TileType = 1
)

// Header is the archive's fixed 127-byte header.
type Header struct {
	RootOffset, RootLength         uint64
	MetadataOffset, MetadataLength uint64
	LeafOffset, LeafLength         uint64
	DataOffset, DataLength         uint64
	AddressedTiles                 uint64
	TileEntries                    uint64
	TileContents                   uint64
	Clustered                      bool
	InternalCompression            Compression
	TileCompression                Compression
	TileType                       TileType
	MinZoom, MaxZoom               uint8
	// Bounds and centre in degrees.
	MinLon, MinLat, MaxLon, MaxLat float64
	CenterZoom                     uint8
	CenterLon, CenterLat           float64
}

const (
	headerLength = 127
	// maxRead bounds one directory or tile read, so a damaged length
	// cannot ask for gigabytes.
	maxRead = 64 << 20
	// maxDepth is how many directories a lookup follows: the root and
	// leaves below it (the spec's writers use at most two levels).
	maxDepth = 4
	// leafCacheSize is how many leaf directories are kept.
	leafCacheSize = 256
)

// ErrNotPMTiles is a file that does not start with a version 3 header.
var ErrNotPMTiles = errors.New("pmtiles: not a PMTiles v3 archive")

// Reader reads tiles from an archive.
type Reader struct {
	src    io.ReaderAt
	header Header
	root   []entry

	mu     sync.Mutex
	leaves map[uint64][]entry // by offset in the leaf section
}

// New reads the archive's header and root directory. It refuses tile
// compression other than none or gzip, which birdseye cannot read.
func New(src io.ReaderAt) (*Reader, error) {
	raw := make([]byte, headerLength)
	if _, err := src.ReadAt(raw, 0); err != nil {
		return nil, fmt.Errorf("pmtiles: header: %w", err)
	}
	h, err := parseHeader(raw)
	if err != nil {
		return nil, err
	}
	switch h.TileCompression {
	case CompressionUnknown, CompressionNone, CompressionGzip:
	default:
		return nil, fmt.Errorf("pmtiles: tile compression %d: birdseye reads gzip or none", h.TileCompression)
	}
	r := &Reader{src: src, header: h, leaves: map[uint64][]entry{}}
	if r.root, err = r.directory(h.RootOffset, h.RootLength); err != nil {
		return nil, fmt.Errorf("pmtiles: root directory: %w", err)
	}
	return r, nil
}

// Header is the archive's header.
func (r *Reader) Header() Header { return r.header }

// Tile reads one tile in XYZ numbering. A tile the archive does not hold
// (or no archive could: a position off the zoom's grid) is an error that
// is fs.ErrNotExist. The context is looked at before the archive is read;
// a read under way is the io.ReaderAt's to finish.
func (r *Reader) Tile(ctx context.Context, z, x, y int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if z < 0 || z > 31 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return nil, notExist(z, x, y)
	}
	id := TileID(uint8(z), uint32(x), uint32(y))
	dir := r.root
	for range maxDepth {
		e, ok := find(dir, id)
		if !ok {
			return nil, notExist(z, x, y)
		}
		if e.runLength > 0 {
			data, err := r.read(r.header.DataOffset+e.offset, uint64(e.length))
			if err != nil {
				return nil, fmt.Errorf("pmtiles: tile %d/%d/%d: %w", z, x, y, err)
			}
			return data, nil
		}
		var err error
		if dir, err = r.leaf(e.offset, uint64(e.length)); err != nil {
			return nil, fmt.Errorf("pmtiles: tile %d/%d/%d: %w", z, x, y, err)
		}
	}
	return nil, fmt.Errorf("pmtiles: tile %d/%d/%d: directories nested deeper than %d", z, x, y, maxDepth)
}

// notExist is the error for a tile the archive does not hold.
func notExist(z, x, y int) error {
	return fmt.Errorf("pmtiles: tile %d/%d/%d: %w", z, x, y, fs.ErrNotExist)
}

// leaf is a leaf directory, read once.
func (r *Reader) leaf(offset, length uint64) ([]entry, error) {
	r.mu.Lock()
	dir, ok := r.leaves[offset]
	r.mu.Unlock()
	if ok {
		return dir, nil
	}
	dir, err := r.directory(r.header.LeafOffset+offset, length)
	if err != nil {
		return nil, fmt.Errorf("leaf directory: %w", err)
	}
	r.mu.Lock()
	if len(r.leaves) >= leafCacheSize {
		clear(r.leaves) // a bound, not a policy
	}
	r.leaves[offset] = dir
	r.mu.Unlock()
	return dir, nil
}

// directory reads, unpacks and decodes one directory.
func (r *Reader) directory(offset, length uint64) ([]entry, error) {
	raw, err := r.read(offset, length)
	if err != nil {
		return nil, err
	}
	switch r.header.InternalCompression {
	case CompressionUnknown, CompressionNone:
	case CompressionGzip:
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if raw, err = io.ReadAll(io.LimitReader(zr, maxRead)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("internal compression %d: only gzip or none is read", r.header.InternalCompression)
	}
	return decodeDirectory(raw)
}

func (r *Reader) read(offset, length uint64) ([]byte, error) {
	if length > maxRead {
		return nil, fmt.Errorf("a read of %d bytes", length)
	}
	buf := make([]byte, length)
	n, err := r.src.ReadAt(buf, int64(offset))
	if n == len(buf) {
		return buf, nil // io.ReaderAt may answer EOF with the last byte
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return nil, err
}

// File is a Reader over an archive on disk.
type File struct {
	*Reader
	f *os.File
}

// Open opens an archive file.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := New(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &File{Reader: r, f: f}, nil
}

// Close closes the file.
func (f *File) Close() error { return f.f.Close() }

func parseHeader(b []byte) (Header, error) {
	if len(b) < headerLength || string(b[:7]) != "PMTiles" || b[7] != 3 {
		return Header{}, ErrNotPMTiles
	}
	u := func(at int) uint64 { return binary.LittleEndian.Uint64(b[at:]) }
	deg := func(at int) float64 { return float64(int32(binary.LittleEndian.Uint32(b[at:]))) / 1e7 }
	return Header{
		RootOffset: u(8), RootLength: u(16),
		MetadataOffset: u(24), MetadataLength: u(32),
		LeafOffset: u(40), LeafLength: u(48),
		DataOffset: u(56), DataLength: u(64),
		AddressedTiles: u(72), TileEntries: u(80), TileContents: u(88),
		Clustered:           b[96] == 1,
		InternalCompression: Compression(b[97]),
		TileCompression:     Compression(b[98]),
		TileType:            TileType(b[99]),
		MinZoom:             b[100], MaxZoom: b[101],
		MinLon: deg(102), MinLat: deg(106), MaxLon: deg(110), MaxLat: deg(114),
		CenterZoom: b[118], CenterLon: deg(119), CenterLat: deg(123),
	}, nil
}

// entry is one directory entry: runLength tiles from tileID on share the
// bytes at offset (in the data section), or, with a runLength of 0, a leaf
// directory at offset (in the leaf section).
type entry struct {
	tileID    uint64
	offset    uint64
	length    uint32
	runLength uint32
}

// decodeDirectory reads a directory's entries: their count, then each
// column in turn as varints (tile ids as deltas, run lengths, lengths, and
// offsets plus one, 0 meaning right after the previous entry's bytes).
func decodeDirectory(b []byte) ([]entry, error) {
	r := bytes.NewReader(b)
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("directory: %w", err)
	}
	if n > uint64(len(b))/4 { // every entry takes at least four bytes, one a column
		return nil, fmt.Errorf("directory: %d entries in %d bytes", n, len(b))
	}
	entries := make([]entry, n)
	next := func() uint64 {
		if err != nil {
			return 0
		}
		var v uint64
		v, err = binary.ReadUvarint(r)
		return v
	}
	var id uint64
	for i := range entries {
		id += next()
		entries[i].tileID = id
	}
	for i := range entries {
		entries[i].runLength = uint32(next())
	}
	for i := range entries {
		entries[i].length = uint32(next())
	}
	for i := range entries {
		v := next()
		switch {
		case v > 0:
			entries[i].offset = v - 1
		case i > 0:
			entries[i].offset = entries[i-1].offset + uint64(entries[i-1].length)
		case err == nil: // nothing precedes the first entry
			err = errors.New("the first entry has no offset")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("directory: %w", err)
	}
	return entries, nil
}

// find is the entry for a tile id: the last entry at or before it, when
// it is a leaf directory or its run reaches the id.
func find(entries []entry, id uint64) (entry, bool) {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].tileID > id }) - 1
	if i < 0 {
		return entry{}, false
	}
	e := entries[i]
	if e.runLength == 0 || id-e.tileID < uint64(e.runLength) {
		return e, true
	}
	return entry{}, false
}

// TileID is a tile's position on the archive's Hilbert curve: the tiles of
// every lower zoom first, then the tile's place along the curve at its
// zoom.
func TileID(z uint8, x, y uint32) uint64 {
	var id uint64
	for i := uint8(0); i < z; i++ {
		id += 1 << (2 * i)
	}
	n := uint64(1) << z
	tx, ty := uint64(x), uint64(y)
	for s := n >> 1; s > 0; s >>= 1 {
		var rx, ry uint64
		if tx&s != 0 {
			rx = 1
		}
		if ty&s != 0 {
			ry = 1
		}
		id += s * s * ((3 * rx) ^ ry)
		if ry == 0 { // rotate the quadrant
			if rx == 1 {
				tx, ty = n-1-tx, n-1-ty
			}
			tx, ty = ty, tx
		}
	}
	return id
}
