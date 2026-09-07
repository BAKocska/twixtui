package bot

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// allTiers is every tier there is, so that a property asserted once is
// asserted for all of them.
var allTiers = []Tier{Beginner, Intermediate, Pro, Max}

// testTiers is the beginner-to-pro ladder, which is what the tournament and
// depth-ceiling measurements in strength_test.go and invariant_test.go are
// calibrated against: their per-tier budget table is a [3]time.Duration indexed
// by Tier, so Max cannot be added here without widening that table. Properties
// that must hold of every tier use allTiers.
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
	if len(names) != int(Max)+1 {
		t.Fatalf("TierNames = %v, want one entry per tier", names)
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
		for _, tier := range allTiers {
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
	for _, tier := range allTiers {
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
	for _, tier := range allTiers {
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
	for _, tier := range allTiers {
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

// mustPoint reads a hole from the notation a fixture is written in, so a test
// names the same hole the moves above it named.
func mustPoint(t testing.TB, s string) game.Point {
	t.Helper()
	p, err := game.ParsePoint(s)
	if err != nil {
		t.Fatalf("ParsePoint(%q): %v", s, err)
	}
	return p
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
	for _, tier := range allTiers {
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

	for _, tier := range allTiers {
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
	s.ctx = context.Background()
	s.deadline = time.Now().Add(time.Minute)
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

// --- effort limits and what a search spent -----------------------------------

// TestNewWithLimitsRefusesWhatCannotBeSearched covers the validation a
// measurement rests on. A limit that cannot be honoured has to come back as an
// error: searching under the tier's own guard instead would have a benchmark
// report work nobody bounded, and a tier that does not exist would quietly be
// measured as the beginner.
func TestNewWithLimitsRefusesWhatCannotBeSearched(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tier   Tier
		limits Limits
	}{
		{"negative nodes", Pro, Limits{Nodes: -1}},
		{"negative depth", Pro, Limits{Depth: -1}},
		{"negative time", Pro, Limits{Time: -time.Second}},
		{"deeper than the search can hold", Pro, Limits{Depth: MaxDepth + 1}},
		{"tier below the ladder", Tier(-1), Limits{}},
		{"tier above the ladder", Max + 1, Limits{}},
	} {
		b, err := NewWithLimits(tc.tier, 1, tc.limits)
		if err == nil {
			t.Errorf("%s: NewWithLimits(%v, %+v) was accepted", tc.name, tc.tier, tc.limits)
		}
		if b != nil {
			t.Errorf("%s: NewWithLimits returned a bot alongside its error", tc.name)
		}
	}
	// The boundary is inside the contract: MaxDepth is a search the recursion
	// can hold, and every tier accepts limits that change nothing.
	if _, err := NewWithLimits(Pro, 1, Limits{Depth: MaxDepth}); err != nil {
		t.Errorf("NewWithLimits refused the deepest search it can hold: %v", err)
	}
	for _, tier := range allTiers {
		if _, err := NewWithLimits(tier, 1, Limits{}); err != nil {
			t.Errorf("NewWithLimits(%v) refused empty limits: %v", tier, err)
		}
	}
}

// TestEmptyLimitsPlayTheTierUnchanged checks that a zero field inherits the
// tier's own guard rather than becoming a guard of zero. The two depth-capped
// tiers answer the same way every time from the same seed and position, so
// their move is the observable: a bot given a zero budget or a zero width would
// answer from the ordering heuristic instead of from a search.
func TestEmptyLimitsPlayTheTierUnchanged(t *testing.T) {
	g := randomGame(t, smallRules(10), 12, rand.New(rand.NewPCG(41, 42)))
	if g.Result().Over() {
		t.Fatal("fixture finished before anybody had to move")
	}
	ctx := context.Background()
	for _, tier := range []Tier{Beginner, Intermediate} {
		want, err := New(tier, 7).Move(ctx, g)
		if err != nil {
			t.Fatalf("%v.Move: %v", tier, err)
		}
		limited, err := NewWithLimits(tier, 7, Limits{})
		if err != nil {
			t.Fatalf("NewWithLimits(%v): %v", tier, err)
		}
		got, err := limited.Move(ctx, g)
		if err != nil {
			t.Fatalf("%v.Move under empty limits: %v", tier, err)
		}
		if got != want {
			t.Errorf("%v played %v under empty limits and %v without them", tier, got, want)
		}
	}
}

// TestNodeLimitBoundsTheWork is the guard a reproducible measurement rests on.
// With the clock lifted far out of the way the node ceiling is what ends the
// search, it is never exceeded, the same ceiling twice gives the same move, and
// a higher ceiling buys more work — without which the ceiling would be a number
// the search accepts and ignores.
func TestNodeLimitBoundsTheWork(t *testing.T) {
	g := randomGame(t, smallRules(24), 30, rand.New(rand.NewPCG(43, 44)))
	if g.Result().Over() {
		t.Fatal("fixture finished before anybody had to move")
	}
	var a analysis
	a.load(g)
	if a.need[sideIndex(game.Vertical)] <= 1 || a.need[sideIndex(game.Horizontal)] <= 1 {
		t.Fatalf("fixture is a move from being decided, so no ceiling would ever bind\n%s", g)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spent := map[int64]int64{}
	for _, limit := range []int64{4_000, 40_000} {
		b, err := NewWithLimits(Max, 5, Limits{Nodes: limit, Time: time.Hour})
		if err != nil {
			t.Fatalf("NewWithLimits: %v", err)
		}
		p, err := b.Move(ctx, g)
		if err != nil {
			t.Fatalf("Move under a %d-node ceiling: %v", limit, err)
		}
		if err := g.CanPlace(g.Turn(), p); err != nil {
			t.Fatalf("%v under a %d-node ceiling is illegal: %v", p, limit, err)
		}
		st := StatsOf(b)
		if st.Nodes > limit {
			t.Errorf("a %d-node ceiling visited %d nodes", limit, st.Nodes)
		}
		if st.StopReason != "nodes" {
			t.Errorf("a %d-node ceiling stopped for %q after %d nodes at depth %d in %v; the work bound was not what ended the search",
				limit, st.StopReason, st.Nodes, st.Depth, st.Elapsed)
		}
		again, err := NewWithLimits(Max, 5, Limits{Nodes: limit, Time: time.Hour})
		if err != nil {
			t.Fatalf("NewWithLimits: %v", err)
		}
		q, err := again.Move(ctx, g)
		if err != nil {
			t.Fatalf("Move under a %d-node ceiling, second bot: %v", limit, err)
		}
		if q != p {
			t.Errorf("a %d-node ceiling gave %v and then %v; work-bounded search is not reproducible", limit, p, q)
		}
		spent[limit] = st.Nodes
	}
	if spent[40_000] <= spent[4_000] {
		t.Errorf("raising the ceiling tenfold bought no more work: %d nodes then %d", spent[4_000], spent[40_000])
	}
}

// TestDepthLimitCapsTheSearch checks the other work bound. A max-tier bot given
// a two-ply ceiling and an hour must answer in the time two plies take, which
// is what says the ceiling replaced the tier's own rather than joining it.
func TestDepthLimitCapsTheSearch(t *testing.T) {
	g := randomGame(t, smallRules(16), 20, rand.New(rand.NewPCG(45, 46)))
	if g.Result().Over() {
		t.Fatal("fixture finished before anybody had to move")
	}
	b, err := NewWithLimits(Max, 3, Limits{Depth: 2, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	// A safety net, so that a depth ceiling that never took hold fails the test
	// instead of sitting here for the hour it was given.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	p, err := b.Move(ctx, g)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Move under a two-ply ceiling: %v", err)
	}
	if err := g.CanPlace(g.Turn(), p); err != nil {
		t.Fatalf("%v under a two-ply ceiling is illegal: %v", p, err)
	}
	st := StatsOf(b)
	if st.Depth > 2 {
		t.Errorf("a two-ply ceiling finished depth %d", st.Depth)
	}
	if st.Depth < 1 {
		t.Errorf("a two-ply ceiling finished no iteration at all: %+v", st)
	}
	if st.StopReason != "depth" && st.StopReason != "decided" {
		t.Errorf("a two-ply ceiling stopped for %q, want its own ceiling or a proven result", st.StopReason)
	}
	if took > 5*time.Second {
		t.Errorf("two plies of a 16x16 position took %v; the ten-second budget looks to still be in charge", took)
	}
}

// TestStatsOfReportsTheLastSearchOnly pins what a caller may conclude from
// StatsOf: it describes the search that has just happened, it costs nothing to
// read, reading it twice says the same thing, and a second move replaces the
// count instead of adding to it — a running total would drift past the ceiling
// it is supposed to be measuring.
func TestStatsOfReportsTheLastSearchOnly(t *testing.T) {
	const ceiling = 5_000
	b, err := NewWithLimits(Pro, 9, Limits{Nodes: ceiling, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	if got := StatsOf(b); got != (SearchStats{}) {
		t.Errorf("a bot that has not searched reports %+v, want the zero value", got)
	}
	g := randomGame(t, smallRules(16), 16, rand.New(rand.NewPCG(47, 48)))
	if g.Result().Over() {
		t.Fatal("fixture finished before anybody had to move")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := b.Move(ctx, g); err != nil {
		t.Fatalf("Move: %v", err)
	}
	first := StatsOf(b)
	if first.Nodes <= 0 {
		t.Errorf("the search reports %d nodes: %+v", first.Nodes, first)
	}
	if first.Evaluations <= 0 {
		t.Errorf("the search reports %d evaluations: %+v", first.Evaluations, first)
	}
	if first.Depth < 1 {
		t.Errorf("the search reports no finished iteration: %+v", first)
	}
	if first.Elapsed <= 0 {
		t.Errorf("the search reports no elapsed time: %+v", first)
	}
	if first.StopReason == "" {
		t.Errorf("the search reports no reason for stopping: %+v", first)
	}
	if second := StatsOf(b); second != first {
		t.Errorf("reading the count twice gave %+v then %+v", first, second)
	}
	if _, err := b.Move(ctx, g); err != nil {
		t.Fatalf("second Move: %v", err)
	}
	if got := StatsOf(b); got.Nodes > ceiling {
		t.Errorf("after two searches the count is %d, beyond the %d-node ceiling one search may spend: %+v",
			got.Nodes, ceiling, got)
	}
}

// scriptedBot is a Bot with no search behind it, which is the case StatsOf has
// to answer for: Bot is what the screens accept, and a stub opponent or a
// scripted one has nothing to report.
type scriptedBot struct{ at game.Point }

func (scriptedBot) Tier() Tier { return Beginner }

func (b scriptedBot) Move(context.Context, *game.Game) (game.Point, error) { return b.at, nil }

func (scriptedBot) Hint(context.Context, *game.Game) (Hint, error) { return Hint{}, nil }

func TestStatsOfIsZeroForABotThatKeepsNoCount(t *testing.T) {
	if got := StatsOf(scriptedBot{}); got != (SearchStats{}) {
		t.Errorf("a bot with no search reports %+v, want the zero value", got)
	}
}

// TestEngineRefusesAStagedTurn covers the precondition the search cannot honour
// any other way. The search plays and takes back trial moves on the game it is
// given, and taking a move back restores the position from before the turn, so
// a turn in progress would be searched as part of the position and then thrown
// away. Both entry points refuse, and the refusal leaves the turn as it was.
func TestEngineRefusesAStagedTurn(t *testing.T) {
	ctx := context.Background()

	t.Run("a peg placed but not committed", func(t *testing.T) {
		g := randomGame(t, smallRules(10), 8, rand.New(rand.NewPCG(49, 50)))
		if g.Result().Over() {
			t.Fatal("fixture finished before anybody had to move")
		}
		var spot game.Point
		found := false
		g.EachLegalPlacement(g.Turn(), func(p game.Point) bool {
			spot, found = p, true
			return false
		})
		if !found {
			t.Fatal("fixture has nowhere to play")
		}
		if err := g.PlacePeg(spot); err != nil {
			t.Fatalf("PlacePeg(%v): %v", spot, err)
		}
		ply := g.Ply()
		b := New(Pro, 1)
		if _, err := b.Move(ctx, g); !errors.Is(err, ErrStagedTurn) {
			t.Errorf("Move on a staged turn returned %v, want ErrStagedTurn", err)
		}
		if _, err := b.Hint(ctx, g); !errors.Is(err, ErrStagedTurn) {
			t.Errorf("Hint on a staged turn returned %v, want ErrStagedTurn", err)
		}
		if st := g.Staged(); !st.PegPlaced || st.Peg != spot {
			t.Errorf("the refusal did not leave the staged peg alone: %+v", st)
		}
		if got := g.Ply(); got != ply {
			t.Errorf("the refusal committed something: ply %d, want %d", got, ply)
		}
		// Committing the turn makes the position searchable again, which is
		// what the error asks the caller to do.
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("CommitTurn: %v", err)
		}
		if _, err := b.Move(ctx, g); err != nil {
			t.Errorf("Move on the committed position: %v", err)
		}
	})

	t.Run("a link withdrawn by hand", func(t *testing.T) {
		// Deliberate linking, so that a link can be taken off by itself: the
		// paper-and-pencil ruleset the other fixtures use links automatically
		// and permanently, and has no staged link to test.
		rs := game.Std
		rs.Size, rs.Swap = 10, false
		g := game.MustNew(rs)
		playMoves(t, g, "B1", "G2", "C3", "G4")
		from, to := mustPoint(t, "B1"), mustPoint(t, "C3")
		l, ok := game.NewLink(from, to)
		if !ok {
			t.Fatalf("%v and %v are not a knight's move apart", from, to)
		}
		if !g.HasLink(l) {
			t.Fatalf("fixture has no link to withdraw\n%s", g)
		}
		if err := g.RemoveLink(from, to); err != nil {
			t.Fatalf("RemoveLink: %v", err)
		}
		b := New(Intermediate, 1)
		if _, err := b.Move(ctx, g); !errors.Is(err, ErrStagedTurn) {
			t.Errorf("Move with a link withdrawn returned %v, want ErrStagedTurn", err)
		}
		if _, err := b.Hint(ctx, g); !errors.Is(err, ErrStagedTurn) {
			t.Errorf("Hint with a link withdrawn returned %v, want ErrStagedTurn", err)
		}
		if g.HasLink(l) {
			t.Error("the refusal put the withdrawn link back")
		}
		if st := g.Staged(); len(st.Removed) != 1 || st.Removed[0] != l {
			t.Errorf("the refusal did not leave the withdrawal alone: %+v", st)
		}
		// Aborting the turn restores the link and the position searches again.
		g.AbortTurn()
		if !g.HasLink(l) {
			t.Fatal("aborting the turn did not restore the link")
		}
		if _, err := b.Move(ctx, g); err != nil {
			t.Errorf("Move after the turn was aborted: %v", err)
		}
	})
}
