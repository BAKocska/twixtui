package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestPegsHolesCornersDistinguishableWithoutColour(t *testing.T) {
	g := hubGame(t)
	for _, sc := range []Scale{Compact, Detail} {
		bv := &BoardView{
			Scale:      sc,
			Cursor:     game.Point{Col: 5, Row: 8},
			ShowCursor: true,
			Highlights: []game.Point{{Col: 8, Row: 8}},
		}
		lines := renderPlain(t, bv, g)
		all := strings.Join(lines, "\n")
		if esc := strings.IndexByte(all, 0x1b); esc >= 0 {
			t.Fatalf("%s: plain render contains an escape byte at %d", sc, esc)
		}
		counts := map[rune]int{}
		for _, r := range all {
			counts[r]++
		}
		if counts[glyphPegVertical] != 9 {
			t.Errorf("%s: %d vertical pegs rendered, want 9", sc, counts[glyphPegVertical])
		}
		if counts[glyphPegHorizontal] != 8 {
			t.Errorf("%s: %d horizontal pegs rendered, want 8", sc, counts[glyphPegHorizontal])
		}
		// Every hole is accounted for: an occupied one shows its peg, an empty
		// one shows its dot unless a shallow link legitimately crosses the cell.
		// The detail scale has a spare row for every crossing, so there it must
		// never come to that and all 123 dots survive.
		covered := 0
		for row := range g.Size() {
			for col := range g.Size() {
				p := game.Point{Col: col, Row: row}
				if !g.Exists(p) {
					continue
				}
				got := cellAt(t, lines, g.Size(), sc.holeX(col), sc.holeY(row))
				switch g.At(p) {
				case game.Vertical:
					if got != glyphPegVertical {
						t.Errorf("%s: %v holds a vertical peg but renders %q", sc, p, got)
					}
				case game.Horizontal:
					if got != glyphPegHorizontal {
						t.Errorf("%s: %v holds a horizontal peg but renders %q", sc, p, got)
					}
				default:
					switch {
					case got == glyphHole:
					case isLinkGlyph(got):
						covered++
					default:
						t.Errorf("%s: empty hole %v renders %q, want a dot or a link stroke", sc, p, got)
					}
				}
			}
		}
		if sc == Detail && covered != 0 {
			t.Errorf("detail: %d hole dots were covered by link strokes; the scale has room for every crossing", covered)
		}
		if counts[glyphHole]+covered != 12*12-4-17 {
			t.Errorf("%s: %d dots plus %d covered holes, want %d holes in total",
				sc, counts[glyphHole], covered, 12*12-4-17)
		}
		if counts[glyphCursorLeft] != 1 || counts[glyphCursorRight] != 1 {
			t.Errorf("%s: cursor brackets not rendered exactly once", sc)
		}
		if counts[glyphMarkLeft] != 1 || counts[glyphMarkRight] != 1 {
			t.Errorf("%s: highlight brackets not rendered exactly once", sc)
		}
		// Corners are absent: the top-left board cell must be blank.
		if got := cellAt(t, lines, g.Size(), sc.holeX(0), sc.holeY(0)); got != ' ' {
			t.Errorf("%s: corner hole rendered as %q, want blank", sc, got)
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
		if !strings.ContainsRune(all, glyphCursorLeft) || !strings.ContainsRune(all, glyphCursorRight) {
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
	for _, sc := range []Scale{Compact, Detail} {
		bv := &BoardView{
			Scale:      sc,
			Cursor:     game.Point{Col: 5, Row: 8},
			ShowCursor: true,
			Highlights: []game.Point{{Col: 8, Row: 8}},
		}
		got := strings.Join(renderPlain(t, bv, g), "\n") + "\n"
		path := filepath.Join("testdata", "hub_"+sc.String()+".golden")
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
			t.Fatalf("golden file missing (run with -update after eyeballing): %v", err)
		}
		if got != string(want) {
			t.Errorf("%s frame drifted from golden:\ngot:\n%s\nwant:\n%s", sc, got, want)
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
