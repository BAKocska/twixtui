package ui

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/BAKocska/twixtui/internal/game"
)

var update = flag.Bool("regen", false, "rewrite golden files")

// hubGame builds a 12×12 position whose centre peg carries links in all eight
// directions, plus a horizontal chain with shallow links, a broken second
// chain, and one isolated peg. Every glyph the renderer can draw appears.
func hubGame(t *testing.T) *game.Game {
	t.Helper()
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	moves := []game.Point{
		{Col: 6, Row: 3}, {Col: 0, Row: 9},
		{Col: 7, Row: 4}, {Col: 2, Row: 10},
		{Col: 7, Row: 6}, {Col: 4, Row: 9},
		{Col: 6, Row: 7}, {Col: 6, Row: 9},
		{Col: 4, Row: 7}, {Col: 8, Row: 9},
		{Col: 3, Row: 6}, {Col: 10, Row: 10},
		{Col: 3, Row: 4}, {Col: 11, Row: 8},
		{Col: 4, Row: 3}, {Col: 9, Row: 4},
		{Col: 5, Row: 5},
	}
	for i, p := range moves {
		res, err := g.PlayPeg(p)
		if err != nil {
			t.Fatalf("move %d (%s): %v", i+1, p, err)
		}
		if res.Over() {
			t.Fatalf("move %d (%s) unexpectedly ended the game", i+1, p)
		}
	}
	center := game.Point{Col: 5, Row: 5}
	if got := g.LinkMask(center); got != 0xFF {
		t.Fatalf("hub setup broken: centre link mask %08b, want all eight", got)
	}
	return g
}

// renderPlain renders the game unstyled with no clipping.
func renderPlain(t *testing.T, bv *BoardView, g *game.Game) []string {
	t.Helper()
	st := PlainStyles()
	cw, ch := bv.Scale.CanvasSize(g.Size())
	lines := bv.Render(g, &st, cw+gutterWidth(g.Size()), ch+1)
	if len(lines) != ch+1 {
		t.Fatalf("unclipped render returned %d lines, want %d", len(lines), ch+1)
	}
	return lines
}

// cellAt returns the rune at canvas coordinates (x, y) of an unclipped render.
func cellAt(t *testing.T, lines []string, n, x, y int) rune {
	t.Helper()
	row := []rune(lines[y+1]) // line 0 is the letters row
	pos := gutterWidth(n) + x
	if pos >= len(row) {
		return ' ' // right-trimmed blank
	}
	return row[pos]
}

func TestCompactLinkGlyphs(t *testing.T) {
	g := hubGame(t)
	bv := &BoardView{Scale: Compact}
	lines := renderPlain(t, bv, g)

	// Centre hole (5,5) sits at canvas (11,5). Steep links are single diagonals.
	// The four shallow links run into the peg along its own row and step away
	// one column short of the outer peg, so the two arriving from each side share
	// one horizontal run and meet it at a tee.
	checks := []struct {
		x, y int
		want rune
		what string
	}{
		{12, 4, '╱', "NNE steep"},
		{10, 4, '╲', "NNW steep"},
		{10, 6, '╱', "SSW steep"},
		{12, 6, '╲', "SSE steep"},
		{12, 5, '─', "run into the peg from the east"},
		{10, 5, '─', "run into the peg from the west"},
		{14, 5, '┤', "ENE and ESE meet the eastern run"},
		{8, 5, '├', "WNW and WSW meet the western run"},
		{14, 4, '╭', "ENE steps up to its peg"},
		{14, 6, '╰', "ESE steps down to its peg"},
		{8, 4, '╮', "WNW steps up to its peg"},
		{8, 6, '╯', "WSW steps down to its peg"},
		// Horizontal player's chain: A10-C11 is a falling shallow link. It steps
		// immediately, then runs along the lower row into its far peg.
		{2, 9, '─', "H chain ESE run"},
		{4, 9, '╮', "H chain ESE step down"},
		{4, 10, '╰', "H chain ESE arriving row"},
		{6, 10, '─', "H chain ENE run out of the middle peg"},
	}
	for _, c := range checks {
		if got := cellAt(t, lines, g.Size(), c.x, c.y); got != c.want {
			t.Errorf("%s: cell (%d,%d) = %q, want %q", c.what, c.x, c.y, got, c.want)
		}
	}
}

func TestDetailLinkGlyphs(t *testing.T) {
	g := hubGame(t)
	bv := &BoardView{Scale: Detail}
	lines := renderPlain(t, bv, g)

	// Centre hole (5,5) sits at canvas (21,10).
	checks := []struct {
		x, y int
		want rune
		what string
	}{
		{22, 9, '╱', "NNE steep first cell"},
		{24, 7, '╱', "NNE steep last cell"},
		{20, 9, '╲', "NNW steep"},
		{20, 11, '╱', "SSW steep"},
		{22, 11, '╲', "SSE steep"},
		{22, 10, '─', "run into the peg from the east"},
		{20, 10, '─', "run into the peg from the west"},
		{23, 10, '┤', "ENE and ESE meet the eastern run"},
		{23, 9, '╭', "ENE turns out of the peg's row"},
		{24, 9, '─', "ENE runs along the spare row"},
		{27, 9, '╯', "ENE steps up again"},
		{27, 8, '╭', "ENE arrives in its peg's row"},
		{28, 8, '─', "ENE runs into its peg"},
	}
	for _, c := range checks {
		if got := cellAt(t, lines, g.Size(), c.x, c.y); got != c.want {
			t.Errorf("%s: cell (%d,%d) = %q, want %q", c.what, c.x, c.y, got, c.want)
		}
	}
}

// pegOwner maps every glyph that stands for a peg to the side that owns it. A
// peg is drawn as itself, as the last move, or under either overlay, and the
// owner has to be readable in every form, because nothing may hide a peg.
func pegOwner(r rune) game.Player {
	switch r {
	case glyphPegVertical, glyphPegVerticalLast,
		glyphCursorPegVertical, glyphMarkPegVertical, glyphCursorMarkPegVertical:
		return game.Vertical
	case glyphPegHorizontal, glyphPegHorizontalLast,
		glyphCursorPegHorizontal, glyphMarkPegHorizontal, glyphCursorMarkPegHorizontal:
		return game.Horizontal
	}
	return game.NoPlayer
}

// isEmptyHoleGlyph reports whether a glyph says "a hole with nothing in it":
// its own dot, or an overlay's mark for an empty hole.
func isEmptyHoleGlyph(r rune) bool {
	switch r {
	case glyphHole, glyphCursorHole, glyphMarkHole, glyphCursorMarkHole:
		return true
	}
	return false
}

func inMarks(r rune, m overlayMarks) bool {
	return r == m.hole || r == m.vertical || r == m.horizontal
}

// cursorShown reports whether the frame says the cursor is on p: a bracket in
// one of the two cells beside the hole, or a mark on the hole's own cell from a
// set that includes the cursor.
func cursorShown(t *testing.T, lines []string, n int, sc Scale, p game.Point) bool {
	t.Helper()
	x, y := sc.holeX(p.Col), sc.holeY(p.Row)
	if cellAt(t, lines, n, x-1, y) == glyphCursorLeft || cellAt(t, lines, n, x+1, y) == glyphCursorRight {
		return true
	}
	own := cellAt(t, lines, n, x, y)
	return inMarks(own, cursorMarks) || inMarks(own, cursorHighlightMarks)
}

// highlightShown is cursorShown for a highlight.
func highlightShown(t *testing.T, lines []string, n int, sc Scale, p game.Point) bool {
	t.Helper()
	x, y := sc.holeX(p.Col), sc.holeY(p.Row)
	if cellAt(t, lines, n, x-1, y) == glyphMarkLeft || cellAt(t, lines, n, x+1, y) == glyphMarkRight {
		return true
	}
	own := cellAt(t, lines, n, x, y)
	return inMarks(own, highlightMarks) || inMarks(own, cursorHighlightMarks)
}

