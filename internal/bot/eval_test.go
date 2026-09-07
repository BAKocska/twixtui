package bot

import (
	"container/heap"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// randomGame plays up to plies random legal moves and returns the position,
// stopping early if the game finishes.
func randomGame(t testing.TB, rs game.Ruleset, plies int, src *rand.Rand) *game.Game {
	t.Helper()
	g, err := game.New(rs)
	if err != nil {
		t.Fatalf("game.New: %v", err)
	}
	for range plies {
		if g.Result().Over() {
			break
		}
		legal := g.LegalPlacements(g.Turn())
		if len(legal) == 0 {
			break
		}
		if _, err := g.PlayPeg(legal[src.IntN(len(legal))]); err != nil {
			t.Fatalf("PlayPeg: %v", err)
		}
	}
	return g
}

func smallRules(size int) game.Ruleset {
	rs := game.PP
	rs.Size = size
	return rs
}

func TestNeedCountsPegsLeftOnEmptyBoard(t *testing.T) {
	// On an empty 10x10 board Vertical must cross ten rows. The cheapest chain
	// takes rows 0, 2, 4, 6, 8 and 9: six pegs. Horizontal is the mirror of
	// that, so the position is symmetric.
	g := game.MustNew(smallRules(10))
	var a analysis
	a.load(g)
	if got := a.need[sideIndex(game.Vertical)]; got != 6 {
		t.Errorf("Vertical need = %d, want 6", got)
	}
	if got := a.need[sideIndex(game.Horizontal)]; got != 6 {
		t.Errorf("Horizontal need = %d, want 6", got)
	}
}

func TestNeedZeroMatchesConnected(t *testing.T) {
	src := rand.New(rand.NewPCG(1, 2))
	var a analysis
	for range 200 {
		g := randomGame(t, smallRules(8), 60, src)
		a.load(g)
		for _, pl := range []game.Player{game.Vertical, game.Horizontal} {
			want := g.Connected(pl)
			got := a.need[sideIndex(pl)] == 0
			if got != want {
				t.Fatalf("need==0 for %s = %v, engine Connected = %v\n%s", pl, got, want, g)
			}
		}
	}
}

// TestNeedOneMeansImmediateWin checks the tactical oracle the whole search
// leans on: the side to move has a move that wins at once exactly when its
// need is one, and winningHole names such a move.
func TestNeedOneMeansImmediateWin(t *testing.T) {
	src := rand.New(rand.NewPCG(3, 4))
	var a analysis
	checkedThreats := 0
	for range 700 {
		g := randomGame(t, smallRules(8), 50, src)
		if g.Result().Over() {
			continue
		}
		me := g.Turn()
		a.load(g)

		wins := false
		for _, p := range g.LegalPlacements(me) {
			res, err := g.PlayPeg(p)
			if err != nil {
				t.Fatalf("PlayPeg %v: %v", p, err)
			}
			won := res.Winner() == me
			if err := g.UndoLastMove(); err != nil {
				t.Fatalf("UndoLastMove: %v", err)
			}
			if won {
				wins = true
				break
			}
		}

		if got := a.need[sideIndex(me)] == 1; got != wins {
			t.Fatalf("need==1 for %s = %v, brute force found a winning move = %v\n%s",
				me, got, wins, g)
		}
		if !wins {
			continue
		}
		checkedThreats++
		hole, ok := a.winningHole(sideIndex(me))
		if !ok {
			t.Fatalf("need==1 but winningHole found nothing\n%s", g)
		}
		res, err := g.PlayPeg(hole)
		if err != nil {
			t.Fatalf("winningHole %v is not legal: %v", hole, err)
		}
		if res.Winner() != me {
			t.Fatalf("winningHole %v did not win for %s: %v", hole, me, res)
		}
		if err := g.UndoLastMove(); err != nil {
			t.Fatalf("UndoLastMove: %v", err)
		}
	}
	if checkedThreats < 20 {
		t.Fatalf("only %d positions with an immediate win were exercised, too few to trust", checkedThreats)
	}
}

// TestUndoRestoresAnalysis is the make/unmake correctness check that makes
// searching in place safe: playing a move and undoing it must leave the
// evaluation exactly where it was.
func TestUndoRestoresAnalysis(t *testing.T) {
	src := rand.New(rand.NewPCG(5, 6))
	var before, after analysis
	for range 100 {
		g := randomGame(t, game.Ruleset{Size: 10, DeliberateLinking: true, LinkRemoval: true, Swap: true}, 30, src)
		if g.Result().Over() {
			continue
		}
		before.load(g)
		legal := g.LegalPlacements(g.Turn())
		p := legal[src.IntN(len(legal))]
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("PlayPeg: %v", err)
		}
		if err := g.UndoLastMove(); err != nil {
			t.Fatalf("UndoLastMove: %v", err)
		}
		after.load(g)
		if before.need != after.need || before.bottlenecks != after.bottlenecks || before.hash != after.hash {
			t.Fatalf("undo changed the analysis: need %v->%v bottlenecks %v->%v hash %x->%x",
				before.need, after.need, before.bottlenecks, after.bottlenecks, before.hash, after.hash)
		}
	}
}

