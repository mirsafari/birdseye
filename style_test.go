package birdseye

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestStylesheetSetsEveryColourThePictureReads: the colours the picture
// draws with are ColorVariables, and Stylesheet sets each of them for light
// and for dark.
func TestStylesheetSetsEveryColourThePictureReads(t *testing.T) {
	used := map[string]bool{}
	for _, p := range places {
		pic, err := RenderWith(t.Context(), fixtures, p.Pin, Options{DrawSparse: true})
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		for _, m := range regexp.MustCompile(`var\((--birdseye-[a-z0-9-]+)\)`).FindAllStringSubmatch(string(pic.SVG), -1) {
			used[m[1]] = true
		}
	}
	for name := range used {
		if !slices.Contains(ColorVariables(), name) {
			t.Errorf("the picture reads %s, which ColorVariables leaves out", name)
		}
	}
	drawn := append([]string{colGround, colGreen, colWater, colRoad, colRoof, colEdge, colPinnedRoof, colPinnedEdge}, colWalls[:]...)
	drawn = append(drawn, colPinnedWalls[:]...)
	for i, c := range drawn {
		drawn[i] = strings.TrimSuffix(strings.TrimPrefix(c, "var("), ")")
	}
	slices.Sort(drawn)
	listed := slices.Sorted(slices.Values(ColorVariables()))
	if !slices.Equal(drawn, listed) {
		t.Errorf("the picture's colours %v, ColorVariables %v", drawn, listed)
	}
	light, dark, found := strings.Cut(Stylesheet(), "@media (prefers-color-scheme: dark)")
	if !found {
		t.Fatal("no dark block")
	}
	for _, name := range ColorVariables() {
		for scheme, css := range map[string]string{"light": light, "dark": dark} {
			if !strings.Contains(css, "\n    "+name+": ") && !strings.Contains(css, "\n  "+name+": ") {
				t.Errorf("%s: Stylesheet does not set %s", scheme, name)
			}
		}
	}
}

// TestColorVariablesAreTheCallersOwn: a caller that sorts or edits its
// list changes nobody else's.
func TestColorVariablesAreTheCallersOwn(t *testing.T) {
	mine := ColorVariables()
	if len(mine) != 14 {
		t.Fatalf("%d colour variables, want the 14 the package documentation names", len(mine))
	}
	mine[0] = "--mine"
	slices.Reverse(mine)
	if again := ColorVariables(); again[0] != "--birdseye-ground" || slices.Contains(again, "--mine") {
		t.Fatalf("a caller's edit reached the next caller: %v", again)
	}
}
