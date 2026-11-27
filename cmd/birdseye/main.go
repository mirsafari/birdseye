// Command birdseye is birdseye's dev tool: it draws one pin, writes a
// contact sheet of the fixture places to judge the look by, and cuts the
// fixtures from a tile archive.
//
//	birdseye one   [-tiles croatia-z14.pmtiles] -lat 45.0827 -lng 13.6312 [-radius 0] -o rovinj.svg
//	birdseye sheet [-tiles croatia-z14.pmtiles] [-o sheet] [-beside 360] [-serve :8099]
//	birdseye cut   -tiles croatia-z14.pmtiles [-o birdseyetest/fixtures/tiles]
//
// -tiles is a PMTiles archive or a directory of <z>-<x>-<y>.pbf files;
// without it, one and sheet read the fixtures embedded in birdseyetest.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mirsafari/birdseye"
	"github.com/mirsafari/birdseye/birdseyetest"
	"github.com/mirsafari/birdseye/pmtiles"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "one":
		err = one(os.Args[2:])
	case "sheet":
		err = sheet(os.Args[2:])
	case "cut":
		err = cut(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "birdseye:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: birdseye one|sheet|cut [flags] (see -h of each)")
	os.Exit(2)
}

// source is a tile source and what closes it.
type source struct {
	birdseye.Tiles
	io.Closer
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// open opens -tiles: an archive, a directory, or the fixtures when empty.
func open(path string) (source, error) {
	if path == "" {
		return source{birdseyetest.Tiles(), nopCloser{}}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return source{}, err
	}
	if info.IsDir() {
		return source{birdseye.DirTiles(path), nopCloser{}}, nil
	}
	f, err := pmtiles.Open(path)
	if err != nil {
		return source{}, err
	}
	return source{f, f}, nil
}

// stats is one picture's numbers.
func stats(pic birdseye.Picture, took time.Duration) string {
	return fmt.Sprintf("r %3.0f m  buildings %4d  guessed %4d  faces %5d  cycles %d  svg %4.0f KB  pinned %-5v  %5.1f ms",
		pic.Radius, pic.Buildings, pic.Guessed, pic.Faces, pic.Cycles, float64(len(pic.SVG))/1024, pic.Pinned,
		float64(took.Microseconds())/1000)
}

func one(args []string) error {
	fs := flag.NewFlagSet("one", flag.ExitOnError)
	from := fs.String("tiles", "", "a PMTiles archive or a tiles directory (default: the fixtures)")
	lat := fs.Float64("lat", 0, "latitude")
	lng := fs.Float64("lng", 0, "longitude")
	radius := fs.Float64("radius", 0, "radius in metres (0: the place's own)")
	out := fs.String("o", "birdseye.svg", "the SVG to write")
	_ = fs.Parse(args)
	src, err := open(*from)
	if err != nil {
		return err
	}
	defer src.Close()
	start := time.Now()
	pic, err := birdseye.RenderWith(src, birdseye.Pin{Lat: *lat, Lng: *lng}, birdseye.Options{Radius: *radius, DrawSparse: true})
	if err != nil {
		return err
	}
	fmt.Println(stats(pic, time.Since(start)) + sparseNote(pic))
	return os.WriteFile(*out, pic.SVG, 0o644)
}

// sparseNote says of a picture the tool drew anyway (Options.DrawSparse)
// that an app gets none: Render answers ErrTooSparse there.
func sparseNote(pic birdseye.Picture) string {
	if pic.Buildings >= birdseye.MinBuildings {
		return ""
	}
	return "  (too sparse: a site shows a map)"
}

// cut copies the tiles every fixture place reads from an archive into a
// directory.
func cut(args []string) error {
	fs := flag.NewFlagSet("cut", flag.ExitOnError)
	from := fs.String("tiles", "", "the PMTiles archive")
	out := fs.String("o", filepath.Join("birdseyetest", "fixtures", "tiles"), "the fixtures directory")
	_ = fs.Parse(args)
	if *from == "" {
		return errors.New("cut needs -tiles")
	}
	src, err := open(*from)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	seen := map[[3]int]bool{}
	var n, size int
	for _, p := range birdseyetest.Places {
		for _, t := range birdseye.TilesFor(p.Pin) {
			if seen[t] {
				continue
			}
			seen[t] = true
			data, err := src.Tile(t[0], t[1], t[2])
			if err != nil {
				return err
			}
			if data == nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(*out, fmt.Sprintf("%d-%d-%d.pbf", t[0], t[1], t[2])), data, 0o644); err != nil {
				return err
			}
			n++
			size += len(data)
		}
	}
	fmt.Printf("%d tiles, %.1f MB\n", n, float64(size)/1e6)
	return nil
}
