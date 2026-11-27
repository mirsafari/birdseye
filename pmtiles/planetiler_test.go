package pmtiles_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
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
			want, _ := fixtures.Tile(tz[0], tz[1], tz[2])
			if want == nil {
				continue
			}
			got, err := archive.Tile(tz[0], tz[1], tz[2])
			if err != nil || got == nil {
				t.Fatalf("tile %v: %v, %d bytes", tz, err, len(got))
			}
			if bytes.Equal(gunzip(t, got), gunzip(t, want)) {
				same++
			} else {
				differ++
			}
		}
		a, errA := birdseye.Render(archive, p.Pin)
		if errA != nil && !errors.Is(errA, birdseye.ErrTooSparse) {
			t.Errorf("%s: %v", p.Name, errA)
		}
		if exact {
			b, errB := birdseye.Render(fixtures, p.Pin)
			if !errors.Is(errA, errB) || !bytes.Equal(a.SVG, b.SVG) {
				t.Errorf("%s: the archive draws %d bytes (%v), the fixtures %d (%v)", p.Name, len(a.SVG), errA, len(b.SVG), errB)
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
