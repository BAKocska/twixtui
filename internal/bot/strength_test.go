package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// The strength harness plays the tiers against each other under the discipline
// the Hex tournament literature uses, because without it a measured gap means
// very little:
//
//   - Every pairing is colour balanced: each opening is played twice, once with
//     each tier moving first.
//   - The swap option is off. Which side benefits from swapping is a confound
//     on which tier is to move, so the opening is varied explicitly instead.
//   - The opening is varied rather than the seed. Two of the three tiers play
//     deterministically, so without opening variation the same game would be
//     replayed N times; with it, every game is a different, reproducible game.
//   - Draws score a half, the usual tournament convention, and the win rate is
//     converted to an Elo gap with the standard logistic relation.

// matchConfig describes one tier-vs-tier match.
type matchConfig struct {
	a, b       Tier
	size       int
	openings   int
	budgets    [3]time.Duration
	workers    int
	trackDepth bool
}

type matchResult struct {
	cfg            matchConfig
	games          int
	aWins, bWins   int
	draws          int
	plies          int
	aDepth, bDepth depthStats
	elapsed        time.Duration
}

// depthStats summarises how deep one side's search actually got. It belongs
// beside every win rate this harness reports, because only the two weaker tiers
// stop at their depth ceiling. The pro tier's ceiling of sixteen plies is far
// beyond what any practical per-move budget reaches: with the configured budget
// lifted to an hour, so that only the hard stop bound it, a thirty-second search
// of a 16x16 position got to depth 7. The hour is what it was allowed, not what it
// had; thirty seconds is what it spent, and that is the number worth quoting. So
// its rate
// is always a rate at a depth the clock chose, and the depth has to be stated
// for the rate to be worth anything.
type depthStats struct {
	// moves and sum cover only the moves a search actually scored.
	moves, sum int
	min, max   int
	// unsearched counts moves returned without one finished iteration. capped
	// counts moves that ran at least as long as their configured budget.
	unsearched, capped int
}

func (d *depthStats) add(o depthStats) {
	if o.moves > 0 && (d.moves == 0 || o.min < d.min) {
		d.min = o.min
	}
	if o.max > d.max {
		d.max = o.max
	}
	d.moves += o.moves
	d.sum += o.sum
	d.unsearched += o.unsearched
	d.capped += o.capped
}

func (d depthStats) String() string {
	if d.moves == 0 {
		return fmt.Sprintf("no move searched (%d unsearched)", d.unsearched)
	}
	return fmt.Sprintf("depth %.2f mean, %d-%d over %d moves (%d unsearched, %d reached budget)",
		float64(d.sum)/float64(d.moves), d.min, d.max, d.moves, d.unsearched, d.capped)
}

// depthTally plays a tier's moves and remembers how deep each search got and
// whether its clock stopped it. It is wrapped around bots only in the opt-in
// tournament, so the ordinary suite pays none of its timing overhead.
type depthTally struct {
	bot   *engine
	stats depthStats
}

func (d *depthTally) Tier() Tier { return d.bot.Tier() }

func (d *depthTally) Hint(ctx context.Context, g *game.Game) (Hint, error) {
	return d.bot.Hint(ctx, g)
}

func (d *depthTally) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	start := time.Now()
	p, err := d.bot.Move(ctx, g)
	elapsed := time.Since(start)
	if err != nil {
		return p, err
	}
	if elapsed >= d.bot.p.budget {
		d.stats.capped++
	}
	got := d.bot.play.lastDepth
	if got < 1 {
		d.stats.unsearched++
		return p, nil
	}
	if d.stats.moves == 0 || got < d.stats.min {
		d.stats.min = got
	}
	if got > d.stats.max {
		d.stats.max = got
	}
	d.stats.moves++
	d.stats.sum += got
	return p, nil
}

func (m matchResult) scoreA() float64 { return float64(m.aWins) + 0.5*float64(m.draws) }

func (m matchResult) rateA() float64 {
	if m.games == 0 {
		return 0
	}
	return m.scoreA() / float64(m.games)
}

// eloGap converts a win rate into the Elo difference that would produce it.
func eloGap(rate float64) float64 {
	switch {
	case rate <= 0:
		return math.Inf(-1)
	case rate >= 1:
		return math.Inf(1)
	}
	return -400 * math.Log10(1/rate-1)
}

