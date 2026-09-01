package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/ui"

	"github.com/BAKocska/twixtui/internal/learn"
)

// obTestPlayer is the profile these tests play as. The introduction keys its flag
// to the profile the run is playing as rather than to the stored current one, so
// the two can differ and the tests have to name which they mean.
const obTestPlayer = "ada"

// Everything here drives the screen through its Bubble Tea messages and presses
// the keys the keymap names, through shellKeyPress, so a binding written
// against a key name a terminal never produces cannot pass.

// onboardingTestDeps builds a player and the collaborators the introduction
// needs. The profile is chosen, because that is the state the launch path
// guarantees before anything a player can reach the introduction from.
func onboardingTestDeps(t *testing.T) Deps {
	t.Helper()
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("Ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Profiles.UseCurrent("Ada"); err != nil {
		t.Fatal(err)
	}
	return d
}

// onboardingRoomy is a terminal with room for the whole board and for every
// step's prose in one page, so that one press of the pager key is one step. The
// small sizes are exercised separately, where paging is the point.
var onboardingRoomy = [2]int{120, 40}

// newOnboardingTest opens the screen and gives it a size, which is the message
// Bubble Tea guarantees before the first render.
func newOnboardingTest(t *testing.T, d Deps, w, h int) *onboardingModel {
	t.Helper()
	return newOnboardingTestAs(t, d, obTestPlayer, w, h)
}

// newOnboardingTestAs builds the introduction for a named profile, which the
// tests about two players on one machine need: the flag belongs to the profile
// the run plays as, not to whichever one the store calls current.
func newOnboardingTestAs(t *testing.T, d Deps, player string, w, h int) *onboardingModel {
	t.Helper()
	s, err := NewOnboarding(d, player)
	if err != nil {
		t.Fatalf("NewOnboarding: %v", err)
	}
	m, ok := s.(*onboardingModel)
	if !ok {
		t.Fatalf("NewOnboarding returned %T, not the introduction", s)
	}
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// onboardingPress delivers a key and checks the screen keeps its identity, as a
// screen that swapped itself out mid-update would lose its state.
func onboardingPress(t *testing.T, m *onboardingModel, key string) tea.Cmd {
	t.Helper()
	press := shellKeyPress(key)
	if got := press.String(); got != key {
		t.Fatalf("the test cannot press %q: it encodes as %q", key, got)
	}
	next, cmd := m.Update(press)
	if next != tea.Model(m) {
		t.Fatalf("pressing %q replaced the model with %T", key, next)
	}
	return cmd
}

// onboardingIntroduced reports the stored flag as a fresh reader of the
// configuration directory sees it, which is the only reading that says anything
// about the next launch.
func onboardingIntroduced(t *testing.T, dir string) bool {
	t.Helper()
	s, err := profile.Open(dir)
	if err != nil {
		t.Fatalf("reopening the profile store: %v", err)
	}
	p, ok := s.Current()
	if !ok {
		t.Fatal("no profile is chosen after the introduction")
	}
	return p.Introduced
}

// onboardingWalkTo advances to a step with the pager key.
func onboardingWalkTo(t *testing.T, m *onboardingModel, step int) {
	t.Helper()
	for m.step < step {
		before := m.step
		onboardingPress(t, m, "n")
		if m.step == before {
			t.Fatalf("the pager key did not leave step %d", before+1)
		}
	}
}

// onboardingSkipKeys are the keys that must leave the screen from anywhere. The
// quit binding is the one the status line names; esc is the second way out
// every other screen in the program also offers.
var onboardingSkipKeys = []string{"q", "esc"}

// TestTheIntroductionIsOfferedOnceAndThenNot is the whole of what the launch
// path asks: show this to somebody who has not seen it, and to nobody else.
func TestTheIntroductionIsOfferedOnceAndThenNot(t *testing.T) {
	d := onboardingTestDeps(t)
	if OnboardingSeen(d, obTestPlayer) {
		t.Fatal("a fresh profile is reported as having seen the introduction")
	}

	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	for range len(m.steps) {
		onboardingPress(t, m, "n")
	}
	if !m.done {
		t.Fatalf("after %d presses the introduction is on step %d, not finished", len(m.steps), m.step+1)
	}
	if cmd := onboardingPress(t, m, "n"); cmd == nil {
		t.Fatal("the pager key on the closing panel does not leave the screen")
	}

	if !OnboardingSeen(d, obTestPlayer) {
		t.Error("having read the introduction through, the player is still reported as not having seen it")
	}
	if !onboardingIntroduced(t, d.ConfigDir) {
		t.Error("the flag did not reach the profile store")
	}
}

// TestSkippingAtAnyStepCountsAsSeen is the requirement that the introduction is
// not a wall. Somebody who leaves it on the first step has refused it, and a
// program that offers it again next launch has ignored the refusal.
//
// Every step is tried, and both keys that leave, because the failure this
// guards against is a screen that records the flag on one path out and not on
// another.
func TestSkippingAtAnyStepCountsAsSeen(t *testing.T) {
	for _, key := range onboardingSkipKeys {
		for step := range onboardingSteps {
			t.Run(key+"/step"+string(rune('1'+step)), func(t *testing.T) {
				d := onboardingTestDeps(t)
				m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
				onboardingWalkTo(t, m, step)
				if m.step != step {
					t.Fatalf("the walk reached step %d, not %d", m.step+1, step+1)
				}
				if cmd := onboardingPress(t, m, key); cmd == nil {
					t.Fatalf("%q on step %d did not leave the screen", key, step+1)
				}
				if !onboardingIntroduced(t, d.ConfigDir) {
					t.Errorf("skipping with %q on step %d did not count as seen", key, step+1)
				}
				if OnboardingSeen(d, obTestPlayer) != true {
					t.Errorf("skipping with %q on step %d leaves the launch path still offering it", key, step+1)
				}
			})
		}
	}
}

// TestTheFlagSurvivesARestart is the point of storing it at all. The store is
// reopened from the directory rather than reread through the one the screen
// wrote, because the second only proves the flag reached memory.
func TestTheFlagSurvivesARestart(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, m, "q")

	restarted, err := profile.Open(d.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	next := Deps{ConfigDir: d.ConfigDir, Profiles: restarted, Styles: d.Styles, Keymap: d.Keymap}
	if !OnboardingSeen(next, obTestPlayer) {
		t.Error("a second run of the program offers the introduction again")
	}
}

// TestTheIntroductionCanBeReplayedAfterItHasBeenSeen is the menu entry's
// requirement: having been through it once must not make it unreachable, since
// the whole point of the entry is asking for it again.
func TestTheIntroductionCanBeReplayedAfterItHasBeenSeen(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, m, "q")
	if !OnboardingSeen(d, obTestPlayer) {
		t.Fatal("the first run did not record itself, so this test proves nothing")
	}

	replay := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	if replay.step != 0 || replay.done {
		t.Errorf("the replay opens on step %d (done=%v), not at the beginning", replay.step+1, replay.done)
	}
	if frame := replay.frame(); !strings.Contains(frame, "step 1 of 5") {
		t.Errorf("the replay's first frame is not the first step:\n%s", frame)
	}
	onboardingWalkTo(t, replay, onboardingSteps-1)
	if replay.step != onboardingSteps-1 {
		t.Errorf("the replay stopped at step %d", replay.step+1)
	}
	if !OnboardingSeen(d, obTestPlayer) {
		t.Error("replaying the introduction unset the flag")
	}
}

