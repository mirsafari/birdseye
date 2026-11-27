package birdseye

import (
	"math"
	"slices"
)

// ring is a closed outline without its closing point: the edge from the
// last point runs back to the first. cut[i] marks the edge from pts[i] to
// the next point as one a tile edge made, not one the building has: it
// gets no wall and no outline, so a footprint split between two tiles
// draws no seam.
type ring struct {
	pts []point
	cut []bool
}

func (r ring) hasCut() bool { return slices.Contains(r.cut, true) }

// area is the signed area of the points as a ring: positive when they run
// counter-clockwise with y pointing north. In tile units, where y points
// down, it is positive for an exterior ring of the tile format (clockwise
// on screen) and negative for a hole.
func area[P point | tpoint](pts []P) float64 {
	var s float64
	n := len(pts)
	for i := range pts {
		a, b := point(pts[i]), point(pts[(i+1)%n])
		s += a.x*b.y - b.x*a.y
	}
	return s / 2
}

// reversed returns the ring running the other way, cut flags following
// their edges.
func (r ring) reversed() ring {
	n := len(r.pts)
	out := ring{pts: make([]point, n), cut: make([]bool, n)}
	for i := range r.pts {
		out.pts[i] = r.pts[n-1-i]
	}
	// The edge from out.pts[i] to out.pts[i+1] is the original edge from
	// pts[n-2-i] to pts[n-1-i].
	for i := range out.cut {
		out.cut[i] = r.cut[((n-2-i)%n+n)%n]
	}
	return out
}

// northOf cuts the ring to the ground at or north of the line y (a
// half-plane, so Sutherland and Hodgman's algorithm is exact), marking the
// edges the cut makes as cut edges: no wall, no outline. ok is false when
// nothing is left. A ring wholly north of the line comes back as it is.
func (r ring) northOf(y float64) (out ring, ok bool) {
	south := false
	for _, p := range r.pts {
		if p.y < y {
			south = true
			break
		}
	}
	if !south {
		return r, true
	}
	n := len(r.pts)
	for i, a := range r.pts {
		b := r.pts[(i+1)%n]
		cross := func() point { return point{a.x + (b.x-a.x)*(y-a.y)/(b.y-a.y), y} }
		switch ia, ib := a.y >= y, b.y >= y; {
		case ia && ib:
			out.pts, out.cut = append(out.pts, a), append(out.cut, r.cut[i])
		case ia && !ib: // leaving: the next edge runs along the line
			out.pts, out.cut = append(out.pts, a, cross()), append(out.cut, r.cut[i], true)
		case !ia && ib: // coming back: the rest of this edge
			out.pts, out.cut = append(out.pts, cross()), append(out.cut, r.cut[i])
		}
	}
	return out, len(out.pts) >= 3
}

// sharesCut reports whether the two rings are parts of one footprint cut
// by a tile edge: a cut edge of each lies on the same line (a tile edge
// runs east or north) and one covers at least half of the other. Parts of
// one footprint cover each other's cut but for a tile unit of rounding;
// neighbours whose shared wall ends on the tile edge overlap by that
// rounding at most.
func sharesCut(a, b ring) bool {
	const eps = 1e-3 // metres; the two tiles place their shared edge a few nanometres apart
	span := func(a0, a1, b0, b1 float64) (over, shorter float64) {
		a0, a1 = min(a0, a1), max(a0, a1)
		b0, b1 = min(b0, b1), max(b0, b1)
		return min(a1, b1) - max(a0, b0), min(a1-a0, b1-b0)
	}
	covers := func(over, shorter float64) bool { return shorter > eps && over >= shorter/2 }
	na, nb := len(a.pts), len(b.pts)
	for i, p := range a.pts {
		if !a.cut[i] {
			continue
		}
		q := a.pts[(i+1)%na]
		for j, r := range b.pts {
			if !b.cut[j] {
				continue
			}
			s := b.pts[(j+1)%nb]
			switch {
			case math.Abs(p.x-q.x) < eps && math.Abs(r.x-s.x) < eps && math.Abs(p.x-r.x) < eps: // both along one north-south edge
				if covers(span(p.y, q.y, r.y, s.y)) {
					return true
				}
			case math.Abs(p.y-q.y) < eps && math.Abs(r.y-s.y) < eps && math.Abs(p.y-r.y) < eps: // both along one east-west edge
				if covers(span(p.x, q.x, r.x, s.x)) {
					return true
				}
			}
		}
	}
	return false
}

// contains reports whether p lies inside the ring (even-odd).
func contains(pts []point, p point) bool {
	in := false
	n := len(pts)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%n]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			in = !in
		}
	}
	return in
}

