package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/learn"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Everything here drives the screen through its Bubble Tea messages, and the
// board moves are made by pressing the keys the keymap names rather than by
// setting the cursor, so the tests exercise the dispatch a player goes through.

// tutorialTestDeps builds the collaborators the tutorial needs. Colour is off so
// assertions can be made on plain text, and the configuration directory is the
// test's own so a run never touches the state of the person running it.
func tutorialTestDeps(t *testing.T) Deps {
	t.Helper()
	styles := ui.PlainStyles()
	return Deps{
		ConfigDir: t.TempDir(),
		Styles:    &styles,
		Keymap:    ui.DefaultKeymap(),
	}
}

// newTutorialTestModel opens the screen and gives it a terminal size, which is
// the message Bubble Tea guarantees before the first render.
func newTutorialTestModel(t *testing.T, d Deps, lessonID string, w, h int) *tutorialModel {
	t.Helper()
	s, err := NewTutorialScreen(d, lessonID)
	if err != nil {
		t.Fatalf("NewTutorialScreen(%q): %v", lessonID, err)
	}
	m, ok := s.(*tutorialModel)
	if !ok {
		t.Fatalf("NewTutorialScreen returned %T, not the tutorial model", s)
	}
	tutorialSend(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// tutorialSend delivers one message and checks the screen keeps its identity, as
// a screen that swaps itself out mid-update would lose its state.
func tutorialSend(t *testing.T, m *tutorialModel, msg tea.Msg) tea.Cmd {
	t.Helper()
	next, cmd := m.Update(msg)
	if next != tea.Model(m) {
		t.Fatalf("Update replaced the model with %T", next)
	}
	return cmd
}

// tutorialKeyMsg turns a key string from the keymap back into the message
// Bubble Tea would deliver for it, so the tests press what the keymap names.
func tutorialKeyMsg(key string) tea.KeyPressMsg {
	special := map[string]rune{
		"enter": tea.KeyEnter,
		"esc":   tea.KeyEscape,
		"tab":   tea.KeyTab,
		"space": tea.KeySpace,
		"up":    tea.KeyUp,
		"down":  tea.KeyDown,
		"left":  tea.KeyLeft,
		"right": tea.KeyRight,
	}
	if code, ok := special[key]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	if rest, ok := strings.CutPrefix(key, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: []rune(rest)[0], Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
}

func tutorialPress(t *testing.T, m *tutorialModel, key string) tea.Cmd {
	t.Helper()
	if got := tutorialKeyMsg(key).String(); got != key {
		t.Fatalf("the test cannot press %q: it builds as %q", key, got)
	}
	return tutorialSend(t, m, tutorialKeyMsg(key))
}

// tutorialPoint parses a hole name the way the lessons write them.
func tutorialPoint(t *testing.T, name string) game.Point {
	t.Helper()
	p, err := game.ParsePoint(name)
	if err != nil {
		t.Fatalf("parsing %q: %v", name, err)
	}
	return p
}

// tutorialWalkTo moves the board cursor onto a hole with the movement keys,
// which is how a learner gets there.
func tutorialWalkTo(t *testing.T, m *tutorialModel, p game.Point) {
	t.Helper()
	for range 4 * m.g.Size() {
		c := m.board.Cursor
		switch {
		case c == p:
			return
		case c.Col < p.Col:
			tutorialPress(t, m, "l")
		case c.Col > p.Col:
			tutorialPress(t, m, "h")
		case c.Row < p.Row:
			tutorialPress(t, m, "j")
		default:
			tutorialPress(t, m, "k")
		}
	}
	t.Fatalf("the cursor stopped on %s heading for %s", m.board.Cursor, p)
}

// tutorialAdvance presses next until the screen leaves the step it is on, so a
// step whose prose needs more than one page is read through rather than skipped.
func tutorialAdvance(t *testing.T, m *tutorialModel) {
	t.Helper()
	step, done := m.step, m.done
	for range 64 {
		tutorialPress(t, m, "n")
		if m.step != step || m.done != done {
			return
		}
	}
	t.Fatalf("lesson %s will not leave step %d: %q", m.lesson.ID, step+1, m.message)
}

// tutorialFinishLesson works the current lesson through to its end, answering
// each task with the model answer on the way.
func tutorialFinishLesson(t *testing.T, m *tutorialModel) {
	t.Helper()
	for range 64 {
		if m.done {
			return
		}
		if task := m.lesson.Steps[m.step].Task; task != nil && !m.solved {
			tutorialWalkTo(t, m, task.Answer)
			tutorialPress(t, m, "space")
			if !m.solved {
				t.Fatalf("%s step %d: the model answer %s was rejected: %s",
					m.lesson.ID, m.step+1, task.Answer, m.feedback)
			}
		}
		tutorialAdvance(t, m)
	}
	t.Fatalf("lesson %s never reached its end", m.lesson.ID)
}

// tutorialCheckFrame holds the one invariant every size has to satisfy: nothing
// wider than the terminal and no more lines than it has rows.
func tutorialCheckFrame(t *testing.T, where, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) > h {
		t.Errorf("%s: %d lines in a terminal %d rows high", where, len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("%s: line %d is %d cells wide in a terminal %d wide: %q", where, i+1, got, w, l)
		}
	}
}

// TestEveryLessonPlaysThroughToTheEnd drives every lesson from its first step to
// its last through the model, answering each task with the model answer. It is a
// regression test on the screen and on the content at once: a step whose
// position will not replay, a task whose answer the screen mishandles, or a
// lesson that cannot be finished all fail here.
func TestEveryLessonPlaysThroughToTheEnd(t *testing.T) {
	d := tutorialTestDeps(t)
	lessons, steps, tasks := 0, 0, 0
	for _, l := range learn.Lessons() {
		m := newTutorialTestModel(t, d, l.ID, 200, 60)
		for i := range l.Steps {
			if m.step != i {
				t.Fatalf("%s: expected step %d, the screen is on %d", l.ID, i+1, m.step+1)
			}
			steps++
			if task := l.Steps[i].Task; task != nil {
				tasks++
				tutorialWalkTo(t, m, task.Answer)
				tutorialPress(t, m, "space")
				if !m.solved {
					t.Fatalf("%s step %d: the model answer %s was rejected: %s",
						l.ID, i+1, task.Answer, m.feedback)
				}
				if strings.TrimSpace(m.feedback) == "" {
					t.Errorf("%s step %d: accepted %s without saying anything", l.ID, i+1, task.Answer)
				}
				if m.g.At(task.Answer) != game.Vertical {
					t.Errorf("%s step %d: %s was accepted but no peg was played", l.ID, i+1, task.Answer)
				}
			}
			tutorialAdvance(t, m)
		}
		if !m.done {
			t.Fatalf("%s: ran out of steps without finishing", l.ID)
		}
		frame := m.frame()
		if !strings.Contains(frame, "finished") {
			t.Errorf("%s: the closing frame does not say the lesson is finished:\n%s", l.ID, frame)
		}
		if next, ok := m.following(); ok && !strings.Contains(frame, next.Title) {
			t.Errorf("%s: the closing frame does not offer %q", l.ID, next.Title)
		}
		lessons++
	}
	t.Logf("drove %d lessons and %d steps end to end, answering %d tasks", lessons, steps, tasks)
}

// TestWrongAnswerExplainsItselfAndDoesNotAdvance checks the loop a learner
// actually spends their time in: a wrong move is explained, the step does not
// move on, the board is left alone, and the right answer can follow at once.
func TestWrongAnswerExplainsItselfAndDoesNotAdvance(t *testing.T) {
	d := tutorialTestDeps(t)
	m := newTutorialTestModel(t, d, "links", 200, 60)
	tutorialAdvance(t, m) // the first step is prose; the second asks for a link

	task := m.lesson.Steps[m.step].Task
	if task == nil {
		t.Fatalf("expected a task on step %d of the links lesson", m.step+1)
	}
	step, ply := m.step, m.g.Ply()

	// G7 is the diagonal neighbour of F6 that beginners reach for.
	wrong := tutorialPoint(t, "G7")
	tutorialWalkTo(t, m, wrong)
	tutorialPress(t, m, "space")

	if m.solved {
		t.Fatalf("%s was accepted: %s", wrong, m.feedback)
	}
	for _, want := range []string{"diagonal neighbour of F6", "knight's move"} {
		if !strings.Contains(m.feedback, want) {
			t.Errorf("rejecting %s did not explain %q; the feedback was %q", wrong, want, m.feedback)
		}
	}
	if m.step != step {
		t.Errorf("a wrong answer moved the lesson from step %d to %d", step+1, m.step+1)
	}
	if m.g.At(wrong) != game.NoPlayer || m.g.Ply() != ply {
		t.Errorf("a rejected move was played anyway: %s holds %v at ply %d", wrong, m.g.At(wrong), m.g.Ply())
	}
	frame := m.frame()
	if !strings.Contains(frame, "Not yet.") || !strings.Contains(frame, "diagonal") {
		t.Errorf("the explanation is not on screen:\n%s", frame)
	}

	// Next must refuse to skip an unanswered task, and must say why.
	tutorialPress(t, m, "n")
	if m.step != step {
		t.Errorf("next skipped an unanswered task")
	}
	if !strings.Contains(m.message, tutorialKeyLabel(tutActShow)) {
		t.Errorf("refusing to go on did not point at the show key: %q", m.message)
	}

	// The learner can answer again straight away.
	tutorialWalkTo(t, m, task.Answer)
	tutorialPress(t, m, "space")
	if !m.solved {
		t.Fatalf("the model answer %s was rejected after a wrong one: %s", task.Answer, m.feedback)
	}
	if m.g.At(task.Answer) != game.Vertical {
		t.Errorf("%s was accepted but no peg was played", task.Answer)
	}
	tutorialAdvance(t, m)
	if m.step != step+1 {
		t.Errorf("after a correct answer the lesson is on step %d, expected %d", m.step+1, step+2)
	}
}

// TestIllegalPicksReachTheChecker is the trap the content author called out.
// Both mistakes a beginner really makes are illegal moves — reaching into the
// opponent's border line, and reaching for a corner that does not exist — and
// learn.Task.Accept is the only thing that names them. A screen that filtered
// illegal holes out before asking would leave the learner blocked in silence, so
// this drives the cursor onto each of them and insists on the explanation.
func TestIllegalPicksReachTheChecker(t *testing.T) {
	cases := []struct {
		name   string
		hole   string
		reason []string
	}{
		{
			"the opponent's border line",
			"A5",
			[]string{"in Horizontal's border line", "never place a peg in your opponent's border line"},
		},
		{
			"a corner that does not exist",
			"A1",
			[]string{"is a corner", "corner holes do not exist"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := tutorialTestDeps(t)
			m := newTutorialTestModel(t, d, "board", 200, 60)
			for m.lesson.Steps[m.step].Task == nil {
				tutorialAdvance(t, m)
			}
			hole := tutorialPoint(t, c.hole)
			if err := m.g.CanPlace(m.g.Turn(), hole); err == nil {
				t.Fatalf("%s is supposed to be an illegal placement here", hole)
			}
			step, ply := m.step, m.g.Ply()

			tutorialWalkTo(t, m, hole)
			if m.board.Cursor != hole {
				t.Fatalf("the cursor cannot be put on %s, so the learner can never ask about it", hole)
			}
			tutorialPress(t, m, "space")

			if m.solved {
				t.Fatalf("%s was accepted: %s", hole, m.feedback)
			}
			for _, want := range c.reason {
				if !strings.Contains(m.feedback, want) {
					t.Errorf("picking %s did not explain %q; the feedback was %q", hole, want, m.feedback)
				}
			}
			if m.step != step || m.g.Ply() != ply {
				t.Errorf("an illegal pick changed the position: step %d, ply %d", m.step+1, m.g.Ply())
			}
			if !strings.Contains(m.frame(), "Not yet.") {
				t.Errorf("the frame does not show that the pick was refused:\n%s", m.frame())
			}
		})
	}
}

// TestCompletionIsRememberedAcrossScreens checks that a finished lesson is still
// finished for a screen opened later over the same configuration directory.
func TestCompletionIsRememberedAcrossScreens(t *testing.T) {
	d := tutorialTestDeps(t)
	finished := "swap"

	m := newTutorialTestModel(t, d, finished, 200, 60)
	tutorialFinishLesson(t, m)
	if !m.done {
		t.Fatalf("the %s lesson did not finish", finished)
	}

	fresh := newTutorialTestModel(t, d, "", 200, 60)
	if fresh.mode != tutorialChoosing {
		t.Fatalf("an empty lesson id did not open the chooser")
	}
	if !fresh.progress.completed(finished) {
		t.Errorf("a fresh screen over %s does not remember %s", d.ConfigDir, finished)
	}
	for _, other := range learn.Lessons() {
		if other.ID != finished && fresh.progress.completed(other.ID) {
			t.Errorf("%s is marked done and was never played", other.ID)
		}
	}
	if !strings.Contains(fresh.frame(), "(done)") {
		t.Errorf("the chooser does not mark the finished lesson:\n%s", fresh.frame())
	}

	data, err := os.ReadFile(filepath.Join(d.ConfigDir, tutorialProgressFile))
	if err != nil {
		t.Fatalf("reading the progress file: %v", err)
	}
	var rec tutorialProgressRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("the progress file is not valid json: %v", err)
	}
	if rec.Version != tutorialProgressVersion || !slices.Equal(rec.Completed, []string{finished}) {
		t.Errorf("the progress file holds %+v", rec)
	}
	// The temporary file the atomic write uses must not be left behind.
	entries, err := os.ReadDir(d.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("the write left %s behind", e.Name())
		}
	}
}

