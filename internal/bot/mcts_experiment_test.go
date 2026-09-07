package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// A heuristic Monte Carlo tree search over the same positions, the same move
// ordering and the same static score as the alpha-beta engine, offered as a
// research candidate against it.
//
// It lives in a test file on purpose. Nothing in the shipped binary can depend
// on it, no tier can be configured onto it by accident, and rejecting it costs
// one deletion rather than an unpicking. It is opt-in: the benchmark and
// strength harnesses call experimentMCTS directly.
//
// What is Monte Carlo about it, and what is not:
//
//   - Each simulation walks the tree by PUCT — the mean value backed up so far
//     plus an exploration term weighted by a prior — expands one new node, and
//     plays a bounded, seeded continuation from it. Values are backed up along
//     the whole path with the sign flipped at every ply, so a node's mean is
//     always the value of the position for the side to move at that node.
//   - There is no trained model. The prior over a node's replies is a softmax
//     of the engine's own ordering score, and a leaf that is not a finished
//     game is valued by the engine's own static score squashed into [-1, 1]. So
//     this contender differs from the incumbent in how it spends its effort,
//     not in what it knows.
//   - Terminal results are exact, never sampled: a finished game, a side one
//     peg from a finished chain, a threat with no defence, and a board with
//     nowhere left to play are all recorded as proven values, and only proven
//     values are allowed to override the visit count at the root.
//   - The root policy is the engine's: an immediate win is taken by
//     analysis.winningHole without searching, and against an opponent one peg
//     from finishing the candidate list is searcher.defences, so the search
//     cannot spend its simulations discovering a one-ply blunder.
//
// Nothing here is a claim about strength. It is a contender to be measured.

const (
	// mctsMaxPly bounds how deep the tree may grow. It stays one ply inside
	// the alpha-beta recursion limit so that the two contenders are bounded by
	// the same constant, and a node at the limit keeps its static score as a
	// value that is explicitly not proof.
	mctsMaxPly = maxSearchPly - 1
	// mctsRollSlot is the per-ply move buffer the rollout borrows from the
	// shared generator. It sits outside the range the tree itself uses, so a
	// rollout cannot overwrite a candidate list the descent is still reading,
	// and it is not zero, so a rollout gets the interior width rather than the
	// root width.
	mctsRollSlot = maxSearchPly
)

// Child slot markers. A slot holds a node index once expanded.
const (
	mctsUnexpanded = int32(-1)
	mctsIllegal    = int32(-2)
)

// mctsParams are the contender's levers, the counterpart of params.
type mctsParams struct {
	// exploration is the PUCT constant: how much an unexplored reply with a
	// good prior outweighs a reply already known to score well.
	exploration float64
	// fpu is how much worse than its parent an unvisited reply is assumed to
	// be. Without it the first visit to a node would fan out over every reply
	// in the widening window before deepening any of them.
	fpu float64
	// priorTau spreads the prior over the ordering score, in ordering-score
	// units. Small values make the search follow the ordering heuristic
	// closely; large values flatten the prior towards uniform.
	priorTau float64
	// width and rootWidth cap the candidate list exactly as they do for the
	// alpha-beta search, since the same generator produces it.
	width     int
	rootWidth int
	// widenBase controls progressive widening: a node with v visits may use
	// 1+widenBase*sqrt(v) of its replies. A node reached once is searched for
	// its best reply alone; a node the search keeps returning to is allowed to
	// consider more. Defence lists are exempt — they are short and exact, and
	// narrowing them would reintroduce the blunder the list exists to avoid.
	widenBase float64
	// rolloutPlies bounds the continuation played from a new leaf. Zero values
	// the leaf statically and plays no continuation at all.
	rolloutPlies int
	// rolloutTop is how many of the ordered replies a rollout ply samples
	// from, and rolloutTau spreads that sampling over their ordering scores.
	rolloutTop int
	rolloutTau float64
	// valueScale converts the static score into [-1, 1] through tanh. One peg
	// of head start is distWeight, so a scale of two pegs leaves a one-peg
	// lead well short of saturation, which keeps a rollout's shape information
	// visible instead of rounding every position to a win or a loss.
	valueScale float64
	// fullEval admits the bottleneck and ground terms into the leaf score,
	// with the same meaning as in params.
	fullEval bool
	// evalLimit caps how many analyses one call may spend, zero for no cap.
	// It is what makes an equal-evaluation comparison against the alpha-beta
	// search expressible directly, instead of calibrating a simulation count
	// per position and hoping it holds.
	//
	// The cap is read at every simulation boundary and at every rollout ply,
	// so it stops the search rather than bounding it exactly: one move
	// generation confirms its tactical candidates by playing them, and those
	// analyses all land before the next boundary. The overshoot is therefore
	// at most the number of candidates one defence list confirms, which is
	// bounded by the holes on the board, and it is reported — Evaluations is
	// what was actually spent, never the cap.
	evalLimit int64
}

func defaultMCTSParams() mctsParams {
	return mctsParams{
		exploration:  1.6,
		fpu:          0.15,
		priorTau:     10,
		width:        18,
		rootWidth:    24,
		widenBase:    1.5,
		rolloutPlies: 8,
		rolloutTop:   4,
		rolloutTau:   8,
		valueScale:   2 * distWeight,
		fullEval:     true,
	}
}

func (p mctsParams) validate() error {
	switch {
	case p.width < 1:
		return fmt.Errorf("mcts: width %d is not positive", p.width)
	case p.rootWidth < 1:
		return fmt.Errorf("mcts: rootWidth %d is not positive", p.rootWidth)
	case p.exploration < 0:
		return fmt.Errorf("mcts: exploration %v is negative", p.exploration)
	case p.priorTau <= 0:
		return fmt.Errorf("mcts: priorTau %v is not positive", p.priorTau)
	case p.widenBase <= 0:
		return fmt.Errorf("mcts: widenBase %v is not positive", p.widenBase)
	case p.rolloutPlies < 0:
		return fmt.Errorf("mcts: rolloutPlies %d is negative", p.rolloutPlies)
	case p.rolloutTop < 1:
		return fmt.Errorf("mcts: rolloutTop %d is not positive", p.rolloutTop)
	case p.rolloutTau <= 0:
		return fmt.Errorf("mcts: rolloutTau %v is not positive", p.rolloutTau)
	case p.valueScale <= 0:
		return fmt.Errorf("mcts: valueScale %v is not positive", p.valueScale)
	case p.evalLimit < 0:
		return fmt.Errorf("mcts: evalLimit %d is negative", p.evalLimit)
	}
	return nil
}

