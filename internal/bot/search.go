package bot

import (
	"context"
	"slices"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// One alpha-beta search serves all three tiers. The tiers differ only in the
// params below: how long they may think, how deep they may look, how many
// candidate moves they keep per node, whether they can see route redundancy at
// all, and whether they sample instead of playing the best move found.
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

// params are the strength levers. All three tiers share one engine and differ
// only here.
type params struct {
	// budget is the longest a move may take. Only the strongest tier normally
	// reaches it; the weaker tiers stop at their depth ceiling first, so for
	// them the budget is a guard against a pathological position rather than
	// the thing that decides how hard they think.
	budget time.Duration
	// maxDepth caps iterative deepening. It is the main strength lever: each
	// extra ply up to about five is worth a large win-rate margin, whereas
	// extra time on its own buys only a fraction of a ply, so a tier is defined
	// by how deep it may look rather than by how long it may take.
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
}

// scoredMove is one candidate placement: its ordering score before the search
// and its search score after.
type scoredMove struct {
	at    game.Point
	hole  int32
	order int32
	score int
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
	flag  int8
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
	// lastDepth is the deepest iteration the most recent search finished, kept
	// so that a strength measurement can report how far each tier actually got.
	lastDepth int

	// One analysis and one move buffer per ply: a node needs its own view of
	// the position to survive the recursion into its children.
	perPly []plyState
	probe  analysis

	hist  [2][]int32
	table []tableEntry
	mask  uint64

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
	if p.useTable {
		const bits = 16
		s.table = make([]tableEntry, 1<<bits)
		s.mask = 1<<bits - 1
	}
	return s
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
	// The table is emptied between moves on purpose: carrying entries over
	// would make a move depend on which positions happened to be searched
	// earlier, and the bot is meant to be a function of the seed and the
	// position alone.
	clear(s.table)
}

func (s *searcher) expired() bool {
	if s.ctx != nil && s.ctx.Err() != nil {
		return true
	}
	return time.Now().After(s.deadline)
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
func (s *searcher) search(g *game.Game, depth, ply, alpha, beta, ext int) int {
	s.nodes++
	if s.nodes&127 == 0 && s.expired() {
		s.stopped = true
	}
	if s.stopped || ply >= maxSearchPly {
		return alpha
	}

	me := g.Turn()
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	st := s.at(ply)
	an := &st.an
	an.load(g)

	if an.need[mine] == 1 {
		return winScore - ply
	}
	forced := an.need[theirs] == 1

	if depth <= 0 {
		if forced && ext > 0 {
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
			if int(e.depth) >= depth {
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
		if res.Over() {
			switch res.Winner() {
			case me:
				v = winScore - ply - 1
			case game.NoPlayer:
				v = 0
			default:
				v = -(winScore - ply - 1)
			}
		} else {
			v = -s.search(g, depth-1, ply+1, -beta, -alpha, ext)
		}
		if err := g.UndoLastMove(); err != nil {
			s.stopped = true
			return best
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
			break
		}
	}

	if s.table != nil && bestHole >= 0 {
		flag := flagExact
		switch {
		case best <= openAlpha:
			flag = flagUpper
		case best >= beta:
			flag = flagLower
		}
		e := &s.table[key&s.mask]
		if e.key != key || int(e.depth) <= depth {
			*e = tableEntry{
				key:   key,
				score: packScore(best, ply),
				best:  bestHole,
				depth: int16(depth),
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
		p := game.Point{Col: int(hole) % n, Row: int(hole) / n}
		res, err := g.PlayPeg(p)
		if err != nil {
			continue
		}
		answered := res.Over()
		if !answered {
			s.probe.load(g)
			// Anything other than "one peg from finishing" answers the threat,
			// including sealing the opponent out of a chain altogether.
			answered = s.probe.need[theirs] != 1
		}
		if err := g.UndoLastMove(); err != nil {
			s.stopped = true
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
	// moves holds the searched root moves, best first.
	moves []scoredMove
	// immediate marks a position where the side to move simply had a winning
	// hole, taken without searching.
	immediate bool
	// threatened marks an opponent one peg from a finished chain.
	threatened bool
	// defences counts the moves that answered that threat.
	defences int
	// terms is the decomposition of the position before the move.
	terms Terms
	// an is the loaded root position, kept for the hint's highlighting.
	an *analysis
}

// root searches the position and returns the move to play.
func (s *searcher) root(ctx context.Context, g *game.Game) (rootResult, error) {
	me := g.Turn()
	n := g.Size()
	s.prepare(n)
	s.ctx = ctx
	s.stopped = false
	s.nodes = 0
	s.lastDepth = 0
	s.deadline = time.Now().Add(s.p.budget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(s.deadline) {
		s.deadline = dl
	}

	st := s.at(0)
	an := &st.an
	an.load(g)
	mine, theirs := sideIndex(me), sideIndex(me.Opponent())
	out := rootResult{terms: an.terms(me), an: an}

	if an.need[mine] == 1 {
		hole, ok := an.winningHole(mine)
		if !ok {
			return out, ErrNoMove
		}
		out.best = hole
		out.score = winScore
		out.immediate = true
		out.moves = []scoredMove{{at: hole, hole: int32(hole.Row*n + hole.Col), score: winScore}}
		return out, nil
	}

	out.threatened = an.need[theirs] == 1
	var moves []scoredMove
	if out.threatened {
		moves = s.defences(g, an, me, 0)
		out.defences = len(moves)
	}
	if len(moves) == 0 {
		// Either there was no threat, or nothing answers it. Either way the
		// bot still has to play, so fall back to the ordinary candidate list.
		moves = s.candidates(g, an, me, 0)
	}
	if len(moves) == 0 {
		return out, ErrNoMove
	}
	// Before any search has scored a move, the ordering heuristic is the
	// answer: a cancelled context returns this rather than nothing.
	out.best = moves[0].at
	out.moves = moves
	if s.expired() {
		return out, nil
	}

	for depth := 1; depth <= s.p.maxDepth; depth++ {
		alpha := -infScore
		finished := true
		for i := range moves {
			res, err := g.PlayPeg(moves[i].at)
			if err != nil {
				moves[i].score = -infScore
				continue
			}
			var v int
			if res.Over() {
				switch res.Winner() {
				case me:
					v = winScore - 1
				case game.NoPlayer:
					v = 0
				default:
					v = -(winScore - 1)
				}
			} else {
				v = -s.search(g, depth-1, 1, -infScore, -alpha, s.p.extend)
			}
			if err := g.UndoLastMove(); err != nil {
				return out, err
			}
			if s.stopped {
				finished = false
				break
			}
			moves[i].score = v
			if v > alpha {
				alpha = v
			}
		}
		if !finished {
			break
		}
		// Moves that score alike are separated by the ordering heuristic, not
		// by their position on the board. This matters most in a lost
		// position, where every reply scores as the same forced loss: without
		// a positional tie-break the bot would stop playing sensibly the
		// moment it saw the loss coming, and an opponent that has not seen it
		// yet would still have to be given something to beat.
		slices.SortFunc(moves, func(x, y scoredMove) int {
			if x.score != y.score {
				return y.score - x.score
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
		out.moves = moves
		if out.score >= decidedScore {
			// A win is proven; nothing deeper can improve on it.
			break
		}
		if s.expired() {
			break
		}
	}
	return out, nil
}
