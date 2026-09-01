package cover

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// TestFitKeepsShape checks the aspect arithmetic that everything else leans
// on: cells are half as wide as tall, so a square image must come out twice
// as many columns as rows, and nothing may ever exceed the box.
func TestFitKeepsShape(t *testing.T) {
	cases := []struct {
		iw, ih, w, h, cw, ch int
	}{
		// A square image in a box with square display proportions.
		{200, 200, 80, 40, 80, 40},
		// The box is wider than the image needs: height limits.
		{200, 200, 200, 40, 80, 40},
		// The box is taller than the image needs: width limits.
		{200, 200, 40, 40, 40, 20},
		// A tall scan: width goes unused.
		{100, 400, 80, 40, 20, 40},
		// Degenerate boxes still give at least one cell.
		{200, 200, 1, 1, 1, 1},
	}
	for _, c := range cases {
		img := image.NewRGBA(image.Rect(0, 0, c.iw, c.ih))
		cw, ch := fit(img, c.w, c.h)
		if cw != c.cw || ch != c.ch {
			t.Errorf("fit(%dx%d image, %dx%d box) = %dx%d, want %dx%d",
				c.iw, c.ih, c.w, c.h, cw, ch, c.cw, c.ch)
		}
	}
}

// TestProjectionKeepsAspect renders a disc and measures it: if the sampling
// mishandles the cell shape the disc comes back as an egg, which is exactly
// how a broken converter melts a face. Braille is measured because its output
// is plain runes, but it shares fit and boxSample with the colour path.
func TestProjectionKeepsAspect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			dx, dy := x-100, y-100
			if dx*dx+dy*dy < 90*90 {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	lines := projectBraille(img, 80, 40)

	minX, maxX, minY, maxY := 1<<30, -1, 1<<30, -1
	for y, l := range lines {
		for x, r := range []rune(l) {
			if r >= 0x2800 && r <= 0x28ff && r != 0x2800 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		t.Fatal("the disc rendered to nothing")
	}
	cols := maxX - minX + 1
	rows := maxY - minY + 1
	ratio := float64(cols) / float64(rows)
	// Cells are twice as tall as wide, so a round disc should span twice as
	// many columns as rows. Well outside that and the picture is melted.
	if ratio < 1.6 || ratio > 2.4 {
		t.Errorf("a disc came back %d columns by %d rows (ratio %.2f, want about 2)", cols, rows, ratio)
	}
}

func TestBrailleSpeaksOnlyBraille(t *testing.T) {
	for _, l := range projectBraille(testImage(), 40, 20) {
		for _, r := range l {
			if r < 0x2800 || r > 0x28ff {
				t.Fatalf("braille output holds %q", r)
			}
		}
	}
}

// TestQuantiserIsExactOnPaletteColours: a colour the xterm palette actually
// contains must map to itself, both in the cube and on the grey ramp.
func TestQuantiserIsExactOnPaletteColours(t *testing.T) {
	cases := []struct {
		in   rgb
		want uint8
	}{
		{rgb{0, 0, 0}, 16},
		{rgb{255, 255, 255}, 231},
		{rgb{95, 135, 175}, 67},
		{rgb{255, 0, 0}, 196},
		{rgb{8, 8, 8}, 232},
		{rgb{128, 128, 128}, 244},
		{rgb{238, 238, 238}, 255},
	}
	for _, c := range cases {
		idx, got := xtermQuantise(c.in)
		if idx != c.want {
			t.Errorf("xtermQuantise(%v) picked index %d, want %d", c.in, idx, c.want)
		}
		if got != c.in {
			t.Errorf("xtermQuantise(%v) rendered %v; a palette colour should survive exactly", c.in, got)
		}
	}
}

// TestQuantiserIsNearestNeighbour holds the fast quantiser to the answer a
// brute-force search over the cube and grey ramp gives: the shortcut through
// midpoint thresholds must never pick a farther colour than the palette
// offers. The measured error over a 17-step lattice — mean 28.1, worst 74.7
// — is the geometry of xterm's palette, not a property of the search, so it
// is pinned only loosely as a regression rail.
func TestQuantiserIsNearestNeighbour(t *testing.T) {
	palette := make([]rgb, 0, 240)
	for i := range 216 {
		palette = append(palette, rgb{cubeLevels[i/36], cubeLevels[i/6%6], cubeLevels[i%6]})
	}
	for i := range 24 {
		palette = append(palette, rgb{greyLevel(i), greyLevel(i), greyLevel(i)})
	}

	var sum, worst float64
	var n int
	for r := 0; r <= 255; r += 17 {
		for g := 0; g <= 255; g += 17 {
			for b := 0; b <= 255; b += 17 {
				c := rgb{uint8(r), uint8(g), uint8(b)}
				_, q := xtermQuantise(c)
				got := dist2(c, q)

				best := 1 << 30
				for _, p := range palette {
					if d := dist2(c, p); d < best {
						best = d
					}
				}
				if got != best {
					t.Errorf("xtermQuantise(%v) accepts distance² %d, the palette offers %d", c, got, best)
				}

				d := math.Sqrt(float64(got))
				sum += d
				worst = math.Max(worst, d)
				n++
			}
		}
	}
	if mean := sum / float64(n); mean > 30 {
		t.Errorf("mean quantisation error %.1f, want at most 30", mean)
	}
	if worst > 80 {
		t.Errorf("worst quantisation error %.1f, want at most 80", worst)
	}
}