// ---------------------------------------------------------------------------
// Independent reference traversal
// ---------------------------------------------------------------------------
//
// The four sweeps take their neighbours, their border seeds and their reset
// template from tables cached per board size and shared between analyses. A
// wrong table does not crash: it quietly drops a border hole, steps off one
// edge onto the next row, or hands a board another board's geometry, and every
// number the search and the hint feature read stays plausible. So the
// reference below re-derives the same distances from the engine alone — At,
// LinkMask, HasLink, InBounds, Point.Add and LinksCross — with a priority
// queue in place of the layered sweep, and shares no table with eval.go: in
// particular it puts every link pair to LinksCross itself rather than trusting
// a crossing-offset table or a block mask. Only span, the bottleneck count and
// ground are restated from their definitions, because those are arithmetic on
// the distances rather than the traversal under test.

type refNode struct {
	hole int
	cost int32
}

type refQueue []refNode

func (q refQueue) Len() int           { return len(q) }
func (q refQueue) Less(i, j int) bool { return q[i].cost < q[j].cost }
func (q refQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *refQueue) Push(x any)        { *q = append(*q, x.(refNode)) }
func (q *refQueue) Pop() any {
	old := *q
	last := old[len(old)-1]
	*q = old[:len(old)-1]
	return last
}

// refAnalysis is what an analysis of a position should contain.
type refAnalysis struct {
	dist        [2][2][]int32
	span        [2][]int32
	need        [2]int
	bottlenecks [2]int
	ground      [2]int
}

