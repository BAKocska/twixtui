package bot

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// quietPosition is the fixture the bound, budget and ownership checks run on:
// four pegs on an 8x8 board, Vertical to move three pegs from a finished chain
// and Horizontal further off still. Nothing inside a few plies is a win, no
// threat narrows the candidate list, and every score a shallow search reports
// is the static evaluation's own.
func quietPosition(t testing.TB) *game.Game {
	t.Helper()
	g := game.MustNew(smallRules(8))
	playMoves(t, g, "B1", "G2", "B5", "G3")
	if g.Turn() != game.Vertical {
		t.Fatalf("fixture leaves %s to move, want Vertical", g.Turn())
	}
	var a analysis
	a.load(g)
	if need := a.need[sideIndex(game.Vertical)]; need < 3 {
		t.Fatalf("Vertical is %d pegs from a chain, so a three-ply search can win it", need)
	}
	if need := a.need[sideIndex(game.Horizontal)]; need < 2 {
		t.Fatalf("Horizontal is %d pegs from a chain, so the root is answering a threat", need)
	}
	return g
}

// finiteParams are the levers the oracle check runs under: a fixed-depth
// search over every legal placement, with no extensions, no table and no
// principal variation search, so the tree is a plain finite minimax whose
// moves a second search can measure one at a time.
func finiteParams(depth int) params {
	p := hintParams()
	p.maxDepth = depth
	p.width, p.rootWidth = 64, 64
	p.budget = time.Hour
	p.nodeLimit = 0
	p.extend = 0
	p.pvs = false
	p.useTable = false
	return p
}

// hintLevers are the analysis defaults with the clock lifted out of the way and
// the depth fixed, so that two searches under them are comparable.
func hintLevers(depth int) params {
	p := hintParams()
	p.maxDepth = depth
	p.budget = time.Hour
	return p
}

// termsOf reads a position's decomposition independently of any search.
func termsOf(g *game.Game, me game.Player) Terms {
	var a analysis
	a.templates = hintParams().templates
	a.load(g)
	return a.terms(me)
}

// state is everything about a game that analysing it must leave alone.
type state struct {
	digest  string
	record  string
	turn    game.Player
	ply     int
	result  game.Result
	staged  bool
	entries int
}

func stateOf(t testing.TB, g *game.Game) state {
	t.Helper()
	rec, err := g.Record()
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return state{
		digest:  game.PositionDigest(g),
		record:  rec.Encode(),
		turn:    g.Turn(),
		ply:     g.Ply(),
		result:  g.Result(),
		staged:  staged(g),
		entries: len(g.History()),
	}
}

func TestAnalyzeRefusesAPositionItCannotRead(t *testing.T) {
	ctx := context.Background()

	t.Run("no game", func(t *testing.T) {
		if _, err := Analyze(ctx, nil, AnalysisOptions{}); err == nil {
			t.Fatal("Analyze accepted a nil game")
		}
	})

	t.Run("a peg placed but not committed", func(t *testing.T) {
		g := quietPosition(t)
		spot := mustPoint(t, "D4")
		if err := g.PlacePeg(spot); err != nil {
			t.Fatalf("PlacePeg(%v): %v", spot, err)
		}
		before := stateOf(t, g)
		if _, err := Analyze(ctx, g, AnalysisOptions{}); !errors.Is(err, ErrStagedTurn) {
			t.Fatalf("Analyze on a staged turn returned %v, want ErrStagedTurn", err)
		}
		if st := g.Staged(); !st.PegPlaced || st.Peg != spot {
			t.Errorf("the refusal did not leave the staged peg alone: %+v", st)
		}
		if got := stateOf(t, g); got != before {
			t.Errorf("the refusal changed the game: %+v, want %+v", got, before)
		}
		// Committing makes the position analysable, which is what the error
		// asks the caller to do.
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("CommitTurn: %v", err)
		}
		if _, err := Analyze(ctx, g, AnalysisOptions{Limits: Limits{Depth: 1, Time: time.Hour}}); err != nil {
			t.Errorf("Analyze on the committed position: %v", err)
		}
	})

	t.Run("a link withdrawn by hand", func(t *testing.T) {
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
		if _, err := Analyze(ctx, g, AnalysisOptions{}); !errors.Is(err, ErrStagedTurn) {
			t.Fatalf("Analyze with a link withdrawn returned %v, want ErrStagedTurn", err)
		}
		if g.HasLink(l) {
			t.Error("the refusal put the withdrawn link back")
		}
		g.AbortTurn()
		if _, err := Analyze(ctx, g, AnalysisOptions{Limits: Limits{Depth: 1, Time: time.Hour}}); err != nil {
			t.Errorf("Analyze after the turn was aborted: %v", err)
		}
	})
}

func TestAnalyzeRefusesLimitsTheSearchCannotHonour(t *testing.T) {
	g := quietPosition(t)
	cases := []struct {
		name   string
		limits Limits
	}{
		{"negative nodes", Limits{Nodes: -1}},
		{"negative depth", Limits{Depth: -1}},
		{"negative time", Limits{Time: -time.Second}},
		{"deeper than the search can hold", Limits{Depth: MaxDepth + 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			observed := false
			out, err := Analyze(context.Background(), g, AnalysisOptions{
				Limits:     c.limits,
				OnComplete: func(AnalysisProgress) { observed = true },
			})
			if err == nil {
				t.Fatalf("Analyze accepted %+v", c.limits)
			}
			if observed {
				t.Error("the refused limits still started a search")
			}
			if out.Recommended != nil || out.Stats != nil {
				t.Errorf("the refusal came back with an answer: %+v", out)
			}
		})
	}
}

