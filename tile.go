package birdseye

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
)

// Zoom is the zoom level the picture reads: the OpenMapTiles schema's
// highest, where footprints are kept as drawn.
const Zoom = 14

// earthRadius is the Web Mercator sphere's, in metres.
const earthRadius = 6378137.0

// Tiles is where the vector tiles come from: a PMTiles archive (package
// pmtiles), a directory (DirTiles) or a file system (FSTiles). Tile
// answers one tile in XYZ numbering, gzipped or not; a tile that does not
// exist is nil with no error.
type Tiles interface {
	Tile(z, x, y int) ([]byte, error)
}

// FSTiles reads tiles from a file system holding <z>-<x>-<y>.pbf files, as
// the fixtures are stored (birdseyetest embeds them).
func FSTiles(fsys fs.FS) Tiles { return fsTiles{fsys} }

// DirTiles is FSTiles over a directory.
func DirTiles(dir string) Tiles { return fsTiles{os.DirFS(dir)} }

type fsTiles struct{ fsys fs.FS }

// Tile reads one tile file; a missing file is a missing tile.
func (t fsTiles) Tile(z, x, y int) ([]byte, error) {
	b, err := fs.ReadFile(t.fsys, fmt.Sprintf("%d-%d-%d.pbf", z, x, y))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// TilesFor lists the tiles (z, x, y) a picture of the pin can read: every
// tile within reach of the widest disc. Cutting fixtures from an archive
// takes these.
func TilesFor(pin Pin) [][3]int {
	var out [][3]int
	for _, id := range tilesAround(newFrame(pin), MaxRadius+reachMargin) {
		out = append(out, [3]int{Zoom, id.x, id.y})
	}
	return out
}

// Pin is a point on the map, in degrees.
type Pin struct {
	Lat, Lng float64
}

// tileID names one tile at Zoom.
type tileID struct{ x, y int }

// mercator is a position as a fraction of the Web Mercator world, x from
// the antimeridian eastwards and y from the north edge southwards.
func mercator(lat, lng float64) (x, y float64) {
	x = (lng + 180) / 360
	s := math.Sin(lat * math.Pi / 180)
	y = 0.5 - math.Log((1+s)/(1-s))/(4*math.Pi)
	return x, y
}

// frame converts tile units into metres east and north of the pin. Over a
// few hundred metres one scale (the Mercator scale at the pin's latitude)
// is exact to well under a millimetre per metre.
type frame struct {
	x0, y0 float64 // the pin, as a fraction of the world
	scale  float64 // metres on the ground per fraction of the world
}

func newFrame(pin Pin) frame {
	x0, y0 := mercator(pin.Lat, pin.Lng)
	return frame{x0: x0, y0: y0, scale: 2 * math.Pi * earthRadius * math.Cos(pin.Lat*math.Pi/180)}
}

// tile returns where a tile's north-west corner is, as a fraction of the
// world, and the fraction one unit of its extent spans, so tile units
// convert with one multiply and add.
func (f frame) tile(id tileID, extent float64) (ox, oy, unit float64) {
	n := float64(int(1) << Zoom)
	unit = 1 / (n * extent)
	return float64(id.x) / n, float64(id.y) / n, unit
}

// point is a position on the ground in metres: x east, y north of the pin.
type point struct{ x, y float64 }

// toLocal converts a tile position.
func (f frame) toLocal(ox, oy, unit float64, p tpoint) point {
	return point{
		x: (ox + p.x*unit - f.x0) * f.scale,
		y: (f.y0 - (oy + p.y*unit)) * f.scale,
	}
}

// fromLocal is toLocal's inverse, in tile units of the given tile.
func (f frame) fromLocal(ox, oy, unit float64, p point) tpoint {
	return tpoint{
		x: (p.x/f.scale + f.x0 - ox) / unit,
		y: (f.y0 - p.y/f.scale - oy) / unit,
	}
}

// tilesAround lists the tiles that cover a square of the given half-size
// around the pin, west to east and north to south.
func tilesAround(f frame, half float64) []tileID {
	n := float64(int(1) << Zoom)
	d := half / f.scale
	x0, x1 := int(math.Floor((f.x0-d)*n)), int(math.Floor((f.x0+d)*n))
	y0, y1 := int(math.Floor((f.y0-d)*n)), int(math.Floor((f.y0+d)*n))
	var out []tileID
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			out = append(out, tileID{x, y})
		}
	}
	return out
}