func refAnalyse(g *game.Game) refAnalysis {
	n := g.Size()
	cells := n * n
	ownBlocks := !g.Rules().OwnLinksMayCross
	point := func(i int) game.Point { return game.Point{Col: i % n, Row: i / n} }
	index := func(p game.Point) int { return p.Row*n + p.Col }
	at := func(i int) game.Player { return g.At(point(i)) }

	// Every link on the board, with the side that owns it.
	type owned struct {
		link game.Link
		side int
	}
	var links []owned
	for i := range cells {
		mask := g.LinkMask(point(i))
		for d := range game.Dir(4) {
			if mask&(1<<d) == 0 {
				continue
			}
			l := game.Link{From: point(i), Dir: d}
			if ow := g.LinkOwner(l); ow != game.NoPlayer {
				links = append(links, owned{link: l, side: sideIndex(ow)})
			}
		}
	}

	// crossed[s][i] has bit d set when a link of side s crosses the canonical
	// link leaving hole i in direction d.
	var crossed [2][]uint8
	for s := range crossed {
		crossed[s] = make([]uint8, cells)
	}
	for i := range cells {
		for d := range game.Dir(4) {
			cand := game.Link{From: point(i), Dir: d}
			if !g.InBounds(cand.To()) {
				continue
			}
			for _, e := range links {
				if game.LinksCross(cand, e.link) {
					crossed[e.side][i] |= 1 << d
				}
			}
		}
	}

	usable := func(s, i int) bool {
		p := point(i)
		if s == 0 {
			return p.Col > 0 && p.Col < n-1 && g.At(p) != game.Horizontal
		}
		return p.Row > 0 && p.Row < n-1 && g.At(p) != game.Vertical
	}
	holeCost := func(s, i int) int32 {
		if at(i) == game.Player(s+1) {
			return 0
		}
		return 1
	}
	onBorder := func(s, b, i int) bool {
		line := 0
		if b == 1 {
			line = n - 1
		}
		p := point(i)
		if s == 0 {
			return p.Row == line
		}
		return p.Col == line
	}
	open := func(s, u, v int) bool {
		l, ok := game.NewLink(point(u), point(v))
		if !ok {
			return false
		}
		if g.HasLink(l) {
			return true
		}
		if at(u) != game.NoPlayer && at(v) != game.NoPlayer {
			return false
		}
		bit := uint8(1) << l.Dir
		if crossed[1-s][index(l.From)]&bit != 0 {
			return false
		}
		return !ownBlocks || crossed[s][index(l.From)]&bit == 0
	}

	// Dijkstra from every usable hole of one border, with the holes weighted
	// rather than the steps.
	sweep := func(s, b int) []int32 {
		dist := make([]int32, cells)
		for i := range dist {
			dist[i] = unusable
		}
		q := &refQueue{}
		for i := range cells {
			if !onBorder(s, b, i) || !usable(s, i) {
				continue
			}
			if c := holeCost(s, i); c < dist[i] {
				dist[i] = c
				heap.Push(q, refNode{hole: i, cost: c})
			}
		}
		for q.Len() > 0 {
			nd := heap.Pop(q).(refNode)
			if nd.cost != dist[nd.hole] {
				continue
			}
			p := point(nd.hole)
			for d := range game.Dir(game.NumDirs) {
				w := p.Add(d)
				if !g.InBounds(w) {
					continue
				}
				v := index(w)
				if !usable(s, v) {
					continue
				}
				step := nd.cost + holeCost(s, v)
				if step >= dist[v] || !open(s, nd.hole, v) {
					continue
				}
				dist[v] = step
				heap.Push(q, refNode{hole: v, cost: step})
			}
		}
		return dist
	}

	var ref refAnalysis
	for s := range 2 {
		from, to := sweep(s, 0), sweep(s, 1)
		ref.dist[s][0], ref.dist[s][1] = from, to
		span := make([]int32, cells)
		best := unusable
		for i := range cells {
			if onBorder(s, 1, i) && from[i] < best {
				best = from[i]
			}
			if from[i] >= unusable || to[i] >= unusable {
				span[i] = unusable
				continue
			}
			span[i] = from[i] + to[i] - holeCost(s, i)
		}
		ref.span[s] = span
		if best >= unusable {
			ref.need[s] = NoChain
			continue
		}
		ref.need[s] = int(best)
		counts := make([]int, best+1)
		for i := range cells {
			if span[i] != best || at(i) != game.NoPlayer || !usable(s, i) {
				continue
			}
			if step := from[i]; step >= 1 && step <= best {
				counts[step]++
			}
		}
		for _, c := range counts[1:] {
			if c == 1 {
				ref.bottlenecks[s]++
			}
		}
	}
	lead, total := 0, 0
	for i := range cells {
		m, o := ref.span[0][i], ref.span[1][i]
		if m >= unusable && o >= unusable {
			continue
		}
		total++
		switch {
		case m < o:
			lead++
		case o < m:
			lead--
		}
	}
	if total > 0 {
		per := lead * 1000 / total
		ref.ground[0], ref.ground[1] = per, -per
	}
	return ref
}

// checkAgainstReference loads a position and compares every distance and every
// term the search and the hint feature can see against the independent
// traversal.
func checkAgainstReference(t *testing.T, a *analysis, g *game.Game, label string) {
	t.Helper()
	a.load(g)
	ref := refAnalyse(g)
	n := g.Size()
	hole := func(i int) game.Point { return game.Point{Col: i % n, Row: i / n} }
	for s := range 2 {
		for b := range 2 {
			for i, want := range ref.dist[s][b] {
				if got := a.dist[s][b][i]; got != want {
					t.Fatalf("%s: side %d distance from border %d to %v = %d, independent traversal says %d\n%s",
						label, s, b, hole(i), got, want, g)
				}
			}
		}
		for i, want := range ref.span[s] {
			if got := a.span[s][i]; got != want {
				t.Fatalf("%s: side %d cheapest chain through %v costs %d, independent traversal says %d\n%s",
					label, s, hole(i), got, want, g)
			}
		}
	}
	if a.need != ref.need {
		t.Fatalf("%s: need = %v, independent traversal says %v\n%s", label, a.need, ref.need, g)
	}
	if a.bottlenecks != ref.bottlenecks {
		t.Fatalf("%s: bottlenecks = %v, independent traversal says %v\n%s",
			label, a.bottlenecks, ref.bottlenecks, g)
	}
	for _, pl := range []game.Player{game.Vertical, game.Horizontal} {
		me, opp := sideIndex(pl), sideIndex(pl.Opponent())
		want := Terms{
			Dist:           ref.need[me],
			OppDist:        ref.need[opp],
			Bottlenecks:    ref.bottlenecks[me],
			OppBottlenecks: ref.bottlenecks[opp],
			Ground:         ref.ground[me],
		}
		if got := a.terms(pl); got != want {
			t.Fatalf("%s: terms for %s = %+v, independent traversal says %+v\n%s", label, pl, got, want, g)
		}
	}
}

