package bot

import (
	"math/rand/v2"
	"sync"

	"github.com/BAKocska/twixtui/internal/game"
)

// The evaluation answers one question: how much cheaper is it for me to finish
// my border-to-border chain than for my opponent to finish theirs?
//
// "Cheaper" is counted in pegs. For a side, every hole it could still use costs
// one peg, every hole it already owns costs nothing, and every hole the
// opponent owns or is forbidden to it costs everything. Two holes are joined
// when they are a knight's move apart and a link between them either already
// stands or could still be created. The cheapest border-to-border walk under
// those costs is exactly "moves I still need to win", which is the quantity
// Moesker's TwixT evaluation calls f_path and which Anshelevich's Hex work
// calls virtual-connection depth.
//
// Because the weights are only 0 and 1, the walk is found by a layered
// breadth-first sweep rather than by Dijkstra: zero-cost steps stay in the
// current layer, one-cost steps go to the next. Each side is swept from both of
// its borders, which costs one extra sweep and buys three things the search and
// the hint feature both need: an exact "one peg from winning" test, the set of
// holes a cheapest chain could still run through, and a cheap slack measure for
// move ordering.

// sideIndex maps a player onto 0 for Vertical and 1 for Horizontal so that the
// per-side scratch arrays can be indexed without a branch or a map.
func sideIndex(pl game.Player) int { return int(pl) - 1 }

// unusable is the cost of a hole no chain of the side under test can reach. It
// is far above any real cost yet small enough that adding two of them cannot
// overflow an int32.
const unusable = int32(1) << 20

// dirDelta caches the column and row displacement of each link direction, so
// the sweep's inner loop does not call through a method.
var dirDelta = func() [game.NumDirs][2]int {
	var out [game.NumDirs][2]int
	for d := range game.Dir(game.NumDirs) {
		dCol, dRow := d.Offset()
		out[d] = [2]int{dCol, dRow}
	}
	return out
}()

// oppositeDir caches Dir.Opposite for the same reason.
var oppositeDir = func() [game.NumDirs]game.Dir {
	var out [game.NumDirs]game.Dir
	for d := range game.Dir(game.NumDirs) {
		out[d] = d.Opposite()
	}
	return out
}()

// crossOffset is a link that crosses a reference link, expressed relative to
// the reference link's From endpoint.
type crossOffset struct {
	dCol, dRow int
	dir        game.Dir
}

// crossRadius bounds the offsets examined when building the crossing table. A
// knight's-move segment spans three columns and rows, so a crossing link's
// canonical endpoint is never further than three holes away.
const crossRadius = 4

// crossTable[d] lists every canonical link that crosses the canonical link
// {From: origin, Dir: d}, as offsets from that origin. It is derived from
// game.LinksCross rather than transcribed, so the bot and the engine cannot
// disagree about what crosses what.
var crossTable = func() [4][]crossOffset {
	var table [4][]crossOffset
	origin := game.Point{}
	for d := range game.Dir(4) {
		ref := game.Link{From: origin, Dir: d}
		for dCol := -crossRadius; dCol <= crossRadius; dCol++ {
			for dRow := -crossRadius; dRow <= crossRadius; dRow++ {
				for other := range game.Dir(4) {
					cand := game.Link{From: game.Point{Col: dCol, Row: dRow}, Dir: other}
					if cand == ref {
						continue
					}
					if game.LinksCross(ref, cand) {
						table[d] = append(table[d], crossOffset{dCol: dCol, dRow: dRow, dir: other})
					}
				}
			}
		}
	}
	return table
}()

// zobrist holds the random words that turn a position into a hash key. Links
// are hashed as well as pegs: which links stand depends on the order the pegs
// were played, so two positions with the same pegs are not necessarily the same
// position.
type zobrist struct {
	peg  [2][]uint64
	link [4][]uint64
	turn uint64
}

var zobristBySize sync.Map

// zobristFor returns the hash words for a board of the given side length,
// building them once. The generator is seeded with a constant so that a hash is
// reproducible between runs, which is what lets a seeded bot be deterministic.
func zobristFor(n int) *zobrist {
	if v, ok := zobristBySize.Load(n); ok {
		return v.(*zobrist)
	}
	src := rand.New(rand.NewPCG(0x7717c7f0dd1e5eed, uint64(n)))
	z := &zobrist{turn: src.Uint64()}
	cells := n * n
	for s := range z.peg {
		z.peg[s] = make([]uint64, cells)
		for i := range z.peg[s] {
			z.peg[s][i] = src.Uint64()
		}
	}
	for d := range z.link {
		z.link[d] = make([]uint64, cells)
		for i := range z.link[d] {
			z.link[d][i] = src.Uint64()
		}
	}
	actual, _ := zobristBySize.LoadOrStore(n, z)
	return actual.(*zobrist)
}

