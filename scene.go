package birdseye

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
)

// layers is what the picture reads from a tile.
var layers = map[string]bool{
	"building": true, "water": true, "waterway": true, "park": true,
	"landcover": true, "landuse": true, "transportation": true,
}

// prism is one footprint raised from z0 to z1.
type prism struct {
	outer   ring   // counter-clockwise
	holes   []ring // clockwise
	z0, z1  float64
	guessed bool
	pinned  bool

	// of is the prism this one is a part of, when a tile edge cut their
	// footprint in two (joinParts); nil for a footprint in one piece and
	// for the part the others are counted with.
	of *prism

	// The back-to-front order's working values.
	dist     float64
	az0, az1 float64
}

// whole is the prism that stands for the building: the prism itself, or
// the part its footprint's other parts were joined to.
func (p *prism) whole() *prism {
	for p.of != nil {
		p = p.of
	}
	return p
}

// cutNorthOf cuts the prism to the ground north of the line y metres
// north of the pin (camera.go's southEdge) and reports whether anything is
// left of it.
func (p *prism) cutNorthOf(y float64) bool {
	outer, ok := p.outer.northOf(y)
	if !ok {
		return false
	}
	p.outer = outer
	holes := p.holes[:0]
	for _, h := range p.holes {
		if h, ok := h.northOf(y); ok {
			holes = append(holes, h)
		}
	}
	p.holes = holes
	return true
}

// patchKind is what a ground polygon is, in drawing order.
type patchKind int

const (
	patchGreen patchKind = iota
	patchWater
	patchPaved
)

// patch is a ground polygon: an outer ring and its holes.
type patch struct {
	kind  patchKind
	rings [][]point
}

// strokeKind is what a ground line is.
type strokeKind int

const (
	strokeWater strokeKind = iota
	strokeRoad
)

// stroke is a ground line of a width in metres.
type stroke struct {
	kind  strokeKind
	width float64
	pts   []point
}

// scene is everything within reach of the pin, in metres around it, not
// yet cut to the disc.
type scene struct {
	prisms  []*prism
	patches []patch
	strokes []stroke
}

// loadScene reads the tiles that cover a square of half-size reach around
// the pin, and how many of them exist. A reach that takes more tiles than
// a picture reads is ErrTooWide. It stops between two tiles, with the
// context's error, once the context is done, whether or not the source
// looks at the context itself.
func loadScene(ctx context.Context, src Tiles, pin Pin, reach float64) (scene, int, error) {
	var sc scene
	found := 0
	fr := newFrame(pin)
	ids := tilesAround(fr, reach)
	if ids == nil {
		return sc, 0, ErrTooWide
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return sc, found, err
		}
		data, err := src.Tile(ctx, Zoom, id.x, id.y)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return sc, found, fmt.Errorf("birdseye: tile %d/%d/%d: %w", Zoom, id.x, id.y, err)
		}
		found++
		ls, err := decodeTile(data, layers)
		if err != nil {
			return sc, found, fmt.Errorf("birdseye: tile %d/%d/%d: %w", Zoom, id.x, id.y, err)
		}
		if err := sc.add(fr, id, ls, reach); err != nil {
			return sc, found, fmt.Errorf("birdseye: tile %d/%d/%d: %w", Zoom, id.x, id.y, err)
		}
	}
	joinParts(sc.prisms)
	return sc, found, nil
}

// joinParts finds the footprints a tile edge cut in two (each tile draws
// its own side, so the building is drawn once and without a seam) and
// makes them one building again for what is said of a building as a
// whole: that it is the pinned one, and that it counts as one. Two parts
// belong together when they stand as high as one another and cut edges of
// theirs lie on one tile edge and mostly cover one another. A party wall
// that ends on a tile edge meets the neighbour's cut at a point, not along
// it; and the parts of a tower that stand over one another at different
// heights stay the separate prisms they are away from a tile edge.
func joinParts(prisms []*prism) {
	var cut []*prism
	for _, p := range prisms {
		if p.outer.hasCut() {
			cut = append(cut, p)
		}
	}
	for i, p := range cut {
		for _, q := range cut[i+1:] {
			if a, b := p.whole(), q.whole(); a != b && sameHeights(p, q) && sharesCut(p.outer, q.outer) {
				b.of = a
			}
		}
	}
}

