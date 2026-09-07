package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// The effort levers -- principal variation search, the node budget, the
// counters and the stop reasons -- are what let two configurations be compared
// on the same amount of work rather than on the same amount of wall clock. Each
// of them can fail quietly: a null-window search that forgets to re-search
// returns a bound as if it were a score, a node budget that is checked every
// hundred and twenty-eighth node makes the comparison depend on the machine
// after all, an interrupted iteration overwrites the finished one it was
// supposed to improve on, and a transposition table that ignores the extension
// budget answers one search with another search's number. None of those show up
// as a crash or an illegal move, so they are pinned here.
//
// The helpers are prefixed with effort because this package's tests share one
// namespace and several files were being written at once.

// effortParams is a small, wholly deterministic search: narrow enough to run
// many times in a test, deep enough that iterative deepening has iterations to
// interrupt, and clocked so generously that only a node budget can stop it. It
// is written out rather than derived from a tier, so that retuning a tier
// cannot silently change what these tests measure.
func effortParams() params {
	return params{
		budget:    time.Minute,
		maxDepth:  4,
		width:     6,
		rootWidth: 8,
		fullEval:  true,
		useTable:  true,
		extend:    2,
		pvs:       true,
	}
}

// effortFullWidth keeps every legal placement as a candidate. Width is the one
// lever that can make two sound searches of the same position disagree: the
// history heuristic feeds the ordering, the ordering decides which moves the
// width cap keeps, and two searches that prune differently learn different
// history. With no cap the move set is the position's own, so any disagreement
// left is a disagreement about value.
func effortFullWidth() params {
	p := effortParams()
	p.maxDepth = 3
	p.width, p.rootWidth = 200, 200
	p.useTable = false
	p.extend = 0
	p.pvs = false
	return p
}

// effortSearcher prepares a searcher for a direct call to search, which does
// not go through root and so has to be handed its clock.
func effortSearcher(t testing.TB, p params, n int) *searcher {
	t.Helper()
	s := newSearcher(p)
	s.ctx = context.Background()
	s.deadline = time.Now().Add(time.Minute)
	s.prepare(n)
	return s
}

// effortFingerprint is everything a search must leave exactly as it found it:
// whose turn it is, how long the record is, and every peg and link on the
// board. A search that returns without undoing a trial move corrupts the
// caller's game, and the caller has no way to notice.
func effortFingerprint(g *game.Game) string {
	var b strings.Builder
	fmt.Fprintf(&b, "turn=%v ply=%d entries=%d result=%v", g.Turn(), g.Ply(), g.Entries(), g.Result())
	n := g.Size()
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			fmt.Fprintf(&b, " %v/%d", g.At(p), g.LinkMask(p))
		}
	}
	return b.String()
}

func effortCopyMoves(moves []scoredMove) []scoredMove {
	return append([]scoredMove(nil), moves...)
}

// effortRanked checks the root list is ordered by score, which is what a
// finished iteration leaves behind. A list that is not is a list two iterations
// wrote into.
func effortRanked(t *testing.T, label string, moves []scoredMove) {
	t.Helper()
	for i := 1; i < len(moves); i++ {
		if moves[i-1].score < moves[i].score {
			t.Fatalf("%s: root move %d scores %d, behind %d which scores %d",
				label, i-1, moves[i-1].score, i, moves[i].score)
		}
	}
}

// referenceWithExtensions is plain minimax over the same move generation, the
// same threat extensions and the same recursion cap as search, with no pruning,
// no table and no principal variation. Its value for a node is by definition
// the value the real search must return.
//
// invariant_test.go has a reference of its own which deliberately runs without
// extensions. This one carries the extension budget because the budget is
// exactly what is under test here: it changes what a node's score means, and a
// transposition table that stores scores without it hands one search's number
// to a different search.
func (s *searcher) referenceWithExtensions(g *game.Game, depth, ply, ext int) int {
	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	st := s.at(ply)
	an := &st.an
	an.load(g)
	if an.need[mine] == 1 {
		return winScore - ply
	}
	forced := an.need[theirs] == 1
	atCap := ply >= maxSearchPly
	if depth <= 0 || atCap {
		if forced && ext > 0 && !atCap {
			depth, ext = 1, ext-1
		} else {
			return s.leaf(an, me)
		}
	}
	moves := s.generate(g, an, me, ply, forced)
	if len(moves) == 0 {
		if forced {
			return -(winScore - ply - 1)
		}
		return 0
	}
	// The recursion reuses this ply's buffer, so keep a copy.
	list := effortCopyMoves(moves)
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
			v = -s.referenceWithExtensions(g, depth-1, ply+1, ext)
		}
		if err := g.UndoLastMove(); err != nil {
			panic(fmt.Sprintf("reference could not undo %v: %v", mv.at, err))
		}
		if v > best {
			best = v
		}
	}
	return best
}

