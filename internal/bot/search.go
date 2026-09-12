package bot

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// One alpha-beta search serves every tier. The tiers differ only in the params
// below: how long they may think, how many nodes they may enter, how deep they
// may look, how many candidate moves they keep per node, whether they can see
// route redundancy at all, whether they search the moves after the first with a
// null window, and whether they sample instead of playing the best move found.
//
// Two things are true of every tier, because a bot that walks into a one-move
// loss or misses a one-move win looks broken rather than weak:
//
//   - A side one peg from a finished chain is detected before any move is
//     generated, so the win is always taken.
//   - When the opponent is one peg from a finished chain, the candidate list is
//     narrowed to the moves that actually answer it, and that list is exact:
//     any other move leaves the winning hole empty with its links available.

const (
	// winScore values a won position above anything the static evaluation can
	// reach, so a win is never traded for shape. It is reduced by the ply at
	// which it is found, which makes the search prefer the quicker win and the
	// slower loss.
	winScore = 1 << 22
	// infScore bounds the alpha-beta window.
	infScore = 1 << 24
	// maxSearchPly bounds recursion so threat extensions cannot run away.
	maxSearchPly = 48
	// decidedScore is the floor above which a score means a forced win.
	decidedScore = winScore - maxSearchPly
)

// Why a search stopped. A search that ran to its own end reports depth or
// decided; every other reason means it was cut short, and what the caller gets
// is the last iteration that finished rather than the one that was running.
const (
	// stopDepth means every iteration the tier allows was finished.
	stopDepth = "depth"
	// stopDecided means the search found a forced win inside the moves it
	// looked at and stopped deepening, since a forced win does not improve
	// with depth. It is a statement about the tree that was searched and not
	// about the position: the width caps discard candidate moves, so a defence
	// the ordering threw away is a defence the search never saw.
	stopDecided = "decided"
	// stopImmediate means the side to move had a winning hole and took it
	// without searching.
	stopImmediate = "immediate"
	// stopTime means the wall-clock budget or the context deadline ran out.
	stopTime = "time"
	// stopNodes means the node budget was spent.
	stopNodes = "nodes"
	// stopCanceled means the context was canceled.
	stopCanceled = "canceled"
	// stopNoMove means the position had nowhere to play.
	stopNoMove = "no-move"
	// stopError means the engine could not restore the position after a trial
	// move. The search's own numbers cannot be trusted after that, which is
	// why it is a reason of its own rather than an ordinary stop.
	stopError = "error"
)

// params are the effort levers. Every tier shares one engine and differs only
// here.
type params struct {
	// budget is a wall-time guard checked at work boundaries. Small depth
	// ceilings normally finish first; elapsed time can exceed the guard by
	// the work between checks.
	budget time.Duration
	// maxDepth caps iterative deepening; a larger horizon is more effort, not
	// a guarantee of better play.
	maxDepth int
	// width is how many candidate moves survive ordering at an interior node.
	width int
	// rootWidth is the same for the move actually being chosen.
	rootWidth int
	// fullEval admits the bottleneck and ground terms into the leaf score. With
	// it off the bot only counts pegs, which is the impoverished evaluation the
	// beginner tier plays on: it can see who is ahead in the race but not
	// whether a plan can be cut or who holds more of the board.
	fullEval bool
	// useTable enables the transposition table.
	useTable bool
	// extend is the threat-extension budget in plies: how often a line may be
	// deepened past the nominal depth to resolve a forced defence.
	extend int
	// temperature, in pegs, spreads the root choice over near-best moves
	// instead of taking the best. Zero plays the best move found.
	temperature float64
	// nodeLimit caps how many nodes one search may enter, counted across every
	// iteration of the deepening loop. Zero means no limit. It exists so that
	// two configurations can be given the same work rather than the same time:
	// a clock budget measures the machine as much as the search, whereas a node
	// budget stops at the same node on any machine, which is what makes a
	// comparison between two searches repeatable.
	nodeLimit int64
	// pvs enables principal variation search at interior nodes: once one move
	// has a score, the others are asked only whether they beat it, and only a
	// move that says yes is searched again properly. With the same move set it
	// returns the same value as plain alpha-beta, so it is a speed lever rather
	// than a strength one -- but it changes which cutoffs happen, and cutoffs
	// feed the history heuristic that decides which moves survive the width
	// cap, so a narrow search can still end up choosing differently.
	pvs bool
	// killers enables the killer-move heuristic: each ply remembers the last
	// two holes that produced a beta cutoff there, and tries them first at the
	// next node it reaches at the same ply. Siblings of a node tend to be
	// refuted by the same reply, so the refutation found once is worth trying
	// before anything else -- and the earlier a cutoff comes, the fewer
	// children the node has to search.
	//
	// It only ever reorders the candidate list a node already has: a
	// remembered hole that is not among this node's candidates is skipped
	// rather than added, so the moves searched are the same set in a different
	// order. That is not the same as leaving the search unchanged. Which
	// cutoffs happen decides what the history heuristic learns, the history
	// feeds the ordering score, and the ordering score decides which moves
	// survive the width cap at later nodes, so a narrow search can end up
	// looking at a different candidate set further down. At full width the
	// move set is the position's own and only the cost may differ.
	killers bool
	// aspiration narrows the window the root's first move is searched with,
	// from the whole scale to a band around what the previous iteration
	// concluded, and widens the failing side back to the end of the scale
	// when the value turns out to lie outside. A root score is therefore only
	// ever taken from a search whose window contained it and never from a
	// bound.
	//
	// It is off in every tier, because it was measured and it does not pay
	// here. Aspiration rests on consecutive iterations agreeing closely, and
	// in this evaluation they do not: the leaf score is dominated by a term
	// quantised in pegs, and the root score moves by more than two pegs
	// between one iteration and the next on a third of them. The band
	// therefore fails often, and every failure re-searches the root's largest
	// subtree. At a fixed depth of six on the effort harness's frozen
	// positions the band spent 3.4% more nodes than the whole window, and 13.6%
	// more at depth eight on 10x10 with the two-peg band; of twenty band
	// settings between one and forty-eight pegs the best saved 0.02%, which is
	// noise, and every other cost nodes. The lever is kept, off, so that the
	// measurement can be repeated -- docs/MANUAL.md records it -- and not
	// because it is worth turning on.
	aspiration bool
	// templates lets the evaluation discount a step of a cheapest chain that a
	// proved edge template shows the opponent cannot take away. See
	// templates.go for what is proved and templates_prove_test.go for the
	// proofs; the lever exists so that a measurement can run the same search
	// with the corpus out of the evaluation.
	templates bool
}