// analysis is the reusable working state behind the evaluation: a flat copy of
// one position plus, for each side, the cost of reaching every hole from each of
// that side's two borders. One analysis serves one node of one search and is
// not safe for concurrent use.
type analysis struct {
	n int
	// ownBlocks records whether a side's own links block each other, which the
	// paper-and-pencil ruleset switches off.
	ownBlocks bool

	pegs []game.Player
	link []uint8

	// block[s][i] bit d is set when a link owned by side s crosses the link
	// leaving hole i in direction d.
	block [2][]uint8
	// use[s][i] reports whether side s could ever own hole i.
	use [2][]bool
	// cost[s][i] is the number of pegs side s must place to own hole i.
	cost [2][]int8
	// dist[s][b][i] is the cheapest cost of a chain from side s's border b to
	// hole i, counting hole i itself.
	dist [2][2][]int32
	// span[s][i] is the cost of the cheapest chain that runs through hole i.
	span [2][]int32

	// need[s] is the number of pegs side s must still place to join its two
	// borders: 0 means it already has, 1 means it wins with its next move, and
	// NoChain means no chain of any length is left to it.
	need [2]int
	// bottlenecks[s] counts the holes every one of side s's cheapest chains
	// must run through. A chain whose every step has an alternative cannot be
	// cut by one peg; a chain that is a single forced line can be cut at every
	// step. Unlike a raw count of holes on cheapest chains, this does not grow
	// merely because a side is far from finishing, so the search cannot inflate
	// it by refusing to commit.
	bottlenecks [2]int
	// ground[s] is how much more of the board side s can reach cheaply than the
	// opponent, in parts per thousand of the holes either side can use.
	ground [2]int

	hash uint64

	// levels counts, per prefix cost, how many holes a cheapest chain could use
	// at that step. It is scratch for the bottleneck count.
	levels []int32

	qa, qb []int32
}

func (a *analysis) resize(n int) {
	if a.n == n {
		return
	}
	cells := n * n
	a.n = n
	a.pegs = make([]game.Player, cells)
	a.link = make([]uint8, cells)
	for s := range 2 {
		a.block[s] = make([]uint8, cells)
		a.use[s] = make([]bool, cells)
		a.cost[s] = make([]int8, cells)
		a.span[s] = make([]int32, cells)
		for b := range 2 {
			a.dist[s][b] = make([]int32, cells)
		}
	}
	a.levels = make([]int32, cells+2)
	a.qa = make([]int32, 0, cells)
	a.qb = make([]int32, 0, cells)
}

// load reads the position and computes every derived quantity.
func (a *analysis) load(g *game.Game) {
	n := g.Size()
	a.resize(n)
	a.ownBlocks = !g.Rules().OwnLinksMayCross
	clear(a.block[0])
	clear(a.block[1])

	z := zobristFor(n)
	var hash uint64
	if g.Turn() == game.Horizontal {
		hash ^= z.turn
	}

	for row := range n {
		base := row * n
		for col := range n {
			i := base + col
			p := game.Point{Col: col, Row: row}
			pl := g.At(p)
			mask := g.LinkMask(p)
			a.pegs[i] = pl
			a.link[i] = mask

			// Vertical joins the top and bottom rows, so it may use every row
			// but not the outer columns; Horizontal is the mirror image.
			vertOK := col > 0 && col < n-1 && pl != game.Horizontal
			horzOK := row > 0 && row < n-1 && pl != game.Vertical
			a.use[0][i] = vertOK
			a.use[1][i] = horzOK
			a.cost[0][i] = 1
			a.cost[1][i] = 1
			switch pl {
			case game.Vertical:
				a.cost[0][i] = 0
				hash ^= z.peg[0][i]
			case game.Horizontal:
				a.cost[1][i] = 0
				hash ^= z.peg[1][i]
			}

			if mask == 0 || pl == game.NoPlayer {
				continue
			}
			s := sideIndex(pl)
			for d := range 4 {
				if mask&(1<<uint(d)) == 0 {
					continue
				}
				hash ^= z.link[d][i]
				for _, off := range crossTable[d] {
					c, r := col+off.dCol, row+off.dRow
					if c < 0 || c >= n || r < 0 || r >= n {
						continue
					}
					j := r*n + c
					a.block[s][j] |= 1 << off.dir
					c2, r2 := c+dirDelta[off.dir][0], r+dirDelta[off.dir][1]
					if c2 < 0 || c2 >= n || r2 < 0 || r2 >= n {
						continue
					}
					a.block[s][r2*n+c2] |= 1 << oppositeDir[off.dir]
				}
			}
		}
	}
	a.hash = hash

	for s := range 2 {
		a.sweep(s, 0, a.dist[s][0])
		a.sweep(s, 1, a.dist[s][1])
		a.summarise(s)
	}
	a.measureGround()
}

