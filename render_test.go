package birdseye

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fixtures and places are birdseyetest's, read from its directory: these
// tests see the unexported code, so they cannot import birdseyetest, which
// imports this package.
var (
	fixtures = DirTiles("birdseyetest/fixtures/tiles")
	places   = func() []struct {
		Name string
		Pin  Pin
	} {
		data, err := os.ReadFile("birdseyetest/fixtures/places.json")
		if err != nil {
			panic(err)
		}
		var raw []struct {
			Name     string
			Lat, Lng float64
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			panic(err)
		}
		out := make([]struct {
			Name string
			Pin  Pin
		}, len(raw))
		for i, r := range raw {
			out[i].Name, out[i].Pin = r.Name, Pin{r.Lat, r.Lng}
		}
		return out
	}()
)

// pb builds protocol buffer bytes for hand-made tiles.
type pb []byte

func (b pb) varint(v uint64) pb {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
func (b pb) key(num, wire int) pb       { return b.varint(uint64(num<<3 | wire)) }
func (b pb) bytes(num int, v []byte) pb { return append(b.key(num, 2).varint(uint64(len(v))), v...) }
func (b pb) uint(num int, v uint64) pb  { return b.key(num, 0).varint(v) }
func (b pb) packed(num int, vs ...uint32) pb {
	var p pb
	for _, v := range vs {
		p = p.varint(uint64(v))
	}
	return b.bytes(num, p)
}
func zz(v int32) uint32 { return uint32((v << 1) ^ (v >> 31)) }

// square is a polygon feature's geometry: one clockwise (on screen) ring.
func square(x, y, size int32) []uint32 {
	return []uint32{
		1 | 1<<3, zz(x), zz(y),
		2 | 3<<3, zz(size), 0, 0, zz(size), zz(-size), 0,
		7 | 1<<3,
	}
}

func TestDecodeTileReadsLayersPropertiesAndRings(t *testing.T) {
	feature := pb{}.packed(2, 0, 0, 1, 1).uint(3, 3).packed(4, square(100, 200, 50)...)
	layerBytes := pb{}.uint(15, 2).bytes(1, []byte("building")).bytes(2, feature).
		bytes(3, []byte("render_height")).bytes(3, []byte("hide_3d")).
		bytes(4, pb{}.uint(4, 12)).bytes(4, pb{}.uint(7, 1)).uint(5, 4096)
	other := pb{}.bytes(1, []byte("poi")).uint(5, 4096)
	tile := pb{}.bytes(3, layerBytes).bytes(3, other)

	ls, err := decodeTile(tile, layers)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ls["poi"]; ok {
		t.Fatal("a layer the picture does not read was decoded")
	}
	l := ls["building"]
	if l == nil || l.extent != 4096 || len(l.features) != 1 {
		t.Fatalf("building layer = %+v", l)
	}
	f := l.features[0]
	if f.kind != geomPolygon || f.num("render_height") != 12 || !f.flag("hide_3d") {
		t.Fatalf("feature = %+v", f)
	}
	paths, err := f.paths()
	if err != nil {
		t.Fatal(err)
	}
	want := []tpoint{{100, 200}, {150, 200}, {150, 250}, {100, 250}, {100, 200}}
	if len(paths) != 1 || len(paths[0]) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i, p := range want {
		if paths[0][i] != p {
			t.Fatalf("paths = %v, want %v", paths[0], want)
		}
	}
	if tarea(openRing(paths[0])) <= 0 {
		t.Fatal("an exterior ring must have a positive area in tile units")
	}
	lo, hi, ok := f.bounds()
	if !ok || lo != (tpoint{100, 200}) || hi != (tpoint{150, 250}) {
		t.Fatalf("bounds = %v %v %v", lo, hi, ok)
	}
}

func TestDecodeTileRefusesTruncatedBytes(t *testing.T) {
	tile := pb{}.bytes(3, pb{}.bytes(1, []byte("building")))
	if _, err := decodeTile(tile[:len(tile)-3], layers); err == nil {
		t.Fatal("a truncated tile decoded")
	}
}

func TestDecodeFixtureTile(t *testing.T) {
	id := tilesAround(newFrame(places[2].Pin), 1)[0] // Varaždin's centre
	data, err := fixtures.Tile(Zoom, id.x, id.y)
	if err != nil || data == nil {
		t.Fatalf("fixture %v: %v", id, err)
	}
	ls, err := decodeTile(data, layers)
	if err != nil {
		t.Fatal(err)
	}
	b := ls["building"]
	if b == nil || b.extent != 4096 || len(b.features) == 0 {
		t.Fatalf("no buildings in Varaždin's tile: %+v", b)
	}
	if ls["transportation"] == nil {
		t.Fatal("no streets in Varaždin's tile")
	}
}

func TestMissingTileIsNil(t *testing.T) {
	data, err := fixtures.Tile(Zoom, 0, 0)
	if data != nil || err != nil {
		t.Fatalf("missing tile = %v, %v", data, err)
	}
}

func TestTileUnitsRoundTripToMetres(t *testing.T) {
	pin := Pin{45.0827, 13.6312}
	fr := newFrame(pin)
	ids := tilesAround(fr, 400)
	if len(ids) == 0 || len(ids) > 4 {
		t.Fatalf("tiles around Rovinj = %v", ids)
	}
	for _, id := range ids {
		ox, oy, u := fr.tile(id, 4096)
		for _, p := range []point{{0, 0}, {123.4, -56.7}, {-400, 400}} {
			back := fr.toLocal(ox, oy, u, fr.fromLocal(ox, oy, u, p))
			if math.Hypot(back.x-p.x, back.y-p.y) > 1e-6 {
				t.Fatalf("round trip of %v = %v", p, back)
			}
		}
	}
	// The pin is in exactly one of the tiles, at its local origin.
	in := 0
	for _, id := range ids {
		ox, oy, u := fr.tile(id, 4096)
		q := fr.fromLocal(ox, oy, u, point{})
		if q.x >= 0 && q.x < 4096 && q.y >= 0 && q.y < 4096 {
			in++
		}
	}
	if in != 1 {
		t.Fatalf("the pin is in %d tiles", in)
	}
	// One unit of a zoom-14 tile is about 0.42 m of ground at 45°.
	ox, oy, u := fr.tile(ids[0], 4096)
	a, b := fr.toLocal(ox, oy, u, tpoint{0, 0}), fr.toLocal(ox, oy, u, tpoint{1, 0})
	if d := b.x - a.x; d < 0.40 || d > 0.44 {
		t.Fatalf("a tile unit is %.3f m", d)
	}
}

func TestHeights(t *testing.T) {
	f := func(props map[string]any) feature { return feature{props: props} }
	cases := []struct {
		props     map[string]any
		footprint float64
		z0, z1    float64
		guessed   bool
	}{
		{map[string]any{"render_height": int64(24)}, 300, 0, 24, false},
		{map[string]any{"render_height": int64(5)}, 30, 0, 3.5, true},
		{map[string]any{"render_height": int64(5)}, 100, 0, 7, true},
		{map[string]any{"render_height": int64(5)}, 500, 0, 9.5, true},
		{map[string]any{}, 2000, 0, 11, true},
		{map[string]any{"render_height": int64(12), "render_min_height": int64(12)}, 50, 12, 13, false},
	}
	for _, c := range cases {
		z0, z1, g := heights(f(c.props), c.footprint)
		if z0 != c.z0 || z1 != c.z1 || g != c.guessed {
			t.Errorf("heights(%v, %v) = %v %v %v, want %v %v %v", c.props, c.footprint, z0, z1, g, c.z0, c.z1, c.guessed)
		}
	}
}

// box is a hand-made prism with its south-west corner at (x, y).
func box(name string, x, y, w, d, h float64) *prism {
	pts := []point{{x, y}, {x + w, y}, {x + w, y + d}, {x, y + d}}
	return &prism{outer: ring{pts: pts, cut: make([]bool, 4)}, z1: h, guessed: name != ""}
}

func indexOf(ps []*prism, p *prism) int {
	for i, q := range ps {
		if q == p {
			return i
		}
	}
	return -1
}

func TestOrderDrawsFarBeforeNear(t *testing.T) {
	foot := point{0, -520}
	near, mid, far := box("near", -5, 0, 10, 10, 20), box("mid", -5, 30, 10, 10, 20), box("far", -5, 60, 10, 10, 20)
	ps := []*prism{near, far, mid}
	if cycles := order(ps, foot); cycles != 0 {
		t.Fatalf("cycles = %d", cycles)
	}
	if !(indexOf(ps, far) < indexOf(ps, mid) && indexOf(ps, mid) < indexOf(ps, near)) {
		t.Fatalf("order = far %d, mid %d, near %d", indexOf(ps, far), indexOf(ps, mid), indexOf(ps, near))
	}
}

func TestOrderPutsABoxInsideAnLInFrontOfItsBackWing(t *testing.T) {
	foot := point{0, -520}
	// An L whose long wing runs behind (north of) a tall box and whose
	// short wing stands west of it, and a low box in front of both.
	l := &prism{outer: ring{pts: []point{{-40, 0}, {-30, 0}, {-30, 40}, {40, 40}, {40, 60}, {-40, 60}}, cut: make([]bool, 6)}, z1: 15}
	inner := box("inner", -10, 10, 15, 15, 30)
	front := box("front", -10, -30, 15, 10, 5)
	ps := []*prism{front, inner, l}
	if cycles := order(ps, foot); cycles != 0 {
		t.Fatalf("cycles = %d", cycles)
	}
	if indexOf(ps, l) > indexOf(ps, inner) || indexOf(ps, inner) > indexOf(ps, front) {
		t.Fatalf("back wing %d, inner %d, front %d", indexOf(ps, l), indexOf(ps, inner), indexOf(ps, front))
	}
}

func TestOrderDrawsAPavilionInACourtyardAfterTheFarWing(t *testing.T) {
	foot := point{0, -520}
	// A ring-shaped block, its courtyard a hole, with a pavilion in the
	// courtyard: the pavilion stands in front of the far wing.
	block := &prism{
		outer: ring{pts: []point{{-50, 0}, {50, 0}, {50, 100}, {-50, 100}}, cut: make([]bool, 4)},
		holes: []ring{{pts: []point{{-30, 20}, {-30, 80}, {30, 80}, {30, 20}}, cut: make([]bool, 4)}},
		z1:    12,
	}
	far := box("far", -20, 140, 40, 20, 12)
	pavilion := box("pavilion", -5, 40, 10, 10, 6)
	ps := []*prism{pavilion, block, far}
	order(ps, foot)
	if indexOf(ps, far) > indexOf(ps, block) {
		t.Fatalf("far %d drawn after block %d", indexOf(ps, far), indexOf(ps, block))
	}
}

func TestClipRectMarksTheTileEdgeAsCut(t *testing.T) {
	// A square straddling the tile's east edge.
	pts, cut := clipRect([]tpoint{{4000, 100}, {4200, 100}, {4200, 300}, {4000, 300}}, 4096)
	if len(pts) != 4 {
		t.Fatalf("clipped = %v", pts)
	}
	cuts := 0
	for i, c := range cut {
		if c {
			cuts++
			a, b := pts[i], pts[(i+1)%len(pts)]
			if a.x != 4096 || b.x != 4096 {
				t.Fatalf("cut edge %v-%v is not on the tile edge", a, b)
			}
		}
	}
	if cuts != 1 {
		t.Fatalf("cut edges = %d, want 1", cuts)
	}
	if pts2, _ := clipRect([]tpoint{{5000, 100}, {5100, 100}, {5100, 200}}, 4096); pts2 != nil {
		t.Fatalf("a ring outside the tile survived: %v", pts2)
	}
}

func TestACutEdgeDrawsNoWallAndNoOutline(t *testing.T) {
	cam := newCamera(360, 0)
	// The west half of a building cut along x = 0 by a tile edge: the cut
	// edge (east, from (0,0) to (0,10)) faces the camera's foot at
	// (0, -520) only obliquely, so make it the south edge instead.
	whole := box("", -10, 0, 20, 10, 10)
	half := box("", -10, 0, 20, 10, 10)
	half.outer.cut[0] = true // the south edge, which faces the camera
	draw := func(p *prism) string {
		w := &svgWriter{cam: cam}
		w.prism(p, cam.foot())
		return w.buf.String()
	}
	a, b := draw(whole), draw(half)
	if strings.Count(b, "Z") >= strings.Count(a, "Z") {
		t.Fatalf("the cut wall was drawn:\n%s\n%s", a, b)
	}
	if !strings.Contains(b, `fill="none"`) {
		t.Fatalf("a cut roof must be outlined apart from its fill:\n%s", b)
	}
	// The roof outline has the three edges the building has, not the cut.
	outline := b[strings.LastIndex(b, `fill="none"`):]
	if n := strings.Count(outline, "M"); n != 3 {
		t.Fatalf("roof outline has %d edges, want 3:\n%s", n, outline)
	}
}

func TestEveryPlaceRendersInOrder(t *testing.T) {
	// The fixture counts move when the fixtures are refreshed (README).
	// A tile edge crosses three of the discs (Zagreb, Zelena Laguna and
	// Delnice), where a footprint cut in two counts once.
	want := map[string]struct {
		buildings int
		pinned    bool
		sparse    bool
	}{
		"split-riva":         {135, true, false},
		"zagreb-vukovarska":  {47, false, false},
		"varazdin-centre":    {80, false, false},
		"porec-riva":         {141, false, false},
		"komiza-harbour":     {321, false, false},
		"rovinj-old-town":    {452, false, false},
		"makarska-riva":      {123, false, false},
		"zelena-laguna-camp": {96, false, false},
		"sv-martin-na-muri":  {165, false, false},
		"delnice-centre":     {126, false, false},
		"zagreb-jankomir":    {22, false, true},
	}
	for _, p := range places {
		pic, err := Render(fixtures, p.Pin)
		sparse := errors.Is(err, ErrTooSparse)
		if err != nil && !sparse {
			t.Fatalf("%s: %v", p.Name, err)
		}
		w := want[p.Name]
		t.Logf("%-20s radius %3.0f buildings %4d guessed %4d faces %5d cycles %d svg %d KB pinned %v", p.Name, pic.Radius, pic.Buildings, pic.Guessed, pic.Faces, pic.Cycles, len(pic.SVG)/1024, pic.Pinned)
		if pic.Buildings != w.buildings {
			t.Errorf("%s: buildings = %d, want %d", p.Name, pic.Buildings, w.buildings)
		}
		if pic.Pinned != w.pinned {
			t.Errorf("%s: pinned = %v, want %v", p.Name, pic.Pinned, w.pinned)
		}
		if sparse != w.sparse {
			t.Errorf("%s: sparse = %v, want %v", p.Name, sparse, w.sparse)
		}
		if pic.Cycles != 0 {
			t.Errorf("%s: %d order cycles", p.Name, pic.Cycles)
		}
		if !bytes.HasPrefix(pic.SVG, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 1000"><g id="s"`)) {
			t.Errorf("%s: svg starts %q", p.Name, pic.SVG[:80])
		}
	}
}

func TestTheSameInputDrawsTheSameBytes(t *testing.T) {
	pin := places[0].Pin
	a, err := Render(fixtures, pin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Render(fixtures, pin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.SVG, b.SVG) {
		t.Fatal("two renders of one pin differ")
	}
}

func TestAPinOutsideTheTilesHasNoPicture(t *testing.T) {
	if _, err := Render(fixtures, Pin{48.2082, 16.3738}); !errors.Is(err, ErrNoTiles) {
		t.Fatalf("Vienna: %v", err)
	}
}

func TestTownsAreDrawnCloserThanVillages(t *testing.T) {
	radius := map[string]float64{}
	for _, p := range places {
		pic, err := Render(fixtures, p.Pin)
		if err != nil && !errors.Is(err, ErrTooSparse) {
			t.Fatalf("%s: %v", p.Name, err)
		}
		if pic.Radius < MinRadius || pic.Radius > MaxRadius {
			t.Fatalf("%s: radius %.0f outside the bounds", p.Name, pic.Radius)
		}
		radius[p.Name] = pic.Radius
	}
	// Zagreb's large blocks are few: counting buildings would draw it
	// about 280 m around, their area draws it as close as the old towns.
	towns := []string{"split-riva", "zagreb-vukovarska", "varazdin-centre", "porec-riva", "rovinj-old-town", "makarska-riva"}
	villages := []string{"zelena-laguna-camp", "sv-martin-na-muri", "delnice-centre"}
	for _, town := range towns {
		if radius[town] > 220 {
			t.Errorf("%s is drawn %.0f m around", town, radius[town])
		}
		for _, village := range villages {
			if radius[town] >= radius[village] {
				t.Errorf("%s (%.0f m) is not drawn closer than %s (%.0f m)", town, radius[town], village, radius[village])
			}
		}
	}

	fixed, err := RenderWith(fixtures, places[0].Pin, Options{Radius: 360})
	if err != nil || fixed.Radius != 360 {
		t.Fatalf("a fixed radius: %.0f, %v", fixed.Radius, err)
	}
}

func TestThePinIsWhereTheCameraAims(t *testing.T) {
	for _, radius := range []float64{MinRadius, MaxRadius} {
		for _, target := range []float64{0, 12, 40} {
			c := newCamera(radius, target)
			p := c.project(0, 0, target)
			if math.Abs(p.x-Width*PinX) > 1e-9 || math.Abs(p.y-Height*PinY) > 1e-9 {
				t.Fatalf("radius %v, target %v projects to %v", radius, target, p)
			}
		}
	}
}

// tilesFunc is a tile source in a function.
type tilesFunc func(z, x, y int) ([]byte, error)

func (f tilesFunc) Tile(z, x, y int) ([]byte, error) { return f(z, x, y) }

// TestAPinOffTheMapHasNoPictureAndReadsNoTiles: towards a pole a metre
// spans ever more of the world, and the tiles around a pin there were
// listed without end (7 GB of them at 89.999°) before one was read.
func TestAPinOffTheMapHasNoPictureAndReadsNoTiles(t *testing.T) {
	read := 0
	counting := tilesFunc(func(z, x, y int) ([]byte, error) { read++; return nil, nil })
	for _, pin := range []Pin{
		{89.999, 16}, {90, 16}, {-90, 16}, {85.06, 16}, {45, 181}, {45, -181},
		{math.NaN(), 16}, {45, math.NaN()}, {math.Inf(1), 16}, {45, math.Inf(-1)},
	} {
		if _, err := Render(counting, pin); !errors.Is(err, ErrNoTiles) {
			t.Errorf("%v: %v, want ErrNoTiles", pin, err)
		}
		if got := TilesFor(pin); got != nil {
			t.Errorf("%v: %d tiles listed", pin, len(got))
		}
	}
	if read != 0 {
		t.Errorf("%d tiles were asked for", read)
	}
	// The northernmost pin on the map is the widest case: a few tiles.
	if n := len(TilesFor(Pin{maxLatitude, 16})); n == 0 || n > 36 {
		t.Errorf("a pin at Web Mercator's edge lists %d tiles", n)
	}
	if _, err := Render(counting, Pin{maxLatitude, 16}); !errors.Is(err, ErrNoTiles) || read == 0 {
		t.Errorf("a pin at Web Mercator's edge: %v after %d tiles", err, read)
	}
}

func TestARadiusTooWideToReadIsRefused(t *testing.T) {
	pin := places[0].Pin
	for _, radius := range []float64{1e6, 1e12, math.Inf(1)} {
		if _, err := RenderWith(fixtures, pin, Options{Radius: radius}); !errors.Is(err, ErrTooWide) {
			t.Errorf("radius %v: %v, want ErrTooWide", radius, err)
		}
	}
	if ids := tilesAround(newFrame(pin), 1e9); ids != nil {
		t.Errorf("%d tiles listed for a reach of a million kilometres", len(ids))
	}
	// A radius that is no number at all is the place's own.
	own, err := Render(fixtures, pin)
	if err != nil {
		t.Fatal(err)
	}
	for _, radius := range []float64{math.NaN(), -5} {
		pic, err := RenderWith(fixtures, pin, Options{Radius: radius})
		if err != nil || pic.Radius != own.Radius {
			t.Errorf("radius %v: drawn %v m around (%v), want the place's %v", radius, pic.Radius, err, own.Radius)
		}
	}
}

// edgeScene is three buildings on the edge between two tiles, each cut in
// two by it, as the two tiles carry them (each into its buffer), and a
// pin in the first one's western half: the shop; a penthouse standing on
// the shop from 12 m up; and a neighbour as high as the shop, south of it
// behind a party wall that ends on the tile edge.
func edgeScene(t *testing.T) scene {
	t.Helper()
	const extent = 4096
	n := float64(int(1) << Zoom)
	west, east := tileID{8900, 5900}, tileID{8901, 5900}
	// The pin: 5 units west of the shared edge, in the middle of the shop.
	wx := (float64(west.x) + float64(extent-5)/extent) / n
	wy := (float64(west.y) + 2000.0/extent) / n
	fr := newFrame(Pin{Lat: math.Atan(math.Sinh(math.Pi*(1-2*wy))) * 180 / math.Pi, Lng: wx*360 - 180})

	building := func(height, minHeight int64, geometry []uint32) feature {
		return feature{kind: geomPolygon, geometry: geometry,
			props: map[string]any{"render_height": height, "render_min_height": minHeight}}
	}
	// x is where the tile has its own east (or west) edge: the buildings
	// reach 20 units to either side of it.
	tile := func(x int32) map[string]*layer {
		return map[string]*layer{"building": {extent: extent, features: []feature{
			building(12, 0, square(x-20, 1980, 40)),  // the shop
			building(20, 12, square(x-10, 1990, 20)), // the penthouse
			building(12, 0, square(x-20, 2020, 40)),  // the neighbour
		}}}
	}
	var sc scene
	if err := sc.add(fr, west, tile(extent), 460); err != nil {
		t.Fatal(err)
	}
	if err := sc.add(fr, east, tile(0), 460); err != nil {
		t.Fatal(err)
	}
	joinParts(sc.prisms)
	if len(sc.prisms) != 6 {
		t.Fatalf("%d prisms, want each building's two halves", len(sc.prisms))
	}
	return sc
}

// TestAFootprintCutByATileEdgeIsOneBuilding: each tile draws its own side
// of a footprint on its edge, and the half the pin was not in used to be
// drawn as any other building.
func TestAFootprintCutByATileEdgeIsOneBuilding(t *testing.T) {
	sc := edgeScene(t)
	pic := draw(sc, MinRadius)
	if !pic.Pinned || pic.Buildings != 3 {
		t.Fatalf("pinned %v, buildings %d, want the pinned shop among 3 buildings", pic.Pinned, pic.Buildings)
	}
	wholes := map[*prism]bool{}
	for _, p := range sc.prisms {
		wholes[p.whole()] = true
		// The neighbour is the one on the ground south of the shop.
		neighbour := p.z0 == 0 && centroid(p.outer.pts).y < -9
		if p.pinned == neighbour {
			t.Errorf("prism at %v from %v m up: pinned %v", centroid(p.outer.pts), p.z0, p.pinned)
		}
	}
	if len(wholes) != 3 {
		t.Errorf("%d buildings among the six halves, want 3", len(wholes))
	}
	// The whole shop is in the pinned colours: both its halves' roofs.
	if n := strings.Count(string(pic.SVG), `fill="`+colPinnedRoof+`"`); n != 4 {
		t.Errorf("%d pinned roofs drawn, want the shop's and the penthouse's two halves each", n)
	}
}

func TestSharesCutTellsAFootprintFromItsNeighbour(t *testing.T) {
	half := func(x0, x1, y0, y1 float64, cut int) ring {
		r := ring{pts: []point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}, cut: make([]bool, 4)}
		r.cut[cut] = true
		return r
	}
	west := half(-10, 0, 0, 10, 1) // its east edge is the cut
	cases := []struct {
		name  string
		other ring
		want  bool
	}{
		{"the other half", half(0, 10, 0.2, 10.1, 3), true}, // a tile unit of rounding
		{"a neighbour behind a party wall", half(0, 10, 10, 20, 3), false},
		{"a neighbour overlapping by the rounding", half(0, 10, 9.7, 20, 3), false},
		{"a half across another edge", half(-10, 0, 10, 20, 0), false},
		{"a building along the same line further on", half(0, 10, 40, 50, 3), false},
	}
	for _, c := range cases {
		if got := sharesCut(west, c.other); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if got := sharesCut(c.other, west); got != c.want {
			t.Errorf("%s, the other way round: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestABuildingDoesNotRunOnBehindTheCamera: a hall whose centre is in the
// disc and whose far end is south of the camera used to be projected
// through the camera's own plane, its south end landing above the picture.
func TestABuildingDoesNotRunOnBehindTheCamera(t *testing.T) {
	// 20 m wide, from 40 m north of the pin to 900 m south of it; the
	// points along its north wall keep its centre (their mean) in the disc.
	pts := []point{{-10, -900}, {10, -900}}
	for x := 10.0; x >= -10; x-- {
		pts = append(pts, point{x, 40})
	}
	hall := &prism{outer: ring{pts: pts, cut: make([]bool, len(pts))}, z1: 9}
	if c := centroid(pts); math.Hypot(c.x, c.y) > MinRadius {
		t.Fatalf("the hall's centre %v is outside the disc", c)
	}
	pic := draw(scene{prisms: []*prism{hall}}, MinRadius)
	if pic.Buildings != 1 {
		t.Fatalf("buildings = %d", pic.Buildings)
	}
	south, cuts := math.Inf(1), 0
	for i, p := range hall.outer.pts {
		south = math.Min(south, p.y)
		if hall.outer.cut[i] {
			cuts++
		}
	}
	if south != southEdge(MinRadius) || cuts != 1 {
		t.Errorf("the hall reaches %v m with %d cut edges, want it cut once at %v", south, cuts, southEdge(MinRadius))
	}
	svg := string(pic.SVG)
	if strings.Contains(svg, "Inf") || strings.Contains(svg, "NaN") {
		t.Fatalf("the picture holds a number that is none:\n%s", svg)
	}
	// Nothing of it is drawn above the frame: the first path is the
	// ground, the rest the hall.
	prisms := svg[strings.Index(svg, `"/>`):]
	for _, m := range regexp.MustCompile(`[ML](-?[0-9.]+),(-?[0-9.]+)`).FindAllStringSubmatch(prisms, -1) {
		if y, err := strconv.ParseFloat(m[2], 64); err != nil || y < -Height {
			t.Fatalf("a point of the hall at y=%v (%v)", m[2], err)
		}
	}

	// A ring that stays north of the line is left alone.
	north := ring{pts: []point{{0, 0}, {10, 0}, {10, 10}}, cut: make([]bool, 3)}
	if got, ok := north.northOf(-5); !ok || len(got.pts) != 3 {
		t.Errorf("a ring north of the line was cut: %v", got)
	}
	if _, ok := north.northOf(50); ok {
		t.Error("a ring south of the line survived")
	}
}

func TestGunzipRefusesATileThatUnpacksTooLarge(t *testing.T) {
	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	_, _ = zw.Write(make([]byte, 1000))
	_ = zw.Close()
	if out, err := gunzip(packed.Bytes(), 1000); err != nil || len(out) != 1000 {
		t.Fatalf("a tile at the limit: %d bytes, %v", len(out), err)
	}
	if _, err := gunzip(packed.Bytes(), 999); err == nil {
		t.Fatal("a tile over the limit was unpacked")
	}
	plain := []byte("not gzipped")
	if out, err := gunzip(plain, 4); err != nil || !bytes.Equal(out, plain) {
		t.Fatalf("a tile that is not gzipped: %q, %v", out, err)
	}
}

func TestDecodeTileRefusesWhatCannotBeDrawn(t *testing.T) {
	feature := pb{}.packed(2, 0, 0).uint(3, 3).packed(4, square(100, 200, 50)...)
	layerOf := func(name string, extent uint64, feature pb) pb {
		return pb{}.bytes(3, pb{}.uint(15, 2).bytes(1, []byte(name)).bytes(2, feature).
			bytes(3, []byte("render_height")).bytes(4, pb{}.uint(4, 12)).uint(5, extent))
	}
	if _, err := decodeTile(layerOf("building", 4096, feature), layers); err != nil {
		t.Fatalf("a good tile: %v", err)
	}
	if _, err := decodeTile(layerOf("building", 0, feature), layers); !errors.Is(err, errMalformed) {
		t.Errorf("an extent of 0: %v", err)
	}
	if _, err := decodeTile(layerOf("poi", 0, feature), layers); err != nil {
		t.Errorf("a layer the picture does not read: %v", err)
	}
	for _, tags := range [][]uint32{{1, 0}, {0, 1}, {1 << 31, 0}, {0, 1<<32 - 1}} {
		bad := pb{}.packed(2, tags...).uint(3, 3).packed(4, square(100, 200, 50)...)
		if _, err := decodeTile(layerOf("building", 4096, bad), layers); !errors.Is(err, errMalformed) {
			t.Errorf("tags %v: %v", tags, err)
		}
	}
}

// TestWhatIsCutSouthIsBelowTheFrame: the cut at southEdge takes nothing
// out of a picture. Wherever a fixture footprint runs south of the line,
// even its roof there is below the frame's bottom edge (with the camera
// aimed at the ground, its highest aim).
func TestWhatIsCutSouthIsBelowTheFrame(t *testing.T) {
	cut := 0
	for _, p := range places {
		sc, _, err := loadScene(fixtures, p.Pin, MaxRadius+reachMargin)
		if err != nil {
			t.Fatal(err)
		}
		for _, radius := range []float64{placeRadius(sc.prisms), MinRadius, 360} {
			cam := newCamera(radius, 0)
			for _, pr := range sc.prisms {
				if c := centroid(pr.outer.pts); math.Hypot(c.x, c.y) > radius {
					continue
				}
				for _, q := range pr.outer.pts {
					if q.y >= southEdge(radius) {
						continue
					}
					cut++
					if at := cam.project(q.x, q.y, pr.z1); at.y <= Height {
						t.Errorf("%s at %v m: a roof %v m up at %v is cut inside the frame (%v)", p.Name, radius, pr.z1, q, at)
					}
				}
			}
		}
	}
	if cut == 0 {
		t.Error("no fixture footprint runs south of the line: the test checks nothing")
	}
	t.Logf("%d footprint points south of the line, all below the frame", cut)
}
