package birdseye

import "math"

// schemaDefaultHeight is the render_height the OpenMapTiles schema gives a
// building that says nothing about its height or levels; such a height is
// guessed from the footprint instead.
const schemaDefaultHeight = 5

// heights returns the bottom and top of a building's prism in metres and
// whether the top was guessed. footprint is the area in square metres.
// Rule: the tile's height wherever it is not the schema's default,
// otherwise a guess from the footprint's area (a kiosk, a house, a block,
// a hall), which reads as a town without inventing detail. A height that
// is no number, infinite or below the ground is not believed: such a
// bottom is the ground, such a top is guessed. Not a number, it went on
// into the picture's coordinates; below the ground, it drew walls over
// the street in front.
func heights(f feature, footprint float64) (z0, z1 float64, guessed bool) {
	z0 = f.num("render_min_height")
	z1 = f.num("render_height")
	if !believed(z0) {
		z0 = 0
	}
	if !believed(z1) || z1 == 0 || z1 == schemaDefaultHeight {
		z1, guessed = guessHeight(footprint), true
	}
	if z1 < z0+1 {
		z1 = z0 + 1
	}
	return z0, z1, guessed
}

// believed reports whether a height from the tile is one: a finite
// number at or above the ground.
func believed(h float64) bool { return h >= 0 && !math.IsInf(h, 1) }

// guessHeight is the height of a building of this footprint.
func guessHeight(footprint float64) float64 {
	switch {
	case footprint < 40:
		return 3.5
	case footprint < 160:
		return 7
	case footprint < 900:
		return 9.5
	}
	return 11
}
