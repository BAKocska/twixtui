// Package bot plays TwixT. One alpha-beta search backs four effort tiers and
// the hint feature; the tiers differ in the guards they search under — how long
// they may think, how deep they may look, how many nodes they may visit — in
// how many candidate moves they keep, and in how much of the evaluation they
// are allowed to see.
//
// A tier names an effort ceiling, not an attained strength. What a search
// actually reaches inside those guards depends on the position, the board size
// and the machine, and more effort is not a promise of a better move in every
// position. The search is also selective — it looks at a shortlist of holes per
// node and places pegs with their links taken automatically — so nothing here
// is a solver for the full game.
//
// Limits and NewWithLimits replace a tier's guards with the caller's own, which
// is how a measurement asks for work rather than for wall-clock time, and
// StatsOf reports what the last search actually spent.
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

// ErrStagedTurn reports a position whose turn in progress carries uncommitted
// edits: a peg placed but not committed, or a link added or withdrawn by hand.
//
// The search plays and takes back trial moves on the game it is given, and
// taking a move back restores the position from before the turn, so a staged
// edit handed to the engine would be searched as though it were part of the
// position and then thrown away. The bot refuses instead of doing either: the
// caller commits or aborts the turn first. This is checked once per request,
// not per node.
var ErrStagedTurn = errors.New("bot: the turn in progress has uncommitted edits: commit or abort it first")

// staged reports whether the turn in progress has anything in it.
func staged(g *game.Game) bool {
	st := g.Staged()
	return st.PegPlaced || len(st.Added) > 0 || len(st.Removed) > 0 ||
		len(st.RemovedPegs) > 0 || len(st.PegLinks) > 0
}

// Tier names how much effort the bot spends on a move.
type Tier int

// The available tiers, in increasing order of the effort they may spend.
const (
	Beginner Tier = iota
	Intermediate
	Pro
	Max
)

var tierNames = [...]string{"beginner", "intermediate", "pro", "max"}