// TestPegsHolesCornersDistinguishableWithoutColour is the whole glyph set held
// to its promise: with colour off, every cell of the board still says what it
// is. It is run over three overlay placements, because an overlay's shape
// depends on what the links around it left free: brackets either side where
// there is room, and a mark on the hole's own cell where a link owns both
// bracket cells. The last placement is the hard one — a highlighted hole under
// the cursor whose own links own both cells — where one glyph has to carry the
// cursor, the highlight and the peg's owner at once.
func TestPegsHolesCornersDistinguishableWithoutColour(t *testing.T) {
	g := hubGame(t)
	// The hub's centre peg carries links in all eight directions, so both of
	// its bracket cells are taken and the overlay has to fall back.
	hub := game.Point{Col: 5, Row: 5}
	placements := []struct {
		what      string
		cursor    game.Point
		highlight game.Point
		fallback  bool
	}{
		{"brackets free", game.Point{Col: 5, Row: 8}, game.Point{Col: 8, Row: 8}, false},
		{"cursor on the hub peg", hub, game.Point{Col: 8, Row: 8}, true},
		{"cursor and highlight both on the hub peg", hub, hub, true},
	}
	for _, sc := range []Scale{Compact, Detail} {
		for _, pl := range placements {
			bv := &BoardView{
				Scale:      sc,
				Cursor:     pl.cursor,
				ShowCursor: true,
				Highlights: []game.Point{pl.highlight},
			}
			lines := renderPlain(t, bv, g)
			all := strings.Join(lines, "\n")
			if esc := strings.IndexByte(all, 0x1b); esc >= 0 {
				t.Fatalf("%s %s: plain render contains an escape byte at %d", sc, pl.what, esc)
			}
			vertical, horizontal, dots := 0, 0, 0
			for _, r := range all {
				switch {
				case pegOwner(r) == game.Vertical:
					vertical++
				case pegOwner(r) == game.Horizontal:
					horizontal++
				case isEmptyHoleGlyph(r):
					dots++
				}
			}
			if vertical != 9 {
				t.Errorf("%s %s: %d vertical pegs rendered, want 9", sc, pl.what, vertical)
			}
			if horizontal != 8 {
				t.Errorf("%s %s: %d horizontal pegs rendered, want 8", sc, pl.what, horizontal)
			}
			// Every hole is accounted for: an occupied one shows its peg, an
			// empty one shows its dot unless a shallow link legitimately
			// crosses the cell. The detail scale has a spare row for every
			// crossing, so there it must never come to that.
			covered := 0
			for row := range g.Size() {
				for col := range g.Size() {
					p := game.Point{Col: col, Row: row}
					if !g.Exists(p) {
						continue
					}
					got := cellAt(t, lines, g.Size(), sc.holeX(col), sc.holeY(row))
					switch want := g.At(p); {
					case want != game.NoPlayer:
						if pegOwner(got) != want {
							t.Errorf("%s %s: %v holds a %v peg but renders %q",
								sc, pl.what, p, want, got)
						}
					case isEmptyHoleGlyph(got):
					case isLinkGlyph(got):
						covered++
					default:
						t.Errorf("%s %s: empty hole %v renders %q, want a dot or a link stroke",
							sc, pl.what, p, got)
					}
				}
			}
			if sc == Detail && covered != 0 {
				t.Errorf("detail %s: %d hole dots were covered by link strokes; the scale has room for every crossing",
					pl.what, covered)
			}
			if dots+covered != 12*12-4-17 {
				t.Errorf("%s %s: %d dots plus %d covered holes, want %d holes in total",
					sc, pl.what, dots, covered, 12*12-4-17)
			}
			// The two overlays, each identifiable on its own hole and never as
			// each other. On the last placement they share one hole, so both
			// facts have to come out of the same cell.
			if !cursorShown(t, lines, g.Size(), sc, pl.cursor) {
				t.Errorf("%s %s: nothing on the frame says the cursor is on %v:\n%s",
					sc, pl.what, pl.cursor, all)
			}
			if !highlightShown(t, lines, g.Size(), sc, pl.highlight) {
				t.Errorf("%s %s: nothing on the frame says %v is highlighted:\n%s",
					sc, pl.what, pl.highlight, all)
			}
			if pl.cursor != pl.highlight && highlightShown(t, lines, g.Size(), sc, pl.cursor) {
				t.Errorf("%s %s: the cursor's hole %v reads as highlighted", sc, pl.what, pl.cursor)
			}
			// Keep the fixture honest: if the links stop owning both bracket
			// cells of the hub peg, the fallback goes untested and this test
			// quietly weakens.
			own := cellAt(t, lines, g.Size(), sc.holeX(pl.cursor.Col), sc.holeY(pl.cursor.Row))
			marked := inMarks(own, cursorMarks) || inMarks(own, cursorHighlightMarks)
			if marked != pl.fallback {
				t.Fatalf("%s %s: the hole's own cell is %q, so fallback=%v, want %v",
					sc, pl.what, own, marked, pl.fallback)
			}
			// Corners are absent: the top-left board cell must be blank.
			if got := cellAt(t, lines, g.Size(), sc.holeX(0), sc.holeY(0)); got != ' ' {
				t.Errorf("%s %s: corner hole rendered as %q, want blank", sc, pl.what, got)
			}
		}
	}
}

// TestEveryBoardGlyphIsItsOwnMark is the premise the rest of the glyph
// assertions rest on: no two of the marks the board draws are the same
// character, so telling them apart is possible at all with colour off.
func TestEveryBoardGlyphIsItsOwnMark(t *testing.T) {
	named := map[rune]string{}
	for _, m := range []struct {
		name  string
		glyph rune
	}{
		{"hole", glyphHole},
		{"vertical peg", glyphPegVertical},
		{"horizontal peg", glyphPegHorizontal},
		{"vertical last move", glyphPegVerticalLast},
		{"horizontal last move", glyphPegHorizontalLast},
		{"cursor left", glyphCursorLeft},
		{"cursor right", glyphCursorRight},
		{"highlight left", glyphMarkLeft},
		{"highlight right", glyphMarkRight},
		{"cursor on an empty hole", glyphCursorHole},
		{"cursor on a vertical peg", glyphCursorPegVertical},
		{"cursor on a horizontal peg", glyphCursorPegHorizontal},
		{"highlight on an empty hole", glyphMarkHole},
		{"highlight on a vertical peg", glyphMarkPegVertical},
		{"highlight on a horizontal peg", glyphMarkPegHorizontal},
		{"both on an empty hole", glyphCursorMarkHole},
		{"both on a vertical peg", glyphCursorMarkPegVertical},
		{"both on a horizontal peg", glyphCursorMarkPegHorizontal},
		{"rising link", glyphRise},
		{"falling link", glyphFall},
		{"crossing", glyphCross},
	} {
		if prev, seen := named[m.glyph]; seen {
			t.Errorf("%q is both the %s and the %s", m.glyph, prev, m.name)
			continue
		}
		named[m.glyph] = m.name
		if isLinkGlyph(m.glyph) && m.glyph != glyphRise && m.glyph != glyphFall && m.glyph != glyphCross {
			t.Errorf("the %s, %q, is also a link stroke", m.name, m.glyph)
		}
	}
}

func TestColourSeparatesOwners(t *testing.T) {
	g := hubGame(t)
	bv := &BoardView{Scale: Compact}
	st := DefaultStyles()
	cw, ch := Compact.CanvasSize(g.Size())
	lines := bv.Render(g, &st, cw+gutterWidth(g.Size()), ch+1)
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "\x1b[") {
		t.Fatal("styled render carries no ANSI at all")
	}
	vIdx := strings.IndexRune(all, glyphPegVertical)
	hIdx := strings.IndexRune(all, glyphPegHorizontal)
	if vIdx < 0 || hIdx < 0 {
		t.Fatal("pegs missing from styled render")
	}
	if sgrBefore(all, vIdx) == sgrBefore(all, hIdx) {
		t.Error("both players' pegs carry the same style")
	}
}

// sgrBefore returns the ANSI sequence immediately preceding byte offset i.
func sgrBefore(s string, i int) string {
	start := strings.LastIndex(s[:i], "\x1b[")
	if start < 0 {
		return ""
	}
	return s[start:i]
}

