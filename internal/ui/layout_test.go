package ui

import (
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

// checkFrameInvariants asserts the inviolable rendering contract: never wider
// than w, never taller than h, and either a board or the explicit too-small
// notice is present.
func checkFrameInvariants(t *testing.T, frame string, w, h int) {
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
	board := strings.ContainsRune(frame, glyphHole)
	notice := strings.Contains(frame, clipTo("terminal too small", w))
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

func clipTo(s string, w int) string {
	if len(s) > w {
		return s[:w]
	}
	return s
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
		if !strings.Contains(frame, clipTo("terminal too small", s[0])) {
			t.Errorf("%dx%d: no too-small notice:\n%q", s[0], s[1], frame)
		}
	}
}