// TestAnalysisAgreesWithIndependentTraversal is the differential regression
// behind caching the board geometry. It covers the smallest and the largest
// legal board as well as the sizes the bot is played on, under every ruleset
// preset, and reuses one analysis throughout so that the board size keeps
// changing under it: geometry kept for the wrong size is invisible to a run
// that never changes size. Each position is also checked one peg further on
// and again after that peg is taken back, so a table that survives a mutation
// but not its undo is caught too.
func TestAnalysisAgreesWithIndependentTraversal(t *testing.T) {
	sizes := []int{game.MinSize, 7, 8, 10, 16, 24, 47, game.MaxSize}
	src := rand.New(rand.NewPCG(13, 14))
	var a analysis
	for _, preset := range game.PresetNames() {
		rs, err := game.Preset(preset)
		if err != nil {
			t.Fatalf("game.Preset(%q): %v", preset, err)
		}
		for _, size := range sizes {
			rs.Size = size
			for _, plies := range []int{0, 6, 24} {
				g := randomGame(t, rs, plies, src)
				label := fmt.Sprintf("%s %dx%d after %d plies", preset, size, size, plies)
				checkAgainstReference(t, &a, g, label)
				if g.Result().Over() {
					continue
				}
				legal := g.LegalPlacements(g.Turn())
				if len(legal) == 0 {
					continue
				}
				p := legal[src.IntN(len(legal))]
				if _, err := g.PlayPeg(p); err != nil {
					t.Fatalf("PlayPeg %v: %v", p, err)
				}
				checkAgainstReference(t, &a, g, fmt.Sprintf("%s plus %v", label, p))
				if err := g.UndoLastMove(); err != nil {
					t.Fatalf("UndoLastMove: %v", err)
				}
				checkAgainstReference(t, &a, g, label+" restored")
			}
		}
	}
}

// someOwnLink names one link belonging to pl, if it has any.
func someOwnLink(g *game.Game, pl game.Player) (game.Point, game.Point, bool) {
	n := g.Size()
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if g.At(p) != pl {
				continue
			}
			mask := g.LinkMask(p)
			for d := range game.Dir(game.NumDirs) {
				if mask&(1<<d) == 0 {
					continue
				}
				return p, p.Add(d), true
			}
		}
	}
	return game.Point{}, game.Point{}, false
}

// unlinkedOwnPairs counts pairs of one side's pegs a knight's move apart with
// no link between them. That is the board state linkOpen refuses to travel
// along, and a game played only through PlayPeg reaches it only where a
// crossing forbade the link.
func unlinkedOwnPairs(g *game.Game) int {
	n := g.Size()
	pairs := 0
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			pl := g.At(p)
			if pl == game.NoPlayer {
				continue
			}
			for d := range game.Dir(4) {
				q := p.Add(d)
				if !g.InBounds(q) || g.At(q) != pl {
					continue
				}
				if l, ok := game.NewLink(p, q); ok && !g.HasLink(l) {
					pairs++
				}
			}
		}
	}
	return pairs
}

// deliberateGame plays random turns under a ruleset that lets the player choose
// links, and uses that choice: it withdraws one of the links every other
// placement offers and, where the ruleset allows it, takes one of its own
// earlier links back before placing. randomGame cannot reach these positions,
// because PlayPeg keeps every link a placement offers. It returns how many
// earlier links were removed.
func deliberateGame(t testing.TB, rs game.Ruleset, turns int, src *rand.Rand) (*game.Game, int) {
	t.Helper()
	g, err := game.New(rs)
	if err != nil {
		t.Fatalf("game.New: %v", err)
	}
	removed := 0
	for turn := range turns {
		if g.Result().Over() {
			break
		}
		me := g.Turn()
		// An earlier link may only come off before the turn's peg goes down.
		if rs.LinkRemoval && turn%3 == 2 {
			if from, to, ok := someOwnLink(g, me); ok {
				if err := g.RemoveLink(from, to); err != nil {
					t.Fatalf("RemoveLink %v-%v: %v", from, to, err)
				}
				removed++
			}
		}
		legal := g.LegalPlacements(me)
		if len(legal) == 0 {
			break
		}
		p := legal[src.IntN(len(legal))]
		if err := g.PlacePeg(p); err != nil {
			t.Fatalf("PlacePeg %v: %v", p, err)
		}
		if offered := g.Staged().AutoLinks; turn%2 == 0 && offered != 0 {
			for d := range game.Dir(game.NumDirs) {
				if offered&(1<<d) == 0 {
					continue
				}
				if err := g.RemoveLink(p, p.Add(d)); err != nil {
					t.Fatalf("withdrawing the %v link of %v: %v", d, p, err)
				}
				break
			}
		}
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("CommitTurn: %v", err)
		}
	}
	return g, removed
}