// sameHeights reports whether two prisms can be parts of one footprint:
// they carry its heights. A guessed top may differ, since each tile
// guesses from as much of the footprint as it carries.
func sameHeights(p, q *prism) bool {
	return p.z0 == q.z0 && (p.z1 == q.z1 || p.guessed && q.guessed)
}

// tileReader converts one tile's features to metres and keeps those that
// reach the square around the pin.
type tileReader struct {
	fr        frame
	ox, oy, u float64
	extent    float64
	lo, hi    tpoint // the square around the pin, in tile units
	reach     float64
}

func newTileReader(fr frame, id tileID, extent, reach float64) tileReader {
	ox, oy, u := fr.tile(id, extent)
	t := tileReader{fr: fr, ox: ox, oy: oy, u: u, extent: extent, reach: reach}
	a := fr.fromLocal(ox, oy, u, point{-reach, reach})
	b := fr.fromLocal(ox, oy, u, point{reach, -reach})
	t.lo, t.hi = a, b
	return t
}

// near reports whether a feature's box meets the square around the pin.
func (t tileReader) near(f feature) bool {
	lo, hi, ok := f.bounds()
	return ok && hi.x >= t.lo.x && lo.x <= t.hi.x && hi.y >= t.lo.y && lo.y <= t.hi.y
}

func (t tileReader) local(p tpoint) point { return t.fr.toLocal(t.ox, t.oy, t.u, p) }

// polygons groups a feature's rings into polygons: an exterior ring (its
// area positive in tile units) followed by its holes.
func polygons(paths [][]tpoint) [][][]tpoint {
	var out [][][]tpoint
	for _, p := range paths {
		r := openRing(p)
		if len(r) < 3 {
			continue
		}
		a := tarea(r)
		switch {
		case a > 0:
			out = append(out, [][]tpoint{r})
		case a < 0 && len(out) > 0:
			out[len(out)-1] = append(out[len(out)-1], r)
		}
	}
	return out
}

// clipped clips a ring to the tile's own square, so a feature the
// neighbouring tile also carries (in its buffer) is drawn once, and
// converts it to metres.
func (t tileReader) clipped(r []tpoint) (ring, bool) {
	pts, cut := clipRect(r, t.extent)
	if len(pts) < 3 {
		return ring{}, false
	}
	out := ring{pts: make([]point, len(pts)), cut: cut}
	for i, p := range pts {
		out.pts[i] = t.local(p)
	}
	return out, true
}

func (sc *scene) add(fr frame, id tileID, ls map[string]*layer, reach float64) error {
	if l := ls["building"]; l != nil {
		t := newTileReader(fr, id, l.extent, reach)
		for _, f := range l.features {
			if f.kind != geomPolygon || f.flag("hide_3d") || !t.near(f) {
				continue
			}
			paths, err := f.paths()
			if err != nil {
				return err
			}
			for _, poly := range polygons(paths) {
				if p, ok := t.prism(f, poly); ok {
					sc.prisms = append(sc.prisms, p)
				}
			}
		}
	}
	for _, name := range []string{"landcover", "landuse", "park", "water", "transportation"} {
		l := ls[name]
		if l == nil {
			continue
		}
		t := newTileReader(fr, id, l.extent, reach)
		for _, f := range l.features {
			if f.kind != geomPolygon || !t.near(f) {
				continue
			}
			kind, ok := patchOf(name, f)
			if !ok {
				continue
			}
			paths, err := f.paths()
			if err != nil {
				return err
			}
			for _, poly := range polygons(paths) {
				var rings [][]point
				for i, r := range poly {
					c, ok := t.clipped(r)
					if !ok {
						if i == 0 {
							break
						}
						continue
					}
					rings = append(rings, c.pts)
				}
				if len(rings) > 0 {
					sc.patches = append(sc.patches, patch{kind: kind, rings: rings})
				}
			}
		}
	}
	for _, name := range []string{"waterway", "transportation"} {
		l := ls[name]
		if l == nil {
			continue
		}
		t := newTileReader(fr, id, l.extent, reach)
		for _, f := range l.features {
			if f.kind != geomLine || !t.near(f) {
				continue
			}
			kind, width, ok := strokeOf(name, f)
			if !ok {
				continue
			}
			paths, err := f.paths()
			if err != nil {
				return err
			}
			for _, p := range paths {
				for _, part := range clipLineRect(p, t.extent) {
					pts := make([]point, len(part))
					for i, q := range part {
						pts[i] = t.local(q)
					}
					sc.strokes = append(sc.strokes, stroke{kind: kind, width: width, pts: pts})
				}
			}
		}
	}
	return nil
}

