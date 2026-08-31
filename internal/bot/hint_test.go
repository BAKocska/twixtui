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

func hintEngine(budget time.Duration) *engine {
	p := hintParams()
	p.budget = budget
	return &engine{tier: Pro, seed: 1, p: tierParams(Pro), hint: newSearcher(p)}
}

// TestHintReasonMatchesDecomposition is the truthfulness check. For each
// position the hint is asked for, the decomposition is recomputed from the
// engine independently of the search, and the reason the prose asserts must be
// the reason those numbers support.
func TestHintReasonMatchesDecomposition(t *testing.T) {
	src := rand.New(rand.NewPCG(31, 32))
	ctx := context.Background()
	e := hintEngine(40 * time.Millisecond)
	seen := map[reason]int{}
	for range 60 {
		g := randomGame(t, smallRules(8), 4+src.IntN(40), src)
		if g.Result().Over() {
			continue
		}
		me := g.Turn()
		h, r, d, err := e.explain(ctx, g)
		if err != nil {
			t.Fatalf("explain: %v\n%s", err, g)
		}
		seen[r]++

		if err := g.CanPlace(me, h.Move); err != nil {
			t.Fatalf("hint recommends illegal %v: %v\n%s", h.Move, err, g)
		}

		// Recompute the two decompositions straight from the engine, without
		// going through the search, and check the hint's own numbers.
		var before, after analysis
		before.load(g)
		next := g.Clone()
		if _, err := next.PlayPeg(h.Move); err != nil {
			t.Fatalf("PlayPeg %v: %v", h.Move, err)
		}
		after.load(next)
		if got := before.terms(me); got != d.Before {
			t.Fatalf("hint claims before-terms %+v, engine says %+v\n%s", d.Before, got, g)
		}
		if got := after.terms(me); got != d.After {
			t.Fatalf("hint claims after-terms %+v, engine says %+v\n%s", d.After, got, g)
		}
		if err := verifyReason(r, d); err != nil {
			t.Fatalf("hint reason %v is not supported by the decomposition: %v\n%+v\n%s", r, err, d, g)
		}
		if r != reasonBalanced && r != chooseReason(d) {
			t.Fatalf("hint reason %v disagrees with the priority order, which picks %v\n%+v",
				r, chooseReason(d), d)
		}
		if h.Headline == "" || h.Detail == "" {
			t.Fatalf("hint for %v has empty prose", h.Move)
		}
		if !strings.Contains(h.Headline, h.Move.String()) {
			t.Fatalf("headline %q does not name the move %v", h.Headline, h.Move)
		}
		if len(h.Highlight) == 0 || h.Highlight[0] != h.Move {
			t.Fatalf("highlight %v does not start at the recommended move %v", h.Highlight, h.Move)
		}
	}
	if len(seen) < 3 {
		t.Fatalf("only %d distinct reasons fired across the sample: %v", len(seen), seen)
	}
	t.Logf("reasons fired: %v", reasonCounts(seen))
}

func reasonCounts(m map[reason]int) map[string]int {
	out := map[string]int{}
	for r, n := range m {
		out[r.String()] = n
	}
	return out
}

// TestVerifyReasonRejectsWrongClaims exercises the guard directly. Each case
// pairs a reason with a decomposition that does not support it, which is
// exactly what a drifting template would produce.
func TestVerifyReasonRejectsWrongClaims(t *testing.T) {
	cases := []struct {
		name string
		r    reason
		d    deltas
	}{
		{
			name: "win claimed with the chain unfinished",
			r:    reasonWin,
			d:    deltas{Before: Terms{Dist: 3}, After: Terms{Dist: 2}},
		},
		{
			name: "only defence claimed with no threat",
			r:    reasonOnlyDefence,
			d:    deltas{Before: Terms{Dist: 3, OppDist: 4}, After: Terms{Dist: 3, OppDist: 4}, Defences: 1},
		},
		{
			name: "only defence claimed with several defences",
			r:    reasonOnlyDefence,
			d: deltas{Before: Terms{Dist: 3, OppDist: 1}, After: Terms{Dist: 3, OppDist: 3},
				Threatened: true, Defences: 4},
		},
		{
			name: "defence claimed while the winning hole stays open",
			r:    reasonDefence,
			d: deltas{Before: Terms{Dist: 3, OppDist: 1}, After: Terms{Dist: 3, OppDist: 1},
				Threatened: true, Defences: 2},
		},
		{
			name: "block claimed when nothing was blocked",
			r:    reasonBlock,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: 3, OppDist: 4}},
		},
		{
			name: "block claimed when advancing dominates",
			r:    reasonBlock,
			d:    deltas{Before: Terms{Dist: 5, OppDist: 4}, After: Terms{Dist: 3, OppDist: 5}},
		},
		{
			name: "advance claimed when the distance did not fall",
			r:    reasonAdvance,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: 4, OppDist: 6}},
		},
		{
			name: "advance claimed when blocking dominates",
			r:    reasonAdvance,
			d:    deltas{Before: Terms{Dist: 5, OppDist: 4}, After: Terms{Dist: 4, OppDist: 7}},
		},
		{
			name: "loosened plan claimed while the distance fell",
			r:    reasonSetup,
			d: deltas{Before: Terms{Dist: 5, OppDist: 4, Bottlenecks: 3},
				After: Terms{Dist: 4, OppDist: 4, Bottlenecks: 0}},
		},
		{
			name: "loosened plan claimed with no bottleneck removed",
			r:    reasonSetup,
			d: deltas{Before: Terms{Dist: 5, OppDist: 4, Bottlenecks: 1},
				After: Terms{Dist: 5, OppDist: 4, Bottlenecks: 4}},
		},
		{
			name: "win claimed where no route is left at all",
			r:    reasonWin,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: NoChain, OppDist: 3}},
		},
		{
			name: "opponent claimed shut out while they still have a route",
			r:    reasonSeal,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: 4, OppDist: 5}},
		},
		{
			name: "own side claimed shut out while a route remains",
			r:    reasonSealedOut,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: 6, OppDist: 3}},
		},
		{
			name: "deadlock claimed with one side still able to connect",
			r:    reasonDeadlock,
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: NoChain, OppDist: 3}},
		},
		{
			name: "block claimed where one side has no route to measure",
			r:    reasonBlock,
			d:    deltas{Before: Terms{Dist: 4, OppDist: NoChain}, After: Terms{Dist: 4, OppDist: NoChain}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := verifyReason(c.r, c.d); err == nil {
				t.Fatalf("verifyReason accepted %v for %+v", c.r, c.d)
			}
		})
	}
}