// TestAnalysisAgreesWithReferenceOnChosenLinkStates covers the positions only
// deliberate linking produces: own pegs a knight's move apart that are not
// joined, and routes an own link blocked until its owner took it back. Both
// change which links the evaluation may still create and which of them block,
// and neither appears in a game played through PlayPeg alone. The two counts
// are asserted so the fixture cannot quietly stop producing those states and
// leave the run vacuous.
func TestAnalysisAgreesWithReferenceOnChosenLinkStates(t *testing.T) {
	src := rand.New(rand.NewPCG(17, 18))
	var a analysis
	unlinked, removed := 0, 0
	for _, preset := range []string{"std", "classic"} {
		rs, err := game.Preset(preset)
		if err != nil {
			t.Fatalf("game.Preset(%q): %v", preset, err)
		}
		if !rs.DeliberateLinking || !rs.LinkRemoval {
			t.Fatalf("%s no longer lets links be chosen and removed: %s", preset, rs.Describe())
		}
		for _, size := range []int{game.MinSize, 10, 24, game.MaxSize} {
			rs.Size = size
			for _, turns := range []int{8, 24} {
				g, took := deliberateGame(t, rs, turns, src)
				removed += took
				unlinked += unlinkedOwnPairs(g)
				label := fmt.Sprintf("%s %dx%d after %d chosen-link turns", preset, size, size, turns)
				checkAgainstReference(t, &a, g, label)
				if g.Result().Over() {
					continue
				}
				legal := g.LegalPlacements(g.Turn())
				if len(legal) == 0 {
					continue
				}
				p := legal[src.IntN(len(legal))]
				if _, err := g.PlayPeg(p); err != nil {
					t.Fatalf("PlayPeg %v: %v", p, err)
				}
				checkAgainstReference(t, &a, g, fmt.Sprintf("%s plus %v", label, p))
				if err := g.UndoLastMove(); err != nil {
					t.Fatalf("UndoLastMove: %v", err)
				}
				checkAgainstReference(t, &a, g, label+" restored")
			}
		}
	}
	if unlinked == 0 || removed == 0 {
		t.Fatalf("fixtures held %d unjoined own pairs and took back %d links; the chosen-link states this test exists for were never reached",
			unlinked, removed)
	}
}

// TestSharedGeometryServesAnalysesConcurrently defends sharing one geometry
// between analyses: evaluations running at the same time on the same board
// size must each still agree with the independent traversal, and under -race
// the same run witnesses that nothing writes to the shared tables.
func TestSharedGeometryServesAnalysesConcurrently(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("worker%d", seed), func(t *testing.T) {
			t.Parallel()
			src := rand.New(rand.NewPCG(seed, 15))
			var a analysis
			for _, size := range []int{16, 24} {
				for _, plies := range []int{5, 20} {
					g := randomGame(t, smallRules(size), plies, src)
					checkAgainstReference(t, &a, g,
						fmt.Sprintf("worker %d %dx%d after %d plies", seed, size, size, plies))
				}
			}
		})
	}
}

func benchPosition(b *testing.B, size, plies int) *game.Game {
	b.Helper()
	return randomGame(b, smallRules(size), plies, rand.New(rand.NewPCG(7, 8)))
}

func BenchmarkAnalysisLoad24(b *testing.B) {
	g := benchPosition(b, 24, 40)
	var a analysis
	a.load(g)
	for b.Loop() {
		a.load(g)
	}
}

func BenchmarkAnalysisLoad10(b *testing.B) {
	g := benchPosition(b, 10, 20)
	var a analysis
	a.load(g)
	for b.Loop() {
		a.load(g)
	}
}

// BenchmarkMakeUnmake24 and BenchmarkClonePlay24 are the measurement behind
// searching in place rather than cloning per node.
func BenchmarkMakeUnmake24(b *testing.B) {
	g := benchPosition(b, 24, 40)
	p := g.LegalPlacements(g.Turn())[0]
	for b.Loop() {
		if _, err := g.PlayPeg(p); err != nil {
			b.Fatal(err)
		}
		if err := g.UndoLastMove(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClonePlay24(b *testing.B) {
	g := benchPosition(b, 24, 40)
	p := g.LegalPlacements(g.Turn())[0]
	for b.Loop() {
		if _, err := g.Clone().PlayPeg(p); err != nil {
			b.Fatal(err)
		}
	}
}
