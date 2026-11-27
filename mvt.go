package birdseye

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/mirsafari/birdseye/internal/unzip"
)

// A vector tile (Mapbox Vector Tile 2.1) is a protocol buffer: layers of
// features, each feature a geometry of drawing commands in tile units
// (0..extent, y pointing down) and tags that index the layer's keys and
// values. The wire format is small enough to read by hand, which keeps the
// renderer on the standard library. Only what the picture needs is
// decoded; unknown fields are skipped.

var errMalformed = errors.New("birdseye: malformed vector tile")

// geomType is a feature's geometry type in the tile.
type geomType uint8

const (
	geomPoint   geomType = 1
	geomLine    geomType = 2
	geomPolygon geomType = 3
)

// layer is one decoded layer of a tile: its features and the keys and
// values their tags index.
type layer struct {
	extent   float64
	keys     []string
	values   []any // string, float64, int64 or bool
	features []feature
}

// feature is one feature: its type, and its tags and geometry as the
// packed varints the tile holds, checked when the tile was decoded and
// read only when the feature is wanted. Most features of a tile are far
// from the pin, and a picture reads a handful of properties of the rest.
type feature struct {
	kind     geomType
	layer    *layer // whose keys and values the tags index
	tags     []byte // key and value indexes, in pairs
	geometry []byte // the command stream
	values   int    // how many varints the geometry holds
}

// prop is a property's value, nil when absent. A key given twice is the
// later value, as a map filled in tag order holds it.
func (f feature) prop(key string) any {
	var out any
	t := varints{b: f.tags}
	for n := t.count(); n >= 2; n -= 2 {
		k, v := t.next(), t.next()
		if f.layer.keys[k] == key {
			out = f.layer.values[v]
		}
	}
	return out
}

// str is a string property, "" when absent or not a string.
func (f feature) str(key string) string {
	s, _ := f.prop(key).(string)
	return s
}

