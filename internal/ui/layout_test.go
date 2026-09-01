package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
)

// frameAt renders a fresh standard-board demo at the given size and returns
// the frame. It is the whole pipeline: Arrange, BoardView.Render, Compose.
func frameAt(t *testing.T, w, h int) string {
	t.Helper()
	d, err := NewDemo(game.Std)
	if err != nil {
		t.Fatal(err)
	}
	d.styles = PlainStyles()
	d.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return d.View().Content
}

// checkFrameFits asserts the size half of the rendering contract: never wider
// than w, never taller than h. It is separate because the Compose tests feed
// the frame board rows that are not a board.
func checkFrameFits(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: %d lines emitted", w, h, len(lines))
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%dx%d: line %d is %d cells wide: %q", w, h, i, lw, l)
		}
	}
}

// checkFrameInvariants asserts the inviolable rendering contract: within the
// terminal's size, and either a board or the explicit too-small notice.
func checkFrameInvariants(t *testing.T, frame string, w, h int) {
	t.Helper()
	checkFrameFits(t, frame, w, h)
	lines := strings.Split(frame, "\n")
	board := strings.ContainsRune(frame, glyphHole)
	notice := tooSmallNotice(frame)
	if !board && !notice {
		t.Errorf("%dx%d: neither board nor too-small notice present:\n%s", w, h, frame)
	}
	if board && w >= MinWidth && h >= MinHeight {
		// The first line is the letters row; some column label must be visible
		// whatever the scroll position.
		if !strings.ContainsFunc(lines[0], func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
			t.Errorf("%dx%d: board shown without column labels: %q", w, h, lines[0])
		}
	}
}

// tooSmallNotice reports whether the frame is the too-small notice rather than
// a board. The notice trims its wording to the width it has, so this asks for
// any of its forms rather than for one fixed sentence.
func tooSmallNotice(frame string) bool {
	for _, line := range strings.Split(frame, "\n") {
		switch strings.TrimSpace(line) {
		case "terminal too small", "too small", "small", "!":
			return true
		}
	}
	return false
}

func TestFrameInvariantsAcrossSizeMatrix(t *testing.T) {
	type size struct{ w, h int }
	sizes := []size{
		{1, 1}, {5, 3}, {20, 8}, {40, 12}, {60, 20}, {80, 24}, {120, 40}, {200, 60},
	}
	for w := 1; w <= 211; w += 10 {
		for h := 1; h <= 71; h += 7 {
			sizes = append(sizes, size{w, h})
		}
	}
	for _, s := range sizes {
		checkFrameInvariants(t, frameAt(t, s.w, s.h), s.w, s.h)
	}
}

func TestArrangePicksSchemesAndPanels(t *testing.T) {
	cases := []struct {
		w, h   int
		scale  Scale
		panel  PanelPlacement
		marker string
	}{
		// 80x24: compact board with a side panel; the board itself needs a
		// vertical viewport (block is 52x26).
		{80, 24, Compact, PanelSide, "A B C"},
		// 120x40: compact fits fully, detail does not (needs 98x49).
		{120, 40, Compact, PanelSide, "A B C"},
		// 200x60: detail fits with room for a side panel.
		{200, 60, Detail, PanelSide, "A   B   C"},
		// 60x20: no room beside or below the board.
		{60, 20, Compact, PanelNone, "A B C"},
		// 52x34: board exactly as wide as the terminal, panel drops below.
		{52, 34, Compact, PanelBottom, "A B C"},
	}
	for _, c := range cases {
		arr := Arrange(c.w, c.h, 24)
		if arr.TooSmall {
			t.Errorf("%dx%d: unexpectedly too small", c.w, c.h)
			continue
		}
		if arr.Scale != c.scale {
			t.Errorf("%dx%d: scale %s, want %s", c.w, c.h, arr.Scale, c.scale)
		}
		if arr.Panel != c.panel {
			t.Errorf("%dx%d: panel %d, want %d", c.w, c.h, arr.Panel, c.panel)
		}
		frame := frameAt(t, c.w, c.h)
		if !strings.Contains(frame, c.marker) {
			t.Errorf("%dx%d: frame lacks scheme marker %q", c.w, c.h, c.marker)
		}
		hasPanel := strings.Contains(frame, "twixt sandbox")
		if (c.panel != PanelNone) != hasPanel {
			t.Errorf("%dx%d: panel presence = %v, want %v", c.w, c.h, hasPanel, c.panel != PanelNone)
		}
	}
}