func TestViewportKeepsCursorVisibleAndLabelsTrue(t *testing.T) {
	g := hubGame(t)
	bv := &BoardView{Scale: Compact, ShowCursor: true}
	st := PlainStyles()

	corners := []game.Point{
		{Col: 1, Row: 0}, {Col: 11, Row: 1}, {Col: 10, Row: 11}, {Col: 0, Row: 10},
	}
	for _, c := range corners {
		bv.Cursor = c
		lines := bv.Render(g, &st, 20, 8)
		all := strings.Join(lines, "\n")
		if len(lines) > 8 {
			t.Fatalf("cursor %s: %d lines exceed height 8", c, len(lines))
		}
		for _, l := range lines {
			if n := len([]rune(l)); n > 20 {
				t.Fatalf("cursor %s: line %q is %d cells wide", c, l, n)
			}
		}
		// What marks the cursor depends on what the links around the hole left
		// free, so this asks only that something does. The subject here is
		// scrolling, not the shape of the mark.
		if !strings.ContainsAny(all, string([]rune{glyphCursorLeft, glyphCursorRight,
			glyphCursorHole, glyphCursorPegVertical, glyphCursorPegHorizontal})) {
			t.Errorf("cursor %s not visible in 20x8 viewport:\n%s", c, all)
		}
	}

	// Scrolled towards the bottom-right the labels must name the columns and
	// rows actually shown. The scroll is minimal, so with the cursor one
	// column short of the edge both overflow arrows show.
	bv.Cursor = game.Point{Col: 10, Row: 11}
	lines := bv.Render(g, &st, 20, 8)
	letters := lines[0]
	if !strings.ContainsRune(letters, 'K') || strings.ContainsRune(letters, 'C') {
		t.Errorf("letters row %q should show K but not C", letters)
	}
	if !strings.ContainsRune(letters, glyphLeft) || !strings.ContainsRune(letters, glyphRight) {
		t.Errorf("letters row %q lacks an overflow arrow while clipped both sides", letters)
	}
	if !strings.HasPrefix(lines[len(lines)-1], "12 ") {
		t.Errorf("bottom visible row starts %q, want the true row number 12", lines[len(lines)-1])
	}
	var hasUp bool
	for _, l := range lines {
		hasUp = hasUp || strings.ContainsRune(l, glyphUp)
	}
	if !hasUp {
		t.Error("no up arrow while rows are hidden above")
	}

	// On the right-edge column the board is flush right: no right arrow.
	bv.Cursor = game.Point{Col: 11, Row: 10}
	lines = bv.Render(g, &st, 20, 8)
	letters = lines[0]
	if !strings.ContainsRune(letters, 'L') {
		t.Errorf("letters row %q lacks column L at the right edge", letters)
	}
	if strings.ContainsRune(letters, glyphRight) {
		t.Errorf("letters row %q shows a right arrow at the right edge", letters)
	}
	if !strings.ContainsRune(letters, glyphLeft) {
		t.Errorf("letters row %q lacks the left overflow arrow", letters)
	}
}

func TestViewportAbsentWhenBoardFits(t *testing.T) {
	g := hubGame(t)
	bv := &BoardView{Scale: Compact, ShowCursor: true, Cursor: game.Point{Col: 5, Row: 8}}
	st := PlainStyles()
	lines := bv.Render(g, &st, 60, 20)
	cw, ch := Compact.CanvasSize(g.Size())
	if len(lines) != ch+1 {
		t.Fatalf("%d lines rendered, want the full board's %d", len(lines), ch+1)
	}
	all := strings.Join(lines, "\n")
	for _, r := range []rune{glyphUp, glyphDown, glyphLeft, glyphRight} {
		if strings.ContainsRune(all, r) {
			t.Errorf("overflow arrow %q shown although the board fits", r)
		}
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > gutterWidth(g.Size())+cw {
			t.Errorf("line %q wider than the board block", l)
		}
	}
}

func TestGoldenFrames(t *testing.T) {
	g := hubGame(t)
	// Two frames per scale. The first has the cursor and the highlight on holes
	// with room either side, which is what the board mostly looks like. The
	// second puts both of them on the hub peg, whose own links own the two
	// cells the brackets would use, so the frame shows what an overlay falls
	// back to when there is nowhere to put a bracket.
	views := []struct {
		name       string
		cursor     game.Point
		highlights []game.Point
	}{
		{"hub", game.Point{Col: 5, Row: 8}, []game.Point{{Col: 8, Row: 8}}},
		{"hub_marked", game.Point{Col: 5, Row: 5}, []game.Point{{Col: 5, Row: 5}, {Col: 3, Row: 6}}},
	}
	for _, sc := range []Scale{Compact, Detail} {
		for _, v := range views {
			bv := &BoardView{
				Scale:      sc,
				Cursor:     v.cursor,
				ShowCursor: true,
				Highlights: v.highlights,
			}
			got := strings.Join(renderPlain(t, bv, g), "\n") + "\n"
			path := filepath.Join("testdata", v.name+"_"+sc.String()+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden file missing (run with -regen after eyeballing): %v", err)
			}
			if got != string(want) {
				t.Errorf("%s %s frame drifted from golden:\ngot:\n%s\nwant:\n%s", sc, v.name, got, want)
			}
		}
	}
}

// TestEveryLinkDrawsAsOneUnbrokenRun is the shape complaint made precise. A link
// between two pegs must read as one connection, which on a character grid means
// no column between the two ends is left without a stroke.
//
// Shallow links used to fail this on the compact scale. Their midpoint falls in
// the column of the hole between the two ends, on the boundary between two hole
// rows, and the renderer skipped any cell belonging to a hole. With one row per
// hole that skipped the midpoint itself, so the link came out as two stubs on
// two different rows with a hole between them, reading as two unrelated marks
// rather than as one link.
func TestEveryLinkDrawsAsOneUnbrokenRun(t *testing.T) {
	for _, sc := range []Scale{Compact, Detail} {
		for d := game.Dir(0); d < game.NumDirs; d++ {
			if !d.IsCanonical() {
				continue
			}
			from := game.Point{Col: 3, Row: 3}
			to := from.Add(d)
			g := linkedPair(t, from, to, game.Point{})
			if gaps, frame := strokeGaps(t, sc, g, from, to); len(gaps) != 0 {
				t.Errorf("%s %v link %v-%v: no stroke in column(s) %v between the ends, so the link reads as separate marks\n%s",
					sc, d, from, to, gaps, frame)
			}
		}
	}
}

// TestEveryLinkInACrowdedPositionIsUnbroken runs the same rule over a real
// position: a peg carrying all eight links at once, surrounded by other pegs and
// links, which is where crossings, merged strokes and neighbouring pegs all
// compete for the same cells.
func TestEveryLinkInACrowdedPositionIsUnbroken(t *testing.T) {
	g := hubGame(t)
	for _, sc := range []Scale{Compact, Detail} {
		links := 0
		for row := range g.Size() {
			for col := range g.Size() {
				from := game.Point{Col: col, Row: row}
				mask := g.LinkMask(from)
				for d := game.Dir(0); d < game.NumDirs; d++ {
					if !d.IsCanonical() || mask&(1<<d) == 0 {
						continue
					}
					links++
					to := from.Add(d)
					if gaps, frame := strokeGaps(t, sc, g, from, to); len(gaps) != 0 {
						t.Errorf("%s: link %v-%v (%v) has no stroke in column(s) %v\n%s",
							sc, from, to, d, gaps, frame)
					}
				}
			}
		}
		if links < 8 {
			t.Fatalf("%s: only %d links examined, the fixture is not the crowded position", sc, links)
		}
	}
}

// TestAPegBesideACrossingDoesNotBreakTheLink covers the other way the same
// defect shows: the crossing cell may be taken by a peg in one of the two holes
// the link passes between, and then the stroke belongs in the other one.
func TestAPegBesideACrossingDoesNotBreakTheLink(t *testing.T) {
	from := game.Point{Col: 3, Row: 3}
	for _, d := range []game.Dir{game.ENE, game.ESE} {
		to := from.Add(d)
		// The two holes the link passes between, in the column between its ends.
		upper := game.Point{Col: (from.Col + to.Col) / 2, Row: from.Row}
		lower := game.Point{Col: upper.Col, Row: to.Row}
		for _, blocked := range []game.Point{upper, lower} {
			g := linkedPair(t, from, to, blocked)
			if gaps, frame := strokeGaps(t, Compact, g, from, to); len(gaps) != 0 {
				t.Errorf("%v link with a peg at %v: column(s) %v carry no stroke\n%s", d, blocked, gaps, frame)
			}
			lines := renderPlain(t, &BoardView{Scale: Compact}, g)
			if got := cellAt(t, lines, g.Size(), Compact.holeX(blocked.Col), Compact.holeY(blocked.Row)); got != glyphPegHorizontal {
				t.Errorf("%v link with a peg at %v: the peg renders %q, want %q", d, blocked, got, glyphPegHorizontal)
			}
		}
	}
}

// strokeGaps renders the game and reports which canvas columns strictly between
// the two ends carry no link stroke in any row, together with the frame, so a
// failure shows the board that produced it.
func strokeGaps(t *testing.T, sc Scale, g *game.Game, from, to game.Point) ([]int, string) {
	t.Helper()
	lines := renderPlain(t, &BoardView{Scale: sc}, g)
	_, h := sc.CanvasSize(g.Size())
	x1, x2 := sc.holeX(from.Col), sc.holeX(to.Col)
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	var gaps []int
	for x := x1 + 1; x < x2; x++ {
		painted := false
		for y := range h {
			switch r := cellAt(t, lines, g.Size(), x, y); {
			case isLinkGlyph(r):
				painted = true
			}
			if painted {
				break
			}
		}
		if !painted {
			gaps = append(gaps, x)
		}
	}
	return gaps, strings.Join(lines, "\n")
}