// measureGround counts, for each side, the holes it could bring into a cheapest
// chain for less than the opponent could, and expresses the difference in parts
// per thousand of the holes either side can use at all.
//
// This exists because the peg counts alone are far too coarse to steer a deep
// search: on a small board they take perhaps eight distinct values, so most
// leaves of a deep tree score identically and the choice between them falls to
// whichever was generated first. Reach over the board changes with almost every
// peg, so it separates positions the peg counts cannot tell apart, while being
// weighted low enough that it never outranks a peg of real progress.
func (a *analysis) measureGround() {
	mine, theirs := a.span[0], a.span[1]
	lead, total := 0, 0
	for i := range mine {
		m, o := mine[i], theirs[i]
		if m >= unusable && o >= unusable {
			continue
		}
		total++
		switch {
		case m < o:
			lead++
		case o < m:
			lead--
		}
	}
	if total == 0 {
		a.ground = [2]int{}
		return
	}
	per := lead * 1000 / total
	a.ground[0], a.ground[1] = per, -per
}

// borderHole reports whether hole (col,row) lies on border b of side s.
func borderHole(s, b, col, row, n int) bool {
	line := 0
	if b == 1 {
		line = n - 1
	}
	if s == 0 {
		return row == line
	}
	return col == line
}

// linkOpen reports whether side s can travel from hole i to hole j in direction
// d: either the link already stands, or it can still be created. Two holes that
// both already hold a peg with no link between them are not joined, because
// creating that link is a move in its own right.
func (a *analysis) linkOpen(s, i int, d game.Dir, j int) bool {
	if a.link[i]&(1<<d) != 0 {
		return true
	}
	if a.pegs[i] != game.NoPlayer && a.pegs[j] != game.NoPlayer {
		return false
	}
	if a.block[1-s][i]&(1<<d) != 0 {
		return false
	}
	if a.ownBlocks && a.block[s][i]&(1<<d) != 0 {
		return false
	}
	return true
}

// sweep fills dst with the cheapest cost of reaching each hole from border b of
// side s. Zero-cost steps stay in the current layer and one-cost steps start
// the next, which is a breadth-first search rather than a priority queue.
func (a *analysis) sweep(s, b int, dst []int32) {
	n := a.n
	for i := range dst {
		dst[i] = unusable
	}
	cur := a.qa[:0]
	next := a.qb[:0]

	for row := range n {
		base := row * n
		for col := range n {
			if !borderHole(s, b, col, row, n) {
				continue
			}
			i := base + col
			if !a.use[s][i] {
				continue
			}
			c := int32(a.cost[s][i])
			if c >= dst[i] {
				continue
			}
			dst[i] = c
			if c == 0 {
				cur = append(cur, int32(i))
			} else {
				next = append(next, int32(i))
			}
		}
	}

	level := int32(0)
	for {
		for k := 0; k < len(cur); k++ {
			u := int(cur[k])
			if dst[u] != level {
				continue
			}
			col, row := u%n, u/n
			for d := range game.Dir(game.NumDirs) {
				c, r := col+dirDelta[d][0], row+dirDelta[d][1]
				if c < 0 || c >= n || r < 0 || r >= n {
					continue
				}
				v := r*n + c
				if !a.use[s][v] {
					continue
				}
				step := level + int32(a.cost[s][v])
				if step >= dst[v] {
					continue
				}
				if !a.linkOpen(s, u, d, v) {
					continue
				}
				dst[v] = step
				if step == level {
					cur = append(cur, int32(v))
				} else {
					next = append(next, int32(v))
				}
			}
		}
		if len(next) == 0 {
			break
		}
		cur, next = next, cur[:0]
		level++
	}
	a.qa, a.qb = cur[:0], next[:0]
}