func TestTooSmallStateIsExplicit(t *testing.T) {
	for _, s := range [][2]int{{19, 24}, {80, 5}, {5, 3}, {1, 1}} {
		frame := frameAt(t, s[0], s[1])
		if strings.ContainsRune(frame, glyphHole) {
			t.Errorf("%dx%d: board rendered below the minimum size", s[0], s[1])
		}
		if !tooSmallNotice(frame) {
			t.Errorf("%dx%d: no too-small notice:\n%q", s[0], s[1], frame)
		}
	}
}

// TestTooSmallNoticeIsNeverCutMidWord holds the one property this notice must
// have. It is shown because the terminal is under the minimum, so at nearly
// every width it can appear at the full sentence does not fit, and a notice cut
// to "terminal too sm" describes the program rather than the window. Every word
// on screen therefore has to be a whole word.
//
// The vocabulary is closed on purpose. A form quoting a stale bound fails here
// as surely as a cut one, because the size below is derived from the very
// constants that put the frame into this state.
func TestTooSmallNoticeIsNeverCutMidWord(t *testing.T) {
	size := strconv.Itoa(MinWidth) + "x" + strconv.Itoa(MinHeight)
	whole := map[string]bool{
		"terminal": true, "too": true, "small": true, "need": true, size: true, "!": true,
	}
	for w := 1; w < MinWidth; w++ {
		for _, h := range []int{1, 2, MinHeight - 1, MinHeight, 24} {
			frame := frameAt(t, w, h)
			said := false
			for _, line := range strings.Split(frame, "\n") {
				for _, word := range strings.Fields(line) {
					said = true
					if !whole[word] {
						t.Errorf("%dx%d: %q is not a whole word of the notice:\n%s", w, h, word, frame)
					}
				}
			}
			if !said {
				t.Errorf("%dx%d: the notice says nothing at all", w, h)
			}
		}
	}
}

// TestASidePanelUsesEveryRowBesideTheBoard is the wasted-space defect made
// precise. At 130x38 a 24-hole board is drawn compact, 26 rows of the 37 above
// the status line, and the panel used to be given the board's height: the key
// help was cut in the middle of the list with eleven blank rows underneath it,
// which reads as a fault rather than as a fit.
func TestASidePanelUsesEveryRowBesideTheBoard(t *testing.T) {
	const w, h = 130, 38
	arr := Arrange(w, h, 24)
	if arr.Panel != PanelSide {
		t.Fatalf("%dx%d: panel placement %d, want beside the board", w, h, arr.Panel)
	}
	if arr.PanelH <= arr.BoardH {
		t.Errorf("%dx%d: the panel is given %d rows beside a %d-row board, leaving %d of the %d free rows unused",
			w, h, arr.PanelH, arr.BoardH, h-1-arr.PanelH, h-1-arr.BoardH)
	}
	if arr.PanelH != h-1 {
		t.Errorf("%dx%d: the panel has %d rows, want every row above the status line (%d)",
			w, h, arr.PanelH, h-1)
	}

	// The rows are not merely promised: Compose has to emit them.
	board := make([]string, arr.BoardH)
	panel := make([]string, arr.PanelH)
	for i := range panel {
		panel[i] = "panel " + strconv.Itoa(i)
	}
	frame := Compose(arr, board, panel, "status", nil)
	last := panel[len(panel)-1]
	if !strings.Contains(frame, last) {
		t.Errorf("%dx%d: the panel's last line %q is missing from the frame:\n%s", w, h, last, frame)
	}
	checkFrameFits(t, frame, w, h)
}