// TestPVSMatchesMinimax is the soundness check for the two search changes that
// can return a wrong number rather than a slow one.
//
// Principal variation search asks most moves only whether they beat the best so
// far, with a window one point wide. The answer to that question is a bound,
// not a score, so a move that beats it has to be searched again with the real
// window. Forget the re-search, or re-search with the wrong window, and the
// search still returns promptly and still plays legal moves -- it just reports
// the bound as the value, which is how a search silently starts preferring
// worse moves.
//
// The last two variants add the transposition table on top of threat
// extensions. An entry stored by a node that still had extensions left is not
// an answer for a node that has spent them: the two searched different trees.
func TestPVSMatchesMinimax(t *testing.T) {
	variants := []struct {
		name  string
		pvs   bool
		table bool
		ext   int
	}{
		{"pvs", true, false, 0},
		{"pvs with table", true, true, 0},
		{"pvs with table and extensions", true, true, 4},
		{"plain with table and extensions", false, true, 4},
	}
	rounds, floor := 3, 2
	if testing.Short() {
		rounds, floor = 2, 1
	}
	for _, v := range variants {
		// One corpus per variant, so a variant that disagrees names a position
		// the others were asked about too.
		src := rand.New(rand.NewPCG(201, 202))
		checked := 0
		for round := range rounds {
			g := randomGame(t, tournamentRules(8), 28+src.IntN(6), src)
			if g.Result().Over() {
				continue
			}
			p := effortFullWidth()
			p.pvs, p.useTable, p.extend = v.pvs, v.table, v.ext

			ref := effortSearcher(t, p, g.Size())
			before := effortFingerprint(g)
			want := ref.referenceWithExtensions(g, 3, 0, v.ext)

			got := effortSearcher(t, p, g.Size())
			have := got.search(g, 3, 0, -infScore, infScore, v.ext)

			if want != have {
				t.Fatalf("%s, round %d: minimax says %d, search says %d\n%s",
					v.name, round, want, have, g)
			}
			if after := effortFingerprint(g); after != before {
				t.Fatalf("%s, round %d: the search did not restore the position", v.name, round)
			}
			checked++
		}
		if checked < floor {
			t.Fatalf("%s: only %d positions compared, too few to trust", v.name, checked)
		}
	}
}

// TestTableAgreesWithItselfUnderExtensions is the cheap, high-volume form of
// the same transposition question, without paying for minimax: turning the
// table on may change how long the search takes and must not change what it
// concludes. It is the variant that catches an entry reused across extension
// budgets, because the budget differs between nodes of one search but the
// position and nominal depth do not.
func TestTableAgreesWithItselfUnderExtensions(t *testing.T) {
	src := rand.New(rand.NewPCG(211, 212))
	rounds, floor := 8, 5
	if testing.Short() {
		rounds, floor = 3, 2
	}
	checked := 0
	for round := range rounds {
		g := randomGame(t, tournamentRules(8), 24+src.IntN(10), src)
		if g.Result().Over() {
			continue
		}
		const ext = 4
		p := effortFullWidth()
		p.pvs, p.extend = true, ext

		p.useTable = false
		plain := effortSearcher(t, p, g.Size())
		want := plain.search(g, 3, 0, -infScore, infScore, ext)

		p.useTable = true
		table := effortSearcher(t, p, g.Size())
		have := table.search(g, 3, 0, -infScore, infScore, ext)

		if want != have {
			t.Fatalf("round %d: %d without the table, %d with it\n%s", round, want, have, g)
		}
		checked++
	}
	if checked < floor {
		t.Fatalf("only %d positions compared, too few to trust", checked)
	}
}

