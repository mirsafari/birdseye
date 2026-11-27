package birdseye

import "math"

// ring is a closed outline without its closing point: the edge from the
// last point runs back to the first. cut[i] marks the edge from pts[i] to
// the next point as one a tile edge made, not one the building has: it
// gets no wall and no outline, so a footprint split between two tiles
// draws no seam.
type ring struct {
	pts []point
	cut []bool
}

func (r ring) hasCut() bool {
	for _, c := range r.cut {
		if c {
			return true
		}
	}
	return false
}

// area is the signed area of the points as a ring: positive when they run
// counter-clockwise with y pointing north.
func area(pts []point) float64 {
	var s float64
	n := len(pts)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%n]
		s += a.x*b.y - b.x*a.y
	}
	return s / 2
}

// tarea is area in tile units, where y points down: an exterior ring of
// the tile format is positive (clockwise on screen), a hole negative.
func tarea(pts []tpoint) float64 {
	var s float64
	n := len(pts)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%n]
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

// clipRect clips a ring in tile units to the square [0, size]² (Sutherland
// and Hodgman's algorithm, exact for a convex window) and marks the edges
// that lie along the square's sides as cut. A concave ring cut into
// several pieces comes back as one ring joined by zero-width bridges along
// the side, which are cut edges too, so they draw nothing.
func clipRect(pts []tpoint, size float64) (out []tpoint, cut []bool) {
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
	const eps = 1e-6
	onSide := func(p tpoint, s int) bool {
		switch s {
		case 1:
			return math.Abs(p.x) < eps
		case 2:
			return math.Abs(p.x-size) < eps
		case 3:
			return math.Abs(p.y) < eps
		case 4:
			return math.Abs(p.y-size) < eps
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
	out := pts[:0:0]
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

// clipLineRect cuts a line in tile units to the square [0, size]², which
// may leave several pieces.
func clipLineRect(pts []tpoint, size float64) [][]tpoint {
	var out [][]tpoint
	var cur []tpoint
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		t0, t1, ok := liangBarsky(a, b, size)
		if !ok {
			if len(cur) > 1 {
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		pa := tpoint{a.x + (b.x-a.x)*t0, a.y + (b.y-a.y)*t0}
		pb := tpoint{a.x + (b.x-a.x)*t1, a.y + (b.y-a.y)*t1}
		if len(cur) == 0 || cur[len(cur)-1] != pa {
			if len(cur) > 1 {
				out = append(out, cur)
			}
			cur = []tpoint{pa}
		}
		cur = append(cur, pb)
		if t1 < 1 {
			out = append(out, cur)
			cur = nil
		}
	}
	if len(cur) > 1 {
		out = append(out, cur)
	}
	return out
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
			t0 = math.Max(t0, r)
		} else {
			if r < t0 {
				return 0, 0, false
			}
			t1 = math.Min(t1, r)
		}
	}
	return t0, t1, t0 < t1
}

// clipLineCircle cuts a line in metres to the circle of the given radius
// around the pin.
func clipLineCircle(pts []point, radius float64) [][]point {
	var out [][]point
	var cur []point
	r2 := radius * radius
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := b.x-a.x, b.y-a.y
		A := dx*dx + dy*dy
		B := 2 * (a.x*dx + a.y*dy)
		C := a.x*a.x + a.y*a.y - r2
		t0, t1 := 0.0, 1.0
		if A == 0 {
			continue
		}
		disc := B*B - 4*A*C
		if disc <= 0 {
			if len(cur) > 1 {
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		s := math.Sqrt(disc)
		t0, t1 = math.Max(t0, (-B-s)/(2*A)), math.Min(t1, (-B+s)/(2*A))
		if t0 >= t1 {
			if len(cur) > 1 {
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		pa := point{a.x + dx*t0, a.y + dy*t0}
		pb := point{a.x + dx*t1, a.y + dy*t1}
		if len(cur) == 0 || cur[len(cur)-1] != pa {
			if len(cur) > 1 {
				out = append(out, cur)
			}
			cur = []point{pa}
		}
		cur = append(cur, pb)
		if t1 < 1 {
			out = append(out, cur)
			cur = nil
		}
	}
	if len(cur) > 1 {
		out = append(out, cur)
	}
	return out
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
