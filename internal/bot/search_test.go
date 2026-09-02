package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// testTiers is every tier, so that a property asserted once is asserted for all
// three.
var testTiers = []Tier{Beginner, Intermediate, Pro}

// boundedEngine is an engine of the given tier whose search is bounded by depth
// rather than by the clock, so the same seed and position must give the same
// answer however loaded the machine is. The depth is small enough that the search
// always finishes well inside the budget, and the budget is generous enough that
// it never decides anything; everything else about the tier — its candidate
// widths, its evaluation, its table, its sampling — is untouched, so this is the
// same code path the tier plays on.
func boundedEngine(t Tier, seed int64) *engine {
	p := tierParams(t)
	p.maxDepth = 2
	p.budget = time.Minute
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}
}

// fastEngine shortens a tier's budget without changing what separates it from
// the others, so that the property tests can run all three tiers over many
// positions in seconds.
func fastEngine(t Tier, seed int64, budget time.Duration) *engine {
	p := tierParams(t)
	p.budget = budget
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}
}

func TestTierNamesRoundTrip(t *testing.T) {
	names := TierNames()
	if len(names) != 3 {
		t.Fatalf("TierNames = %v, want three entries", names)
	}
	for i, name := range names {
		got, err := ParseTier(name)
		if err != nil {
			t.Fatalf("ParseTier(%q): %v", name, err)
		}
		if got != Tier(i) {
			t.Errorf("ParseTier(%q) = %v, want %v", name, got, Tier(i))
		}
		if got.String() != name {
			t.Errorf("Tier(%d).String() = %q, want %q", i, got.String(), name)
		}
		if TierSummary(name) == "" {
			t.Errorf("TierSummary(%q) is empty", name)
		}
	}
	if _, err := ParseTier("grandmaster"); err == nil {
		t.Error("ParseTier accepted an unknown tier")
	}
	if got, err := ParseTier("  PRO "); err != nil || got != Pro {
		t.Errorf("ParseTier(\"  PRO \") = %v, %v, want pro, nil", got, err)
	}
}

// TestMoveIsAlwaysLegal is the property that matters most: whatever the tier,
// the seed or the position, the hole handed back can actually be played.
func TestMoveIsAlwaysLegal(t *testing.T) {
	src := rand.New(rand.NewPCG(11, 12))
	ctx := context.Background()
	checked := 0
	for round := range 40 {
		g := randomGame(t, smallRules(8), 6+src.IntN(40), src)
		if g.Result().Over() {
			continue
		}
		for _, tier := range testTiers {
			b := fastEngine(tier, int64(round), 15*time.Millisecond)
			p, err := b.Move(ctx, g)
			if err != nil {
				t.Fatalf("%v.Move: %v\n%s", tier, err, g)
			}
			if err := g.CanPlace(g.Turn(), p); err != nil {
				t.Fatalf("%v returned illegal move %v: %v\n%s", tier, p, err, g)
			}
			checked++
		}
	}
	if checked < 60 {
		t.Fatalf("only %d moves were checked, too few to trust", checked)
	}
}

// TestMoveIsDeterministic pins the reproducibility the strength measurement
// depends on: the choice is a function of the seed and the position, not of how
// many moves the bot has already made.
//
// The search is bounded by depth here and not by a clock, and that is the whole
// point. A search cut off by a wall-clock budget stops wherever the machine
// happened to get to, so its answer is a function of the load as well as of the
// seed: this test used to run with a forty-millisecond budget and it failed on a
// loaded runner, giving D2, D2 and D6 for one seed. That is not a defect in the
// bot — a truncated search is allowed to answer from what it had — but it is not
// a property a test can assert, and asserting it anyway made a real difference
// between machines look like a bug in the search.
//
// So determinism is pinned where the code guarantees it, with the clock taken out
// of the question, and the tiers' own budgets are exercised by the tests about
// deadlines and cancellation instead.
func TestMoveIsDeterministic(t *testing.T) {
	src := rand.New(rand.NewPCG(13, 14))
	ctx := context.Background()
	for _, tier := range testTiers {
		for round := range 6 {
			g := randomGame(t, smallRules(8), 10+src.IntN(20), src)
			if g.Result().Over() {
				continue
			}
			first := boundedEngine(tier, 99)
			a, err := first.Move(ctx, g)
			if err != nil {
				t.Fatalf("%v.Move: %v", tier, err)
			}
			// A second bot with the same seed, and the same bot asked twice,
			// must both agree.
			second := boundedEngine(tier, 99)
			b, err := second.Move(ctx, g)
			if err != nil {
				t.Fatalf("%v.Move: %v", tier, err)
			}
			c, err := first.Move(ctx, g)
			if err != nil {
				t.Fatalf("%v.Move: %v", tier, err)
			}
			if a != b || a != c {
				t.Fatalf("%v round %d: same seed gave %v, %v and %v", tier, round, a, b, c)
			}
		}
	}
}

