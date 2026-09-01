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
	// bitLink names the first link to reach a cell, one-based into links, and
	// links holds every link the accumulation has seen. A link arriving in a
	// cell has to know which links it is meeting there: two that share a peg are
	// one connected line and belong in a junction, and a pair that shares none
	// must not be drawn as one.
	//
	// Recording only the first arrival was not enough. A third link was compared
	// with the first and never with the second, so three links competing for one
	// cell could be drawn as a junction on the strength of one pair while another
	// pair met nowhere. contenders therefore holds every link that reached a
	// cell, and only for the cells that more than one link reached, which is a
	// handful per frame rather than a slot on every cell.
	bitLink    []uint16
	contenders map[int32][]uint16
	links      []game.Link
}

func newCanvas(w, h int) *canvas {
	cv := &canvas{
		w: w, h: h,
		runes:   make([]rune, w*h),
		ids:     make([]styleID, w*h),
		bits:    make([]linkBits, w*h),
		bitIDs:  make([]styleID, w*h),
		bitLink: make([]uint16, w*h),
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
	// linkUnjoined records that among the links which reached a cell there is a
	// pair that meets at no peg. Two links that share a peg are one connected
	// line and a junction is the truth about them; a pair that shares none is
	// either crossing or merely passing, and in both cases a junction would
	// assert a connection the game does not have. It is not an edge, so it is
	// masked off before the junction table is indexed.
	linkUnjoined

	linkEdges = linkN | linkE | linkS | linkW
)

// edges returns a cell's connectivity without the crossing flag.
func (b linkBits) edges() linkBits { return b & linkEdges }

// joins reports whether a cell's edges form a junction: three or more of them,
// which is the shape that claims the lines in the cell meet. Every call to
// connect contributes exactly two edges, so where two links share a cell a
// third edge appears exactly when the merged shape is one neither of them drew,
// which is also exactly when the picture starts claiming they join.
func (b linkBits) joins() bool {
	n := 0
	for e := b.edges(); e != 0; e &= e - 1 {
		n++
	}
	return n >= 3
}

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

// connect adds edges to a cell's link connectivity. The link is named by ref so
// that a cell two links reach can record whether they cross: a junction there
// would say the two lines meet, which for a crossing is the opposite of what
// happened. game.LinksCross is the only authority on that; the edges alone
// cannot tell a crossing from two links joining at a peg they share.
func (cv *canvas) connect(x, y int, b linkBits, id styleID, ref uint16) {
	if !cv.in(x, y) {
		return
	}
	i := y*cv.w + x
	if cv.bits[i].edges() == 0 {
		cv.bitIDs[i] = id
		cv.bitLink[i] = ref
		cv.bits[i] |= b
		return
	}
	for _, prev := range cv.refsAt(i) {
		if prev != ref && !sharesEnd(cv.links[prev-1], cv.links[ref-1]) {
			cv.bits[i] |= linkUnjoined
		}
	}
	cv.addContender(i, ref)
	cv.bits[i] |= b
}

// refsAt lists every link that has reached a cell.
func (cv *canvas) refsAt(i int) []uint16 {
	if extra, ok := cv.contenders[int32(i)]; ok {
		return extra
	}
	if cv.bitLink[i] == 0 {
		return nil
	}
	return cv.bitLink[i : i+1]
}

// addContender remembers that another link reached a cell.
func (cv *canvas) addContender(i int, ref uint16) {
	if cv.bitLink[i] == ref {
		return
	}
	all := cv.contenders[int32(i)]
	if all == nil {
		all = []uint16{cv.bitLink[i]}
	}
	for _, r := range all {
		if r == ref {
			return
		}
	}
	if cv.contenders == nil {
		cv.contenders = make(map[int32][]uint16)
	}
	cv.contenders[int32(i)] = append(all, ref)
}

// addLink registers a link whose connectivity is about to be accumulated and
// returns the one-based reference the cells record it by.
func (cv *canvas) addLink(l game.Link) uint16 {
	cv.links = append(cv.links, l)
	return uint16(len(cv.links))
}

// resolveLinks turns accumulated connectivity into glyphs. A peg keeps its cell:
// it is the fact the board is about, and a link that passes it is legible from
// the cells either side.
//
// A cell two crossing links reached may not be drawn as a junction, because a
// junction is the picture of two lines meeting and a crossing is the picture of
// two lines that do not. Where the merged edges would make a junction — three
// or four edges, a shape neither link drew on its own — the crossing glyph goes
// in instead. Where they merge into a straight run the run stays: one line
// where two lines run together claims no junction, so it needs no crossing
// glyph. A cell already holding a diagonal is a crossing for the same reason.
func (cv *canvas) resolveLinks() {
	for i, b := range cv.bits {
		e := b.edges()
		if e == 0 {
			continue
		}
		x, y := i%cv.w, i/cv.w
		switch cv.runes[i] {
		case glyphPegVertical, glyphPegHorizontal, glyphPegVerticalLast, glyphPegHorizontalLast:
		case glyphRise, glyphFall, glyphCross:
			cv.set(x, y, glyphCross, cv.bitIDs[i])
		default:
			r := junction[e]
			if b&linkUnjoined != 0 && b.joins() {
				r = glyphCross
			}
			cv.set(x, y, r, cv.bitIDs[i])
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

// bracket draws an overlay's two marks either side of a hole and reports how
// many of them it could place. It writes into an empty cell only: a cell
// holding a link stroke belongs to the link, and a cell holding an earlier
// overlay's bracket belongs to that overlay.
func (cv *canvas) bracket(x, y int, left, right rune, id styleID) int {
	placed := 0
	for _, m := range [2]struct {
		x int
		r rune
	}{{x - 1, left}, {x + 1, right}} {
		if !cv.in(m.x, y) || cv.runes[y*cv.w+m.x] != ' ' {
			continue
		}
		cv.set(m.x, y, m.r, id)
		placed++
	}
	return placed
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
// dots instead of one. It also moves aside where the line would otherwise walk
// into a peg or into a cell belonging to a link this one neither meets nor
// crosses. See shallow.stepColumn.
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
	s := shallow{
		sc: sc, ref: cv.addLink(l), id: id,
		x1: x1, y1: y1, sx: sx, sy: sy, adx: adx, ady: ady,
		// Edges named for the direction of travel, not the screen.
		back: linkW, fwd: linkE, near: linkS, far: linkN,
	}
	if sx < 0 {
		s.back, s.fwd = linkE, linkW
	}
	if sy < 0 {
		s.near, s.far = linkN, linkS
	}
	s.draw(cv)
}

// shallow is one shallow link mid-rasterisation: where its polyline starts, how
// it travels, and the reference the cells it touches record it by.
type shallow struct {
	sc     Scale
	ref    uint16
	id     styleID
	x1, y1 int
	sx, sy int
	// adx and ady are the polyline's span in canvas cells: adx columns, ady
	// rows, and ady is therefore the number of steps it takes.
	adx, ady int
	// Edges named for the direction of travel: back and fwd along the run, near
	// and far across the step.
	back, fwd, near, far linkBits
}

// x maps a column offset measured from the link's first end to a canvas column.
func (s shallow) x(off int) int { return s.x1 + s.sx*off }

// crossing returns the column offset where the line genuinely leaves its k-th
// row, before any adjustment.
func (s shallow) crossing(k int) int {
	return int(roundHalfAway((float64(k) + 0.5) * float64(s.adx) / float64(s.ady)))
}

// draw walks the polyline row by row, choosing where each row hands over to the
// next and recording the edges of every cell it touches.
func (s shallow) draw(cv *canvas) {
	r, off := s.y1, 1
	for k := range s.ady {
		t := s.stepColumn(cv, k, r, off)
		for ; off < t; off++ {
			cv.connect(s.x(off), r, s.back|s.fwd, s.id, s.ref)
		}
		cv.connect(s.x(t), r, s.back|s.near, s.id, s.ref)
		cv.connect(s.x(t), r+s.sy, s.far|s.fwd, s.id, s.ref)
		r += s.sy
		off = t + 1
	}
	for ; off < s.adx; off++ {
		cv.connect(s.x(off), r, s.back|s.fwd, s.id, s.ref)
	}
}

// endpoint reports whether a cell is one of the link's own two pegs. A step at
// the very start or end of the line puts one of its two cells there, which is
// allowed: the peg keeps its own glyph, since resolveLinks leaves a peg cell
// alone, and the corner in the next row claims an edge into it exactly as a run
// arriving from the side does.
func (s shallow) endpoint(x, y int) bool {
	return (x == s.x1 && y == s.y1) || (x == s.x(s.adx) && y == s.y1+s.sy*s.ady)
}

// stepColumn returns the column offset at which the line hands over from row r
// to the next one, given that the run in row r has reached offset off.
//
// Five columns are candidates: where the line genuinely crosses, one either
// side, and a turn at each of the link's own pegs. A column of holes is never
// one of them. Otherwise the true crossing column wins unless it costs the
// picture something, and the cost is measured over the whole stretch of line
// the choice commits to rather than over the two cells of the step alone: a
// horizontal run has no freedom of its own, so a collision it would walk into
// has to be paid for here or not at all.
func (s shallow) stepColumn(cv *canvas, k, r, off int) int {
	t := s.crossing(k)
	late, early := t+1, t-1
	if late >= s.adx {
		late = early
	}
	if early < 1 {
		early = late
	}
	// Where every candidate is blocked the line has to be drawn anyway. Late is
	// the fallback in a column of holes because it leaves the longer run in the
	// row the line starts from.
	fallback := t
	if t%s.sc.colStep == 0 {
		fallback = late
	}
	// Candidates in order of preference. The last two turn the line at one of
	// its own pegs: the step's first cell then falls under the peg, which keeps
	// the glyph the peg already has and costs the dot of the hole beyond it
	// instead, so it buys a whole row of freedom for one dot. A turn at the
	// near peg is only available while the line is still in its first row, and
	// one at the far peg only in its last. Their cost is why they are ranked
	// behind the three ordinary columns rather than beside them: they are worth
	// a dot only where they undo a junction between links that do not join.
	var cands [5]struct {
		t     int
		atPeg bool
	}
	n := 3
	cands[0].t, cands[1].t, cands[2].t = t, late, early
	if k == 0 {
		cands[n] = struct {
			t     int
			atPeg bool
		}{0, true}
		n++
	}
	if k == s.ady-1 {
		cands[n] = struct {
			t     int
			atPeg bool
		}{s.adx, true}
		n++
	}
	best, bestJoins, bestCost, bestShared := -1, 0, 0, 0
	for _, c := range cands[:n] {
		// A column of holes is never a step: both its cells belong to holes and
		// a corner in each would cost two dots instead of one. A turn at a peg
		// stands in such a column by construction and is exempt, because one of
		// the two cells is the peg itself.
		if !c.atPeg && c.t%s.sc.colStep == 0 {
			continue
		}
		cost := 0
		if c.atPeg {
			cost = 1
		}
		joins, shared, blocked := s.shapeCost(cv, k, r, off, c.t)
		if blocked {
			continue
		}
		better := best < 0 || joins < bestJoins ||
			(joins == bestJoins && cost < bestCost) ||
			(joins == bestJoins && cost == bestCost && shared < bestShared)
		if better {
			best, bestJoins, bestCost, bestShared = c.t, joins, cost, shared
		}
		if bestJoins == 0 && bestCost == 0 && bestShared == 0 {
			break
		}
	}
	if best < 0 {
		return fallback
	}
	return best
}

// shapeCost measures what stepping at column offset cand would cost. It walks
// every cell the choice commits the line to: the rest of the run leading into
// the step, the step's own two cells, and the run leaving it. Where the row
// after this one steps again, the run leaving is measured up to that row's
// natural crossing column, which is where it will end unless that step moves
// too.
//
// blocked rules the column out: a peg is in the way, or a stroke already drawn
// is where the step itself would go. The two counts are cells shared with a
// stranger — a link this one neither meets at a peg nor crosses. Sharing draws
// two links as one line; sharing where the merged edges make a junction draws a
// connection between two chains that have none, which is the worse lie of the
// two and is counted apart from it.
func (s shallow) shapeCost(cv *canvas, k, r, off, cand int) (joins, shared int, blocked bool) {
	visit := func(x, y int, mine linkBits, step bool) {
		if !cv.in(x, y) || s.endpoint(x, y) {
			return
		}
		i := y*cv.w + x
		if cv.hasPeg(x, y) || (step && isLinkGlyph(cv.runes[i])) {
			blocked = true
			return
		}
		if cv.bits[i].edges() == 0 || !cv.strangerToAny(i, s.ref) {
			return
		}
		shared++
		if (cv.bits[i] | mine).joins() {
			joins++
		}
	}
	for i := off; i < cand; i++ {
		visit(s.x(i), r, s.back|s.fwd, false)
	}
	visit(s.x(cand), r, s.back|s.near, true)
	visit(s.x(cand), r+s.sy, s.far|s.fwd, true)
	last := s.adx - 1
	if k+1 < s.ady {
		last = s.crossing(k+1) - 1
	}
	for i := cand + 1; i <= last; i++ {
		visit(s.x(i), r+s.sy, s.back|s.fwd, false)
	}
	return joins, shared, blocked
}

// strangerToAny reports whether a link would be misrepresented by joining any
// of the links already in a cell. Asking about the first arrival alone let a
// third link route into a cell it had no business sharing.
func (cv *canvas) strangerToAny(i int, ref uint16) bool {
	for _, prev := range cv.refsAt(i) {
		if cv.strangers(prev, ref) {
			return true
		}
	}
	return false
}

// strangers reports whether two links would be misrepresented by sharing a
// cell: they meet at no peg, so a junction would assert a connection the game
// does not have, and game.LinksCross says they do not cross, so the crossing
// glyph would be a lie as well.
func (cv *canvas) strangers(a, b uint16) bool {
	if a == 0 || b == 0 || a == b {
		return false
	}
	la, lb := cv.links[a-1], cv.links[b-1]
	return !sharesEnd(la, lb) && !game.LinksCross(la, lb)
}

// sharesEnd reports whether two links have an endpoint in common, which is what
// makes them one connected line on the board.
func sharesEnd(a, b game.Link) bool {
	a1, a2 := a.Ends()
	b1, b2 := b.Ends()
	return a1 == b1 || a1 == b2 || a2 == b1 || a2 == b2
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
	// Cursor is the hole the cursor sits on; ShowCursor turns it on. It is
	// drawn as square brackets either side of the hole, or, where a link owns
	// those cells, as a mark on the hole itself.
	Cursor     game.Point
	ShowCursor bool
	// LastMove marks the peg just played, so it can be found on a large board
	// without reading the coordinate off the panel. ShowLastMove turns it on.
	// An overlay on the same hole takes the cell, so the ring is hidden while
	// the cursor rests on the last move, which is the one time it is not
	// needed.
	LastMove     game.Point
	ShowLastMove bool
	// Highlights marks holes to call out (hints, tutorial steps, the staged
	// peg). They render as round brackets around the hole, falling back to a
	// mark on the hole itself the same way the cursor does.
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
// overlays. An overlay wins every cell it is allowed to take; which cells those
// are is the subject of the comment above the overlay block.
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

	// Overlays go on last, but a bracket may not take a cell a link stroke
	// owns. The bracket columns are holeX±1, and at the compact scale those are
	// the only cells a link touching the hole can use: a bracket there detaches
	// the link from the peg it belongs to, and a compact steep link, which is a
	// single cell in exactly that column, disappears altogether. A stroke that
	// claims an edge nothing joins is worse than a plainer cursor, so a bracket
	// goes into an empty cell or not at all.
	//
	// An overlay that could place neither bracket falls back to the hole's own
	// cell, which it may take for the reason a peg may: a link that merely
	// passes a cell stays legible from the cells either side. The fallback
	// glyph names the overlay and says what the hole holds, so nothing is
	// hidden by it.
	//
	// The cursor claims the bracket cells before the highlights, which is the
	// old precedence, and a highlight that loses both of its brackets falls
	// back to the hole's cell instead of vanishing under the cursor. That is
	// the ordinary turn: the staged peg is highlighted and under the cursor at
	// once, and both facts show as [■]. Where a link owns both bracket cells of
	// that one hole there is a single writable cell for two overlays, and the
	// fallback glyph then has to mean both; that is what the third mark family
	// is for.
	cursorX, cursorY := bv.Scale.holeX(bv.Cursor.Col), bv.Scale.holeY(bv.Cursor.Row)
	cursorBrackets := 0
	if bv.ShowCursor {
		cursorBrackets = cv.bracket(cursorX, cursorY, glyphCursorLeft, glyphCursorRight, styCursor)
	}
	cursorMarked := false
	for _, p := range bv.Highlights {
		if bv.ShowCursor && p == bv.Cursor {
			cursorMarked = true
		}
		x, y := bv.Scale.holeX(p.Col), bv.Scale.holeY(p.Row)
		if cv.bracket(x, y, glyphMarkLeft, glyphMarkRight, styHighlight) == 0 && g.Exists(p) {
			cv.set(x, y, highlightMarks.pick(g.At(p)), styHighlight)
		}
	}
	for p, digit := range bv.Digits {
		cv.set(bv.Scale.holeX(p.Col), bv.Scale.holeY(p.Row), digit, styLinkDigit)
	}
	if bv.ShowCursor && cursorBrackets == 0 && g.Exists(bv.Cursor) {
		marks := cursorMarks
		if cursorMarked {
			marks = cursorHighlightMarks
		}
		cv.set(cursorX, cursorY, marks.pick(g.At(bv.Cursor)), styCursor)
	}
	return cv
}

// overlayMarks is the glyph set one overlay uses on a hole's own cell, for the
// three things a hole can hold. An overlay may never hide a peg, so the mark
// has to name the owner as well as the overlay.
type overlayMarks struct{ hole, vertical, horizontal rune }

func (m overlayMarks) pick(pl game.Player) rune {
	switch pl {
	case game.Vertical:
		return m.vertical
	case game.Horizontal:
		return m.horizontal
	}
	return m.hole
}

var (
	cursorMarks    = overlayMarks{glyphCursorHole, glyphCursorPegVertical, glyphCursorPegHorizontal}
	highlightMarks = overlayMarks{glyphMarkHole, glyphMarkPegVertical, glyphMarkPegHorizontal}
	// cursorHighlightMarks is for the one cell that has to carry both, which
	// happens when a highlighted hole is under the cursor and a link owns the
	// two cells either side of it.
	cursorHighlightMarks = overlayMarks{glyphCursorMarkHole, glyphCursorMarkPegVertical, glyphCursorMarkPegHorizontal}
)

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