// wilsonLow is the lower bound of the 95% Wilson interval for the win rate,
// which is the number R6 should be judged on: the measured floor of the gap
// rather than its point estimate.
func wilsonLow(score float64, n int) float64 {
	if n == 0 {
		return 0
	}
	const z = 1.96
	p := score / float64(n)
	den := 1 + z*z/float64(n)
	centre := p + z*z/(2*float64(n))
	spread := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	return (centre - spread) / den
}

func (m matchResult) String() string {
	return fmt.Sprintf(
		"%-12s vs %-12s %dx%d  %3d games  %s: %d-%d-%d  rate %.3f (95%% floor %.3f)  Elo %+.0f  %d plies  %s",
		m.cfg.a, m.cfg.b, m.cfg.size, m.cfg.size, m.games, m.cfg.a,
		m.aWins, m.bWins, m.draws, m.rateA(), wilsonLow(m.scoreA(), m.games),
		eloGap(m.rateA()), m.plies, m.elapsed.Round(time.Millisecond))
}

// depthLine reports how deep each side actually searched, which is what makes
// the rate above it readable: a rate against a clock-bound opponent is a
// statement about a depth, not about the tier in the abstract.
func (m matchResult) depthLine() string {
	return fmt.Sprintf("             %-12s %v\n             %-12s %v",
		m.cfg.a, m.aDepth, m.cfg.b, m.bDepth)
}

// tournamentRules is the ruleset every match is played under: the printed box
// rules on a small board, with the swap option removed.
func tournamentRules(size int) game.Ruleset {
	rs := game.Std
	rs.Size = size
	rs.Swap = false
	return rs
}

