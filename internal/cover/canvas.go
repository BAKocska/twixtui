package cover

import "strings"

// rgb is a colour. The zero value is black, so cells carry explicit flags for
// "no colour here" rather than treating black as absence: the homage paints
// with near-black on purpose.
type rgb struct {
	r, g, b uint8
}

// cell is one character cell: a rune and up to two colours. A cell with no
// colours renders as its rune alone, which is how the same canvas serves the
// monochrome depth without a second composition.
type cell struct {
	r     rune
	fg    rgb
	bg    rgb
	hasFG bool
	hasBG bool
}

// canvas is a fixed grid of cells the homage is composed onto. It exists so
// composition can think in "put this rune here, in this colour" while the
// ANSI, the quantisation and the trailing-space trimming happen once, at
// emission.
type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([]cell, w*h)}
	for i := range c.cells {
		c.cells[i].r = ' '
	}
	return c
}

// set places a rune. Out-of-range coordinates are ignored rather than checked
// by every caller: composition code positions shapes relative to a layout, and
// a shape half outside the box should lose its overhang, not crash.
func (c *canvas) set(x, y int, r rune, fg *rgb, bg *rgb) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	cl := &c.cells[y*c.w+x]
	cl.r = r
	if fg != nil {
		cl.fg, cl.hasFG = *fg, true
	}
	if bg != nil {
		cl.bg, cl.hasBG = *bg, true
	}
}

// fill paints a rectangle of background colour, keeping whatever runes are
// already there.
func (c *canvas) fill(x0, y0, x1, y1 int, bg rgb) {
	for y := y0; y < y1; y++ {
		if y < 0 || y >= c.h {
			continue
		}
		for x := x0; x < x1; x++ {
			if x < 0 || x >= c.w {
				continue
			}
			cl := &c.cells[y*c.w+x]
			cl.bg, cl.hasBG = bg, true
		}
	}
}

// text writes a string one rune per cell. Every rune this package draws with
// occupies one cell — block elements, box drawing, ASCII — so the mapping is
// the identity and nothing here measures display width.
func (c *canvas) text(x, y int, s string, fg *rgb, bg *rgb) {
	for _, r := range s {
		if r != ' ' {
			c.set(x, y, r, fg, bg)
		}
		x++
	}
}

// label writes a line of text that owns its cell row: unlike text, spaces
// overwrite what is beneath them, so lettering laid over the board keeps its
// own word gaps instead of letting peg holes poke through them.
func (c *canvas) label(x, y int, s string, fg *rgb) {
	for _, r := range s {
		c.set(x, y, r, fg, nil)
		x++
	}
}

// sprite draws a multi-line shape whose spaces are transparent, which is what
// lets a peg stand in front of the board without carrying a rectangle of sky
// around with it.
func (c *canvas) sprite(x, y int, rows []string, fg *rgb) {
	for dy, row := range rows {
		c.text(x, y+dy, row, fg, nil)
	}
}

// lines emits the canvas for a depth. Rows are trimmed on the right: a cell
// with no background and a blank rune is padding, and the contract promises
// lines no wider than the box, not lines exactly as wide.
func (c *canvas) lines(depth Depth) []string {
	out := make([]string, 0, c.h)
	for y := range c.h {
		row := c.cells[y*c.w : (y+1)*c.w]
		end := len(row)
		for end > 0 && row[end-1].r == ' ' && !(depth != DepthMono && row[end-1].hasBG) {
			end--
		}
		var b strings.Builder
		var st sgrState
		for x := range end {
			cl := row[x]
			if depth != DepthMono {
				st.apply(&b, cl, depth)
			}
			b.WriteRune(cl.r)
		}
		st.reset(&b)
		out = append(out, b.String())
	}
	return out
}

// sgrState tracks the colours currently in force on a line so that a run of
// same-coloured cells costs one escape, not one per cell. A cover at 120x60
// is drawn on every menu frame; an order of magnitude fewer bytes is worth
// twenty lines of bookkeeping.
type sgrState struct {
	fg, bg       rgb
	hasFG, hasBG bool
	dirty        bool
}

func (s *sgrState) apply(b *strings.Builder, cl cell, depth Depth) {
	if cl.hasFG != s.hasFG || (cl.hasFG && cl.fg != s.fg) ||
		cl.hasBG != s.hasBG || (cl.hasBG && cl.bg != s.bg) {
		// A cell dropping a colour needs a reset first; SGR has "default
		// foreground" (39) and "default background" (49), but one reset and
		// restating the survivor is simpler than tracking which half died.
		if (s.hasFG && !cl.hasFG) || (s.hasBG && !cl.hasBG) {
			b.WriteString("\x1b[0m")
			s.hasFG, s.hasBG = false, false
		}
		if cl.hasFG && (!s.hasFG || cl.fg != s.fg) {
			b.WriteString(sgrColour(cl.fg, depth, false))
		}
		if cl.hasBG && (!s.hasBG || cl.bg != s.bg) {
			b.WriteString(sgrColour(cl.bg, depth, true))
		}
		s.fg, s.bg, s.hasFG, s.hasBG = cl.fg, cl.bg, cl.hasFG, cl.hasBG
		s.dirty = s.hasFG || s.hasBG
	}
}

func (s *sgrState) reset(b *strings.Builder) {
	if s.dirty {
		b.WriteString("\x1b[0m")
		s.dirty = false
		s.hasFG, s.hasBG = false, false
	}
}

// sgrColour renders one colour as an escape: exact at 24-bit, quantised to
// the xterm palette at 256.
func sgrColour(c rgb, depth Depth, background bool) string {
	plane := "38"
	if background {
		plane = "48"
	}
	if depth == Depth256 {
		idx, _ := xtermQuantise(c)
		return "\x1b[" + plane + ";5;" + itoa(int(idx)) + "m"
	}
	return "\x1b[" + plane + ";2;" + itoa(int(c.r)) + ";" + itoa(int(c.g)) + ";" + itoa(int(c.b)) + "m"
}

// itoa is strconv.Itoa for the only integers an SGR ever holds. It keeps the
// hot emission path free of the strconv small-int check chain; colours are
// always 0..255.
func itoa(n int) string {
	if n < 10 {
		return string([]byte{byte('0' + n)})
	}
	var buf [3]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
