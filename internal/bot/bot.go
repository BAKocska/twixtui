// Package bot plays TwixT. One alpha-beta search backs three effort tiers and
// the hint feature; the tiers differ in budget, depth, candidate width and how
// much of the evaluation they are allowed to see.
package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// ErrNoMove reports a position where the bot has nowhere to play.
var ErrNoMove = errors.New("bot: no legal placement available")

// Tier names how much effort the bot spends on a move.
type Tier int

// The available tiers, weakest first.
const (
	Beginner Tier = iota
	Intermediate
	Pro
)

var tierNames = [...]string{"beginner", "intermediate", "pro"}

// tierSummaries describe what each tier actually does. Only the pro tier spends
// its whole budget thinking; the two weaker tiers are capped in depth and so
// answer well inside theirs, which the wording must not overstate.
var tierSummaries = [...]string{
	"one move ahead, counting only how many pegs each side still needs, answered at once: takes a win and blocks one, but has no plan",
	"three moves ahead with the full evaluation, still near-instant: punishes a loose chain",
	"thinks for up to three seconds, five to seven moves ahead, extending forced lines: the strongest play on offer",
}

// String returns the tier's name.
func (t Tier) String() string {
	if t < 0 || int(t) >= len(tierNames) {
		return fmt.Sprintf("Tier(%d)", int(t))
	}
	return tierNames[t]
}

// ParseTier reads a tier name, ignoring case and surrounding space.
func ParseTier(s string) (Tier, error) {
	want := strings.ToLower(strings.TrimSpace(s))
	for i, name := range tierNames {
		if name == want {
			return Tier(i), nil
		}
	}
	return 0, fmt.Errorf("bot: unknown tier %q, want one of %s", s, strings.Join(TierNames(), ", "))
}

// TierNames returns the tier names weakest first.
func TierNames() []string {
	return append([]string(nil), tierNames[:]...)
}

// TierSummary returns the one-line description of a named tier, for help text
// and shell completion. An unknown name returns the empty string.
func TierSummary(name string) string {
	t, err := ParseTier(name)
	if err != nil {
		return ""
	}
	return tierSummaries[t]
}

// Hint is a recommended move together with an explanation derived from the
// search that found it.
type Hint struct {
	Move game.Point
	// Headline is one short line: what to play.
	Headline string
	// Detail is one to three short sentences saying why, in terms of the
	// numbers the search actually measured.
	Detail string
	// Highlight lists the holes the explanation refers to, for the board to
	// mark.
	Highlight []game.Point
}

// Bot plays one side of a game.
//
// A Bot carries the working state of its search and is not safe for concurrent
// use: give each game its own Bot.
type Bot interface {
	Tier() Tier
	// Move returns the hole to play. It never returns an illegal move.
	//
	// A deadline or a cancellation on ctx cuts the search short and Move
	// returns the best move it had reached, which is how a per-move budget is
	// enforced; it is not an error, and even an already-cancelled context
	// yields a legal move chosen by the ordering heuristic. An error means the
	// position genuinely has no move: the game is over, or there is nowhere
	// left to play.
	Move(ctx context.Context, g *game.Game) (game.Point, error)
	// Hint explains the move the bot would consider best for the side to move.
	// A hint is always computed with the strongest settings the package has,
	// whatever tier is playing, so that a beginner-tier game does not also give
	// beginner-tier advice.
	Hint(ctx context.Context, g *game.Game) (Hint, error)
}

// tierParams returns the levers for a tier.
//
// The separation is by depth ceiling first and budget second, because that is
// what the strength measurement showed actually works: each extra ply up to
// about five is worth a large win-rate margin, whereas extra time on its own
// buys only a fraction of a ply. Capping the weaker tiers means the ladder does
// not depend on the machine happening to be fast or slow — a fast machine
// cannot let the beginner catch up, and a slow one still leaves the beginner
// playing on pegs alone with no sense of shape.
func tierParams(t Tier) params {
	switch t {
	case Intermediate:
		return params{
			budget:    time.Second,
			maxDepth:  3,
			width:     14,
			rootWidth: 18,
			fullEval:  true,
		}
	case Pro:
		return params{
			budget:    3 * time.Second,
			maxDepth:  16,
			width:     18,
			rootWidth: 24,
			fullEval:  true,
			useTable:  true,
			extend:    6,
		}
	default:
		return params{
			budget:    100 * time.Millisecond,
			maxDepth:  1,
			width:     6,
			rootWidth: 6,
			fullEval:  false,
			// Roughly a peg and a half of spread, which is enough that the
			// beginner often picks the second- or third-best of its six
			// candidates without ever giving away an immediate win.
			temperature: 1.5,
		}
	}
}