// scoredMove is one candidate placement: its ordering score before the search
// and its search score after.
type scoredMove struct {
	at    game.Point
	hole  int32
	order int32
	score int
	// exact marks a score the search measured rather than bounded. At the root
	// only the moves that improved on everything before them are measured; the
	// rest were asked whether they beat the best so far, and an answer of no is
	// an upper bound that may sit far above what the move is really worth.
	exact bool
}

// Transposition table entry flags.
const (
	flagExact int8 = iota
	flagLower
	flagUpper
)

type tableEntry struct {
	key   uint64
	score int32
	best  int32
	depth int16
	// ext is the threat-extension budget the node that produced this entry
	// still had. Two searches of one position to the same nominal depth are
	// not the same search when one of them may still extend past that depth
	// and the other may not: the first can resolve a forced sequence the
	// second has to guess at. The score is therefore only reused by a node
	// holding exactly the same budget, which stops an entry stored near the
	// root, where extensions were plentiful, from answering a node that spent
	// them on the way down. The move is a hint about where to look rather than
	// a claim about the value, so it is reused whatever the budget was.
	ext  int16
	flag int8
}

// packScore and unpackScore make a win score storable: a win found at ply p is
// worth winScore-p, which depends on where the node sits, so the ply is folded
// out before storing and back in on retrieval.
func packScore(v, ply int) int32 {
	switch {
	case v >= decidedScore:
		return int32(v + ply)
	case v <= -decidedScore:
		return int32(v - ply)
	}
	return int32(v)
}

func unpackScore(v int32, ply int) int {
	switch {
	case int(v) >= decidedScore:
		return int(v) - ply
	case int(v) <= -decidedScore:
		return int(v) + ply
	}
	return int(v)
}

