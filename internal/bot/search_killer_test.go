package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// The killer heuristic is an ordering change and nothing else: each ply
// remembers the last two holes that produced a beta cutoff there and tries
// them first at the next node it reaches at that ply. Every way it can go
// wrong is quiet.
//
// It can remember a hole and never use it, which costs the bookkeeping and
// buys nothing. It can use a hole at the wrong ply, where it answers a
// different position and is noise. It can carry a hole from the previous
// search, which makes the bot's move depend on which position it was asked
// about before. It can try to play a hole the node never generated -- one that
// is occupied, or that the width cap dropped, or that a tactical defence list
// excludes -- which would either widen the search behind the width cap's back
// or crash on an illegal move. And it can change the value the search returns,
// which no ordering may do at a fixed move set.
//
// None of those show up as an error or an illegal move in ordinary play, so
// each is pinned here.

// killerFixture is one frozen position: a board size and the seeded random
// game that reaches it. The seeds are fixed literals, so every run of these
// tests measures the same positions and the node counts below are comparable
// between runs.
type killerFixture struct {
	size  int
	plies int
	a, b  uint64
}

// killerFixtures are six midgames per board size pair: the first six positions
// of a seed sweep that were neither finished nor already decided, taken in
// order rather than picked for their result. A position where one side is a
// peg from a chain is no use here -- the search answers it before it generates
// a move, so it would measure nothing and quietly dilute the totals below.
func killerFixtures() []killerFixture {
	return []killerFixture{
		{size: 8, plies: 20, a: 400, b: 401},
		{size: 8, plies: 20, a: 401, b: 402},
		{size: 8, plies: 20, a: 402, b: 403},
		{size: 8, plies: 20, a: 407, b: 408},
		{size: 8, plies: 20, a: 409, b: 410},
		{size: 8, plies: 20, a: 411, b: 412},
		{size: 10, plies: 30, a: 400, b: 401},
		{size: 10, plies: 30, a: 401, b: 402},
		{size: 10, plies: 30, a: 402, b: 403},
		{size: 10, plies: 30, a: 403, b: 404},
		{size: 10, plies: 30, a: 404, b: 405},
		{size: 10, plies: 30, a: 405, b: 406},
	}
}

// game replays the fixture and checks it is still worth searching. Both
// guards are assertions rather than skips: the seeds are frozen, so a fixture
// that has become terminal or tactical is a broken fixture and not a position
// to pass over.
func (f killerFixture) game(t *testing.T) *game.Game {
	t.Helper()
	g := randomGame(t, tournamentRules(f.size), f.plies, rand.New(rand.NewPCG(f.a, f.b)))
	if g.Result().Over() {
		t.Fatalf("fixture %s is already finished: it measures nothing", f)
	}
	me := g.Turn()
	s := effortSearcher(t, effortFullWidth(), f.size)
	st := s.at(0)
	s.analyse(&st.an, g)
	if st.an.need[sideIndex(me)] <= 1 || st.an.need[sideIndex(me.Opponent())] <= 1 {
		t.Fatalf("fixture %s is a peg from a finished chain: the search answers it before it orders a move", f)
	}
	return g
}

func (f killerFixture) String() string {
	return fmt.Sprintf("%dx%d/%d plies/seed %d", f.size, f.size, f.plies, f.a)
}

// TestKillersLeaveAFullWidthValueAlone is the semantics check, and it is made
// at full width on purpose.
//
// Ordering cannot change a minimax value over a fixed move set, so with the
// width cap lifted the two searches must agree exactly. Behind a width cap
// they need not: the killers change which cutoffs happen, cutoffs feed the
// history heuristic, and the history score decides which moves survive the cap
// at later nodes -- so a narrow search can end up looking at a different set of
// moves. That is a real difference and is measured in the harness rather than
// asserted away here.
//
// The node counts are the other half, and they are logged per position because
// the heuristic is a bet that pays off on average rather than a guarantee: a
// position where the remembered hole is the wrong one costs the tries it takes
// to find that out, and one of the fixtures below does exactly that. What is
// asserted is the total per configuration. A heuristic that costs more work
// than it saves over a frozen set of positions is not one worth carrying, and
// that is the claim the lever has to keep earning.
func TestKillersLeaveAFullWidthValueAlone(t *testing.T) {
	variants := []struct {
		name  string
		pvs   bool
		table bool
	}{
		{"plain", false, false},
		{"pvs with table", true, true},
	}
	const depth = 4
	totals := make(map[string][2]int64, len(variants))
	for _, f := range killerFixtures() {
		g := f.game(t)
		for _, v := range variants {
			p := effortFullWidth()
			p.pvs, p.useTable = v.pvs, v.table

			before := effortFingerprint(g)

			p.killers = false
			off := effortSearcher(t, p, g.Size())
			want := off.search(g, depth, 0, -infScore, infScore, 0)

			p.killers = true
			on := effortSearcher(t, p, g.Size())
			have := on.search(g, depth, 0, -infScore, infScore, 0)

			if want != have {
				t.Errorf("%s, %s: %d without killers, %d with them; ordering may not change a full-width value",
					f, v.name, want, have)
			}
			if after := effortFingerprint(g); after != before {
				t.Fatalf("%s, %s: the search did not restore the position", f, v.name)
			}
			t.Logf("%-24s %-14s value %8d nodes %8d -> %8d (%+6.1f%%)",
				f, v.name, want, off.nodes, on.nodes,
				100*(float64(on.nodes)/float64(off.nodes)-1))
			sum := totals[v.name]
			totals[v.name] = [2]int64{sum[0] + off.nodes, sum[1] + on.nodes}
		}
	}
	for _, v := range variants {
		sum := totals[v.name]
		t.Logf("%-14s over the frozen set: %d nodes without killers, %d with (%+.1f%%)",
			v.name, sum[0], sum[1], 100*(float64(sum[1])/float64(sum[0])-1))
		if sum[1] > sum[0] {
			t.Errorf("%s: killers entered %d nodes over the frozen set against %d without them; the heuristic costs more than it saves here",
				v.name, sum[1], sum[0])
		}
	}
}

