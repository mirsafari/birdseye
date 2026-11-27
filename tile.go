package birdseye

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
// pmtiles), a directory (DirTiles) or a file system (FSTiles).
//
// Tile answers one tile in XYZ numbering, gzipped or not. A tile the
// source does not hold is an error that is fs.ErrNotExist (to errors.Is),
// as a missing file is; bytes with no error are a tile, also when there
// are none of them. A source that can wait (a network, a slow disk) gives
// up with the context's error once the context is done.
//
// A source is safe for concurrent use: an app that draws more than one
// pin at a time shares one source between the drawings, which call Tile
// from their own goroutines (a drawing itself reads its tiles one after
// another), and Render keeps the bytes it is given only while it draws.
// DirTiles, FSTiles over a file system that is safe for concurrent use
// (an embed.FS, os.DirFS) and a pmtiles.Reader are.
type Tiles interface {
	Tile(ctx context.Context, z, x, y int) ([]byte, error)
}

// FSTiles reads tiles from a file system holding <z>-<x>-<y>.pbf files, as
// the fixtures are stored (birdseyetest embeds them).
func FSTiles(fsys fs.FS) Tiles { return fsTiles{fsys} }

// DirTiles is FSTiles over a directory.
func DirTiles(dir string) Tiles { return fsTiles{os.DirFS(dir)} }

type fsTiles struct{ fsys fs.FS }

// Tile reads one tile file; a missing file is a missing tile, in the
// file system's own words. A file larger than any tile is refused before
// it is read.
func (t fsTiles) Tile(ctx context.Context, z, x, y int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%d-%d-%d.pbf", z, x, y)
	f, err := t.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if info, err := f.Stat(); err == nil {
		if info.Size() > maxTileBytes {
			return nil, fmt.Errorf("birdseye: tile file %s of %d bytes, more than %d", name, info.Size(), maxTileBytes)
		}
		buf.Grow(int(info.Size()) + bytes.MinRead)
	}
	if _, err := buf.ReadFrom(io.LimitReader(f, maxTileBytes+1)); err != nil {
		return nil, err
	}
	if buf.Len() > maxTileBytes {
		return nil, fmt.Errorf("birdseye: tile file %s: more than %d bytes", name, maxTileBytes)
	}
	return buf.Bytes(), nil
}

// TilesFor lists the tiles (z, x, y) a picture of the pin can read: every
// tile within reach of the widest disc, none for a pin off the map.
// Cutting fixtures from an archive takes these.
func TilesFor(pin Pin) [][3]int {
	if !pin.OnMap() {
		return nil
	}
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

// MaxLatitude is where Web Mercator's square world ends, north and south,
// in degrees: the edge of every tiled map, where a map widget's pin
// picker ends too.
const MaxLatitude = 85.0511287798066

// OnMap reports whether the pin is a place Web Mercator has tiles for:
// within MaxLatitude north and south, within 180° east and west, and a
// number at all. Render answers ErrNoTiles for any other pin, without
// reading a tile; an app that takes a pin from outside can hold it to the
// same bound when it is entered. Towards a pole a metre spans ever more
// of the world, so a pin there would ask for tiles without end.
func (p Pin) OnMap() bool {
	return math.Abs(p.Lat) <= MaxLatitude && math.Abs(p.Lng) <= 180
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

// maxTiles is the most tiles one picture reads. A pin on the map takes at
// most 36 at a place's own radius and 144 at WidestRadius (at
// MaxLatitude; 4 and 9 in Croatia), so no radius RenderWith lets through
// reaches it: it bounds the list itself, whatever asks for it.
const maxTiles = 256

// tilesAround lists the tiles that cover a square of the given half-size
// around the pin, west to east and north to south. It is nil when they
// are more than maxTiles: the list is never built, whatever the size.
func tilesAround(f frame, half float64) []tileID {
	n := float64(int(1) << Zoom)
	d := half / f.scale
	fx0, fx1 := math.Floor((f.x0-d)*n), math.Floor((f.x0+d)*n)
	fy0, fy1 := math.Floor((f.y0-d)*n), math.Floor((f.y0+d)*n)
	// Written so that a size that is not a number fails it too.
	if count := (fx1 - fx0 + 1) * (fy1 - fy0 + 1); !(count >= 1 && count <= maxTiles) {
		return nil
	}
	x0, x1, y0, y1 := int(fx0), int(fx1), int(fy0), int(fy1)
	out := make([]tileID, 0, (x1-x0+1)*(y1-y0+1))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			out = append(out, tileID{x, y})
		}
	}
	return out
}