// linkedPair plays a vertical peg at from and another at to, so the two link,
// and gives the horizontal player the zero-value-unless-set hole between them.
// The opponent otherwise plays in its own border column, far from the link, so
// nothing it does can paint a cell this test reads.
func linkedPair(t *testing.T, from, to, opponent game.Point) *game.Game {
	t.Helper()
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	if opponent == (game.Point{}) {
		opponent = game.Point{Col: 11, Row: 8}
	}
	for i, p := range []game.Point{from, opponent, to} {
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("move %d at %v: %v", i+1, p, err)
		}
	}
	l, ok := game.NewLink(from, to)
	if !ok {
		t.Fatalf("%v-%v is not a knight's move", from, to)
	}
	if g.LinkOwner(l) != game.Vertical {
		t.Fatalf("no vertical link formed between %v and %v", from, to)
	}
	return g
}

// boxEdges returns the edges a drawn box glyph claims, and whether the glyph is
// one of them at all. It is derived from the junction table rather than listed,
// so the two cannot drift apart.
func boxEdges(r rune) (linkBits, bool) {
	for b, j := range junction {
		if j != 0 && j == r {
			return linkBits(b), true
		}
	}
	return 0, false
}

// opaqueToLinks reports whether a glyph is one a link stroke may legitimately
// run into: a peg in any of its forms, another stroke that is not a box glyph,
// or an overlay's mark on a hole's own cell. All of them stand where a line
// passes and leave it legible from the cells either side, which is the rule a
// peg has always followed.
func opaqueToLinks(r rune) bool {
	if pegOwner(r) != game.NoPlayer || isEmptyHoleGlyph(r) {
		return true
	}
	return r == glyphRise || r == glyphFall || r == glyphCross
}

// danglingEdges lists every cell of a frame whose stroke claims an edge that
// nothing on the other side joins. That is the visible symptom the geometry
// audit named: a line drawn to an edge of its cell with no line, peg or mark
// beyond it reads as a connection to something that is not there.
func danglingEdges(t *testing.T, sc Scale, n int, lines []string) []string {
	t.Helper()
	cw, ch := sc.CanvasSize(n)
	sides := [4]struct {
		edge, back linkBits
		dx, dy     int
	}{
		{linkN, linkS, 0, -1},
		{linkE, linkW, 1, 0},
		{linkS, linkN, 0, 1},
		{linkW, linkE, -1, 0},
	}
	var out []string
	for y := range ch {
		for x := range cw {
			here := cellAt(t, lines, n, x, y)
			edges, ok := boxEdges(here)
			if !ok {
				continue
			}
			for _, s := range sides {
				if edges&s.edge == 0 {
					continue
				}
				nx, ny := x+s.dx, y+s.dy
				if nx < 0 || ny < 0 || nx >= cw || ny >= ch {
					out = append(out, fmt.Sprintf("(%d,%d) %q claims an edge off the canvas", x, y, here))
					continue
				}
				beyond := cellAt(t, lines, n, nx, ny)
				if nb, ok := boxEdges(beyond); ok {
					if nb&s.back == 0 {
						out = append(out, fmt.Sprintf("(%d,%d) %q meets %q at (%d,%d), which does not join back",
							x, y, here, beyond, nx, ny))
					}
					continue
				}
				if !opaqueToLinks(beyond) {
					out = append(out, fmt.Sprintf("(%d,%d) %q claims an edge onto %q at (%d,%d)",
						x, y, here, beyond, nx, ny))
				}
			}
		}
	}
	return out
}

// TestAnOverlayNeverBreaksALink is the cursor-and-highlight complaint made
// precise. The brackets sit at holeX±1, which at the compact scale are the only
// cells a link touching that hole can use, so drawing them over a stroke either
// detaches a link from its own peg or erases a one-cell steep link outright.
//
// The rule asserted here is that an overlay may take the hole's own cell, as a
// peg does, and nothing else: every other cell that carried a stroke without
// the overlay still carries the same stroke with it, and no frame is left with
// a stroke claiming an edge onto nothing.
func TestAnOverlayNeverBreaksALink(t *testing.T) {
	g := hubGame(t)
	for _, sc := range []Scale{Compact, Detail} {
		bare := renderPlain(t, &BoardView{Scale: sc}, g)
		if bad := danglingEdges(t, sc, g.Size(), bare); len(bad) != 0 {
			t.Fatalf("%s: the bare frame already has dangling edges %v:\n%s",
				sc, bad, strings.Join(bare, "\n"))
		}
		cw, ch := sc.CanvasSize(g.Size())
		for row := range g.Size() {
			for col := range g.Size() {
				p := game.Point{Col: col, Row: row}
				if !g.Exists(p) {
					continue
				}
				views := []struct {
					what string
					bv   *BoardView
				}{
					{"cursor", &BoardView{Scale: sc, ShowCursor: true, Cursor: p}},
					{"highlight", &BoardView{Scale: sc, Highlights: []game.Point{p}}},
					{"both", &BoardView{Scale: sc, ShowCursor: true, Cursor: p, Highlights: []game.Point{p}}},
				}
				hx, hy := sc.holeX(col), sc.holeY(row)
				for _, v := range views {
					got := renderPlain(t, v.bv, g)
					for y := range ch {
						for x := range cw {
							if x == hx && y == hy {
								continue // the hole's own cell, which an overlay may take
							}
							was := cellAt(t, bare, g.Size(), x, y)
							if !isLinkGlyph(was) {
								continue
							}
							if now := cellAt(t, got, g.Size(), x, y); now != was {
								t.Fatalf("%s: the %s on %v replaced the stroke %q at (%d,%d) with %q:\n%s",
									sc, v.what, p, was, x, y, now, strings.Join(got, "\n"))
							}
						}
					}
					if bad := danglingEdges(t, sc, g.Size(), got); len(bad) != 0 {
						t.Fatalf("%s: the %s on %v left %v:\n%s",
							sc, v.what, p, bad, strings.Join(got, "\n"))
					}
				}
			}
		}
	}
}

// TestTheStagedPegKeepsTheLinkItJustMade is the case that fires on an ordinary
// turn rather than at an unusual cursor position: placing a peg stages it, the
// screen highlights it, and the cursor is already on it, so both overlays land
// on the one hole whose link has only just been drawn.
func TestTheStagedPegKeepsTheLinkItJustMade(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	for _, p := range []game.Point{{Col: 5, Row: 3}, {Col: 9, Row: 9}} {
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("opening move %v: %v", p, err)
		}
	}
	staged := game.Point{Col: 3, Row: 4}
	if err := g.PlacePeg(staged); err != nil {
		t.Fatalf("staging %v: %v", staged, err)
	}
	l, ok := game.NewLink(staged, game.Point{Col: 5, Row: 3})
	if !ok || g.LinkOwner(l) != game.Vertical {
		t.Fatalf("the placement did not form the link the test is about")
	}
	for _, sc := range []Scale{Compact, Detail} {
		bare := renderPlain(t, &BoardView{Scale: sc}, g)
		over := renderPlain(t, &BoardView{
			Scale: sc, ShowCursor: true, Cursor: staged,
			Highlights: []game.Point{staged},
		}, g)
		if bareN, overN := strokeCells(t, sc, g, bare), strokeCells(t, sc, g, over); overN != bareN {
			t.Errorf("%s: the staged peg's overlay took the link down from %d cells to %d:\n%s",
				sc, bareN, overN, strings.Join(over, "\n"))
		}
		if bad := danglingEdges(t, sc, g.Size(), over); len(bad) != 0 {
			t.Errorf("%s: %v:\n%s", sc, bad, strings.Join(over, "\n"))
		}
		if !cursorShown(t, over, g.Size(), sc, staged) {
			t.Errorf("%s: the cursor is not visible on the staged peg:\n%s", sc, strings.Join(over, "\n"))
		}
		if !highlightShown(t, over, g.Size(), sc, staged) {
			t.Errorf("%s: the staged peg does not read as highlighted:\n%s", sc, strings.Join(over, "\n"))
		}
	}
}

