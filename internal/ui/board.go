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
	// Shallow links accumulate as edge connectivity per cell and become glyphs
	// only once every link has contributed, so a cell shared by several links
	// resolves to the junction that joins all of them rather than to whichever
	// link happened to be drawn last.
	bits   []linkBits
	bitIDs []styleID
}

func newCanvas(w, h int) *canvas {
	cv := &canvas{
		w: w, h: h,
		runes:  make([]rune, w*h),
		ids:    make([]styleID, w*h),
		bits:   make([]linkBits, w*h),
		bitIDs: make([]styleID, w*h),
	}
	for i := range cv.runes {
		cv.runes[i] = ' '
	}
	return cv
}

// linkBits records which of a cell's four edges a link line reaches.
type linkBits uint8

const (
	linkN linkBits = 1 << iota
	linkE
	linkS
	linkW
)

// junction gives the box-drawing glyph joining exactly the edges in a set. A
// cell with one edge is a stub, which happens where a line runs off the clipped
// edge of the canvas.
var junction = [16]rune{
	linkN:                         '╵',
	linkE:                         '╶',
	linkS:                         '╷',
	linkW:                         '╴',
	linkN | linkS:                 '│',
	linkE | linkW:                 '─',
	linkN | linkE:                 '╰',
	linkN | linkW:                 '╯',
	linkS | linkE:                 '╭',
	linkS | linkW:                 '╮',
	linkN | linkE | linkS:         '├',
	linkN | linkW | linkS:         '┤',
	linkE | linkS | linkW:         '┬',
	linkN | linkE | linkW:         '┴',
	linkN | linkE | linkS | linkW: '┼',
}

// isLinkGlyph reports whether a rune is part of a drawn link.
func isLinkGlyph(r rune) bool {
	if r == glyphRise || r == glyphFall || r == glyphCross {
		return true
	}
	for _, j := range junction {
		if j != 0 && r == j {
			return true
		}
	}
	return false
}

// connect adds edges to a cell's link connectivity.
func (cv *canvas) connect(x, y int, b linkBits, id styleID) {
	if !cv.in(x, y) {
		return
	}
	i := y*cv.w + x
	if cv.bits[i] == 0 {
		cv.bitIDs[i] = id
	}
	cv.bits[i] |= b
}

// resolveLinks turns accumulated connectivity into glyphs. A peg keeps its cell:
// it is the fact the board is about, and a link that passes it is legible from
// the cells either side. A cell already holding a diagonal becomes a crossing.
func (cv *canvas) resolveLinks() {
	for i, b := range cv.bits {
		if b == 0 {
			continue
		}
		x, y := i%cv.w, i/cv.w
		switch cv.runes[i] {
		case glyphPegVertical, glyphPegHorizontal, glyphPegVerticalLast, glyphPegHorizontalLast:
		case glyphRise, glyphFall, glyphCross:
			cv.set(x, y, glyphCross, cv.bitIDs[i])
		default:
			cv.set(x, y, junction[b], cv.bitIDs[i])
		}
	}
}

func (cv *canvas) in(x, y int) bool { return x >= 0 && x < cv.w && y >= 0 && y < cv.h }

func (cv *canvas) set(x, y int, r rune, id styleID) {
	if !cv.in(x, y) {
		return
	}
	cv.runes[y*cv.w+x] = r
	cv.ids[y*cv.w+x] = id
}

// hasPeg reports whether a peg occupies a cell.
func (cv *canvas) hasPeg(x, y int) bool {
	if !cv.in(x, y) {
		return false
	}
	switch cv.runes[y*cv.w+x] {
	case glyphPegVertical, glyphPegHorizontal, glyphPegVerticalLast, glyphPegHorizontalLast:
		return true
	}
	return false
}

// mergeDiagonal writes a steep link's diagonal, marking a cell already holding
// a different stroke as a crossing.
func (cv *canvas) mergeDiagonal(x, y int, r rune, id styleID) {
	if !cv.in(x, y) {
		return
	}
	switch old := cv.runes[y*cv.w+x]; old {
	case glyphPegVertical, glyphPegHorizontal, glyphPegVerticalLast, glyphPegHorizontalLast:
		return
	case ' ', glyphHole, r:
	default:
		r = glyphCross
	}
	cv.set(x, y, r, id)
}

