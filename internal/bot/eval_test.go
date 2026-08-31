package bot

import (
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
