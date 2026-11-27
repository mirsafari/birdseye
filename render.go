package birdseye

import (
	"errors"
	"math"
	"sort"
)

const (
	// StyleVersion changes whenever the same tiles and pin draw a different
	// picture, so cached pictures are drawn again.
	StyleVersion = 2

	// MinBuildings is the fewest footprints worth a picture; with fewer
	// Render answers ErrTooSparse, and a page is better off with an
	// ordinary map.
	MinBuildings = 40

	// A place is drawn as far around its pin as it takes for the
	// footprints to cover BuiltArea square metres, between MinRadius and
	// MaxRadius (placeRadius).
	MinRadius = 150.0
	MaxRadius = 400.0
	BuiltArea = 25000.0

	// reachMargin is how far beyond the radius tiles are read, for the
	// footprints whose centre is inside the disc and whose walls are not.
	reachMargin = 60.0
)

var (
	// ErrNoTiles is a pin outside the tiles.
	ErrNoTiles = errors.New("birdseye: no tiles cover the pin")
	// ErrTooSparse is a place with fewer than MinBuildings footprints in
	// the disc. The picture comes back anyway, for a dev tool to look at.
	ErrTooSparse = errors.New("birdseye: too few buildings around the pin")
)

// Options vary a picture; the zero value is the picture as designed.
type Options struct {
	// Radius in metres; zero is the place's own (placeRadius).
	Radius float64
}

// Picture is a rendered picture and what went into it.
type Picture struct {
	SVG       []byte
	Radius    float64
	Buildings int  // prisms drawn
	Guessed   int  // of which the height was guessed
	Cycles    int  // order cycles broken
	Faces     int  // walls and roofs drawn
	Pinned    bool // a footprint holds the pin: the pinned building, in the --birdseye-pinned-* colours
}

// Render draws the picture around a pin.
func Render(tiles Tiles, pin Pin) (Picture, error) {
	return RenderWith(tiles, pin, Options{})
}

// RenderWith draws the picture with options.
func RenderWith(tiles Tiles, pin Pin, opts Options) (Picture, error) {
	radius := opts.Radius
	reach := MaxRadius + reachMargin
	if radius > 0 {
		reach = radius + reachMargin
	}
	sc, found, err := loadScene(tiles, pin, reach)
	if err != nil {
		return Picture{}, err
	}
	if found == 0 {
		return Picture{}, ErrNoTiles
	}
	if radius <= 0 {
		radius = placeRadius(sc.prisms)
	}
	pic := draw(sc, radius)
	if pic.Buildings < MinBuildings {
		return pic, ErrTooSparse
	}
	return pic, nil
}

// placeRadius is how far around the pin a place is drawn: the smallest
// disc whose footprints (by their centres) cover BuiltArea and number at
// least MinBuildings, between MinRadius and MaxRadius. A town is drawn
// close and a village wider. The built area, not a count of buildings,
// decides, so a quarter of large blocks is drawn as close as an old town
// of small houses; the count keeps a lone hall from being drawn alone.
func placeRadius(prisms []*prism) float64 {
	type near struct{ dist, area float64 }
	ns := make([]near, len(prisms))
	for i, p := range prisms {
		c := centroid(p.outer.pts)
		ns[i] = near{math.Hypot(c.x, c.y), footprint(p)}
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].dist < ns[j].dist })
	r := MaxRadius
	built := 0.0
	for i, n := range ns {
		built += n.area
		if built >= BuiltArea && i+1 >= MinBuildings {
			r = n.dist
			break
		}
	}
	return math.Max(MinRadius, math.Min(MaxRadius, r))
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
// writes the picture.
func draw(sc scene, radius float64) Picture {
	d := &drawing{radius: radius}
	target := 0.0
	pinned := false
	guessed := 0
	origin := point{}
	for _, p := range sc.prisms {
		c := centroid(p.outer.pts)
		if math.Hypot(c.x, c.y) > radius {
			continue
		}
		p.pinned = holds(p, origin)
		if p.pinned {
			pinned = true
			target = math.Max(target, p.z1)
		}
		if p.guessed {
			guessed++
		}
		d.prisms = append(d.prisms, p)
	}

	// Ground polygons and lines are cut to the disc; the polygon window
	// is the drawn disc itself and lines stop just inside it.
	window := discPolygon(radius, discSegments)
	for _, pt := range sc.patches {
		var rings [][]point
		for i, r := range pt.rings {
			c := clipConvex(r, window)
			if len(c) < 3 {
				if i == 0 {
					break
				}
				continue
			}
			if a := area(c); (i == 0) != (a > 0) {
				reverse(c)
			}
			rings = append(rings, c)
		}
		if len(rings) > 0 {
			d.patches = append(d.patches, patch{kind: pt.kind, rings: rings})
		}
	}
	inner := radius * math.Cos(math.Pi/discSegments)
	for _, s := range sc.strokes {
		for _, part := range clipLineCircle(s.pts, inner) {
			d.strokes = append(d.strokes, stroke{kind: s.kind, width: s.width, pts: part})
		}
	}

	cam := newCamera(radius, target)
	d.cycles = order(d.prisms, cam.foot())
	svg := writeSVG(d, cam)
	return Picture{
		SVG:       svg,
		Radius:    radius,
		Buildings: len(d.prisms),
		Guessed:   guessed,
		Cycles:    d.cycles,
		Faces:     d.faces,
		Pinned:    pinned,
	}
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

func reverse(pts []point) {
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
}