// mctsStats is what a harness can read back about the work one call did.
type mctsStats struct {
	// Simulations counts the simulations that finished.
	Simulations int
	// Evaluations counts every position analysis the call spent, in the same
	// unit as searcher.evaluations and through the same searcher.analyse: one
	// per node expanded, one per rollout ply, and every tactical probe the
	// shared defence generator makes on the way. That is what makes an
	// equal-evaluation comparison against the alpha-beta search a comparison
	// of like with like; Nodes is a count of tree nodes and is not an
	// analysis count.
	Evaluations int64
	// Nodes counts the tree nodes created, including terminal records.
	Nodes int
	// RootVisits counts the simulations that reached the root, which is every
	// simulation that finished.
	RootVisits int
	// RootExpanded counts the root replies that were given a node of their
	// own, which is how much of the root list the search actually looked at.
	RootExpanded int
	// MaxDepth is the longest path from the root any simulation walked.
	MaxDepth int
	// RootValue is the root's mean backed-up value, for the side to move.
	RootValue float64
	// StopReason names the guard that ended the search. Where the alpha-beta
	// search has a reason for the same thing, its constant is used rather than
	// a second spelling of it: stopCanceled when the context ended the search
	// and stopImmediate for a win taken without searching. The two reasons
	// this contender has of its own are "simulations", the requested count
	// spent, and "evalLimit", the analysis budget spent; "none" means no
	// search was asked for.
	StopReason string
}

// mctsNode is one position in the tree. A node is reached by exactly one move
// sequence, so its position never changes: the candidate list and priors are
// computed once, when the node is created, and every later visit costs a
// PlayPeg and nothing else.
type mctsNode struct {
	mover game.Player
	ply   int
	// moves is this node's replies, best-ordered, owned by the node: the
	// generator's buffer is reused by every other node at the same ply.
	moves []scoredMove
	prior []float64
	child []int32

	visits int32
	// total is the sum of backed-up values from mover's point of view.
	total float64

	// forced marks a node whose list is the exact defence list.
	forced bool
	// terminal marks a node that is never expanded, exact holds its value for
	// mover, and proven distinguishes a game-theoretic result from the static
	// score kept by a node at the depth limit.
	terminal bool
	proven   bool
	exact    float64
}

// mctsStep is one edge of the path a simulation walked.
type mctsStep struct {
	node int32
	slot int
}

// mctsTree is the working state of one search. Everything reusable is reused:
// one searcher (its ordering history, defence scratch and per-ply move
// buffers), one analysis for expansion and one for rollouts, one path buffer,
// and the caller's own game, played and unplayed in place rather than cloned.
type mctsTree struct {
	p   mctsParams
	s   *searcher
	rng *rand.Rand

	nodes []mctsNode
	path  []mctsStep

	expAn  analysis
	rollAn analysis
	rollW  []float64

	maxDepth int
}

func newMCTSTree(p mctsParams) *mctsTree {
	// The searcher is built for its generator and its leaf score only: no
	// transposition table, no depth, no clock. Handing it the same width
	// levers is what makes the two contenders look at the same shortlists.
	sp := params{
		width:     p.width,
		rootWidth: p.rootWidth,
		fullEval:  p.fullEval,
	}
	return &mctsTree{p: p, s: newSearcher(sp)}
}

// evaluations is everything analysed on this call's behalf: the tree's own
// loads and the probes the shared generator made confirming defences.
func (t *mctsTree) evaluations() int64 {
	return t.s.evaluations
}

// generationStopped reports how a move generation ended. A generator that ran
// out of context returns a prefix of the exact list, which is sound to play
// from but must never be read as exhaustive; a generator that could not
// restore the position has broken the engine and is a genuine error.
func (t *mctsTree) generationStopped() (prefix bool, err error) {
	if !t.s.stopped {
		return false, nil
	}
	if t.s.stopReason == stopError {
		return true, errMCTSUndo
	}
	return true, nil
}

// mctsSign is the exact value of a finished game for one side.
func mctsSign(winner, me game.Player) float64 {
	switch winner {
	case me:
		return 1
	case game.NoPlayer:
		return 0
	default:
		return -1
	}
}

// leafValue is the static score of a position for the side to move, squashed
// into [-1, 1]. The score itself comes from searcher.leaf, so the contender
// cannot drift away from the evaluation it is being compared against.
func (t *mctsTree) leafValue(an *analysis, me game.Player) float64 {
	return math.Tanh(float64(t.s.leaf(an, me)) / t.p.valueScale)
}

// mctsPriors turns ordering scores into a distribution over replies.
func mctsPriors(moves []scoredMove, tau float64, dst []float64) {
	top := moves[0].order
	for _, m := range moves[1:] {
		if m.order > top {
			top = m.order
		}
	}
	total := 0.0
	for i, m := range moves {
		w := math.Exp(float64(m.order-top) / tau)
		dst[i] = w
		total += w
	}
	for i := range dst {
		dst[i] /= total
	}
}

// addNode records a node with a candidate list, copying the list out of the
// generator's buffer.
func (t *mctsTree) addNode(n mctsNode, moves []scoredMove) int32 {
	n.moves = append(make([]scoredMove, 0, len(moves)), moves...)
	n.prior = make([]float64, len(moves))
	mctsPriors(n.moves, t.p.priorTau, n.prior)
	n.child = make([]int32, len(moves))
	for i := range n.child {
		n.child[i] = mctsUnexpanded
	}
	t.nodes = append(t.nodes, n)
	return int32(len(t.nodes) - 1)
}

// addTerminal records a node that is never expanded.
func (t *mctsTree) addTerminal(mover game.Player, ply int, value float64, proven bool) int32 {
	t.nodes = append(t.nodes, mctsNode{
		mover:    mover,
		ply:      ply,
		terminal: true,
		proven:   proven,
		exact:    value,
	})
	return int32(len(t.nodes) - 1)
}

var errMCTSUndo = errors.New("mcts: the engine refused to unplay a move")