// TestPanelAndStatusCutsEndAtAWholeWord holds the rest of the frame to the rule
// the too-small notice already keeps. A line that fills its width and ends in
// the truncation mark was cut wherever the width fell: "○ horizontal
// intermedia…" for a seat, "r r…" for a status line whose last item was the
// resign key. Both lie about what they say — the first invents a word, the
// second a key — and the frame is where the last word on the matter is, so this
// asserts on what Compose emits rather than on how a screen built it.
//
// The vocabulary is the source line's own words and items, so a cut that
// invents anything fails, and so does one that keeps a fragment.
func TestPanelAndStatusCutsEndAtAWholeWord(t *testing.T) {
	const w, h = 80, 24
	arr := Arrange(w, h, 24)
	if arr.Panel != PanelSide {
		t.Fatalf("%dx%d: panel placement %d, want beside the board", w, h, arr.Panel)
	}
	// The two lines as their screens really build them: the seat line carries
	// the turn marker and the peg glyph, and the status line is the board keys
	// in order of usefulness, resign last.
	const seat = "  ○ horizontal intermediate bot"
	const hints = "space stage · enter confirm · x link mode · q quit game · ? hint · d draw · r resign"
	// What the screens hand over: their own lines, already cut to the space the
	// arrangement gave them.
	panel := []string{ansi.Truncate(seat, arr.PanelW, "…"), "thinking…"}
	status := ansi.Truncate(hints, arr.Width, "…")
	// The fixture is only about something if both cuts land inside a word: a
	// cut that happens to fall on a space would pass whatever Compose did.
	for _, c := range []struct{ what, got string }{{"the seat line", panel[0]}, {"the status line", status}} {
		body := strings.TrimSuffix(c.got, "…")
		if body == c.got || strings.HasSuffix(body, " ") {
			t.Fatalf("%s was not cut inside a word: %q", c.what, c.got)
		}
	}

	frame := Compose(arr, make([]string, arr.BoardH), panel, status, nil)
	checkFrameFits(t, frame, w, h)
	lines := strings.Split(frame, "\n")

	words := map[string]bool{}
	for _, word := range strings.Fields(seat) {
		words[word] = true
	}
	seatLine := ""
	for _, l := range lines {
		if strings.Contains(l, "horizontal") {
			seatLine = strings.TrimSpace(l)
		}
	}
	if seatLine == "" {
		t.Fatalf("the seat line is not on the frame:\n%s", frame)
	}
	if !strings.HasSuffix(seatLine, "…") {
		t.Errorf("the seat line %q no longer says it was shortened", seatLine)
	}
	for _, word := range strings.Fields(strings.TrimSuffix(seatLine, "…")) {
		if !words[word] {
			t.Errorf("the seat line reads %q, and %q is not a word of %q", seatLine, word, seat)
		}
	}

	items := map[string]bool{}
	for _, item := range strings.Split(hints, " · ") {
		items[item] = true
	}
	statusLine := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasSuffix(statusLine, "…") {
		t.Errorf("the status line %q no longer says it was shortened", statusLine)
	}
	for _, item := range strings.Split(strings.TrimSuffix(statusLine, "…"), " · ") {
		if !items[strings.TrimSpace(item)] {
			t.Errorf("the status line reads %q, and %q is not one of its items", statusLine, item)
		}
	}

	// A line that ends in the mark without reaching the width was not cut by
	// anything: it is a line whose author wrote a mark, and it keeps its words.
	if !strings.Contains(frame, "thinking…") {
		t.Errorf("a line ending in the mark but well inside the width was shortened anyway:\n%s", frame)
	}
}