// TestACompactSteepLinkSurvivesAnOverlay is the sharpest form of the same
// defect: a steep link at the compact scale is one cell, and that cell is a
// bracket column of the holes either side of it, so an overlay on a hole the
// link only passes used to erase the link completely.
func TestACompactSteepLinkSurvivesAnOverlay(t *testing.T) {
	from, to := game.Point{Col: 4, Row: 1}, game.Point{Col: 3, Row: 3}
	g := linkedPair(t, from, to, game.Point{})
	passed := game.Point{Col: 3, Row: 2} // the hole the link passes, between the two
	bare := renderPlain(t, &BoardView{Scale: Compact}, g)
	want := strokeCells(t, Compact, g, bare)
	if want != 1 {
		t.Fatalf("the fixture draws %d stroke cells, want the single steep cell", want)
	}
	for _, v := range []struct {
		what string
		bv   *BoardView
	}{
		{"cursor", &BoardView{Scale: Compact, ShowCursor: true, Cursor: passed}},
		{"highlight", &BoardView{Scale: Compact, Highlights: []game.Point{passed}}},
	} {
		got := renderPlain(t, v.bv, g)
		if n := strokeCells(t, Compact, g, got); n != want {
			t.Errorf("the %s on %v left %d stroke cells, want %d:\n%s",
				v.what, passed, n, want, strings.Join(got, "\n"))
		}
	}
}

// strokeCells counts the cells of a frame holding a link stroke.
func strokeCells(t *testing.T, sc Scale, g *game.Game, lines []string) int {
	t.Helper()
	cw, ch := sc.CanvasSize(g.Size())
	n := 0
	for y := range ch {
		for x := range cw {
			if isLinkGlyph(cellAt(t, lines, g.Size(), x, y)) {
				n++
			}
		}
	}
	return n
}

// shallowLinks lists every shallow link an n-hole board can hold. Steep links
// are left out: one draws a single diagonal and has no polyline to route.
func shallowLinks(n int) []game.Link {
	inside := func(p game.Point) bool {
		if p.Col < 0 || p.Row < 0 || p.Col >= n || p.Row >= n {
			return false
		}
		return !((p.Col == 0 || p.Col == n-1) && (p.Row == 0 || p.Row == n-1))
	}
	var out []game.Link
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if !inside(p) {
				continue
			}
			for d := game.Dir(0); d < game.NumDirs; d++ {
				if !d.IsCanonical() {
					continue
				}
				if dCol, _ := d.Offset(); abs(dCol) != 2 {
					continue
				}
				q := p.Add(d)
				if !inside(q) {
					continue
				}
				if l, ok := game.NewLink(p, q); ok {
					out = append(out, l)
				}
			}
		}
	}
	return out
}

// paintPair lays a board out the way paint does — a dot in every hole, a peg on
// each of the four endpoints — draws the two links in order, resolves them, and
// returns the canvas together with the cells both links reached.
func paintPair(sc Scale, n int, a, b game.Link) (*canvas, []int) {
	cw, ch := sc.CanvasSize(n)
	cv := newCanvas(cw, ch)
	for row := range n {
		for col := range n {
			if (col == 0 || col == n-1) && (row == 0 || row == n-1) {
				continue
			}
			cv.set(sc.holeX(col), sc.holeY(row), glyphHole, styHole)
		}
	}
	for _, p := range []game.Point{a.From, a.To(), b.From, b.To()} {
		cv.set(sc.holeX(p.Col), sc.holeY(p.Row), glyphPegVertical, styPegVertical)
	}
	sc.drawLink(cv, a, styLinkVertical)
	first := make([]linkBits, len(cv.bits))
	copy(first, cv.bits)
	sc.drawLink(cv, b, styLinkVertical)
	var shared []int
	for i := range cv.bits {
		if first[i].edges() != 0 && cv.bits[i].edges() != first[i].edges() {
			shared = append(shared, i)
		}
	}
	cv.resolveLinks()
	return cv, shared
}

// junctionCells lists the cells of a frame drawn as a box junction, which is the
// picture of three or four lines meeting in one cell.
func junctionCells(t *testing.T, sc Scale, n int, lines []string) []string {
	t.Helper()
	cw, ch := sc.CanvasSize(n)
	var out []string
	for y := range ch {
		for x := range cw {
			r := cellAt(t, lines, n, x, y)
			if edges, ok := boxEdges(r); ok && edges.joins() {
				out = append(out, fmt.Sprintf("(%d,%d)=%q", x, y, r))
			}
		}
	}
	return out
}

// TestLinksThatMeetNowhereAreNeverDrawnJoined is the connectivity complaint made
// precise, over every pair of shallow links an eight-square board can hold.
//
// Two links that share no peg and do not cross are two separate things on the
// board. A junction glyph says the lines in its cell meet, so a junction built
// out of both of them draws a connection between two chains that have none, and
// connectivity is the one thing a player reads off a TwixT board. The routing
// has to keep them apart instead.
func TestLinksThatMeetNowhereAreNeverDrawnJoined(t *testing.T) {
	const n = 8
	for _, sc := range []Scale{Compact, Detail} {
		links := shallowLinks(n)
		pairs := 0
		for i := range links {
			for j := i + 1; j < len(links); j++ {
				a, b := links[i], links[j]
				if sharesEnd(a, b) || game.LinksCross(a, b) {
					continue
				}
				pairs++
				cv, shared := paintPair(sc, n, a, b)
				for _, k := range shared {
					if cv.bits[k].joins() {
						t.Fatalf("%s: %v and %v meet at no peg and do not cross, yet cell (%d,%d) draws them joined as %q",
							sc, a, b, k%cv.w, k/cv.w, cv.runes[k])
					}
				}
			}
		}
		if pairs < 2000 {
			t.Fatalf("%s: only %d pairs examined, so the scan is not the exhaustive one", sc, pairs)
		}
	}
}

// TestTwoUnconnectedLinksAreDrawnApart is the reproduction from the geometry
// audit, driven through a rendered frame rather than the canvas: two links, no
// shared peg, no crossing, and under the default ruleset.
func TestTwoUnconnectedLinksAreDrawnApart(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	for _, p := range []game.Point{
		{Col: 1, Row: 0}, {Col: 8, Row: 8},
		{Col: 3, Row: 1}, {Col: 9, Row: 8},
		{Col: 1, Row: 1}, {Col: 10, Row: 8},
		{Col: 3, Row: 2},
	} {
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("move %v: %v", p, err)
		}
	}
	first, ok := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 3, Row: 1})
	if !ok {
		t.Fatal("B1-D2 is not a knight's move")
	}
	second, ok := game.NewLink(game.Point{Col: 1, Row: 1}, game.Point{Col: 3, Row: 2})
	if !ok {
		t.Fatal("B2-D3 is not a knight's move")
	}
	if g.LinkOwner(first) != game.Vertical || g.LinkOwner(second) != game.Vertical {
		t.Fatal("the fixture does not hold both links")
	}
	if sharesEnd(first, second) || game.LinksCross(first, second) {
		t.Fatal("the fixture's links are not the independent pair the test is about")
	}
	for _, sc := range []Scale{Compact, Detail} {
		lines := renderPlain(t, &BoardView{Scale: sc}, g)
		if bad := junctionCells(t, sc, g.Size(), lines); len(bad) != 0 {
			t.Errorf("%s: only two links are on the board and they meet nowhere, yet %v draw them joined:\n%s",
				sc, bad, strings.Join(lines, "\n"))
		}
		for _, l := range []game.Link{first, second} {
			from, to := l.Ends()
			if gaps, frame := strokeGaps(t, sc, g, from, to); len(gaps) != 0 {
				t.Errorf("%s: keeping %v apart broke it: no stroke in column(s) %v\n%s", sc, l, gaps, frame)
			}
		}
	}
}