// openingHoles returns count first moves for Vertical, spread evenly over the
// holes it may use, so that the games in a match are genuinely different games.
func openingHoles(size, count int) []game.Point {
	g := game.MustNew(tournamentRules(size))
	all := g.LegalPlacements(game.Vertical)
	if count >= len(all) {
		return all
	}
	out := make([]game.Point, 0, count)
	// The old diagonal stride is kept whenever its cycle is long enough; only a
	// stride that would repeat within this sample is advanced. That preserves
	// established measurements while preventing a plausible size/count pair
	// from replaying games.
	step := len(all)/count + 7
	for len(all)/gcd(step, len(all)) < count {
		step++
	}
	for i := range count {
		out = append(out, all[(i*step)%len(all)])
	}
	return out
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func TestOpeningHolesAreUnique(t *testing.T) {
	for _, size := range []int{8, 9, 10, 12, 14, 16, 18, 24} {
		for _, count := range []int{8, 12, 20} {
			holes := openingHoles(size, count)
			if len(holes) != count {
				t.Fatalf("%dx%d: got %d openings, want %d", size, size, len(holes), count)
			}
			seen := make(map[game.Point]struct{}, len(holes))
			for _, hole := range holes {
				if _, ok := seen[hole]; ok {
					t.Errorf("%dx%d with %d openings repeats %v; colour balance would replay the same game pair",
						size, size, count, hole)
				}
				seen[hole] = struct{}{}
			}
		}
	}
}

// playTierGame plays one game from the given opening and reports the winner.
func playTierGame(rs game.Ruleset, opening game.Point, vertical, horizontal Bot) (game.Player, int, error) {
	g, err := game.New(rs)
	if err != nil {
		return game.NoPlayer, 0, err
	}
	if _, err := g.PlayPeg(opening); err != nil {
		return game.NoPlayer, 0, fmt.Errorf("opening %v: %w", opening, err)
	}
	limit := rs.Size * rs.Size
	ctx := context.Background()
	for !g.Result().Over() {
		if g.Ply() > limit {
			return game.NoPlayer, g.Ply(), fmt.Errorf("game did not finish within %d plies", limit)
		}
		bot := vertical
		if g.Turn() == game.Horizontal {
			bot = horizontal
		}
		p, err := bot.Move(ctx, g)
		if err != nil {
			return game.NoPlayer, g.Ply(), fmt.Errorf("%v at ply %d: %w", bot.Tier(), g.Ply(), err)
		}
		if _, err := g.PlayPeg(p); err != nil {
			return game.NoPlayer, g.Ply(), fmt.Errorf("%v played illegal %v at ply %d: %w", bot.Tier(), p, g.Ply(), err)
		}
	}
	return g.Result().Winner(), g.Ply(), nil
}

// runMatch plays the whole colour-balanced match and returns the tally.
func runMatch(t *testing.T, cfg matchConfig) matchResult {
	return runMatchTuned(t, cfg, nil, nil)
}

// runMatchTuned is runMatch with optional adjustments applied to each side's
// params, which is how a strength question about one lever is answered.
func runMatchTuned(t *testing.T, cfg matchConfig, mutateA, mutateB func(*params)) matchResult {
	t.Helper()
	if cfg.workers < 1 {
		cfg.workers = 1
	}
	rs := tournamentRules(cfg.size)
	openings := openingHoles(cfg.size, cfg.openings)

	// Each opening is played twice, once with each tier as Vertical.
	type outcome struct {
		winner         game.Player
		aSide          game.Player
		plies          int
		aDepth, bDepth depthStats
		failure        error
	}
	results := make([]outcome, len(openings)*2)
	var wg sync.WaitGroup
	next := make(chan int)
	go func() {
		for i := range results {
			next <- i
		}
		close(next)
	}()
	start := time.Now()
	for range cfg.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				op := openings[i/2]
				aSide := game.Vertical
				if i%2 == 1 {
					aSide = game.Horizontal
				}
				aEngine := tunedEngine(cfg.a, int64(i), func(p *params) {
					p.budget = cfg.budgets[cfg.a]
					if mutateA != nil {
						mutateA(p)
					}
				})
				bEngine := tunedEngine(cfg.b, int64(i), func(p *params) {
					p.budget = cfg.budgets[cfg.b]
					if mutateB != nil {
						mutateB(p)
					}
				})
				var aBot, bBot Bot = aEngine, bEngine
				var aTally, bTally depthTally
				if cfg.trackDepth {
					aTally.bot, bTally.bot = aEngine, bEngine
					aBot, bBot = &aTally, &bTally
				}
				var vert, horz = aBot, bBot
				if aSide == game.Horizontal {
					vert, horz = bBot, aBot
				}
				winner, plies, err := playTierGame(rs, op, vert, horz)
				results[i] = outcome{
					winner: winner, aSide: aSide, plies: plies,
					aDepth: aTally.stats, bDepth: bTally.stats, failure: err,
				}
			}
		}()
	}
	wg.Wait()

	out := matchResult{cfg: cfg, elapsed: time.Since(start)}
	for _, r := range results {
		if r.failure != nil {
			t.Fatalf("match %v vs %v: %v", cfg.a, cfg.b, r.failure)
		}
		out.games++
		out.plies += r.plies
		out.aDepth.add(r.aDepth)
		out.bDepth.add(r.bDepth)
		switch r.winner {
		case game.NoPlayer:
			out.draws++
		case r.aSide:
			out.aWins++
		default:
			out.bWins++
		}
	}
	return out
}

// quickBudgets are the shortened per-move budgets the default measurement uses.
// They are not the shipped budgets scaled by a constant: what matters for a
// fair proxy is that each tier still reaches the depth ceiling that defines it,
// and TestDepthCeilingsSeparateTiers reports the depths these actually produce.
var quickBudgets = [3]time.Duration{
	Beginner:     10 * time.Millisecond,
	Intermediate: 60 * time.Millisecond,
	Pro:          300 * time.Millisecond,
}

