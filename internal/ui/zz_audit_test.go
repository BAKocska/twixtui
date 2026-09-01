package ui

// Temporary render-geometry audit scaffolding (review pass). This file is
// deleted before the audit report is finalised; it is not part of the suite.

import (
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

func zzRules(n int) game.Ruleset {
	rs := game.Std
	rs.Size = n
	return rs
}

func zzGame(t *testing.T, rs game.Ruleset, moves ...game.Point) *game.Game {
	t.Helper()
	g := game.MustNew(rs)
	for i, p := range moves {
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("move %d at %v: %v", i+1, p, err)
		}
	}
	return g
}

func zzCount(lines []string, r rune) int {
	n := 0
	for _, l := range lines {
		n += strings.Count(l, string(r))
	}
	return n
}

func zzFrame(lines []string) string { return strings.Join(lines, "\n") }

// Area 1: on the compact scale a steep link has exactly one stroke cell, and
// that cell is in a bracket column. A cursor or highlight parked on either
// hole flanking the diagonal is painted after the links and eats the stroke.
func TestZZAudit_BracketHidesEntireSteepLinkOnCompact(t *testing.T) {
	g := zzGame(t, zzRules(12),
		game.Point{Col: 3, Row: 3}, // V
		game.Point{Col: 9, Row: 9}, // H parked far away
		game.Point{Col: 4, Row: 1}, // V -> NNE link (3,3)-(4,1)
	)
	control := renderPlain(t, &BoardView{Scale: Compact}, g)
	if got := cellAt(t, control, g.Size(), 8, 2); got != glyphRise {
		t.Fatalf("control: cell (8,2) = %q, want the steep stroke", got)
	}
	if zzCount(control, glyphRise) != 1 {
		t.Fatalf("control: %d rise strokes, want 1", zzCount(control, glyphRise))
	}
	t.Logf("control frame:\n%s", zzFrame(control))

	cases := []struct {
		name string
		bv   BoardView
		eat  rune
	}{
		{"cursor on hole left of stroke", BoardView{Scale: Compact, ShowCursor: true, Cursor: game.Point{Col: 3, Row: 2}}, glyphCursorRight},
		{"cursor on hole right of stroke", BoardView{Scale: Compact, ShowCursor: true, Cursor: game.Point{Col: 4, Row: 2}}, glyphCursorLeft},
		{"highlight on hole left of stroke", BoardView{Scale: Compact, Highlights: []game.Point{{Col: 3, Row: 2}}}, glyphMarkRight},
	}
	for _, tc := range cases {
		bv := tc.bv
		lines := renderPlain(t, &bv, g)
		got := cellAt(t, lines, g.Size(), 8, 2)
		strokes := zzCount(lines, glyphRise) + zzCount(lines, glyphFall)
		t.Logf("%s: cell(8,2)=%q, %d steep strokes remain\n%s", tc.name, got, strokes, zzFrame(lines))
		if got != tc.eat {
			t.Errorf("%s: cell (8,2) = %q, want bracket %q", tc.name, got, tc.eat)
		}
		if strokes != 0 {
			t.Errorf("%s: %d steep strokes still visible, expected the whole link hidden", tc.name, strokes)
		}
	}
}

// Area 1: cursor and highlight brackets on horizontally adjacent holes share a
// cell; the cursor is painted last, leaving an unbalanced highlight bracket.
func TestZZAudit_CursorEatsAdjacentHighlightBracket(t *testing.T) {
	g := zzGame(t, zzRules(12),
		game.Point{Col: 4, Row: 4}, // V peg, highlighted as the staged peg is
		game.Point{Col: 9, Row: 9}, // H parked
	)
	bv := &BoardView{
		Scale:      Compact,
		ShowCursor: true,
		Cursor:     game.Point{Col: 3, Row: 4},
		Highlights: []game.Point{{Col: 4, Row: 4}},
	}
	lines := renderPlain(t, bv, g)
	t.Logf("frame:\n%s", zzFrame(lines))
	if got := cellAt(t, lines, g.Size(), 8, 4); got != glyphCursorRight {
		t.Errorf("cell (8,4) = %q, want %q (cursor bracket wins the shared cell)", got, glyphCursorRight)
	}
	if l, r := zzCount(lines, glyphMarkLeft), zzCount(lines, glyphMarkRight); l != 0 || r != 1 {
		t.Errorf("highlight brackets: %d left, %d right; expected the 0/1 unbalanced pair", l, r)
	}
}