// tutorialSizes is the resize matrix: the documented minimum, a narrow pane, an
// ordinary terminal and a large one.
var tutorialSizes = [][2]int{{20, 8}, {40, 12}, {80, 24}, {200, 60}}

// TestTutorialFitsEverySize renders every part of the screen at every size in
// the matrix and holds the fitting invariant (R3, R4).
func TestTutorialFitsEverySize(t *testing.T) {
	d := tutorialTestDeps(t)

	t.Run("chooser", func(t *testing.T) {
		m := newTutorialTestModel(t, d, "", 80, 24)
		for _, sel := range []int{0, len(m.lessons) - 1} {
			m.selected = sel
			for _, s := range tutorialSizes {
				tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
				tutorialCheckFrame(t, fmt.Sprintf("chooser on %d at %dx%d", sel, s[0], s[1]), m.frame(), s[0], s[1])
			}
		}
	})

	t.Run("steps", func(t *testing.T) {
		for _, l := range learn.Lessons() {
			for i := range l.Steps {
				m := newTutorialTestModel(t, d, l.ID, 80, 24)
				if err := m.loadStep(i); err != nil {
					t.Fatalf("%s step %d: %v", l.ID, i+1, err)
				}
				// A task step is also rendered with an answer on the board and
				// the feedback block filled in, which is the tallest the panel
				// ever gets.
				if l.Steps[i].Task != nil {
					tutorialPress(t, m, "s")
					tutorialPress(t, m, "space")
				}
				for _, s := range tutorialSizes {
					tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
					where := fmt.Sprintf("%s step %d at %dx%d", l.ID, i+1, s[0], s[1])
					tutorialCheckFrame(t, where, m.frame(), s[0], s[1])
				}
			}
		}
	})

	t.Run("key page and closing frame", func(t *testing.T) {
		m := newTutorialTestModel(t, d, "swap", 80, 24)
		tutorialPress(t, m, "?")
		if !m.helping {
			t.Fatal("the key page did not open")
		}
		for _, s := range tutorialSizes {
			tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
			tutorialCheckFrame(t, fmt.Sprintf("key page at %dx%d", s[0], s[1]), m.frame(), s[0], s[1])
		}
		tutorialPress(t, m, "esc")
		tutorialFinishLesson(t, m)
		if !m.done {
			t.Fatal("the lesson did not finish")
		}
		for _, s := range tutorialSizes {
			tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
			tutorialCheckFrame(t, fmt.Sprintf("closing frame at %dx%d", s[0], s[1]), m.frame(), s[0], s[1])
		}
	})

	t.Run("below the minimum", func(t *testing.T) {
		m := newTutorialTestModel(t, d, "board", 80, 24)
		for _, s := range [][2]int{{19, 5}, {10, 3}, {1, 1}, {0, 0}} {
			tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
			frame := m.frame()
			tutorialCheckFrame(t, fmt.Sprintf("too small at %dx%d", s[0], s[1]), frame, max(s[0], 0), max(s[1], 1))
			if s[0] >= ui.MinWidth-1 && s[1] >= 2 && !strings.Contains(frame, "too small") {
				t.Errorf("at %dx%d the frame is not the too-small notice: %q", s[0], s[1], frame)
			}
		}
	})
}

