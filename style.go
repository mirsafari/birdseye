package birdseye

import _ "embed"

// Stylesheet is birdseye.css: default values for every colour the picture
// reads, for light and dark, derived from four base colours a page may set
// (--birdseye-paper, -ink, -accent and -leaf). Serve it, inline it, or copy
// the variables into the page's own theme.
//
//go:embed birdseye.css
var Stylesheet string

// Colors are the CSS variables the picture reads, each one set by
// Stylesheet; a page that styles the picture itself sets them all.
var Colors = []string{
	"--birdseye-ground", "--birdseye-road", "--birdseye-green", "--birdseye-water",
	"--birdseye-roof", "--birdseye-wall-1", "--birdseye-wall-2", "--birdseye-wall-3", "--birdseye-edge",
	"--birdseye-pinned-roof", "--birdseye-pinned-wall-1", "--birdseye-pinned-wall-2", "--birdseye-pinned-wall-3",
	"--birdseye-pinned-edge",
}