// TestChooseReasonAlwaysVerifies is the invariant that keeps the two halves of
// the guard honest: whatever the numbers, the reason the priority order picks
// must be one the checker agrees with.
func TestChooseReasonAlwaysVerifies(t *testing.T) {
	src := rand.New(rand.NewPCG(33, 34))
	for range 20000 {
		d := deltas{
			Before: Terms{
				Dist:           src.IntN(10) - 1,
				OppDist:        src.IntN(10) - 1,
				Bottlenecks:    src.IntN(8),
				OppBottlenecks: src.IntN(8),
				Ground:         src.IntN(2001) - 1000,
			},
			After: Terms{
				Dist:           src.IntN(10) - 1,
				OppDist:        src.IntN(10) - 1,
				Bottlenecks:    src.IntN(8),
				OppBottlenecks: src.IntN(8),
				Ground:         src.IntN(2001) - 1000,
			},
			Threatened: src.IntN(2) == 0,
			Defences:   src.IntN(4),
		}
		r := chooseReason(d)
		if err := verifyReason(r, d); err != nil {
			t.Fatalf("chooseReason picked %v for %+v but the checker rejects it: %v", r, d, err)
		}
		// Whatever the numbers, the prose must never print the value that means
		// "no route at all" as if it were a count.
		headline, detail := describe(r, d, game.Vertical, game.Point{Col: 3, Row: 4})
		for _, text := range []string{headline, detail} {
			if strings.Contains(text, "-1 peg") || strings.Contains(text, fmt.Sprint(blockedDist)) {
				t.Fatalf("prose leaked a sentinel: %q (reason %v, deltas %+v)", text, r, d)
			}
		}
	}
}

// TestHintOnAWinCallsItAWin checks the top of the priority order end to end.
func TestHintOnAWinCallsItAWin(t *testing.T) {
	g := winThreat(t)
	e := hintEngine(200 * time.Millisecond)
	h, r, d, err := e.explain(context.Background(), g)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if r != reasonWin {
		t.Fatalf("reason = %v, want win (deltas %+v)", r, d)
	}
	if h.Move.String() != "C3" {
		t.Errorf("hint move = %v, want C3", h.Move)
	}
	if !strings.Contains(h.Detail, "chain") {
		t.Errorf("detail %q does not describe the completed chain", h.Detail)
	}
	t.Logf("headline: %s", h.Headline)
	t.Logf("detail:   %s", h.Detail)
	t.Logf("marks:    %v", h.Highlight)
}

// TestHintOnAThreatCallsItTheOnlyDefence checks the forced-defence branch, and
// that the claim of being the only defence is only made when it is true.
func TestHintOnAThreatCallsItTheOnlyDefence(t *testing.T) {
	g := winThreat(t)
	playMoves(t, g, "F1")
	if g.Turn() != game.Horizontal {
		t.Fatalf("expected Horizontal to move, got %s", g.Turn())
	}
	e := hintEngine(300 * time.Millisecond)
	h, r, d, err := e.explain(context.Background(), g)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if r != reasonOnlyDefence && r != reasonDefence {
		t.Fatalf("reason = %v, want a defence (deltas %+v)", r, d)
	}
	if !d.Threatened {
		t.Error("the decomposition does not record the threat")
	}
	if d.After.OppDist < 2 {
		t.Errorf("the recommended move leaves Vertical needing %d peg", d.After.OppDist)
	}
	if r == reasonOnlyDefence && d.Defences != 1 {
		t.Errorf("claimed the only defence with %d defences available", d.Defences)
	}
	if !strings.Contains(strings.ToLower(h.Detail), "vertical") {
		t.Errorf("detail %q does not name the threatening side", h.Detail)
	}
	t.Logf("headline: %s", h.Headline)
	t.Logf("detail:   %s", h.Detail)
	t.Logf("marks:    %v", h.Highlight)
}