// num is a numeric property, 0 when absent.
func (f feature) num(key string) float64 {
	switch v := f.prop(key).(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}

// flag is a boolean property (a number other than 0 counts as true).
func (f feature) flag(key string) bool {
	switch v := f.prop(key).(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

// varints reads packed varints that were checked (checkPacked) when the
// tile was decoded, as the uint32 values the tile format stores.
type varints struct {
	b []byte
	i int
}

func (v *varints) next() uint32 {
	var x uint64
	for shift := uint(0); ; shift += 7 {
		c := v.b[v.i]
		v.i++
		x |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return uint32(x)
		}
	}
}

// count is how many varints are left, counted by their last bytes.
func (v varints) count() int {
	n := 0
	for _, c := range v.b[v.i:] {
		if c < 0x80 {
			n++
		}
	}
	return n
}

// checkPacked reports how many varints packed bytes hold, or false when
// they are not whole varints of at most ten bytes each.
func checkPacked(b []byte) (int, bool) {
	n, run := 0, 0
	for _, c := range b {
		run++
		if run > 10 {
			return 0, false
		}
		if c < 0x80 {
			n++
			run = 0
		}
	}
	return n, run == 0
}

// tpoint is a position in tile units.
type tpoint struct{ x, y float64 }

// paths decodes the geometry into its paths: a polygon's rings closed
// (first point repeated at the end), a line's parts open.
func (f feature) paths() ([][]tpoint, error) {
	var out [][]tpoint
	if _, err := f.eachPath(nil, func(path []tpoint) { out = append(out, slices.Clone(path)) }); err != nil {
		return nil, err
	}
	return out, nil
}

// eachPath decodes the geometry path by path into buf, which it reuses
// and returns for the next feature: a polygon's rings closed (first point
// repeated at the end), a line's parts open. A path handed to yield is
// only good until yield returns, so a caller keeps a copy of what it
// wants and pays nothing for what it does not: most paths of a feature
// are far from the pin.
func (f feature) eachPath(buf []tpoint, yield func(path []tpoint)) ([]tpoint, error) {
	cur := buf[:0]
	var x, y int64
	g := varints{b: f.geometry}
	for i := 0; i < f.values; {
		c := g.next()
		cmd, count := c&7, int(c>>3)
		i++
		switch cmd {
		case 1, 2: // MoveTo, LineTo
			if i+2*count > f.values {
				return cur, errMalformed
			}
			if cmd == 2 { // the path's points, and the one ClosePath repeats
				cur = slices.Grow(cur, count+1)
			}
			for k := 0; k < count; k++ {
				x += zigzag(uint64(g.next()))
				y += zigzag(uint64(g.next()))
				i += 2
				if cmd == 1 {
					if len(cur) > 0 {
						yield(cur)
					}
					cur = cur[:0]
				}
				cur = append(cur, tpoint{float64(x), float64(y)})
			}
		case 7: // ClosePath
			if len(cur) > 0 {
				cur = append(cur, cur[0])
			}
		default:
			return cur, errMalformed
		}
	}
	if len(cur) > 0 {
		yield(cur)
	}
	return cur, nil
}

// bounds is the box of the geometry in tile units, read from the commands
// without building the paths, so features far from the pin are skipped
// cheaply. ok is false for an empty geometry.
func (f feature) bounds() (lo, hi tpoint, ok bool) {
	var x, y int64
	lo = tpoint{math.Inf(1), math.Inf(1)}
	hi = tpoint{math.Inf(-1), math.Inf(-1)}
	g := varints{b: f.geometry}
	for i := 0; i < f.values; {
		c := g.next()
		cmd, count := c&7, int(c>>3)
		i++
		if cmd == 7 {
			continue
		}
		for k := 0; k < count && i+1 < f.values; k++ {
			x += zigzag(uint64(g.next()))
			y += zigzag(uint64(g.next()))
			i += 2
			lo.x, lo.y = min(lo.x, float64(x)), min(lo.y, float64(y))
			hi.x, hi.y = max(hi.x, float64(x)), max(hi.y, float64(y))
			ok = true
		}
	}
	return lo, hi, ok
}

// zigzag decodes a protocol buffer's zigzag-encoded signed number, as
// the geometry's parameters and sint values are written.
func zigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// maxTileBytes bounds a tile as it is stored and once unpacked: the
// densest tile of Croatia's archive unpacks to 160 KB, so a few kilobytes
// of gzip cannot ask for gigabytes. What a tile's bytes can then make the
// drawing hold is bounded by maxScenePoints.
const maxTileBytes = 8 << 20

// gunzip unpacks data when it is gzipped and refuses more than limit
// bytes of it, packed or not.
func gunzip(data []byte, limit int) ([]byte, error) {
	if !unzip.Gzipped(data) {
		if len(data) > limit {
			return nil, fmt.Errorf("birdseye: tile of %d bytes, more than %d", len(data), limit)
		}
		return data, nil
	}
	out, err := unzip.Limited(data, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("birdseye: gunzip tile: %w", err)
	}
	return out, nil
}

// decodeTile reads the named layers of a tile, gzipped or not. A layer
// missing from the tile is missing from the map.
func decodeTile(data []byte, wanted map[string]bool) (map[string]*layer, error) {
	data, err := gunzip(data, maxTileBytes)
	if err != nil {
		return nil, err
	}
	layers := map[string]*layer{}
	p := pbuf{b: data}
	for !p.done() {
		num, wire, err := p.field()
		if err != nil {
			return nil, err
		}
		if num != 3 || wire != 2 {
			if err := p.skip(wire); err != nil {
				return nil, err
			}
			continue
		}
		raw, err := p.bytes()
		if err != nil {
			return nil, err
		}
		name, l, err := decodeLayer(raw, wanted)
		if err != nil {
			return nil, err
		}
		if l != nil {
			layers[name] = l
		}
	}
	return layers, nil
}

// decodeLayer reads one layer; l is nil when the layer is not wanted.
func decodeLayer(data []byte, wanted map[string]bool) (string, *layer, error) {
	var (
		name     string
		keys     []string
		values   []any
		features [][]byte
		extent   = 4096.0
	)
	p := pbuf{b: data}
	for !p.done() {
		num, wire, err := p.field()
		if err != nil {
			return "", nil, err
		}
		switch {
		case num == 1 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return "", nil, err
			}
			name = string(b)
			if !wanted[name] {
				return name, nil, nil
			}
		case num == 2 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return "", nil, err
			}
			features = append(features, b)
		case num == 3 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return "", nil, err
			}
			keys = append(keys, string(b))
		case num == 4 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return "", nil, err
			}
			v, err := decodeValue(b)
			if err != nil {
				return "", nil, err
			}
			values = append(values, v)
		case num == 5 && wire == 0:
			v, err := p.varint()
			if err != nil {
				return "", nil, err
			}
			extent = float64(v)
		default:
			if err := p.skip(wire); err != nil {
				return "", nil, err
			}
		}
	}
	if !wanted[name] {
		return name, nil, nil
	}
	if extent == 0 { // every position would divide by it
		return "", nil, errMalformed
	}
	l := &layer{extent: extent, keys: keys, values: values, features: make([]feature, 0, len(features))}
	for _, raw := range features {
		f, err := decodeFeature(raw, l)
		if err != nil {
			return "", nil, err
		}
		l.features = append(l.features, f)
	}
	return name, l, nil
}

