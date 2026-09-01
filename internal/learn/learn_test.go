package learn

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// stepKey names a step the way the tables below refer to it.
func stepKey(l Lesson, i int) string { return l.ID + "/" + strconv.Itoa(i) }

// pointNames renders a set of holes in the sorted form the claim tests below
// compare against, so a position the rules engine works out can be held up
// against the holes a lesson actually names.
func pointNames(ps []game.Point) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

// wrongAnswers is one move per task that the tutorial has to reject, together
// with the substrings its feedback has to contain. The point is not that the
// move is refused but that the learner is told the real reason, so each entry
// names a specific misconception. TestEveryTaskHasAWrongAnswer fails if a task
// is missing from this table, so a new task cannot arrive without one.
var wrongAnswers = map[string]struct {
	played string
	reason []string
}{
	// Reaching into the opponent's border line.
	"board/3": {"A5", []string{"in Horizontal's border line", "never place a peg in your opponent's border line"}},
	// Expecting a diagonal neighbour to link.
	"links/1": {"G7", []string{"is a diagonal neighbour of F6", "knight's move"}},
	// An opponent's link across the route.
	"links/2": {"G8", []string{"no link would appear", "Horizontal's link F7:H8", "crosses the line from F6 to G8"}},
	// The same geometry in one's own colour.
	"links/3": {"G8", []string{"your own link F7:H8", "not even one of your own"}},
	"links/5": {"G7", []string{"is a diagonal neighbour of F6"}},
	// A peg that makes no link of its own cannot block a link.
	"blocking/2": {"F6", []string{"makes no link of yours", "Horizontal still links C6 to A5 and A7"}},
	// The hole the opponent has just taken.
	"double-threat/1": {"G8", []string{"already holds a Horizontal peg", "second route"}},
	// A finished link where a setup was asked for.
	"double-threat/2": {"G8", []string{"knight's move from E7", "made straight away"}},
	// Two holes in the same column never link, border row or not.
	"winning/1": {"H12", []string{"right beside H11", "holes side by side do not link"}},
	// Reaching only one side of the gap.
	"practice/0": {"D7", []string{"links to E5", "not to G9"}},
	// Ignoring the threat.
	"practice/1": {"F6", []string{"does not stop it", "joins its two groups"}},
}

func TestSetupsReplay(t *testing.T) {
	positions := 0
	for _, l := range Lessons() {
		for i, s := range l.Steps {
			g, err := s.Position()
			if err != nil {
				t.Errorf("%s: setup %v does not replay: %v", stepKey(l, i), s.Setup, err)
				continue
			}
			if g.Size() != Rules().Size {
				t.Errorf("%s: board is %d wide, want %d", stepKey(l, i), g.Size(), Rules().Size)
			}
			if g.Ply() != len(s.Setup) {
				t.Errorf("%s: %d moves replayed from a %d move setup", stepKey(l, i), g.Ply(), len(s.Setup))
			}
			positions++
		}
	}
	t.Logf("replayed %d lesson positions on %s", positions, Rules().Canonical())
}

func TestHighlightsAreRealHoles(t *testing.T) {
	board := game.MustNew(Rules())
	for _, l := range Lessons() {
		for i, s := range l.Steps {
			for _, p := range s.Highlight {
				if !board.InBounds(p) {
					t.Errorf("%s: highlighted %s is off the board", stepKey(l, i), p)
					continue
				}
				if board.IsCorner(p) {
					t.Errorf("%s: highlighted %s is a corner, which does not exist", stepKey(l, i), p)
				}
			}
		}
	}
}

func TestStepsCarryContent(t *testing.T) {
	for _, l := range Lessons() {
		if l.Title == "" || l.Summary == "" {
			t.Errorf("%s: lesson is missing a title or summary", l.ID)
		}
		if len(l.Steps) == 0 {
			t.Errorf("%s: lesson has no steps", l.ID)
		}
		for i, s := range l.Steps {
			key := stepKey(l, i)
			if strings.TrimSpace(s.Text) == "" {
				t.Errorf("%s: step text is empty", key)
			}
			if strings.ContainsAny(s.Text, "\n\r\t") {
				t.Errorf("%s: step text carries a hard line break, so the UI cannot wrap it", key)
			}
			if s.Task == nil {
				continue
			}
			if strings.TrimSpace(s.Task.Prompt) == "" {
				t.Errorf("%s: task has no prompt", key)
			}
			if strings.ContainsAny(s.Task.Prompt, "\n\r\t") {
				t.Errorf("%s: task prompt carries a hard line break", key)
			}
			if s.Task.Accept == nil {
				t.Errorf("%s: task has no checker", key)
			}
		}
	}
}

