package birdseye

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
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

// root is whole, pointing every prism on the way straight at it, so
// joining many parts stays quick however they were joined.
func (p *prism) root() *prism {
	r := p.whole()
	for p.of != nil && p.of != r {
		next := p.of
		p.of = r
		p = next
	}
	return r
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

// lowerTo cuts the prism off at a height (camera.go's tallest), leaving a
// metre of a part that stood wholly above it.
func (p *prism) lowerTo(top float64) {
	if p.z1 <= top {
		return
	}
	p.z1 = top
	p.z0 = max(0, min(p.z0, top-1))
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
	points  int // decoded from the tiles so far, against maxScenePoints
}

// maxScenePoints is the most points a drawing decodes from its tiles. The
// densest drawing in Croatia (central Zagreb at WidestRadius) decodes
// 147,000; a million keep everything a drawing makes of them (the copies
// in metres, the walls, the picture) to some tens of megabytes, where a
// few kilobytes of a hostile tile could otherwise make gigabytes of them.
const maxScenePoints = 1 << 20

// spend counts the points of a feature that is about to be decoded.
func (sc *scene) spend(f feature) error {
	sc.points += f.values / 2
	if sc.points > maxScenePoints {
		return fmt.Errorf("%w: more than %d points", ErrTooComplex, maxScenePoints)
	}
	return nil
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
	if err := joinParts(ctx, sc.prisms, fr); err != nil {
		return sc, found, err
	}
	evenHeights(sc.prisms)
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
//
// Only parts on the same tile edge, whose cuts overlap along it, can
// belong together, so the cut edges are sorted by the tile edge they lie
// on and along it, and only overlapping ones are compared: comparing
// every pair of footprints on any tile edge took 13 seconds for a hostile
// tile of 80,000 of them. It looks at the context as it goes.
func joinParts(ctx context.Context, prisms []*prism, fr frame) error {
	type edge struct {
		northSouth bool  // a tile edge along a meridian: x is constant
		at         int64 // which one, in tiles at Zoom from the world's west or north edge
	}
	type span struct {
		p      *prism
		lo, hi float64 // the extent of its cut along the tile edge, in metres
	}
	tiles := float64(int64(1) << Zoom)
	var edges []edge
	spans := map[edge][]span{}
	cut := 0
	for _, p := range prisms {
		r := p.outer
		if !r.hasCut() {
			continue
		}
		if cut++; cut > maxCutPrisms {
			return fmt.Errorf("%w: more than %d footprints on tile edges", ErrTooComplex, maxCutPrisms)
		}
		var mine []span // one per tile edge the footprint is cut along
		var on []edge
		n := len(r.pts)
		for i, a := range r.pts {
			if !r.cut[i] {
				continue
			}
			b := r.pts[(i+1)%n]
			var e edge
			var lo, hi float64
			switch {
			case math.Abs(a.x-b.x) < cutEps && math.Abs(a.y-b.y) >= cutEps:
				e = edge{true, int64(math.Round((a.x/fr.scale + fr.x0) * tiles))}
				lo, hi = min(a.y, b.y), max(a.y, b.y)
			case math.Abs(a.y-b.y) < cutEps && math.Abs(a.x-b.x) >= cutEps:
				e = edge{false, int64(math.Round((fr.y0 - a.y/fr.scale) * tiles))}
				lo, hi = min(a.x, b.x), max(a.x, b.x)
			default: // no length along a tile edge: it covers nothing
				continue
			}
			if k := slices.Index(on, e); k >= 0 {
				mine[k].lo, mine[k].hi = min(mine[k].lo, lo), max(mine[k].hi, hi)
				continue
			}
			on, mine = append(on, e), append(mine, span{p, lo, hi})
		}
		for k, e := range on {
			if _, seen := spans[e]; !seen {
				edges = append(edges, e)
			}
			spans[e] = append(spans[e], mine[k])
		}
	}
	steps := 0
	for _, e := range edges {
		ss := spans[e]
		slices.SortStableFunc(ss, func(a, b span) int { return cmp.Compare(a.lo, b.lo) })
		for i, a := range ss {
			for _, b := range ss[i+1:] {
				if b.lo >= a.hi { // and so every one after it: they do not overlap
					break
				}
				if steps++; steps%checkEvery == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if ra, rb := a.p.root(), b.p.root(); ra != rb && sameHeights(a.p, b.p) && sharesCut(a.p.outer, b.p.outer) {
					rb.of = ra
				}
			}
		}
	}
	return nil
}

// maxCutPrisms is the most footprints on tile edges a scene joins. The
// densest scene in Croatia has 515.
const maxCutPrisms = 1 << 14

// checkEvery is how many steps of a loop that can run long go between two
// looks at the context.
const checkEvery = 1024

// sameHeights reports whether two prisms can be parts of one footprint:
// they carry its heights. A guessed top may differ, since each tile
// guesses from as much of the footprint as it carries.
func sameHeights(p, q *prism) bool {
	return p.z0 == q.z0 && (p.z1 == q.z1 || p.guessed && q.guessed)
}

// evenHeights gives the parts of one footprint one guessed height. Each
// tile guesses from as much of the footprint as it carries, its own side
// and some metres of the other, so the two sides of a building that runs
// on across a tile edge could come out a storey apart: a step in the roof
// along the edge, where no wall is drawn. The guess is made once more
// from the parts' areas together, which is the footprint's own.
func evenHeights(prisms []*prism) {
	areas := map[*prism]float64{} // by building, of the buildings in parts
	for _, p := range prisms {
		if p.of != nil {
			areas[p.whole()] = 0
		}
	}
	if len(areas) == 0 {
		return
	}
	for _, p := range prisms {
		w := p.whole()
		if _, inParts := areas[w]; inParts {
			areas[w] += footprint(p)
		}
	}
	for _, p := range prisms {
		if a, inParts := areas[p.whole()]; inParts && p.guessed {
			p.z1 = max(guessHeight(a), p.z0+1)
		}
	}
}

// tileReader converts one tile's features to metres and keeps those that
// reach the square around the pin.
type tileReader struct {
	fr        frame
	ox, oy, u float64
	extent    float64
	lo, hi    tpoint // the square around the pin, in tile units
	reach     float64
	pin       tpoint  // the pin, in tile units
	reach2    float64 // the square of reach in tile units, with a margin
	buf       []tpoint
}

func newTileReader(fr frame, id tileID, extent, reach float64) tileReader {
	ox, oy, u := fr.tile(id, extent)
	t := tileReader{fr: fr, ox: ox, oy: oy, u: u, extent: extent, reach: reach}
	a := fr.fromLocal(ox, oy, u, point{-reach, reach})
	b := fr.fromLocal(ox, oy, u, point{reach, -reach})
	t.lo, t.hi = a, b
	t.pin = fr.fromLocal(ox, oy, u, point{})
	r := reach / (u * fr.scale)
	t.reach2 = r * r * (1 + 1e-9)
	return t
}

// near reports whether a feature's box meets the square around the pin.
func (t tileReader) near(f feature) bool {
	lo, hi, ok := f.bounds()
	return ok && hi.x >= t.lo.x && lo.x <= t.hi.x && hi.y >= t.lo.y && lo.y <= t.hi.y
}

// wanted reports whether one polygon or line of a feature near the pin is
// worth converting. A feature can hold many (Planetiler joins a block's
// buildings into one), most of them far from the pin: a polygon whose box
// misses the disc of radius reach around the pin can draw nothing there
// and is left out, unless its box reaches a side of the tile, where it
// may be one part of a footprint the tile edge cut, whose other part the
// neighbouring tile has nearer the pin.
func (t tileReader) wanted(pts []tpoint) bool {
	lo, hi := bbox(pts)
	if lo.x <= sideEps || lo.y <= sideEps || hi.x >= t.extent-sideEps || hi.y >= t.extent-sideEps {
		return true
	}
	nx := max(lo.x, min(t.pin.x, hi.x)) - t.pin.x
	ny := max(lo.y, min(t.pin.y, hi.y)) - t.pin.y
	return nx*nx+ny*ny <= t.reach2
}

func (t tileReader) local(p tpoint) point { return t.fr.toLocal(t.ox, t.oy, t.u, p) }

// eachPolygon groups a feature's rings into polygons, an exterior ring
// (its area positive in tile units) followed by its holes, and hands each
// polygon whose exterior is wanted to yield, as copies of its own. The
// holes of an exterior that is not wanted go with it.
func (t *tileReader) eachPolygon(f feature, yield func(poly [][]tpoint)) error {
	var poly [][]tpoint // nil while the last exterior is not wanted, or before the first
	var err error
	t.buf, err = f.eachPath(t.buf, func(path []tpoint) {
		r := openRing(path)
		if len(r) < 3 {
			return
		}
		a := area(r)
		switch {
		case a > 0:
			if poly != nil {
				yield(poly)
			}
			poly = nil
			if t.wanted(r) {
				poly = [][]tpoint{slices.Clone(r)}
			}
		case a < 0 && poly != nil:
			poly = append(poly, slices.Clone(r))
		}
	})
	if err != nil {
		return err
	}
	if poly != nil {
		yield(poly)
	}
	return nil
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
	var buf []tpoint // the paths' points, decoded into one buffer
	if l := ls["building"]; l != nil {
		t := newTileReader(fr, id, l.extent, reach)
		t.buf = buf
		for _, f := range l.features {
			if f.kind != geomPolygon || f.flag("hide_3d") || !t.near(f) {
				continue
			}
			if err := sc.spend(f); err != nil {
				return err
			}
			err := t.eachPolygon(f, func(poly [][]tpoint) {
				if p, ok := t.prism(f, poly); ok {
					sc.prisms = append(sc.prisms, p)
				}
			})
			if err != nil {
				return err
			}
		}
		buf = t.buf
	}
	for _, name := range []string{"landcover", "landuse", "park", "water", "transportation"} {
		l := ls[name]
		if l == nil {
			continue
		}
		t := newTileReader(fr, id, l.extent, reach)
		t.buf = buf
		for _, f := range l.features {
			if f.kind != geomPolygon || !t.near(f) {
				continue
			}
			kind, ok := patchOf(name, f)
			if !ok {
				continue
			}
			if err := sc.spend(f); err != nil {
				return err
			}
			err := t.eachPolygon(f, func(poly [][]tpoint) {
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
			})
			if err != nil {
				return err
			}
		}
		buf = t.buf
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
			if err := sc.spend(f); err != nil {
				return err
			}
			var err error
			buf, err = f.eachPath(buf, func(p []tpoint) {
				if !t.wanted(p) {
					return
				}
				for _, part := range clipLineRect(p, t.extent) {
					pts := make([]point, len(part))
					for i, q := range part {
						pts[i] = t.local(q)
					}
					sc.strokes = append(sc.strokes, stroke{kind: kind, width: width, pts: pts})
				}
			})
			if err != nil {
				return err
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
		whole += area(r)
	}
	whole *= math.Pow(t.u*t.fr.scale, 2)
	outer, ok := t.clipped(poly[0])
	if !ok {
		return nil, false
	}
	if area(outer.pts) < 0 {
		outer.reverse()
	}
	p := &prism{outer: outer}
	for _, r := range poly[1:] {
		h, ok := t.clipped(r)
		if !ok {
			continue
		}
		if area(h.pts) > 0 {
			h.reverse()
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
