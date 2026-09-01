// Command gen regenerates the reduced cover artwork in assets/ from a
// full-resolution source. The shipped asset is deliberately small — the
// cover is only ever projected into character cells, so detail beyond about
// twice the densest cell grid is bytes the binary carries for nothing — and
// this command is how that reduction is reproduced rather than being a
// mystery blob:
//
//	go run ./assets/gen -in /path/to/poster.png -out assets/cover.png
//
// The output format follows the output extension: .png for flat poster art,
// where JPEG would smear the hard edges, .jpg for photographs. The reduction
// is an area average in linear light, the same filtering the renderer itself
// uses, so the shipped asset looks the way the renderer assumes its input
// looks.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"os"
	"slices"
	"strings"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("gen: ")
	in := flag.String("in", "", "full-resolution source image (JPEG or PNG)")
	out := flag.String("out", "assets/cover.png", "reduced image to write; .png or .jpg")
	height := flag.Int("height", 320, "target height in pixels")
	quality := flag.Int("quality", 82, "JPEG quality, for a .jpg output")
	colors := flag.Int("colors", 0, "quantise a .png output to this many colours; 0 keeps true colour")
	flag.Parse()
	if *in == "" {
		log.Fatal("-in is required")
	}

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		log.Fatalf("decoding %s: %v", *in, err)
	}

	b := src.Bounds()
	th := *height
	tw := b.Dx() * th / b.Dy()
	var dst image.Image = reduce(src, tw, th)
	if *colors > 0 && strings.HasSuffix(*out, ".png") {
		dst = palettise(dst.(*image.RGBA), *colors)
	}

	o, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if strings.HasSuffix(*out, ".png") {
		err = png.Encode(o, dst)
	} else {
		err = jpeg.Encode(o, dst, &jpeg.Options{Quality: *quality})
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := o.Close(); err != nil {
		log.Fatal(err)
	}
	st, err := os.Stat(*out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %dx%d, %d bytes\n", *out, tw, th, st.Size())
}

// palettise snaps the image onto its own most representative colours, found
// by median cut, and returns it paletted. For flat poster art this is most
// of the size win: the area average blends every edge into thousands of
// one-off colours that defeat PNG's palette mode, while snapping them back
// costs nothing the terminal projection would ever have shown. No dithering,
// deliberately — the art is flat and should stay flat.
func palettise(img *image.RGBA, k int) *image.Paletted {
	b := img.Bounds()
	all := make([]color.RGBA, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			all = append(all, img.RGBAAt(x, y))
		}
	}

	// Median cut: repeatedly split the box with the widest channel spread at
	// its median until there are k boxes, then use each box's mean.
	boxes := [][]color.RGBA{all}
	for len(boxes) < k {
		wi, wr := -1, -1
		var wch func(color.RGBA) int
		for i, box := range boxes {
			if len(box) < 2 {
				continue
			}
			for _, ch := range []func(color.RGBA) int{
				func(c color.RGBA) int { return int(c.R) },
				func(c color.RGBA) int { return int(c.G) },
				func(c color.RGBA) int { return int(c.B) },
			} {
				lo, hi := 255, 0
				for _, c := range box {
					v := ch(c)
					lo, hi = min(lo, v), max(hi, v)
				}
				if hi-lo > wr {
					wi, wr, wch = i, hi-lo, ch
				}
			}
		}
		if wi < 0 || wr == 0 {
			break
		}
		box := boxes[wi]
		slices.SortFunc(box, func(a, b color.RGBA) int { return wch(a) - wch(b) })
		mid := len(box) / 2
		boxes[wi] = box[:mid]
		boxes = append(boxes, box[mid:])
	}

	pal := make(color.Palette, 0, len(boxes))
	for _, box := range boxes {
		var r, g, bl int
		for _, c := range box {
			r += int(c.R)
			g += int(c.G)
			bl += int(c.B)
		}
		n := len(box)
		pal = append(pal, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255})
	}

	out := image.NewPaletted(b, pal)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.SetColorIndex(x, y, uint8(pal.Index(img.RGBAAt(x, y))))
		}
	}
	return out
}

// reduce area-averages src down to tw by th in linear light. Averaging the
// gamma-encoded bytes directly would darken every edge, and the box lid is
// mostly edges by the time it is this small.
func reduce(src image.Image, tw, th int) *image.RGBA {
	var toLinear [256]float64
	for i := range toLinear {
		v := float64(i) / 255
		if v <= 0.04045 {
			toLinear[i] = v / 12.92
		} else {
			toLinear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	toByte := func(v float64) uint8 {
		if v <= 0 {
			return 0
		}
		if v >= 1 {
			return 255
		}
		if v <= 0.0031308 {
			return uint8(v*12.92*255 + 0.5)
		}
		return uint8((1.055*math.Pow(v, 1/2.4)-0.055)*255 + 0.5)
	}

	b := src.Bounds()
	iw, ih := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	for ty := range th {
		y0, y1 := b.Min.Y+ty*ih/th, b.Min.Y+(ty+1)*ih/th
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for tx := range tw {
			x0, x1 := b.Min.X+tx*iw/tw, b.Min.X+(tx+1)*iw/tw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl float64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					pr, pg, pb, _ := src.At(x, y).RGBA()
					r += toLinear[pr>>8]
					g += toLinear[pg>>8]
					bl += toLinear[pb>>8]
				}
			}
			n := float64((y1 - y0) * (x1 - x0))
			dst.SetRGBA(tx, ty, color.RGBA{toByte(r / n), toByte(g / n), toByte(bl / n), 255})
		}
	}
	return dst
}