// TestEveryCrossingIsDrawnAsACrossing is the other half of the same rule. Where
// the ruleset lets two of a player's links cross, they do have to share a cell —
// their endpoints interleave, so no routing can separate them — and there the
// glyph has to say they cross rather than meet. game.LinksCross is the authority
// TestEveryCrossingIsDrawnHonestly covers two links that cross, which only the
// paper-and-pencil ruleset permits and then only between one player's own links.
//
// Two things must hold, and they are different things. Where the two links reach
// the same cell, that cell may not be drawn as a junction — a junction is the
// picture of lines meeting and a crossing is the picture of lines that do not —
// so it carries the crossing glyph instead. Where they reach no common cell, at
// the compact scale two crossing steep links land one above the other, there is
// nothing to mark: neither link's cells claim anything about the other, and the
// only way to put a crossing glyph on the picture would be to overwrite one of
// the two strokes, which is the defect this renderer was fixed to stop. So the
// requirement there is that both links keep every cell they drew.
//
// The sweep covers steep and shallow links alike. It used to enumerate shallow
// links only, so the whole steep half of the geometry — including every crossing
// a steep link takes part in — went unexamined.
func TestEveryCrossingIsDrawnHonestly(t *testing.T) {
	const n = 8
	for _, sc := range []Scale{Compact, Detail} {
		var pairs, steepPairs int
		links := allLinks(n)
		for i := range links {
			for j := i + 1; j < len(links); j++ {
				a, b := links[i], links[j]
				if !game.LinksCross(a, b) {
					continue
				}
				pairs++
				both, _ := paintPair(sc, n, a, b)

				// Whatever else happens, a crossing may not read as a meeting
				// and may not cost either link its line.
				for k, r := range both.runes {
					if isJunctionGlyph(r) {
						t.Fatalf("%s: %v and %v cross, yet cell (%d,%d) draws them meeting as %q",
							sc, a, b, k%both.w, k/both.w, r)
					}
				}
				for _, l := range []game.Link{a, b} {
					if gaps := drawnGaps(sc, both, l); len(gaps) != 0 {
						t.Fatalf("%s: %v and %v cross, and %v is left broken at column(s) %v",
							sc, a, b, l, gaps)
					}
				}

				// A steep link has no choice of cells, so two crossing steep
				// links either share a cell or they do not, and that does not
				// depend on the order they were drawn in. Where they do share
				// one, it has to say so: leaving the first link's diagonal there
				// would hide the second link completely, since at the compact
				// scale that one cell is all it has. Where they do not, there is
				// nothing to mark, and marking it would mean overwriting a
				// stroke — which is the defect this renderer exists to prevent.
				if steep(a) && steep(b) {
					steepPairs++
					shared := 0
					for k := range paintAlone(sc, n, a) {
						if paintAlone(sc, n, b)[k] {
							shared++
							if both.runes[k] != glyphCross {
								t.Fatalf("%s: steep %v and %v cross in cell (%d,%d), which shows %q rather than a crossing",
									sc, a, b, k%both.w, k/both.w, both.runes[k])
							}
						}
					}
				}
			}
		}
		if pairs < 500 || steepPairs < 50 {
			t.Fatalf("%s: %d crossing pairs and %d steep ones; the scan is not exercising the rule", sc, pairs, steepPairs)
		}
	}
}

// steep reports whether a link is a column-one, row-two knight's move, which is
// drawn as an exact diagonal and has no choice of cells.
func steep(l game.Link) bool {
	from, to := l.Ends()
	return abs(from.Col-to.Col) == 1
}

// drawnGaps reports the canvas columns strictly between a link's ends that carry
// no link glyph on an already-painted canvas.
func drawnGaps(sc Scale, cv *canvas, l game.Link) []int {
	from, to := l.Ends()
	x1, x2 := sc.holeX(from.Col), sc.holeX(to.Col)
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	var gaps []int
	for x := x1 + 1; x < x2; x++ {
		painted := false
		for y := range cv.h {
			if isLinkGlyph(cv.runes[y*cv.w+x]) {
				painted = true
				break
			}
		}
		if !painted {
			gaps = append(gaps, x)
		}
	}
	return gaps
}

// paintAlone draws one link on an otherwise empty board and returns the canvas
// cells it put a link glyph in.
func paintAlone(sc Scale, n int, l game.Link) map[int]bool {
	cv, _ := paintLinks(sc, n, l)
	out := map[int]bool{}
	for i, r := range cv.runes {
		if isLinkGlyph(r) {
			out[i] = true
		}
	}
	return out
}

// allLinks returns every link the board admits, steep and shallow alike.
func allLinks(n int) []game.Link {
	inside := func(p game.Point) bool {
		if p.Col < 0 || p.Row < 0 || p.Col >= n || p.Row >= n {
			return false
		}
		return !((p.Col == 0 || p.Col == n-1) && (p.Row == 0 || p.Row == n-1))
	}
	var out []game.Link
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if !inside(p) {
				continue
			}
			for d := game.Dir(0); d < game.NumDirs; d++ {
				if !d.IsCanonical() {
					continue
				}
				q := p.Add(d)
				if !inside(q) {
					continue
				}
				if l, ok := game.NewLink(p, q); ok {
					out = append(out, l)
				}
			}
		}
	}
	return out
}

// TestACrossingReadsAsOneUnderThePPRuleset is the reproduction, through a
// rendered frame: two of one player's links crossing, which the paper-and-pencil
// ruleset allows, used to come out as a pair of tee junctions claiming a
// horizontal connection between two pegs two columns apart in the same row,
// which is not a knight's move and can never be a link.
func TestACrossingReadsAsOneUnderThePPRuleset(t *testing.T) {
	rs := game.PP
	rs.Size = 12
	g := game.MustNew(rs)
	for _, p := range []game.Point{
		{Col: 3, Row: 4}, {Col: 9, Row: 2},
		{Col: 5, Row: 3}, {Col: 0, Row: 9},
		{Col: 3, Row: 3}, {Col: 10, Row: 5},
		{Col: 5, Row: 4},
	} {
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("move %v: %v", p, err)
		}
	}
	first, _ := game.NewLink(game.Point{Col: 3, Row: 4}, game.Point{Col: 5, Row: 3})
	second, _ := game.NewLink(game.Point{Col: 3, Row: 3}, game.Point{Col: 5, Row: 4})
	if g.LinkOwner(first) != game.Vertical || g.LinkOwner(second) != game.Vertical {
		t.Fatal("the fixture does not hold both of the crossing links")
	}
	if !game.LinksCross(first, second) {
		t.Fatal("the fixture's links do not cross, so it is not this reproduction")
	}
	for _, sc := range []Scale{Compact, Detail} {
		lines := renderPlain(t, &BoardView{Scale: sc}, g)
		if bad := junctionCells(t, sc, g.Size(), lines); len(bad) != 0 {
			t.Errorf("%s: the only two links on the board cross, yet %v draw them meeting:\n%s",
				sc, bad, strings.Join(lines, "\n"))
		}
		crossings := 0
		for _, l := range lines {
			crossings += strings.Count(l, string(glyphCross))
		}
		if crossings == 0 {
			t.Errorf("%s: nothing on the frame says the two links cross:\n%s", sc, strings.Join(lines, "\n"))
		}
		for _, l := range []game.Link{first, second} {
			from, to := l.Ends()
			if gaps, frame := strokeGaps(t, sc, g, from, to); len(gaps) != 0 {
				t.Errorf("%s: %v has no stroke in column(s) %v\n%s", sc, l, gaps, frame)
			}
		}
	}
}

// paintLinks draws a set of links over a board of holes and pegs and returns the
// canvas together with every cell more than one of them reached.
func paintLinks(sc Scale, n int, links ...game.Link) (*canvas, map[int][]game.Link) {
	cw, ch := sc.CanvasSize(n)
	cv := newCanvas(cw, ch)
	for row := range n {
		for col := range n {
			if (col == 0 || col == n-1) && (row == 0 || row == n-1) {
				continue
			}
			cv.set(sc.holeX(col), sc.holeY(row), glyphHole, styHole)
		}
	}
	for _, l := range links {
		for _, p := range []game.Point{l.From, l.To()} {
			cv.set(sc.holeX(p.Col), sc.holeY(p.Row), glyphPegVertical, styPegVertical)
		}
	}
	reached := map[int][]game.Link{}
	for _, l := range links {
		before := make([]linkBits, len(cv.bits))
		copy(before, cv.bits)
		sc.drawLink(cv, l, styLinkVertical)
		for i := range cv.bits {
			if cv.bits[i].edges() != before[i].edges() {
				reached[i] = append(reached[i], l)
			}
		}
	}
	cv.resolveLinks()
	shared := map[int][]game.Link{}
	for i, ls := range reached {
		if len(ls) > 1 {
			shared[i] = ls
		}
	}
	return cv, shared
}

// TestThreeLinksAreNeverDrawnAsAFalseJunction is the case pairwise reasoning
// cannot see. A cell recorded only the first link that reached it, so a third
// arrival was compared with the first and never with the second: three links
// competing for one cell could be drawn as a junction on the strength of one
// pair while another pair met at no peg. A junction says the lines it joins are
// connected, so drawing one there changes what the board says about the game.
//
// The rule asserted here is the honest one and does not depend on how the
// routing happens to place a step: wherever a cell is reached by two links that
// share no peg, that cell may not be drawn as a junction. A crossing mark is
// allowed, being the picture of lines that do not meet.
func TestThreeLinksAreNeverDrawnAsAFalseJunction(t *testing.T) {
	const n = 8
	for _, sc := range []Scale{Compact, Detail} {
		links := shallowLinks(n)
		triples := 0
		for i := range links {
			for j := i + 1; j < len(links); j++ {
				for k := j + 1; k < len(links); k++ {
					set := []game.Link{links[i], links[j], links[k]}
					if !anyPairMeetsNowhere(set) {
						continue
					}
					triples++
					cv, shared := paintLinks(sc, n, set...)
					for cell, reached := range shared {
						if !anyPairMeetsNowhere(reached) {
							continue
						}
						if isJunctionGlyph(cv.runes[cell]) {
							t.Fatalf("%s: %v reached cell (%d,%d) and a pair of them meets at no peg, yet it draws as the junction %q",
								sc, reached, cell%cv.w, cell/cv.w, cv.runes[cell])
						}
					}
				}
			}
		}
		if triples < 5000 {
			t.Fatalf("%s: only %d triples examined, so the scan is not the exhaustive one", sc, triples)
		}
	}
}

