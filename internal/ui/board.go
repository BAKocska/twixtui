// Package ui is the reusable terminal view layer for TwixT: a board renderer
// with two drawing scales and a scrolling viewport, a layout engine that fits
// board and information panel into any terminal size, and a data-driven keymap.
// Game screens are built on top of it; the package itself owns no game flow
// beyond a demonstration model.
package ui

import (
	"strconv"
	"strings"

	"github.com/BAKocska/twixtui/internal/game"
)

// Scale is a board drawing density: how many screen columns and rows separate
// neighbouring holes.
//
// The knight-move link geometry constrains the choice hard. With colStep=2c
// and rowStep=c, a steep link (column ±1, row ±2) spans 2c×2c screen cells —
// an exact diagonal — and a shallow link (column ±2, row ±1) runs at slope
// 1/4, which the renderer draws as a ramp of horizontal scan-line glyphs.
// Both provided scales keep that shape, so one rasteriser serves both, and
// both match the roughly 1:2 width:height aspect of terminal cells, so the
// board looks square.
type Scale struct {
	name    string
	colStep int
	rowStep int
}

// The two supported scales. Compact fits a standard 24×24 board into roughly
// 52×26 cells; Detail spreads it over roughly 98×49 and draws every link with
// three-cell strokes.
var (
	Compact = Scale{name: "compact", colStep: 2, rowStep: 1}
	Detail  = Scale{name: "detail", colStep: 4, rowStep: 2}
)

// String returns the scale's name.
func (sc Scale) String() string { return sc.name }

// CanvasSize returns the size of the unclipped board canvas for an n-hole
// side, excluding coordinate labels. One margin column on each side leaves
// room for cursor brackets around edge holes.
func (sc Scale) CanvasSize(n int) (w, h int) {
	return sc.colStep*(n-1) + 3, sc.rowStep*(n-1) + 1
}

// holeX and holeY map a hole to its canvas cell.
func (sc Scale) holeX(col int) int { return 1 + sc.colStep*col }
func (sc Scale) holeY(row int) int { return sc.rowStep * row }

// gutterWidth is the width of the row-number gutter for an n-hole board.
func gutterWidth(n int) int { return len(strconv.Itoa(n)) + 1 }

// BlockSize returns the full size of the rendered board block — canvas plus
// gutter and letters row — before any viewport clipping.
func (sc Scale) BlockSize(n int) (w, h int) {
	cw, ch := sc.CanvasSize(n)
	return gutterWidth(n) + cw, ch + 1
}

// canvas is a grid of glyphs with per-cell style tags.
type canvas struct {
	w, h  int
	runes []rune
	ids   []styleID
}

func newCanvas(w, h int) *canvas {
	cv := &canvas{w: w, h: h, runes: make([]rune, w*h), ids: make([]styleID, w*h)}
	for i := range cv.runes {
		cv.runes[i] = ' '
	}
	return cv
}

func (cv *canvas) in(x, y int) bool { return x >= 0 && x < cv.w && y >= 0 && y < cv.h }

func (cv *canvas) set(x, y int, r rune, id styleID) {
	if !cv.in(x, y) {
		return
	}
	cv.runes[y*cv.w+x] = r
	cv.ids[y*cv.w+x] = id
}

// isShallowStroke reports whether r belongs to the shallow-link glyph family.
func isShallowStroke(r rune) bool {
	switch r {
	case glyphScanHigh, glyphScanUpper, glyphScanMid, glyphScanLower, glyphScanLow, glyphPair:
		return true
	}
	return false
}

// mergeLink writes a link glyph, combining with whatever link stroke already
// occupies the cell. Two shallow strokes in one cell come from a peg with a
// rising and a falling link on the same side and render as a double stroke;
// any other combination is a geometric crossing.
func (cv *canvas) mergeLink(x, y int, r rune, id styleID) {
	if !cv.in(x, y) {
		return
	}
	old := cv.runes[y*cv.w+x]
	switch {
	case old == ' ' || old == r:
		// Same stroke from the other endpoint's walk keeps its glyph.
	case isShallowStroke(old) && isShallowStroke(r):
		r = glyphPair
	default:
		r = glyphCross
	}
	cv.set(x, y, r, id)
}

// scanGlyph picks the shallow-link stroke height for a line passing frac cells
// below the centre of its row (negative frac is above centre).
func scanGlyph(frac float64) rune {
	switch {
	case frac <= -0.375:
		return glyphScanHigh
	case frac <= -0.125:
		return glyphScanUpper
	case frac < 0.125:
		return glyphScanMid
	case frac < 0.375:
		return glyphScanLower
	default:
		return glyphScanLow
	}
}