// TestFindSetupNeedsTwoLiveCarriers checks the setup detection the prose leans
// on: a gap is only called a setup when both of its carriers are genuinely
// still available.
func TestFindSetupNeedsTwoLiveCarriers(t *testing.T) {
	// Vertical pegs at C2 and D5 sit a 1-3 gap apart, joined either through
	// D4 or through E3... the carriers are computed, so read them off.
	g := game.MustNew(smallRules(10))
	playMoves(t, g, "C2", "H2", "D5", "H3")
	var a analysis
	a.load(g)
	partner, carriers, gap, ok := findSetup(&a, game.Vertical, game.Point{Col: 3, Row: 4})
	if !ok {
		t.Fatalf("no setup found for D5 next to C2")
	}
	if len(carriers) < 2 {
		t.Fatalf("setup reported with %d carriers", len(carriers))
	}
	t.Logf("D5 forms a %s gap with %v through %v", gap, partner, carriers)

	// Fill one carrier with an opponent peg and the setup must stop being one,
	// because a single block would now break the link.
	blocked := g.Clone()
	if err := blocked.PlayNotation(carriers[0].String()); err != nil {
		t.Fatalf("PlayNotation(%v): %v", carriers[0], err)
	}
	var b analysis
	b.load(blocked)
	if _, again, _, still := findSetup(&b, game.Vertical, game.Point{Col: 3, Row: 4}); still && len(again) >= 2 {
		// Another partner may legitimately provide a different setup; only a
		// setup through the blocked carrier is wrong.
		for _, c := range again {
			if c == carriers[0] {
				t.Fatalf("carrier %v is occupied but still counted", c)
			}
		}
	}
}

func TestHintHonoursDeadline(t *testing.T) {
	g := randomGame(t, smallRules(24), 24, rand.New(rand.NewPCG(35, 36)))
	b := New(Pro, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	h, err := b.Hint(ctx, g)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if err := g.CanPlace(g.Turn(), h.Move); err != nil {
		t.Fatalf("hint move %v is illegal: %v", h.Move, err)
	}
	if took > 300*time.Millisecond {
		t.Errorf("Hint took %v for a 50ms deadline", took)
	}
}

func TestPegsPhraseNeverPrintsTheSentinel(t *testing.T) {
	if got := pegsPhrase(NoChain); got != "no route at all" {
		t.Errorf("pegsPhrase(NoChain) = %q", got)
	}
	for _, d := range []int{NoChain, 0, 1, 2, 17} {
		if got := pegsPhrase(d); got == "" {
			t.Errorf("pegsPhrase(%d) is empty", d)
		}
	}
}

// TestHintOnASealedPositionSaysSo is a regression test for a real defect: in a
// position where one side has been walled out of the board altogether, the
// "no route" marker reached the player as a peg count, so the hint offered
// advice about a "144-peg route" on a 12x12 board.
//
// The position is genuine: horizontal's chain A6-C5-E6-G7-I6-K5 runs from its
// own left border across to column K, and since vertical may only use columns
// B to K and a link spans at most two columns, no link of vertical's can get
// round the far end of that chain without crossing it.
func TestHintOnASealedPositionSaysSo(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	playMoves(t, g, "C1", "A6", "E2", "C5", "D4", "E6", "F5", "G7", "E7", "I6", "G8", "K5")
	if g.Turn() != game.Vertical {
		t.Fatalf("expected vertical to move, got %s", g.Turn())
	}

	var a analysis
	a.load(g)
	if got := a.need[sideIndex(game.Vertical)]; got != NoChain {
		t.Fatalf("vertical need = %d, want NoChain: the fixture no longer seals it out", got)
	}
	if got := a.need[sideIndex(game.Horizontal)]; got != 1 {
		t.Fatalf("horizontal need = %d, want 1", got)
	}

	e := hintEngine(200 * time.Millisecond)
	h, r, d, err := e.explain(context.Background(), g)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if r != reasonSealedOut {
		t.Errorf("reason = %v, want sealed-out (deltas %+v)", r, d)
	}
	for _, text := range []string{h.Headline, h.Detail} {
		for _, bad := range []string{"144", fmt.Sprint(blockedDist), "-1 peg"} {
			if strings.Contains(text, bad) {
				t.Errorf("hint prose %q contains %q, which is a marker and not a peg count", text, bad)
			}
		}
	}
	if !strings.Contains(h.Detail, "seal") {
		t.Errorf("detail %q does not say the routes are sealed", h.Detail)
	}
	t.Logf("headline: %s", h.Headline)
	t.Logf("detail:   %s", h.Detail)
}