// newNode reads the position at ply and records the node for it. g must hold
// that position.
func (t *mctsTree) newNode(g *game.Game, ply int) (int32, error) {
	an := &t.expAn
	t.s.analyse(an, g)
	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	n := mctsNode{mover: me, ply: ply}

	switch {
	case an.need[mine] == 1:
		// The side to move has a hole that finishes its chain. This is the
		// same exact test the alpha-beta search stops on, one ply before any
		// move is generated.
		n.terminal, n.proven, n.exact = true, true, 1
	case ply >= mctsMaxPly:
		// The tree may not grow here. The static score stands in for a value,
		// and proven stays false so no root decision rests on it.
		n.terminal, n.exact = true, t.leafValue(an, me)
	default:
		n.forced = an.need[theirs] == 1
		moves := t.s.generate(g, an, me, ply, n.forced)
		prefix, err := t.generationStopped()
		if err != nil {
			return 0, err
		}
		switch {
		case len(moves) > 0:
			return t.addNode(n, moves), nil
		case prefix:
			// The list was cut short, so its emptiness proves nothing. The
			// node keeps the static score, unproven, and is never expanded:
			// reading a truncated defence list as "no defence" would invent a
			// forced loss out of a cancellation.
			n.terminal, n.exact = true, t.leafValue(an, me)
		case n.forced:
			// Nothing answers the threat: the opponent connects next move.
			n.terminal, n.proven, n.exact = true, true, -1
		default:
			// Nowhere left to play, which the engine scores as a draw.
			n.terminal, n.proven, n.exact = true, true, 0
		}
	}
	t.nodes = append(t.nodes, n)
	return int32(len(t.nodes) - 1), nil
}

// allowed is how many of a node's replies the widening schedule admits.
func (t *mctsTree) allowed(n *mctsNode) int {
	if n.forced {
		return len(n.moves)
	}
	w := 1 + int(t.p.widenBase*math.Sqrt(float64(n.visits)))
	if w > len(n.moves) {
		w = len(n.moves)
	}
	return w
}

// selectSlot picks the reply to walk into: the best PUCT score inside the
// widening window, with ties going to the better-ordered reply so that the
// choice is a function of the position and the seed alone. The window is
// stretched past its limit only while nothing inside it can be played.
func (t *mctsTree) selectSlot(idx int32) (int, bool) {
	n := &t.nodes[idx]
	allowed := t.allowed(n)
	parentQ := 0.0
	if n.visits > 0 {
		parentQ = n.total / float64(n.visits)
	}
	sqrtN := math.Sqrt(float64(max(n.visits, 1)))

	bestScore, bestSlot := math.Inf(-1), -1
	for i := range n.moves {
		if i >= allowed && bestSlot >= 0 {
			break
		}
		kid := n.child[i]
		if kid == mctsIllegal {
			continue
		}
		visits := int32(0)
		q := parentQ - t.p.fpu
		if kid >= 0 {
			if c := &t.nodes[kid]; c.visits > 0 {
				visits = c.visits
				// A child's mean is its own mover's, so the parent's view of
				// it is the negation. This is the one sign that decides
				// whether the search is a search or noise.
				q = -c.total / float64(c.visits)
			}
		}
		u := t.p.exploration * n.prior[i] * sqrtN / float64(1+visits)
		if s := q + u; s > bestScore {
			bestScore, bestSlot = s, i
		}
	}
	return bestSlot, bestSlot >= 0
}

func (t *mctsTree) evalLimitHit() bool {
	return t.p.evalLimit > 0 && t.evaluations() >= t.p.evalLimit
}

// rollout plays a bounded, seeded continuation from the current position and
// returns its value for the side to move now. The continuation follows the
// ordering heuristic — sampled among its best few replies, and narrowed to the
// exact defence list whenever a side is one peg from finishing — so a rollout
// is a plausible line rather than random noise, and a line that finishes is
// scored exactly. The position is restored before returning, whatever happens.
func (t *mctsTree) rollout(ctx context.Context, g *game.Game, an *analysis) (value float64, err error) {
	played := 0
	defer func() {
		for range played {
			if uerr := g.UndoLastMove(); uerr != nil && err == nil {
				err = fmt.Errorf("%w: %v", errMCTSUndo, uerr)
			}
		}
	}()

	me0 := g.Turn()
	cur := an
	value = t.leafValue(cur, me0)
	for range t.p.rolloutPlies {
		if ctx.Err() != nil || t.evalLimitHit() {
			return value, nil
		}
		me := g.Turn()
		mine, theirs := sideIndex(me), sideIndex(me.Opponent())
		if cur.need[mine] == 1 {
			return mctsSign(me, me0), nil
		}
		forced := cur.need[theirs] == 1
		moves := t.s.generate(g, cur, me, mctsRollSlot, forced)
		prefix, gerr := t.generationStopped()
		if gerr != nil {
			return 0, gerr
		}
		if len(moves) == 0 {
			// A list cut short by cancellation says nothing about the
			// position, so the rollout ends on the value it already had
			// rather than on a verdict it has not earned.
			switch {
			case prefix:
				return value, nil
			case forced:
				return mctsSign(me.Opponent(), me0), nil
			default:
				return 0, nil
			}
		}
		res, perr := g.PlayPeg(moves[t.pickRollout(moves)].at)
		if perr != nil {
			return value, nil
		}
		played++
		if res.Over() {
			return mctsSign(res.Winner(), me0), nil
		}
		t.s.analyse(&t.rollAn, g)
		cur = &t.rollAn
		value = t.leafValue(cur, g.Turn())
		if g.Turn() != me0 {
			value = -value
		}
	}
	return value, nil
}

// pickRollout samples one of the best few replies in proportion to
// exp(order/tau), which keeps a rollout from being a single deterministic line
// while still following the ordering heuristic.
func (t *mctsTree) pickRollout(moves []scoredMove) int {
	top := min(t.p.rolloutTop, len(moves))
	if top <= 1 {
		return 0
	}
	best := moves[0].order
	for _, m := range moves[1:top] {
		if m.order > best {
			best = m.order
		}
	}
	weights := t.rollW[:0]
	total := 0.0
	for _, m := range moves[:top] {
		w := math.Exp(float64(m.order-best) / t.p.rolloutTau)
		weights = append(weights, w)
		total += w
	}
	t.rollW = weights
	r := t.rng.Float64() * total
	for i, w := range weights {
		r -= w
		if r <= 0 {
			return i
		}
	}
	return top - 1
}

// backprop credits the leaf's value along the path, flipping the sign at every
// ply so that every node's total stays in its own mover's terms.
func (t *mctsTree) backprop(leaf int32, value float64) {
	t.nodes[leaf].visits++
	t.nodes[leaf].total += value
	v := value
	for i := len(t.path) - 1; i >= 0; i-- {
		v = -v
		n := &t.nodes[t.path[i].node]
		n.visits++
		n.total += v
	}
}

