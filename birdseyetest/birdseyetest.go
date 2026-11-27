// Package birdseyetest holds birdseye's fixtures for tests, its own and an
// app's: the zoom-14 tiles around eleven places in Croatia, embedded (about
// 700 KB, only in the test binaries that import this), and the places.
// fixtures/README.md says where the tiles come from.
package birdseyetest

import (
	"embed"
	"encoding/json"
	"io/fs"

	"github.com/mirsafari/birdseye"
)

//go:embed fixtures/tiles/*.pbf fixtures/places.json
var files embed.FS

// Tiles are the fixture tiles. Render answers ErrNoTiles away from Places.
func Tiles() birdseye.Tiles {
	sub, err := fs.Sub(files, "fixtures/tiles")
	if err != nil {
		panic(err)
	}
	return birdseye.FSTiles(sub)
}

// Place is a named pin the fixtures cover.
type Place struct {
	Name string
	Pin  birdseye.Pin
	Note string
}

// Places are the pins the fixtures cover, in a fixed order: Split (the pin
// on a building), Zagreb, Varaždin (the pin in a square) and Poreč, then
// seven harder cases, the last one too sparse for a picture.
var Places = load()

// Named is the place of that name; it panics for a name not in Places.
func Named(name string) Place {
	for _, p := range Places {
		if p.Name == name {
			return p
		}
	}
	panic("birdseyetest: no place " + name)
}

func load() []Place {
	data, err := files.ReadFile("fixtures/places.json")
	if err != nil {
		panic(err)
	}
	var raw []struct {
		Name     string
		Lat, Lng float64
		Note     string
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		panic(err)
	}
	out := make([]Place, len(raw))
	for i, r := range raw {
		out[i] = Place{r.Name, birdseye.Pin{Lat: r.Lat, Lng: r.Lng}, r.Note}
	}
	return out
}
