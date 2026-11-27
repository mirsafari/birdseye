package birdseye

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestStylesheetSetsEveryColourThePictureReads: the colours the picture
// draws with are Colors, and Stylesheet sets each of them for light and for dark.
func TestStylesheetSetsEveryColourThePictureReads(t *testing.T) {
	used := map[string]bool{}
	for _, p := range places {
		pic, _ := Render(fixtures, p.Pin)
		for _, m := range regexp.MustCompile(`var\((--birdseye-[a-z0-9-]+)\)`).FindAllStringSubmatch(string(pic.SVG), -1) {
			used[m[1]] = true
		}
	}
	for name := range used {
		if !slices.Contains(Colors, name) {
			t.Errorf("the picture reads %s, which Colors leaves out", name)
		}
	}
	drawn := append([]string{colGround, colGreen, colWater, colRoad, colRoof, colEdge, colPinnedRoof, colPinnedEdge}, colWalls[:]...)
	drawn = append(drawn, colPinnedWalls[:]...)
	for i, c := range drawn {
		drawn[i] = strings.TrimSuffix(strings.TrimPrefix(c, "var("), ")")
	}
	slices.Sort(drawn)
	listed := slices.Sorted(slices.Values(Colors))
	if !slices.Equal(drawn, listed) {
		t.Errorf("the picture's colours %v, Colors %v", drawn, listed)
	}
	light, dark, found := strings.Cut(Stylesheet, "@media (prefers-color-scheme: dark)")
	if !found {
		t.Fatal("no dark block")
	}
	for _, name := range Colors {
		for scheme, css := range map[string]string{"light": light, "dark": dark} {
			if !strings.Contains(css, "\n    "+name+": ") && !strings.Contains(css, "\n  "+name+": ") {
				t.Errorf("%s: Stylesheet does not set %s", scheme, name)
			}
		}
	}
}