// TestNodeBudgetIsExactAndRepeatable pins what the node budget is for. A budget
// that is only checked every hundred and twenty-eighth node, or that counts
// something other than what it caps, gives two contenders different amounts of
// work and calls the comparison fair. So: never more nodes than the budget,
// exactly the budget when the budget is what stopped it, the same answer twice
// running, and a legal move and an intact position however small the budget is.
func TestNodeBudgetIsExactAndRepeatable(t *testing.T) {
	src := rand.New(rand.NewPCG(221, 222))
	ctx := context.Background()
	positions := 0
	for round := range 6 {
		g := randomGame(t, tournamentRules(8), 14+src.IntN(10), src)
		if g.Result().Over() {
			continue
		}
		base := effortParams()
		free := newSearcher(base)
		want, err := free.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: unbounded search: %v", round, err)
		}
		unbounded := free.nodes
		if unbounded < 64 {
			// Nothing to cut short.
			continue
		}
		before := effortFingerprint(g)
		for _, limit := range []int64{1, 2, 7, 128, 129, unbounded / 2, unbounded, unbounded + 1024} {
			p := base
			p.nodeLimit = limit
			s := newSearcher(p)
			out, err := s.root(ctx, g)
			if err != nil {
				t.Fatalf("round %d, budget %d: %v", round, limit, err)
			}
			if s.nodes > limit {
				t.Fatalf("round %d, budget %d: entered %d nodes", round, limit, s.nodes)
			}
			if after := effortFingerprint(g); after != before {
				t.Fatalf("round %d, budget %d: the position was not restored", round, limit)
			}
			if err := g.CanPlace(g.Turn(), out.best); err != nil {
				t.Fatalf("round %d, budget %d: illegal move %v: %v", round, limit, out.best, err)
			}
			effortRanked(t, fmt.Sprintf("round %d, budget %d", round, limit), out.moves)

			if limit < unbounded {
				if s.stopReason != stopNodes {
					t.Fatalf("round %d, budget %d: stopped because %q, want %q",
						round, limit, s.stopReason, stopNodes)
				}
				if s.nodes != limit {
					t.Fatalf("round %d, budget %d: stopped after %d nodes, leaving budget unspent",
						round, limit, s.nodes)
				}
			} else {
				if s.nodes != unbounded {
					t.Fatalf("round %d, budget %d: entered %d nodes, the same search unbounded entered %d",
						round, limit, s.nodes, unbounded)
				}
				if out.best != want.best || out.score != want.score || out.depth != want.depth {
					t.Fatalf("round %d, budget %d: answered %v/%d at depth %d, unbounded answered %v/%d at depth %d",
						round, limit, out.best, out.score, out.depth, want.best, want.score, want.depth)
				}
			}

			again := newSearcher(p)
			twice, err := again.root(ctx, g)
			if err != nil {
				t.Fatalf("round %d, budget %d, second run: %v", round, limit, err)
			}
			if again.nodes != s.nodes || again.evaluations != s.evaluations || again.stopReason != s.stopReason {
				t.Fatalf("round %d, budget %d: first run %d nodes/%d evaluations/%q, second run %d/%d/%q",
					round, limit, s.nodes, s.evaluations, s.stopReason,
					again.nodes, again.evaluations, again.stopReason)
			}
			if twice.best != out.best || twice.score != out.score || twice.depth != out.depth {
				t.Fatalf("round %d, budget %d: answered %v/%d at depth %d, then %v/%d at depth %d",
					round, limit, out.best, out.score, out.depth, twice.best, twice.score, twice.depth)
			}
		}
		positions++
	}
	if positions < 3 {
		t.Fatalf("only %d positions exercised, too few to trust", positions)
	}
}

