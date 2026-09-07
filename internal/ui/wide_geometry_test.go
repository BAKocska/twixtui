package ui

import (
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/charmbracelet/x/ansi"
)

// TestWideBoardFitsEveryTerminalSize is the widest board against the sizes a
// player really produces, down to the documented minimum. The header on a
// 48-column board takes two rows, so every consumer that budgets by
// Scale.BlockSize has to have followed it; a frame one row or one cell over its
// terminal corrupts the display, which is the failure a header-height change
// produces.
func TestWideBoardFitsEveryTerminalSize(t *testing.T) {
	rs := game.Std
	rs.Size = 48
	g, err := game.New(rs)
	if err != nil {
		t.Fatal(err)
	}
	st := PlainStyles()

	var bv BoardView
	for _, size := range [][2]int{
		{120, 46}, {100, 34}, {80, 24}, {60, 20}, {40, 14}, {24, 10}, {20, 8}, {20, 6}, {80, 24},
	} {
		w, h := size[0], size[1]
		arr := Arrange(w, h, rs.Size)
		if arr.TooSmall {
			t.Fatalf("%dx%d is within the documented minimum but was refused", w, h)
		}
		bv.Scale = arr.Scale
		bv.Cursor, bv.ShowCursor = game.Point{Col: 47, Row: 46}, true
		lines := bv.Render(g, &st, arr.BoardAvailW, arr.BoardAvailH)
		if !strings.Contains(strings.Join(lines, "\n"), "[·]") {
			t.Fatalf("%dx%d: the cursor's existing hole is not visible", w, h)
		}
		if len(lines) > h {
			t.Errorf("%dx%d: %d lines, more than the terminal has", w, h, len(lines))
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("%dx%d: line %d is %d cells wide", w, h, i, got)
			}
		}
		composed := strings.Split(Compose(arr, lines, []string{"vertical to move"}, "space place", &st), "\n")
		if len(composed) != h || !strings.Contains(composed[len(composed)-1], "space") {
			t.Fatalf("%dx%d: composition lost the status row", w, h)
		}
		for i, line := range composed {
			if ansi.StringWidth(line) > w {
				t.Errorf("%dx%d: composed row %d overflows", w, h, i)
			}
		}
		// The cursor's own column must be named in full, or the coordinate the
		// player is standing on is unreadable at exactly the moment it matters.
		want := game.ColumnName(47)
		header := lines[:min(headerRows(rs.Size), len(lines))]
		_, left := bv.Viewport()
		labels, _ := headerLabels(t, header, gutterWidth(rs.Size), left)
		if got := labels[bv.Scale.holeX(bv.Cursor.Col)]; got != want {
			t.Errorf("%dx%d: the cursor's column is named %q, want %q:\n%s", w, h, got, want, header)
		}
	}
}
