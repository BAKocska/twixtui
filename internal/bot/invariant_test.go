package bot

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// tunedEngine builds an engine with one tier's levers adjusted, which is how the
// tests ask what a single lever is worth.
func tunedEngine(t Tier, seed int64, mutate func(*params)) *engine {
	p := tierParams(t)
	mutate(&p)
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}
}

// reference is a plain minimax over the same move generation, with no pruning,
// no table and no extensions. Its value for a node is by definition the value
// the pruned search must return.
func (s *searcher) reference(g *game.Game, depth, ply int) int {
	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	st := s.at(ply)
	an := &st.an
	an.load(g)
	if an.need[mine] == 1 {
		return winScore - ply
	}
	forced := an.need[theirs] == 1
	if depth <= 0 {
		return s.leaf(an, me)
	}
	moves := s.generate(g, an, me, ply, forced)
	if len(moves) == 0 {
		if forced {
			return -(winScore - ply - 1)
		}
		return 0
	}
	// The recursion reuses this ply's buffer, so keep a copy.
	list := append([]scoredMove(nil), moves...)
	best := -infScore
	for _, mv := range list {
		res, err := g.PlayPeg(mv.at)
		if err != nil {
			continue
		}
		var v int
		if res.Over() {
			switch res.Winner() {
			case me:
				v = winScore - ply - 1
			case game.NoPlayer:
				v = 0
			default:
				v = -(winScore - ply - 1)
			}
		} else {
			v = -s.reference(g, depth-1, ply+1)
		}
		if err := g.UndoLastMove(); err != nil {
			panic(err)
		}
		if v > best {
			best = v
		}
	}
	return best
}

// TestSearchMatchesMinimax pins alpha-beta soundness, with and without the
// transposition table: pruning and hashing may change how long the search takes
// but never what it concludes.
//
// It is the second most expensive test in the repository and the sample is the
// reason, so the sample was measured rather than guessed before anyone trims
// it. Searching every child with a null window disagrees with minimax on 17 of
// the 38 positions this seed produces, which any handful of rounds would catch.
// Cutting one point too early -- alpha >= beta-1 rather than alpha >= beta --
// disagrees on exactly one, round 18, and on none at all in the table variant.
// That is what forty rounds buys: not redundancy against the obvious defect but
// the one position in the corpus that separates a cutoff off by a single point
// from a sound one. Twelve rounds would miss it, which is worth knowing about
// the short run: -short samples the property, it does not discharge it.
func TestSearchMatchesMinimax(t *testing.T) {
	src := rand.New(rand.NewPCG(51, 52))
	rounds, floor := 40, 20
	if testing.Short() {
		rounds, floor = 12, 6
	}
	for _, variant := range []struct {
		name string
		mut  func(*params)
	}{
		{"plain", func(p *params) { p.useTable = false; p.extend = 0 }},
		{"table", func(p *params) { p.useTable = true; p.extend = 0 }},
	} {
		checked := 0
		for round := range rounds {
			g := randomGame(t, tournamentRules(8), 4+src.IntN(24), src)
			if g.Result().Over() {
				continue
			}
			p := tierParams(Pro)
			p.budget = time.Minute
			// Full width, so that both searches see the same move set.
			p.width, p.rootWidth = 200, 200
			variant.mut(&p)

			ref := newSearcher(p)
			ref.ctx = context.Background()
			ref.deadline = time.Now().Add(time.Minute)
			ref.prepare(g.Size())
			want := ref.reference(g, 3, 0)

			got := newSearcher(p)
			got.ctx = context.Background()
			got.deadline = time.Now().Add(time.Minute)
			got.prepare(g.Size())
			have := got.search(g, 3, 0, -infScore, infScore, p.extend)

			if want != have {
				t.Fatalf("%s round %d: minimax says %d, alpha-beta says %d\n%s",
					variant.name, round, want, have, g)
			}
			checked++
		}
		if checked < floor {
			t.Fatalf("%s: only %d positions compared, too few to trust", variant.name, checked)
		}
	}
}