// restore unplays the simulation's moves, leaving the caller's game exactly as
// it was.
func (t *mctsTree) restore(g *game.Game) error {
	var err error
	for range t.path {
		if uerr := g.UndoLastMove(); uerr != nil && err == nil {
			err = fmt.Errorf("%w: %v", errMCTSUndo, uerr)
		}
	}
	t.path = t.path[:0]
	return err
}

// simulate walks one simulation: select, expand, evaluate, back up.
func (t *mctsTree) simulate(ctx context.Context, g *game.Game) error {
	t.path = t.path[:0]
	cur := int32(0)
	var value float64
	for {
		if t.nodes[cur].terminal {
			value = t.nodes[cur].exact
			break
		}
		slot, ok := t.selectSlot(cur)
		if !ok {
			// Every reply turned out to be unplayable, which is the same
			// nowhere-left-to-play the engine scores as a draw.
			n := &t.nodes[cur]
			n.terminal, n.proven, n.exact = true, true, 0
			value = 0
			break
		}
		mv := t.nodes[cur].moves[slot]
		res, err := g.PlayPeg(mv.at)
		if err != nil {
			// A node's position never changes, so this cannot happen for a
			// generated candidate; marking the slot keeps a corrupted list
			// from looping rather than trusting that it cannot.
			t.nodes[cur].child[slot] = mctsIllegal
			continue
		}
		t.path = append(t.path, mctsStep{node: cur, slot: slot})
		if len(t.path) > t.maxDepth {
			t.maxDepth = len(t.path)
		}
		if kid := t.nodes[cur].child[slot]; kid >= 0 {
			cur = kid
			continue
		}

		var kid int32
		if res.Over() {
			// The move finished the game: an exact result, from the point of
			// view of the side that would have replied.
			next := t.nodes[cur].mover.Opponent()
			kid = t.addTerminal(next, len(t.path), mctsSign(res.Winner(), next), true)
			value = t.nodes[kid].exact
		} else {
			kid, err = t.newNode(g, len(t.path))
			if err != nil {
				return errors.Join(err, t.restore(g))
			}
			if t.nodes[kid].terminal {
				value = t.nodes[kid].exact
			} else if value, err = t.rollout(ctx, g, &t.expAn); err != nil {
				return errors.Join(err, t.restore(g))
			}
		}
		t.nodes[cur].child[slot] = kid
		cur = kid
		break
	}
	t.backprop(cur, value)
	return t.restore(g)
}

// bestRootMove is the most-visited root reply, which is the standard Monte
// Carlo answer, with one override: a proven result beats a visit count. A
// reply that leaves the opponent proven lost is played at once, and a reply
// proven lost for us is passed over while any alternative remains. Only
// game-theoretic results count as proof; a node stopped at the depth limit
// carries a static score and is treated as an estimate like any other.
func (t *mctsTree) bestRootMove() game.Point {
	n := &t.nodes[0]
	fallback, bestSlot := -1, -1
	bestVisits := int32(-1)
	for i := range n.moves {
		kid := n.child[i]
		if kid == mctsIllegal {
			continue
		}
		if fallback < 0 {
			fallback = i
		}
		visits := int32(0)
		if kid >= 0 {
			c := &t.nodes[kid]
			if c.proven {
				switch {
				case c.exact <= -1:
					return n.moves[i].at
				case c.exact >= 1:
					continue
				}
			}
			visits = c.visits
		}
		if visits > bestVisits {
			bestVisits, bestSlot = visits, i
		}
	}
	switch {
	case bestSlot >= 0:
		return n.moves[bestSlot].at
	case fallback >= 0:
		return n.moves[fallback].at
	}
	return n.moves[0].at
}

func (t *mctsTree) snapshot(sims int, reason string) mctsStats {
	st := mctsStats{
		Simulations: sims,
		Evaluations: t.evaluations(),
		Nodes:       len(t.nodes),
		MaxDepth:    t.maxDepth,
		StopReason:  reason,
	}
	if len(t.nodes) == 0 {
		return st
	}
	root := &t.nodes[0]
	st.RootVisits = int(root.visits)
	if root.visits > 0 {
		st.RootValue = root.total / float64(root.visits)
	}
	for _, kid := range root.child {
		if kid >= 0 {
			st.RootExpanded++
		}
	}
	return st
}

// experimentMCTS runs the contender for the side to move and returns the hole
// it would play together with how many positions it analysed — the count
// searcher.analyse keeps, tactical probes included, so it is the same unit the
// alpha-beta search reports and the currency an equal-work comparison between
// the two is settled in.
//
// The answer is a function of the position, the seed and the simulation count:
// no clock and no machine speed enters into it, which is what lets a
// deterministic comparison be repeated. A cancelled context stops the search
// and yields the best move reached so far — the ordering heuristic's choice if
// nothing was reached — rather than an error, matching Bot.Move. An error means
// the position genuinely has no move. g is left exactly as it was found.
func experimentMCTS(ctx context.Context, g *game.Game, seed int64, simulations int) (game.Point, int64, error) {
	move, st, err := experimentMCTSStats(ctx, g, seed, simulations, defaultMCTSParams())
	return move, st.Evaluations, err
}

