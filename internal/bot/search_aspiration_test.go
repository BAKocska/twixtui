package bot

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// The root's aspiration band is a lever on how much work the root's first move
// costs, and every way it can go wrong produces a search that still returns
// promptly and still plays a legal move.
//
// A band that is never widened reports the edge of its own window as the
// move's value, and that value becomes the alpha every later root move is
// judged against: the root would then rank its whole candidate list against a
// number that came from the window rather than from the position. A band that
// is centred where it should not be -- on a forced win or loss, or before any
// iteration has finished -- pays for a re-search on every iteration and buys
// nothing. And a band that is wired in but never engaged is a lever that does
// not move, which no comparison of answers would ever notice.
//
// Those are the three things pinned here: the value that comes back is one its
// own window contained, the band stands down where it cannot help, and turning
// it on changes the work while leaving the answer alone.

// aspirationParams is the deterministic full-width search these tests measure
// against: no table, no extensions and no principal variation, so the value
// alpha-beta returns is plain minimax over the same move set and any
// disagreement is a disagreement about value rather than about which moves
// were looked at.
func aspirationParams() params {
	p := effortFullWidth()
	p.aspiration = true
	return p
}

// aspirationTierParams is the shipped pro lever set at a horizon a test can
// afford, with the transposition table off. It is derived from the tier on
// purpose, unlike the other effort helpers: the risk this variant exists to
// measure is that the width cap and the history heuristic together let a
// window change reach the answer, and that risk lives at the widths the bot
// actually plays with. Retuning the tier should move this test.
//
// The table is the one lever that has to come off, and it is worth being
// precise about why, because it is the boundary of what this file can claim.
// An entry is reused by any node at the same extension budget and at most the
// stored depth, so a search that walks a different tree stores a different set
// of entries and later nodes are answered from a different, deeper horizon.
// Two searches that differ anywhere then differ everywhere, and the difference
// is not a disagreement about a value: both answers are exact for the tree
// each one actually walked. That is a pre-existing property of the table and
// not of this band -- it is why the whole window and the band disagree on the
// reported score of about one position in twenty at these widths with the
// table on, and it is measured in docs/MANUAL.md rather than asserted here.
func aspirationTierParams() params {
	p := tierParams(Pro)
	p.budget = time.Minute
	p.maxDepth = 4
	p.useTable = false
	p.aspiration = true
	return p
}

// aspirationFirstMove is the root move the root would search first: the head of
// the ordinary candidate list, before any search has taught the ordering
// anything. An empty point means the position is one the root answers without
// searching, which these tests skip.
func aspirationFirstMove(t *testing.T, p params, g *game.Game) (game.Point, bool) {
	t.Helper()
	s := effortSearcher(t, p, g.Size())
	me := g.Turn()
	an := &s.at(0).an
	s.analyse(an, g)
	if an.need[sideIndex(me)] == 1 || an.need[sideIndex(me.Opponent())] == 1 {
		return game.Point{}, false
	}
	moves := s.candidates(g, an, me, 0)
	if len(moves) == 0 {
		return game.Point{}, false
	}
	return moves[0].at, true
}

// aspirationMeasure plays one root move and scores it from ply one the way the
// root does, either through the band or with a single window, on a searcher
// that has seen nothing else.
func aspirationMeasure(t *testing.T, p params, g *game.Game, at game.Point, depth, lo, hi int, band bool) int {
	t.Helper()
	s := effortSearcher(t, p, g.Size())
	if _, err := g.PlayPeg(at); err != nil {
		t.Fatalf("PlayPeg(%v): %v", at, err)
	}
	var v int
	if band {
		v = s.aspirate(g, depth, lo, hi)
	} else {
		v = -s.search(g, depth-1, 1, -hi, -lo, p.extend)
	}
	if err := g.UndoLastMove(); err != nil {
		t.Fatalf("UndoLastMove: %v", err)
	}
	return v
}