// TestAnalyzeReadsAFinishedGameWithoutAdvising covers the one case Analyze
// takes and Hint refuses. A finished game has a position and a result and no
// move to play, so it gets its decomposition and nothing that would imply a
// continuation.
func TestAnalyzeReadsAFinishedGameWithoutAdvising(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		build func(testing.TB) *game.Game
	}{
		{"won by connection", func(t testing.TB) *game.Game {
			g := winThreat(t)
			playMoves(t, g, "C3")
			return g
		}},
		{"won by resignation", func(t testing.TB) *game.Game {
			g := quietPosition(t)
			if err := g.Resign(game.Vertical); err != nil {
				t.Fatalf("Resign: %v", err)
			}
			return g
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := c.build(t)
			if !g.Result().Over() {
				t.Fatalf("fixture is not finished: %+v", g.Result())
			}
			before := stateOf(t, g)
			out, err := Analyze(ctx, g, AnalysisOptions{
				OnComplete: func(AnalysisProgress) { t.Error("a finished game reported a search iteration") },
			})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if out.Result != g.Result() {
				t.Errorf("result = %+v, want %+v", out.Result, g.Result())
			}
			if out.Policy != PlacementOnlyPolicy() {
				t.Errorf("policy = %s, want %s", out.Policy, PlacementOnlyPolicy())
			}
			if want := termsOf(g, g.Turn()); out.Before != want {
				t.Errorf("before-terms = %+v, want %+v", out.Before, want)
			}
			if out.Recommended != nil {
				t.Errorf("a finished game was given a move to play: %v", *out.Recommended)
			}
			if out.After != nil {
				t.Errorf("a finished game was given after-terms: %+v", *out.After)
			}
			if out.Stats != nil {
				t.Errorf("a finished game reported a search: %+v", *out.Stats)
			}
			if out.Candidates == nil || len(out.Candidates) != 0 {
				t.Errorf("candidates = %v, want an empty list", out.Candidates)
			}
			if out.Highlight == nil || len(out.Highlight) != 0 {
				t.Errorf("highlight = %v, want an empty list", out.Highlight)
			}
			if out.Reason != "" || out.Headline != "" || out.Detail != "" {
				t.Errorf("a finished game was explained: %q / %q / %q", out.Reason, out.Headline, out.Detail)
			}
			if got := stateOf(t, g); got != before {
				t.Errorf("reading a finished game changed it: %+v, want %+v", got, before)
			}
			// The divergence the refactor has to keep: the same position is
			// advice Hint will not give.
			if _, err := New(Max, 1).Hint(ctx, g); !errors.Is(err, game.ErrGameOver) {
				t.Errorf("Hint on a finished game returned %v, want ErrGameOver", err)
			}
		})
	}
}

func TestAnalyzeUnderADepthCeilingStopsAtThatDepth(t *testing.T) {
	ctx := context.Background()
	g := quietPosition(t)
	var shallowNodes int64
	for _, depth := range []int{1, 2} {
		out, err := Analyze(ctx, g, AnalysisOptions{Limits: Limits{Depth: depth, Time: time.Hour}})
		if err != nil {
			t.Fatalf("Analyze at depth %d: %v", depth, err)
		}
		if out.Stats == nil {
			t.Fatalf("depth %d reported no search", depth)
		}
		if out.Stats.Depth != depth {
			t.Errorf("depth %d finished iteration %d, want %d (%+v)", depth, out.Stats.Depth, depth, *out.Stats)
		}
		if out.Stats.StopReason != stopDepth {
			t.Errorf("depth %d stopped for %q, want %q", depth, out.Stats.StopReason, stopDepth)
		}
		if out.Stats.Evaluations <= out.Stats.Nodes {
			t.Errorf("depth %d analysed %d positions for %d nodes: the root's own analysis is not counted",
				depth, out.Stats.Evaluations, out.Stats.Nodes)
		}
		if depth == 1 {
			shallowNodes = out.Stats.Nodes
		} else if out.Stats.Nodes <= shallowNodes {
			t.Errorf("deepening from 1 to 2 spent %d nodes against %d: the ceiling is not binding",
				out.Stats.Nodes, shallowNodes)
		}
		if out.Recommended == nil {
			t.Fatal("no move came back from a finished search")
		}
		if err := g.CanPlace(g.Turn(), *out.Recommended); err != nil {
			t.Errorf("recommended %v is illegal: %v", *out.Recommended, err)
		}
		if len(out.Candidates) == 0 {
			t.Fatal("a finished iteration kept no candidates")
		}
		if out.Candidates[0].Move != *out.Recommended {
			t.Errorf("candidates lead with %v, not the recommended %v", out.Candidates[0].Move, *out.Recommended)
		}
		exact := 0
		for _, c := range out.Candidates {
			switch c.Bound {
			case BoundExact:
				exact++
			case BoundUpper:
			default:
				t.Errorf("a finished iteration left %v %s", c.Move, c.Bound)
			}
		}
		if exact == 0 {
			t.Error("a finished iteration measured nothing")
		}
		if out.After == nil {
			t.Fatal("no after-terms came back")
		}
		if want := termsOf(g, g.Turn()); out.Before != want {
			t.Errorf("before-terms = %+v, want %+v", out.Before, want)
		}
		if out.Reason == "" || out.Headline == "" || out.Detail == "" {
			t.Errorf("no explanation came back: %q / %q / %q", out.Reason, out.Headline, out.Detail)
		}
		if len(out.Highlight) == 0 || out.Highlight[0] != *out.Recommended {
			t.Errorf("highlight %v does not start at the recommended move %v", out.Highlight, *out.Recommended)
		}
	}
}

