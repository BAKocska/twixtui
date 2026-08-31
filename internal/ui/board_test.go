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

	// Centre hole (5,5) sits at canvas (11,5). Steep links are single
	// diagonals; the rising and falling shallow pairs merge to '=' beside the
	// peg and continue with scan strokes beyond.
	checks := []struct {
		x, y int
		want rune
		what string
	}{
		{12, 4, '╱', "NNE steep"},
		{10, 4, '╲', "NNW steep"},
		{10, 6, '╱', "SSW steep"},
		{12, 6, '╲', "SSE steep"},
		{12, 5, '=', "ENE+ESE beside peg"},
		{10, 5, '=', "WNW+WSW beside peg"},
		{14, 4, '⎼', "ENE far stroke"},
		{14, 6, '⎻', "ESE far stroke"},
		{8, 4, '⎼', "WNW far stroke"},
		{8, 6, '⎻', "WSW far stroke"},
		// Horizontal player's chain: A10-C11 is a falling shallow link.
		{2, 9, '⎼', "H chain ESE near stroke"},
		{4, 10, '⎻', "H chain ESE far stroke"},
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
		{22, 10, '=', "ENE+ESE beside peg"},
		{20, 10, '=', "WNW+WSW beside peg"},
		{23, 10, '⎺', "ENE ramp leaving row"},
		{24, 9, '⎼', "ENE ramp entering next row"},
		{25, 9, '─', "ENE ramp midpoint"},
		{26, 9, '⎻', "ENE ramp rising"},
		{28, 8, '⎼', "ENE ramp final stroke"},
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
		if counts[glyphHole] != 12*12-4-17 {
			t.Errorf("%s: %d empty holes rendered, want %d", sc, counts[glyphHole], 12*12-4-17)
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
