package birdseyetest_test

import (
	"errors"
	"testing"

	"github.com/mirsafari/birdseye"
	"github.com/mirsafari/birdseye/birdseyetest"
)

// TestFixturesDrawEveryPlace: the embedded tiles hold every place's
// picture, Split's pin is on a building, and the last place is too sparse.
func TestFixturesDrawEveryPlace(t *testing.T) {
	tiles := birdseyetest.Tiles()
	if len(birdseyetest.Places) != 11 {
		t.Fatalf("%d places", len(birdseyetest.Places))
	}
	for i, p := range birdseyetest.Places {
		pic, err := birdseye.Render(t.Context(), tiles, p.Pin)
		last := i == len(birdseyetest.Places)-1
		if last != errors.Is(err, birdseye.ErrTooSparse) || (!last && err != nil) {
			t.Errorf("%s: %v", p.Name, err)
		}
		if pic.Pinned != (p.Name == "split-riva") {
			t.Errorf("%s: pinned = %v", p.Name, pic.Pinned)
		}
		if last == (len(pic.SVG) > 0) {
			t.Errorf("%s: %d bytes with %v", p.Name, len(pic.SVG), err)
		}
	}
	if birdseyetest.Named("varazdin-centre") != birdseyetest.Places[2] {
		t.Error("Named does not find Varaždin")
	}
	if _, err := birdseye.Render(t.Context(), tiles, birdseye.Pin{Lat: 48.2082, Lng: 16.3738}); !errors.Is(err, birdseye.ErrNoTiles) {
		t.Errorf("Vienna: %v", err)
	}
}