// TestShrinkingKeepsTheProseReachable is the case a resize really breaks: the
// learner pages down in a narrow pane, then the pane grows or shrinks and the
// offset no longer points at any text. The panel has to keep showing something.
func TestShrinkingKeepsTheProseReachable(t *testing.T) {
	d := tutorialTestDeps(t)
	m := newTutorialTestModel(t, d, "board", 20, 8)
	for range 3 {
		tutorialPress(t, m, "n")
	}
	if m.scroll == 0 {
		t.Fatalf("paging in a 20x8 pane did not move the panel offset")
	}
	if m.step != 0 {
		t.Fatalf("paging the first step left it for step %d", m.step+1)
	}

	// Growing the pane a little must keep the reader where they were rather than
	// throwing them back to the top, because there is still text below them.
	paged := m.scroll
	tutorialSend(t, m, tea.WindowSizeMsg{Width: 40, Height: 12})
	grown := m.arrange()
	if len(m.panel(grown).lines) > grown.PanelH && m.scroll == 0 {
		t.Errorf("resizing from 20x8 to 40x12 threw the reader back to the top from offset %d", paged)
	}

	for _, s := range [][2]int{{200, 60}, {20, 8}, {40, 12}, {80, 24}} {
		tutorialSend(t, m, tea.WindowSizeMsg{Width: s[0], Height: s[1]})
		where := fmt.Sprintf("after resizing to %dx%d", s[0], s[1])
		tutorialCheckFrame(t, where, m.frame(), s[0], s[1])
		arr := m.arrange()
		lines := m.panel(arr).lines
		if m.scroll >= len(lines) {
			t.Errorf("%s: the offset %d is past the %d lines of text", where, m.scroll, len(lines))
		}
		if len(tutorialWindow(lines, m.scroll, arr.PanelH)) == 0 {
			t.Errorf("%s: the panel shows nothing", where)
		}
	}
	// Widening far enough for the whole step brings the reader back to the top.
	tutorialSend(t, m, tea.WindowSizeMsg{Width: 200, Height: 60})
	if m.scroll != 0 {
		t.Errorf("the whole step fits at 200x60, so the offset should be 0, not %d", m.scroll)
	}
}

