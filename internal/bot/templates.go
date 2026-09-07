package bot

import (
	"slices"

	"github.com/BAKocska/twixtui/internal/game"
)

// An edge template is the one thing this package says about a position that is
// not a heuristic. Everything in eval.go is a static count that assumes the
// opponent stands still; a template is a claim that the opponent may move as
// they like and still cannot stop a particular connection from being made, and
// nothing is called a template here until an exhaustive search of the real
// engine has demonstrated exactly that. templates_prove_test.go is that search,
// and it runs over every entry below: a corpus nobody has proved is a corpus of
// guesses, and a wrong template is a wrong evaluation in every position it
// matches.
//
// The claim, stated once and for all. Take Vertical, which joins row 0 to row
// n-1, and a Vertical peg — the anchor — sitting depth rows below row 0. The
// template names two sets of holes relative to that anchor: cells, the holes
// the connection is allowed to use, and guard, the holes nobody may be standing
// in. If every cell is on the board, usable by Vertical and empty, and every
// guard hole that is on the board is empty, then with the OPPONENT TO MOVE
// Vertical can place need pegs, all of them in cells, and end with the anchor
// linked to row 0, whatever the opponent does in between.
//
// Two details of that sentence carry the weight.
//
// The first is "whatever the opponent does", which has to mean anywhere on the
// board and not merely anywhere in the carrier. It does, and the guard set is
// why. A peg that is not in the carrier cannot occupy a cell, so the only way
// it could interfere is by being one end of a link that crosses a link the
// connection needs. The guard is computed as exactly that: every hole that
// could be an endpoint of a link crossing any link the connection might make,
// with the holes beyond the border row — which no peg can ever occupy —
// dropped, since a link with a phantom endpoint can never exist. Both endpoints
// of such a crossing link are therefore in the carrier, so a crossing link
// needs two carrier holes, and an empty carrier has none. Play outside the
// carrier is then provably irrelevant, which is what lets the prover restrict
// the opponent to the carrier and still search exhaustively.
//
// The second is need. The plain claim — the connection can be completed
// eventually — would not justify what eval.go does with it. Eval counts a
// bottleneck when every cheapest chain has to run through one particular hole,
// on the grounds that an opposing peg there sets the plan back a peg; a
// connection that survives interference only by costing an extra peg is set
// back, so it is still a bottleneck. So the proof carries a placement budget:
// the connection must be completed within need placements, need being the
// cheapest count from the anchor to the border with the board otherwise empty.
// That is the stronger claim, and it is the one that makes the exemption in
// summarise honest.
//
// What is deliberately not claimed: nothing here says the side owning the
// anchor may fill its own carrier and keep the guarantee. The connection is
// proved against opponent play, and the prover never lets the anchor's side
// place outside cells. On a real board the side may of course play a guard hole
// anyway — its links are taken automatically and one of them may be the very
// link that blocks the connection — and when it does, the carrier is no longer
// empty and the template simply stops matching at the next node. The guarantee
// is re-established from the position at hand at every node rather than
// remembered across moves, so there is no stale claim to go wrong.
//
// Last, what this corpus is currently worth, which is not much: no tier
// switches the lever on. Only the two-row template ever changes a bottleneck
// count on an unarranged position — about one position in eighty — and a paired
// match of the lever on against off found no gain. The corpus is kept because a
// proof does not expire and because the next attempt should start from a
// prover, not from an argument; it is switched off because a proof is not a
// strength result. See docs/MANUAL.md and .work/bot-slices/edge-templates.md.

// tmplCell is a hole named by its offset from a template's anchor. Columns grow
// east and rows grow south, as everywhere else in the engine.
type tmplCell struct{ dCol, dRow int }

func (c tmplCell) point() game.Point { return game.Point{Col: c.dCol, Row: c.dRow} }

