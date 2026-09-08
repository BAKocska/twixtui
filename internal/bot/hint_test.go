package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
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
		// going through the search, and check the hint's own numbers. A hint
		// is answered with the highest-effort levers, so the reference
		// analyses have to read bottlenecks with the same lever the hint's
		// search used.
		var before, after analysis
		before.templates = hintParams().templates
		after.templates = before.templates
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
		// Every hint the package hands out states the restriction it was
		// produced under. An unstated policy on a real hint would let the
		// prose read as a claim about legal play.
		if h.Policy != PlacementOnlyPolicy() {
			t.Fatalf("hint for %v states policy %s, want %s", h.Move, h.Policy, PlacementOnlyPolicy())
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
			// Won is set, so the distance is the only thing left to reject:
			// without it this case would be turned away for having no result
			// behind it and would stop exercising the arithmetic.
			name: "win claimed with the chain unfinished",
			r:    reasonWin,
			d:    deltas{Before: Terms{Dist: 3}, After: Terms{Dist: 2}, Won: true},
		},
		{
			name: "win claimed with a finished chain but no result to show for it",
			r:    reasonWin,
			d:    deltas{Before: Terms{Dist: 1, OppDist: 4}, After: Terms{Dist: 0, OppDist: 4}},
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
			d:    deltas{Before: Terms{Dist: 4, OppDist: 4}, After: Terms{Dist: NoChain, OppDist: 3}, Won: true},
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
			Won:        src.IntN(2) == 0,
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
	if !d.Won {
		t.Error("the win was claimed with no result from replaying the move behind it")
	}
	// The result the claim rests on, read independently of the deltas.
	next := g.Clone()
	res, err := next.PlayPeg(h.Move)
	if err != nil {
		t.Fatalf("replaying %v: %v", h.Move, err)
	}
	if res.Winner() != g.Turn() {
		t.Errorf("the recommended move ends the game as %+v, which is not a win for %s", res, g.Turn())
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
	t.Logf("headline: %s", h.Headline)
	t.Logf("detail:   %s", h.Detail)
}

type hintCancelOnPoll struct {
	context.Context
	cancel context.CancelFunc
	calls  int
}

func (c *hintCancelOnPoll) Err() error {
	c.calls++
	if c.calls == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestHintDoesNotCountAnInterruptedDefencePrefix(t *testing.T) {
	g := game.MustNew(smallRules(8))
	playMoves(t, g, "B1", "A3", "B5", "G3", "C7", "G4", "E8", "G5", "F1")
	defended := 0
	for _, p := range g.LegalPlacements(g.Turn()) {
		next := g.Clone()
		if _, err := next.PlayPeg(p); err != nil {
			t.Fatal(err)
		}
		wins := false
		if !next.Result().Over() {
			for _, reply := range next.LegalPlacements(next.Turn()) {
				leaf := next.Clone()
				r, err := leaf.PlayPeg(reply)
				if err != nil {
					t.Fatal(err)
				}
				if r.Winner() == next.Turn() {
					wins = true
					break
				}
			}
		}
		if !wins {
			defended++
		}
	}
	if defended < 2 {
		t.Fatalf("fixture only has %d defences", defended)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &hintCancelOnPoll{Context: parent, cancel: cancel}
	e := New(Pro, 1).(*engine)
	before := game.PositionDigest(g)
	h, r, d, err := e.explain(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if game.PositionDigest(g) != before {
		t.Fatal("hint changed position")
	}
	if r == reasonOnlyDefence || d.Defences == 1 {
		t.Fatalf("counted incomplete prefix as exact: actual=%d reported=%d reason=%v hint=%+v", defended, d.Defences, r, h)
	}
	if d.Defences != -1 {
		t.Fatalf("the interrupted enumeration reported %d rather than the marker", d.Defences)
	}
	if r != reasonDefence {
		t.Fatalf("fixture never exercised the defence-count prose: reason=%v", r)
	}
	// Recognise counts independently of defencePhrase, including the -1
	// interrupted marker. Route lengths in pegs are not defence counts.
	count := regexp.MustCompile(`(?i)(?:\b(?:one|only|single)|-?\d+)\s+(?:peg placement|repl(?:y|ies)|answer|defen[cs]e)`)
	if count.MatchString(h.Detail) {
		t.Fatalf("interrupted enumeration emitted a defence count: %q", h.Detail)
	}
	known := d
	known.Defences = defended
	_, counted := describe(reasonDefence, known, g.Turn(), h.Move)
	if !count.MatchString(counted) {
		t.Fatalf("positive control did not expose the completed enumeration's count: %q", counted)
	}
}

// hintPoint reads a hole by the name a player types, so a fixture and the moves
// it is checked against read the way the board does.
func hintPoint(t testing.TB, name string) game.Point {
	t.Helper()
	p, err := game.ParsePoint(name)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return p
}

// rm26DeadlockFixture builds a committed position both sides read as having no
// route left, in a game that is not over and that Vertical wins in one turn.
//
// Every peg is placed and then stripped of the links that placement offered,
// and two links are put back by hand on the last vertical turn. Both halves are
// ordinary play: under the printed rules the mover chooses which of the offered
// links to keep, and may join two of their own pegs already on the board. So
// this is a position a game arrives at by being played rather than one edited
// into existence afterwards, and what it leaves behind is exactly what the
// evaluation's sweep refuses to travel along — own pegs a knight's move apart
// with no link between them.
//
// The move list is fixed rather than searched for: the fixture exists for one
// specific pair of readings, NoChain for both sides at once, and a fixture that
// went looking for it would be a different test every run.
func rm26DeadlockFixture(t testing.TB) *game.Game {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	declineEveryLink := func(name string) {
		t.Helper()
		p := hintPoint(t, name)
		if err := g.PlacePeg(p); err != nil {
			t.Fatalf("place %s: %v", name, err)
		}
		for d := range game.Dir(game.NumDirs) {
			if g.LinkMask(p)&(1<<d) == 0 {
				continue
			}
			if err := g.RemoveLink(p, p.Add(d)); err != nil {
				t.Fatalf("decline the %s link at %s: %v", d, name, err)
			}
		}
	}
	commit := func(who string, turn int) {
		t.Helper()
		res, err := g.CommitTurn()
		if err != nil {
			t.Fatalf("%s turn %d: %v", who, turn, err)
		}
		if res.Over() {
			t.Fatalf("%s turn %d ended the game as %+v", who, turn, res)
		}
	}
	vertical := []string{"B1", "C1", "D1", "E1", "B6", "C6", "D6", "E6", "B2", "C2", "D2", "B3", "C3", "C4", "B5"}
	horizontal := []string{"A2", "A3", "A4", "A5", "F2", "F3", "F4", "F5", "D3", "B4", "D4", "E4", "C5", "D5", "E5"}
	for i := range vertical {
		declineEveryLink(vertical[i])
		if i == len(vertical)-1 {
			for _, pair := range [][2]string{{"B1", "C3"}, {"B5", "D6"}} {
				if err := g.AddLink(hintPoint(t, pair[0]), hintPoint(t, pair[1])); err != nil {
					t.Fatalf("join %s and %s: %v", pair[0], pair[1], err)
				}
			}
		}
		commit("vertical", i)
		declineEveryLink(horizontal[i])
		commit("horizontal", i)
	}
	return g
}

// TestHintDoesNotCallAPlacementDeadlockADraw is RM-26's counterexample, and the
// defect it stands on was released: on this position the hint read "the game is
// already drawn ... no further play can win it", while Vertical wins from it in
// one legal turn.
//
// Both sides read NoChain because the evaluation counts placements and refuses
// to travel between two occupied holes with no link between them. That refusal
// is right about placements and says nothing about the game: Vertical plays the
// compulsory peg and joins two pegs already down, and the game ends there. The
// test holds three things together — that the position is one Hint accepts and
// leaves alone, that the winning turn is legal, and that the prose therefore
// claims nothing unconditional — because any one of them alone would let the
// misleading wording back in.
func TestHintDoesNotCallAPlacementDeadlockADraw(t *testing.T) {
	g := rm26DeadlockFixture(t)
	if res := g.Result(); res.Over() {
		t.Fatalf("the fixture is a finished game: %+v", res)
	}
	if g.Turn() != game.Vertical {
		t.Fatalf("expected vertical to move, got %s", g.Turn())
	}
	var a analysis
	a.load(g)
	mine, theirs := a.terms(game.Vertical), a.terms(game.Horizontal)
	if mine.Dist != NoChain || theirs.Dist != NoChain {
		t.Fatalf("the fixture no longer reads as a deadlock: vertical %+v, horizontal %+v", mine, theirs)
	}

	// A committed position with links chosen by hand is a position to advise
	// on, not one to refuse: the refusal is for a turn still in progress.
	before, err := g.Record()
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	e := New(Max, 1).(*engine)
	ctx := context.Background()
	if _, err := e.Hint(ctx, g); err != nil {
		t.Fatalf("Hint refused a committed hand-linked position: %v", err)
	}
	h, r, d, err := e.explain(ctx, g)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	after, err := g.Record()
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if after != before {
		t.Fatalf("asking for advice changed the game record:\n%s\n%s", before.Encode(), after.Encode())
	}
	if r != reasonDeadlock {
		t.Fatalf("reason = %v, want deadlock: the fixture no longer exercises the branch (deltas %+v)", r, d)
	}

	// The counterexample: one legal turn, the compulsory peg plus a link
	// between two pegs already down, wins for the side that reads NoChain.
	proof := g.Clone()
	if err := proof.PlacePeg(hintPoint(t, "E2")); err != nil {
		t.Fatalf("place E2: %v", err)
	}
	if err := proof.AddLink(hintPoint(t, "C3"), hintPoint(t, "B5")); err != nil {
		t.Fatalf("join C3 and B5: %v", err)
	}
	res, err := proof.CommitTurn()
	if err != nil {
		t.Fatalf("commit the winning turn: %v", err)
	}
	if res.Winner() != game.Vertical {
		t.Fatalf("the winning turn no longer wins: %+v", res)
	}

	// So no unconditional verdict may appear in the prose. A policy badge
	// elsewhere on the screen cannot rescue a headline that says the game is
	// over.
	for _, text := range []string{h.Headline, h.Detail} {
		low := strings.ToLower(text)
		for _, claim := range []string{"already drawn", "the game is drawn", "no further play", "for good", "cannot win", "impossible"} {
			if strings.Contains(low, claim) {
				t.Errorf("prose %q claims %q of a position that is won in one legal turn", text, claim)
			}
		}
	}
	if !strings.Contains(h.Headline, "placement-only") {
		t.Errorf("the deadlock headline does not scope its no-route claim: %q", h.Headline)
	}
	if h.Policy != PlacementOnlyPolicy() {
		t.Errorf("hint states policy %s, want %s", h.Policy, PlacementOnlyPolicy())
	}
	t.Logf("headline: %s", h.Headline)
	t.Logf("detail:   %s", h.Detail)
	t.Logf("record:   %s", before.Encode())
}

func TestHintDoesNotDenyAnActualDraw(t *testing.T) {
	g := rm26DeadlockFixture(t)
	if _, err := g.PlayPeg(hintPoint(t, "E2")); err != nil {
		t.Fatal(err)
	}
	h, r, _, err := New(Max, 1).(*engine).explain(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if r != reasonDeadlock {
		t.Fatalf("fixture missed the no-route explanation: %v", r)
	}
	next := g.Clone()
	result, err := next.PlayPeg(h.Move)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != game.Draw || result.Reason != game.NoMovesLeft {
		t.Fatalf("the recommended last peg did not really draw: %+v", result)
	}
	if strings.Contains(h.Detail, "not a drawn game") {
		t.Fatalf("advice denies the actual result of its recommended move: %q", h.Detail)
	}
}

func TestPPDefenceAdviceDoesNotOfferForbiddenLinkEdits(t *testing.T) {
	g := game.MustNew(smallRules(8))
	playMoves(t, g, "B1", "G2", "B5", "G3", "C7", "G4", "E8", "G5", "F1")
	if err := g.AddLink(hintPoint(t, "G2"), hintPoint(t, "E3")); err == nil {
		t.Fatal("the PP fixture unexpectedly permits deliberate links")
	}
	h, r, _, err := New(Max, 1).(*engine).explain(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if r != reasonOnlyDefence {
		t.Fatalf("fixture missed the only-defence explanation: %v", r)
	}
	if !strings.Contains(h.Headline, "placement-only") {
		t.Fatalf("defence count is not scoped to placements: %q", h.Headline)
	}
	if strings.Contains(h.Detail, "A turn may edit a link") {
		t.Fatalf("PP advice offers an action its rules forbid: %q", h.Detail)
	}
}