// TestProseIsWrappedNotClippedAtFortyColumns holds the promise the content makes
// by carrying no line breaks: the prose is wrapped to the pane, whole words are
// kept whole, every line of it is reachable by paging, and what the panel builds
// is what reaches the terminal.
func TestProseIsWrappedNotClippedAtFortyColumns(t *testing.T) {
	const width, height = 40, 12
	d := tutorialTestDeps(t)
	// The opening step of the board lesson carries the longest paragraph in the
	// tutorial.
	m := newTutorialTestModel(t, d, "board", width, height)
	step := m.lesson.Steps[0]
	arr := m.arrange()
	if arr.PanelW != width {
		t.Fatalf("the panel is %d columns wide in a %d column terminal", arr.PanelW, width)
	}
	if arr.PanelH < 1 {
		t.Fatalf("there is no panel at %dx%d", width, height)
	}
	lines := m.panel(arr).lines

	for i, l := range lines {
		if got := ansi.StringWidth(l); got > width {
			t.Errorf("panel line %d is %d cells wide: %q", i+1, got, l)
		}
	}
	// Words are only broken when one is wider than the pane, and none is here.
	for _, w := range strings.Fields(step.Text) {
		if len(w) > width {
			t.Fatalf("the step has a word wider than the pane: %q", w)
		}
	}

	// Paging over the panel has to reproduce the step's text word for word.
	var shown []string
	for top := 0; top < len(lines); top += arr.PanelH {
		shown = append(shown, tutorialWindow(lines, top, arr.PanelH)...)
	}
	if len(shown) != len(lines) {
		t.Fatalf("paging showed %d of %d lines", len(shown), len(lines))
	}
	got := strings.Join(strings.Fields(strings.Join(shown, " ")), " ")
	want := strings.Join(strings.Fields(step.Text), " ")
	if !strings.Contains(got, want) {
		t.Errorf("the prose does not survive wrapping at %d columns.\nwant: %s\ngot:  %s", width, want, got)
	}

	// The pager really reaches the last page rather than stopping short.
	last := tutorialClampScroll(len(lines), len(lines), arr.PanelH)
	for range 64 {
		if m.scroll == last {
			break
		}
		before := m.scroll
		tutorialPress(t, m, "n")
		if m.scroll <= before {
			t.Fatalf("paging stopped at %d of %d lines", m.scroll, len(lines))
		}
	}
	if m.scroll != last {
		t.Errorf("the pager reached %d, not the last page at %d", m.scroll, last)
	}

	// And what the panel builds is what the terminal gets: Compose must not be
	// trimming these lines.
	m.scroll = 0
	frame := m.frame()
	for _, l := range tutorialWindow(lines, 0, arr.PanelH) {
		if l != "" && !strings.Contains(frame, l) {
			t.Errorf("the frame does not carry the panel line %q:\n%s", l, frame)
		}
	}
}