// searcher holds the working state of one search. It is reused between moves
// and is not safe for concurrent use.
type searcher struct {
	p        params
	ctx      context.Context
	deadline time.Time
	stopped  bool
	nodes    int64
	// evaluations counts the position analyses the search performed. It is a
	// different quantity from nodes: a node that hits the transposition table
	// still had to analyse the position to know its hash, and confirming a
	// defence analyses a position that never becomes a node at all.
	evaluations int64
	// lastDepth is the deepest iteration the most recent search finished, kept
	// so that a strength measurement can report how far each tier actually got.
	lastDepth int
	// elapsed is how long the most recent search took, set on every exit.
	elapsed time.Duration
	// stopReason is why the most recent search ended, one of the stop
	// constants above. It is latched: the first reason to trip wins, because
	// by the time anything notices, the clock and the node budget may both be
	// past and only one of them ended the search.
	stopReason string

	// onDepth, when set, is called at the end of every root iteration that
	// finished, with the result as it then stands and the search time so far.
	// An iteration that was cut short, the ordering fallback and a win taken
	// without searching do not reach it: what it reports is completed work.
	// The result it is handed is the root's own, so a hook that needs to keep
	// anything copies it.
	onDepth func(res *rootResult, elapsed time.Duration)

	// One analysis and one move buffer per ply: a node needs its own view of
	// the position to survive the recursion into its children.
	perPly []plyState
	probe  analysis
	// rootMoves holds the last finished iteration's root moves, copied out of
	// the working buffer that the next iteration overwrites.
	rootMoves []scoredMove

	hist  [2][]int32
	table []tableEntry
	mask  uint64

	// killers holds, per ply, the last two holes that produced a beta cutoff
	// there, most recent first, with -1 for a slot no cutoff has filled. They
	// are kept per ply rather than per node because that is what makes them
	// worth remembering: two nodes at the same ply are sibling positions a
	// move apart, and a reply that refuted one usually refutes the next, while
	// a hole from another ply answers a different position and would be noise.
	//
	// This is a fixed array rather than a field of plyState because it is
	// written after the recursion returns: the per-ply slice can grow while a
	// child is searched, and a pointer into it taken before the child ran
	// would then address the array it grew out of. The index is safe because
	// a node at or past the recursion cap returns before it generates a move,
	// so no ply from maxSearchPly up ever reaches the code below.
	killers [maxSearchPly][2]int32

	// stamp deduplicates candidate holes without clearing an array per call.
	stamp    []int32
	stampGen int32
	cands    []int32
	needed   []game.Link
}

type plyState struct {
	an    analysis
	moves []scoredMove
}

func newSearcher(p params) *searcher {
	s := &searcher{p: p}
	s.forgetKillers()
	if p.useTable {
		const bits = 16
		s.table = make([]tableEntry, 1<<bits)
		s.mask = 1<<bits - 1
	}
	return s
}

// forgetKillers empties every ply's killer slots. Zero cannot stand for empty:
// it is the top-left hole, and a searcher that had never recorded a cutoff
// would be trying that hole first at every ply.
func (s *searcher) forgetKillers() {
	for i := range s.killers {
		s.killers[i] = [2]int32{-1, -1}
	}
}

func (s *searcher) at(ply int) *plyState {
	for len(s.perPly) <= ply {
		s.perPly = append(s.perPly, plyState{})
	}
	return &s.perPly[ply]
}

// prepare resets the per-move state for a board of the given size.
func (s *searcher) prepare(n int) {
	cells := n * n
	for i := range s.hist {
		if len(s.hist[i]) != cells {
			s.hist[i] = make([]int32, cells)
		} else {
			clear(s.hist[i])
		}
	}
	if len(s.stamp) != cells {
		s.stamp = make([]int32, cells)
		s.stampGen = 0
	}
	// The killers belong to the search that recorded them: they are holes that
	// refuted a sibling of some node in the tree just searched, and in the next
	// position that tree no longer exists.
	s.forgetKillers()
	// The table is emptied between moves on purpose: carrying entries over
	// would make a move depend on which positions happened to be searched
	// earlier, and the bot is meant to be a function of the seed and the
	// position alone.
	clear(s.table)
}

// halt records why the search is stopping, first reason winning.
func (s *searcher) halt(reason string) {
	if s.stopReason == "" {
		s.stopReason = reason
	}
}

// abort stops the search in flight. Everything the running iteration has
// computed is then discarded: part of it was searched and part of it was not,
// so together they are not a search of anything.
func (s *searcher) abort(reason string) {
	s.halt(reason)
	s.stopped = true
}

// expired checks cancellation and the clock at work boundaries. Node limits are
// checked only before entering search: reaching the ceiling must not discard
// an iteration whose remaining root moves finish without entering another node.
func (s *searcher) expired() bool {
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				s.halt(stopCanceled)
			} else {
				s.halt(stopTime)
			}
			return true
		}
	}
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		s.halt(stopTime)
		return true
	}
	return false
}

// analyse loads a position into an and counts it. Every analysis the search
// performs goes through here, including the root's own and the probes that
// confirm a defence, because analysing positions is what the search spends its
// time on and a count that left some of them out would not be a measure of the
// work done.
func (s *searcher) analyse(an *analysis, g *game.Game) {
	s.evaluations++
	an.templates = s.p.templates
	an.load(g)
}

// leaf is the static score of a position for the side to move.
func (s *searcher) leaf(an *analysis, me game.Player) int {
	t := an.terms(me)
	if !s.p.fullEval {
		return t.distScore()
	}
	return t.Score()
}