// edgeTemplate is one claim, written in the frame in which Vertical connects
// upwards to row 0, so every cell has a negative dRow.
type edgeTemplate struct {
	// name identifies the claim in the proofs, the report and nowhere else.
	name string
	// depth is how many rows separate the anchor from the border row.
	depth int
	// need is the placement budget the proof honours, which is also the
	// cheapest count from the anchor to the border on an empty board.
	need int
	// cells are the holes the connection may use. All of them must be on the
	// board, usable by the side and empty for the template to match.
	cells []tmplCell
	// guard are the holes that must be empty so that no link crossing the
	// connection can already exist or be built from one peg. It is computed by
	// templateGuard rather than written by hand.
	guard []tmplCell
}

// baseTemplates is the corpus, in the Vertical-upwards frame. Every entry is
// proved by templates_prove_test.go; an entry added here without a proof fails
// that test, which is the whole point of keeping the two together.
//
// r2 is a peg two rows from the border, which reaches it in one placement
// through either of the two holes a knight's move away on the border row. It is
// written as two one-cell claims rather than one two-cell claim on purpose: a
// single landing hole is already unstoppable, because the opponent may never
// stand on the anchor's own border row and one peg makes no link, so the weaker
// precondition is the useful one. The eval only ever asks about a hole that is
// the sole survivor at its step anyway.
//
// r3 is a peg three rows out. It reaches the border in two placements through
// one of the two holes a knight's move away on the row next to the border, and
// from there onto the border row itself. The opponent's first move can take one
// of the two, never both, and the second leg — a link from the row next to the
// border onto the border row — cannot be crossed by any opposing link at all,
// because both ends of any such link would have to lie beyond the border row or
// on it, where the opponent may not play.
//
// r4 is a peg four rows out, reaching the border in two placements through one
// of the two holes two rows up, each of which has two landing holes on the
// border row. Here the second leg can be crossed, so the proof is doing real
// work: the opponent gets two pegs and therefore one link, and the search shows
// no single link answers both landing holes of whichever intermediate the side
// picks.
var baseTemplates = []edgeTemplate{
	{
		name:  "r2-west",
		depth: 2,
		need:  1,
		cells: []tmplCell{{-1, -2}},
	},
	{
		name:  "r3",
		depth: 3,
		need:  2,
		cells: []tmplCell{{-1, -2}, {1, -2}, {-1, -3}, {1, -3}},
	},
	{
		name:  "r4",
		depth: 4,
		need:  2,
		cells: []tmplCell{{-1, -2}, {1, -2}, {-2, -4}, {0, -4}, {2, -4}},
	},
}

// templateLinks lists every link the connection might come to hold: every pair
// of carrier cells, and every cell with the anchor, that stand a knight's move
// apart. Placements take every link they are offered, so a link between two
// cells is one the connection might hold whether it wanted it or not, and the
// closure has to account for all of them.
func templateLinks(t edgeTemplate) []game.Link {
	holes := append([]tmplCell{{0, 0}}, t.cells...)
	var out []game.Link
	for i, a := range holes {
		for _, b := range holes[i+1:] {
			if l, ok := game.NewLink(a.point(), b.point()); ok {
				out = append(out, l)
			}
		}
	}
	return out
}

// templateGuard computes the carrier closure: every hole that could hold a peg
// at one end of a link crossing one of the connection's own links.
//
// A crossing link with an endpoint beyond the border row is discarded whole
// rather than half: no peg ever stands there, so that link can never exist and
// its other endpoint threatens nothing. That is what keeps the guard finite and
// small enough to be worth checking. Holes on the border row itself are kept:
// the opponent may not stand there, but the anchor's own side may, and its own
// links block it too under the printed rules.
//
// The crossing offsets come from crossTable, which is derived from
// game.LinksCross, so this cannot disagree with the engine about what crosses
// what.
func templateGuard(t edgeTemplate) []tmplCell {
	carrier := map[tmplCell]bool{{0, 0}: true}
	for _, c := range t.cells {
		carrier[c] = true
	}
	seen := map[tmplCell]bool{}
	var guard []tmplCell
	for _, l := range templateLinks(t) {
		for _, off := range crossTable[l.Dir] {
			from := tmplCell{l.From.Col + off.dCol, l.From.Row + off.dRow}
			to := tmplCell{from.dCol + dirDelta[off.dir][0], from.dRow + dirDelta[off.dir][1]}
			if from.dRow < -t.depth || to.dRow < -t.depth {
				continue
			}
			for _, end := range [2]tmplCell{from, to} {
				if carrier[end] || seen[end] {
					continue
				}
				seen[end] = true
				guard = append(guard, end)
			}
		}
	}
	slices.SortFunc(guard, func(a, b tmplCell) int {
		if a.dRow != b.dRow {
			return a.dRow - b.dRow
		}
		return a.dCol - b.dCol
	})
	return guard
}