// TestInterruptedIterationKeepsTheFinishedOne is the deepening loop's contract.
// An iteration that is cut off has scored some root moves at the new depth and
// left the rest at the old one; handing that mixture back is worse than handing
// back the shallower search, because the beginner tier samples among these
// scores and the hint reads the gap between the first two.
//
// The budget is in nodes rather than milliseconds so that the interruption
// lands in the same place every run: a search stopped a fixed number of nodes
// into iteration d+1 must answer exactly as the search that stopped after
// iteration d.
func TestInterruptedIterationKeepsTheFinishedOne(t *testing.T) {
	const finished = 2
	src := rand.New(rand.NewPCG(231, 232))
	ctx := context.Background()
	checked := 0
	for round := range 8 {
		g := randomGame(t, tournamentRules(8), 14+src.IntN(10), src)
		if g.Result().Over() {
			continue
		}
		shallow := effortParams()
		shallow.maxDepth = finished
		short := newSearcher(shallow)
		want, err := short.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: shallow search: %v", round, err)
		}
		if want.depth != finished {
			// The search settled the position before reaching that depth.
			continue
		}
		spent := short.nodes
		wanted := effortCopyMoves(want.moves)

		deep := effortParams()
		deep.maxDepth = finished + 2
		free := newSearcher(deep)
		if _, err := free.root(ctx, g); err != nil {
			t.Fatalf("round %d: deep search: %v", round, err)
		}
		if free.nodes <= spent {
			continue
		}

		for _, extra := range []int64{1, 3, 11, 47} {
			p := deep
			p.nodeLimit = spent + extra
			if p.nodeLimit >= free.nodes {
				continue
			}
			s := newSearcher(p)
			out, err := s.root(ctx, g)
			if err != nil {
				t.Fatalf("round %d, %d nodes past iteration %d: %v", round, extra, finished, err)
			}
			if out.depth != finished {
				// It got further than the iteration under test, so there is no
				// mixture to look for here.
				continue
			}
			if s.stopReason != stopNodes {
				t.Fatalf("round %d, %d nodes past iteration %d: stopped because %q",
					round, extra, finished, s.stopReason)
			}
			if len(out.moves) != len(wanted) {
				t.Fatalf("round %d, %d nodes past iteration %d: %d root moves, want %d",
					round, extra, finished, len(out.moves), len(wanted))
			}
			for i := range wanted {
				if out.moves[i] != wanted[i] {
					t.Fatalf("round %d, %d nodes past iteration %d: root move %d is %v scoring %d, want %v scoring %d: the unfinished iteration reached the answer",
						round, extra, finished, i,
						out.moves[i].at, out.moves[i].score, wanted[i].at, wanted[i].score)
				}
			}
			if out.best != want.best || out.score != want.score {
				t.Fatalf("round %d, %d nodes past iteration %d: answered %v/%d, the finished iteration answered %v/%d",
					round, extra, finished, out.best, out.score, want.best, want.score)
			}
			checked++
		}
	}
	if checked < 3 {
		t.Fatalf("only %d interrupted iterations observed, too few to trust", checked)
	}
}

// TestStoppedSearchStillAnswers covers the exits that are not the search
// finishing: a context that was cancelled before the call, a deadline already
// in the past, and a budget that runs out with the search wide open. Each has
// to come back with a legal move, an untouched position, and the reason it
// stopped -- a bot that returns nothing because it was interrupted is a bot
// that cannot be given a turn clock.
func TestStoppedSearchStillAnswers(t *testing.T) {
	src := rand.New(rand.NewPCG(241, 242))
	var g *game.Game
	for range 20 {
		g = randomGame(t, tournamentRules(10), 16, src)
		if !g.Result().Over() {
			break
		}
	}
	if g.Result().Over() {
		t.Fatal("could not build an unfinished position")
	}
	before := effortFingerprint(g)

	t.Run("cancelled before the call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := newSearcher(effortParams())
		out, err := s.root(ctx, g)
		if err != nil {
			t.Fatalf("root: %v", err)
		}
		if s.stopReason != stopCanceled {
			t.Errorf("stopped because %q, want %q", s.stopReason, stopCanceled)
		}
		if s.nodes != 0 {
			t.Errorf("entered %d nodes after cancellation", s.nodes)
		}
		if out.depth != 0 {
			t.Errorf("reported depth %d, but no iteration can have finished", out.depth)
		}
		if err := g.CanPlace(g.Turn(), out.best); err != nil {
			t.Errorf("illegal move %v: %v", out.best, err)
		}
		if after := effortFingerprint(g); after != before {
			t.Error("the position was not restored")
		}
	})

	t.Run("deadline already past", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		s := newSearcher(effortParams())
		out, err := s.root(ctx, g)
		if err != nil {
			t.Fatalf("root: %v", err)
		}
		if s.stopReason != stopTime {
			t.Errorf("stopped because %q, want %q", s.stopReason, stopTime)
		}
		if err := g.CanPlace(g.Turn(), out.best); err != nil {
			t.Errorf("illegal move %v: %v", out.best, err)
		}
		if after := effortFingerprint(g); after != before {
			t.Error("the position was not restored")
		}
	})

	t.Run("budget runs out mid-search", func(t *testing.T) {
		p := effortParams()
		p.budget = time.Millisecond
		p.maxDepth, p.width, p.rootWidth, p.extend = 24, 20, 24, 8
		s := newSearcher(p)
		start := time.Now()
		out, err := s.root(context.Background(), g)
		took := time.Since(start)
		if err != nil {
			t.Fatalf("root: %v", err)
		}
		// A depth-24 search of this position does not finish in a second; if
		// it came back inside one, the budget is what stopped it.
		if took > time.Second {
			t.Errorf("a %v budget took %v", p.budget, took)
		}
		if s.stopReason != stopTime && s.stopReason != stopDecided {
			t.Errorf("stopped because %q, want %q or %q", s.stopReason, stopTime, stopDecided)
		}
		if s.elapsed <= 0 {
			t.Errorf("reported %v elapsed", s.elapsed)
		}
		if err := g.CanPlace(g.Turn(), out.best); err != nil {
			t.Errorf("illegal move %v: %v", out.best, err)
		}
		effortRanked(t, "interrupted by the clock", out.moves)
		if after := effortFingerprint(g); after != before {
			t.Error("the position was not restored")
		}
	})
}