// centroid is the mean of the points, which is enough to tell whether a
// footprint stands inside the disc.
func centroid(pts []point) point {
	var c point
	for _, p := range pts {
		c.x += p.x
		c.y += p.y
	}
	n := float64(len(pts))
	return point{c.x / n, c.y / n}
}

// openRing drops a closed path's repeated last point.
func openRing(pts []tpoint) []tpoint {
	if n := len(pts); n > 1 && pts[0] == pts[n-1] {
		return pts[:n-1]
	}
	return pts
}

// sideEps is how near a side of the tile's square a point counts as on
// it, in tile units.
const sideEps = 1e-6

// clipRect clips a ring in tile units to the square [0, size]² and marks
// the edges that lie along the square's sides as cut. Most rings stand
// wholly inside their tile: those are not clipped at all, which is where
// a drawing spent a third of what it allocated.
func clipRect(pts []tpoint, size float64) (out []tpoint, cut []bool) {
	if !insideSquare(pts, size) {
		return clipSides(pts, size)
	}
	if out = dedupe(pts); len(out) < 3 {
		return nil, nil
	}
	return out, make([]bool, len(out))
}

// insideSquare reports whether every point is inside the square and off
// its sides: the ring then has nothing to clip and no edge along a side.
func insideSquare(pts []tpoint, size float64) bool {
	for _, p := range pts {
		if !(p.x > sideEps && p.x < size-sideEps && p.y > sideEps && p.y < size-sideEps) {
			return false
		}
	}
	return true
}

// clipSides is clipRect for a ring that reaches a side of the square or
// crosses one (Sutherland and Hodgman's algorithm, exact for a convex
// window). A concave ring cut into several pieces comes back as one ring
// joined by zero-width bridges along the side, which are cut edges too,
// so they draw nothing.
func clipSides(pts []tpoint, size float64) (out []tpoint, cut []bool) {
	type plane struct {
		in    func(tpoint) bool
		cross func(a, b tpoint) tpoint
	}
	at := func(a, b tpoint, t float64) tpoint { return tpoint{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t} }
	planes := []plane{
		{func(p tpoint) bool { return p.x >= 0 }, func(a, b tpoint) tpoint { p := at(a, b, a.x/(a.x-b.x)); p.x = 0; return p }},
		{func(p tpoint) bool { return p.x <= size }, func(a, b tpoint) tpoint { p := at(a, b, (a.x-size)/(a.x-b.x)); p.x = size; return p }},
		{func(p tpoint) bool { return p.y >= 0 }, func(a, b tpoint) tpoint { p := at(a, b, a.y/(a.y-b.y)); p.y = 0; return p }},
		{func(p tpoint) bool { return p.y <= size }, func(a, b tpoint) tpoint { p := at(a, b, (a.y-size)/(a.y-b.y)); p.y = size; return p }},
	}
	out = pts
	for _, pl := range planes {
		if len(out) == 0 {
			return nil, nil
		}
		in := out
		out = make([]tpoint, 0, len(in)+4)
		n := len(in)
		for i := range in {
			a, b := in[(i+n-1)%n], in[i]
			ia, ib := pl.in(a), pl.in(b)
			switch {
			case ia && ib:
				out = append(out, b)
			case ia && !ib:
				out = append(out, pl.cross(a, b))
			case !ia && ib:
				out = append(out, pl.cross(a, b), b)
			}
		}
	}
	out = dedupe(out)
	if len(out) < 3 {
		return nil, nil
	}
	onSide := func(p tpoint, s int) bool {
		switch s {
		case 1:
			return math.Abs(p.x) < sideEps
		case 2:
			return math.Abs(p.x-size) < sideEps
		case 3:
			return math.Abs(p.y) < sideEps
		case 4:
			return math.Abs(p.y-size) < sideEps
		}
		return false
	}
	cut = make([]bool, len(out))
	n := len(out)
	for i := range out {
		a, b := out[i], out[(i+1)%n]
		for s := 1; s <= 4; s++ {
			if onSide(a, s) && onSide(b, s) {
				cut[i] = true
				break
			}
		}
	}
	return out, cut
}

// dedupe drops repeated consecutive points, the closing one included.
func dedupe[P point | tpoint](pts []P) []P {
	out := make([]P, 0, len(pts))
	for i, p := range pts {
		if i > 0 && p == out[len(out)-1] {
			continue
		}
		out = append(out, p)
	}
	for len(out) > 1 && out[0] == out[len(out)-1] {
		out = out[:len(out)-1]
	}
	return out
}