// TestDepthCeilingsSeparateTiers checks the mechanism the strength ladder rests
// on, rather than only its outcome: at the budgets the measurement uses, the
// weaker tiers reach exactly their ceiling and the pro tier gets strictly
// deeper. A tier that has already proven a win stops deepening, which is
// correct and is not counted against it.
func TestDepthCeilingsSeparateTiers(t *testing.T) {
	// The ceilings themselves must differ. This is a property of the tier
	// configuration and holds on any machine.
	if a, b := tierParams(Beginner).maxDepth, tierParams(Intermediate).maxDepth; a >= b {
		t.Fatalf("beginner's ceiling %d is not below intermediate's %d", a, b)
	}
	if a, b := tierParams(Intermediate).maxDepth, tierParams(Pro).maxDepth; a >= b {
		t.Fatalf("intermediate's ceiling %d is not below pro's %d", a, b)
	}

	src := rand.New(rand.NewPCG(61, 62))

	// The exact ceilings are checked on a small board with a budget generous
	// enough that reaching them does not depend on how fast the machine is.
	// Asserting an exact depth under a short wall-clock budget measures the
	// machine rather than the search, and fails on a slow continuous-integration
	// runner for no useful reason.
	//
	// Two seconds is generous rather than arbitrary. Beginner and intermediate
	// stop at their ceilings after under a millisecond and about eight
	// milliseconds respectively, so for them the budget is never the binding
	// constraint at any plausible speed. Pro is the only tier that spends its
	// whole budget, since its ceiling of sixteen is out of reach on this board,
	// and all it has to do here is finish one iteration beyond intermediate's
	// ceiling of three. Measured on these positions it completes depth four
	// inside a hundred milliseconds, so two seconds tolerates a machine twenty
	// times slower than the one this was measured on. The twenty seconds this
	// used to allow bought no further guarantee and cost forty seconds of the
	// package's runtime, which is the sort of price that stops people running
	// the suite at all.
	const generous = 2 * time.Second
	compared, decided := 0, 0
	for _, plies := range []int{4, 12, 20} {
		g := randomGame(t, tournamentRules(10), plies, src)
		if g.Result().Over() {
			continue
		}
		depths, settled := depthsReached(t, g, generous)
		t.Logf("10x10 ply=%2d depths: beginner=%d intermediate=%d pro=%d decided=%v",
			plies, depths[Beginner], depths[Intermediate], depths[Pro], settled)
		if settled {
			// A tier that has already proven a win stops deepening, which is
			// correct and is not counted against it.
			decided++
			continue
		}
		for _, tier := range []Tier{Beginner, Intermediate} {
			if want := tierParams(tier).maxDepth; depths[tier] != want {
				t.Errorf("10x10 ply=%d: %v reached depth %d, want its ceiling %d",
					plies, tier, depths[tier], want)
			}
		}
		if depths[Pro] <= depths[Intermediate] {
			// The budget is named in the failure because the one way this can
			// fail without a real regression is a machine too slow to finish
			// pro's fourth iteration inside it.
			t.Errorf("10x10 ply=%d: pro reached depth %d within %s, no deeper than intermediate's %d",
				plies, depths[Pro], generous, depths[Intermediate])
		}
		compared++
	}
	if compared < 2 {
		t.Fatalf("only %d undecided positions were compared (%d were already decided)", compared, decided)
	}

	// On the shipped board size the assertion is the ordering rather than the
	// exact depth, since a slow machine may legitimately be cut off part way
	// through an iteration. No tier may exceed its own ceiling, and a stronger
	// tier must never search shallower than a weaker one.
	for _, plies := range []int{4, 12, 20} {
		g := randomGame(t, tournamentRules(24), plies, src)
		if g.Result().Over() {
			continue
		}
		depths, settled := depthsReached(t, g, quickBudgets)
		t.Logf("24x24 ply=%2d depths: beginner=%d intermediate=%d pro=%d decided=%v",
			plies, depths[Beginner], depths[Intermediate], depths[Pro], settled)
		for _, tier := range testTiers {
			if got, ceiling := depths[tier], tierParams(tier).maxDepth; got > ceiling {
				t.Errorf("24x24 ply=%d: %v reached depth %d, beyond its ceiling %d",
					plies, tier, got, ceiling)
			}
		}
		if depths[Intermediate] < depths[Beginner] {
			t.Errorf("24x24 ply=%d: intermediate searched shallower than beginner (%d < %d)",
				plies, depths[Intermediate], depths[Beginner])
		}
		if depths[Pro] < depths[Intermediate] {
			t.Errorf("24x24 ply=%d: pro searched shallower than intermediate (%d < %d)",
				plies, depths[Pro], depths[Intermediate])
		}
	}
}

// depthsReached runs each tier's search once on a position and reports the depth
// it finished, together with whether any tier had already settled the game.
// budgets is either one duration applied to every tier, or the per-tier table.
func depthsReached(t *testing.T, g *game.Game, budgets any) (map[Tier]int, bool) {
	t.Helper()
	depths := map[Tier]int{}
	settled := false
	for _, tier := range testTiers {
		s := newSearcher(func() params {
			p := tierParams(tier)
			switch b := budgets.(type) {
			case time.Duration:
				p.budget = b
			case [3]time.Duration:
				p.budget = b[tier]
			}
			return p
		}())
		out, err := s.root(context.Background(), g)
		if err != nil {
			t.Fatalf("%v root: %v", tier, err)
		}
		depths[tier] = out.depth
		if out.immediate || out.score >= decidedScore || out.score <= -decidedScore {
			settled = true
		}
	}
	return depths, settled
}