// TestCountersMeasureTheWorkDone checks the two counters mean what a comparison
// between configurations needs them to mean. Nodes are entries into the
// recursion and evaluations are analyses of a position, which is why there are
// always more of the second: every node analyses its position, and the root
// analyses one that is not a node. Deepening the same search must move both.
func TestCountersMeasureTheWorkDone(t *testing.T) {
	src := rand.New(rand.NewPCG(251, 252))
	ctx := context.Background()
	compared := 0
	for round := range 6 {
		g := randomGame(t, tournamentRules(8), 14+src.IntN(10), src)
		if g.Result().Over() {
			continue
		}
		shallow, deep := effortParams(), effortParams()
		shallow.maxDepth, deep.maxDepth = 1, 3
		a, b := newSearcher(shallow), newSearcher(deep)
		outA, err := a.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: shallow search: %v", round, err)
		}
		outB, err := b.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: deep search: %v", round, err)
		}
		if outA.depth != 1 || outB.depth != 3 {
			// One of them settled the position early; the pair is not a
			// comparison of one ply against three.
			continue
		}
		if b.nodes <= a.nodes {
			t.Fatalf("round %d: three plies entered %d nodes, one ply entered %d", round, b.nodes, a.nodes)
		}
		if b.evaluations <= a.evaluations {
			t.Fatalf("round %d: three plies made %d evaluations, one ply made %d",
				round, b.evaluations, a.evaluations)
		}
		for _, c := range []struct {
			name string
			s    *searcher
		}{{"one ply", a}, {"three plies", b}} {
			if c.s.nodes == 0 {
				t.Fatalf("round %d, %s: no nodes counted", round, c.name)
			}
			if c.s.evaluations < c.s.nodes+1 {
				t.Fatalf("round %d, %s: %d evaluations for %d nodes, but every node analyses its position and the root analyses one more",
					round, c.name, c.s.evaluations, c.s.nodes)
			}
			if c.s.elapsed <= 0 {
				t.Fatalf("round %d, %s: reported %v elapsed", round, c.name, c.s.elapsed)
			}
			if c.s.stopReason == "" {
				t.Fatalf("round %d, %s: no stop reason recorded", round, c.name)
			}
		}
		compared++
	}
	if compared < 3 {
		t.Fatalf("only %d positions compared, too few to trust", compared)
	}
}