// Area 2: a clipped render must be a pure window over the full canvas — every
// visible cell identical to the unclipped render, at every viewport origin.
func TestZZAudit_ViewportWindowsMatchFullCanvas(t *testing.T) {
	g := hubGame(t)
	st := PlainStyles()
	n := g.Size()
	gut := gutterWidth(n)
	for _, sc := range []Scale{Compact, Detail} {
		cw, ch := sc.CanvasSize(n)
		full := renderPlain(t, &BoardView{Scale: sc}, g)
		const vw, vh = 10, 5
		bad := 0
		for top := 0; top+vh <= ch; top++ {
			for left := 0; left+vw <= cw; left++ {
				bv := &BoardView{Scale: sc}
				bv.top, bv.left = top, left
				lines := bv.Render(g, &st, gut+vw, vh+1)
				if len(lines) != vh+1 {
					t.Fatalf("%s top=%d left=%d: %d lines, want %d", sc, top, left, len(lines), vh+1)
				}
				if gotTop, gotLeft := bv.Viewport(); gotTop != top || gotLeft != left {
					t.Fatalf("%s: viewport drifted to %d,%d", sc, gotTop, gotLeft)
				}
				for yi := 0; yi < vh; yi++ {
					row := []rune(lines[1+yi])
					for xi := 0; xi < vw; xi++ {
						got := ' '
						if gut+xi < len(row) {
							got = row[gut+xi]
						}
						want := cellAt(t, full, n, left+xi, top+yi)
						if got != want && bad < 5 {
							bad++
							t.Errorf("%s top=%d left=%d cell(%d,%d): clipped %q, full %q\n%s",
								sc, top, left, left+xi, top+yi, got, want, zzFrame(lines))
						}
					}
				}
			}
		}
	}
}

// Area 2, evidence only: a viewport cut between two linked pegs shows the peg
// without its stroke, flagged by the overflow arrow.
func TestZZAudit_FrameLinkCutByViewport(t *testing.T) {
	g := zzGame(t, zzRules(12),
		game.Point{Col: 3, Row: 3}, game.Point{Col: 9, Row: 9},
		game.Point{Col: 4, Row: 5}, // SSE steep link (3,3)-(4,5), stroke at (8,4)
	)
	st := PlainStyles()
	bv := &BoardView{Scale: Compact}
	lines := bv.Render(g, &st, gutterWidth(12)+8, 7) // vw=8: columns 0..7 only
	t.Logf("clipped frame (vw=8, stroke column 8 outside):\n%s", zzFrame(lines))
	if !strings.ContainsRune(lines[0], glyphRight) {
		t.Errorf("letters row lacks the right overflow arrow")
	}
	if zzCount(lines, glyphFall) != 0 {
		t.Errorf("stroke unexpectedly visible in the 8-column window")
	}
	if zzCount(lines, glyphPegVertical) == 0 {
		t.Errorf("from-peg should be visible in the window")
	}
}