// TestTutorialKeysDoNotShadowTheKeymap keeps the tutorial's own keys out of the
// shared keymap's way. A key that meant two things at once would make one of
// them unreachable.
func TestTutorialKeysDoNotShadowTheKeymap(t *testing.T) {
	km := ui.DefaultKeymap()
	seen := map[string]tutorialAction{}
	for _, b := range tutorialBindings {
		if b.label == "" || b.help == "" {
			t.Errorf("the binding for action %d has no label or no help", b.action)
		}
		for _, k := range b.keys {
			if kb, ok := km.Lookup(ui.CtxBoard, k); ok {
				t.Errorf("the tutorial key %q also means %q on the board", k, kb.Help)
			}
			if other, dup := seen[k]; dup {
				t.Errorf("the tutorial binds %q twice, to actions %d and %d", k, other, b.action)
			}
			seen[k] = b.action
		}
	}
}

// TestBoardKeysComeFromTheSharedKeymap presses every key the keymap gives each
// board action and checks the cursor does what the keymap says. This is the
// reason the keymap is data: the tutorial teaches the game's keys, so a key
// added or renamed in ui has to work here without a second list being edited.
func TestBoardKeysComeFromTheSharedKeymap(t *testing.T) {
	km := ui.DefaultKeymap()
	d := tutorialTestDeps(t)
	moves := []struct {
		action     ui.Action
		dCol, dRow int
	}{
		{ui.ActMoveLeft, -1, 0},
		{ui.ActMoveRight, 1, 0},
		{ui.ActMoveUp, 0, -1},
		{ui.ActMoveDown, 0, 1},
		{ui.ActJumpLeft, -ui.JumpStep, 0},
		{ui.ActJumpRight, ui.JumpStep, 0},
		{ui.ActJumpUp, 0, -ui.JumpStep},
		{ui.ActJumpDown, 0, ui.JumpStep},
	}
	for _, mv := range moves {
		b, ok := km.ByAction(ui.CtxBoard, mv.action)
		if !ok {
			t.Errorf("the keymap has no binding for action %d", mv.action)
			continue
		}
		if len(b.Keys) == 0 {
			t.Errorf("%q has no keys", b.Help)
		}
		for _, key := range b.Keys {
			m := newTutorialTestModel(t, d, "links", 200, 60)
			from := m.board.Cursor
			tutorialPress(t, m, key)
			want := game.Point{Col: from.Col + mv.dCol, Row: from.Row + mv.dRow}
			if m.board.Cursor != want {
				t.Errorf("%q (%s) moved the cursor from %s to %s, expected %s",
					key, b.Help, from, m.board.Cursor, want)
			}
		}
	}

	// The edge keys and both placement keys go through the same table.
	m := newTutorialTestModel(t, d, "board", 200, 60)
	for _, c := range []struct {
		action ui.Action
		want   game.Point
	}{
		{ui.ActEdgeTop, game.Point{Col: m.board.Cursor.Col, Row: 0}},
		{ui.ActEdgeLeft, game.Point{Col: 0, Row: 0}},
		{ui.ActEdgeBottom, game.Point{Col: 0, Row: m.g.Size() - 1}},
		{ui.ActEdgeRight, game.Point{Col: m.g.Size() - 1, Row: m.g.Size() - 1}},
	} {
		b, ok := km.ByAction(ui.CtxBoard, c.action)
		if !ok {
			t.Fatalf("the keymap has no binding for action %d", c.action)
		}
		tutorialPress(t, m, b.Keys[0])
		if m.board.Cursor != c.want {
			t.Errorf("%q left the cursor on %s, expected %s", b.Keys[0], m.board.Cursor, c.want)
		}
	}

	// Both keys the keymap offers for putting a peg down answer a task.
	for _, action := range []ui.Action{ui.ActPlacePeg, ui.ActConfirm} {
		b, ok := km.ByAction(ui.CtxBoard, action)
		if !ok {
			t.Fatalf("the keymap has no binding for action %d", action)
		}
		m := newTutorialTestModel(t, d, "links", 200, 60)
		tutorialAdvance(t, m)
		task := m.lesson.Steps[m.step].Task
		if task == nil {
			t.Fatalf("expected a task on step %d", m.step+1)
		}
		tutorialWalkTo(t, m, task.Answer)
		tutorialPress(t, m, b.Keys[0])
		if !m.solved {
			t.Errorf("%q did not answer the task: %q", b.Keys[0], m.feedback)
		}
	}
}