// search returns the value of the position for the side to move.
//
// One node is one entry into this function, which is what the node budget
// counts. That budget is checked at every entry rather than every hundred and
// twenty-eighth, so a search given a node budget walks exactly the same tree on
// a fast machine and a slow one; the clock is checked periodically, because it
// cannot be made repeatable anyway.
func (s *searcher) search(g *game.Game, depth, ply, alpha, beta, ext int) int {
	if s.stopped {
		return alpha
	}
	if s.p.nodeLimit > 0 && s.nodes >= s.p.nodeLimit {
		s.abort(stopNodes)
		return alpha
	}
	s.nodes++
	if s.nodes&127 == 0 && s.expired() {
		s.stopped = true
		return alpha
	}

	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	st := s.at(ply)
	an := &st.an
	s.analyse(an, g)

	if an.need[mine] == 1 {
		return winScore - ply
	}
	forced := an.need[theirs] == 1

	// The recursion cap is a horizon like the depth ceiling, and it is
	// evaluated like one. Returning alpha there instead would hand the parent
	// a number that came from the window rather than from the position, and
	// the parent is free to store that number as the line's value: a
	// configuration whose depth and extension budget together reach the cap
	// would be reading its own window back as a score, and a window can sit
	// above the level at which a score is read as a win.
	atCap := ply >= maxSearchPly
	if depth <= 0 || atCap {
		if forced && ext > 0 && !atCap {
			depth, ext = 1, ext-1
		} else {
			return s.leaf(an, me)
		}
	}

	key := an.hash
	tableMove := int32(-1)
	if s.table != nil {
		e := &s.table[key&s.mask]
		if e.key == key {
			tableMove = e.best
			if int(e.ext) == ext && int(e.depth) >= depth {
				v := unpackScore(e.score, ply)
				switch e.flag {
				case flagExact:
					return v
				case flagLower:
					if v >= beta {
						return v
					}
				case flagUpper:
					if v <= alpha {
						return v
					}
				}
			}
		}
	}

	moves := s.generate(g, an, me, ply, forced)
	if s.stopped {
		// Listing the defences was interrupted, so what came back is a prefix
		// of the exact list. An empty prefix does not mean the threat is
		// unanswerable and a short one does not mean the answers left out
		// lose, so nothing here may be scored.
		return alpha
	}
	if len(moves) == 0 {
		if forced {
			// Nothing answers the threat: the opponent connects next move.
			return -(winScore - ply - 1)
		}
		// Nowhere left to play, which the engine scores as a draw.
		return 0
	}
	if tableMove >= 0 {
		promote(moves, tableMove)
	}
	if s.p.killers {
		// Second killer first, so the more recent one ends up ahead of it.
		// promote only reorders a hole the list already holds: a killer that
		// is not a candidate here -- because the hole is taken, or because the
		// width cap dropped it, or because this node is answering a threat and
		// its list is the exact defences -- leaves the list untouched.
		for i := len(s.killers[ply]) - 1; i >= 0; i-- {
			if k := s.killers[ply][i]; k >= 0 {
				promote(moves, k)
			}
		}
	}

	openAlpha := alpha
	best := -infScore
	bestHole := int32(-1)
	for i := range moves {
		mv := moves[i]
		res, err := g.PlayPeg(mv.at)
		if err != nil {
			continue
		}
		var v int
		switch {
		case res.Over():
			switch res.Winner() {
			case me:
				v = winScore - ply - 1
			case game.NoPlayer:
				v = 0
			default:
				v = -(winScore - ply - 1)
			}
		case s.p.pvs && bestHole >= 0 && alpha+1 < beta:
			// Principal variation search. The move that scored first is taken
			// to be the best one, so the rest are searched with a window one
			// point wide, which is cheap and usually confirms it. A move that
			// beats that window has only been shown to be better than alpha,
			// not measured, so it is searched again with the real window
			// before its score is believed. The probe is skipped when the
			// window is already one point wide, since it would then be the
			// same search run twice.
			v = -s.search(g, depth-1, ply+1, -(alpha + 1), -alpha, ext)
			if !s.stopped && v > alpha && v < beta {
				v = -s.search(g, depth-1, ply+1, -beta, -alpha, ext)
			}
		default:
			v = -s.search(g, depth-1, ply+1, -beta, -alpha, ext)
		}
		if err := g.UndoLastMove(); err != nil {
			s.abort(stopError)
			return alpha
		}
		if s.stopped {
			if bestHole < 0 {
				return alpha
			}
			return best
		}
		if v > best {
			best, bestHole = v, mv.hole
		}
		if v > alpha {
			alpha = v
		}
		if alpha >= beta {
			s.hist[mine][mv.hole] += int32(depth * depth)
			if s.p.killers {
				k := &s.killers[ply]
				if k[0] != mv.hole {
					// A hole already in the first slot stays where it is:
					// moving it would push the other killer out for a
					// refutation the ply has already got.
					k[1] = k[0]
					k[0] = mv.hole
				}
			}
			break
		}
	}

	// Nothing is stored on the way out of an interrupted node: the returns
	// above are the only way out once s.stopped is set, so a score that was
	// never finished cannot be left behind for a later node to believe.
	if s.table != nil && bestHole >= 0 {
		flag := flagExact
		switch {
		case best <= openAlpha:
			flag = flagUpper
		case best >= beta:
			flag = flagLower
		}
		e := &s.table[key&s.mask]
		if e.key != key || int(e.ext) != ext || int(e.depth) <= depth {
			*e = tableEntry{
				key:   key,
				score: packScore(best, ply),
				best:  bestHole,
				depth: int16(depth),
				ext:   int16(ext),
				flag:  flag,
			}
		}
	}
	return best
}