// Area 3 (steep part): two genuinely crossing steep links, legal under the PP
// ruleset, must merge into the cross glyph exactly at the shared cell.
func TestZZAudit_GenuineSteepCrossing(t *testing.T) {
	rs := game.PP
	rs.Size = 12
	g := zzGame(t, rs,
		game.Point{Col: 3, Row: 3}, game.Point{Col: 9, Row: 2},
		game.Point{Col: 4, Row: 5}, game.Point{Col: 0, Row: 9},
		game.Point{Col: 3, Row: 5}, game.Point{Col: 10, Row: 5},
		game.Point{Col: 4, Row: 3},
	)
	a, okA := game.NewLink(game.Point{Col: 3, Row: 3}, game.Point{Col: 4, Row: 5})
	b, okB := game.NewLink(game.Point{Col: 3, Row: 5}, game.Point{Col: 4, Row: 3})
	if !okA || !okB {
		t.Fatal("fixture links are not knight moves")
	}
	if !game.LinksCross(a, b) {
		t.Fatal("fixture links do not cross geometrically")
	}
	if g.LinkOwner(a) != game.Vertical || g.LinkOwner(b) != game.Vertical {
		t.Fatalf("links missing: owner(a)=%v owner(b)=%v", g.LinkOwner(a), g.LinkOwner(b))
	}

	lines := renderPlain(t, &BoardView{Scale: Compact}, g)
	t.Logf("compact frame:\n%s", zzFrame(lines))
	if got := cellAt(t, lines, 12, 8, 4); got != glyphCross {
		t.Errorf("compact: crossing cell (8,4) = %q, want %q", got, glyphCross)
	}
	if c := zzCount(lines, glyphCross); c != 1 {
		t.Errorf("compact: %d cross glyphs, want 1", c)
	}

	linesD := renderPlain(t, &BoardView{Scale: Detail}, g)
	t.Logf("detail frame:\n%s", zzFrame(linesD))
	checks := []struct {
		x, y int
		want rune
	}{
		{15, 8, glyphCross},
		{14, 7, glyphFall}, {16, 9, glyphFall},
		{14, 9, glyphRise}, {16, 7, glyphRise},
	}
	for _, c := range checks {
		if got := cellAt(t, linesD, 12, c.x, c.y); got != c.want {
			t.Errorf("detail: cell (%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
	if c := zzCount(linesD, glyphCross); c != 1 {
		t.Errorf("detail: %d cross glyphs, want 1", c)
	}
}

// Area 3 (steep part), exhaustive: over every pair of steep links on an 8x8
// board, the cross glyph appears iff the links genuinely cross, and a genuine
// crossing is never rendered as two visually separated strokes.
func TestZZAudit_SteepPairMarkersExhaustive(t *testing.T) {
	const n = 8
	var links []game.Link
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			for _, d := range []game.Dir{game.NNE, game.SSE} {
				from := game.Point{Col: c, Row: r}
				to := from.Add(d)
				if to.Col < 0 || to.Col >= n || to.Row < 0 || to.Row >= n {
					continue
				}
				if l, ok := game.NewLink(from, to); ok {
					links = append(links, l)
				}
			}
		}
	}
	if len(links) < 50 {
		t.Fatalf("only %d steep links enumerated", len(links))
	}
	for _, sc := range []Scale{Compact, Detail} {
		cw, ch := sc.CanvasSize(n)
		cellsOf := func(l game.Link) map[[2]int]rune {
			cv := newCanvas(cw, ch)
			sc.drawLink(cv, l, styLinkVertical)
			m := map[[2]int]rune{}
			for y := 0; y < ch; y++ {
				for x := 0; x < cw; x++ {
					if r := cv.runes[y*cv.w+x]; r != ' ' {
						m[[2]int{x, y}] = r
					}
				}
			}
			return m
		}
		for i := 0; i < len(links); i++ {
			for j := i + 1; j < len(links); j++ {
				a, b := links[i], links[j]
				ca, cb := cellsOf(a), cellsOf(b)
				var shared [][2]int
				for k := range ca {
					if _, ok := cb[k]; ok {
						shared = append(shared, k)
					}
				}
				cv := newCanvas(cw, ch)
				sc.drawLink(cv, a, styLinkVertical)
				sc.drawLink(cv, b, styLinkVertical)
				var crossCells [][2]int
				for y := 0; y < ch; y++ {
					for x := 0; x < cw; x++ {
						if cv.runes[y*cv.w+x] == glyphCross {
							crossCells = append(crossCells, [2]int{x, y})
						}
					}
				}
				cross := game.LinksCross(a, b)
				if len(crossCells) > 0 && !cross {
					t.Errorf("%s: %v and %v do not cross but render %d cross glyphs at %v", sc, a, b, len(crossCells), crossCells)
				}
				if cross {
					if len(shared) > 0 {
						if len(crossCells) != len(shared) {
							t.Errorf("%s: %v x %v: shared cells %v but cross glyphs %v", sc, a, b, shared, crossCells)
						}
					} else {
						touch := false
						for ka := range ca {
							for kb := range cb {
								dx, dy := ka[0]-kb[0], ka[1]-kb[1]
								if dx < 0 {
									dx = -dx
								}
								if dy < 0 {
									dy = -dy
								}
								if dx <= 1 && dy <= 1 {
									touch = true
								}
							}
						}
						if !touch {
							t.Errorf("%s: %v and %v cross but their strokes are visually separated", sc, a, b)
						}
					}
				}
			}
		}
	}
}

// Area 6: every legal link on the smallest, standard and largest boards stays
// inside the canvas, never touches the margin columns or an absent corner
// cell, and never silently drops a stroke cell.
func TestZZAudit_AllLinksStayInCanvasAllSizes(t *testing.T) {
	for _, n := range []int{game.MinSize, 24, game.MaxSize} {
		for _, sc := range []Scale{Compact, Detail} {
			cw, ch := sc.CanvasSize(n)
			corner := map[[2]int]bool{
				{1, 0}: true, {cw - 2, 0}: true, {1, ch - 1}: true, {cw - 2, ch - 1}: true,
			}
			for r := 0; r < n; r++ {
				for c := 0; c < n; c++ {
					for d := game.Dir(0); d < game.NumDirs; d++ {
						if !d.IsCanonical() {
							continue
						}
						from := game.Point{Col: c, Row: r}
						to := from.Add(d)
						if to.Col < 0 || to.Col >= n || to.Row < 0 || to.Row >= n {
							continue
						}
						l, ok := game.NewLink(from, to)
						if !ok {
							continue
						}
						cv := newCanvas(cw, ch)
						sc.drawLink(cv, l, styLinkVertical)
						count := 0
						for y := 0; y < ch; y++ {
							for x := 0; x < cw; x++ {
								if cv.runes[y*cv.w+x] == ' ' {
									continue
								}
								count++
								if x < 1 || x > cw-2 {
									t.Errorf("n=%d %s link %v: stroke at (%d,%d) in margin or out of canvas", n, sc, l, x, y)
								}
								if corner[[2]int{x, y}] {
									t.Errorf("n=%d %s link %v: stroke on absent corner cell (%d,%d)", n, sc, l, x, y)
								}
							}
						}
						dCol, _ := d.Offset()
						want := sc.colStep*dCol - 1
						if count != want {
							t.Errorf("n=%d %s link %v: %d stroke cells, want %d (dropped or duplicated strokes)", n, sc, l, count, want)
						}
					}
				}
			}
		}
	}
}

// Area 4: a digit overlay replaces the peg glyph on its hole but cannot touch
// a steep stroke cell (digits sit on hole centres; steep strokes never do).
func TestZZAudit_DigitCoversPegButNotSteepStroke(t *testing.T) {
	g := zzGame(t, zzRules(12),
		game.Point{Col: 3, Row: 3}, game.Point{Col: 9, Row: 9},
		game.Point{Col: 4, Row: 5}, // SSE link (3,3)-(4,5), stroke at (8,4)
	)
	bv := &BoardView{Scale: Compact, Digits: map[game.Point]rune{{Col: 4, Row: 5}: '4'}}
	lines := renderPlain(t, bv, g)
	t.Logf("frame:\n%s", zzFrame(lines))
	if got := cellAt(t, lines, g.Size(), 9, 5); got != '4' {
		t.Errorf("digit target renders %q, want '4' replacing the peg", got)
	}
	if got := zzCount(lines, glyphPegVertical); got != 1 {
		t.Errorf("%d vertical pegs visible, want 1 (the digit hides the other)", got)
	}
	if got := cellAt(t, lines, g.Size(), 8, 4); got != glyphFall {
		t.Errorf("steep stroke cell (8,4) = %q, want %q untouched by the digit", got, glyphFall)
	}
}
