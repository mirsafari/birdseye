// Package birdseye draws the surroundings of a map pin as a picture: the
// buildings around it raised from their OpenStreetMap footprints and seen
// from above at an angle, over the parks, water and streets, as an SVG
// that a web page colours with its own theme. It is a cartographer's
// locator map, made per pin.
//
// The picture is a pure function of the vector tiles (the OpenMapTiles
// schema at Zoom), the pin and StyleVersion, so nothing needs storing: an
// app may draw on demand and keep what it drew. The package reads nothing
// but the Tiles it is given and uses only the standard library. Tiles come
// from a PMTiles archive (package pmtiles), a directory of tile files
// (DirTiles) or any fs.FS (FSTiles); package birdseyetest has fixtures.
//
// # What a page relies on
//
//   - The SVG holds one group with id "s" in a viewBox of Width by Height.
//     Show it through <svg viewBox="0 0 1600 1000"><use href="pic.svg#s"/></svg>,
//     so the page's CSS variables reach the referenced tree.
//   - Every colour is a CSS variable, --birdseye-ground, -road, -green,
//     -water, -roof, -wall-1 to -wall-3 (lit to shaded), -edge, and the
//     pinned building's -pinned-roof, -pinned-wall-1 to -3 and
//     -pinned-edge. Stylesheet holds defaults that follow the reader's
//     colour scheme; a page with a theme sets its own.
//   - The camera aims at the point above the pin (on the pinned
//     building's roof when there is one), which lands at PinX and PinY of
//     the picture, so an HTML marker placed there stands on the pin.
//   - North is up. The same tiles, pin and StyleVersion give the same
//     bytes, so a hash of the SVG is a cache key and an address version.
//   - ErrNoTiles and ErrTooSparse say when there is no picture worth
//     showing; a page then shows an ordinary map.
//
// OpenStreetMap's data is under the ODbL and the OpenMapTiles schema under
// CC-BY: a page showing the picture credits "© OpenMapTiles
// © OpenStreetMap contributors".
package birdseye