// drawLink rasterises one link. A shallow link contributes edge connectivity
// rather than glyphs, so nothing it draws is visible until resolveLinks runs.
//
// A steep link (column ±1, row ±2) is an exact diagonal in screen cells under
// both scales, so a run of one glyph draws it.
//
// A shallow link (column ±2, row ±1) is four screen columns per row and has no
// diagonal glyph. It is drawn as a connected polyline: horizontal runs along
// the rows of holes it passes between, joined by single-column steps where it
// crosses from one row to the next. Each cell records the edges the line
// reaches rather than a finished glyph, so the corners and the junctions where
// several links share a cell come out of one table instead of a special case
// per collision.
//
// A step is placed at the column where the line truly crosses the row
// boundary, except that it is never placed in a column of holes: there the two
// candidate cells both belong to holes and a corner in each would cost two hole
// dots instead of one. The step moves to the neighbouring column on whichever
// side leaves the horizontal run passing an empty hole rather than a peg, so a
// peg beside the crossing cannot break the line.
func (sc Scale) drawLink(cv *canvas, l game.Link, id styleID) {
	from, to := l.Ends()
	x1, y1 := sc.holeX(from.Col), sc.holeY(from.Row)
	x2, y2 := sc.holeX(to.Col), sc.holeY(to.Row)
	dx, dy := x2-x1, y2-y1
	adx, ady := abs(dx), abs(dy)
	sx, sy := sign(dx), sign(dy)

	if adx == ady {
		g := glyphRise
		if (dx > 0) == (dy > 0) {
			g = glyphFall
		}
		for i := 1; i < adx; i++ {
			cv.mergeDiagonal(x1+sx*i, y1+sy*i, g, id)
		}
		return
	}

	// Edges named for the direction of travel, not the screen.
	back, fwd := linkW, linkE
	if sx < 0 {
		back, fwd = linkE, linkW
	}
	near, far := linkS, linkN
	if sy < 0 {
		near, far = linkN, linkS
	}

	r := y1
	off := 1
	for k := range ady {
		t := sc.stepColumn(cv, x1, r, sx, sy, k, adx, ady)
		for ; off < t; off++ {
			cv.connect(x1+sx*off, r, back|fwd, id)
		}
		cv.connect(x1+sx*t, r, back|near, id)
		cv.connect(x1+sx*t, r+sy, far|fwd, id)
		r += sy
		off = t + 1
	}
	for ; off < adx; off++ {
		cv.connect(x1+sx*off, r, back|fwd, id)
	}
}

// stepColumn returns the column offset, measured from the link's first end, at
// which the line crosses from row r to the next one.
func (sc Scale) stepColumn(cv *canvas, x1, r, sx, sy, k, adx, ady int) int {
	// Where the line genuinely crosses the boundary.
	t := int(roundHalfAway((float64(k) + 0.5) * float64(adx) / float64(ady)))
	if t%sc.colStep != 0 {
		return t
	}
	// A column of holes. Stepping here would put a corner in both of its cells
	// and cost two dots, so step one column earlier or later instead. Later
	// leaves the run passing the hole in row r; earlier leaves it passing the
	// hole in the row the line moves to. Prefer the one that is not a peg.
	late, early := t+1, t-1
	if late >= adx {
		late = early
	}
	if early < 1 {
		early = late
	}
	// Stepping late leaves the horizontal run passing the hole in row r;
	// stepping early leaves it passing the hole in the row the line moves to.
	// A run may cross an empty hole but not a peg, and a step may not land on a
	// diagonal already drawn, so prefer the side that costs neither.
	lateClear := !cv.hasPeg(x1+sx*t, r) && cv.stepFree(x1+sx*late, r, sy)
	earlyClear := !cv.hasPeg(x1+sx*t, r+sy) && cv.stepFree(x1+sx*early, r, sy)
	if !lateClear && earlyClear {
		return early
	}
	return late
}

// stepFree reports whether both cells a step would occupy are available.
func (cv *canvas) stepFree(x, r, sy int) bool {
	for _, y := range [2]int{r, r + sy} {
		if !cv.in(x, y) {
			continue
		}
		if isLinkGlyph(cv.runes[y*cv.w+x]) || cv.hasPeg(x, y) {
			return false
		}
	}
	return true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
	// LastMove marks the peg just played, so it can be found on a large board
	// without reading the coordinate off the panel. ShowLastMove turns it on.
	LastMove     game.Point
	ShowLastMove bool
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

	// Holes and pegs are laid down before the links, so a link stroke can see
	// the pegs it must not cover and may take the dot of an empty hole it
	// legitimately crosses.
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if !g.Exists(p) {
				continue
			}
			x, y := bv.Scale.holeX(col), bv.Scale.holeY(row)
			last := bv.ShowLastMove && p == bv.LastMove
			switch g.At(p) {
			case game.Vertical:
				if last {
					cv.set(x, y, glyphPegVerticalLast, styLastMove)
				} else {
					cv.set(x, y, glyphPegVertical, styPegVertical)
				}
			case game.Horizontal:
				if last {
					cv.set(x, y, glyphPegHorizontalLast, styLastMove)
				} else {
					cv.set(x, y, glyphPegHorizontal, styPegHorizontal)
				}
			default:
				cv.set(x, y, glyphHole, styHole)
			}
		}
	}

	// Steep links are exact diagonals with no freedom in where they go, so they
	// are drawn first; a shallow link chooses which column it steps in and can
	// then avoid a cell a diagonal has already taken.
	for _, steep := range [2]bool{true, false} {
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
					if dCol, _ := d.Offset(); (abs(dCol) == 1) != steep {
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
	}
	cv.resolveLinks()

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