// anyPairMeetsNowhere reports whether some pair in a set of links shares no peg,
// which is what makes a junction between them a lie.
func anyPairMeetsNowhere(links []game.Link) bool {
	for i := range links {
		for j := i + 1; j < len(links); j++ {
			if !sharesEnd(links[i], links[j]) {
				return true
			}
		}
	}
	return false
}

// isJunctionGlyph reports whether a rune is one of the box-drawing pieces that
// joins three or more edges, which is the picture of lines meeting.
func isJunctionGlyph(r rune) bool {
	for b, j := range junction {
		if j != 0 && r == j && linkBits(b).joins() {
			return true
		}
	}
	return false
}

// TestNoFalseJunctionInRealPositions asserts the same rule on positions a game
// actually reaches, at every supported board size, reading the contributors out
// of the canvas the real paint produced so the instrument cannot disagree with
// what was drawn.
//
// The exhaustive triple scan works on links placed by hand; this one covers what
// competition for cells looks like when a board fills up. Stranger pairs do share
// cells often — a few thousand times over these positions at the compact scale —
// which is what makes the assertion worth making: sharing is normal, and the rule
// is only that such a cell is never drawn as lines meeting.
func TestNoFalseJunctionInRealPositions(t *testing.T) {
	for _, n := range []int{8, 12, 18, 24} {
		for _, sc := range []Scale{Compact, Detail} {
			shared, bridges := 0, 0
			for seed := range 200 {
				g := crowdedBoard(t, n, int64(seed)+int64(n)*1000)
				cv := (&BoardView{Scale: sc}).paint(g)
				for i := range cv.bits {
					// A peg carrying a run is drawn through, and only a run: a
					// corner or a junction on a peg would say the line turns or
					// branches at that peg, which is a different untruth from
					// passing through it. A link may step on its own end peg, so
					// this is reachable rather than defensive.
					if r := cv.runes[i]; r == glyphPegVerticalBridge || r == glyphPegHorizontalBridge {
						bridges++
						if e := cv.bits[i].edges(); e != linkE|linkW {
							t.Fatalf("n=%d %s seed=%d: cell (%d,%d) draws a peg bridged by %q but its edges are %04b, not a straight run",
								n, sc, seed, i%cv.w, i/cv.w, r, e)
						}
					}
					refs := cv.refsAt(i)
					if len(refs) < 2 {
						continue
					}
					ls := make([]game.Link, 0, len(refs))
					for _, r := range refs {
						ls = append(ls, cv.links[r-1])
					}
					if !anyPairMeetsNowhere(ls) {
						continue
					}
					shared++
					if isJunctionGlyph(cv.runes[i]) {
						t.Fatalf("n=%d %s seed=%d: %v reached cell (%d,%d) and a pair of them meets at no peg, yet it draws as the junction %q",
							n, sc, seed, ls, i%cv.w, i/cv.w, cv.runes[i])
					}
				}
			}
			// Guards against the sweep quietly measuring nothing.
			if sc == Compact && shared == 0 {
				t.Fatalf("n=%d compact: no stranger pair ever shared a cell, so this proved nothing", n)
			}
			if sc == Compact && bridges == 0 {
				t.Fatalf("n=%d compact: no run ever crossed a peg, so the bridge rule proved nothing", n)
			}
		}
	}
}

// crowdedBoard plays pseudo-random legal moves until the board is half full or
// the game ends, giving a position with many links competing for cells.
func crowdedBoard(t *testing.T, n int, seed int64) *game.Game {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	rs := game.Std
	rs.Size = n
	g := game.MustNew(rs)
	for range n * n / 2 {
		if _, err := g.PlayPeg(game.Point{Col: rng.Intn(n), Row: rng.Intn(n)}); err != nil {
			continue
		}
		if g.Result().Over() {
			break
		}
	}
	return g
}

// TestAPegOnEitherMidpointDoesNotBreakTheLink covers the cells a shallow link's
// horizontal run has to cross: the two holes in the column between its ends. A
// peg keeps its cell, so with one of them occupied the run goes round by stepping
// on the other side. With both occupied there is no free cell left, and the run is
// drawn through one of the pegs rather than stopped by it: the peg glyph carries
// the run through it and keeps the filled-or-hollow distinction that names the
// owner, so the link stays whole and the peg stays a peg of the right colour.
//
// The link must come out unbroken in every one of those cases. Blessing a gap
// here would be blessing exactly the appearance this whole change set exists to
// remove.
func TestAPegOnEitherMidpointDoesNotBreakTheLink(t *testing.T) {
	from := game.Point{Col: 3, Row: 3}
	for _, d := range []game.Dir{game.ENE, game.ESE} {
		to := from.Add(d)
		upper := game.Point{Col: (from.Col + to.Col) / 2, Row: from.Row}
		lower := game.Point{Col: upper.Col, Row: to.Row}
		for _, blocked := range [][]game.Point{{upper}, {lower}, {upper, lower}} {
			for _, owner := range []game.Player{game.Vertical, game.Horizontal} {
				g := linkedPairWith(t, from, to, owner, blocked...)
				if gaps, frame := strokeGaps(t, Compact, g, from, to); len(gaps) != 0 {
					t.Errorf("%v with %v pegs at %v: column(s) %v carry no stroke\n%s",
						d, owner, blocked, gaps, frame)
				}
				lines := renderPlain(t, &BoardView{Scale: Compact}, g)
				for _, p := range blocked {
					got := cellAt(t, lines, g.Size(), Compact.holeX(p.Col), Compact.holeY(p.Row))
					mine, theirs := pegGlyphsFor(owner)
					if !slices.Contains(mine, got) {
						t.Errorf("%v with a %v peg at %v: renders %q, which is not a %v peg",
							d, owner, p, got, owner)
					}
					if slices.Contains(theirs, got) {
						t.Errorf("%v with a %v peg at %v: renders %q, which reads as the other player",
							d, owner, p, got)
					}
				}
			}
		}
	}
}

// pegGlyphsFor returns the glyphs that mean a peg of this player, and those that
// mean the other one, so a test can check the owner survived as well as the peg.
func pegGlyphsFor(p game.Player) (mine, theirs []rune) {
	vertical := []rune{glyphPegVertical, glyphPegVerticalLast, glyphPegVerticalBridge}
	horizontal := []rune{glyphPegHorizontal, glyphPegHorizontalLast, glyphPegHorizontalBridge}
	if p == game.Horizontal {
		return horizontal, vertical
	}
	return vertical, horizontal
}

// linkedPairWith plays a vertical link from-to and gives the named holes to one
// player or the other, so a midpoint can be blocked by either colour. Vertical
// moves first, so the fillers are interleaved to put each peg in the right hands
// without either side forming a link of its own near the one under test.
func linkedPairWith(t *testing.T, from, to game.Point, owner game.Player, blocked ...game.Point) *game.Game {
	t.Helper()
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	// Far corner holes for whichever side needs to lose a turn. Vertical may not
	// play the border columns and Horizontal may not play the border rows, so
	// each filler is legal only for its own side.
	vFill := []game.Point{{Col: 5, Row: 0}, {Col: 7, Row: 0}, {Col: 9, Row: 0}}
	hFill := []game.Point{{Col: 0, Row: 5}, {Col: 0, Row: 7}, {Col: 0, Row: 9}}
	var vi, hi int
	play := func(p game.Point, want game.Player) {
		for g.Turn() != want {
			var filler game.Point
			if g.Turn() == game.Vertical {
				filler, vi = vFill[vi], vi+1
			} else {
				filler, hi = hFill[hi], hi+1
			}
			if _, err := g.PlayPeg(filler); err != nil {
				t.Fatalf("filler at %v: %v", filler, err)
			}
		}
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("%v for %v: %v", p, want, err)
		}
	}
	play(from, game.Vertical)
	for _, p := range blocked {
		play(p, owner)
	}
	play(to, game.Vertical)

	l, ok := game.NewLink(from, to)
	if !ok {
		t.Fatalf("%v-%v is not a knight's move", from, to)
	}
	if g.LinkOwner(l) != game.Vertical {
		t.Fatalf("no vertical link formed between %v and %v with pegs at %v", from, to, blocked)
	}
	return g
}

