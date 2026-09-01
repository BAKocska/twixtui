package cover

import (
	"image"
	"math"
	"sort"
	"strings"
)

// The converter's geometry problem: a terminal cell is about twice as tall as
// it is wide, and every projection subdivides a cell differently. Getting
// either wrong melts the picture. Both are solved in one place — fit below
// works in display units where a cell is 1 wide and 2 tall, and each
// projection then samples the image at its own subdivision of the cells it
// was given, so the sampling regions come out with the right shape without
// any projection doing aspect arithmetic of its own.

// fit returns how many cells of a w by h box the image should occupy to keep
// its shape, in cells that are twice as tall as wide. At least one cell each
// way, never more than the box.
func fit(img image.Image, w, h int) (cw, ch int) {
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	s := float64(w) / iw
	if s2 := float64(2*h) / ih; s2 < s {
		s = s2
	}
	cw = clampInt(int(iw*s+0.5), 1, w)
	ch = clampInt(int(ih*s/2+0.5), 1, h)
	return cw, ch
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// lin is a colour in linear light. Averaging happens here rather than on sRGB
// bytes because sRGB is gamma-encoded: averaging it directly darkens every
// edge, which on a picture full of thin links reads as a smudge.
type lin struct {
	r, g, b float64
}

var srgbToLinear [256]float64

func init() {
	for i := range srgbToLinear {
		v := float64(i) / 255
		if v <= 0.04045 {
			srgbToLinear[i] = v / 12.92
		} else {
			srgbToLinear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
}

func linearToByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	var s float64
	if v <= 0.0031308 {
		s = v * 12.92
	} else {
		s = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(s*255 + 0.5)
}

func (l lin) rgb() rgb {
	return rgb{linearToByte(l.r), linearToByte(l.g), linearToByte(l.b)}
}

// luminance is relative luminance in linear light, 0..1.
func (l lin) luminance() float64 {
	return 0.2126*l.r + 0.7152*l.g + 0.0722*l.b
}

// boxSample reduces the image to a tw by th grid, each output pixel the mean
// of its source rectangle. A box filter is as plain as resampling gets, but
// the covers being projected shrink by a factor of ten or more, and at that
// ratio the filter window dwarfs any difference a fancier kernel would make.
func boxSample(img image.Image, tw, th int) [][]lin {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	out := make([][]lin, th)
	for ty := range th {
		row := make([]lin, tw)
		y0 := b.Min.Y + ty*ih/th
		y1 := b.Min.Y + (ty+1)*ih/th
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for tx := range tw {
			x0 := b.Min.X + tx*iw/tw
			x1 := b.Min.X + (tx+1)*iw/tw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var acc lin
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bb, _ := img.At(x, y).RGBA()
					acc.r += srgbToLinear[r>>8]
					acc.g += srgbToLinear[g>>8]
					acc.b += srgbToLinear[bb>>8]
				}
			}
			n := float64((y1 - y0) * (x1 - x0))
			row[tx] = lin{acc.r / n, acc.g / n, acc.b / n}
		}
		out[ty] = row
	}
	return out
}

// projectQuadrant renders two by two pixels per cell with quadrant blocks. A
// cell still only has two colours, so the four pixels are split about their
// mean luminance and each side gets its average colour.
//
// This is the projection colour terminals get. A half-block projection — one
// cell per two vertical pixels, full colour in each half — was built and
// compared expecting it to win on fidelity; on the printed covers this path
// exists for, the quadrants' doubled resolution beat the extra colour at
// every size tried, and on smooth photographic tone the two drew level, so
// the half blocks were dropped rather than kept as a second path.
//
// Sextants were considered and rejected: they buy half a step more resolution
// but live in Unicode 13's legacy computing block, which the fonts terminals
// actually ship still render patchily, and a cover that draws as tofu on a
// stock macOS terminal is worse than a slightly softer one.
func projectQuadrant(img image.Image, w, h int, depth Depth) []string {
	cw, ch := fit(img, w, h)
	px := boxSample(img, 2*cw, 2*ch)
	cv := newCanvas(cw, ch)
	for y := range ch {
		for x := range cw {
			quad := [4]lin{px[2*y][2*x], px[2*y][2*x+1], px[2*y+1][2*x], px[2*y+1][2*x+1]}
			mean := (quad[0].luminance() + quad[1].luminance() + quad[2].luminance() + quad[3].luminance()) / 4
			var bits int
			var hi, lo lin
			var nhi, nlo int
			for i, q := range quad {
				if q.luminance() >= mean {
					bits |= 1 << i
					hi.r += q.r
					hi.g += q.g
					hi.b += q.b
					nhi++
				} else {
					lo.r += q.r
					lo.g += q.g
					lo.b += q.b
					nlo++
				}
			}
			if nlo == 0 {
				// A flat cell: no split exists, paint it solid.
				fg := lin{hi.r / 4, hi.g / 4, hi.b / 4}.rgb()
				cv.set(x, y, '█', &fg, &fg)
				continue
			}
			fg := lin{hi.r / float64(nhi), hi.g / float64(nhi), hi.b / float64(nhi)}.rgb()
			bg := lin{lo.r / float64(nlo), lo.g / float64(nlo), lo.b / float64(nlo)}.rgb()
			cv.set(x, y, quadRunes[bits], &fg, &bg)
		}
	}
	return cv.lines(depth)
}

// quadRunes maps a bitmask of lit quadrants — 1 top-left, 2 top-right, 4
// bottom-left, 8 bottom-right — to the block element with exactly those
// quarters inked.
var quadRunes = [16]rune{
	' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛',
	'▗', '▚', '▐', '▜', '▄', '▙', '▟', '█',
}

// projectBraille renders two by four dots per cell as a luminance bitmap: the
// highest spatial resolution a character cell offers, and the projection of
// choice when there is no colour to spend. Dots are square at the usual cell
// shape, so the fit needs no special case. An ASCII luminance ramp was built
// and compared for the same duty and lost at every size — one level per
// whole cell turns a low-contrast cover into grey mush — so braille is the
// only monochrome projection kept. It assumes light ink on a dark terminal,
// as the interface's own monochrome theme does.
//
// A dot means "brighter than its neighbourhood", decided by Floyd-Steinberg
// dithering over normalised lightness rather than a fixed threshold: the
// period covers this exists for are low-contrast beige things, and a fixed
// threshold turns them into a white sheet with three specks.
func projectBraille(img image.Image, w, h int) []string {
	cw, ch := fit(img, w, h)
	px := boxSample(img, 2*cw, 4*ch)
	light := lightnessGrid(px)
	dots := dither(light)
	out := make([]string, 0, ch)
	for y := range ch {
		var b strings.Builder
		for x := range cw {
			bits := 0
			// Braille bit layout: dots 1..3 down the left column, 4..6 down
			// the right, 7 and 8 the bottom pair.
			for dy := range 3 {
				if dots[4*y+dy][2*x] {
					bits |= 1 << dy
				}
				if dots[4*y+dy][2*x+1] {
					bits |= 8 << dy
				}
			}
			if dots[4*y+3][2*x] {
				bits |= 0x40
			}
			if dots[4*y+3][2*x+1] {
				bits |= 0x80
			}
			b.WriteRune(rune(0x2800 + bits))
		}
		out = append(out, strings.TrimRight(b.String(), "\u2800"))
	}
	return out
}

// lightnessGrid turns sampled colours into normalised lightness. The stretch
// to the 2nd..98th percentile is deliberate: a monochrome projection has no
// absolute level to be faithful to, contrast is the only thing it can carry,
// and the scans this faces waste most of their range on beige.
func lightnessGrid(px [][]lin) [][]float64 {
	all := make([]float64, 0, len(px)*len(px[0]))
	out := make([][]float64, len(px))
	for y, row := range px {
		out[y] = make([]float64, len(row))
		for x, p := range row {
			// Lightness, not luminance: dithering distributes error the eye
			// weighs, and the eye works on the gamma side.
			l := math.Pow(p.luminance(), 1/2.2)
			out[y][x] = l
			all = append(all, l)
		}
	}
	sort.Float64s(all)
	lo := all[len(all)*2/100]
	hi := all[len(all)*98/100]
	if hi-lo < 1e-6 {
		return out
	}
	for y, row := range out {
		for x, l := range row {
			out[y][x] = math.Max(0, math.Min(1, (l-lo)/(hi-lo)))
		}
	}
	return out
}

// dither thresholds lightness at one half, diffusing each cell's error to its
// unvisited neighbours with the Floyd-Steinberg weights.
func dither(light [][]float64) [][]bool {
	h := len(light)
	w := len(light[0])
	buf := make([][]float64, h)
	for y, row := range light {
		buf[y] = append([]float64(nil), row...)
	}
	out := make([][]bool, h)
	for y := range h {
		out[y] = make([]bool, w)
		for x := range w {
			old := buf[y][x]
			on := old >= 0.5
			out[y][x] = on
			var target float64
			if on {
				target = 1
			}
			err := old - target
			if x+1 < w {
				buf[y][x+1] += err * 7 / 16
			}
			if y+1 < h {
				if x > 0 {
					buf[y+1][x-1] += err * 3 / 16
				}
				buf[y+1][x] += err * 5 / 16
				if x+1 < w {
					buf[y+1][x+1] += err * 1 / 16
				}
			}
		}
	}
	return out
}
