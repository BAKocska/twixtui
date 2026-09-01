package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/learn"
	"github.com/BAKocska/twixtui/internal/ui"
)

// The tutorial's board shares the screen with the lesson's prose, and the prose
// takes its rows first. That makes the drawing scale a question about the rows
// the board is left with rather than about the terminal, and answering the
// wrong question hides the very holes the step is pointing at. Both tests below
// are about that one decision.

// tutorialArrowsIn reports the viewport arrows a frame carries, which is how a
// clipped board announces itself.
func tutorialArrowsIn(frame string) string {
	var found []rune
	for _, r := range []rune{'↑', '↓', '←', '→'} {
		if strings.ContainsRune(frame, r) {
			found = append(found, r)
		}
	}
	return string(found)
}

// tutorialGutterHas reports whether a row number is drawn in the gutter, which
// is the honest test of a row being on screen: a clipped row has the arrow
// there instead.
func tutorialGutterHas(frame string, row int) bool {
	want := strconv.Itoa(row)
	for _, line := range strings.Split(frame, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == want {
			return true
		}
	}
	return false
}

// TestLessonBoardIsWholeAtAHundredByThirty is the size the review caught. The
// detail board is 24 rows and the screen has 29, so the terminal on its own
// says detail; but the prose takes eight rows and the bottom three rows of the
// board went behind the viewport arrow — two of the four missing corners and
// the whole bottom group of highlighted holes, while the prose was pointing at
// them. The compact board is 13 rows and fits with room over.
//
// The first three steps of the board lesson are the ones that highlight the
// borders, so each is checked: nothing a step marks may be off screen.
func TestLessonBoardIsWholeAtAHundredByThirty(t *testing.T) {
	const w, h = 100, 30
	d := tutorialTestDeps(t)
	for step := range 3 {
		m := newTutorialTestModel(t, d, "board", w, h)
		if err := m.loadStep(step); err != nil {
			t.Fatalf("step %d: %v", step+1, err)
		}
		frame := m.frame()
		if arrows := tutorialArrowsIn(frame); arrows != "" {
			t.Errorf("step %d at %dx%d: board clipped (%s) although a scale fits:\n%s",
				step+1, w, h, arrows, frame)
		}
		for _, p := range m.highlights() {
			if !tutorialGutterHas(frame, p.Row+1) {
				t.Errorf("step %d at %dx%d: row %d is highlighted but not on screen:\n%s",
					step+1, w, h, p.Row+1, frame)
			}
		}
		if n := m.boardSize(); !tutorialGutterHas(frame, n) {
			t.Errorf("step %d at %dx%d: the last row of the board is not on screen:\n%s",
				step+1, w, h, frame)
		}
	}
}

// TestTheLessonBoardIsOnlyClippedWhenNoScaleFits states the rule behind that
// size for the whole matrix. A clipped board is sometimes unavoidable — twenty
// columns cannot hold twelve holes at any scale — but it is only allowed when
// the compact scale would not have fitted either.
func TestTheLessonBoardIsOnlyClippedWhenNoScaleFits(t *testing.T) {
	n := learn.Rules().Size
	compactW, compactH := ui.Compact.BlockSize(n)
	for w := ui.MinWidth; w <= 210; w++ {
		for h := ui.MinHeight; h <= 60; h++ {
			arr := tutorialArrange(w, h, n)
			if arr.TooSmall {
				continue
			}
			blockW, blockH := arr.Scale.BlockSize(n)
			if blockW <= arr.BoardAvailW && blockH <= arr.BoardAvailH {
				continue
			}
			if compactW <= arr.BoardAvailW && compactH <= arr.BoardAvailH {
				t.Fatalf("%dx%d: the board is drawn at %s (%dx%d) in %dx%d and clipped, "+
					"though compact (%dx%d) would have fitted whole",
					w, h, arr.Scale, blockW, blockH, arr.BoardAvailW, arr.BoardAvailH,
					compactW, compactH)
			}
		}
	}
}