// promote moves the named hole to the front of the list, keeping the rest in
// order.
func promote(moves []scoredMove, hole int32) {
	for i := range moves {
		if moves[i].hole != hole {
			continue
		}
		m := moves[i]
		copy(moves[1:i+1], moves[:i])
		moves[0] = m
		return
	}
}

// generate returns the moves worth searching at this node.
func (s *searcher) generate(g *game.Game, an *analysis, me game.Player, ply int, forced bool) []scoredMove {
	if forced {
		return s.defences(g, an, me, ply)
	}
	return s.candidates(g, an, me, ply)
}

// clampSlack caps how far off a cheapest chain a hole may be before the
// ordering stops caring about the difference.
func clampSlack(v int32) int32 {
	if v < 0 {
		return 0
	}
	if v > 8 {
		return 8
	}
	return v
}

// historyCeiling caps what a cutoff elsewhere in the tree can contribute to a
// hole's ordering score. It is deliberately small: the ordering score also
// decides which moves survive the width cap, and a hole that produced a cutoff
// in an unrelated line must not be able to push a hole on the contested route
// out of the candidate list altogether. Within a set of similar holes it still
// decides which is tried first, which is all the heuristic is for.
const historyCeiling = int32(12)

// orderScore ranks a hole by how close it is to the cheapest chain of either
// side, which is where a connection game is decided, plus what the search has
// learnt about the hole from earlier cutoffs.
func (s *searcher) orderScore(an *analysis, mine, theirs int, hole int32) int32 {
	needMine := int32(distValue(an.need[mine]))
	needTheirs := int32(distValue(an.need[theirs]))
	order := int32(240)
	order -= 8 * clampSlack(an.span[mine][hole]-needMine)
	order -= 7 * clampSlack(an.span[theirs][hole]-needTheirs)
	if an.hasNeighbourPeg(int(hole)) {
		order += 18
	}
	if h := s.hist[mine][hole]; h > 0 {
		if h > historyCeiling {
			h = historyCeiling
		}
		order += h
	}
	return order
}

// candidates keeps the best-ordered legal placements, up to the tier's width.
func (s *searcher) candidates(g *game.Game, an *analysis, me game.Player, ply int) []scoredMove {
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	width := s.p.width
	if ply == 0 {
		width = s.p.rootWidth
	}
	st := s.at(ply)
	buf := st.moves[:0]
	n := an.n
	g.EachLegalPlacement(me, func(p game.Point) bool {
		hole := int32(p.Row*n + p.Col)
		buf = insertTop(buf, scoredMove{
			at:    p,
			hole:  hole,
			order: s.orderScore(an, mine, theirs, hole),
		}, width)
		return true
	})
	st.moves = buf
	return buf
}

// insertTop keeps buf sorted by descending order score, holding at most width
// entries. Equal scores keep the earlier hole, which makes the list a function
// of the position alone.
func insertTop(buf []scoredMove, m scoredMove, width int) []scoredMove {
	if len(buf) >= width && m.order <= buf[len(buf)-1].order {
		return buf
	}
	pos := len(buf)
	for pos > 0 && buf[pos-1].order < m.order {
		pos--
	}
	if len(buf) < width {
		buf = append(buf, scoredMove{})
	}
	end := len(buf)
	copy(buf[pos+1:end], buf[pos:end-1])
	buf[pos] = m
	return buf
}

