package birdseye

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
)

const (
	// StyleVersion changes whenever the same tiles and pin draw a different
	// picture, so cached pictures are drawn again.
	StyleVersion = 5

	// MinBuildings is the fewest buildings worth a picture; with fewer
	// Render answers ErrTooSparse, and a page is better off with an
	// ordinary map.
	MinBuildings = 40

	// A place is drawn as far around its pin as it takes for the
	// footprints to cover BuiltArea square metres, between MinRadius and
	// MaxRadius (placeRadius).
	MinRadius = 150.0
	MaxRadius = 400.0
	BuiltArea = 25000.0

	// WidestRadius is the widest Options.Radius drawn, in metres; a wider
	// one is ErrTooWide. A drawing's time and size grow with the buildings
	// in its disc: around central Zagreb, 730 of them at 400 m (21 ms, a
	// 450 KB picture), 3,400 at 1,000 m (70 ms, 1.9 MB) and 22,000 at
	// 3,000 m (half a second, 10 MB). No place's own radius is over
	// MaxRadius; an app that takes a radius from outside checks it against
	// this.
	WidestRadius = 1000.0

	// reachMargin is how far beyond the radius tiles are read, for the
	// footprints whose centre is inside the disc and whose walls are not.
	reachMargin = 60.0
)

var (
	// ErrNoTiles is a pin outside the tiles, or off the map altogether
	// (not Pin.OnMap: beyond MaxLatitude north or south, or not a number).
	ErrNoTiles = errors.New("birdseye: no tiles cover the pin")
	// ErrTooWide is an Options.Radius over WidestRadius.
	ErrTooWide = errors.New("birdseye: the radius is too wide to draw")
	// ErrTooSparse is a place with fewer than MinBuildings buildings in
	// the disc: no picture, unless Options.DrawSparse asks for it.
	ErrTooSparse = errors.New("birdseye: too few buildings around the pin")
	// ErrTooComplex is tiles that hold more around the pin than any place
	// does, past one of the bounds a drawing keeps to so that its time
	// and memory stay bounded: broken or hostile tiles, never a real map
	// (the densest place in Croatia uses a seventh of the points allowed).
	ErrTooComplex = errors.New("birdseye: the tiles hold too much around the pin to draw")
)

// Options vary a picture; the zero value is the picture as designed.
type Options struct {
	// Radius in metres, up to WidestRadius; zero is the place's own
	// (placeRadius). A wider one is ErrTooWide.
	Radius float64
	// DrawSparse draws a place of fewer than MinBuildings buildings too,
	// in place of ErrTooSparse, for a tool that looks at such pictures;
	// Picture.Buildings says how many it has.
	DrawSparse bool
}

// Picture is a rendered picture and what went into it.
type Picture struct {
	SVG       []byte
	Radius    float64
	Buildings int  // buildings drawn; a footprint a tile edge cuts in two counts once
	Guessed   int  // of which the height was guessed
	Cycles    int  // order cycles broken
	Faces     int  // walls and roofs drawn
	Pinned    bool // a footprint holds the pin: the pinned building, in the --birdseye-pinned-* colours
}

// Render draws the picture around a pin. With an error there is no
// picture: ErrNoTiles and ErrTooSparse are places without one, the
// context's error a drawing given up (between two tiles, or before the
// picture is drawn from them), ErrTooComplex tiles past a drawing's
// bounds, anything else a tile that could not be read.
func Render(ctx context.Context, tiles Tiles, pin Pin) (Picture, error) {
	return RenderWith(ctx, tiles, pin, Options{})
}

// RenderWith draws the picture with options.
func RenderWith(ctx context.Context, tiles Tiles, pin Pin, opts Options) (Picture, error) {
	if !pin.OnMap() {
		return Picture{}, ErrNoTiles
	}
	radius := opts.Radius
	if radius > WidestRadius {
		return Picture{}, ErrTooWide
	}
	reach := MaxRadius + reachMargin
	if radius > 0 {
		reach = radius + reachMargin
	}
	sc, found, err := loadScene(ctx, tiles, pin, reach)
	if err != nil {
		return Picture{}, err
	}
	if found == 0 {
		return Picture{}, ErrNoTiles
	}
	if !(radius > 0) { // zero, negative or not a number
		radius = placeRadius(sc.prisms)
	}
	pic, err := draw(ctx, sc, radius)
	if err != nil {
		return Picture{}, err
	}
	if pic.Buildings < MinBuildings && !opts.DrawSparse {
		return Picture{}, ErrTooSparse
	}
	return pic, nil
}