func TestLessonIDsUniqueAndFindable(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range Lessons() {
		if l.ID == "" {
			t.Error("lesson with an empty id")
		}
		if seen[l.ID] {
			t.Errorf("duplicate lesson id %q", l.ID)
		}
		seen[l.ID] = true
		found, ok := Find(l.ID)
		if !ok {
			t.Errorf("Find(%q) found nothing", l.ID)
			continue
		}
		if found.Title != l.Title || len(found.Steps) != len(l.Steps) {
			t.Errorf("Find(%q) returned a different lesson", l.ID)
		}
	}
	if _, ok := Find("no-such-lesson"); ok {
		t.Error("Find returned a lesson for an unknown id")
	}
}

func TestTaskAnswersAccepted(t *testing.T) {
	tasks := 0
	for _, l := range Lessons() {
		for i, s := range l.Steps {
			if s.Task == nil {
				continue
			}
			key := stepKey(l, i)
			g, err := s.Position()
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			// Every task in the tutorial puts the learner on Vertical's side,
			// which the prompts all assume.
			if g.Turn() != game.Vertical {
				t.Errorf("%s: %s is to move, but the prompts address Vertical", key, g.Turn())
			}
			if g.Result().Over() {
				t.Errorf("%s: the position is already finished, so no move can be made", key)
			}
			if err := g.CanPlace(g.Turn(), s.Task.Answer); err != nil {
				t.Errorf("%s: the model answer %s is not even legal: %v", key, s.Task.Answer, err)
			}
			before, err := g.Transcript()
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			ok, feedback := s.Task.Accept(g, s.Task.Answer)
			if !ok {
				t.Errorf("%s: model answer %s rejected: %s", key, s.Task.Answer, feedback)
			}
			if strings.TrimSpace(feedback) == "" {
				t.Errorf("%s: accepted %s with no feedback", key, s.Task.Answer)
			}
			if strings.ContainsAny(feedback, "\n\r\t") {
				t.Errorf("%s: feedback carries a hard line break: %q", key, feedback)
			}
			after, err := g.Transcript()
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			if before != after {
				t.Errorf("%s: Accept modified the position: %q became %q", key, before, after)
			}
			tasks++
		}
	}
	t.Logf("accepted the model answer of %d tasks", tasks)
}

func TestWrongAnswersRejectedWithAReason(t *testing.T) {
	for _, l := range Lessons() {
		for i, s := range l.Steps {
			if s.Task == nil {
				continue
			}
			key := stepKey(l, i)
			probe, found := wrongAnswers[key]
			if !found {
				continue // reported by TestEveryTaskHasAWrongAnswer
			}
			g, err := s.Position()
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			played, err := game.ParsePoint(probe.played)
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			ok, feedback := s.Task.Accept(g, played)
			if ok {
				t.Errorf("%s: %s was accepted, but it is meant to be wrong: %s", key, probe.played, feedback)
				continue
			}
			for _, want := range probe.reason {
				if !strings.Contains(feedback, want) {
					t.Errorf("%s: rejecting %s did not explain %q; feedback was %q", key, probe.played, want, feedback)
				}
			}
			if strings.ContainsAny(feedback, "\n\r\t") {
				t.Errorf("%s: feedback carries a hard line break: %q", key, feedback)
			}
		}
	}
}

func TestEveryTaskHasAWrongAnswer(t *testing.T) {
	var keys []string
	for _, l := range Lessons() {
		for i, s := range l.Steps {
			if s.Task == nil {
				continue
			}
			key := stepKey(l, i)
			keys = append(keys, key)
			if _, ok := wrongAnswers[key]; !ok {
				t.Errorf("%s: no wrong answer is tested for this task", key)
			}
		}
	}
	for key := range wrongAnswers {
		if !slices.Contains(keys, key) {
			t.Errorf("wrongAnswers has an entry for %q, which is not a task", key)
		}
	}
}