// TestASecondProfileOnTheSameMachineIsStillANewcomer is the case the whole
// per-profile decision rests on. Two people sharing a machine with a profile
// each are two players, and the second of them is exactly the newcomer the
// introduction was written for: a flag belonging to the machine would let the
// first person through the door consume it for everybody.
func TestASecondProfileOnTheSameMachineIsStillANewcomer(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTestAs(t, d, "Ada", onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, m, "q")
	if !OnboardingSeen(d, "Ada") {
		t.Fatal("Ada's run did not record itself, so this test proves nothing")
	}

	if _, err := d.Profiles.Create("Grace"); err != nil {
		t.Fatal(err)
	}
	if OnboardingSeen(d, "Grace") {
		t.Error("Grace is told she has seen an introduction that Ada read")
	}

	// And the reverse: Grace going through it must not touch Ada's answer.
	g := newOnboardingTestAs(t, d, "Grace", onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, g, "q")
	if !OnboardingSeen(d, "Grace") {
		t.Error("Grace's own run was not recorded")
	}
	if !OnboardingSeen(d, "Ada") {
		t.Error("Grace's run cleared Ada's")
	}
}

// TestTheFlagFollowsTheProfileTheRunIsPlayingAs is the regression for a defect
// found in review. The flag used to be read from and written to whichever profile
// the store called current, but --profile overrides the stored choice for one run
// without writing it back, precisely so a scripted game cannot retarget the next
// interactive one. So a run playing as Alice asked whether Bob had seen the
// introduction, and answered on his behalf: Alice never saw it, and Bob was offered
// it again.
func TestTheFlagFollowsTheProfileTheRunIsPlayingAs(t *testing.T) {
	d := onboardingTestDeps(t)
	for _, name := range []string{"Bob", "Alice"} {
		if _, err := d.Profiles.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	// Bob is the stored choice; Alice is who this run is playing as.
	if _, err := d.Profiles.UseCurrent("Bob"); err != nil {
		t.Fatal(err)
	}

	if OnboardingSeen(d, "Alice") {
		t.Fatal("Alice has not seen it yet")
	}
	m := newOnboardingTestAs(t, d, "Alice", onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, m, "q")

	if !OnboardingSeen(d, "Alice") {
		t.Error("Alice's run was not recorded against Alice")
	}
	if OnboardingSeen(d, "Bob") {
		t.Error("Alice's run was recorded against Bob, the stored choice she overrode")
	}
	if cur, ok := d.Profiles.Current(); !ok || cur.Name != "Bob" {
		t.Errorf("the stored choice moved to %v; --profile must not write it back", cur.Name)
	}
}

// TestTheIntroductionIsOfferedWhenThereIsNoProfileToAsk pins the answer given
// when the question cannot be: offer it. The other answer would lose the
// introduction to whoever has a configuration directory it cannot read, which
// is the player most likely to need it.
func TestTheIntroductionIsOfferedWhenThereIsNoProfileToAsk(t *testing.T) {
	d := shellTestDeps(t)
	if OnboardingSeen(d, obTestPlayer) {
		t.Error("with no profile chosen the introduction is reported as seen")
	}
	if OnboardingSeen(Deps{}, obTestPlayer) {
		t.Error("with no profile store at all the introduction is reported as seen")
	}

	// It must also run and leave cleanly, since that is what the answer above
	// commits the program to.
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	if cmd := onboardingPress(t, m, "q"); cmd == nil {
		t.Error("with no profile to record against, the introduction cannot be left")
	}
}

// TestTheGlobalQuitKeyCountsAsSeen closes the gap the Departing interface
// exists for. The shell answers the control form of the quit key itself, so the
// screen never sees the key: without Depart, leaving with ctrl+c would show the
// introduction and not record it, and quitting with the plain letter and with
// the control key are the same act from the player's point of view.
func TestTheGlobalQuitKeyCountsAsSeen(t *testing.T) {
	d := onboardingTestDeps(t)
	s, err := NewOnboarding(d, obTestPlayer)
	if err != nil {
		t.Fatal(err)
	}
	shell := NewShell(d, s)
	shell.Update(tea.WindowSizeMsg{Width: onboardingRoomy[0], Height: onboardingRoomy[1]})
	if !shellRun(t, shell, shellKeyPress("ctrl+c")) {
		t.Fatal("ctrl+c did not end the program")
	}
	if !onboardingIntroduced(t, d.ConfigDir) {
		t.Error("quitting with ctrl+c did not count as seen")
	}
}

// TestNoStepRefusesToAdvance is the no-wall requirement stated as a property.
// The introduction has steps that invite a move, and not one of them may make
// the move a condition of going on.
func TestNoStepRefusesToAdvance(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	invited := 0
	for step := range onboardingSteps {
		if m.steps[step].invite != "" {
			invited++
		}
		before := m.step
		onboardingPress(t, m, "n")
		if step+1 < onboardingSteps && m.step != before+1 {
			t.Fatalf("step %d did not advance without a peg being placed", step+1)
		}
	}
	if invited == 0 {
		t.Fatal("no step invites a move, so this test proves nothing")
	}
	if !m.done {
		t.Fatalf("walking every step left the introduction on step %d", m.step+1)
	}
}

// TestEveryFrameNamesTheKeyThatLeaves is the other half of being skippable: a
// way out nobody is told about is not one. The hint is required at every step,
// on the closing panel, and at every size the program supports.
func TestEveryFrameNamesTheKeyThatLeaves(t *testing.T) {
	quit, ok := ui.DefaultKeymap().ByAction(ui.CtxBoard, ui.ActQuit)
	if !ok {
		t.Fatal("the keymap binds no quit key")
	}
	for _, size := range shellSizes {
		w, h := size[0], size[1]
		d := onboardingTestDeps(t)
		m := newOnboardingTest(t, d, w, h)
		for step := 0; ; step++ {
			frame := m.frame()
			shellAssertFits(t, "the introduction", frame, w, h)
			arr := m.arrange()
			switch {
			case arr.TooSmall:
				// The too-small notice is the one frame with no status line;
				// ui owns it and it says what it can in the columns there are.
			case m.done:
				// The closing panel offers one key and leaving is what it does,
				// so a second name for the same act would be noise.
				if !strings.Contains(frame, tutorialKeyLabel(tutActNext)) {
					t.Errorf("at %dx%d the closing panel names no key:\n%s", w, h, frame)
				}
			default:
				if !strings.Contains(frame, quit.Label+" skip") {
					t.Errorf("at %dx%d step %d does not name the key that leaves:\n%s", w, h, m.step+1, frame)
				}
			}
			if m.done {
				break
			}
			// A twenty-column pane pages every step several times over, so the
			// bound is generous: it is here to catch a pager that has stopped
			// advancing, not to assert a page count.
			if step > 60*onboardingSteps {
				t.Fatalf("at %dx%d the introduction did not finish in %d presses", w, h, step)
			}
			onboardingPress(t, m, "n")
		}
	}
}

// TestTheIntroductionRendersAtEverySupportedSize covers the sizes a board
// cannot be drawn at, which is the size this screen is required to survive.
// Each step's frame is rendered rather than only the first, because it is the
// steps that talk about marked holes that a missing board would break.
func TestTheIntroductionRendersAtEverySupportedSize(t *testing.T) {
	sizes := append([][2]int{{40, 12}, {28, 22}, {19, 5}, {1, 1}}, shellSizes...)
	for _, size := range sizes {
		w, h := size[0], size[1]
		d := onboardingTestDeps(t)
		m := newOnboardingTest(t, d, w, h)
		for step := range onboardingSteps {
			if err := m.loadStep(step); err != nil {
				t.Fatalf("step %d: %v", step+1, err)
			}
			shellAssertFits(t, "the introduction", m.frame(), w, h)
		}
		m.done = true
		shellAssertFits(t, "the closing panel", m.frame(), w, h)
	}
}

// TestNoPageOfProseIsPaddedWithBlanks is why this screen does not share the
// tutorial's pager clamp. The tutorial pages in whole panels, so a step that
// overflows by one line ends on a page holding that line above nine blank rows;
// in a lesson somebody chose to work through that is merely untidy, and three
// keypresses into an introduction it reads as the program having stopped.
//
// The property, stated from the frame: whenever there is more text than the
// panel holds, the panel is full.
func TestNoPageOfProseIsPaddedWithBlanks(t *testing.T) {
	overflowed := 0
	for _, size := range append([][2]int{{80, 24}, {60, 20}}, shellSizes...) {
		w, h := size[0], size[1]
		d := onboardingTestDeps(t)
		m := newOnboardingTest(t, d, w, h)
		for press := 0; ; press++ {
			arr := m.arrange()
			if !arr.TooSmall && arr.PanelH > 0 {
				total := len(m.panel(arr).lines)
				if total > arr.PanelH {
					overflowed++
					if m.scroll+arr.PanelH > total {
						t.Errorf("at %dx%d step %d shows %d rows of a %d-line panel from offset %d, so %d rows are blank padding",
							w, h, m.step+1, arr.PanelH, total, m.scroll, m.scroll+arr.PanelH-total)
					}
				}
			}
			if m.done {
				break
			}
			if press > 60*onboardingSteps {
				t.Fatalf("at %dx%d the introduction did not finish", w, h)
			}
			onboardingPress(t, m, "n")
		}
	}
	if overflowed == 0 {
		t.Fatal("no size in the set overflows its panel, so this test proves nothing")
	}
}

// TestTheBoardIsDroppedRatherThanClipped is the layout decision this screen
// makes differently from the tutorial. A reader who has not chosen to be here
// is worse served by four rows of a twelve-row board — with the border rows the
// prose is about behind the viewport arrow — than by the four lines of prose
// those rows would have held. So the board is either whole or absent.
func TestTheBoardIsDroppedRatherThanClipped(t *testing.T) {
	n := 12
	drawn := 0
	for w := ui.MinWidth; w <= 120; w += 3 {
		for h := ui.MinHeight; h <= 44; h += 2 {
			arr := onboardingArrange(w, h, n)
			if arr.TooSmall || arr.BoardH == 0 {
				continue
			}
			drawn++
			blockW, blockH := arr.Scale.BlockSize(n)
			if blockW > arr.BoardW || blockH > arr.BoardH {
				t.Fatalf("at %dx%d a %s board of %dx%d is drawn into %dx%d",
					w, h, arr.Scale, blockW, blockH, arr.BoardW, arr.BoardH)
			}
		}
	}
	if drawn == 0 {
		t.Fatal("no size in the range draws a board, so this test proves nothing")
	}
}

// TestTheBoardVanishesInATerminalTooSmallForIt states the same rule from the
// player's side, and pins that the prose keeps the whole pane when it does.
func TestTheBoardVanishesInATerminalTooSmallForIt(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, 40, 12)
	arr := m.arrange()
	if arr.BoardH != 0 {
		t.Fatalf("at 40x12 the board is given %d rows", arr.BoardH)
	}
	if arr.PanelH != 11 {
		t.Errorf("at 40x12 the prose has %d rows, want the 11 above the status line", arr.PanelH)
	}
	frame := m.frame()
	if strings.Contains(frame, "A B C D") {
		t.Errorf("at 40x12 a board is drawn after all:\n%s", frame)
	}
	if !strings.Contains(frame, "step 1 of 5") {
		t.Errorf("at 40x12 the step is not readable:\n%s", frame)
	}
}