// TestPlainSearchIsAUsableBaseline keeps the pvs lever honest as a baseline to
// measure against: with the same move set, turning it off must change how the
// search gets to its answer and not what the answer is worth. If the two ever
// valued a position differently, a measurement of what principal variation
// search costs or saves would be comparing two different searches.
func TestPlainSearchIsAUsableBaseline(t *testing.T) {
	src := rand.New(rand.NewPCG(261, 262))
	ctx := context.Background()
	rounds, floor := 3, 2
	if testing.Short() {
		rounds, floor = 2, 1
	}
	compared := 0
	for round := range rounds {
		g := randomGame(t, tournamentRules(8), 26+src.IntN(8), src)
		if g.Result().Over() {
			continue
		}
		plainParams := effortFullWidth()
		pvsParams := plainParams
		pvsParams.pvs = true

		plain, pvs := newSearcher(plainParams), newSearcher(pvsParams)
		outPlain, err := plain.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: plain search: %v", round, err)
		}
		outPVS, err := pvs.root(ctx, g)
		if err != nil {
			t.Fatalf("round %d: principal variation search: %v", round, err)
		}
		if outPlain.depth != outPVS.depth {
			t.Fatalf("round %d: plain reached depth %d, pvs reached %d", round, outPlain.depth, outPVS.depth)
		}
		if outPlain.score != outPVS.score {
			t.Fatalf("round %d: plain values the position at %d, pvs at %d\n%s",
				round, outPlain.score, outPVS.score, g)
		}
		for _, c := range []struct {
			name string
			out  rootResult
		}{{"plain", outPlain}, {"pvs", outPVS}} {
			if err := g.CanPlace(g.Turn(), c.out.best); err != nil {
				t.Fatalf("round %d, %s: illegal move %v: %v", round, c.name, c.out.best, err)
			}
			effortRanked(t, fmt.Sprintf("round %d, %s", round, c.name), c.out.moves)
		}
		compared++
	}
	if compared < floor {
		t.Fatalf("only %d positions compared, too few to trust", compared)
	}
}

// effortPosition replays a transcript of hole names into a game.
func effortPosition(t *testing.T, rs game.Ruleset, moves ...string) *game.Game {
	t.Helper()
	g, err := game.New(rs)
	if err != nil {
		t.Fatalf("game.New: %v", err)
	}
	for _, name := range moves {
		p, err := game.ParsePoint(name)
		if err != nil {
			t.Fatalf("ParsePoint(%q): %v", name, err)
		}
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("PlayPeg(%v): %v", p, err)
		}
	}
	return g
}

// effortRootValue is what a root move is actually worth to the side that plays
// it: minimax to the same horizon the root's last iteration used, with no
// window to hide behind.
func effortRootValue(t *testing.T, ref *searcher, g *game.Game, at game.Point, depth, ext int) int {
	t.Helper()
	me := g.Turn()
	res, err := g.PlayPeg(at)
	if err != nil {
		t.Fatalf("PlayPeg(%v): %v", at, err)
	}
	var v int
	if res.Over() {
		switch res.Winner() {
		case me:
			v = winScore - 1
		case game.NoPlayer:
			v = 0
		default:
			v = -(winScore - 1)
		}
	} else {
		v = -ref.referenceWithExtensions(g, depth, 1, ext)
	}
	if err := g.UndoLastMove(); err != nil {
		t.Fatalf("UndoLastMove: %v", err)
	}
	return v
}