// defences lists every move that answers an opponent one peg from a finished
// chain, and nothing else.
//
// The list is exact. The opponent wins by filling some hole w whose cheapest
// chain costs one peg; every other hole on that chain already holds one of
// their pegs, and every link it uses either stands or can still be made. A
// reply can only interfere in two ways: take w itself, or place a peg whose
// own new link crosses a link that chain still needs. Every hole satisfying
// either description is collected, then confirmed by playing it and measuring
// the threat again, so a hole that looks like a defence but is not never
// reaches the search.
func (s *searcher) defences(g *game.Game, an *analysis, me game.Player, ply int) []scoredMove {
	n := an.n
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	opp := me.Opponent()

	s.stampGen++
	gen := s.stampGen
	s.cands = s.cands[:0]
	s.needed = s.needed[:0]
	add := func(hole int32) {
		if s.stamp[hole] == gen {
			return
		}
		s.stamp[hole] = gen
		s.cands = append(s.cands, hole)
	}

	for i := range an.span[theirs] {
		if an.span[theirs][i] != 1 || an.pegs[i] != game.NoPlayer || !an.use[theirs][i] {
			continue
		}
		col, row := i%n, i/n
		w := game.Point{Col: col, Row: row}
		if g.CanPlace(me, w) == nil {
			add(int32(i))
		}
		for d := range game.Dir(game.NumDirs) {
			c, r := col+dirDelta[d][0], row+dirDelta[d][1]
			if c < 0 || c >= n || r < 0 || r >= n {
				continue
			}
			j := r*n + c
			if an.pegs[j] != opp || !an.use[theirs][j] {
				continue
			}
			if !an.linkOpen(theirs, i, d, j) {
				continue
			}
			if l, ok := game.NewLink(w, game.Point{Col: c, Row: r}); ok {
				s.needed = append(s.needed, l)
			}
		}
	}

	for _, l := range s.needed {
		for _, off := range crossTable[l.Dir] {
			a := game.Point{Col: l.From.Col + off.dCol, Row: l.From.Row + off.dRow}
			b := a.Add(off.dir)
			if a.Col < 0 || a.Col >= n || a.Row < 0 || a.Row >= n {
				continue
			}
			if b.Col < 0 || b.Col >= n || b.Row < 0 || b.Row >= n {
				continue
			}
			ia, ib := a.Row*n+a.Col, b.Row*n+b.Col
			// Only one end may be empty: the reply places a single peg, and it
			// is that placement which brings the crossing link into being.
			// Establish that before asking whether the link can be made, since
			// linkOpen reads the link mask of a hole and only means anything
			// for a hole this side could own.
			var hole int32
			switch {
			case an.pegs[ia] == me && an.pegs[ib] == game.NoPlayer && g.CanPlace(me, b) == nil:
				hole = int32(ib)
			case an.pegs[ib] == me && an.pegs[ia] == game.NoPlayer && g.CanPlace(me, a) == nil:
				hole = int32(ia)
			default:
				continue
			}
			if !an.linkOpen(mine, ia, off.dir, ib) {
				continue
			}
			add(hole)
		}
	}

	st := s.at(ply)
	buf := st.moves[:0]
	for _, hole := range s.cands {
		if s.expired() {
			// Confirming the candidates is the expensive half of this, and a
			// cancelled search must not sit through the rest of it. What comes
			// back is then a prefix of the exact list; every caller discards
			// the move list of a stopped node rather than reading it as exact.
			s.stopped = true
			break
		}
		p := game.Point{Col: int(hole) % n, Row: int(hole) / n}
		res, err := g.PlayPeg(p)
		if err != nil {
			continue
		}
		answered := res.Over()
		if !answered {
			s.analyse(&s.probe, g)
			// Anything other than "one peg from finishing" answers the threat,
			// including sealing the opponent out of a chain altogether.
			answered = s.probe.need[theirs] != 1
		}
		if err := g.UndoLastMove(); err != nil {
			s.abort(stopError)
			break
		}
		if answered {
			buf = append(buf, scoredMove{
				at:    p,
				hole:  hole,
				order: s.orderScore(an, mine, theirs, hole),
			})
		}
	}
	slices.SortFunc(buf, func(x, y scoredMove) int {
		if x.order != y.order {
			return int(y.order - x.order)
		}
		return int(x.hole - y.hole)
	})
	st.moves = buf
	return buf
}

// rootResult is everything the caller and the hint feature need from a search.
type rootResult struct {
	best  game.Point
	score int
	// depth is the deepest iteration that finished, 0 when the budget did not
	// allow even one and the cheap ordering chose the move.
	depth int
	// moves holds the searched root moves, best first. Only the scores marked
	// exact are measurements; the rest are upper bounds, which is all that
	// searching a move against the best found so far can establish.
	moves []scoredMove
	// immediate marks a position where the side to move simply had a winning
	// hole, taken without searching.
	immediate bool
	// threatened marks an opponent one peg from a finished chain.
	threatened bool
	// defences counts the moves that answered that threat; -1 means listing
	// was interrupted, so the usable prefix must not be read as an exact count.
	defences int
	// terms is the decomposition of the position before the move.
	terms Terms
	// an is the loaded root position, kept for the hint's highlighting.
	an *analysis
}

// aspirationDelta is how far either side of the previous iteration's score the
// root's first window opens, in the units the evaluation itself uses: one peg
// of head start is distWeight, so this band is two pegs wide either way.
//
// The scale is what fixes the choice. Distance dominates the leaf score and is
// quantised in pegs, so the smallest change a deepening can make to a position
// whose peg counts moved is one peg; the bottleneck and reach terms together
// are worth less than a peg and move the score inside that step. A band of one
// peg would therefore be missed by any iteration that changes the peg race at
// all, and a band much wider than two stops pruning. Two pegs is the width
// that absorbs the sub-peg terms and the ordinary one-peg swing.
//
// It is not what makes the lever unprofitable. Widths from one peg to
// forty-eight were measured against the same positions and none of them saved
// work; the number is documented here so that a reader knows where it came
// from, not because a different one would change the verdict on the field.
const aspirationDelta = 2 * distWeight