// prism raises one footprint. Its height is guessed from the footprint as
// the tile carries it, before the cut to the tile's square, so the two
// halves of a building on a tile edge get the same height.
func (t tileReader) prism(f feature, poly [][]tpoint) (*prism, bool) {
	whole := 0.0
	for _, r := range poly {
		whole += tarea(r)
	}
	whole *= math.Pow(t.u*t.fr.scale, 2)
	outer, ok := t.clipped(poly[0])
	if !ok {
		return nil, false
	}
	if area(outer.pts) < 0 {
		outer = outer.reversed()
	}
	p := &prism{outer: outer}
	for _, r := range poly[1:] {
		h, ok := t.clipped(r)
		if !ok {
			continue
		}
		if area(h.pts) > 0 {
			h = h.reversed()
		}
		p.holes = append(p.holes, h)
	}
	p.z0, p.z1, p.guessed = heights(f, whole)
	return p, true
}

// patchOf says whether a ground polygon is drawn, and as what. Protected
// areas and national parks are left out: they can cover a whole town.
func patchOf(layerName string, f feature) (patchKind, bool) {
	class := f.str("class")
	switch layerName {
	case "water":
		return patchWater, f.str("brunnel") != "tunnel"
	case "park":
		return patchGreen, class == "park" || class == "garden"
	case "landcover":
		return patchGreen, class == "grass" || class == "wood" || class == "wetland"
	case "landuse":
		switch class {
		case "cemetery", "pitch", "playground", "garden", "park":
			return patchGreen, true
		}
	case "transportation":
		switch class {
		case "pier", "path", "minor", "service", "pedestrian", "bridge":
			return patchPaved, f.str("brunnel") != "tunnel"
		}
	}
	return 0, false
}

// roadWidths is a road's drawn width in metres by its class.
var roadWidths = map[string]float64{
	"motorway": 11, "trunk": 10, "primary": 9, "secondary": 8, "tertiary": 7,
	"minor": 6, "service": 3.5, "track": 2.5, "path": 1.8, "pier": 3, "raceway": 4, "busway": 6,
}

// waterWidths is a waterway's drawn width in metres by its class.
var waterWidths = map[string]float64{"river": 8, "canal": 6, "stream": 2.5, "drain": 1.5, "ditch": 1.5}

// strokeOf says whether a ground line is drawn, as what and how wide.
// Tunnels, ferries, railways and lifts are left out.
func strokeOf(layerName string, f feature) (strokeKind, float64, bool) {
	if f.str("brunnel") == "tunnel" {
		return 0, 0, false
	}
	class := f.str("class")
	if layerName == "waterway" {
		w, ok := waterWidths[class]
		return strokeWater, w, ok
	}
	w, ok := roadWidths[class]
	if class == "path" && f.str("subclass") == "pedestrian" {
		w = 5
	}
	return strokeRoad, w, ok
}