// summarise derives need, span and the bottleneck count for one side from its
// two sweeps.
//
// Along a cheapest chain the empty holes carry prefix costs 1, 2, ... need in
// order, exactly one per step. So the holes a cheapest chain could use at step
// L are the empty on-chain holes whose distance from the first border is L, and
// a step with only one such hole is a hole every cheapest chain must use: one
// opposing peg there makes the whole plan more expensive.
func (a *analysis) summarise(s int) {
	n := a.n
	from, to := a.dist[s][0], a.dist[s][1]
	best := unusable
	for row := range n {
		base := row * n
		for col := range n {
			i := base + col
			if borderHole(s, 1, col, row, n) && from[i] < best {
				best = from[i]
			}
			if from[i] >= unusable || to[i] >= unusable {
				a.span[s][i] = unusable
				continue
			}
			a.span[s][i] = from[i] + to[i] - int32(a.cost[s][i])
		}
	}
	if best >= unusable {
		a.need[s] = NoChain
		a.bottlenecks[s] = 0
		return
	}
	a.need[s] = int(best)
	levels := a.levels[:best+1]
	clear(levels)
	for i := range a.span[s] {
		if a.span[s][i] != best || a.pegs[i] != game.NoPlayer || !a.use[s][i] {
			continue
		}
		if step := from[i]; step >= 1 && step <= best {
			levels[step]++
		}
	}
	forced := 0
	for _, count := range levels[1:] {
		if count == 1 {
			forced++
		}
	}
	a.bottlenecks[s] = forced
}

// winningHole returns the hole that completes side s's chain, valid only when
// need[s] is 1.
func (a *analysis) winningHole(s int) (game.Point, bool) {
	for i := range a.span[s] {
		if a.pegs[i] == game.NoPlayer && a.use[s][i] && a.span[s][i] == 1 {
			return game.Point{Col: i % a.n, Row: i / a.n}, true
		}
	}
	return game.Point{}, false
}

// hasNeighbourPeg reports whether any of a hole's eight knight neighbours holds
// a peg of either colour. It is the cheap "is anything happening here" signal
// that keeps move ordering away from empty corners of the board, and it runs
// once per candidate at every node, so it stops at the first hit.
func (a *analysis) hasNeighbourPeg(i int) bool {
	n := a.n
	col, row := i%n, i/n
	for d := range game.Dir(game.NumDirs) {
		c, r := col+dirDelta[d][0], row+dirDelta[d][1]
		if c < 0 || c >= n || r < 0 || r >= n {
			continue
		}
		if a.pegs[r*n+c] != game.NoPlayer {
			return true
		}
	}
	return false
}

// NoChain is the Dist of a side that can no longer join its two borders at all,
// because the opponent's links seal every route. It is a distinct value rather
// than a large number so that a caller cannot mistake it for a peg count.
const NoChain = -1

// Terms is the decomposition of a static evaluation, in the units the hint
// feature talks about. Every score the search produces at a leaf is a function
// of these numbers and nothing else, which is what makes a derived explanation
// checkable.
type Terms struct {
	// Dist is the number of pegs the side still needs to finish its chain.
	// Zero means the chain is complete; NoChain means there is no route left.
	Dist int
	// OppDist is the same count for the opponent.
	OppDist int
	// Bottlenecks counts the holes every one of the side's cheapest chains has
	// to run through, so a single opposing peg would set the plan back. Zero
	// means every step of the plan has an alternative.
	Bottlenecks int
	// OppBottlenecks is the same count for the opponent.
	OppBottlenecks int
	// Ground is how much more of the board the side can reach cheaply than the
	// opponent, in parts per thousand of the usable holes. It is negative when
	// the opponent has the wider reach.
	Ground int
}

// Evaluation weights. Distance dominates: a peg of head start outweighs any
// amount of redundancy or reach, so those two terms only ever decide between
// moves that leave the peg counts level. Together they are worth less than one
// peg, and reach is deliberately the finest of the three so that it can
// separate positions the other two score alike. A side with no route at all is
// scored as if it were a long way from finishing, which is far enough to be
// hopeless without reaching the values reserved for a proven win.
const (
	distWeight       = 1024
	bottleneckWeight = 192
	groundDivisor    = 3
	blockedDist      = 512
)

// distValue turns a Dist into a number that can be arithmetic on.
func distValue(d int) int {
	if d == NoChain {
		return blockedDist
	}
	return d
}

// Score is the static value of the position for the side the terms describe.
func (t Terms) Score() int {
	return distWeight*(distValue(t.OppDist)-distValue(t.Dist)) +
		bottleneckWeight*(t.OppBottlenecks-t.Bottlenecks) +
		t.Ground/groundDivisor
}

// distScore is the impoverished score the beginner tier plays on: pegs only,
// with no sense of whether a plan can be cut or of who holds more ground.
func (t Terms) distScore() int {
	return distWeight * (distValue(t.OppDist) - distValue(t.Dist))
}

// terms reads the decomposition out of an analysis from one side's point of
// view.
func (a *analysis) terms(me game.Player) Terms {
	m, o := sideIndex(me), sideIndex(me.Opponent())
	return Terms{
		Dist:           a.need[m],
		OppDist:        a.need[o],
		Bottlenecks:    a.bottlenecks[m],
		OppBottlenecks: a.bottlenecks[o],
		Ground:         a.ground[m],
	}
}