func matchWorkers() int {
	workers := runtime.NumCPU() / 2
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

// TestTierStrengthOrdering measures the tier gap that holds on any machine.
//
// Only the intermediate-versus-beginner pairing is asserted here, and the
// reason matters. This match runs on a small board with shortened budgets so
// that it fits in a normal test run, and a deeper search is not reliably better
// on the smaller boards.
//
// The earlier record said that removing the budgets gave pro rates of 0.250 on
// 8x8 and 0.458 on 9x9, but it did not preserve the command, budget, opening
// count or worker count that produced them. It also mixed those figures with
// 0.938 on 24x24 measured at the shipped budget, so they are not one comparable
// series. TestTierStrengthBySize now preserves an exact rerunnable protocol.
// Measured on 2026-09-01 with both tiers given a 30-second guard, 12 unique
// openings played from both sides, swap off and eight workers, pro scored:
//
//	board    W-L-D    games    rate    95% floor
//	10x10   11-13-0     24    0.458       0.279
//	12x12   10-14-0     24    0.417       0.245
//	14x14   14-10-0     24    0.583       0.388
//	16x16   14-10-0     24    0.583       0.388
//
// That guard is not a ceiling measurement either: pro ran for at least the full
// guard on 102, 147, 174 and 208 searched moves respectively, while intermediate
// never did. A separate 16x16 position probe reached only depth 7 in 30 seconds
// against pro's nominal ceiling of 16. Literal removal of the clock is therefore
// not a tractable tournament protocol for this bot. The figures say only that
// even ten times the shipped clock does not establish a reliable mid-board
// advantage; they must not be presented as depth-ceiling results.
//
// The shipped-budget match answers the narrower player-facing question. On the
// same 24-game protocol pro scored 13-11-0 (0.542) on 12x12 and 23-1-0 (0.958)
// on 16x16. With only 24 games these are indicative rather than tight: 12x12
// is effectively even, while 16x16 showed a large advantage in this run.
//
// What remains automated on every run is the pairing that is decisive
// everywhere, together with the deterministic evidence in search_test.go and
// invariant_test.go: pro takes an immediate win, blocks one, finds exact
// defences, builds setups, agrees with full-width minimax, and searches strictly
// deeper than the tiers below it.
func TestTierStrengthOrdering(t *testing.T) {
	if testing.Short() {
		t.Skip("strength measurement plays a few hundred games")
	}
	workers := matchWorkers()

	res := runMatch(t, matchConfig{
		a: Intermediate, b: Beginner, size: 10, openings: 30,
		budgets: quickBudgets, workers: workers,
	})
	t.Log(res.String())
	if res.rateA() < 0.65 {
		t.Errorf("intermediate scored only %.3f against beginner over %d games; R6 wants the tiers to differ substantially",
			res.rateA(), res.games)
	}

	// Pro is still played against intermediate on the proxy, but the assertion
	// is only that more search does not make it materially worse. A real
	// regression, such as an evaluation term that deeper search maximises into a
	// blunder, shows up here; the size of pro's advantage does not.
	pro := runMatch(t, matchConfig{
		a: Pro, b: Intermediate, size: 10, openings: 20,
		budgets: quickBudgets, workers: workers,
	})
	t.Log(pro.String())
	const floor = 0.45
	if pro.rateA() < floor {
		t.Errorf("pro scored %.3f against intermediate over %d games on the small-board proxy, below the %.2f floor; deeper search has become actively harmful",
			pro.rateA(), pro.games, floor)
	}
}

// TestTierStrengthFullBudget repeats the measurement at the budgets the tiers
// actually ship with. It takes minutes rather than seconds, so it only runs
// when asked for by name through the environment.
func TestTierStrengthFullBudget(t *testing.T) {
	if os.Getenv("TWIXT_BOT_TOURNAMENT") == "" {
		t.Skip("set TWIXT_BOT_TOURNAMENT=1 to run the full-budget tournament")
	}
	workers := matchWorkers()
	budgets := shippedBudgets()
	size := 14
	if v := os.Getenv("TWIXT_BOT_SIZE"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &size); err != nil {
			t.Fatalf("TWIXT_BOT_SIZE: %v", err)
		}
	}
	openings := 10
	if v := os.Getenv("TWIXT_BOT_OPENINGS"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &openings); err != nil {
			t.Fatalf("TWIXT_BOT_OPENINGS: %v", err)
		}
	}
	for _, cfg := range []matchConfig{
		{a: Intermediate, b: Beginner, size: size, openings: openings, budgets: budgets, workers: workers},
		{a: Pro, b: Intermediate, size: size, openings: openings, budgets: budgets, workers: workers},
		{a: Pro, b: Beginner, size: size, openings: openings, budgets: budgets, workers: workers},
	} {
		res := runMatch(t, cfg)
		t.Log(res.String())
	}
}

// shippedBudgets are the per-move budgets the tiers actually run with in the
// program. They are what a measurement has to use to describe what a player
// gets: the two weaker tiers answer well inside theirs, so for them the budget
// is only a guard, but the pro tier spends all of its own and is therefore
// measured at whatever depth the clock allowed.
func shippedBudgets() [3]time.Duration {
	return [3]time.Duration{
		Beginner:     tierParams(Beginner).budget,
		Intermediate: tierParams(Intermediate).budget,
		Pro:          tierParams(Pro).budget,
	}
}