// aspirationWindow is the band the root's first move may be searched in, given
// what the previous iteration concluded.
//
// It stands down in the cases where a narrow window would measure the wrong
// thing rather than the same thing faster. There is nothing to centre on
// before an iteration has finished, so the first iteration is searched wide.
// Sampling reads every root move's value and not just the best one, so it
// keeps the full window at the root and cannot use this at all. And a decided
// score sits at the far end of the scale rather than on it: a band around a
// forced loss lies almost entirely below every real reply, so it would fail
// high on the first move of every remaining iteration and pay for a re-search
// each time.
func (s *searcher) aspirationWindow(depth, prev int, have bool) (lo, hi int, ok bool) {
	switch {
	case !s.p.aspiration || !have || depth < 2 || s.p.temperature > 0:
	case prev >= decidedScore || prev <= -decidedScore:
	default:
		return prev - aspirationDelta, prev + aspirationDelta, true
	}
	return -infScore, infScore, false
}

// aspirate searches one root move inside a narrow window and widens the window
// on whichever side it failed, until the window contains the answer.
//
// A search whose window did not contain the value has established which side
// of the window the value lies on and nothing more, so what comes back from a
// failing window is a bound. The root reads its first move's score as a
// measurement -- it is the alpha every later move is judged against, and the
// beginner tier and the hint read the numbers themselves -- so a bound must
// never be recorded as one. Widening the failing side to the end of the scale
// makes a fail on that side impossible, which bounds this at three searches
// and guarantees the value returned is one its own window contained.
//
// An interrupted search is handed straight back: the caller drops the whole
// iteration, exactly as it does for any other root move.
func (s *searcher) aspirate(g *game.Game, depth, lo, hi int) int {
	for {
		v := -s.search(g, depth-1, 1, -hi, -lo, s.p.extend)
		switch {
		case s.stopped:
			return v
		case v <= lo && lo > -infScore:
			lo = -infScore
		case v >= hi && hi < infScore:
			hi = infScore
		default:
			return v
		}
	}
}