// TestASeededKillerIsTriedFirstAndOnlyAtItsPly measures the mechanism itself,
// exactly, by counting nodes.
//
// At depth one every child of the node is a leaf and so is worth exactly one
// node, and the window is set one point below the best child's value: the node
// searches its candidates in order and stops at the first that reaches the
// window. The node count is therefore the position of the cutoff move in the
// list plus two, which turns "was the killer tried first" into an exact number
// rather than a plausible one.
//
// The four cases around it are the ways the mechanism can be wrong while still
// looking right: a killer remembered for a different ply, a killer offered
// while the lever is off, and a killer naming a hole this node never generated.
// Each must leave the search exactly as it was without the killer.
func TestASeededKillerIsTriedFirstAndOnlyAtItsPly(t *testing.T) {
	const ply, depth = 1, 1
	g := randomGame(t, tournamentRules(8), 24, rand.New(rand.NewPCG(302, 303)))
	if g.Result().Over() {
		t.Fatal("the fixture is already finished")
	}
	base := effortParams()
	base.pvs, base.useTable, base.extend = false, false, 0
	base.width = 10
	base.killers = true

	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	probe := effortSearcher(t, base, g.Size())
	st := probe.at(ply)
	probe.analyse(&st.an, g)
	if st.an.need[mine] == 1 || st.an.need[theirs] == 1 {
		t.Fatal("the fixture is decided or forced: the node would not generate an ordinary candidate list")
	}
	moves := effortCopyMoves(probe.candidates(g, &st.an, me, ply))
	if len(moves) < 3 {
		t.Fatalf("the fixture generated %d candidates, too few to tell an ordering apart", len(moves))
	}

	// What each candidate is worth to this node, measured one at a time and
	// with the whole window open, so the numbers do not depend on the order
	// the node happens to try them in.
	values := make([]int, len(moves))
	for i, mv := range moves {
		res, err := g.PlayPeg(mv.at)
		if err != nil {
			t.Fatalf("PlayPeg(%v): %v", mv.at, err)
		}
		if res.Over() {
			t.Fatalf("candidate %v ends the game: this test counts one node per child", mv.at)
		}
		child := effortSearcher(t, base, g.Size())
		values[i] = -child.search(g, depth-1, ply+1, -infScore, infScore, 0)
		if err := g.UndoLastMove(); err != nil {
			t.Fatalf("UndoLastMove: %v", err)
		}
	}
	best := slices.Max(values)
	cut := slices.Index(values, best)
	if cut == 0 {
		t.Fatal("the ordering already puts the cutoff move first: this fixture cannot tell a promoted killer from an unpromoted one")
	}
	killer := moves[cut].hole
	alpha, beta := best-1, best
	// The node itself, then the children up to and including the one that
	// reaches the window.
	unhelped := int64(cut + 2)

	occupied := int32(-1)
	n := g.Size()
	for row := range n {
		for col := range n {
			if p := (game.Point{Col: col, Row: row}); g.At(p) != game.NoPlayer {
				occupied = int32(row*n + col)
			}
		}
	}
	if occupied < 0 {
		t.Fatal("the fixture has no peg on the board")
	}

	run := func(t *testing.T, lever bool, seedPly int, hole int32) int64 {
		t.Helper()
		p := base
		p.killers = lever
		s := effortSearcher(t, p, g.Size())
		if seedPly >= 0 {
			s.killers[seedPly] = [2]int32{hole, -1}
		}
		before := effortFingerprint(g)
		got := s.search(g, depth, ply, alpha, beta, 0)
		if got != best {
			t.Fatalf("the node returned %d, want the best child's %d", got, best)
		}
		if after := effortFingerprint(g); after != before {
			t.Fatal("the search did not restore the position")
		}
		return s.nodes
	}

	t.Run("nothing remembered", func(t *testing.T) {
		if got := run(t, true, -1, 0); got != unhelped {
			t.Fatalf("%d nodes, want %d: the cutoff move sits at index %d of the list", got, unhelped, cut)
		}
	})
	t.Run("remembered at this ply", func(t *testing.T) {
		if got := run(t, true, ply, killer); got != 2 {
			t.Errorf("%d nodes, want 2: a killer this node can play must be searched before anything else", got)
		}
	})
	t.Run("remembered at another ply", func(t *testing.T) {
		if got := run(t, true, ply+1, killer); got != unhelped {
			t.Errorf("%d nodes, want the unhelped %d: a cutoff recorded at another ply answers another position", got, unhelped)
		}
	})
	t.Run("remembered with the lever off", func(t *testing.T) {
		if got := run(t, false, ply, killer); got != unhelped {
			t.Errorf("%d nodes, want the unhelped %d: the lever is off", got, unhelped)
		}
	})
	t.Run("naming a hole the node cannot play", func(t *testing.T) {
		if got := run(t, true, ply, occupied); got != unhelped {
			t.Errorf("%d nodes, want the unhelped %d: an occupied hole is not a candidate and must be ignored rather than inserted", got, unhelped)
		}
	})
}

