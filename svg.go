package birdseye

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"strconv"
)

// The picture takes its colours from the page: every fill and stroke is a
// CSS variable (Stylesheet has defaults; a themed page derives its own
// from its tokens), so the one file follows light and dark themes. The
// page shows it through <svg><use href="…#s"/></svg>, where the page's
// variables reach the referenced tree; presentation attributes are the
// spelling every engine reads there.
const (
	colGround     = "var(--birdseye-ground)"
	colGreen      = "var(--birdseye-green)"
	colWater      = "var(--birdseye-water)"
	colRoad       = "var(--birdseye-road)"
	colRoof       = "var(--birdseye-roof)"
	colEdge       = "var(--birdseye-edge)"
	colPinnedRoof = "var(--birdseye-pinned-roof)"
	colPinnedEdge = "var(--birdseye-pinned-edge)"
	edgeWidth     = "1.1"
	symbolID      = "s"
	discSegments  = 96
)

var (
	colWalls       = [3]string{"var(--birdseye-wall-1)", "var(--birdseye-wall-2)", "var(--birdseye-wall-3)"}
	colPinnedWalls = [3]string{"var(--birdseye-pinned-wall-1)", "var(--birdseye-pinned-wall-2)", "var(--birdseye-pinned-wall-3)"}
)

// light is where the light comes from across the ground (south-west), for
// the three wall tones.
var light = point{-0.55, -0.83}

// svgWriter writes the picture. The output is deterministic: the same
// scene gives the same bytes (no map iteration, no generated ids), so a
// picture's hash is its version.
type svgWriter struct {
	buf bytes.Buffer
	cam camera
	num []byte

	// Reused from prism to prism.
	rings []ring
	walls []wall
	tops  []point // the rings' roof points, projected, one ring after another
}

func (w *svgWriter) f(v float64) {
	w.num = strconv.AppendFloat(w.num[:0], math.Round(v*10)/10, 'f', -1, 64)
	if len(w.num) == 2 && w.num[0] == '-' && w.num[1] == '0' {
		w.num = w.num[1:]
	}
	w.buf.Write(w.num)
}

// ring writes one closed subpath of frame points.
func (w *svgWriter) ring(pts []point) {
	w.line(pts)
	w.buf.WriteByte('Z')
}

// line writes one open subpath.
func (w *svgWriter) line(pts []point) {
	for i, p := range pts {
		if i == 0 {
			w.buf.WriteByte('M')
		} else {
			w.buf.WriteByte('L')
		}
		w.f(p.x)
		w.buf.WriteByte(',')
		w.f(p.y)
	}
}

func (w *svgWriter) open(attrs string) {
	w.buf.WriteString(`<path `)
	w.buf.WriteString(attrs)
	w.buf.WriteString(` d="`)
}

func (w *svgWriter) close() { w.buf.WriteString(`"/>`) }

// ground writes points on the ground as one subpath, closed into a ring
// or open as a line, projecting them as it goes.
func (w *svgWriter) ground(pts []point, closed bool) {
	for i, p := range pts {
		q := w.cam.project(p.x, p.y, 0)
		if i == 0 {
			w.buf.WriteByte('M')
		} else {
			w.buf.WriteByte('L')
		}
		w.f(q.x)
		w.buf.WriteByte(',')
		w.f(q.y)
	}
	if closed {
		w.buf.WriteByte('Z')
	}
}

// pointBytes is a little over what one point takes in a path ("L812.3,455.1"),
// and the size of a picture is about that times its points.
const pointBytes = 13

// sizeHint is about how many bytes the drawing writes: every ground point
// once, every footprint point for its roof and about twice for the walls
// that face the camera (four points each, half the edges), and some for
// each element. The buffer then grows once instead of doubling its way
// up to a picture of hundreds of kilobytes.
func (d *drawing) sizeHint() int {
	n := len(d.disc.polygon)
	for _, p := range d.patches {
		for _, r := range p.rings {
			n += len(r)
		}
	}
	for _, s := range d.strokes {
		n += len(s.pts)
	}
	elements := 0
	for _, p := range d.prisms {
		n += 3 * len(p.outer.pts)
		for _, h := range p.holes {
			n += 3 * len(h.pts)
		}
		elements += 4
	}
	return 1024 + n*pointBytes + elements*64
}

// drawing is what one picture draws, cut to its disc.
type drawing struct {
	disc    disc
	prisms  []*prism // back to front
	patches []patch
	strokes []stroke
	cycles  int
	faces   int
}

