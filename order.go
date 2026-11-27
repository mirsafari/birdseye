package birdseye

import (
	"cmp"
	"container/heap"
	"math"
	"slices"
)

// order sorts the prisms back to front for the painter, and returns how
// many cycles it had to break.
//
// Two prisms can only hide one another where their azimuths, seen from
// the camera's foot, overlap. There three rays are cast along the ground
// and the footprint each ray reaches first votes for being in front. The
// pairs make a graph, sorted topologically with the farthest prism first
// among those free to go; a cycle (rare: two wings of one building around
// another) is broken by releasing the farthest prism left.
func order(prisms []*prism, foot point) int {
	n := len(prisms)
	for _, p := range prisms {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, q := range p.outer.pts {
			a := math.Atan2(q.x-foot.x, q.y-foot.y)
			lo, hi = min(lo, a), max(hi, a)
		}
		p.az0, p.az1 = lo, hi
		c := centroid(p.outer.pts)
		p.dist = math.Hypot(c.x-foot.x, c.y-foot.y)
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(prisms[a].az0, prisms[b].az0) })

	after := make([][]int, n) // after[i]: the prisms drawn after i
	indeg := make([]int, n)
	for k, i := range idx {
		pi := prisms[i]
		for _, j := range idx[k+1:] {
			pj := prisms[j]
			if pj.az0 >= pi.az1 {
				break
			}
			lo, hi := max(pi.az0, pj.az0), min(pi.az1, pj.az1)
			if hi-lo < 1e-5 {
				continue
			}
			votes := 0
			for _, t := range [3]float64{0.25, 0.5, 0.75} {
				az := lo + (hi-lo)*t
				ti, oki := rayHit(foot, az, pi.outer.pts)
				tj, okj := rayHit(foot, az, pj.outer.pts)
				if !oki || !okj || math.Abs(ti-tj) < 1e-6 {
					continue
				}
				if ti < tj {
					votes++
				} else {
					votes--
				}
			}
			switch {
			case votes > 0: // i is in front: j goes first
				after[j] = append(after[j], i)
				indeg[i]++
			case votes < 0:
				after[i] = append(after[i], j)
				indeg[j]++
			}
		}
	}

	ready := &farthest{prisms: prisms}
	for i := range prisms {
		if indeg[i] == 0 {
			heap.Push(ready, i)
		}
	}
	done := make([]bool, n)
	out := make([]*prism, 0, n)
	cycles := 0
	for len(out) < n {
		if ready.Len() == 0 {
			best := -1
			for i := range prisms {
				if !done[i] && (best < 0 || prisms[i].dist > prisms[best].dist) {
					best = i
				}
			}
			indeg[best] = 0
			heap.Push(ready, best)
			cycles++
		}
		i := heap.Pop(ready).(int)
		if done[i] {
			continue
		}
		done[i] = true
		out = append(out, prisms[i])
		for _, j := range after[i] {
			indeg[j]--
			if indeg[j] == 0 && !done[j] {
				heap.Push(ready, j)
			}
		}
	}
	copy(prisms, out)
	return cycles
}

// rayHit is the ground distance from the foot at which a ray of the given
// azimuth (clockwise from north) first crosses the ring.
func rayHit(foot point, az float64, pts []point) (float64, bool) {
	dx, dy := math.Sin(az), math.Cos(az)
	best, ok := 0.0, false
	n := len(pts)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%n]
		x1, y1 := a.x-foot.x, a.y-foot.y
		ex, ey := b.x-a.x, b.y-a.y
		den := dx*ey - dy*ex
		if math.Abs(den) < 1e-12 {
			continue
		}
		t := (x1*ey - y1*ex) / den
		u := (x1*dy - y1*dx) / den
		if t > 0 && u >= 0 && u <= 1 && (!ok || t < best) {
			best, ok = t, true
		}
	}
	return best, ok
}

// farthest is a heap of prism indexes, the farthest from the camera on
// top; ties go to the lower index, so the order is deterministic.
type farthest struct {
	prisms []*prism
	items  []int
}

func (h *farthest) Len() int { return len(h.items) }
func (h *farthest) Less(a, b int) bool {
	pa, pb := h.prisms[h.items[a]], h.prisms[h.items[b]]
	if pa.dist != pb.dist {
		return pa.dist > pb.dist
	}
	return h.items[a] < h.items[b]
}
func (h *farthest) Swap(a, b int) { h.items[a], h.items[b] = h.items[b], h.items[a] }
func (h *farthest) Push(x any)    { h.items = append(h.items, x.(int)) }
func (h *farthest) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}