// decodeFeature reads a feature's type and finds its tags and geometry,
// checking that they are whole varints and that every tag indexes one of
// the layer's keys and values, so reading them later cannot fail.
func decodeFeature(data []byte, l *layer) (feature, error) {
	f := feature{layer: l}
	p := pbuf{b: data}
	for !p.done() {
		num, wire, err := p.field()
		if err != nil {
			return f, err
		}
		switch {
		case num == 2 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return f, err
			}
			n, ok := checkPacked(b)
			if !ok {
				return f, errMalformed
			}
			t := varints{b: b}
			for ; n >= 2; n -= 2 {
				// Compared unsigned: as an int, an index past 2³¹ is
				// negative where int has 32 bits.
				k, v := t.next(), t.next()
				if uint64(k) >= uint64(len(l.keys)) || uint64(v) >= uint64(len(l.values)) {
					return f, errMalformed
				}
			}
			f.tags = b
		case num == 3 && wire == 0:
			v, err := p.varint()
			if err != nil {
				return f, err
			}
			f.kind = geomType(v)
		case num == 4 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return f, err
			}
			n, ok := checkPacked(b)
			if !ok {
				return f, errMalformed
			}
			f.geometry, f.values = b, n
		default:
			if err := p.skip(wire); err != nil {
				return f, err
			}
		}
	}
	return f, nil
}

func decodeValue(data []byte) (any, error) {
	p := pbuf{b: data}
	var out any
	for !p.done() {
		num, wire, err := p.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == 2:
			b, err := p.bytes()
			if err != nil {
				return nil, err
			}
			out = string(b)
		case num == 2 && wire == 5:
			v, err := p.fixed(4)
			if err != nil {
				return nil, err
			}
			out = float64(math.Float32frombits(uint32(v)))
		case num == 3 && wire == 1:
			v, err := p.fixed(8)
			if err != nil {
				return nil, err
			}
			out = math.Float64frombits(v)
		case (num == 4 || num == 5) && wire == 0:
			v, err := p.varint()
			if err != nil {
				return nil, err
			}
			out = int64(v)
		case num == 6 && wire == 0:
			v, err := p.varint()
			if err != nil {
				return nil, err
			}
			out = zigzag(v)
		case num == 7 && wire == 0:
			v, err := p.varint()
			if err != nil {
				return nil, err
			}
			out = v != 0
		default:
			if err := p.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// pbuf reads the protocol buffer wire format.
type pbuf struct {
	b []byte
	i int
}

func (p *pbuf) done() bool { return p.i >= len(p.b) }

func (p *pbuf) varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if p.i >= len(p.b) {
			return 0, errMalformed
		}
		c := p.b[p.i]
		p.i++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, errMalformed
}

func (p *pbuf) field() (num, wire int, err error) {
	v, err := p.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(v >> 3), int(v & 7), nil
}

func (p *pbuf) bytes() ([]byte, error) {
	n, err := p.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(p.b)-p.i) {
		return nil, errMalformed
	}
	b := p.b[p.i : p.i+int(n)]
	p.i += int(n)
	return b, nil
}

func (p *pbuf) fixed(n int) (uint64, error) {
	if p.i+n > len(p.b) {
		return 0, errMalformed
	}
	var v uint64
	for k := n - 1; k >= 0; k-- {
		v = v<<8 | uint64(p.b[p.i+k])
	}
	p.i += n
	return v, nil
}

func (p *pbuf) skip(wire int) error {
	switch wire {
	case 0:
		_, err := p.varint()
		return err
	case 1:
		_, err := p.fixed(8)
		return err
	case 2:
		_, err := p.bytes()
		return err
	case 5:
		_, err := p.fixed(4)
		return err
	}
	return errMalformed
}