// drawLink rasterises one link. Steep links are exact diagonals under both
// scales; shallow links run at slope 1/4 and are drawn column by column,
// skipping hole cells, with the stroke height following the exact line.
func (sc Scale) drawLink(cv *canvas, l game.Link, id styleID) {
	from, to := l.Ends()
	x1, y1 := sc.holeX(from.Col), sc.holeY(from.Row)
	x2, y2 := sc.holeX(to.Col), sc.holeY(to.Row)
	dx, dy := x2-x1, y2-y1
	adx := dx
	if adx < 0 {
		adx = -adx
	}
	ady := dy
	if ady < 0 {
		ady = -ady
	}
	if adx == ady {
		g := glyphRise
		if (dx > 0) == (dy > 0) {
			g = glyphFall
		}
		sx, sy := sign(dx), sign(dy)
		for i := 1; i < adx; i++ {
			cv.mergeLink(x1+sx*i, y1+sy*i, g, id)
		}
		return
	}
	slope := float64(dy) / float64(dx)
	step := sign(dx)
	for i := 1; i < adx; i++ {
		x := x1 + step*i
		yf := float64(y1) + slope*float64(x-x1)
		y := int(roundHalfAway(yf))
		// Never draw into a hole cell: on the compact scale the line passes
		// exactly between two vertically neighbouring holes.
		if (x-1)%sc.colStep == 0 && y%sc.rowStep == 0 {
			continue
		}
		cv.mergeLink(x, y, scanGlyph(yf-float64(y)), id)
	}
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// roundHalfAway rounds to the nearest integer, halves away from zero.
func roundHalfAway(v float64) float64 {
	if v < 0 {
		return float64(int(v - 0.5))
	}
	return float64(int(v + 0.5))
}

// BoardView renders a game onto a scrolling viewport. The zero value is
// usable; set Scale before rendering. Viewport position is internal state that
// follows the cursor and survives resizes.
type BoardView struct {
	// Scale selects the drawing density. The layout engine picks it per frame.
	Scale Scale
	// Cursor is the hole the cursor sits on; ShowCursor turns it on.
	Cursor     game.Point
	ShowCursor bool
	// Highlights marks holes to call out (hints, tutorial steps, the staged
	// peg). They render as round brackets around the hole.
	Highlights []game.Point
	// Digits overlays link-direction digits on target holes in link mode.
	Digits map[game.Point]rune

	top, left int
}

// Viewport returns the current viewport origin in canvas cells, for tests and
// debugging.
func (bv *BoardView) Viewport() (top, left int) { return bv.top, bv.left }

// Render draws the game into at most availW×availH cells, including the
// coordinate labels, scrolling as needed to keep the cursor visible. Lines are
// styled with st and right-trimmed; every line's display width is at most
// availW and at most availH lines are returned.
func (bv *BoardView) Render(g *game.Game, st *Styles, availW, availH int) []string {
	n := g.Size()
	cw, ch := bv.Scale.CanvasSize(n)
	gutter := gutterWidth(n)
	vw := min(availW-gutter, cw)
	vh := min(availH-1, ch)
	if vw < 1 || vh < 1 {
		return nil
	}
	bv.scrollIntoView(vw, vh, cw, ch)

	cv := bv.paint(g)

	lines := make([]string, 0, vh+1)
	lines = append(lines, bv.lettersRow(n, gutter, vw, cw, st))
	for y := bv.top; y < bv.top+vh; y++ {
		lines = append(lines, bv.boardRow(cv, y, n, gutter, vw, vh, ch, st))
	}
	return lines
}

// paint draws the full board canvas: links first, then holes and pegs, then
// overlays, so overlays always win.
func (bv *BoardView) paint(g *game.Game) *canvas {
	n := g.Size()
	cw, ch := bv.Scale.CanvasSize(n)
	cv := newCanvas(cw, ch)

	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			mask := g.LinkMask(p)
			if mask == 0 {
				continue
			}
			for d := game.Dir(0); d < game.NumDirs; d++ {
				if !d.IsCanonical() || mask&(1<<d) == 0 {
					continue
				}
				l, _ := game.NewLink(p, p.Add(d))
				id := styLinkVertical
				if g.LinkOwner(l) == game.Horizontal {
					id = styLinkHorizontal
				}
				bv.Scale.drawLink(cv, l, id)
			}
		}
	}

	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if !g.Exists(p) {
				continue
			}
			x, y := bv.Scale.holeX(col), bv.Scale.holeY(row)
			switch g.At(p) {
			case game.Vertical:
				cv.set(x, y, glyphPegVertical, styPegVertical)
			case game.Horizontal:
				cv.set(x, y, glyphPegHorizontal, styPegHorizontal)
			default:
				cv.set(x, y, glyphHole, styHole)
			}
		}
	}

	for _, p := range bv.Highlights {
		x, y := bv.Scale.holeX(p.Col), bv.Scale.holeY(p.Row)
		cv.set(x-1, y, glyphMarkLeft, styHighlight)
		cv.set(x+1, y, glyphMarkRight, styHighlight)
	}
	for p, digit := range bv.Digits {
		cv.set(bv.Scale.holeX(p.Col), bv.Scale.holeY(p.Row), digit, styLinkDigit)
	}
	if bv.ShowCursor {
		x, y := bv.Scale.holeX(bv.Cursor.Col), bv.Scale.holeY(bv.Cursor.Row)
		cv.set(x-1, y, glyphCursorLeft, styCursor)
		cv.set(x+1, y, glyphCursorRight, styCursor)
	}
	return cv
}