// experimentMCTSStats is experimentMCTS with the levers exposed and the work
// reported, for the harnesses that vary them.
func experimentMCTSStats(ctx context.Context, g *game.Game, seed int64, simulations int, p mctsParams) (game.Point, mctsStats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g == nil {
		return game.Point{}, mctsStats{}, errors.New("mcts: no game")
	}
	if g.Result().Over() {
		return game.Point{}, mctsStats{}, game.ErrGameOver
	}
	if simulations < 0 {
		return game.Point{}, mctsStats{}, fmt.Errorf("mcts: simulations %d is negative", simulations)
	}
	if err := p.validate(); err != nil {
		return game.Point{}, mctsStats{}, err
	}
	me := g.Turn()
	if !g.HasLegalPlacement(me) {
		return game.Point{}, mctsStats{}, ErrNoMove
	}

	t := newMCTSTree(p)
	t.s.prepare(g.Size())
	t.s.ctx = ctx
	t.s.stopped = false
	t.s.stopReason = ""
	t.s.evaluations = 0
	// No hidden wall clock: simulation-bounded runs stop only on their
	// requested work or the caller's context, including its deadline.
	t.s.deadline = time.Time{}

	an := &t.expAn
	t.s.analyse(an, g)
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	if an.need[mine] == 1 {
		// The engine's own answer, taken without searching: a simulation
		// budget must not be able to talk the bot out of a win in one.
		hole, ok := an.winningHole(mine)
		if !ok {
			return game.Point{}, t.snapshot(0, stopImmediate), ErrNoMove
		}
		return hole, t.snapshot(0, stopImmediate), nil
	}
	// The seed is folded together with the position hash, exactly as the
	// engine does when it samples, so the same position draws the same
	// rollouts wherever in a game it turns up.
	t.rng = rand.New(rand.NewPCG(uint64(seed), an.hash))

	forced := an.need[theirs] == 1
	moves := t.s.generate(g, an, me, 0, forced)
	if len(moves) == 0 {
		// Either there was no threat, or listing the answers to it was cut
		// short or found none. The bot still has to play, so fall back to the
		// ordinary candidate list as the alpha-beta root does; that list is
		// built without probing, so a cancelled context still leaves a legal
		// move to hand back.
		forced = false
		moves = t.s.candidates(g, an, me, 0)
	}
	if _, err := t.generationStopped(); err != nil {
		return game.Point{}, t.snapshot(0, t.s.stopReason), err
	}
	if len(moves) == 0 {
		return game.Point{}, t.snapshot(0, "none"), ErrNoMove
	}
	t.addNode(mctsNode{mover: me, ply: 0, forced: forced}, moves)

	reason := "simulations"
	if simulations == 0 {
		reason = "none"
	}
	sims := 0
	for sims < simulations {
		// Both guards are checked before a simulation starts, so a cancelled
		// context or an exhausted evaluation budget costs no further work.
		if ctx.Err() != nil {
			reason = stopCanceled
			break
		}
		if t.evalLimitHit() {
			reason = "evalLimit"
			break
		}
		if err := t.simulate(ctx, g); err != nil {
			return game.Point{}, t.snapshot(sims, reason), err
		}
		sims++
	}
	return t.bestRootMove(), t.snapshot(sims, reason), nil
}

// --- tests -----------------------------------------------------------------

// mctsRules is the paper-and-pencil ruleset at a given size with the swap
// option off, so a fixture's move list means one position and no other.
func mctsRules(size int) game.Ruleset {
	rs := game.PP
	rs.Size = size
	rs.Swap = false
	return rs
}

func mctsPlay(t testing.TB, g *game.Game, moves ...string) {
	t.Helper()
	for _, m := range moves {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("PlayNotation(%q): %v\n%s", m, err, g)
		}
	}
}

