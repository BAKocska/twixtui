package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/BAKocska/twixtui/internal/game"
)

// press drives one key through the model, verifying the constructed message
// really produces the key string the keymap binds — so the bindings are proven
// against the real Bubble Tea key encoding, not a parallel invention.
func press(t *testing.T, d *Demo, key string) {
	t.Helper()
	var k tea.Key
	switch key {
	case "space":
		k = tea.Key{Code: tea.KeySpace, Text: " "}
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	default:
		r := []rune(key)
		if len(r) != 1 {
			t.Fatalf("press helper cannot encode %q", key)
		}
		k = tea.Key{Code: r[0], Text: key}
	}
	msg := tea.KeyPressMsg(k)
	if got := msg.String(); got != key {
		t.Fatalf("constructed key encodes as %q, want %q", got, key)
	}
	d.Update(msg)
}

func newDemo(t *testing.T, size int) *Demo {
	t.Helper()
	rs := game.Std
	rs.Size = size
	d, err := NewDemo(rs)
	if err != nil {
		t.Fatal(err)
	}
	d.styles = PlainStyles()
	return d
}

func maxLineWidth(frame string) int {
	w := 0
	for _, l := range strings.Split(frame, "\n") {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// TestResizeReshapesTheFrame drives resizes through Update and asserts the
// frame changed in ways only the new size can produce. If the model dropped
// the WindowSizeMsg, the frame would keep the old 80-cell bound and the
// compact letter pitch, and every assertion below would fail.
func TestResizeReshapesTheFrame(t *testing.T) {
	d := newDemo(t, 24)
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	small := d.View().Content
	checkFrameInvariants(t, small, 80, 24)
	if !strings.Contains(small, "A B C") {
		t.Fatal("80x24 frame is not the compact scheme")
	}

	d.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	large := d.View().Content
	checkFrameInvariants(t, large, 200, 60)
	if !strings.Contains(large, "A   B   C") {
		t.Error("200x60 frame did not switch to the detail scheme")
	}
	if w := maxLineWidth(large); w <= 80 {
		t.Errorf("200x60 frame is only %d cells wide — the new width was not consumed", w)
	}

	d.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	tiny := d.View().Content
	checkFrameInvariants(t, tiny, 40, 12)
	if !strings.ContainsRune(tiny, glyphDown) && !strings.ContainsRune(tiny, glyphUp) {
		t.Error("40x12 frame shows no vertical overflow arrow although 24 rows cannot fit")
	}
	if !strings.ContainsRune(tiny, glyphLeft) && !strings.ContainsRune(tiny, glyphRight) {
		t.Error("40x12 frame shows no horizontal overflow arrow although 49 columns cannot fit")
	}
}

// TestStateAndCursorSurviveResize plays real moves, walks the cursor to the
// far corner region, shrinks the terminal far enough that the cursor's hole
// leaves the original viewport, grows back, and requires the frame to be
// identical — which can only hold if pegs, links, cursor and viewport state
// all survived. The cursor is parked on the last row and near the last column
// deliberately: there minimal scrolling admits exactly one viewport origin at
// each size, so frame equality is well defined.
func TestStateAndCursorSurviveResize(t *testing.T) {
	d := newDemo(t, 24)
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	press(t, d, "space") // vertical peg at the cursor start (12,12)
	press(t, d, "enter") // commit
	press(t, d, "j")     // horizontal's turn: move off the peg
	press(t, d, "space")
	press(t, d, "enter")
	press(t, d, "G") // cursor to the bottom edge
	press(t, d, "$") // then the right edge; the corner gives way to (22,23)
	if got := (game.Point{Col: 22, Row: 23}); d.board.Cursor != got {
		t.Fatalf("cursor at %s after setup, want %s", d.board.Cursor, got)
	}
	if d.g.Ply() != 2 {
		t.Fatalf("ply %d after setup, want 2", d.g.Ply())
	}
	before := d.View().Content
	checkFrameInvariants(t, before, 80, 24)

	d.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	shrunk := d.View().Content
	checkFrameInvariants(t, shrunk, 30, 10)
	if !strings.ContainsRune(shrunk, glyphCursorLeft) {
		t.Error("cursor left the viewport on shrink")
	}
	if _, left := d.board.Viewport(); left == 0 {
		t.Error("viewport did not scroll although the cursor sits at column W")
	}

	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	after := d.View().Content
	if after != before {
		t.Errorf("frame after shrink+regrow differs from the original:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := (game.Point{Col: 22, Row: 23}); d.board.Cursor != got {
		t.Errorf("cursor drifted to %s across resizes", d.board.Cursor)
	}
	if d.g.Ply() != 2 {
		t.Errorf("game state lost: ply %d", d.g.Ply())
	}
}

// TestLinkModeTogglesLinks proves the digit overlay appears on real link
// targets and that the digits drive AddLink/RemoveLink on the engine.
func TestLinkModeTogglesLinks(t *testing.T) {
	d := newDemo(t, 12)
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Vertical: peg at F6; commit. Horizontal: peg at A10; commit.
	d.board.Cursor = game.Point{Col: 5, Row: 5}
	press(t, d, "space")
	press(t, d, "enter")
	d.board.Cursor = game.Point{Col: 0, Row: 9}
	press(t, d, "space")
	press(t, d, "enter")

	// Vertical again: peg at G4, a knight's move from F6 — the link G4-F6
	// comes with the placement.
	d.board.Cursor = game.Point{Col: 6, Row: 3}
	press(t, d, "space")
	link, _ := game.NewLink(game.Point{Col: 6, Row: 3}, game.Point{Col: 5, Row: 5})
	if !d.g.HasLink(link) {
		t.Fatal("placement did not auto-link G4-F6")
	}

	press(t, d, "x") // link mode
	if !d.linkMode {
		t.Fatal("x did not enter link mode")
	}
	d.View() // computes the digit overlay
	if d.board.Digits == nil {
		t.Fatal("no digit overlay in link mode on an own peg")
	}
	// From G4, F6 lies south-south-west: direction index 4, digit 5.
	target := game.Point{Col: 5, Row: 5}
	if got := d.board.Digits[target]; got != '5' {
		t.Fatalf("digit at F6 is %q, want '5'", got)
	}
	// The digit must be visible on the board itself.
	st := PlainStyles()
	lines := d.board.Render(d.g, &st, 200, 200)
	if got := cellAt(t, lines, 12, Compact.holeX(5), Compact.holeY(5)); got != '5' {
		t.Fatalf("board cell for F6 shows %q, want the digit 5", got)
	}

	press(t, d, "5") // withdraw the staged link
	if d.g.HasLink(link) {
		t.Fatal("digit did not remove the link")
	}
	press(t, d, "5") // put it back
	if !d.g.HasLink(link) {
		t.Fatal("digit did not re-add the link")
	}
	press(t, d, "esc")
	if d.linkMode {
		t.Fatal("esc did not leave link mode")
	}
	press(t, d, "enter") // commit the turn
	if d.g.Ply() != 3 {
		t.Fatalf("ply %d after commit, want 3", d.g.Ply())
	}
}

func TestCursorNeverRestsOnACorner(t *testing.T) {
	d := newDemo(t, 12)
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	d.board.Cursor = game.Point{Col: 0, Row: 5}
	press(t, d, "g") // top edge from the left column: corner gives way down
	if got := (game.Point{Col: 0, Row: 1}); d.board.Cursor != got {
		t.Errorf("cursor %s after g in column A, want %s", d.board.Cursor, got)
	}
	d.board.Cursor = game.Point{Col: 5, Row: 0}
	press(t, d, "0") // left edge along the top row: corner gives way right
	if got := (game.Point{Col: 1, Row: 0}); d.board.Cursor != got {
		t.Errorf("cursor %s after 0 in row 1, want %s", d.board.Cursor, got)
	}
	d.board.Cursor = game.Point{Col: 1, Row: 0}
	press(t, d, "h") // into the corner: no move at all
	if got := (game.Point{Col: 1, Row: 0}); d.board.Cursor != got {
		t.Errorf("cursor %s after h beside the corner, want %s", d.board.Cursor, got)
	}
}

// TestProgramResizeEndToEnd runs the model under a real Bubble Tea program via
// teatest, resizing mid-run exactly as SIGWINCH would, and requires the
// re-rendered output to show the detail scheme that only a 200-cell terminal
// produces.
//
// The waits are generous rather than tight. What is being asserted is that the
// resize reaches the program and changes what it draws, which is a property of
// the program; how long a shared continuous-integration runner takes to get
// there is not, and a short deadline turns this into a test of the machine.
func TestProgramResizeEndToEnd(t *testing.T) {
	const wait = 30 * time.Second

	d := newDemo(t, 24)
	tm := teatest.NewTestModel(t, d, teatest.WithInitialTermSize(80, 24))

	// One space between holes is the compact scheme and cannot occur in the
	// other, so this is the scale and not merely the board.
	waitForFrame(t, tm, wait, compactSpacing)

	tm.Send(tea.WindowSizeMsg{Width: 200, Height: 60})

	// Three spaces is the detail scheme, which only the larger size can produce,
	// so this fails if the resize is dropped rather than merely being slow.
	waitForFrame(t, tm, wait, detailSpacing)

	tm.Send(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	tm.WaitFinished(t, teatest.WithFinalTimeout(wait))
}

// The gap between two holes in a board row. Each belongs to one drawing scale
// and cannot appear in the other, which TestTheSpacingTellsTheScalesApart pins,
// so finding one in a frame identifies the scale that drew it.
const (
	compactSpacing = "· ·"
	detailSpacing  = "·   ·"
)

// TestTheSpacingTellsTheScalesApart pins what the resize test leans on. If a
// change to the glyphs or the steps ever made one spacing appear under the other
// scale, that test would go quiet rather than fail, so the property is asserted
// here instead of assumed there.
func TestTheSpacingTellsTheScalesApart(t *testing.T) {
	for _, n := range []int{game.MinSize, 12, 24, game.MaxSize} {
		for _, sc := range []Scale{Compact, Detail} {
			bv := &BoardView{Scale: sc, ShowCursor: true}
			w, h := sc.BlockSize(n)
			st := PlainStyles()
			frame := strings.Join(bv.Render(crowdedBoard(t, n, int64(n)), &st, w+40, h+6), "\n")
			want, other := compactSpacing, detailSpacing
			if sc == Detail {
				want, other = detailSpacing, compactSpacing
			}
			if !strings.Contains(frame, want) {
				t.Errorf("n=%d %s: frame does not contain its own spacing %q", n, sc, want)
			}
			if strings.Contains(frame, other) {
				t.Errorf("n=%d %s: frame contains the other scale's spacing %q", n, sc, other)
			}
		}
	}
}

// waitForFrame waits until the program's output carries want.
//
// What it looks for is a run inside one board row rather than the row of column
// labels. Bubble Tea redraws differentially: on the resize it moved the cursor
// and printed "  B   C   D ..." without reprinting the "A" that was already in
// the right place, so a test looking for "A   B   C" was looking for something
// that was on the screen and not in the bytes. It passed here and failed on the
// continuous-integration runner, which is the signature of a test measuring the
// renderer's choice of full or partial repaint rather than the program.
//
// A board row changes completely when the scale changes, so it is repainted
// whole, and the spacing between holes belongs to one scale or the other. Two
// adjacent holes are enough, so the condition survives a repaint that starts
// part-way along the row. Measured on the bytes the second wait actually
// receives, which are only those emitted after the first wait consumed the
// stream: teatest gives each wait its own buffer.
func waitForFrame(t *testing.T, tm *teatest.TestModel, wait time.Duration, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), want)
	}, teatest.WithDuration(wait))
}