// TestTheDepartingLineNamesTheTutorialOnce is how a player who skipped ever
// learns the tutorial exists. The line goes to the shell rather than onto a
// step, because the step that says where the tutorial is is exactly the step a
// player who skips never reaches.
func TestTheDepartingLineNamesTheTutorialOnce(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingPress(t, m, "q")

	note := m.DepartNote()
	if !strings.Contains(note, onboardingTutorialEntry) {
		t.Errorf("the departing line does not name the menu entry: %q", note)
	}
	if again := m.DepartNote(); again != "" {
		t.Errorf("the departing line is given a second time: %q", again)
	}
}

// TestTheMenuEntryTheIntroductionPointsAtExists guards the one string in this
// file that names something outside it. A pointer at a menu entry that has been
// renamed is worse than no pointer at all, and the entry table is not reachable
// from here, so the label is checked against the menu a player really gets.
func TestTheMenuEntryTheIntroductionPointsAtExists(t *testing.T) {
	d := onboardingTestDeps(t)
	menu := NewMenu(d, "Ada")
	menu.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	if frame := menu.View().Content; !strings.Contains(frame, onboardingTutorialEntry) {
		t.Errorf("the menu has no %q entry for the introduction to point at:\n%s", onboardingTutorialEntry, frame)
	}
}