// tmplSymmetry is one of the eight isometries of the square board. Each maps
// knight's moves onto knight's moves and crossings onto crossings, so each maps
// a proved template onto a proved template — a fact the proofs re-check on a
// derived instance rather than take on trust.
type tmplSymmetry struct {
	name  string
	apply func(tmplCell) tmplCell
}

var tmplSymmetries = []tmplSymmetry{
	{"identity", func(c tmplCell) tmplCell { return c }},
	{"mirrored columns", func(c tmplCell) tmplCell { return tmplCell{-c.dCol, c.dRow} }},
	{"mirrored rows", func(c tmplCell) tmplCell { return tmplCell{c.dCol, -c.dRow} }},
	{"half turn", func(c tmplCell) tmplCell { return tmplCell{-c.dCol, -c.dRow} }},
	{"transposed", func(c tmplCell) tmplCell { return tmplCell{c.dRow, c.dCol} }},
	{"anti-transposed", func(c tmplCell) tmplCell { return tmplCell{-c.dRow, -c.dCol} }},
	{"quarter turn", func(c tmplCell) tmplCell { return tmplCell{-c.dRow, c.dCol} }},
	{"three-quarter turn", func(c tmplCell) tmplCell { return tmplCell{c.dRow, -c.dCol} }},
}

// placedTemplate is a base template in one orientation: which side it belongs
// to, which of that side's two borders it reaches, and the offsets in that
// frame.
type placedTemplate struct {
	name   string
	base   string
	side   int
	border int
	depth  int
	need   int
	cells  []tmplCell
	guard  []tmplCell
}

// templateCorpus holds the proved templates by side and border, and
// templateReach is how far from a border a cell of any of them can lie, which
// lets the evaluation reject a hole in the middle of the board with one
// comparison instead of a scan.
var templateCorpus, templateReach = buildTemplateCorpus()

func buildTemplateCorpus() (corpus [2][2][]placedTemplate, reach [2][2]int) {
	for _, base := range baseTemplates {
		base.guard = templateGuard(base)
		for _, sym := range tmplSymmetries {
			p := placeTemplate(base, sym)
			bucket := corpus[p.side][p.border]
			if slices.ContainsFunc(bucket, func(q placedTemplate) bool {
				return slices.Equal(q.cells, p.cells) && slices.Equal(q.guard, p.guard)
			}) {
				continue
			}
			corpus[p.side][p.border] = append(bucket, p)
		}
	}
	for s := range corpus {
		for b := range corpus[s] {
			for _, t := range corpus[s][b] {
				for _, c := range t.cells {
					if d := t.depth + inward(s, b, c); d > reach[s][b] {
						reach[s][b] = d
					}
				}
			}
		}
	}
	return corpus, reach
}

// inward is how much further from the border a cell lies than the anchor does,
// in the frame of one side and border. It is negative for every cell of every
// template, which is only to say that a template's cells all sit between its
// anchor and the border.
func inward(side, border int, c tmplCell) int {
	step := c.dRow
	if side == 1 {
		step = c.dCol
	}
	if border == 1 {
		return -step
	}
	return step
}

// placeTemplate applies one symmetry to a base template written in the
// Vertical-upwards frame. Which side and border the result belongs to is read
// off the image of the direction the base template points in, so the mapping is
// derived from the symmetry rather than tabulated beside it.
func placeTemplate(base edgeTemplate, sym tmplSymmetry) placedTemplate {
	towards := sym.apply(tmplCell{0, -1})
	p := placedTemplate{
		name:  base.name + " " + sym.name,
		base:  base.name,
		depth: base.depth,
		need:  base.need,
		cells: make([]tmplCell, len(base.cells)),
		guard: make([]tmplCell, len(base.guard)),
	}
	switch {
	case towards.dRow < 0:
		p.side, p.border = 0, 0
	case towards.dRow > 0:
		p.side, p.border = 0, 1
	case towards.dCol < 0:
		p.side, p.border = 1, 0
	default:
		p.side, p.border = 1, 1
	}
	for i, c := range base.cells {
		p.cells[i] = sym.apply(c)
	}
	for i, c := range base.guard {
		p.guard[i] = sym.apply(c)
	}
	sortCells(p.cells)
	sortCells(p.guard)
	return p
}

