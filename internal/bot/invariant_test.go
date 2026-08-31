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
func TestSearchMatchesMinimax(t *testing.T) {
	src := rand.New(rand.NewPCG(51, 52))
	// Full-width minimax at three plies is the expensive part of the package's
	// tests, so the quick run takes a smaller sample of the same property.
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
	src := rand.New(rand.NewPCG(61, 62))
	compared, decided := 0, 0
	for _, size := range []int{10, 24} {
		for _, plies := range []int{4, 12, 20} {
			g := randomGame(t, tournamentRules(size), plies, src)
			if g.Result().Over() {
				continue
			}
			depths := map[Tier]int{}
			settled := false
			for _, tier := range testTiers {
				s := newSearcher(func() params {
					p := tierParams(tier)
					p.budget = quickBudgets[tier]
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
			t.Logf("%dx%d ply=%2d depths: beginner=%d intermediate=%d pro=%d decided=%v",
				size, size, plies, depths[Beginner], depths[Intermediate], depths[Pro], settled)
			if settled {
				decided++
				continue
			}
			if depths[Beginner] != tierParams(Beginner).maxDepth {
				t.Errorf("%dx%d ply=%d: beginner reached depth %d, want its ceiling %d",
					size, size, plies, depths[Beginner], tierParams(Beginner).maxDepth)
			}
			if depths[Intermediate] != tierParams(Intermediate).maxDepth {
				t.Errorf("%dx%d ply=%d: intermediate reached depth %d, want its ceiling %d",
					size, size, plies, depths[Intermediate], tierParams(Intermediate).maxDepth)
			}
			if depths[Pro] <= depths[Intermediate] {
				t.Errorf("%dx%d ply=%d: pro reached depth %d, no deeper than intermediate's %d",
					size, size, plies, depths[Pro], depths[Intermediate])
			}
			compared++
		}
	}
	if compared < 3 {
		t.Fatalf("only %d undecided positions were compared (%d were already decided)", compared, decided)
	}
}