// TestSetupTaskNamesEveryNearMiss walks the branches of the hardest task, the
// one that asks the learner to build a setup. Each near miss has its own
// reason, and a learner told the wrong one would draw the wrong lesson.
func TestSetupTaskNamesEveryNearMiss(t *testing.T) {
	l, ok := Find("double-threat")
	if !ok {
		t.Fatal("no double-threat lesson")
	}
	step := l.Steps[2]
	g, err := step.Position()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		played string
		accept bool
		reason string
	}{
		{"F10", true, "one column across and three rows down"},
		{"D10", true, "one column across and three rows down"},
		{"H8", true, "three columns across and one row down"},
		{"B8", true, "three columns across and one row down"},
		// Two joining holes, but they touch, so one peg threatens both.
		{"H10", false, "they are touching"},
		// Two joining holes far apart, but the pair has gone nowhere.
		{"F8", false, "barely travels"},
		// One joining hole is not a setup at all.
		{"G11", false, "A setup needs two joining holes, not one"},
		// No joining hole.
		{"G10", false, "Nothing joins G10 to E7"},
		// A finished link instead of a setup.
		{"G8", false, "made straight away"},
		// The wrong way up the board.
		{"F4", false, "makes no ground towards the bottom row"},
	}
	for _, c := range cases {
		p, err := game.ParsePoint(c.played)
		if err != nil {
			t.Fatal(err)
		}
		ok, feedback := step.Task.Accept(g, p)
		if ok != c.accept {
			t.Errorf("%s: ok=%v, want %v (%s)", c.played, ok, c.accept, feedback)
		}
		if !strings.Contains(feedback, c.reason) {
			t.Errorf("%s: feedback %q does not explain %q", c.played, feedback, c.reason)
		}
	}
}

// TestPuzzleClaims checks the tactical claims the prose makes about the three
// worked positions. The prose says which moves win and which move saves; if a
// setup is ever edited, these are the statements that quietly become lies.
func TestPuzzleClaims(t *testing.T) {
	winners := func(t *testing.T, s Step) []string {
		t.Helper()
		g, err := s.Position()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range g.LegalPlacements(g.Turn()) {
			c := g.Clone()
			res, err := c.PlayPeg(p)
			if err == nil && res.Winner() == g.Turn() {
				out = append(out, p.String())
			}
		}
		slices.Sort(out)
		return out
	}

	winning, ok := Find("winning")
	if !ok {
		t.Fatal("no winning lesson")
	}
	// The step before poses the same position and marks the holes its prose
	// names, so the claim is checked against the lesson rather than against a
	// copy of it kept here: editing the highlight without editing the position
	// used to leave this passing.
	marked := pointNames(winning.Steps[0].Highlight)
	if len(marked) == 0 {
		t.Fatal("winning lesson: the opening step marks no holes, so there is no claim left to check")
	}
	if got := winners(t, winning.Steps[1]); !slices.Equal(got, marked) {
		t.Errorf("winning lesson: winning moves are %v, but the step highlights %v", got, marked)
	}
	final, err := winning.Steps[2].Position()
	if err != nil {
		t.Fatal(err)
	}
	if final.Result().Winner() != game.Vertical || final.Result().Reason != game.Connection {
		t.Errorf("winning lesson: the finished position is %v/%v, want vertical by connection",
			final.Result().Outcome, final.Result().Reason)
	}

	practice, ok := Find("practice")
	if !ok {
		t.Fatal("no practice lesson")
	}
	// The model answer is the claim: the prompt says find *the* move that wins.
	if got, want := winners(t, practice.Steps[0]), []string{practice.Steps[0].Task.Answer.String()}; !slices.Equal(got, want) {
		t.Errorf("practice: winning moves are %v, want only the model answer %v", got, want)
	}

	save, err := practice.Steps[1].Position()
	if err != nil {
		t.Fatal(err)
	}
	// Horizontal must really be threatening to win, and exactly one Vertical
	// move may answer it, otherwise the exercise has no answer or many.
	var savers []string
	for _, p := range save.LegalPlacements(game.Vertical) {
		c := save.Clone()
		if _, err := c.PlayPeg(p); err != nil {
			continue
		}
		if c.Result().Over() {
			continue
		}
		if _, threatened := immediateWin(c); !threatened {
			savers = append(savers, p.String())
		}
	}
	slices.Sort(savers)
	if got, want := savers, []string{practice.Steps[1].Task.Answer.String()}; !slices.Equal(got, want) {
		t.Errorf("practice: moves that answer the threat are %v, want only the model answer %v", got, want)
	}
	idle := save.Clone()
	if _, err := idle.PlayPeg(game.Point{Col: 5, Row: 5}); err != nil {
		t.Fatal(err)
	}
	if _, threatened := immediateWin(idle); !threatened {
		t.Error("practice: Horizontal is not actually threatening to win, so the exercise is empty")
	}
}