// unpairedBrackets lists every overlay bracket in a frame that has no partner.
// A pair encloses its hole, so a left mark's partner stands two cells to its
// right. Half a pair is not a smaller version of a mark: it points at a hole
// from one side, and next to another mark it reads as a pair enclosing the hole
// between the two, which is the wrong hole.
func unpairedBrackets(t *testing.T, sc Scale, n int, lines []string) []string {
	t.Helper()
	partner := map[rune]struct {
		want rune
		at   int
	}{
		glyphCursorLeft:  {glyphCursorRight, 2},
		glyphCursorRight: {glyphCursorLeft, -2},
		glyphMarkLeft:    {glyphMarkRight, 2},
		glyphMarkRight:   {glyphMarkLeft, -2},
	}
	cw, ch := sc.CanvasSize(n)
	var bad []string
	for y := range ch {
		for x := range cw {
			got := cellAt(t, lines, n, x, y)
			p, ok := partner[got]
			if !ok {
				continue
			}
			if cellAt(t, lines, n, x+p.at, y) != p.want {
				bad = append(bad, fmt.Sprintf("%q at (%d,%d) with no %q beside it", got, x, y, p.want))
			}
		}
	}
	return bad
}

// TestAdjacentHighlightsEachCarryTheirOwnMark is the run-of-highlights defect
// made precise. Holes are two cells apart at the compact scale, so the pair
// around one highlighted hole wants the cell the next hole's pair wants, and
// adjacent highlights are ordinary: a lesson marks a whole border row, a hint
// marks a route.
//
// Three properties. Every marked hole reads as marked — the run may not contain
// a hole that looks untouched. No bracket stands without its partner, whatever
// the marks had to give up. And where there is room for pairs, pairs are still
// what a highlight gets: the detail scale's holes are four cells apart and
// contend for nothing, so falling back to a mark there would be throwing away
// the clearer form for no reason.
func TestAdjacentHighlightsEachCarryTheirOwnMark(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	var run []game.Point
	for col := 1; col < 11; col++ {
		run = append(run, game.Point{Col: col, Row: 0})
	}
	// One cursor inside the run, competing for the same cells, and one well
	// away from it.
	for _, cursor := range []game.Point{{Col: 3, Row: 0}, {Col: 5, Row: 5}} {
		for _, sc := range []Scale{Compact, Detail} {
			bv := &BoardView{Scale: sc, ShowCursor: true, Cursor: cursor, Highlights: run}
			lines := renderPlain(t, bv, g)
			frame := strings.Join(lines, "\n")
			for _, p := range run {
				if !highlightShown(t, lines, g.Size(), sc, p) {
					t.Errorf("%s, cursor on %v: nothing marks the highlighted hole %v:\n%s",
						sc, cursor, p, frame)
				}
			}
			if bad := unpairedBrackets(t, sc, g.Size(), lines); len(bad) != 0 {
				t.Errorf("%s, cursor on %v: %v:\n%s", sc, cursor, bad, frame)
			}

			// A run reads as a run only if it is drawn one way throughout.
			// Handing the contended cell to whichever mark asked first drew
			// alternate holes as pairs and the rest as marks, which is two
			// kinds of highlight in one row and no help to anybody.
			forms := map[string][]game.Point{}
			for _, p := range run {
				if p == cursor {
					continue // the cursor has taken the pair, by precedence
				}
				form := "a mark on the hole"
				if cellAt(t, lines, g.Size(), sc.holeX(p.Col)-1, sc.holeY(p.Row)) == glyphMarkLeft {
					form = "a bracket pair"
				}
				forms[form] = append(forms[form], p)
			}
			if len(forms) > 1 {
				t.Errorf("%s, cursor on %v: the run is drawn two ways at once, %v:\n%s",
					sc, cursor, forms, frame)
			}
			if sc != Detail {
				continue
			}
			for _, p := range run {
				if p == cursor {
					continue // the cursor has precedence over the pair there
				}
				x, y := sc.holeX(p.Col), sc.holeY(p.Row)
				if cellAt(t, lines, g.Size(), x-1, y) != glyphMarkLeft ||
					cellAt(t, lines, g.Size(), x+1, y) != glyphMarkRight {
					t.Errorf("detail, cursor on %v: %v lost its brackets although its neighbours' are two cells clear:\n%s",
						cursor, p, frame)
				}
			}
		}
	}
}

// TestBorderLabelsCarryTheirOwnersColour holds the board to what the panel
// says in words: vertical joins the top and bottom rows, horizontal the left
// and right columns. Those four labels are the only ones that name an edge
// somebody has to reach, and each takes its owner's colour — the colour that
// owner's pegs carry, so a scheme cannot say one thing with its pegs and
// another with its edges. Every other label keeps the neutral one.
//
// The colours here are the test's own, not a scheme's: what is asserted is
// which label gets whose colour, not what the colours are.
func TestBorderLabelsCarryTheirOwnersColour(t *testing.T) {
	g := hubGame(t)
	n := g.Size()
	st := Styles{
		Hole:          lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		PegVertical:   lipgloss.NewStyle().Foreground(lipgloss.Color("201")).Bold(true),
		PegHorizontal: lipgloss.NewStyle().Foreground(lipgloss.Color("202")).Bold(true),
		Label:         lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
	}
	bv := &BoardView{Scale: Compact}
	cw, ch := Compact.CanvasSize(n)
	lines := bv.Render(g, &st, cw+gutterWidth(n), ch+1)

	// Each label is looked up by the text it is, which no escape sequence can
	// contain: the gutter numbers keep their padding, the letters are letters.
	letters := lines[0]
	last := game.ColumnName(n - 1)
	for _, c := range []struct {
		what  string
		line  string
		text  string
		owner string
	}{
		{"the top row's number", lines[1], " 1 ", "201"},
		{"the bottom row's number", lines[len(lines)-1], strconv.Itoa(n) + " ", "201"},
		{"an inner row's number", lines[5], " 5 ", "203"},
		{"the left column's letter", letters, "A", "202"},
		{"the right column's letter", letters, last, "202"},
		{"an inner column's letter", letters, "F", "203"},
	} {
		i := strings.Index(c.line, c.text)
		if i < 0 {
			t.Fatalf("%s (%q) is not on the frame: %q", c.what, c.text, c.line)
		}
		sgr := sgrBefore(c.line, i)
		if !strings.Contains(sgr, c.owner) {
			t.Errorf("%s is styled %q, want the colour %s", c.what, sgr, c.owner)
		}
		if strings.Contains(sgr, "\x1b[1m") {
			t.Errorf("%s is bold: a label is a coordinate, not a peg", c.what)
		}
	}
}

// TestBorderOwnershipAddsNoGlyphs is the other half of the same change. Border
// ownership is the one distinction on this board carried by colour alone, which
// is a debt: with colour off it is not there. What must not happen in exchange
// is noise — a marker pressed into the one spare gutter column, or a label
// bracketed — so with colour off every label is exactly the coordinate it
// names and nothing else.
func TestBorderOwnershipAddsNoGlyphs(t *testing.T) {
	g := hubGame(t)
	n := g.Size()
	gutter := gutterWidth(n)
	bv := &BoardView{Scale: Compact}
	lines := renderPlain(t, bv, g)

	var want strings.Builder
	want.WriteString(strings.Repeat(" ", gutter))
	for col := range n {
		for want.Len() < gutter+Compact.holeX(col) {
			want.WriteByte(' ')
		}
		want.WriteString(game.ColumnName(col))
	}
	if got := lines[0]; got != strings.TrimRight(want.String(), " ") {
		t.Errorf("letters row is %q, want just the column names %q", got, want.String())
	}
	for y, line := range lines[1:] {
		label := line
		if len(label) > gutter {
			label = label[:gutter]
		}
		if y%Compact.rowStep != 0 {
			if strings.TrimSpace(label) != "" {
				t.Errorf("canvas row %d carries the gutter text %q, want blank", y, label)
			}
			continue
		}
		if got, want := label, pad(strconv.Itoa(y/Compact.rowStep+1), gutter); got != want {
			t.Errorf("gutter of board row %d is %q, want just the number %q", y/Compact.rowStep+1, got, want)
		}
	}
}
