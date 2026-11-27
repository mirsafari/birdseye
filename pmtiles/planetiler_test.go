package pmtiles_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/mirsafari/birdseye"
	"github.com/mirsafari/birdseye/birdseyetest"
	"github.com/mirsafari/birdseye/pmtiles"
)

// TestPlanetilerArchiveDrawsWhatTheFixturesDraw reads an archive Planetiler
// wrote (BIRDSEYE_PMTILES, an absolute path to a Croatia archive, since the
// test runs in this directory; it skips without one),
// so the reader is checked against the format's own writer, not this
// package's idea of it: every fixture tile is in the archive and every
// place draws a picture from it, as the release checks its archive. With
// BIRDSEYE_PMTILES_EXACT=1 (an archive from the fixtures' own extract)
// every tile's content and every picture must also be the fixtures'.
func TestPlanetilerArchiveDrawsWhatTheFixturesDraw(t *testing.T) {
	path := os.Getenv("BIRDSEYE_PMTILES")
	if path == "" {
		t.Skip("BIRDSEYE_PMTILES is not set")
	}
	exact := os.Getenv("BIRDSEYE_PMTILES_EXACT") == "1"
	archive, err := pmtiles.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	h := archive.Header()
	if h.TileType != pmtiles.TileTypeMVT || h.MinZoom != birdseye.Zoom || h.MaxZoom != birdseye.Zoom {
		t.Fatalf("header: %+v", h)
	}
	t.Logf("%d tiles (%d entries, %d contents), tiles %d, directories %d, clustered %v",
		h.AddressedTiles, h.TileEntries, h.TileContents, h.TileCompression, h.InternalCompression, h.Clustered)

	fixtures := birdseyetest.Tiles()
	same, differ := 0, 0
	for _, p := range birdseyetest.Places {
		for _, tz := range birdseye.TilesFor(p.Pin) {
			want, err := fixtures.Tile(t.Context(), tz[0], tz[1], tz[2])
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := archive.Tile(t.Context(), tz[0], tz[1], tz[2])
			if err != nil {
				t.Fatalf("tile %v: %v", tz, err)
			}
			if bytes.Equal(gunzip(t, got), gunzip(t, want)) {
				same++
			} else {
				differ++
			}
		}
		// Every place is drawn, the sparse one too, so each has bytes to
		// compare.
		a, errA := birdseye.RenderWith(t.Context(), archive, p.Pin, birdseye.Options{DrawSparse: true})
		if errA != nil || len(a.SVG) == 0 {
			t.Errorf("%s: %d bytes, %v", p.Name, len(a.SVG), errA)
		}
		if exact {
			b, errB := birdseye.RenderWith(t.Context(), fixtures, p.Pin, birdseye.Options{DrawSparse: true})
			if errB != nil || !bytes.Equal(a.SVG, b.SVG) {
				t.Errorf("%s: the archive draws %d bytes, the fixtures %d (%v)", p.Name, len(a.SVG), len(b.SVG), errB)
			}
		}
	}
	t.Logf("fixture tiles: %d the same, %d different", same, differ)
	if exact && differ > 0 {
		t.Errorf("%d fixture tiles differ from the archive's", differ)
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	if len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		return b
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