// TestPlacingAPegOnTheLinkStepMakesALink and its blocking twin are why the
// introduction is built on the real engine: the two steps are the same act with
// opposite answers, and the line the player is given is read off the board
// rather than written beside each step.
func TestPlacingAPegOnTheLinkStepMakesALink(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingWalkToStep(t, m, "links")

	onboardingPlaceAt(t, m, "G8")
	if got := m.g.At(onboardingPoint(t, "G8")); got != game.Vertical {
		t.Fatalf("G8 holds %v after the peg was placed", got)
	}
	if m.g.LinkMask(onboardingPoint(t, "G8")) == 0 {
		t.Fatal("the link step made no link, so it teaches the opposite of what it says")
	}
	if !strings.Contains(m.told, "There it is") {
		t.Errorf("the player is not told a link was made: %q", m.told)
	}
}

// TestTheAnswerToAPegIsBroughtIntoView is what makes placing a peg feel like it
// did something. At eighty by twenty-four a step's prose already fills the
// panel, so the line the peg earned is off the bottom of it: without paging to
// the line, the keypress changes one glyph on the board and nothing else, and
// the player has no way to know there was anything to read.
func TestTheAnswerToAPegIsBroughtIntoView(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, 80, 24)
	onboardingWalkToStep(t, m, "peg")

	arr := m.arrange()
	if len(m.panel(arr).lines) > arr.PanelH {
		t.Fatal("the step already pages before a peg is placed, so this test would prove nothing")
	}
	onboardingPlaceAt(t, m, "F6")
	if m.told == "" {
		t.Fatal("placing a peg said nothing")
	}
	// The whole answer is asserted, not its opening: the panel overflows by
	// less than the answer's own height, so leaving the pager at the top still
	// shows the answer's first line and cuts the rest — which is the bug this
	// guards, and an assertion on the first line alone would miss it.
	wrapped := tutorialWrap(m.told, arr.PanelW)
	if len(wrapped) < 2 {
		t.Fatalf("the answer is one line at this width, so this test would prove nothing: %q", m.told)
	}
	after := m.panel(m.arrange())
	if len(after.lines) <= arr.PanelH {
		t.Fatal("the answer fits without paging, so this test would prove nothing")
	}
	if len(after.lines)-after.toldAt >= arr.PanelH {
		t.Fatal("the answer is taller than the panel, so nothing could show all of it")
	}
	frame := m.frame()
	for i, line := range wrapped {
		if !strings.Contains(frame, line) {
			t.Errorf("line %d of the answer is not on screen after the peg went down: %q\n%s", i+1, line, frame)
		}
	}
}