// TestTierStrengthBySize measures the pro-versus-intermediate gap across board
// sizes under the fully specified lifted-budget protocol recorded above. It
// reports both the result and how often each tier reached the guard; the latter
// prevents a clock-bound run from being mistaken for a depth-ceiling run.
//
// Every opening is played twice, once from each side, swap is off and draws
// score a half. The match takes nearly an hour, so it runs only when explicitly
// selected through the existing tournament opt-in.
func TestTierStrengthBySize(t *testing.T) {
	if os.Getenv("TWIXT_BOT_TOURNAMENT") == "" {
		t.Skip("set TWIXT_BOT_TOURNAMENT=1 to run the by-size measurement")
	}
	workers := matchWorkers()
	sizes := []int{10, 12, 14, 16}
	if v := os.Getenv("TWIXT_BOT_SIZE"); v != "" {
		var size int
		if _, err := fmt.Sscanf(v, "%d", &size); err != nil {
			t.Fatalf("TWIXT_BOT_SIZE: %v", err)
		}
		sizes = []int{size}
	}
	openings := 12
	if v := os.Getenv("TWIXT_BOT_OPENINGS"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &openings); err != nil {
			t.Fatalf("TWIXT_BOT_OPENINGS: %v", err)
		}
	}

	// Thirty seconds reproduces the recorded mid-board run. The override is
	// available for replication at another explicit guard, not as an invitation
	// to call a finite clock a removed budget.
	budget := 30 * time.Second
	if v := os.Getenv("TWIXT_BOT_BUDGET"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("TWIXT_BOT_BUDGET: %v", err)
		}
		budget = d
	}
	budgets := [3]time.Duration{Beginner: budget, Intermediate: budget, Pro: budget}
	how := "every budget guarded at " + budget.String()
	t.Logf("pro vs intermediate, %s, %d openings colour balanced, %d workers",
		how, openings, workers)

	for _, size := range sizes {
		res := runMatch(t, matchConfig{
			a: Pro, b: Intermediate, size: size, openings: openings,
			budgets: budgets, workers: workers, trackDepth: true,
		})
		t.Log(res.String())
		t.Log(res.depthLine())
	}
}

// randomBot plays a uniformly chosen legal hole. It is the floor the weakest
// tier has to clear: a bot that is deliberately weak still has to be playing
// the game.
type randomBot struct{ src *rand.Rand }

func (randomBot) Tier() Tier { return Beginner }

func (b randomBot) Move(_ context.Context, g *game.Game) (game.Point, error) {
	legal := g.LegalPlacements(g.Turn())
	if len(legal) == 0 {
		return game.Point{}, ErrNoMove
	}
	return legal[b.src.IntN(len(legal))], nil
}

func (randomBot) Hint(context.Context, *game.Game) (Hint, error) {
	return Hint{}, errors.New("randomBot gives no hints")
}

// TestBeginnerBeatsRandomPlay is the "weak but not broken" check for R6's floor.
// The beginner tier samples among near-best moves and evaluates on peg counts
// alone, but it still has to be trying to finish its own chain.
func TestBeginnerBeatsRandomPlay(t *testing.T) {
	if testing.Short() {
		t.Skip("plays a few dozen games")
	}
	rs := tournamentRules(10)
	openings := openingHoles(10, 12)
	wins, games := 0, 0
	for i, op := range openings {
		for _, beginnerFirst := range []bool{true, false} {
			bot := fastEngine(Beginner, int64(i), quickBudgets[Beginner])
			noise := randomBot{src: rand.New(rand.NewPCG(uint64(i), 99))}
			var vert, horz Bot = bot, noise
			side := game.Vertical
			if !beginnerFirst {
				vert, horz = noise, bot
				side = game.Horizontal
			}
			winner, _, err := playTierGame(rs, op, vert, horz)
			if err != nil {
				t.Fatalf("game %d: %v", i, err)
			}
			games++
			if winner == side {
				wins++
			}
		}
	}
	rate := float64(wins) / float64(games)
	t.Logf("beginner vs random play: %d/%d games won, rate %.3f", wins, games, rate)
	if rate < 0.9 {
		t.Errorf("beginner won only %.3f against random play over %d games; it is not playing the game",
			rate, games)
	}
}