// TestChooserListsEveryLessonWithItsSummary checks the list carries what a
// player needs in order to choose: every lesson's title, its summary where the
// pane has room, and every entry still reachable when it does not.
func TestChooserListsEveryLessonWithItsSummary(t *testing.T) {
	d := tutorialTestDeps(t)
	m := newTutorialTestModel(t, d, "", 100, 40)
	frame := m.frame()
	for _, l := range m.lessons {
		if !strings.Contains(frame, l.Title) {
			t.Errorf("the chooser does not list %q", l.Title)
		}
		for _, word := range strings.Fields(l.Summary) {
			// A hyphen is a wrapping point, so a hyphenated word may be split
			// across two lines and is not a fair thing to look for.
			if strings.Contains(word, "-") {
				continue
			}
			if !strings.Contains(frame, word) {
				t.Errorf("the summary of %s is missing %q from the chooser:\n%s", l.ID, word, frame)
			}
		}
	}

	// In a pane too short for the summaries every lesson must still be
	// reachable: moving the selection down has to scroll the list.
	tutorialSend(t, m, tea.WindowSizeMsg{Width: 40, Height: 12})
	for range len(m.lessons) {
		tutorialPress(t, m, "j")
	}
	if m.selected != len(m.lessons)-1 {
		t.Fatalf("moving down stopped on lesson %d of %d", m.selected+1, len(m.lessons))
	}
	if last := m.lessons[len(m.lessons)-1]; !strings.Contains(m.frame(), last.Title) {
		t.Errorf("the last lesson is not on screen after scrolling to it:\n%s", m.frame())
	}
}

