package main

import (
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirsafari/birdseye"
	"github.com/mirsafari/birdseye/birdseyetest"
)

// sheet draws every fixture place at its own radius and, beside it, at a
// fixed one, and writes a page that shows them the way a site would:
// through <use>, coloured by birdseye's default stylesheet (light or dark
// with the reader's colour scheme), with an HTML marker at PinX, PinY. It
// is served (-serve) rather than opened as a file, since <use> does not
// load a file:// reference.
func sheet(args []string) error {
	fs := flag.NewFlagSet("sheet", flag.ExitOnError)
	from := fs.String("tiles", "", "a PMTiles archive or a tiles directory (default: the fixtures)")
	out := fs.String("o", "sheet", "the directory to write")
	beside := fs.Float64("beside", 360, "the fixed radius in metres to draw each place at beside its own")
	serve := fs.String("serve", "", "serve the sheet at this address, e.g. :8099")
	_ = fs.Parse(args)
	src, err := open(*from)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "birdseye.css"), []byte(birdseye.Stylesheet()), 0o644); err != nil {
		return err
	}

	var rows strings.Builder
	for _, p := range birdseyetest.Places {
		rows.WriteString(`<section><h2>` + html.EscapeString(p.Name) + ` <small>` + html.EscapeString(p.Note) + `</small></h2><div class="row">`)
		for _, v := range []struct {
			file   string
			radius float64
		}{{p.Name + ".svg", 0}, {fmt.Sprintf("%s-%.0f.svg", p.Name, *beside), *beside}} {
			start := time.Now()
			pic, err := birdseye.RenderWith(src, p.Pin, birdseye.Options{Radius: v.radius, DrawSparse: true})
			if err != nil {
				return fmt.Errorf("%s: %w", p.Name, err)
			}
			line := stats(pic, time.Since(start)) + sparseNote(pic)
			fmt.Printf("%-20s %s\n", p.Name, line)
			if err := os.WriteFile(filepath.Join(*out, v.file), pic.SVG, 0o644); err != nil {
				return err
			}
			rows.WriteString(fmt.Sprintf(`<figure><div class="birdseye frame"><svg viewBox="0 0 %d %d"><use href="%s#s"/></svg><span class="pin" style="left:%g%%;top:%g%%"></span></div><figcaption>%s</figcaption></figure>`,
				birdseye.Width, birdseye.Height, v.file, birdseye.PinX*100, birdseye.PinY*100, html.EscapeString(line)))
		}
		rows.WriteString(`</div></section>`)
	}
	page := strings.Replace(sheetPage, "{{rows}}", rows.String(), 1)
	if err := os.WriteFile(filepath.Join(*out, "index.html"), []byte(page), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", filepath.Join(*out, "index.html"))
	if *serve == "" {
		fmt.Println("serve it with -serve :8099 (<use> does not load from file://)")
		return nil
	}
	fmt.Printf("serving http://localhost%s/\n", *serve)
	return http.ListenAndServe(*serve, http.FileServer(http.Dir(*out)))
}

const sheetPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>birdseye sheet</title><link rel="stylesheet" href="birdseye.css">
<style>
:root { color-scheme: light dark; font: 14px/1.4 system-ui, sans-serif; }
body { margin: 24px; background: var(--birdseye-paper); color: var(--birdseye-ink); }
h2 { font-size: 16px; margin: 24px 0 8px; } small { font-weight: normal; opacity: .6; }
.row { display: flex; flex-wrap: wrap; gap: 24px; }
figure { margin: 0; width: min(560px, 100%); }
figcaption { font: 11px ui-monospace, monospace; opacity: .7; margin-top: 4px; }
.frame { position: relative; } .frame svg { display: block; width: 100%; height: auto; }
.pin { position: absolute; width: 14px; height: 14px; margin: -7px 0 0 -7px; border-radius: 50%;
  background: var(--birdseye-accent); border: 2px solid var(--birdseye-paper); box-shadow: 0 1px 3px #0006; }
</style></head>
<body><h1>birdseye sheet</h1>
<p>Each place at its own radius and at a fixed one; light or dark follows the system.</p>
{{rows}}
</body></html>
`