// hintParams are the settings a hint is computed with. A hint is a one-off
// request with no turn clock behind it, so it is always answered with the
// strongest settings the package has regardless of which tier is playing:
// a beginner-tier game giving beginner-tier advice would be useless.
func hintParams() params {
	p := tierParams(Pro)
	p.budget = 2 * time.Second
	p.temperature = 0
	return p
}

type engine struct {
	tier Tier
	seed int64
	p    params
	play *searcher
	hint *searcher
}

// New returns a bot of the given tier. The seed fixes its choices where the
// search is bounded by depth: the beginner and intermediate tiers always answer
// the same way from the same seed and position. The pro tier is bounded by the
// clock, so a loaded machine can stop its search earlier and answer
// differently; its games are reproducible in practice rather than by guarantee.
func New(t Tier, seed int64) Bot {
	if t < Beginner || t > Pro {
		t = Beginner
	}
	p := tierParams(t)
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}
}

func (e *engine) Tier() Tier { return e.tier }

func (e *engine) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g == nil {
		return game.Point{}, errors.New("bot: no game")
	}
	if g.Result().Over() {
		return game.Point{}, game.ErrGameOver
	}
	me := g.Turn()
	if !g.HasLegalPlacement(me) {
		return game.Point{}, ErrNoMove
	}

	res, err := e.play.root(ctx, g)
	if err != nil {
		return game.Point{}, err
	}
	move := res.best
	if e.p.temperature > 0 && !res.immediate {
		move = sampleMove(res.moves, e.p.temperature, rand.New(rand.NewPCG(uint64(e.seed), res.an.hash)))
	}
	if g.CanPlace(me, move) != nil {
		// The search is supposed to make this impossible. Returning an illegal
		// move would break the caller, so fall back to a hole that is legal by
		// construction.
		var fallback game.Point
		found := false
		g.EachLegalPlacement(me, func(p game.Point) bool {
			fallback, found = p, true
			return false
		})
		if !found {
			return game.Point{}, ErrNoMove
		}
		return fallback, nil
	}
	return move, nil
}

// sampleMove picks among the root moves in proportion to exp(-loss/temperature),
// where loss is measured in pegs. A move that loses outright, or that is worse
// than a win already found, is never picked: the tier is meant to be weak, not
// broken.
func sampleMove(moves []scoredMove, temperature float64, src *rand.Rand) game.Point {
	if len(moves) == 0 {
		return game.Point{}
	}
	best := moves[0].score
	for _, m := range moves[1:] {
		if m.score > best {
			best = m.score
		}
	}
	if best >= decidedScore {
		// A win is on the board. Take it.
		for _, m := range moves {
			if m.score == best {
				return m.at
			}
		}
	}
	scale := temperature * distWeight
	total := 0.0
	weights := make([]float64, len(moves))
	for i, m := range moves {
		if m.score <= -decidedScore && best > -decidedScore {
			continue
		}
		w := 1.0
		if d := float64(best - m.score); d > 0 {
			w = math.Exp(-d / scale)
		}
		weights[i] = w
		total += w
	}
	if total <= 0 {
		return moves[0].at
	}
	pick := src.Float64() * total
	for i, w := range weights {
		pick -= w
		if pick < 0 {
			return moves[i].at
		}
	}
	return moves[0].at
}

// sortPoints puts a highlight list in a stable order so that two identical
// hints highlight identical holes in identical order.
func sortPoints(ps []game.Point) {
	slices.SortFunc(ps, func(a, b game.Point) int {
		if a.Row != b.Row {
			return a.Row - b.Row
		}
		return a.Col - b.Col
	})
}