// TestAnalyzeUnderATinyNodeCeilingScoresNothing holds the honesty of the
// interrupted case: a ceiling that stops the first iteration leaves the
// ordering heuristic's list, and none of it was measured.
func TestAnalyzeUnderATinyNodeCeilingScoresNothing(t *testing.T) {
	g := quietPosition(t)
	out, err := Analyze(context.Background(), g, AnalysisOptions{
		Limits:     Limits{Nodes: 1, Time: time.Hour},
		OnComplete: func(AnalysisProgress) { t.Error("an unfinished iteration was reported") },
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if out.Stats == nil {
		t.Fatal("no search was reported")
	}
	if out.Stats.Nodes > 1 {
		t.Errorf("a one-node ceiling let %d nodes through", out.Stats.Nodes)
	}
	if out.Stats.StopReason != stopNodes {
		t.Errorf("stopped for %q, want %q (%+v)", out.Stats.StopReason, stopNodes, *out.Stats)
	}
	if out.Stats.Depth != 0 {
		t.Errorf("reported iteration %d as finished under a one-node ceiling", out.Stats.Depth)
	}
	if out.Recommended == nil {
		t.Fatal("no move came back")
	}
	if err := g.CanPlace(g.Turn(), *out.Recommended); err != nil {
		t.Errorf("recommended %v is illegal: %v", *out.Recommended, err)
	}
	if len(out.Candidates) == 0 {
		t.Fatal("no candidates came back")
	}
	for _, c := range out.Candidates {
		if c.Bound != BoundUnscored {
			t.Errorf("%v is %s after a search that finished no iteration", c.Move, c.Bound)
		}
		if c.Score != 0 {
			t.Errorf("%v carries score %d with nothing having measured it", c.Move, c.Score)
		}
	}
	if out.Reason == "" || out.Headline == "" {
		t.Error("an interrupted search gave no explanation of the move it fell back on")
	}
}

func TestAnalyzeOnACanceledContextFallsBackToOrdering(t *testing.T) {
	g := quietPosition(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := Analyze(ctx, g, AnalysisOptions{
		OnComplete: func(AnalysisProgress) { t.Error("a canceled search reported an iteration") },
	})
	if err != nil {
		t.Fatalf("Analyze on a canceled context: %v", err)
	}
	if out.Stats == nil {
		t.Fatal("no search was reported")
	}
	if out.Stats.StopReason != stopCanceled {
		t.Errorf("stopped for %q, want %q", out.Stats.StopReason, stopCanceled)
	}
	if out.Stats.Depth != 0 {
		t.Errorf("a canceled search reported iteration %d as finished", out.Stats.Depth)
	}
	if out.Recommended == nil {
		t.Fatal("no move came back from a canceled search")
	}
	if err := g.CanPlace(g.Turn(), *out.Recommended); err != nil {
		t.Errorf("recommended %v is illegal: %v", *out.Recommended, err)
	}
	for _, c := range out.Candidates {
		if c.Bound != BoundUnscored {
			t.Errorf("%v is %s after a canceled search", c.Move, c.Bound)
		}
	}
}

func TestAnalyzeLeavesThePositionAsItFoundIt(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		ctx    context.Context
		limits Limits
	}{
		{"a finished search", context.Background(), Limits{Depth: 2, Time: time.Hour}},
		{"a search stopped by its node ceiling", context.Background(), Limits{Nodes: 1, Time: time.Hour}},
		{"a canceled search", canceled, Limits{Time: time.Hour}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := quietPosition(t)
			before := stateOf(t, g)
			if _, err := Analyze(c.ctx, g, AnalysisOptions{Limits: c.limits}); err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if got := stateOf(t, g); got != before {
				t.Errorf("the search left the game changed: %+v, want %+v", got, before)
			}
		})
	}
}

func TestAnalyzeUnderAFixedNodeBudgetRepeatsItself(t *testing.T) {
	ctx := context.Background()
	g := quietPosition(t)
	limits := Limits{Nodes: 30_000, Time: time.Hour}
	first, err := Analyze(ctx, g, AnalysisOptions{Limits: limits})
	if err != nil {
		t.Fatalf("first Analyze: %v", err)
	}
	second, err := Analyze(ctx, g, AnalysisOptions{Limits: limits})
	if err != nil {
		t.Fatalf("second Analyze: %v", err)
	}
	if first.Stats.StopReason != stopNodes {
		t.Fatalf("the node budget did not bind: %+v", *first.Stats)
	}
	if first.Stats.Depth < 1 {
		t.Fatalf("no iteration finished inside the budget: %+v", *first.Stats)
	}
	if *first.Recommended != *second.Recommended {
		t.Errorf("two identical budgets recommended %v and %v", *first.Recommended, *second.Recommended)
	}
	if !slices.Equal(first.Candidates, second.Candidates) {
		t.Errorf("candidate lists differ:\n%+v\n%+v", first.Candidates, second.Candidates)
	}
	if !slices.Equal(first.Highlight, second.Highlight) {
		t.Errorf("highlights differ: %v and %v", first.Highlight, second.Highlight)
	}
	if first.Before != second.Before || *first.After != *second.After {
		t.Errorf("terms differ: %+v/%+v and %+v/%+v", first.Before, *first.After, second.Before, *second.After)
	}
	if first.Reason != second.Reason || first.Headline != second.Headline || first.Detail != second.Detail {
		t.Errorf("explanations differ:\n%q %q\n%q %q", first.Reason, first.Headline, second.Reason, second.Headline)
	}
	// Elapsed measures the machine rather than the work, so it is the one
	// field two runs of the same budget need not agree on.
	a, b := *first.Stats, *second.Stats
	a.Elapsed, b.Elapsed = 0, 0
	if a != b {
		t.Errorf("the same node budget spent %+v and %+v", a, b)
	}
	if a.Nodes > limits.Nodes {
		t.Errorf("spent %d nodes against a ceiling of %d", a.Nodes, limits.Nodes)
	}
}

// TestAnalyzeUpperBoundsAreCeilings is the honesty check on the candidate
// bounds. Every alternative the root reports as an upper bound is measured
// again on its own, in a window that cannot bound the answer, and the bound
// must not sit below the value that measurement finds. Every candidate the
// root reports as exact must equal it.
func TestAnalyzeUpperBoundsAreCeilings(t *testing.T) {
	ctx := context.Background()
	g := quietPosition(t)
	const depth = 2
	p := finiteParams(depth)
	out, _, _, err := analyzeOn(ctx, newSearcher(p), g, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if out.Stats.Depth != depth {
		t.Fatalf("the full-width search finished iteration %d, want %d (%+v)", out.Stats.Depth, depth, *out.Stats)
	}
	if n := len(out.Candidates); n < 10 {
		t.Fatalf("only %d candidates came back, so the width cap is still discarding moves", n)
	}
	uppers, exacts := 0, 0
	for _, c := range out.Candidates {
		if c.Bound == BoundUnscored {
			t.Fatalf("%v is unscored after a finished iteration", c.Move)
		}
		if c.Score >= decidedScore || c.Score <= -decidedScore {
			t.Fatalf("fixture reached a forced result at %v (%d), which the ply-indexed win scores make incomparable", c.Move, c.Score)
		}
		value := valueOfMove(ctx, t, g, c.Move, p)
		switch c.Bound {
		case BoundExact:
			exacts++
			if value != c.Score {
				t.Errorf("%v is reported exact at %d but measures %d", c.Move, c.Score, value)
			}
		case BoundUpper:
			uppers++
			if value > c.Score {
				t.Errorf("%v is bounded above by %d but measures %d", c.Move, c.Score, value)
			}
		case BoundLower:
			t.Errorf("%v came back as a lower bound, which this search never establishes", c.Move)
		}
	}
	if exacts == 0 {
		t.Error("nothing was measured, so the comparison proved nothing")
	}
	if uppers == 0 {
		t.Error("no candidate came back bounded, so the ceiling claim was never exercised")
	}
	t.Logf("%d exact and %d bounded candidates at depth %d", exacts, uppers, depth)
}

// valueOfMove measures one root move on its own: the move is played and the
// position it leaves is searched to the remaining depth with the whole scale
// open, so what comes back is the move's value under those levers and not a
// ceiling.
func valueOfMove(ctx context.Context, t testing.TB, g *game.Game, move game.Point, p params) int {
	t.Helper()
	child := g.Clone()
	if _, err := child.PlayPeg(move); err != nil {
		t.Fatalf("PlayPeg(%v): %v", move, err)
	}
	op := p
	op.maxDepth = p.maxDepth - 1
	res, err := newSearcher(op).root(ctx, child)
	if err != nil {
		t.Fatalf("measuring %v: %v", move, err)
	}
	if res.depth != op.maxDepth {
		t.Fatalf("measuring %v finished iteration %d, want %d", move, res.depth, op.maxDepth)
	}
	return -res.score
}

func TestAnalyzeObservesOnlyFinishedIterations(t *testing.T) {
	ctx := context.Background()

	t.Run("one report per finished iteration, in order", func(t *testing.T) {
		g := quietPosition(t)
		const depth = 3
		var seen []AnalysisProgress
		out, err := Analyze(ctx, g, AnalysisOptions{
			Limits:     Limits{Depth: depth, Time: time.Hour},
			OnComplete: func(p AnalysisProgress) { seen = append(seen, p) },
		})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if out.Stats.Depth < 2 {
			t.Fatalf("only %d iterations finished, which is too few to compare reports: %+v", out.Stats.Depth, *out.Stats)
		}
		if len(seen) != out.Stats.Depth {
			t.Fatalf("%d reports for %d finished iterations", len(seen), out.Stats.Depth)
		}
		for i, p := range seen {
			if p.Stats.Depth != i+1 {
				t.Errorf("report %d carries depth %d", i, p.Stats.Depth)
			}
			if p.Stats.StopReason != "" {
				t.Errorf("report %d says the search stopped for %q", i, p.Stats.StopReason)
			}
			if p.Stats.Elapsed <= 0 {
				t.Errorf("report %d is dated %v: while the search runs, the boundary's own reading is the only elapsed figure there is",
					i, p.Stats.Elapsed)
			}
			if p.Stats.Elapsed > out.Stats.Elapsed {
				t.Errorf("report %d is dated %v, past the %v the whole search took", i, p.Stats.Elapsed, out.Stats.Elapsed)
			}
			if len(p.Candidates) == 0 {
				t.Fatalf("report %d carries no candidates", i)
			}
			if p.Candidates[0].Move != p.Recommended {
				t.Errorf("report %d leads with %v, not its own recommendation %v", i, p.Candidates[0].Move, p.Recommended)
			}
			for _, c := range p.Candidates {
				if c.Bound == BoundUnscored {
					t.Errorf("report %d carries unscored %v from a finished iteration", i, c.Move)
				}
			}
			if err := g.CanPlace(g.Turn(), p.Recommended); err != nil {
				t.Errorf("report %d recommends illegal %v: %v", i, p.Recommended, err)
			}
			if i > 0 {
				if p.Stats.Nodes < seen[i-1].Stats.Nodes {
					t.Errorf("report %d spent fewer nodes than report %d", i, i-1)
				}
				if p.Stats.Elapsed < seen[i-1].Stats.Elapsed {
					t.Errorf("report %d is earlier than report %d", i, i-1)
				}
			}
		}
		last := seen[len(seen)-1]
		if last.Recommended != *out.Recommended {
			t.Errorf("the last report recommends %v and the result %v", last.Recommended, *out.Recommended)
		}
		if !slices.Equal(last.Candidates, out.Candidates) {
			t.Errorf("the last report's candidates differ from the result's:\n%+v\n%+v", last.Candidates, out.Candidates)
		}
		// The depth ceiling ended the search on the iteration that was just
		// reported, so the work the result reports is that iteration's work
		// and nothing more: reading the explanation afterwards is not search.
		if out.Stats.StopReason != stopDepth {
			t.Fatalf("the search stopped for %q rather than on its depth ceiling, so the result need not be the last report's work: %+v",
				out.Stats.StopReason, *out.Stats)
		}
		if out.Stats.Depth != last.Stats.Depth || out.Stats.Nodes != last.Stats.Nodes || out.Stats.Evaluations != last.Stats.Evaluations {
			t.Errorf("the result reports %d nodes and %d analyses at depth %d, the last iteration to finish %d and %d at depth %d",
				out.Stats.Nodes, out.Stats.Evaluations, out.Stats.Depth,
				last.Stats.Nodes, last.Stats.Evaluations, last.Stats.Depth)
		}
	})

	t.Run("a win taken without searching is not an iteration", func(t *testing.T) {
		g := winThreat(t)
		out, err := Analyze(ctx, g, AnalysisOptions{
			OnComplete: func(AnalysisProgress) { t.Error("the immediate win was reported as a finished iteration") },
		})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if out.Stats.StopReason != stopImmediate {
			t.Fatalf("stopped for %q, want %q", out.Stats.StopReason, stopImmediate)
		}
		if out.Stats.Depth != 0 {
			t.Errorf("an unsearched win reported iteration %d", out.Stats.Depth)
		}
		if len(out.Candidates) != 1 {
			t.Fatalf("an unsearched win kept %d candidates", len(out.Candidates))
		}
		c := out.Candidates[0]
		if c.Bound != BoundExact {
			t.Errorf("the winning hole came back %s, want exact", c.Bound)
		}
		if c.Score < decidedScore {
			t.Errorf("the winning hole scores %d, which is not a win", c.Score)
		}
		if *out.Recommended != c.Move {
			t.Errorf("recommended %v against the single candidate %v", *out.Recommended, c.Move)
		}
	})
}

// TestAnalyzeCanceledAtAnIterationBoundaryKeepsThatIteration cancels from
// inside the observer, so the cancellation lands between two iterations rather
// than at a moment the clock picks. What comes back is the iteration that was
// reported, labelled as cancelled: one finished iteration's work, and none of
// the one that never ran. An iteration cut off part-way through is the other
// case, and the node-ceiling checks above hold it.
func TestAnalyzeCanceledAtAnIterationBoundaryKeepsThatIteration(t *testing.T) {
	g := quietPosition(t)
	before := stateOf(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var seen []AnalysisProgress
	out, err := Analyze(ctx, g, AnalysisOptions{
		Limits: Limits{Depth: 3, Time: time.Hour},
		OnComplete: func(p AnalysisProgress) {
			seen = append(seen, p)
			cancel()
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("%d iterations were reported after the first one cancelled the search", len(seen))
	}
	first := seen[0]
	if first.Stats.Depth != 1 {
		t.Errorf("the report carries depth %d, want the first iteration", first.Stats.Depth)
	}
	if first.Stats.StopReason != "" {
		t.Errorf("the iteration was reported as already stopped for %q", first.Stats.StopReason)
	}
	if out.Stats.StopReason != stopCanceled {
		t.Errorf("stopped for %q, want %q (%+v)", out.Stats.StopReason, stopCanceled, *out.Stats)
	}
	if out.Stats.Depth != 1 {
		t.Errorf("the result reports iteration %d as its deepest finished one", out.Stats.Depth)
	}
	if *out.Recommended != first.Recommended {
		t.Errorf("the result recommends %v against the only iteration that finished, %v", *out.Recommended, first.Recommended)
	}
	if !slices.Equal(out.Candidates, first.Candidates) {
		t.Errorf("the result's candidates differ from the only finished iteration's:\n%+v\n%+v", out.Candidates, first.Candidates)
	}
	// A cancellation at the boundary keeps finished work rather than falling
	// back on the ordering heuristic, so the answer has to read as measured.
	exact := 0
	for _, c := range out.Candidates {
		switch c.Bound {
		case BoundExact:
			exact++
		case BoundUpper:
		default:
			t.Errorf("%v is %s after an iteration that finished", c.Move, c.Bound)
		}
	}
	if exact == 0 {
		t.Error("nothing in the kept iteration was measured")
	}
	if out.Stats.Nodes == 0 || out.Stats.Evaluations <= out.Stats.Nodes {
		t.Errorf("the cancelled search reports %d nodes and %d analyses, which is not one finished iteration's work",
			out.Stats.Nodes, out.Stats.Evaluations)
	}
	if got := stateOf(t, g); got != before {
		t.Errorf("the cancelled search left the game changed: %+v, want %+v", got, before)
	}
}

// TestAnalyzeStopsOnTheIterationThatDecidesIt is the one iteration the search
// both finishes and stops on. The win is a move beyond the root policy, so it
// has to be searched for; the iteration that proves it is a finished iteration
// and is reported as one, with no stop reason yet, and the search stops there
// rather than deepening a tree that cannot improve on a forced win.
func TestAnalyzeStopsOnTheIterationThatDecidesIt(t *testing.T) {
	g, killer := mctsWinInTwo(t)
	before := stateOf(t, g)

	var seen []AnalysisProgress
	out, err := Analyze(context.Background(), g, AnalysisOptions{
		// Room for four iterations, so stopping on the first one is the
		// forced win's doing and not the ceiling's.
		Limits:     Limits{Depth: 4, Time: time.Hour},
		OnComplete: func(p AnalysisProgress) { seen = append(seen, p) },
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if out.Stats.StopReason != stopDecided {
		t.Fatalf("stopped for %q, want %q (%+v)", out.Stats.StopReason, stopDecided, *out.Stats)
	}
	if out.Stats.Depth != 1 {
		t.Errorf("the win was proved on iteration %d, want the first", out.Stats.Depth)
	}
	if out.Stats.Nodes == 0 {
		t.Error("the win came back without a node being entered, so it was taken at the root rather than searched for")
	}
	if len(seen) != 1 {
		t.Fatalf("%d iterations were reported by a search decided on its first", len(seen))
	}
	first := seen[0]
	if first.Stats.Depth != 1 {
		t.Errorf("the report carries depth %d, want the first iteration", first.Stats.Depth)
	}
	if first.Stats.StopReason != "" {
		t.Errorf("the deciding iteration was reported as already stopped for %q", first.Stats.StopReason)
	}
	if *out.Recommended != first.Recommended {
		t.Errorf("the result recommends %v against the deciding iteration's %v", *out.Recommended, first.Recommended)
	}
	if !slices.Equal(out.Candidates, first.Candidates) {
		t.Errorf("the result's candidates differ from the deciding iteration's:\n%+v\n%+v", out.Candidates, first.Candidates)
	}
	if len(out.Candidates) == 0 {
		t.Fatal("the deciding iteration kept no candidates")
	}
	if lead := out.Candidates[0]; lead.Move != *out.Recommended || lead.Bound != BoundExact || lead.Score < decidedScore {
		t.Errorf("the list leads with %+v, want the recommended %v measured at a forced win", lead, *out.Recommended)
	}
	// The move the fixture derives for itself, by playing every reply rather
	// than by asking the search: nothing short of a searched iteration can
	// score it as won, because the position it wins from is a move away.
	i := slices.IndexFunc(out.Candidates, func(c AnalysisCandidate) bool { return c.Move == killer })
	if i < 0 {
		t.Fatalf("the unanswerable %v is not among the candidates:\n%+v", killer, out.Candidates)
	}
	if c := out.Candidates[i]; c.Bound == BoundUnscored || c.Score < decidedScore {
		t.Errorf("the unanswerable %v comes back as %+v, so the search did not measure the win", killer, c)
	}
	if err := g.CanPlace(g.Turn(), *out.Recommended); err != nil {
		t.Errorf("recommended %v is illegal: %v", *out.Recommended, err)
	}
	if got := stateOf(t, g); got != before {
		t.Errorf("the search left the game changed: %+v, want %+v", got, before)
	}
}

// TestAnalyzeObserverCannotReachTheSearch is the ownership check on the
// observer's side: a report belongs to the observer, and a report the observer
// has scribbled on changes neither the next report nor what the search
// concludes. The lists it was handed are kept as they came rather than only
// copied, so a search that handed out a buffer it reuses would be caught
// writing over the observer's own scribble.
func TestAnalyzeObserverCannotReachTheSearch(t *testing.T) {
	ctx := context.Background()
	g := quietPosition(t)
	limits := Limits{Depth: 2, Time: time.Hour}
	junk := AnalysisCandidate{Move: mustPoint(t, "A1"), Score: 1 << 20, Bound: BoundLower}

	var arrived, kept [][]AnalysisCandidate
	out, err := Analyze(ctx, g, AnalysisOptions{
		Limits: limits,
		OnComplete: func(p AnalysisProgress) {
			arrived = append(arrived, slices.Clone(p.Candidates))
			kept = append(kept, p.Candidates)
			for i := range p.Candidates {
				p.Candidates[i] = junk
			}
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(arrived) < 2 {
		t.Fatalf("%d reports, which is not enough to check one against the next", len(arrived))
	}
	for i, r := range arrived {
		if len(r) == 0 {
			t.Fatalf("report %d arrived empty, so scribbling on it settled nothing", i)
		}
		if slices.Contains(r, junk) {
			t.Fatalf("report %d arrived carrying the previous report's scribble", i)
		}
	}
	if slices.Contains(out.Candidates, junk) {
		t.Fatal("the result carries the observer's scribble")
	}

	control, err := Analyze(ctx, g, AnalysisOptions{Limits: limits})
	if err != nil {
		t.Fatalf("control Analyze: %v", err)
	}
	if slices.Contains(control.Candidates, junk) {
		t.Fatal("the control result carries the observer's scribble")
	}
	// Nothing but the observer writes to a report it has been given: what the
	// kept lists read back is the scribble and nothing else, whatever the rest
	// of that search and the search after it did.
	for i, r := range kept {
		for j, c := range r {
			if c != junk {
				t.Fatalf("report %d reads %+v at position %d after two further searches, so the search was still writing to the observer's list",
					i, c, j)
			}
		}
	}
	if *out.Recommended != *control.Recommended {
		t.Errorf("the observed search recommended %v against the control's %v", *out.Recommended, *control.Recommended)
	}
	if !slices.Equal(out.Candidates, control.Candidates) {
		t.Errorf("the observed search concluded differently from the control:\n%+v\n%+v", out.Candidates, control.Candidates)
	}
}

// TestAnalysisResultOutlivesTheNextSearch is the ownership check on the
// caller's side. The searcher's root list, per-ply analyses and loaded
// position are scratch it overwrites, so a result handed out of one search must
// survive the next one on the same searcher.
func TestAnalysisResultOutlivesTheNextSearch(t *testing.T) {
	ctx := context.Background()
	s := newSearcher(finiteParams(2))

	first, _, _, err := analyzeOn(ctx, s, quietPosition(t), nil)
	if err != nil {
		t.Fatalf("first analysis: %v", err)
	}
	// Copies of the values behind the pointers as well, so that a later search
	// writing through them would show up here.
	after, move, stats := *first.After, *first.Recommended, *first.Stats
	kept := AnalysisResult{
		Result:      first.Result,
		Policy:      first.Policy,
		Before:      first.Before,
		After:       &after,
		Recommended: &move,
		Candidates:  slices.Clone(first.Candidates),
		Reason:      first.Reason,
		Headline:    first.Headline,
		Detail:      first.Detail,
		Highlight:   slices.Clone(first.Highlight),
		Stats:       &stats,
	}

	other := winThreat(t)
	playMoves(t, other, "F1")
	if _, _, _, err := analyzeOn(ctx, s, other, nil); err != nil {
		t.Fatalf("second analysis: %v", err)
	}

	if *first.Recommended != *kept.Recommended {
		t.Errorf("the first recommendation became %v, was %v", *first.Recommended, *kept.Recommended)
	}
	if !slices.Equal(first.Candidates, kept.Candidates) {
		t.Errorf("the first candidate list changed under a later search:\n%+v\n%+v", first.Candidates, kept.Candidates)
	}
	if !slices.Equal(first.Highlight, kept.Highlight) {
		t.Errorf("the first highlight changed: %v, was %v", first.Highlight, kept.Highlight)
	}
	if first.Before != kept.Before || *first.After != *kept.After {
		t.Errorf("the first terms changed: %+v/%+v, were %+v/%+v", first.Before, *first.After, kept.Before, *kept.After)
	}
	if *first.Stats != *kept.Stats {
		t.Errorf("the first stats changed: %+v, were %+v", *first.Stats, *kept.Stats)
	}
	if first.Reason != kept.Reason || first.Headline != kept.Headline || first.Detail != kept.Detail {
		t.Error("the first explanation changed under a later search")
	}
}

// TestAnalyzeDefaultsAreTheHintsOwn checks what a zero Limits inherits: the
// same levers a hint searches under, with only the fields the caller stated
// replaced.
func TestAnalyzeDefaultsAreTheHintsOwn(t *testing.T) {
	ctx := context.Background()

	t.Run("the levers a zero Limits keeps", func(t *testing.T) {
		g := quietPosition(t)
		const depth = 2
		want, _, _, err := analyzeOn(ctx, newSearcher(hintLevers(depth)), g, nil)
		if err != nil {
			t.Fatalf("reference analysis: %v", err)
		}
		got, err := Analyze(ctx, g, AnalysisOptions{Limits: Limits{Depth: depth, Time: time.Hour}})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if *got.Recommended != *want.Recommended {
			t.Errorf("recommended %v, the hint's levers recommend %v", *got.Recommended, *want.Recommended)
		}
		if !slices.Equal(got.Candidates, want.Candidates) {
			t.Errorf("candidates differ from the hint's levers:\n%+v\n%+v", got.Candidates, want.Candidates)
		}
		a, b := *got.Stats, *want.Stats
		a.Elapsed, b.Elapsed = 0, 0
		if a != b {
			t.Errorf("the search spent %+v against the hint's levers' %+v", a, b)
		}
	})

	t.Run("the clock a zero Limits keeps", func(t *testing.T) {
		// An empty 24x24 board: the default depth ceiling is 24 plies and no
		// machine reaches it here, so what ends the search is the guard.
		g := game.MustNew(smallRules(24))
		out, err := Analyze(ctx, g, AnalysisOptions{})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if out.Stats.StopReason != stopTime {
			t.Fatalf("a 24x24 opening stopped for %q rather than on the clock: %+v", out.Stats.StopReason, *out.Stats)
		}
		if out.Stats.Elapsed > 5*time.Second {
			t.Errorf("the default guard let the search run for %v, which is not the hint's two seconds", out.Stats.Elapsed)
		}
	})

	t.Run("a hint is the same analysis", func(t *testing.T) {
		g := quietPosition(t)
		const depth = 2
		// A beginner-tier bot, because a hint is answered with the package's
		// highest-effort levers whatever tier is playing. Its hint searcher is
		// taken beforehand and given the depth and the clock that make two
		// searches comparable, and nothing else: the width, the evaluation and
		// the table stay whatever Hint runs on, so a hint searching the
		// playing tier's levers shows up in the work it did.
		e := New(Beginner, 7).(*engine)
		s := e.hintSearcher()
		s.p.maxDepth, s.p.budget = depth, time.Hour
		h, err := e.Hint(ctx, g)
		if err != nil {
			t.Fatalf("Hint: %v", err)
		}
		out, err := Analyze(ctx, g, AnalysisOptions{Limits: Limits{Depth: depth, Time: time.Hour}})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if h.Move != *out.Recommended {
			t.Errorf("Hint plays %v, Analyze %v", h.Move, *out.Recommended)
		}
		if h.Headline != out.Headline || h.Detail != out.Detail {
			t.Errorf("prose differs:\n%q / %q\n%q / %q", h.Headline, h.Detail, out.Headline, out.Detail)
		}
		if !slices.Equal(h.Highlight, out.Highlight) {
			t.Errorf("highlights differ: %v and %v", h.Highlight, out.Highlight)
		}
		if h.Policy != out.Policy {
			t.Errorf("policies differ: %s and %s", h.Policy, out.Policy)
		}
		// One position searched twice the same way. Without this the prose
		// checks would pass on a hint that reached the same sentence from a
		// different search; the elapsed time is the machine rather than the
		// work, and the only field two searches need not agree on.
		a, b := s.stats(), *out.Stats
		a.Elapsed, b.Elapsed = 0, 0
		if a != b {
			t.Errorf("the hint spent %+v against the analysis' %+v", a, b)
		}
	})
}

// TestAnalyzeDoesNotDisturbMoveStats holds the separation StatsOf promises: it
// reports the bot's own last move search, and neither a hint nor an analysis
// is one.
//
// The move search and the two requests are given deliberately different work:
// the move is a node-bounded search of a quiet position, and the requests read
// a position with a win already on the board, which is answered without
// entering a node. A hint answered on the playing searcher would then show up
// as the work it did and not as two readings of a clock.
func TestAnalyzeDoesNotDisturbMoveStats(t *testing.T) {
	ctx := context.Background()
	b, err := NewWithLimits(Max, 1, Limits{Nodes: 5_000, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	if _, err := b.Move(ctx, quietPosition(t)); err != nil {
		t.Fatalf("Move: %v", err)
	}
	before := StatsOf(b)
	if before.StopReason != stopNodes || before.Nodes == 0 || before.Depth == 0 {
		t.Fatalf("the move search spent %+v, want a node-bounded search with an iteration behind it", before)
	}

	g := winThreat(t)
	out, err := Analyze(ctx, g, AnalysisOptions{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if out.Stats == nil {
		t.Fatal("Analyze reported no search of its own")
	}
	// Its own search, and one that is nothing like the move's.
	if out.Stats.StopReason != stopImmediate || out.Stats.Nodes != 0 {
		t.Errorf("Analyze reports %+v, want the win it was handed taken at the root", *out.Stats)
	}
	if got := StatsOf(b); got != before {
		t.Fatalf("Analyze changed StatsOf to %+v, want the move search's %+v", got, before)
	}
	if _, err := b.Hint(ctx, g); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if got := StatsOf(b); got != before {
		t.Errorf("StatsOf now reports %+v, want the move search's %+v", got, before)
	}
}