// TestSeedChangesBeginnerChoice checks the seed is actually wired in: the
// beginner tier samples among near-best moves, so different seeds must be able
// to disagree.
func TestSeedChangesBeginnerChoice(t *testing.T) {
	g := randomGame(t, smallRules(10), 8, rand.New(rand.NewPCG(15, 16)))
	ctx := context.Background()
	seen := map[game.Point]bool{}
	for seed := range int64(24) {
		b := fastEngine(Beginner, seed, 30*time.Millisecond)
		p, err := b.Move(ctx, g)
		if err != nil {
			t.Fatalf("Move: %v", err)
		}
		seen[p] = true
	}
	if len(seen) < 2 {
		t.Fatalf("24 seeds all produced %v; the seed is not reaching the choice", seen)
	}
}

func TestMoveHonoursDeadline(t *testing.T) {
	g := randomGame(t, smallRules(24), 30, rand.New(rand.NewPCG(17, 18)))
	for _, tier := range testTiers {
		// The full tier budget, so that only the context can stop the search.
		b := New(tier, 1).(*engine)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		p, err := b.Move(ctx, g)
		took := time.Since(start)
		cancel()
		if err != nil {
			t.Fatalf("%v.Move: %v", tier, err)
		}
		if err := g.CanPlace(g.Turn(), p); err != nil {
			t.Fatalf("%v returned illegal move %v under a deadline: %v", tier, p, err)
		}
		if took > 250*time.Millisecond {
			t.Errorf("%v took %v for a 50ms deadline", tier, took)
		}
	}
}

func TestMoveHonoursCancellation(t *testing.T) {
	g := randomGame(t, smallRules(24), 30, rand.New(rand.NewPCG(19, 20)))
	for _, tier := range testTiers {
		b := New(tier, 1).(*engine)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		p, err := b.Move(ctx, g)
		took := time.Since(start)
		if err != nil {
			t.Fatalf("%v.Move on a cancelled context: %v", tier, err)
		}
		if err := g.CanPlace(g.Turn(), p); err != nil {
			t.Fatalf("%v returned illegal move %v on a cancelled context: %v", tier, p, err)
		}
		if took > 100*time.Millisecond {
			t.Errorf("%v took %v on an already cancelled context", tier, took)
		}
	}
}

func TestMoveRefusesFinishedGame(t *testing.T) {
	g := game.MustNew(smallRules(10))
	if err := g.Resign(game.Vertical); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	b := New(Pro, 1)
	if _, err := b.Move(context.Background(), g); err == nil {
		t.Error("Move accepted a finished game")
	}
	if _, err := b.Hint(context.Background(), g); err == nil {
		t.Error("Hint accepted a finished game")
	}
}

// playMoves plays a list of holes in notation, alternating sides as the engine
// dictates.
func playMoves(t testing.TB, g *game.Game, moves ...string) {
	t.Helper()
	for _, m := range moves {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("PlayNotation(%q): %v\n%s", m, err, g)
		}
	}
}

// winThreat builds a position where Vertical is one peg from joining the top
// and bottom rows of an 8x8 board, with Vertical to move.
//
// Vertical's chain runs B1-C3-B5-C7-E8, each step a knight's move. C3 is left
// out, and C3 is the only hole that joins B1 to the rest, so C3 wins at once
// and taking C3 away is the only way to stop it.
func winThreat(t testing.TB) *game.Game {
	t.Helper()
	g := game.MustNew(smallRules(8))
	// Horizontal answers in column G, where its pegs are two rows apart and so
	// never link to each other and never block anything.
	playMoves(t, g,
		"B1", "G2",
		"B5", "G3",
		"C7", "G4",
		"E8", "G5",
	)
	return g
}

func TestProTakesImmediateWin(t *testing.T) {
	g := winThreat(t)
	if g.Turn() != game.Vertical {
		t.Fatalf("expected Vertical to move, got %s", g.Turn())
	}
	var a analysis
	a.load(g)
	if got := a.need[sideIndex(game.Vertical)]; got != 1 {
		t.Fatalf("fixture is not a one-move win: Vertical needs %d pegs\n%s", got, g)
	}
	for _, tier := range testTiers {
		b := fastEngine(tier, 5, 200*time.Millisecond)
		p, err := b.Move(context.Background(), g)
		if err != nil {
			t.Fatalf("%v.Move: %v", tier, err)
		}
		probe := g.Clone()
		res, err := probe.PlayPeg(p)
		if err != nil {
			t.Fatalf("%v returned unplayable %v: %v", tier, p, err)
		}
		if res.Winner() != game.Vertical {
			t.Errorf("%v played %v, which does not win; expected the winning hole\n%s", tier, p, g)
		}
	}
}