// TestAspirationReportsAValueAndNeverABound drives the band from centres that
// are deliberately wrong, which is the only way to see the re-search happen on
// demand: a centre far below the move's value forces a fail high and one far
// above forces a fail low. Both have to come back with the value the whole
// window gives rather than with the edge of the window that failed. That the
// whole window gives the minimax value is pinned elsewhere; what is on trial
// here is the band, so the whole window is what it is held against.
//
// The single-window measurement alongside it is the control. If a displaced
// band could return the right value without widening, this test would pass on
// an implementation that never re-searched at all, so the corpus is required
// to contain positions where one window on its own gives the wrong answer.
func TestAspirationReportsAValueAndNeverABound(t *testing.T) {
	const depth = 3
	src := rand.New(rand.NewPCG(271, 272))
	p := aspirationParams()
	rounds, floor := 40, 20
	if testing.Short() {
		rounds, floor = 14, 7
	}
	checked, forced := 0, 0
	for round := range rounds {
		g := randomGame(t, tournamentRules(8), 4+src.IntN(24), src)
		if g.Result().Over() {
			continue
		}
		at, ok := aspirationFirstMove(t, p, g)
		if !ok {
			continue
		}
		before := effortFingerprint(g)
		want := aspirationMeasure(t, p, g, at, depth, -infScore, infScore, false)
		if want >= decidedScore || want <= -decidedScore {
			// A decided move is what the band stands down for, so it says
			// nothing about the widening this test is about.
			continue
		}

		for _, c := range []struct {
			name   string
			centre int
			fails  bool
		}{
			{"a band the value sits above", want - 6*aspirationDelta, true},
			{"a band around the value", want, false},
			{"a band the value sits below", want + 6*aspirationDelta, true},
		} {
			lo, hi := c.centre-aspirationDelta, c.centre+aspirationDelta
			got := aspirationMeasure(t, p, g, at, depth, lo, hi, true)
			if got != want {
				t.Fatalf("round %d, %s (%d,%d): the band reported %d for a move worth %d\n%s",
					round, c.name, lo, hi, got, want, g)
			}
			if c.fails {
				if bound := aspirationMeasure(t, p, g, at, depth, lo, hi, false); bound != want {
					forced++
				}
			}
		}
		if after := effortFingerprint(g); after != before {
			t.Fatalf("round %d: measuring the band did not restore the position", round)
		}
		checked++
	}
	if checked < floor {
		t.Fatalf("only %d positions measured, too few to trust", checked)
	}
	if forced == 0 {
		t.Fatalf("no displaced band in %d positions returned a wrong answer without widening: "+
			"the widening this test is about was never exercised", checked)
	}
}

// TestAspirationStandsDownWhereItCannotHelp pins the cases the band refuses,
// each of which costs work and buys nothing.
//
// The forced-win and forced-loss cases are the ones with teeth. A decided
// score sits at the far end of the scale, so a band around it lies almost
// entirely outside the range every ordinary reply scores in: it would fail on
// the first move of every iteration that follows and pay for a widening each
// time. The root keeps deepening after it sees a forced loss -- it is still
// looking for something to play -- so this is not a case that never happens.
func TestAspirationStandsDownWhereItCannotHelp(t *testing.T) {
	ordinary := 3 * distWeight
	for _, c := range []struct {
		name  string
		mut   func(*params)
		depth int
		prev  int
		have  bool
		want  bool
	}{
		{"the lever is off", func(p *params) { p.aspiration = false }, 3, ordinary, true, false},
		{"no iteration has finished yet", nil, 3, ordinary, false, false},
		{"the first iteration", nil, 1, ordinary, true, false},
		{"sampling, which reads every root value", func(p *params) { p.temperature = 1.5 }, 3, ordinary, true, false},
		{"a previous score that is a forced win", nil, 3, decidedScore, true, false},
		{"a previous score that is a forced loss", nil, 3, -decidedScore, true, false},
		{"an ordinary previous score", nil, 3, ordinary, true, true},
		{"an ordinary previous loss", nil, 5, -ordinary, true, true},
	} {
		p := aspirationParams()
		if c.mut != nil {
			c.mut(&p)
		}
		lo, hi, ok := newSearcher(p).aspirationWindow(c.depth, c.prev, c.have)
		if ok != c.want {
			t.Errorf("%s: narrowed=%v, want %v", c.name, ok, c.want)
			continue
		}
		wantLo, wantHi := -infScore, infScore
		if c.want {
			wantLo, wantHi = c.prev-aspirationDelta, c.prev+aspirationDelta
		}
		if lo != wantLo || hi != wantHi {
			t.Errorf("%s: window (%d,%d), want (%d,%d)", c.name, lo, hi, wantLo, wantHi)
		}
	}
}