// TestChooserOpensAndLeavesLessons walks the way in and out of a lesson, which
// is the only navigation the menu depends on.
func TestChooserOpensAndLeavesLessons(t *testing.T) {
	d := tutorialTestDeps(t)
	m := newTutorialTestModel(t, d, "", 80, 24)
	if m.mode != tutorialChoosing {
		t.Fatalf("an empty lesson id did not open the chooser")
	}

	tutorialPress(t, m, "j")
	if m.selected != 1 {
		t.Fatalf("moving down left the selection on %d", m.selected)
	}
	tutorialPress(t, m, "enter")
	if m.mode != tutorialInLesson || m.lesson.ID != m.lessons[1].ID {
		t.Fatalf("opening the second lesson landed on %v/%s", m.mode, m.lesson.ID)
	}

	tutorialPress(t, m, "esc")
	if m.mode != tutorialChoosing {
		t.Fatalf("leaving a lesson reached from the chooser did not go back to it")
	}
	if m.selected != 1 {
		t.Errorf("the chooser came back on %d rather than the lesson just left", m.selected)
	}

	cmd := tutorialPress(t, m, "q")
	if cmd == nil {
		t.Fatal("leaving the chooser emitted nothing")
	}
	msg, ok := cmd().(DoneMsg)
	if !ok {
		t.Fatalf("leaving the chooser emitted %T", cmd())
	}
	if msg.Next != nil || msg.Err != nil || msg.Quit {
		t.Errorf("leaving the chooser should go back, it emitted %+v", msg)
	}

	// A screen opened on one named lesson leaves the screen instead, because
	// there is no chooser behind it.
	alone := newTutorialTestModel(t, d, "board", 80, 24)
	cmd = tutorialPress(t, alone, "esc")
	if cmd == nil {
		t.Fatal("leaving a lesson opened by name emitted nothing")
	}
	if msg, ok := cmd().(DoneMsg); !ok || msg.Next != nil || msg.Err != nil {
		t.Errorf("leaving a lesson opened by name emitted %#v", cmd())
	}
}