// root searches the position and returns the move to play.
//
// Only a finished iteration is ever handed back. An iteration that is cut short
// has scored some root moves at the new depth and the rest at the old one, and
// that mixture is not a search of anything: it is dropped, and the deepest
// iteration that finished is what the caller sees. The scores are read and not
// merely ranked -- the beginner tier samples among them and the hint measures
// how close the second move was -- so a mixture would show up in how the bot
// plays, not only in what it reports.
func (s *searcher) root(ctx context.Context, g *game.Game) (rootResult, error) {
	me := g.Turn()
	n := g.Size()
	s.prepare(n)
	s.ctx = ctx
	s.stopped = false
	s.stopReason = ""
	s.nodes = 0
	s.evaluations = 0
	s.lastDepth = 0
	start := time.Now()
	s.elapsed = 0
	defer func() { s.elapsed = time.Since(start) }()
	s.deadline = start.Add(s.p.budget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(s.deadline) {
		s.deadline = dl
	}

	st := s.at(0)
	an := &st.an
	s.analyse(an, g)
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	out := rootResult{terms: an.terms(me), an: an}

	if an.need[mine] == 1 {
		hole, ok := an.winningHole(mine)
		if !ok {
			s.halt(stopNoMove)
			return out, ErrNoMove
		}
		s.halt(stopImmediate)
		out.best = hole
		out.score = winScore
		out.immediate = true
		out.moves = []scoredMove{{at: hole, hole: int32(hole.Row*n + hole.Col), score: winScore, exact: true}}
		return out, nil
	}

	out.threatened = an.need[theirs] == 1
	var moves []scoredMove
	if out.threatened {
		moves = s.defences(g, an, me, 0)
		out.defences = len(moves)
		if s.stopped {
			out.defences = -1
		}
	}
	if len(moves) == 0 {
		// Either there was no threat, or nothing answers it, or listing the
		// answers was cut short. Either way the bot still has to play, so fall
		// back to the ordinary candidate list, which is one cheap pass over the
		// legal holes and cannot itself be interrupted.
		moves = s.candidates(g, an, me, 0)
	}
	if len(moves) == 0 {
		s.halt(stopNoMove)
		return out, ErrNoMove
	}
	// Before any search has scored a move, the ordering heuristic is the
	// answer: a cancelled context returns this rather than nothing.
	out.best = moves[0].at
	out.moves = s.keep(moves)
	if s.stopped || s.expired() {
		return out, nil
	}

	prev, havePrev := 0, false
	for depth := 1; depth <= s.p.maxDepth; depth++ {
		alpha := -infScore
		lo, hi, narrow := s.aspirationWindow(depth, prev, havePrev)
		finished := true
		for i := range moves {
			if s.expired() {
				// Between two root moves is the cheapest place to notice, and
				// the only place that catches a budget spent on root moves
				// that are each too small to check the clock on their own.
				finished = false
				break
			}
			res, err := g.PlayPeg(moves[i].at)
			if err != nil {
				moves[i].score, moves[i].exact = -infScore, false
				continue
			}
			var v int
			// The first move to be scored is searched with a window that will
			// contain its value, so it is measured. After that the root asks
			// each move only whether it beats alpha, and a move that says no
			// comes back with a bound instead of a value.
			exact := alpha == -infScore || s.p.temperature > 0
			if res.Over() {
				switch res.Winner() {
				case me:
					v = winScore - 1
				case game.NoPlayer:
					v = 0
				default:
					v = -(winScore - 1)
				}
				// A finished game is worth what it is worth whatever window
				// the move was searched with.
				exact = true
			} else {
				switch {
				case s.p.temperature > 0:
					// Sampling needs values for every candidate, not upper
					// bounds. Otherwise a losing move whose bound is close to
					// the PV can receive the same probability as a genuinely
					// close reply.
					v = -s.search(g, depth-1, 1, -infScore, infScore, s.p.extend)
				case narrow && alpha == -infScore:
					// The first move to be scored, and an earlier iteration to
					// centre a band on. Whatever window ends up containing the
					// value, the value comes back measured.
					v = s.aspirate(g, depth, lo, hi)
				default:
					v = -s.search(g, depth-1, 1, -infScore, -alpha, s.p.extend)
				}
			}
			if err := g.UndoLastMove(); err != nil {
				s.abort(stopError)
				return out, err
			}
			if s.stopped {
				finished = false
				break
			}
			moves[i].score = v
			moves[i].exact = exact || v > alpha
			if v > alpha {
				alpha = v
			}
		}
		if !finished {
			break
		}
		// Moves that score alike are separated first by whether the score is a
		// measurement, and only then by the ordering heuristic.
		//
		// Exactness has to come first because of how the root searches. Every
		// move after the first is searched with the window open only above
		// alpha, so a move that comes back level with alpha has been shown not
		// to beat it and may be far worse; only the move that set alpha was
		// measured. Ranking on the number alone lets the positional tie-break
		// promote a move that was never worth alpha over the one that was,
		// which is a worse move played for a better-looking reason.
		//
		// Below that the ordering heuristic decides, rather than the hole's
		// position on the board. That matters most in a lost position, where
		// every reply scores as the same forced loss: without a positional
		// tie-break the bot would stop playing sensibly the moment it saw the
		// loss coming, and an opponent that has not seen it yet would still
		// have to be given something to beat.
		slices.SortFunc(moves, func(x, y scoredMove) int {
			if x.score != y.score {
				return y.score - x.score
			}
			if x.exact != y.exact {
				if x.exact {
					return -1
				}
				return 1
			}
			if x.order != y.order {
				return int(y.order - x.order)
			}
			return int(x.hole - y.hole)
		})
		out.best = moves[0].at
		out.score = moves[0].score
		out.depth = depth
		s.lastDepth = depth
		out.moves = s.keep(moves)
		// Only a finished iteration may centre the next one's first window:
		// an interrupted iteration has scored some moves at the new depth and
		// the rest at the old one, and the loop leaves before reaching here.
		prev, havePrev = out.score, true
		if s.onDepth != nil {
			s.onDepth(&out, time.Since(start))
		}
		if out.score >= decidedScore {
			// A forced win inside the moves the search looked at, and a forced
			// win does not get better with depth, so deepening this same
			// selective tree can only cost time. It is not a proof about the
			// position: the width caps threw candidate moves away, and a
			// defence thrown away is a defence never searched.
			s.halt(stopDecided)
			break
		}
		if s.expired() {
			break
		}
	}
	s.halt(stopDepth)
	return out, nil
}

// keep copies a finished iteration's root moves out of the buffer the next
// iteration overwrites. The copy is reused between searches on the same terms
// as the per-ply buffers: it belongs to the searcher and stays valid until the
// next call to root.
func (s *searcher) keep(moves []scoredMove) []scoredMove {
	s.rootMoves = append(s.rootMoves[:0], moves...)
	return s.rootMoves
}

// stats is what the most recent search on s spent, and the zero value before
// the first one.
func (s *searcher) stats() SearchStats {
	return SearchStats{
		Nodes:       s.nodes,
		Evaluations: s.evaluations,
		Depth:       s.lastDepth,
		Elapsed:     s.elapsed,
		StopReason:  s.stopReason,
	}
}