// TestPlacingAPegOnTheBlockingStepMakesNoLink is the step that would be a lie
// if the engine were not underneath it.
func TestPlacingAPegOnTheBlockingStepMakesNoLink(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingWalkToStep(t, m, "blocking")

	g8 := onboardingPoint(t, "G8")
	onboardingPlaceAt(t, m, "G8")
	if got := m.g.At(g8); got != game.Vertical {
		t.Fatalf("G8 holds %v: the peg was refused, and the step is that nothing refuses it", got)
	}
	if mask := m.g.LinkMask(g8); mask != 0 {
		t.Fatalf("a link was made on the blocking step (mask %d)", mask)
	}
	if !strings.Contains(m.told, "no link was made") {
		t.Errorf("the player is not told the link was blocked: %q", m.told)
	}
	if !strings.Contains(m.told, "crosses a link already on the board") {
		t.Errorf("the player is not told why: %q", m.told)
	}
}

// TestAnIllegalHoleIsExplainedRatherThanRefused covers the two mistakes
// newcomers actually make. Both are illegal moves, and a player silently
// stopped has learned nothing from being stopped.
func TestAnIllegalHoleIsExplainedRatherThanRefused(t *testing.T) {
	cases := []struct {
		hole, says string
	}{
		{"A1", "missing corners"},
		// A6 is in a border column, so the refusal must name columns. Naming the
		// rows instead was a real defect: the sentence was written from Vertical's
		// point of view and used whatever side happened to be on the move.
		{"A6", "left and right columns"},
	}
	for _, c := range cases {
		t.Run(c.hole, func(t *testing.T) {
			d := onboardingTestDeps(t)
			m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
			onboardingWalkToStep(t, m, "peg")

			p := onboardingPoint(t, c.hole)
			onboardingPlaceAt(t, m, c.hole)
			if got := m.g.At(p); got != game.NoPlayer {
				t.Fatalf("%s holds %v: an illegal move was played", c.hole, got)
			}
			if !strings.Contains(m.told, c.says) {
				t.Errorf("%s is refused without naming the rule: %q", c.hole, m.told)
			}
			// The step must still be leavable and still advance.
			before := m.step
			onboardingPress(t, m, "n")
			if m.step == before {
				t.Error("an illegal move left the step unable to advance")
			}
		})
	}
}