// mctsRandomGame plays up to plies random legal moves.
func mctsRandomGame(t testing.TB, size, plies int, src *rand.Rand) *game.Game {
	t.Helper()
	g := game.MustNew(mctsRules(size))
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

// mctsWinThreat builds an 8x8 position where Vertical is one peg from joining
// the top and bottom rows, with Vertical to move. Its chain runs
// B1-C3-B5-C7-E8, each step a knight's move, and C3 is left out: C3 is the only
// hole that joins B1 to the rest, so C3 wins at once and taking C3 away is the
// only way to stop it. Horizontal answers in column G, where its pegs are one
// row apart and so never link to each other and never block anything.
func mctsWinThreat(t testing.TB) *game.Game {
	t.Helper()
	g := game.MustNew(mctsRules(8))
	mctsPlay(t, g, "B1", "G2", "B5", "G3", "C7", "G4", "E8", "G5")
	return g
}

// mctsLopsided is the same shape two pegs short: Vertical needs C3 and E8, so
// it is two pegs from finishing while Horizontal has made no progress at all.
func mctsLopsided(t testing.TB) *game.Game {
	t.Helper()
	g := game.MustNew(mctsRules(8))
	mctsPlay(t, g, "B1", "G2", "B5", "G3", "C7", "G4")
	return g
}

// mctsNeed reports how many pegs a side still needs, so a fixture can assert
// what it is a fixture of.
func mctsNeed(t testing.TB, g *game.Game, pl game.Player) int {
	t.Helper()
	var a analysis
	a.load(g)
	return a.need[sideIndex(pl)]
}

// mctsPositionKey is everything about a position that a search must not
// change: the moves that made it, whose turn it is, and how it renders.
func mctsPositionKey(t testing.TB, g *game.Game) string {
	t.Helper()
	tr, err := g.Transcript()
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	return fmt.Sprintf("%s | turn %v | entries %d | %v\n%s", tr, g.Turn(), g.Entries(), g.Result(), g)
}

func TestExperimentMCTSTakesImmediateWin(t *testing.T) {
	g := mctsWinThreat(t)
	if g.Turn() != game.Vertical {
		t.Fatalf("expected Vertical to move, got %v", g.Turn())
	}
	if need := mctsNeed(t, g, game.Vertical); need != 1 {
		t.Fatalf("fixture is not a one-move win: Vertical needs %d pegs\n%s", need, g)
	}
	// A budget of one simulation is far too small to find a win by searching,
	// and a budget of ten thousand must not be spent looking for one that is
	// already there: an immediate win is the root policy's, taken for the one
	// evaluation that found it, which is also what the harness's work
	// accounting depends on.
	for _, sims := range []int{0, 1, 10000} {
		move, st, err := experimentMCTSStats(context.Background(), g, 3, sims, defaultMCTSParams())
		if err != nil {
			t.Fatalf("experimentMCTSStats(sims=%d): %v", sims, err)
		}
		probe := g.Clone()
		res, err := probe.PlayPeg(move)
		if err != nil {
			t.Fatalf("sims=%d: returned unplayable %v: %v", sims, move, err)
		}
		if res.Winner() != game.Vertical {
			t.Errorf("sims=%d: played %v, which does not win\n%s", sims, move, g)
		}
		if st.StopReason != stopImmediate {
			t.Errorf("sims=%d: StopReason = %q, want the win taken without searching", sims, st.StopReason)
		}
		if st.Simulations != 0 || st.Evaluations != 1 {
			t.Errorf("sims=%d: the win cost %d simulations and %d evaluations, want 0 and 1",
				sims, st.Simulations, st.Evaluations)
		}
	}
}

func TestExperimentMCTSAnswersImmediateThreat(t *testing.T) {
	g := mctsWinThreat(t)
	// Hand the move to Horizontal by playing a Vertical peg far from the
	// threat, which leaves the threat standing.
	mctsPlay(t, g, "F1")
	if g.Turn() != game.Horizontal {
		t.Fatalf("expected Horizontal to move, got %v", g.Turn())
	}
	if need := mctsNeed(t, g, game.Vertical); need != 1 {
		t.Fatalf("fixture no longer threatens: Vertical needs %d pegs\n%s", need, g)
	}
	for _, sims := range []int{1, 8, 128} {
		move, _, err := experimentMCTS(context.Background(), g, 5, sims)
		if err != nil {
			t.Fatalf("experimentMCTS(sims=%d): %v", sims, err)
		}
		probe := g.Clone()
		if _, err := probe.PlayPeg(move); err != nil {
			t.Fatalf("sims=%d: returned unplayable %v: %v", sims, move, err)
		}
		if need := mctsNeed(t, probe, game.Vertical); need < 2 {
			t.Errorf("sims=%d: played %v and left Vertical needing %d peg; the threat was not answered\n%s",
				sims, move, need, probe)
		}
	}
}

// mctsWinInTwo is an 8x8 position where Vertical, to move, is two pegs from
// finishing and E6 leaves Horizontal with no reply at all: after E6 every hole
// Horizontal may take still leaves Vertical one peg from a finished chain. Both
// sides need two pegs, so a search that only counted pegs would have no reason
// to prefer it. It was found by enumeration and the test re-derives the claim
// by playing every legal reply, so the fixture does not rest on the same
// routine the search uses to narrow defences.
func mctsWinInTwo(t testing.TB) (*game.Game, game.Point) {
	t.Helper()
	g := game.MustNew(mctsRules(8))
	mctsPlay(t, g, "C8", "H5", "B3", "G7", "F8", "B4", "G5", "F7", "D4", "C6", "E1", "H6")
	return g, game.Point{Col: 4, Row: 5}
}

// TestExperimentMCTSFindsAWinInTwo is the exact-terminal machinery end to end:
// the win is one ply beyond the root policy, so it can only come from the
// search recording a threat with no defence as a proven result and backing it
// up, rather than from a rollout happening to like the position.
func TestExperimentMCTSFindsAWinInTwo(t *testing.T) {
	g, killer := mctsWinInTwo(t)
	if g.Turn() != game.Vertical {
		t.Fatalf("expected Vertical to move, got %v", g.Turn())
	}
	if got := mctsNeed(t, g, game.Vertical); got != 2 {
		t.Fatalf("fixture: Vertical needs %d pegs, want 2 so the root policy cannot answer\n%s", got, g)
	}
	after := g.Clone()
	if _, err := after.PlayPeg(killer); err != nil {
		t.Fatalf("fixture: killer %v is unplayable: %v\n%s", killer, err, g)
	}
	if got := mctsNeed(t, after, game.Vertical); got != 1 {
		t.Fatalf("fixture: %v leaves Vertical needing %d pegs, want 1\n%s", killer, got, after)
	}
	for _, reply := range after.LegalPlacements(after.Turn()) {
		probe := after.Clone()
		res, err := probe.PlayPeg(reply)
		if err != nil {
			continue
		}
		if res.Over() || mctsNeed(t, probe, game.Vertical) != 1 {
			t.Fatalf("fixture: %v answers the threat, so %v is not a win in two\n%s", reply, killer, probe)
		}
	}

	for _, sims := range []int{20, 200} {
		move, st, err := experimentMCTSStats(context.Background(), g, 17, sims, defaultMCTSParams())
		if err != nil {
			t.Fatalf("experimentMCTSStats(sims=%d): %v", sims, err)
		}
		if move != killer {
			t.Errorf("sims=%d: played %v, want the unanswerable %v\n%s", sims, move, killer, g)
		}
		if st.StopReason != "simulations" {
			t.Errorf("sims=%d: StopReason = %q; the win is two plies away and has to be searched for",
				sims, st.StopReason)
		}
	}
}

// TestExperimentMCTSValueFollowsTheSideToMove pins the sign convention that
// separates a search from noise: a node's value is always the value for the
// side to move at that node. The same lopsided position is read from both
// sides, and the two readings must have opposite signs.
func TestExperimentMCTSValueFollowsTheSideToMove(t *testing.T) {
	ahead := mctsLopsided(t)
	if got := mctsNeed(t, ahead, game.Vertical); got != 2 {
		t.Fatalf("fixture: Vertical needs %d pegs, want 2\n%s", got, ahead)
	}
	if got := mctsNeed(t, ahead, game.Horizontal); got < 4 {
		t.Fatalf("fixture: Horizontal needs only %d pegs, want a side with no progress\n%s", got, ahead)
	}
	if ahead.Turn() != game.Vertical {
		t.Fatalf("expected Vertical to move, got %v", ahead.Turn())
	}

	behind := mctsLopsided(t)
	// A Vertical peg on its own border row, far from its plan, hands the move
	// over without changing what either side needs.
	mctsPlay(t, behind, "E1")
	if got := mctsNeed(t, behind, game.Vertical); got != 2 {
		t.Fatalf("handing over the move changed the position: Vertical needs %d pegs\n%s", got, behind)
	}
	if behind.Turn() != game.Horizontal {
		t.Fatalf("expected Horizontal to move, got %v", behind.Turn())
	}

	_, aheadStats, err := experimentMCTSStats(context.Background(), ahead, 9, 160, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats(ahead): %v", err)
	}
	_, behindStats, err := experimentMCTSStats(context.Background(), behind, 9, 160, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats(behind): %v", err)
	}
	if aheadStats.RootValue <= 0 {
		t.Errorf("the side two pegs from finishing values the position at %+.3f, want positive",
			aheadStats.RootValue)
	}
	if behindStats.RootValue >= 0 {
		t.Errorf("the side with no progress values the position at %+.3f, want negative",
			behindStats.RootValue)
	}
}

// TestExperimentMCTSGrowsSurerOfADecidedPosition covers the other half of the
// sign convention: not just that a value is read from the right side, but that
// selection walks towards the replies that are good for the side to move. A
// search that explored its worst replies instead would still build a tree and
// still read its root the right way round, and its root value would drift away
// from the truth as the budget grew instead of towards it.
//
// The fixture is two pegs from a win against a side with no chain at all, so
// the truth is close to +1, and the reply played must be one that actually
// makes that progress.
func TestExperimentMCTSGrowsSurerOfADecidedPosition(t *testing.T) {
	g := mctsLopsided(t)
	if got := mctsNeed(t, g, game.Vertical); got != 2 {
		t.Fatalf("fixture: Vertical needs %d pegs, want 2\n%s", got, g)
	}
	small, large := 40, 400
	_, smallStats, err := experimentMCTSStats(context.Background(), g, 13, small, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats(%d): %v", small, err)
	}
	move, largeStats, err := experimentMCTSStats(context.Background(), g, 13, large, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats(%d): %v", large, err)
	}
	if largeStats.RootValue <= smallStats.RootValue {
		t.Errorf("root value went from %+.3f at %d simulations to %+.3f at %d; a decided position must "+
			"look better the longer it is searched, not worse",
			smallStats.RootValue, small, largeStats.RootValue, large)
	}
	if largeStats.RootValue < 0.8 {
		t.Errorf("root value %+.3f after %d simulations in a position two pegs from a win against a side "+
			"with no chain", largeStats.RootValue, large)
	}
	probe := g.Clone()
	if _, err := probe.PlayPeg(move); err != nil {
		t.Fatalf("returned unplayable %v: %v", move, err)
	}
	if got := mctsNeed(t, probe, game.Vertical); got != 1 {
		t.Errorf("played %v, which leaves Vertical needing %d pegs; the search did not take the win nearer",
			move, got)
	}
}

// TestExperimentMCTSVerdictOverridesOrdering is what stops this being a greedy
// move picker wearing the name of a search: the answer must be the search's
// verdict, not the ordering heuristic's. A budget of zero returns the ordering
// choice by construction, so the two can be compared directly, and over a
// spread of positions the search has to disagree with the heuristic sometimes.
// How often is not the point and is no claim about which is stronger; that a
// search which never overrules its prior would be measuring nothing is.
func TestExperimentMCTSVerdictOverridesOrdering(t *testing.T) {
	src := rand.New(rand.NewPCG(201, 202))
	searched, differed := 0, 0
	for i := range 10 {
		g := mctsRandomGame(t, 10, 6+2*i, src)
		if g.Result().Over() {
			continue
		}
		ordering, _, err := experimentMCTS(context.Background(), g, 77, 0)
		if err != nil {
			t.Fatalf("position %d, ordering: %v", i, err)
		}
		verdict, _, err := experimentMCTS(context.Background(), g, 77, 240)
		if err != nil {
			t.Fatalf("position %d, search: %v", i, err)
		}
		searched++
		if ordering != verdict {
			differed++
		}
	}
	if searched < 10 {
		t.Fatalf("only %d of 10 fixtures were live positions", searched)
	}
	if differed < 3 {
		t.Errorf("the search agreed with the ordering heuristic in %d of %d positions; its simulations are "+
			"not deciding the move", searched-differed, searched)
	}
}

// TestExperimentMCTSSearches checks the shape of the work the simulations do:
// a tree that grows, looks past one ply, spreads over more than one root reply,
// and grows further when the budget does.
func TestExperimentMCTSSearches(t *testing.T) {
	g := mctsRandomGame(t, 10, 12, rand.New(rand.NewPCG(101, 102)))
	if g.Result().Over() {
		t.Fatal("fixture finished before the search could run")
	}
	const sims = 240
	_, st, err := experimentMCTSStats(context.Background(), g, 11, sims, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats: %v", err)
	}
	if st.Simulations != sims {
		t.Errorf("ran %d of %d simulations, StopReason %q", st.Simulations, sims, st.StopReason)
	}
	if st.RootVisits != sims {
		t.Errorf("root has %d visits after %d simulations; every simulation must back up to the root",
			st.RootVisits, sims)
	}
	if st.MaxDepth < 2 {
		t.Errorf("deepest simulation reached ply %d; a one-ply tree is a move picker, not a search", st.MaxDepth)
	}
	if st.RootExpanded < 2 {
		t.Errorf("only %d root reply expanded; the search never explored an alternative", st.RootExpanded)
	}
	if st.Nodes <= st.RootExpanded+1 {
		t.Errorf("tree holds %d nodes for %d root replies; nothing was searched below the root replies",
			st.Nodes, st.RootExpanded)
	}
	// Evaluations is the currency an equal-work comparison is settled in, and
	// it has to include the rollout plies, not just the nodes expanded, or a
	// comparison against the alpha-beta search would be handing this
	// contender most of its work for free.
	if st.Evaluations <= int64(st.Simulations) {
		t.Errorf("%d evaluations over %d simulations; the rollout plies are not being counted",
			st.Evaluations, st.Simulations)
	}
	if st.StopReason != "simulations" {
		t.Errorf("StopReason = %q, want the simulation budget to be what stopped it", st.StopReason)
	}

	// More simulations must buy more search, or the budget means nothing.
	_, small, err := experimentMCTSStats(context.Background(), g, 11, 40, defaultMCTSParams())
	if err != nil {
		t.Fatalf("experimentMCTSStats(40): %v", err)
	}
	if small.Nodes >= st.Nodes || small.Evaluations >= st.Evaluations {
		t.Errorf("40 simulations built %d nodes over %d evaluations, %d simulations built %d over %d; "+
			"the budget does not buy search", small.Nodes, small.Evaluations, sims, st.Nodes, st.Evaluations)
	}
}

// TestExperimentMCTSIsDeterministic holds the contender to the same standard
// as the depth-bounded tiers: the same position, seed and simulation count must
// give the same move and the same amount of work on any machine, since a
// comparison that cannot be repeated cannot be evidence.
func TestExperimentMCTSIsDeterministic(t *testing.T) {
	src := rand.New(rand.NewPCG(103, 104))
	for _, size := range []int{8, 10, 16} {
		g := mctsRandomGame(t, size, 3*size, src)
		if g.Result().Over() {
			continue
		}
		first, firstStats, err := experimentMCTSStats(context.Background(), g, 21, 96, defaultMCTSParams())
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		second, secondStats, err := experimentMCTSStats(context.Background(), g, 21, 96, defaultMCTSParams())
		if err != nil {
			t.Fatalf("size %d, repeat: %v", size, err)
		}
		if first != second {
			t.Errorf("size %d: same seed and budget gave %v then %v", size, first, second)
		}
		if firstStats != secondStats {
			t.Errorf("size %d: same seed and budget did %+v then %+v", size, firstStats, secondStats)
		}
	}
}

// TestExperimentMCTSLeavesTheGameUntouched covers the two things every caller
// depends on: the hole handed back can actually be played, and the game handed
// in is exactly as it was, links and turn included, however deep the search
// went into it.
func TestExperimentMCTSLeavesTheGameUntouched(t *testing.T) {
	src := rand.New(rand.NewPCG(105, 106))
	checked := 0
	for _, size := range []int{8, 10, 16} {
		for _, plies := range []int{0, size, 3 * size} {
			g := mctsRandomGame(t, size, plies, src)
			if g.Result().Over() {
				continue
			}
			me := g.Turn()
			before := mctsPositionKey(t, g)
			move, evals, err := experimentMCTS(context.Background(), g, int64(size*100+plies), 64)
			if err != nil {
				t.Fatalf("size %d, %d plies: %v", size, plies, err)
			}
			if err := g.CanPlace(me, move); err != nil {
				t.Errorf("size %d, %d plies: returned illegal %v: %v", size, plies, move, err)
			}
			if after := mctsPositionKey(t, g); after != before {
				t.Errorf("size %d, %d plies: the search changed the game\nbefore:\n%s\nafter:\n%s",
					size, plies, before, after)
			}
			if evals == 0 {
				t.Errorf("size %d, %d plies: no position was evaluated", size, plies)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no position was searched")
	}
}

func TestExperimentMCTSHonoursCancellation(t *testing.T) {
	g := mctsRandomGame(t, 16, 30, rand.New(rand.NewPCG(107, 108)))
	if g.Result().Over() {
		t.Fatal("fixture finished before the search could run")
	}
	me := g.Turn()
	before := mctsPositionKey(t, g)

	// Already cancelled: a legal move still comes back, chosen by the ordering
	// heuristic, and nothing is searched.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	move, st, err := experimentMCTSStats(dead, g, 31, 1_000_000, defaultMCTSParams())
	if err != nil {
		t.Fatalf("cancelled search: %v", err)
	}
	if err := g.CanPlace(me, move); err != nil {
		t.Errorf("cancelled search returned illegal %v: %v", move, err)
	}
	if st.Simulations != 0 {
		t.Errorf("cancelled search ran %d simulations", st.Simulations)
	}
	if st.StopReason != stopCanceled {
		t.Errorf("StopReason = %q, want %q", st.StopReason, stopCanceled)
	}
	if after := mctsPositionKey(t, g); after != before {
		t.Errorf("cancelled search changed the game\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// Cancelled while running: it must stop promptly, hand back a legal move
	// from the work it did finish, and leave the game alone.
	timed, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	start := time.Now()
	move, st, err = experimentMCTSStats(timed, g, 31, 1_000_000, defaultMCTSParams())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("timed search: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("timed search took %v to notice a 20ms deadline", elapsed)
	}
	if err := g.CanPlace(me, move); err != nil {
		t.Errorf("timed search returned illegal %v: %v", move, err)
	}
	if st.StopReason != stopCanceled {
		t.Errorf("StopReason = %q, want %q", st.StopReason, stopCanceled)
	}
	if st.Simulations >= 1_000_000 {
		t.Errorf("timed search claims %d simulations inside 20ms", st.Simulations)
	}
	if after := mctsPositionKey(t, g); after != before {
		t.Errorf("timed search changed the game\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestExperimentMCTSHonoursTheEvaluationBudget covers the lever an equal-work
// comparison is run on: the search must stop on the analysis count itself,
// not on a simulation count calibrated to approximate it. The count is the
// searcher's own, tactical probes included, so it is the same quantity the
// alpha-beta side reports.
func TestExperimentMCTSHonoursTheEvaluationBudget(t *testing.T) {
	g := mctsRandomGame(t, 10, 20, rand.New(rand.NewPCG(109, 110)))
	if g.Result().Over() {
		t.Fatal("fixture finished before the search could run")
	}
	me := g.Turn()
	p := defaultMCTSParams()
	p.evalLimit = 400
	move, st, err := experimentMCTSStats(context.Background(), g, 41, 1_000_000, p)
	if err != nil {
		t.Fatalf("experimentMCTSStats: %v", err)
	}
	// The budget is read at simulation and rollout-ply boundaries, so the
	// analyses of one move generation can land past it. The overshoot is
	// bounded by the candidates a single defence list confirms, and no list
	// can hold more holes than the board has.
	board := int64(g.Size() * g.Size())
	if st.Evaluations > p.evalLimit+board {
		t.Errorf("spent %d analyses against a budget of %d, more than one move generation past it",
			st.Evaluations, p.evalLimit)
	}
	if st.Evaluations < p.evalLimit/2 {
		t.Errorf("spent only %d of %d evaluations; the budget stopped the search far too early",
			st.Evaluations, p.evalLimit)
	}
	if st.StopReason != "evalLimit" {
		t.Errorf("StopReason = %q, want evalLimit", st.StopReason)
	}
	if st.Simulations == 0 {
		t.Error("the budget bought no simulations at all")
	}
	if err := g.CanPlace(me, move); err != nil {
		t.Errorf("returned illegal %v: %v", move, err)
	}
}

func TestExperimentMCTSRefusesPositionsWithNoMove(t *testing.T) {
	g := game.MustNew(mctsRules(8))
	if err := g.Resign(game.Vertical); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if _, _, err := experimentMCTS(context.Background(), g, 1, 16); err == nil {
		t.Error("searched a finished game")
	}
	if _, _, err := experimentMCTS(context.Background(), nil, 1, 16); err == nil {
		t.Error("searched a nil game")
	}
	live := mctsRandomGame(t, 8, 4, rand.New(rand.NewPCG(111, 112)))
	if _, _, err := experimentMCTS(context.Background(), live, 1, -1); err == nil {
		t.Error("accepted a negative simulation count")
	}
	bad := defaultMCTSParams()
	bad.width = 0
	if _, _, err := experimentMCTSStats(context.Background(), live, 1, 16, bad); err == nil {
		t.Error("accepted a zero candidate width")
	}
}

// TestExperimentMCTSPlaysAGame is the harness precondition: the contender has
// to be able to play move after move from its own positions, not just answer
// one prepared fixture.
func TestExperimentMCTSPlaysAGame(t *testing.T) {
	g := game.MustNew(mctsRules(8))
	ctx := context.Background()
	total := int64(0)
	moves := 0
	for ply := range 16 {
		if g.Result().Over() {
			break
		}
		move, evals, err := experimentMCTS(ctx, g, int64(ply)+1, 32)
		if err != nil {
			t.Fatalf("ply %d: %v\n%s", ply, err, g)
		}
		if _, err := g.PlayPeg(move); err != nil {
			t.Fatalf("ply %d: returned unplayable %v: %v\n%s", ply, move, err, g)
		}
		total += evals
		moves++
	}
	if moves < 16 && !g.Result().Over() {
		t.Errorf("played only %d moves without finishing the game", moves)
	}
	if total == 0 {
		t.Error("a whole game evaluated no positions")
	}
}