func TestProBlocksImmediateWin(t *testing.T) {
	g := winThreat(t)
	// Hand the move to Horizontal by having Vertical play somewhere harmless
	// far from its own threat, leaving the threat standing.
	playMoves(t, g, "F1")
	if g.Turn() != game.Horizontal {
		t.Fatalf("expected Horizontal to move, got %s", g.Turn())
	}
	var a analysis
	a.load(g)
	if got := a.need[sideIndex(game.Vertical)]; got != 1 {
		t.Fatalf("fixture no longer threatens: Vertical needs %d pegs\n%s", got, g)
	}

	for _, tier := range testTiers {
		b := fastEngine(tier, 7, 300*time.Millisecond)
		p, err := b.Move(context.Background(), g)
		if err != nil {
			t.Fatalf("%v.Move: %v", tier, err)
		}
		probe := g.Clone()
		if _, err := probe.PlayPeg(p); err != nil {
			t.Fatalf("%v returned unplayable %v: %v", tier, p, err)
		}
		var after analysis
		after.load(probe)
		if got := after.need[sideIndex(game.Vertical)]; got < 2 {
			t.Errorf("%v played %v and left Vertical needing %d peg; the threat was not answered\n%s",
				tier, p, got, probe)
		}
	}
}

// TestDefencesAreExact checks the narrowing that makes the block above cheap:
// the generated defence list must contain every move that answers the threat
// and no move that does not, compared against playing every legal hole.
func TestDefencesAreExact(t *testing.T) {
	src := rand.New(rand.NewPCG(21, 22))
	s := newSearcher(tierParams(Pro))
	found := 0
	for range 900 {
		g := randomGame(t, smallRules(8), 45, src)
		if g.Result().Over() {
			continue
		}
		me := g.Turn()
		theirs := sideIndex(me.Opponent())
		s.prepare(g.Size())
		st := s.at(0)
		st.an.load(g)
		if st.an.need[theirs] != 1 || st.an.need[sideIndex(me)] == 1 {
			continue
		}
		found++

		want := map[game.Point]bool{}
		for _, p := range g.LegalPlacements(me) {
			probe := g.Clone()
			res, err := probe.PlayPeg(p)
			if err != nil {
				t.Fatalf("PlayPeg: %v", err)
			}
			if res.Over() {
				want[p] = true
				continue
			}
			var after analysis
			after.load(probe)
			if after.need[theirs] != 1 {
				want[p] = true
			}
		}

		got := map[game.Point]bool{}
		for _, m := range s.defences(g, &st.an, me, 0) {
			got[m.at] = true
		}
		for p := range want {
			if !got[p] {
				t.Fatalf("defence %v was missed\n%s", p, g)
			}
		}
		for p := range got {
			if !want[p] {
				t.Fatalf("defence list contains %v, which does not answer the threat\n%s", p, g)
			}
		}
	}
	if found < 15 {
		t.Fatalf("only %d threatened positions were exercised, too few to trust", found)
	}
}

// TestSetupShapes pins the gap shapes the hint may name. They are computed from
// the link geometry rather than transcribed, so this test states what that
// computation must produce. Up to reflection and axis swap there are five
// shapes, which is what the literature says a TwixT player learns.
func TestSetupShapes(t *testing.T) {
	got := map[string]int{}
	for _, off := range setupOffsets {
		got[fmt.Sprintf("%d-%d", abs(off[0]), abs(off[1]))]++
	}
	want := map[string]int{
		"1-1": 4, "0-2": 2, "2-0": 2, "0-4": 2, "4-0": 2,
		"1-3": 4, "3-1": 4, "3-3": 4,
	}
	for name, count := range want {
		if got[name] != count {
			t.Errorf("gap shape %s appears %d times, want %d (all shapes: %v)", name, got[name], count, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("gap shapes = %v, want exactly the keys of %v", got, want)
	}
	// Every named shape must really have two or more shared knight neighbours,
	// which is what makes it a setup rather than a single route.
	for _, off := range setupOffsets {
		shared := 0
		for _, a := range dirDelta {
			for _, b := range dirDelta {
				if a[0] == off[0]+b[0] && a[1] == off[1]+b[1] {
					shared++
				}
			}
		}
		if shared < 2 {
			t.Errorf("offset %v is listed as a setup but shares %d carriers", off, shared)
		}
	}
}