// tierSummaries describe what each tier is allowed to do, which is not the same
// as what it achieves. The two weaker tiers are capped in depth and answer well
// inside their budgets; the two stronger ones can be stopped by the clock with
// their depth ceilings still far away, so the wording names the guards and
// leaves the depth reached to the position and the machine.
var tierSummaries = [...]string{
	"one move ahead, counting only how many pegs each side still needs, answered at once: takes a win and blocks one, but has no plan",
	"three moves ahead with the full evaluation, still near-instant: punishes a loose chain",
	"up to three seconds under a sixteen-move ceiling it rarely reaches, extending forced lines and remembering positions it has already scored",
	"the largest effort on offer: up to ten seconds, a twenty-four-move ceiling and a wider shortlist of candidates — more thinking, not a guarantee of a better move",
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

// TierNames returns tier names in increasing configured effort order.
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

// AnalysisPolicy identifies the move restrictions and heuristic scope of advice.
// The zero value is unstated, not unrestricted. Hint carries the policy so its
// consumers can distinguish a placement-only recommendation from full-rule
// analysis. Exact terminal claims additionally require an engine-verified result.
type AnalysisPolicy struct {
	// kind is unexported so that the only stated policy a caller can hold is
	// one this package handed out. The policy is a claim about how the search
	// works, and nothing outside the search is in a position to make it.
	kind policyKind
}

// policyKind is which of the two cases an AnalysisPolicy is in.
type policyKind uint8

const (
	policyUnstated policyKind = iota
	policyPlacementOnly
)

// PlacementOnlyPolicy is the policy every search in this package runs under: it
// places one peg a turn and keeps every link that placement offers, and it
// looks at a shortlist of holes rather than at every legal continuation.
//
// A turn under the printed rules may do more than that — join two pegs already
// down, take one of the mover's own links back, take the swap — and none of it
// is searched. So what comes back is a steer under those restrictions and never
// a statement about legal play: a side this evaluation reads as having no route
// left can still have a legal winning turn, because a link it refuses to travel
// along is a link a player may add by hand.
func PlacementOnlyPolicy() AnalysisPolicy { return AnalysisPolicy{kind: policyPlacementOnly} }

// Label is the badge for a status line with no room for a sentence.
func (p AnalysisPolicy) Label() string {
	if p.kind == policyPlacementOnly {
		return "placement-only"
	}
	return "unstated"
}

// String is the compact form, for a log line or a machine-readable field.
func (p AnalysisPolicy) String() string {
	if p.kind == policyPlacementOnly {
		return "placement-only, offered-links-kept, no-swap"
	}
	return "unstated"
}

// Summary is the sentence a reader gets. It names the badge inside itself, so a
// surface with room for one line of policy and no more still shows which policy
// it is.
func (p AnalysisPolicy) Summary() string {
	if p.kind == policyPlacementOnly {
		return "This advice is placement-only: one peg a turn with the links that placement offers, no link joined or taken back by hand, no swap, and a shortlist of holes rather than every legal line — a steer, not a proof."
	}
	return "This advice states no analysis policy, so what it covers is unknown: it is neither a proof nor a reading of the whole of the printed rules."
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
	// Policy is the restriction the advice was produced under. Every hint this
	// package returns carries PlacementOnlyPolicy; the zero value states
	// nothing, which is what a stub or scripted bot leaves behind.
	Policy AnalysisPolicy
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
	// position cannot be searched: the game is over, there is nowhere left to
	// play, or the turn in progress has uncommitted edits.
	Move(ctx context.Context, g *game.Game) (game.Point, error)
	// Hint explains the move the bot would consider best for the side to move.
	// A hint is always computed with the highest-effort settings the package
	// has, whatever tier is playing, so that a beginner-tier game does not also
	// give beginner-tier advice.
	Hint(ctx context.Context, g *game.Game) (Hint, error)
}

// tierParams returns the levers for a tier.
//
// Depth, candidate width, evaluation and time distinguish effort, not guaranteed
// playing strength. A node ceiling is optional: callers use Limits to compare
// reproducible amounts of work while retaining a wall-clock safety guard.
//
// Three levers exist that no tier turns on: killers, aspiration and templates.
// All are implemented, tested and switchable from the effort benchmark, and all
// were measured on the pro tier at its shipped shortlist and found not to pay
// there. Killers returned identical values with fewer nodes only at full
// width, where the bot never runs; at pro's width the saving was inconsistent
// across depths (worse at five, better at six and seven) and a fifth of the
// scores changed. Aspiration with its two-peg band cost nodes at both depths
// tried, six and eight, and no band setting saved more than noise. The proved
// edge-template corpus, in and out of the evaluation at 30,000 nodes a move
// over twelve openings from both sides, changed no move at all on 16x16 and
// changed the result of one opening in twelve on 10x10, against the corpus. A
// lever measured not to help is left off rather than shipped on the strength
// of the idea, so what the tiers play is what the tournament measured. The
// numbers are in docs/MANUAL.md; the pvs-killers, pvs-aspiration and
// pvs-templates candidates are how each is measured again. The other tiers
// were not measured with these levers and inherit the same default.
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
			pvs:       true,
		}
	case Max:
		// A broader shortlist and larger search horizon for the highest-effort
		// mode. More work is not a guarantee of better play in every position.
		return params{
			budget:    10 * time.Second,
			maxDepth:  24,
			width:     32,
			rootWidth: 48,
			fullEval:  true,
			useTable:  true,
			extend:    8,
			pvs:       true,
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
// highest-effort levers the package has regardless of which tier is playing: a
// beginner-tier game giving beginner-tier advice would be useless.
//
// The one lever not taken from the highest-effort tier is its clock. Somebody
// is waiting for the answer, so a hint keeps a two-second guard instead of ten
// seconds. It uses the same policy but can finish at a shallower depth.
func hintParams() params {
	p := tierParams(Max)
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

// New returns a bot of the given tier. A tier that does not exist gives the
// beginner rather than an error, so a caller that has not validated its input
// still plays; NewWithLimits refuses it instead.
//
// The seed decides the beginner tier's choice among near-best moves, and
// nothing else: the other tiers play the best move their search found. That
// move is a function of the seed and the position for as long as the search
// runs to completion. The beginner and intermediate tiers stop at their depth
// ceilings, which they reach well inside their budgets, so in practice they do
// run to completion; the pro and max tiers carry ceilings their budgets need
// not be long enough to reach, and any tier can be cut short by a deadline or
// a cancellation on the caller's context. A search that was cut short answers
// from the work it had finished, so reproducibility follows the completed work
// rather than the tier — see Limits for how a caller asks for work it can
// count on rather than for wall-clock time.
func New(t Tier, seed int64) Bot {
	if t < Beginner || t > Max {
		t = Beginner
	}
	p := tierParams(t)
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}
}

// Limits replace the effort guards a tier searches under. A zero field keeps the
// tier's own value, so a caller states only what it means to change. A negative
// one is refused rather than ignored: it can only be a mistake, and silently
// searching under the tier's own guard instead would make a measurement report
// work it never bounded.
//
// Nodes and Depth bound work rather than time, and work is what a caller can
// hold constant between machines. That only holds as far as the work bound is
// the guard that actually binds: the search always carries a wall-clock guard
// as well, a deadline or cancellation on the caller's context always cuts it
// short, and whichever guard comes first is the one that ends the search. So a
// caller after a search it can reproduce sets Nodes or Depth, lifts Time far
// out of the way — Time: time.Hour — passes no deadline it expects to hit, and
// checks StatsOf: a StopReason of "nodes" or "depth" says the work bound ended
// the search and the answer is reproducible, while "time" or "canceled" says
// it did not and the answer is whatever that machine had reached by then.
type Limits struct {
	// Nodes is the most nodes the search may visit across all its iterations.
	// Zero keeps the tier's own ceiling, which for every tier is none.
	Nodes int64
	// Depth is the deepest iteration the search may run. Zero keeps the tier's
	// own ceiling, and MaxDepth is the most the search can hold.
	Depth int
	// Time is a search-time guard, checked at work boundaries rather than a
	// hard real-time deadline. Zero keeps the tier's own budget.
	Time time.Duration
}

// MaxDepth is the deepest search Limits may ask for. The search keeps one
// working position per ply and deepens forced lines past their nominal depth,
// so the last ply it can hold is reserved for those extensions.
const MaxDepth = maxSearchPly - 1

// apply folds the limits into a tier's params, refusing what the search cannot
// honour.
func (l Limits) apply(p *params) error {
	switch {
	case l.Nodes < 0:
		return fmt.Errorf("bot: node limit %d is negative", l.Nodes)
	case l.Depth < 0:
		return fmt.Errorf("bot: depth limit %d is negative", l.Depth)
	case l.Time < 0:
		return fmt.Errorf("bot: time limit %v is negative", l.Time)
	case l.Depth > MaxDepth:
		return fmt.Errorf("bot: depth limit %d is beyond the %d plies the search can hold", l.Depth, MaxDepth)
	}
	if l.Nodes > 0 {
		p.nodeLimit = l.Nodes
	}
	if l.Depth > 0 {
		p.maxDepth = l.Depth
	}
	if l.Time > 0 {
		p.budget = l.Time
	}
	return nil
}

// NewWithLimits returns a bot of the given tier searching under the given
// limits. Everything else about the tier — its candidate widths, its
// evaluation, its table, its sampling — is untouched, so the bot is the tier
// playing under a different guard rather than a different evaluation policy.
//
// Unlike New it refuses a tier that does not exist. A caller passing explicit
// limits is configuring a measurement rather than starting a game, and would
// rather hear about the mistake than quietly measure the beginner.
func NewWithLimits(t Tier, seed int64, limits Limits) (Bot, error) {
	if t < Beginner || t > Max {
		return nil, fmt.Errorf("bot: unknown tier %d, want one of %s", int(t), strings.Join(TierNames(), ", "))
	}
	p := tierParams(t)
	if err := limits.apply(&p); err != nil {
		return nil, err
	}
	return &engine{tier: t, seed: seed, p: p, play: newSearcher(p)}, nil
}

// SearchStats is what a search actually spent. The search records it as it
// goes, so reading it costs nothing and starts nothing: it describes a search
// that has already happened, and is the zero value before the first one.
type SearchStats struct {
	// Nodes is how many positions the search visited below the root, summed
	// over every iteration of its deepening loop.
	Nodes int64
	// Evaluations is how many positions it loaded and scored, root and threat
	// probes included, so it is normally larger than Nodes.
	Evaluations int64
	// Depth is the deepest completed iteration. Zero includes immediate wins
	// and searches interrupted before their first iteration completed.
	Depth int
	// Elapsed is how long the search took.
	Elapsed time.Duration
	// StopReason names what ended it: "immediate" for a winning hole taken
	// without searching, "depth" for every allowed iteration finished,
	// "decided" for a forced result inside the selective search tree (not a
	// proof about every legal TwixT continuation), "time" for the budget or the
	// caller's deadline, "nodes" for the node ceiling, "canceled" for a
	// cancelled context, "no-move" for a position with nowhere to play, and
	// "error" for a search that could not restore the position it was given.
	// The first three mean the search ended on its own terms and Depth is what
	// it reached; the rest mean it was cut short and Depth is the last
	// iteration it had completed.
	StopReason string
}

// StatsOf reports what a bot's last Move search spent, or the zero SearchStats
// for a bot that does not keep the count.
//
// Keeping it is optional on purpose. Bot is the contract the screens and the
// tests depend on, and a stub or a scripted opponent has no search to report;
// an implementation that does report offers Stats() SearchStats, which is what
// this asks for. It never runs a search itself, so a caller may read it after
// every move without changing what the bot does or how long it takes.
func StatsOf(b Bot) SearchStats {
	if s, ok := b.(interface{ Stats() SearchStats }); ok {
		return s.Stats()
	}
	return SearchStats{}
}

func (e *engine) Tier() Tier { return e.tier }

// Stats reports what the last Move search spent, and the zero value before the
// first one. The hint search is deliberately left out of it: a hint runs under
// its own settings and at the player's request, so folding it in would answer
// "what did the engine spend on its move" with somebody else's search.
func (e *engine) Stats() SearchStats {
	if e.play == nil {
		return SearchStats{}
	}
	return SearchStats{
		Nodes:       e.play.nodes,
		Evaluations: e.play.evaluations,
		Depth:       e.play.lastDepth,
		Elapsed:     e.play.elapsed,
		StopReason:  e.play.stopReason,
	}
}

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
	if staged(g) {
		return game.Point{}, ErrStagedTurn
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