// TestFrozenRootChoiceIsWorthItsScore is the regression for the root playing a
// move it had never measured.
//
// The root searches its first move with the whole window and every later move
// with the window open only above alpha. A later move that cannot beat alpha
// comes back with an upper bound, and a fail-soft bound can land exactly on
// alpha -- so the move looks level with the best one while being worth
// anything at all below that. Ranking on the number alone then lets the
// positional tie-break promote it, and the bot plays a move whose value was
// never established.
//
// This is not a hypothetical. On the position below, taken from a run against
// the frozen baseline, a depth-3 full-width search reported +521357 and played
// D5, a hole whose actual value at the same horizon is a forced loss. So the
// test measures the move the search chose rather than the score it printed:
// a search that reports the right number while playing the wrong hole is the
// exact failure being guarded against, and it passes any check that only reads
// the score. One frozen position rather than a random corpus, because this
// costs a full minimax of every root move.
func TestFrozenRootChoiceIsWorthItsScore(t *testing.T) {
	g := effortPosition(t, tournamentRules(8),
		"E8", "A2", "C5", "F5", "D7", "E6", "E1", "E3", "B7",
		"D2", "G4", "F7", "C2", "F4", "F2", "G6", "G3")
	if g.Result().Over() {
		t.Fatal("the frozen position is already finished")
	}

	p := effortFullWidth()
	ref := effortSearcher(t, p, g.Size())
	measured := make(map[game.Point]int)
	valueOf := func(at game.Point) int {
		if v, ok := measured[at]; ok {
			return v
		}
		v := effortRootValue(t, ref, g, at, p.maxDepth-1, p.extend)
		measured[at] = v
		return v
	}

	for _, variant := range []struct {
		name        string
		pvs         bool
		temperature float64
	}{{"plain", false, 0}, {"pvs", true, 0}, {"sampled", false, 1.5}} {
		q := p
		q.pvs = variant.pvs
		q.temperature = variant.temperature
		s := newSearcher(q)
		out, err := s.root(context.Background(), g)
		if err != nil {
			t.Fatalf("%s: root: %v", variant.name, err)
		}
		if out.depth != q.maxDepth {
			t.Fatalf("%s: finished depth %d, want %d", variant.name, out.depth, q.maxDepth)
		}
		best := -infScore
		for _, m := range out.moves {
			if v := valueOf(m.at); v > best {
				best = v
			}
		}
		played := valueOf(out.best)
		if played != best {
			t.Fatalf("%s: played %v, worth %d at this horizon, when %d was available and the search reported %d",
				variant.name, out.best, played, best, out.score)
		}
		if out.score != best {
			t.Fatalf("%s: reported %d for a position worth %d", variant.name, out.score, best)
		}
		if q.temperature > 0 {
			for _, m := range out.moves {
				if actual := valueOf(m.at); m.score != actual {
					t.Fatalf("sampling weights %v as %d, but its minimax value is %d", m.at, m.score, actual)
				}
			}
			for seed := range 32 {
				at := sampleMove(out.moves, q.temperature, rand.New(rand.NewPCG(uint64(seed), 91)))
				if actual := valueOf(at); actual <= -decidedScore && best > -decidedScore {
					t.Fatalf("sampling chose %v, an avoidable forced loss worth %d, with a %d-valued reply available", at, actual, best)
				}
			}
		}
	}
}

func TestNodeCeilingAllowsItsLastAdmittedLeaf(t *testing.T) {
	g := game.MustNew(smallRules(16))
	p := params{budget: time.Minute, maxDepth: 1, width: 128, rootWidth: 128, fullEval: true}
	free := newSearcher(p)
	want, err := free.root(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if free.nodes != 128 || want.depth != 1 {
		t.Fatalf("control did not visit exactly 128 leaves: nodes=%d depth=%d", free.nodes, want.depth)
	}
	p.nodeLimit = 128
	bounded := newSearcher(p)
	got, err := bounded.root(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if bounded.nodes != 128 || got.depth != want.depth || got.best != want.best || got.score != want.score {
		t.Fatalf("last admitted leaf was discarded: nodes=%d depth=%d move=%v score=%d; want depth=%d move=%v score=%d",
			bounded.nodes, got.depth, got.best, got.score, want.depth, want.best, want.score)
	}
}

func TestRecursionCapEvaluatesInsteadOfInventingAWin(t *testing.T) {
	g := game.MustNew(smallRules(8))
	p := effortParams()
	s := effortSearcher(t, p, g.Size())
	var a analysis
	a.load(g)
	want := a.terms(g.Turn()).Score()
	before := game.PositionDigest(g)
	got := s.search(g, 1, maxSearchPly, decidedScore+1, infScore, 8)
	if got != want || got >= decidedScore || got <= -decidedScore {
		t.Fatalf("recursion cap returned %d, want finite evaluation %d", got, want)
	}
	if game.PositionDigest(g) != before {
		t.Fatal("recursion cap changed position")
	}
}

func TestTableRejectsAValueFromAnotherExtensionHorizon(t *testing.T) {
	g := game.MustNew(smallRules(8))
	p := effortParams()
	p.useTable = false
	control := effortSearcher(t, p, g.Size())
	want := control.search(g, 1, 0, -infScore, infScore, 0)
	p.useTable = true
	s := effortSearcher(t, p, g.Size())
	var a analysis
	a.load(g)
	s.table[a.hash&s.mask] = tableEntry{key: a.hash, score: 1234567, best: -1, depth: 1, ext: 4, flag: flagExact}
	got := s.search(g, 1, 0, -infScore, infScore, 0)
	if got != want {
		t.Fatalf("different extension horizon supplied value %d; fresh search says %d", got, want)
	}
}