// TestBlockingClaim checks the blocking lesson's three statements: that a peg in
// the way of a link is no obstruction at all, that Horizontal can reach its
// border from C6 by two routes, and that exactly one Vertical move closes both.
func TestBlockingClaim(t *testing.T) {
	l, ok := Find("blocking")
	if !ok {
		t.Fatal("no blocking lesson")
	}
	// Step one: Horizontal's peg on G7 sits across the line from F6 to H7 and
	// is meant to make no difference whatever.
	past, err := l.Steps[0].Position()
	if err != nil {
		t.Fatal(err)
	}
	f6, h7 := game.Point{Col: 5, Row: 5}, game.Point{Col: 7, Row: 6}
	if _, err := past.PlayPeg(h7); err != nil {
		t.Fatalf("H7: %v", err)
	}
	if !linked(past, f6, h7) {
		t.Error("a peg on G7 must not stop the link from F6 to H7")
	}

	step := l.Steps[2]
	g, err := step.Position()
	if err != nil {
		t.Fatal(err)
	}
	c6 := game.Point{Col: 2, Row: 5}
	before := pointNames(borderReach(g, game.Horizontal, c6))
	// Step two marks the two routes its prose names, on the same position.
	routes := pointNames(l.Steps[1].Highlight)
	if len(routes) == 0 {
		t.Fatal("the blocking lesson marks no routes, so there is no claim left to check")
	}
	if !slices.Equal(before, routes) {
		t.Errorf("C6 reaches %v, but the step marks %v", before, routes)
	}
	var cutters []string
	for _, p := range g.LegalPlacements(game.Vertical) {
		after := g.Clone()
		if _, err := after.PlayPeg(p); err != nil {
			continue
		}
		if len(borderReach(after, game.Horizontal, c6)) == 0 {
			cutters = append(cutters, p.String())
		}
	}
	slices.Sort(cutters)
	if got, want := cutters, []string{step.Task.Answer.String()}; !slices.Equal(got, want) {
		t.Errorf("moves that cut C6 off are %v, want only the model answer %v", got, want)
	}
}

// TestDeclinedLinkMatters checks the pair of positions the declining lesson
// turns on: the same peg on G8 makes no link while the F7 to H8 link is on the
// board, and makes one once that link has been declined.
func TestDeclinedLinkMatters(t *testing.T) {
	l, ok := Find("links")
	if !ok {
		t.Fatal("no links lesson")
	}
	f6, g8 := game.Point{Col: 5, Row: 5}, game.Point{Col: 6, Row: 7}
	blocked, err := l.Steps[3].Position() // own link F7 to H8 in place
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.PlayPeg(g8); err != nil {
		t.Fatalf("G8 should still be a legal placement: %v", err)
	}
	if linked(blocked, f6, g8) {
		t.Error("the F7 to H8 link should stop F6 linking to G8")
	}
	freed, err := l.Steps[4].Position() // same position with the link declined
	if err != nil {
		t.Fatal(err)
	}
	if _, err := freed.PlayPeg(g8); err != nil {
		t.Fatalf("G8: %v", err)
	}
	if !linked(freed, f6, g8) {
		t.Error("with the link declined, F6 to G8 should be made")
	}
}

// TestOwnLinkRemovalClaim grounds the two rules statements the own-link step
// makes: that a player may take their own link off again on a later turn, which
// frees what it was crossing, and that an opponent's link is never removable.
// Both depend on Rules() carrying LinkRemoval, so this fails loudly if the
// tutorial's ruleset is ever changed under the prose.
func TestOwnLinkRemovalClaim(t *testing.T) {
	if !Rules().LinkRemoval {
		t.Fatal("the own-link step tells the learner they may remove their own links, which this ruleset forbids")
	}
	l, ok := Find("links")
	if !ok {
		t.Fatal("no links lesson")
	}
	f6, g8 := game.Point{Col: 5, Row: 5}, game.Point{Col: 6, Row: 7}
	f7, h8 := game.Point{Col: 5, Row: 6}, game.Point{Col: 7, Row: 7}

	// Vertical, to move, removes its own F7 to H8 link and then plays G8: the
	// link the crossing forbade is now made.
	g, err := l.Steps[3].Position()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveLink(f7, h8); err != nil {
		t.Fatalf("removing one's own link before placing a peg: %v", err)
	}
	if _, err := g.PlayPeg(g8); err != nil {
		t.Fatalf("G8: %v", err)
	}
	if !linked(g, f6, g8) {
		t.Error("with the own link removed, F6 to G8 should be made")
	}

	// Horizontal owns the same link in the earlier position and Vertical must
	// not be able to touch it.
	theirs, err := l.Steps[2].Position()
	if err != nil {
		t.Fatal(err)
	}
	if theirs.LinkOwner(game.Link{From: f7, Dir: game.ESE}) != game.Horizontal {
		t.Fatalf("expected Horizontal to own F7:H8, got %v", theirs.LinkOwner(game.Link{From: f7, Dir: game.ESE}))
	}
	if err := theirs.RemoveLink(f7, h8); err == nil {
		t.Error("Vertical was allowed to remove Horizontal's link")
	}
}