// placeRadius is how far around the pin a place is drawn: the smallest
// disc whose footprints (by their centres) cover BuiltArea and number at
// least MinBuildings, between MinRadius and MaxRadius. A town is drawn
// close and a village wider. The built area, not a count of buildings,
// decides, so a quarter of large blocks is drawn as close as an old town
// of small houses; the count keeps a lone hall from being drawn alone.
// The parts of a footprint a tile edge cut count as the one building.
func placeRadius(prisms []*prism) float64 {
	type near struct {
		dist, area float64
		building   *prism
	}
	ns := make([]near, len(prisms))
	for i, p := range prisms {
		c := centroid(p.outer.pts)
		ns[i] = near{math.Hypot(c.x, c.y), footprint(p), p.whole()}
	}
	// Stable, so footprints at one distance (the parts of a tower) keep
	// the tiles' order whatever the sort's algorithm.
	slices.SortStableFunc(ns, func(a, b near) int { return cmp.Compare(a.dist, b.dist) })
	r := MaxRadius
	built := 0.0
	buildings := map[*prism]bool{}
	for _, n := range ns {
		built += n.area
		buildings[n.building] = true
		if built >= BuiltArea && len(buildings) >= MinBuildings {
			r = n.dist
			break
		}
	}
	return max(MinRadius, min(MaxRadius, r))
}

// footprint is a prism's ground area in square metres, courtyards
// excluded (the outer ring runs counter-clockwise, the holes clockwise).
func footprint(p *prism) float64 {
	a := area(p.outer.pts)
	for _, h := range p.holes {
		a += area(h.pts)
	}
	return a
}

// draw cuts the scene to the disc, finds the pinned building, orders the prisms and
// writes the picture. It cuts the scene's prisms in place (to the ground
// north of southEdge, and to the height the camera can see, tallest), so
// a scene is drawn once. It gives up with the context's error once the
// context is done, before it starts or while it orders.
func draw(ctx context.Context, sc scene, radius float64) (Picture, error) {
	if err := ctx.Err(); err != nil {
		return Picture{}, err
	}
	d := &drawing{disc: newDisc(radius)}
	target := 0.0
	guessed := 0
	origin := point{}
	south := southEdge(radius)
	top := tallest(radius)
	buildings := map[*prism]bool{} // the buildings in the disc; true for the pinned one
	for _, p := range inDisc(sc.prisms, radius) {
		holdsPin := holds(p, origin)
		if !p.cutNorthOf(south) {
			continue
		}
		// Before the camera is aimed: it aims at the pinned roof.
		p.lowerTo(top)
		if holdsPin {
			target = max(target, p.z1)
		}
		b := p.whole()
		if _, seen := buildings[b]; !seen && p.guessed {
			guessed++
		}
		buildings[b] = buildings[b] || holdsPin
		d.prisms = append(d.prisms, p)
	}
	// The pinned building is drawn whole: every part of the footprint
	// that holds the pin, not only the part on the pin's side of a tile
	// edge.
	pinned := false
	for _, p := range d.prisms {
		p.pinned = buildings[p.whole()]
		pinned = pinned || p.pinned
	}

	// Ground polygons and lines are cut to the disc.
	for _, pt := range sc.patches {
		var rings [][]point
		for i, r := range pt.rings {
			c := d.disc.clip(r)
			if len(c) < 3 {
				if i == 0 {
					break
				}
				continue
			}
			if a := area(c); (i == 0) != (a > 0) {
				slices.Reverse(c)
			}
			rings = append(rings, c)
		}
		if len(rings) > 0 {
			d.patches = append(d.patches, patch{kind: pt.kind, rings: rings})
		}
	}
	for _, s := range sc.strokes {
		for _, part := range d.disc.cutLine(s.pts) {
			d.strokes = append(d.strokes, stroke{kind: s.kind, width: s.width, pts: part})
		}
	}

	cam := newCamera(radius, target)
	cycles, err := order(ctx, d.prisms, cam.foot())
	if err != nil {
		return Picture{}, err
	}
	d.cycles = cycles
	svg := writeSVG(d, cam)
	return Picture{
		SVG:       svg,
		Radius:    radius,
		Buildings: len(buildings),
		Guessed:   guessed,
		Cycles:    d.cycles,
		Faces:     d.faces,
		Pinned:    pinned,
	}, nil
}

// inDisc is the prisms of the buildings in the disc: a building is in it
// when the centre of a part of it is, and then every part of it is drawn.
// A footprint a tile edge cut in two is one building, drawn whole or not
// at all: a part left out showed the camera the drawn part's cut edge,
// which has no wall, as the missing front of a building at the disc's
// edge.
func inDisc(prisms []*prism, radius float64) []*prism {
	in := map[*prism]bool{}
	for _, p := range prisms {
		if c := centroid(p.outer.pts); math.Hypot(c.x, c.y) <= radius {
			in[p.whole()] = true
		}
	}
	out := make([]*prism, 0, len(in))
	for _, p := range prisms {
		if in[p.whole()] {
			out = append(out, p)
		}
	}
	return out
}

// holds reports whether the footprint, courtyards excluded, holds p.
func holds(pr *prism, p point) bool {
	if !contains(pr.outer.pts, p) {
		return false
	}
	for _, h := range pr.holes {
		if contains(h.pts, p) {
			return false
		}
	}
	return true
}