// writeSVG writes the drawing seen by the camera.
func writeSVG(d *drawing, cam camera) []byte {
	w := &svgWriter{cam: cam}
	w.buf.Grow(d.sizeHint())
	w.buf.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	w.buf.WriteString(strconv.Itoa(Width) + " " + strconv.Itoa(Height))
	w.buf.WriteString(`"><g id="` + symbolID + `" stroke-linejoin="round" stroke-width="` + edgeWidth + `">`)

	w.open(`fill="` + colGround + `"`)
	w.ground(d.disc.polygon, true)
	w.close()

	for _, kind := range []patchKind{patchGreen, patchWater, patchPaved} {
		started := false
		for _, p := range d.patches {
			if p.kind != kind {
				continue
			}
			if !started {
				w.open(`fill="` + [...]string{colGreen, colWater, colRoad}[kind] + `"`)
				started = true
			}
			for _, r := range p.rings {
				w.ground(r, true)
			}
		}
		if started {
			w.close()
		}
	}

	type group struct {
		kind  strokeKind
		width float64
	}
	var groups []group
	seen := map[group]bool{}
	for _, s := range d.strokes {
		g := group{s.kind, s.width}
		if !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	slices.SortFunc(groups, func(a, b group) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.width, b.width))
	})
	for _, g := range groups {
		col := colRoad
		if g.kind == strokeWater {
			col = colWater
		}
		w.buf.WriteString(`<path fill="none" stroke="` + col + `" stroke-linecap="round" stroke-width="`)
		w.f(g.width * cam.scale())
		w.buf.WriteString(`" d="`)
		for _, s := range d.strokes {
			if s.kind == g.kind && s.width == g.width {
				w.ground(s.pts, false)
			}
		}
		w.close()
	}

	foot := cam.foot()
	for _, p := range d.prisms {
		d.faces += w.prism(p, foot)
	}
	w.buf.WriteString(`</g></svg>`)
	return w.buf.Bytes()
}

// wall is one camera-facing wall of a prism.
type wall struct {
	dist float64
	tone int
	quad [4]point
}

// prism draws one prism: its camera-facing walls far to near in three
// tones by their angle to the light, then the roof with its courtyards
// cut out. It returns the faces drawn.
func (w *svgWriter) prism(p *prism, foot point) int {
	w.rings = append(append(w.rings[:0], p.outer), p.holes...)
	walls := w.walls[:0]
	for _, r := range w.rings {
		n := len(r.pts)
		for i := range r.pts {
			if r.cut[i] {
				continue
			}
			a, b := r.pts[i], r.pts[(i+1)%n]
			nx, ny := b.y-a.y, -(b.x - a.x) // outward for a counter-clockwise outer, inward for a clockwise hole
			mx, my := (a.x+b.x)/2, (a.y+b.y)/2
			if nx*(foot.x-mx)+ny*(foot.y-my) <= 0 {
				continue
			}
			lit := (nx*light.x + ny*light.y) / math.Hypot(nx, ny)
			tone := 2
			switch {
			case lit > 0.35:
				tone = 0
			case lit > -0.35:
				tone = 1
			}
			walls = append(walls, wall{
				dist: math.Hypot(mx-foot.x, my-foot.y),
				tone: tone,
				quad: [4]point{
					w.cam.project(a.x, a.y, p.z0), w.cam.project(b.x, b.y, p.z0),
					w.cam.project(b.x, b.y, p.z1), w.cam.project(a.x, a.y, p.z1),
				},
			})
		}
	}
	w.walls = walls
	slices.SortStableFunc(walls, func(a, b wall) int { return cmp.Compare(b.dist, a.dist) })

	fills, roof, edge := colWalls, colRoof, colEdge
	if p.pinned {
		fills, roof, edge = colPinnedWalls, colPinnedRoof, colPinnedEdge
	}
	// Consecutive walls of one tone are one element.
	for i := 0; i < len(walls); {
		j := i
		w.open(`fill="` + fills[walls[i].tone] + `" stroke="` + edge + `"`)
		for ; j < len(walls) && walls[j].tone == walls[i].tone; j++ {
			w.ring(walls[j].quad[:])
		}
		w.close()
		i = j
	}

	// The roof: every ring projected once, one after another in tops.
	w.tops = w.tops[:0]
	cut := false
	for _, r := range w.rings {
		for _, q := range r.pts {
			w.tops = append(w.tops, w.cam.project(q.x, q.y, p.z1))
		}
		cut = cut || r.hasCut()
	}
	top := func(each func(r ring, pts []point)) {
		at := 0
		for _, r := range w.rings {
			each(r, w.tops[at:at+len(r.pts)])
			at += len(r.pts)
		}
	}
	if !cut {
		w.open(`fill="` + roof + `" stroke="` + edge + `"`)
		top(func(_ ring, pts []point) { w.ring(pts) })
		w.close()
		return len(walls) + 1
	}
	// A footprint cut at a tile edge: the roof is filled whole and outlined
	// only along the building's own edges.
	w.open(`fill="` + roof + `"`)
	top(func(_ ring, pts []point) { w.ring(pts) })
	w.close()
	w.open(`fill="none" stroke="` + edge + `"`)
	top(func(r ring, pts []point) {
		n := len(pts)
		for i := range pts {
			if !r.cut[i] {
				w.line([]point{pts[i], pts[(i+1)%n]})
			}
		}
	})
	w.close()
	return len(walls) + 1
}