// TestEveryStepPositionLoads is the content check: a move list that does not
// replay is a test failure rather than something a first-run player discovers.
func TestEveryStepPositionLoads(t *testing.T) {
	steps := onboardingContent()
	if len(steps) != onboardingSteps {
		t.Fatalf("the introduction has %d steps, not the %d it claims", len(steps), onboardingSteps)
	}
	seen := make(map[string]bool, len(steps))
	for i, s := range steps {
		if s.id == "" {
			t.Errorf("step %d has no id", i+1)
		}
		if seen[s.id] {
			t.Errorf("step %d repeats the id %q, so a test naming it means either", i+1, s.id)
		}
		seen[s.id] = true
		g, err := s.position()
		if err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if s.text == "" {
			t.Errorf("step %d has no prose", i+1)
		}
		for _, p := range s.highlight {
			if !g.InBounds(p) {
				t.Errorf("step %d marks %s, which is off the board", i+1, p)
			}
		}
		if s.invite != "" && s.told == nil {
			t.Errorf("step %d invites a move and says nothing about it", i+1)
		}
		if s.invite == "" && s.told != nil {
			t.Errorf("step %d has a line about a move it never invites", i+1)
		}
	}
}

// TestTheFirstStepShowsAFinishedGame checks the picture the first step's prose
// claims. The step says a chain runs from the top row to the bottom and names
// its two ends; a position that is not a finished win would make the first
// thing a newcomer reads untrue.
func TestTheFirstStepShowsAFinishedGame(t *testing.T) {
	g, err := onboardingContent()[0].position()
	if err != nil {
		t.Fatal(err)
	}
	if !g.Result().Over() {
		t.Fatal("the first step's position is not a finished game")
	}
	if got := g.Result().Winner(); got != game.Vertical {
		t.Errorf("the first step's position is won by %v, and the prose says Vertical", got)
	}
	for _, name := range []string{"F1", "E12"} {
		p := onboardingPoint(t, name)
		if got := g.At(p); got != game.Vertical {
			t.Errorf("the prose names %s as an end of the chain, and it holds %v", name, got)
		}
	}
}