// TestAspirationChangesTheWorkAndNotTheAnswer is the pair of claims that make
// the band a lever on work rather than on play: the root reaches the same
// depth, plays the same move and reports the same score with the band on and
// off, and it does not reach them by doing the same work.
//
// The second half is the control. A band that was added to params, documented
// and never consulted would satisfy every agreement check in this file, and
// the node count is the only thing that can tell that apart from a band that
// is doing its job. It says nothing about the direction of the difference:
// what the band costs is a measurement and lives in docs/MANUAL.md, not here.
//
// The regimes are the two the band is value-neutral in, and they are not the
// whole story. Full width removes the width cap, so the move set is the
// position's own and an agreement is an agreement about values. The second
// keeps the shipped pro widths, where the ordering decides which moves survive
// the cap and the ordering learns from cutoffs that a window change moves:
// that is the history route, exercised rather than assumed. Both run with the
// transposition table off, and the route that leaves out is a real one: with
// the table on at these widths the band and the whole window do disagree, on
// two of the forty positions the effort harness measures at depth six and on
// one of a hundred and forty-seven random ones at depth four. That is recorded
// in docs/MANUAL.md as the measurement it is, and it is half of why the lever
// ships off. It is not asserted here because an assertion that fails is not a
// test, and because pinning today's disagreement would pin the accident rather
// than the property.
func TestAspirationChangesTheWorkAndNotTheAnswer(t *testing.T) {
	rounds, floor := 40, 20
	if testing.Short() {
		rounds, floor = 14, 7
	}
	for _, regime := range []struct {
		name string
		p    params
	}{
		{"full width", aspirationParams()},
		{"pro widths, no table", aspirationTierParams()},
	} {
		// One corpus per regime, so a regime that disagrees names a position
		// the other was asked about too.
		src := rand.New(rand.NewPCG(281, 282))
		checked, moved := 0, 0
		for round := range rounds {
			g := randomGame(t, tournamentRules(8), 6+src.IntN(22), src)
			if g.Result().Over() {
				continue
			}
			off := regime.p
			off.aspiration = false

			with := newSearcher(regime.p)
			gotWith, err := with.root(context.Background(), g)
			if err != nil {
				t.Fatalf("%s round %d: root with the band: %v", regime.name, round, err)
			}
			// The searcher owns the list it hands back, so it has to be copied
			// out before the second search is run on the same board.
			withMoves := effortCopyMoves(gotWith.moves)
			without := newSearcher(off)
			gotWithout, err := without.root(context.Background(), g)
			if err != nil {
				t.Fatalf("%s round %d: root without the band: %v", regime.name, round, err)
			}

			switch {
			case gotWith.depth != gotWithout.depth:
				t.Fatalf("%s round %d: the band finished depth %d, the whole window finished %d\n%s",
					regime.name, round, gotWith.depth, gotWithout.depth, g)
			case gotWith.score != gotWithout.score:
				t.Fatalf("%s round %d: the band reported %d at depth %d, the whole window reported %d\n%s",
					regime.name, round, gotWith.score, gotWith.depth, gotWithout.score, g)
			case gotWith.best != gotWithout.best:
				t.Fatalf("%s round %d: the band played %v, the whole window played %v, both scoring %d\n%s",
					regime.name, round, gotWith.best, gotWithout.best, gotWith.score, g)
			case len(withMoves) != len(gotWithout.moves):
				t.Fatalf("%s round %d: the band left %d root moves, the whole window left %d",
					regime.name, round, len(withMoves), len(gotWithout.moves))
			}
			// Only the head of the list is compared, and deliberately so. The
			// moves after the best one carry upper bounds rather than values:
			// they were asked whether they beat alpha and said no, and how far
			// below alpha a fail-soft answer lands depends on the order the
			// children happened to be tried in. Two searches that prune
			// differently learn different history, try children in a different
			// order and so report different bounds for the same move, without
			// either of them being wrong about anything. Requiring those to
			// agree would be requiring two searches to walk the same tree,
			// which is the one thing this lever exists not to do.
			if with.nodes != without.nodes {
				moved++
			}
			checked++
		}
		if checked < floor {
			t.Fatalf("%s: only %d positions compared, too few to trust", regime.name, checked)
		}
		if moved == 0 {
			t.Fatalf("%s: the band entered exactly the same nodes as the whole window on all %d positions, "+
				"so nothing here would notice a lever that is never consulted", regime.name, checked)
		}
	}
}