// TestKillersAreForgottenBetweenSearches is the reuse check. One searcher
// serves every move of a game, so state that survives a search makes the bot's
// move depend on which positions it happened to be asked about earlier -- and
// the bot is meant to be a function of the seed and the position alone. A
// searcher that has already searched somewhere else must walk exactly the tree
// a fresh one walks.
func TestKillersAreForgottenBetweenSearches(t *testing.T) {
	p := effortParams()
	p.killers = true
	ctx := context.Background()

	elsewhere := randomGame(t, tournamentRules(8), 20, rand.New(rand.NewPCG(342, 343)))
	g := randomGame(t, tournamentRules(8), 24, rand.New(rand.NewPCG(348, 349)))
	if elsewhere.Result().Over() || g.Result().Over() {
		t.Fatal("a fixture is already finished")
	}

	fresh := newSearcher(p)
	want, err := fresh.root(ctx, g)
	if err != nil {
		t.Fatalf("fresh search: %v", err)
	}

	reused := newSearcher(p)
	if _, err := reused.root(ctx, elsewhere); err != nil {
		t.Fatalf("first search: %v", err)
	}
	if !slices.ContainsFunc(reused.killers[:], func(k [2]int32) bool { return k[0] >= 0 }) {
		t.Fatal("the first search recorded no killer at all, so this test would pass without a reset")
	}
	have, err := reused.root(ctx, g)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}

	if want.best != have.best || want.score != have.score || want.depth != have.depth {
		t.Errorf("the reused searcher played %v scoring %d at depth %d, the fresh one %v scoring %d at depth %d",
			have.best, have.score, have.depth, want.best, want.score, want.depth)
	}
	if fresh.nodes != reused.nodes || fresh.evaluations != reused.evaluations {
		t.Errorf("the reused searcher entered %d nodes and made %d analyses against the fresh one's %d and %d: it did not start from the same state",
			reused.nodes, reused.evaluations, fresh.nodes, fresh.evaluations)
	}
}

// TestPromoteOnlyReordersHolesTheListAlreadyHas pins the property the killer
// promotion is built on. A remembered hole is frequently not among the moves
// the node generated, and promote is what makes that harmless: it reorders the
// list or leaves it alone, and never grows it. A promote that appended instead
// would hand the search a move the width cap had dropped, or a hole with a peg
// already in it.
func TestPromoteOnlyReordersHolesTheListAlreadyHas(t *testing.T) {
	list := func() []scoredMove {
		return []scoredMove{{hole: 5}, {hole: 9}, {hole: 2}, {hole: 7}}
	}
	holes := func(moves []scoredMove) []int32 {
		out := make([]int32, len(moves))
		for i, m := range moves {
			out[i] = m.hole
		}
		return out
	}

	moves := list()
	promote(moves, 7)
	if got, want := holes(moves), []int32{7, 5, 9, 2}; !slices.Equal(got, want) {
		t.Errorf("promoting the last hole gave %v, want %v", got, want)
	}

	moves = list()
	promote(moves, 5)
	if got, want := holes(moves), []int32{5, 9, 2, 7}; !slices.Equal(got, want) {
		t.Errorf("promoting the first hole gave %v, want %v", got, want)
	}

	moves = list()
	promote(moves, 11)
	if got, want := holes(moves), holes(list()); !slices.Equal(got, want) {
		t.Errorf("promoting a hole the list does not hold gave %v, want the list unchanged as %v", got, want)
	}
	if len(moves) != len(list()) {
		t.Errorf("the list grew to %d entries: promote must never add a move", len(moves))
	}
}