// TestTheIntroductionAnswersOnlyKeysItNames guards against a key that does
// something the status line never offered. The tutorial's teaching keys are the
// ones at risk, since the introduction takes its pager keys from that table and
// has no answer, no restart and no key page of its own.
func TestTheIntroductionAnswersOnlyKeysItNames(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingWalkTo(t, m, onboardingSteps-1)
	before := m.frame()
	for _, key := range []string{"r", "s", "?", "x", "a", "1"} {
		if cmd := onboardingPress(t, m, key); cmd != nil {
			t.Errorf("%q returned a command from the introduction", key)
		}
		if after := m.frame(); after != before {
			t.Errorf("%q changed the frame, and nothing names it:\n%s", key, after)
		}
	}
}

// onboardingWalkToStep advances to the step with the given id, so a test names
// the step it means by identity rather than by an index or a phrase, either of
// which a change to the content would silently move.
func onboardingWalkToStep(t *testing.T, m *onboardingModel, id string) {
	t.Helper()
	for i, s := range m.steps {
		if s.id == id {
			onboardingWalkTo(t, m, i)
			return
		}
	}
	t.Fatalf("the introduction has no step called %q", id)
}

// onboardingPlaceAt walks the cursor onto a hole with the movement keys and
// presses the peg key, which is how a player gets there.
func onboardingPlaceAt(t *testing.T, m *onboardingModel, hole string) {
	t.Helper()
	p := onboardingPoint(t, hole)
	km := ui.DefaultKeymap()
	key := func(a ui.Action) string {
		b, ok := km.ByAction(ui.CtxBoard, a)
		if !ok {
			t.Fatalf("the keymap binds nothing for action %d", a)
		}
		return b.Keys[0]
	}
	for range 4 * m.g.Size() {
		c := m.board.Cursor
		switch {
		case c.Col < p.Col:
			onboardingPress(t, m, key(ui.ActMoveRight))
		case c.Col > p.Col:
			onboardingPress(t, m, key(ui.ActMoveLeft))
		case c.Row < p.Row:
			onboardingPress(t, m, key(ui.ActMoveDown))
		case c.Row > p.Row:
			onboardingPress(t, m, key(ui.ActMoveUp))
		default:
			onboardingPress(t, m, key(ui.ActPlacePeg))
			return
		}
	}
	t.Fatalf("the cursor never reached %s from %s", hole, m.board.Cursor)
}

// onboardingPoint parses a hole name the way the content writes them.
func onboardingPoint(t *testing.T, name string) game.Point {
	t.Helper()
	p, err := game.ParsePoint(name)
	if err != nil {
		t.Fatalf("parsing %q: %v", name, err)
	}
	return p
}

// TestTheInvitationIsAnsweredOnceSoThePlayerStaysVertical is the regression for a
// defect a review found by pressing the advertised key twice. The engine
// alternates the mover, so the second peg was played for Horizontal while every
// line of the introduction still addressed the player as Vertical: the caption
// congratulated them on a hole somebody else had just taken, and the refusal named
// Vertical's forbidden columns for a hole in Horizontal's forbidden rows.
func TestTheInvitationIsAnsweredOnceSoThePlayerStaysVertical(t *testing.T) {
	d := onboardingTestDeps(t)
	m := newOnboardingTest(t, d, onboardingRoomy[0], onboardingRoomy[1])
	onboardingWalkToStep(t, m, "peg")
	step := m.step
	theirs := onboardingCount(m, game.Horizontal)

	// The invitation, answered. It is the player's own peg and it does not hand
	// the turn over: the prose addresses the player as Vertical throughout.
	onboardingPlaceAt(t, m, "F6")
	if got := m.g.At(onboardingPoint(t, "F6")); got != game.Vertical {
		t.Fatalf("the invited peg is %v, want the player's own colour", got)
	}
	if got := onboardingCount(m, game.Horizontal); got != theirs {
		t.Fatalf("answering the invitation changed the opponent's pegs from %d to %d", theirs, got)
	}
	if m.step != step {
		t.Fatalf("answering the invitation left the step")
	}

	// A second press must page on rather than place again. Placing again was the
	// defect: the engine had moved the turn to Horizontal, so the player was
	// handed the other side while the text still said "you".
	onboardingPress(t, m, "space")
	if m.step == step {
		t.Errorf("a second press stayed on the step; it either placed for the opponent or did nothing")
	}
}