// clipConvex clips a ring to a convex window whose points run counter-
// clockwise (Sutherland and Hodgman).
func clipConvex(pts []point, window []point) []point {
	out := pts
	m := len(window)
	for k := range window {
		if len(out) == 0 {
			return nil
		}
		e0, e1 := window[k], window[(k+1)%m]
		inside := func(p point) bool { return (e1.x-e0.x)*(p.y-e0.y)-(e1.y-e0.y)*(p.x-e0.x) >= 0 }
		cross := func(a, b point) point {
			da := (e1.x-e0.x)*(a.y-e0.y) - (e1.y-e0.y)*(a.x-e0.x)
			db := (e1.x-e0.x)*(b.y-e0.y) - (e1.y-e0.y)*(b.x-e0.x)
			t := da / (da - db)
			return point{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t}
		}
		in := out
		out = make([]point, 0, len(in)+4)
		n := len(in)
		for i := range in {
			a, b := in[(i+n-1)%n], in[i]
			ia, ib := inside(a), inside(b)
			switch {
			case ia && ib:
				out = append(out, b)
			case ia && !ib:
				out = append(out, cross(a, b))
			case !ia && ib:
				out = append(out, cross(a, b), b)
			}
		}
	}
	out = dedupe(out)
	if len(out) < 3 {
		return nil
	}
	return out
}

// pieces collects what clipping leaves of a line: the parts of its
// segments that survive, joined into a piece for as long as each starts
// where the one before it ended.
type pieces[P point | tpoint] struct {
	out [][]P
	cur []P
}

// add takes the part from a to b of the line's next segment. cutShort
// says the part ends before the segment does: the piece ends with it.
func (c *pieces[P]) add(a, b P, cutShort bool) {
	if len(c.cur) == 0 || c.cur[len(c.cur)-1] != a {
		c.gap()
		c.cur = []P{a}
	}
	c.cur = append(c.cur, b)
	if cutShort {
		c.gap()
	}
}

// gap ends the piece being collected, for a segment of which nothing
// survives; a piece is at least one part.
func (c *pieces[P]) gap() {
	if len(c.cur) > 1 {
		c.out = append(c.out, c.cur)
	}
	c.cur = nil
}

// done is the pieces collected.
func (c *pieces[P]) done() [][]P {
	c.gap()
	return c.out
}

// clipLineRect cuts a line in tile units to the square [0, size]², which
// may leave several pieces.
func clipLineRect(pts []tpoint, size float64) [][]tpoint {
	var kept pieces[tpoint]
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		t0, t1, ok := liangBarsky(a, b, size)
		if !ok {
			kept.gap()
			continue
		}
		pa := tpoint{a.x + (b.x-a.x)*t0, a.y + (b.y-a.y)*t0}
		pb := tpoint{a.x + (b.x-a.x)*t1, a.y + (b.y-a.y)*t1}
		kept.add(pa, pb, t1 < 1)
	}
	return kept.done()
}

func liangBarsky(a, b tpoint, size float64) (t0, t1 float64, ok bool) {
	t0, t1 = 0, 1
	dx, dy := b.x-a.x, b.y-a.y
	for _, c := range [4][2]float64{{-dx, a.x}, {dx, size - a.x}, {-dy, a.y}, {dy, size - a.y}} {
		p, q := c[0], c[1]
		if p == 0 {
			if q < 0 {
				return 0, 0, false
			}
			continue
		}
		r := q / p
		if p < 0 {
			if r > t1 {
				return 0, 0, false
			}
			t0 = max(t0, r)
		} else {
			if r < t0 {
				return 0, 0, false
			}
			t1 = min(t1, r)
		}
	}
	return t0, t1, t0 < t1
}

// clipLineCircle cuts a line in metres to the circle of the given radius
// around the pin.
func clipLineCircle(pts []point, radius float64) [][]point {
	var kept pieces[point]
	r2 := radius * radius
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := b.x-a.x, b.y-a.y
		A := dx*dx + dy*dy
		B := 2 * (a.x*dx + a.y*dy)
		C := a.x*a.x + a.y*a.y - r2
		if A == 0 {
			continue
		}
		disc := B*B - 4*A*C
		if disc <= 0 {
			kept.gap()
			continue
		}
		s := math.Sqrt(disc)
		t0, t1 := max(0, (-B-s)/(2*A)), min(1, (-B+s)/(2*A))
		if t0 >= t1 {
			kept.gap()
			continue
		}
		pa := point{a.x + dx*t0, a.y + dy*t0}
		pb := point{a.x + dx*t1, a.y + dy*t1}
		kept.add(pa, pb, t1 < 1)
	}
	return kept.done()
}

// discPolygon is the drawn disc as a counter-clockwise polygon.
func discPolygon(radius float64, n int) []point {
	out := make([]point, n)
	for k := range out {
		a := 2 * math.Pi * float64(k) / float64(n)
		out[k] = point{radius * math.Cos(a), radius * math.Sin(a)}
	}
	return out
}