// scrollIntoView clamps the viewport and scrolls it minimally so the cursor
// cell and its brackets are visible.
func (bv *BoardView) scrollIntoView(vw, vh, cw, ch int) {
	bv.left = clamp(bv.left, 0, cw-vw)
	bv.top = clamp(bv.top, 0, ch-vh)
	if !bv.ShowCursor {
		return
	}
	cx := bv.Scale.holeX(bv.Cursor.Col)
	cy := bv.Scale.holeY(bv.Cursor.Row)
	if cx+2 > bv.left+vw {
		bv.left = cx + 2 - vw
	}
	if cx-1 < bv.left {
		bv.left = cx - 1
	}
	if cy+1 > bv.top+vh {
		bv.top = cy + 1 - vh
	}
	if cy < bv.top {
		bv.top = cy
	}
	bv.left = clamp(bv.left, 0, cw-vw)
	bv.top = clamp(bv.top, 0, ch-vh)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// lettersRow renders the column-letter header for the visible columns, with
// horizontal overflow arrows when the board extends beyond the viewport.
func (bv *BoardView) lettersRow(n, gutter, vw, cw int, st *Styles) string {
	row := make([]rune, gutter+vw)
	for i := range row {
		row[i] = ' '
	}
	for col := range n {
		x := bv.Scale.holeX(col)
		for j, r := range game.ColumnName(col) {
			pos := x - bv.left + j
			if pos >= 0 && pos < vw {
				row[gutter+pos] = r
			}
		}
	}
	if bv.left > 0 {
		row[gutter] = glyphLeft
	}
	if bv.left+vw < cw {
		row[gutter+vw-1] = glyphRight
	}
	return st.apply(styLabel, strings.TrimRight(string(row), " "))
}

// boardRow renders one visible canvas row with its gutter label. The topmost
// and bottommost visible rows show overflow arrows in the gutter when rows are
// hidden beyond them.
func (bv *BoardView) boardRow(cv *canvas, y, n, gutter, vw, vh, ch int, st *Styles) string {
	label := strings.Repeat(" ", gutter)
	switch {
	case y == bv.top && bv.top > 0:
		label = pad(string(glyphUp), gutter)
	case y == bv.top+vh-1 && bv.top+vh < ch:
		label = pad(string(glyphDown), gutter)
	case y%bv.Scale.rowStep == 0:
		label = pad(strconv.Itoa(y/bv.Scale.rowStep+1), gutter)
	}

	// Trim trailing blanks before emitting styled runs.
	end := bv.left + vw
	for end > bv.left && cv.runes[y*cv.w+end-1] == ' ' {
		end--
	}

	var b strings.Builder
	b.WriteString(st.apply(styLabel, label))
	runStart := bv.left
	for x := bv.left; x <= end; x++ {
		if x < end && cv.ids[y*cv.w+x] == cv.ids[y*cv.w+runStart] {
			continue
		}
		b.WriteString(st.apply(cv.ids[y*cv.w+runStart], string(cv.runes[y*cv.w+runStart:y*cv.w+x])))
		runStart = x
	}
	return b.String()
}

// pad right-aligns s in width cells with a trailing space, the gutter shape.
// Widths count runes; every glyph the gutter shows is single-cell.
func pad(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return strings.Repeat(" ", width-1-len(r)) + s + " "
}