// TestRestartAndShowAnswer covers the two ways out of being stuck.
func TestRestartAndShowAnswer(t *testing.T) {
	d := tutorialTestDeps(t)
	m := newTutorialTestModel(t, d, "links", 200, 60)
	tutorialAdvance(t, m)
	task := m.lesson.Steps[m.step].Task
	if task == nil {
		t.Fatalf("expected a task on step %d", m.step+1)
	}

	tutorialPress(t, m, "s")
	if !m.revealed {
		t.Fatal("the show key did not reveal the answer")
	}
	if !slices.Contains(m.highlights(), task.Answer) {
		t.Errorf("the answer %s is not marked on the board", task.Answer)
	}
	if m.g.At(task.Answer) != game.NoPlayer {
		t.Errorf("showing the answer played it; the learner should still put the peg in")
	}
	if !strings.Contains(m.frame(), task.Answer.String()) {
		t.Errorf("the answer is not named on screen:\n%s", m.frame())
	}

	// Answering, then restarting, puts the position back as it was found.
	before, err := m.g.Transcript()
	if err != nil {
		t.Fatal(err)
	}
	tutorialWalkTo(t, m, task.Answer)
	tutorialPress(t, m, "space")
	if !m.solved {
		t.Fatalf("the model answer was rejected: %s", m.feedback)
	}
	tutorialPress(t, m, "r")
	if m.solved || m.feedback != "" || m.revealed {
		t.Errorf("restarting left the step answered")
	}
	after, err := m.g.Transcript()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("restarting left the position as %q, it was %q", after, before)
	}

	// Paging back to a step already answered restores the answer rather than
	// asking for the same move twice.
	tutorialWalkTo(t, m, task.Answer)
	tutorialPress(t, m, "space")
	step := m.step
	tutorialAdvance(t, m)
	if m.step != step+1 {
		t.Fatalf("the lesson did not move on to step %d", step+2)
	}
	for m.step != step {
		tutorialPress(t, m, "p")
	}
	if !m.solved || m.g.At(task.Answer) != game.Vertical {
		t.Errorf("going back to an answered step lost the answer")
	}
}

// TestUnknownLessonIsRefused and the defaults a bare Deps has to survive.
func TestUnknownLessonIsRefused(t *testing.T) {
	d := tutorialTestDeps(t)
	if _, err := NewTutorialScreen(d, "no-such-lesson"); err == nil {
		t.Error("an unknown lesson id was accepted")
	}
	// No configuration directory, no styles and no keymap: the screen still
	// renders, it just remembers nothing.
	s, err := NewTutorialScreen(Deps{}, "")
	if err != nil {
		t.Fatalf("a bare Deps was refused: %v", err)
	}
	m, ok := s.(*tutorialModel)
	if !ok {
		t.Fatalf("NewTutorialScreen returned %T", s)
	}
	if len(m.keymap) == 0 {
		t.Error("no keymap was defaulted in")
	}
	tutorialSend(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	tutorialCheckFrame(t, "bare deps", m.frame(), 80, 24)
	if err := m.progress.mark("board"); err != nil {
		t.Errorf("marking progress without a directory failed: %v", err)
	}
}

// TestEveryScreenSaysHowToLeave holds the rule that no screen is a trap: the
// status line always names the key that gets out, or the message that replaced
// it names what to do instead.
func TestEveryScreenSaysHowToLeave(t *testing.T) {
	d := tutorialTestDeps(t)
	quit, ok := ui.DefaultKeymap().ByAction(ui.CtxBoard, ui.ActQuit)
	if !ok {
		t.Fatal("the keymap has no quit binding")
	}

	m := newTutorialTestModel(t, d, "", 200, 60)
	if !strings.Contains(m.statusLine(false), quit.Label) {
		t.Errorf("the chooser does not say how to leave: %q", m.statusLine(false))
	}
	tutorialPress(t, m, "enter")
	if !strings.Contains(m.statusLine(false), quit.Label) {
		t.Errorf("a lesson does not say how to leave: %q", m.statusLine(false))
	}
	tutorialFinishLesson(t, m)
	if !m.done {
		t.Fatal("the lesson did not finish")
	}
	if !strings.Contains(m.statusLine(false), quit.Label) {
		t.Errorf("the closing frame does not say how to leave: %q", m.statusLine(false))
	}

	// The key page lists every key the tutorial answers to, and nothing it
	// ignores.
	tutorialPress(t, m, "?")
	page := strings.Join(m.helpLines(80), "\n")
	for _, action := range tutorialBoardActions {
		b, found := ui.DefaultKeymap().ByAction(ui.CtxBoard, action)
		if !found {
			t.Errorf("the keymap has no binding for action %d", action)
			continue
		}
		if !strings.Contains(page, b.Label) {
			t.Errorf("the key page does not list %q", b.Label)
		}
	}
	for _, b := range tutorialBindings {
		if !strings.Contains(page, b.help) {
			t.Errorf("the key page does not explain %q", b.label)
		}
	}
	for _, action := range []ui.Action{ui.ActLinkMode, ui.ActAbortTurn} {
		b, found := ui.DefaultKeymap().ByAction(ui.CtxBoard, action)
		if found && strings.Contains(page, b.Help) {
			t.Errorf("the key page offers %q, which the tutorial does not do", b.Help)
		}
	}
}