// onboardingCount counts a player's pegs on the step's board.
func onboardingCount(m *onboardingModel, pl game.Player) int {
	n := 0
	for row := range m.g.Size() {
		for col := range m.g.Size() {
			if m.g.At(game.Point{Col: col, Row: row}) == pl {
				n++
			}
		}
	}
	return n
}

// TestTheIntroductionTeachesTheRulesItIsPlayedUnder pins the content against the
// ruleset it actually runs on. A review found the links step saying links are
// made for you rather than by you, which is the paper-and-pencil rule; the
// introduction runs on the default ruleset, where linking is deliberate, so the
// first thing a player met in a real game — link mode, offering the choice — had
// been described to them as something that does not happen.
//
// Asserted against the ruleset rather than against a phrase, so that changing the
// ruleset the introduction uses cannot leave the prose behind.
func TestTheIntroductionTeachesTheRulesItIsPlayedUnder(t *testing.T) {
	rs := learn.Rules()
	steps := onboardingContent()

	// Every step, not only the one named after the topic. Scoping this to the
	// links step let the goal step go on teaching that links form on their own,
	// under a ruleset where the player is offered them — so the first screen and
	// the fourth stated different rules, and the fix to one of them read as a fix
	// to both.
	if rs.DeliberateLinking {
		for _, s := range steps {
			for _, wrong := range []string{"link up as", "as the peg goes down", "made for you", "automatically"} {
				if strings.Contains(s.text, wrong) {
					t.Errorf("linking is deliberate under %s, yet step %q says %q:\n%s",
						rs.Describe(), s.id, wrong, s.text)
				}
			}
		}
	}

	var links string
	for _, s := range steps {
		if s.id == "links" {
			links = s.text
		}
	}
	if links == "" {
		t.Fatal("the introduction has no step about links")
	}

	if rs.DeliberateLinking {
		// The player chooses. The step must not say the choice is absent, and
		// must say the choice exists.
		for _, wrong := range []string{"made for you rather than by you", "permanent"} {
			if strings.Contains(links, wrong) {
				t.Errorf("linking is deliberate under %s, yet the step says %q", rs.Describe(), wrong)
			}
		}
		if !strings.Contains(links, "decline") {
			t.Errorf("linking is deliberate under %s, yet the step never says the player may decline a link:\n%s",
				rs.Describe(), links)
		}
		return
	}
	// Links are automatic and permanent. Then the step must not promise a choice.
	if strings.Contains(links, "decline") {
		t.Errorf("linking is automatic under %s, yet the step offers to decline a link", rs.Describe())
	}
}

// TestTheLinksStepMarksExactlyTheHolesItNames closes a gap a review proved by
// mutation: deleting one hole from the marked list passed the whole suite, so the
// eight holes drawn on the board could drift away from the eight the sentence
// beside them names. That is the same content-against-board drift as the links
// step's rule claim, which had already been caught once.
//
// The expected set is computed from the engine rather than written out, so this
// cannot drift either: it is the knight's moves from F6 that land on the board.
func TestTheLinksStepMarksExactlyTheHolesItNames(t *testing.T) {
	var step onboardingStep
	for _, s := range onboardingContent() {
		if s.id == "links" {
			step = s
		}
	}
	if step.id == "" {
		t.Fatal("the introduction has no step about links")
	}

	from, err := game.ParsePoint("F6")
	if err != nil {
		t.Fatal(err)
	}
	n := learn.Rules().Size
	want := map[game.Point]bool{}
	for d := game.Dir(0); d < game.NumDirs; d++ {
		q := from.Add(d)
		if q.Col >= 0 && q.Row >= 0 && q.Col < n && q.Row < n {
			want[q] = true
		}
	}
	if len(want) != 8 {
		t.Fatalf("F6 has %d knight's moves on a %dx%d board; the step's sentence says eight", len(want), n, n)
	}

	got := map[game.Point]bool{}
	for _, p := range step.highlight {
		got[p] = true
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%v can link to F6 but is not marked", p)
		}
		if !strings.Contains(step.text, p.String()) {
			t.Errorf("%v is a knight's move from F6 but the sentence does not name it", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("%v is marked but cannot link to F6", p)
		}
	}
}