func sortCells(cs []tmplCell) {
	slices.SortFunc(cs, func(a, b tmplCell) int {
		if a.dRow != b.dRow {
			return a.dRow - b.dRow
		}
		return a.dCol - b.dCol
	})
}

// borderDistance is how many lines separate a hole from border b of side s.
func borderDistance(s, b, n, col, row int) int {
	line := row
	if s == 1 {
		line = col
	}
	if b == 1 {
		return n - 1 - line
	}
	return line
}

// templateFrees reports whether hole is a cell of a matching template.
func (a *analysis) templateFrees(s, hole int, best int32) bool {
	_, _, ok := a.templateMatch(s, hole, best)
	return ok
}

// templateMatch finds the template that frees a hole, and the hole its anchor
// stands in: one anchored on a peg of side s that lies on a cheapest chain, at
// exactly the template's distance from the border, with the whole carrier
// empty. It returns the match so that a test can put the very position the
// evaluation matched on back through the prover, which is the only way to know
// that the conditions checked here are the conditions the proof was about.
//
// It is asked only about a hole that is the sole survivor at its step of a
// cheapest chain, and only about a hole close enough to a border for a cell to
// reach, so the scan below runs a handful of times per position at most.
func (a *analysis) templateMatch(s, hole int, best int32) (*placedTemplate, int, bool) {
	n := a.n
	col, row := hole%n, hole/n
	for b := range 2 {
		if borderDistance(s, b, n, col, row) > templateReach[s][b] {
			continue
		}
		for i := range templateCorpus[s][b] {
			t := &templateCorpus[s][b][i]
			for _, c := range t.cells {
				ac, ar := col-c.dCol, row-c.dRow
				if ac < 0 || ac >= n || ar < 0 || ar >= n {
					continue
				}
				anchor := ar*n + ac
				if a.pegs[anchor] != game.Player(s+1) {
					continue
				}
				// The template is a statement about a peg standing exactly
				// depth lines from the border: its cells are the holes between
				// that peg and the border, and the proof is about the border
				// they land on. A peg at any other distance is a peg the
				// offsets happen to fit around, and matching there would
				// exempt a hole nothing was ever proved about.
				if borderDistance(s, b, n, ac, ar) != t.depth {
					continue
				}
				// It also has to be on a cheapest chain, and the chain has to
				// reach the border through the template rather than past it:
				// the pegs the chain would place between the border and the
				// anchor are exactly the template's own.
				if a.span[s][anchor] != best || int(a.dist[s][b][anchor]) != t.need {
					continue
				}
				if a.templateClear(s, t, ac, ar) {
					return t, anchor, true
				}
			}
		}
	}
	return nil, 0, false
}

// templateClear checks the carrier of one template anchored at one hole: every
// cell on the board, usable and empty, and every guard hole that exists at all
// empty. A guard hole off the board is not a failure but the opposite: nothing
// can ever stand there, so the link it would have been an end of can never be
// built.
func (a *analysis) templateClear(s int, t *placedTemplate, ac, ar int) bool {
	n := a.n
	for _, c := range t.cells {
		col, row := ac+c.dCol, ar+c.dRow
		if col < 0 || col >= n || row < 0 || row >= n {
			return false
		}
		i := row*n + col
		if a.pegs[i] != game.NoPlayer || !a.use[s][i] {
			return false
		}
	}
	for _, c := range t.guard {
		col, row := ac+c.dCol, ar+c.dRow
		if col < 0 || col >= n || row < 0 || row >= n {
			continue
		}
		if a.pegs[row*n+col] != game.NoPlayer {
			return false
		}
	}
	return true
}
