package birdseye

// schemaDefaultHeight is the render_height the OpenMapTiles schema gives a
// building that says nothing about its height or levels; such a height is
// guessed from the footprint instead.
const schemaDefaultHeight = 5

// heights returns the bottom and top of a building's prism in metres and
// whether the top was guessed. footprint is the area in square metres.
// Rule: the tile's height wherever it is not the schema's default,
// otherwise a guess from the footprint's area (a kiosk, a house, a block,
// a hall), which reads as a town without inventing detail.
func heights(f feature, footprint float64) (z0, z1 float64, guessed bool) {
	z0 = f.num("render_min_height")
	z1 = f.num("render_height")
	if z1 == 0 || z1 == schemaDefaultHeight {
		z1, guessed = guessHeight(footprint), true
	}
	if z1 < z0+1 {
		z1 = z0 + 1
	}
	return z0, z1, guessed
}

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
