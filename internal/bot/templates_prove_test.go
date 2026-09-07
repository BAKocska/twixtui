package bot

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// The prover is the oracle behind templates.go, and it is written to share as
// little as possible with the thing it certifies.
//
// It does not call the evaluation, the search or their tables: a template
// certified by the code it is meant to police would only show the two agree. It
// plays the real engine — game.Game, placements with their automatic linking,
// the engine's own crossing rule — and reads the result off the engine's link
// graph by walking it, not out of an analysis. The only thing it takes from
// templates.go is the claim itself: which holes the connection may use and how
// many placements it may spend.
//
// In particular the opponent is not restricted to the carrier. The closure
// argument in templates.go says opponent play outside the carrier cannot
// matter, and that argument would be a fine reason to search a smaller tree —
// but if the closure were wrong, the smaller tree would hide exactly the
// counter-example the closure got wrong, and the proof would certify the bug.
// So the opponent may play any hole on the board that is legal for it, at every
// turn, and the closure is checked separately and independently by
// TestEdgeTemplateCarrierIsClosed. The proof therefore stands on its own: on an
// otherwise empty board, with the opponent free to play anywhere, the
// connection is made within its budget.
//
// The side owning the anchor is restricted to the carrier's cells, which is the
// direction that makes the claim harder to prove rather than easier.

// tmplProofCap is the number of positions one proof may enter. It exists so
// that a template nobody can prove fails loudly instead of running until the
// test timeout and being quietly assumed. Every proof in this file finishes two
// orders of magnitude inside it; a search that reaches it is reported as capped
// and is never reported as proved.
const tmplProofCap = 5_000_000

// tmplProof is what one run of the prover found.
type tmplProof struct {
	proved bool
	capped bool
	// decided is how many branches ended in a won game before the connection
	// question could be settled. It is zero for every proof on an empty board.
	decided int
	nodes   int64
	elapsed time.Duration
	// refutation is the opponent's answer when the claim is false: the moves
	// down to the point where the side ran out of budget without connecting.
	refutation []string
	err        error
}

func (r tmplProof) failure() string {
	return fmt.Sprintf("proved=%v capped=%v err=%v refutation=%v nodes=%d", r.proved, r.capped, r.err, r.refutation, r.nodes)
}

type tmplProver struct {
	tmpl   placedTemplate
	anchor game.Point
	side   game.Player
	opp    game.Player
	n      int
	// cells are the holes the side may place in, in board coordinates.
	cells []game.Point
	// pass is a hole the opponent may use that is provably irrelevant, so that
	// declining to interfere is one of the moves the search considers. It is
	// tried first, and it is only set for a proof on an otherwise empty board,
	// where such a hole can be identified.
	pass    game.Point
	hasPass bool
	// oppMoves is every hole the opponent may ever use, the pass first. It is
	// the whole board rather than the carrier on purpose.
	oppMoves []game.Point
	// decided counts the branches in which the game ended before the question
	// could be answered, which the claim says nothing about.
	decided int
	nodes   int64
	capped  bool
	err     error
	stack   []string
	refute  []string
}

// onBorder reports whether a hole lies on the border line the template reaches.
func (p *tmplProver) onBorder(q game.Point) bool {
	line := q.Row
	if p.tmpl.side == 1 {
		line = q.Col
	}
	if p.tmpl.border == 1 {
		return line == p.n-1
	}
	return line == 0
}

// reachesBorder walks the engine's link graph from the anchor and reports
// whether it arrives on the border line. It reads g.LinkMask and g.At, which is
// the position as the engine holds it, so nothing about the evaluation's view
// of connectivity can make a proof succeed.
func (p *tmplProver) reachesBorder(g *game.Game) bool {
	seen := make([]bool, p.n*p.n)
	seen[p.anchor.Row*p.n+p.anchor.Col] = true
	stack := []game.Point{p.anchor}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.onBorder(cur) {
			return true
		}
		mask := g.LinkMask(cur)
		for d := range game.Dir(game.NumDirs) {
			if mask&(1<<d) == 0 {
				continue
			}
			q := cur.Add(d)
			if !g.InBounds(q) || g.At(q) != p.side {
				continue
			}
			i := q.Row*p.n + q.Col
			if seen[i] {
				continue
			}
			seen[i] = true
			stack = append(stack, q)
		}
	}
	return false
}

func (p *tmplProver) enter() bool {
	p.nodes++
	if p.nodes > tmplProofCap {
		p.capped = true
	}
	return !p.capped && p.err == nil
}

// oppTurn is the opponent to move with the side holding budget placements. It
// returns true when the side connects against every reply, which is the
// definition being proved.
func (p *tmplProver) oppTurn(g *game.Game, budget int) bool {
	if !p.enter() {
		return false
	}
	if p.reachesBorder(g) {
		return true
	}
	if g.Result().Over() {
		// Somebody has won the game outright. A template says the opponent
		// cannot stop this connection, not that they cannot win elsewhere, so
		// a branch the game itself decides is outside the claim and is not
		// counted against it. On an empty board this never happens, and the
		// corpus proofs check that it did not.
		p.decided++
		return true
	}
	if budget == 0 {
		p.refute = slices.Clone(p.stack)
		return false
	}
	if g.Turn() != p.opp {
		p.err = fmt.Errorf("prover desynchronised: %s to move where the opponent %s was expected", g.Turn(), p.opp)
		return false
	}
	for i, m := range p.oppMoves {
		if g.CanPlace(p.opp, m) != nil {
			continue
		}
		next := g.Clone()
		if _, err := next.PlayPeg(m); err != nil {
			p.err = fmt.Errorf("opponent cannot play %v: %w", m, err)
			return false
		}
		label := p.opp.String() + " " + m.String()
		if i == 0 && p.hasPass {
			label += " (pass)"
		}
		p.stack = append(p.stack, label)
		ok := p.sideTurn(next, budget)
		p.stack = p.stack[:len(p.stack)-1]
		if !ok {
			if p.err != nil || p.capped {
				return false
			}
			p.refute = append(slices.Clone(p.stack), label)
			return false
		}
	}
	return true
}

// sideTurn is the side to move with budget placements left. It returns true
// when some placement inside the carrier's cells still forces the connection.
func (p *tmplProver) sideTurn(g *game.Game, budget int) bool {
	if !p.enter() {
		return false
	}
	if p.reachesBorder(g) {
		return true
	}
	if g.Result().Over() {
		p.decided++
		return true
	}
	if budget == 0 {
		return false
	}
	if g.Turn() != p.side {
		p.err = fmt.Errorf("prover desynchronised: %s to move where %s was expected", g.Turn(), p.side)
		return false
	}
	for _, m := range p.cells {
		if g.CanPlace(p.side, m) != nil {
			continue
		}
		next := g.Clone()
		if _, err := next.PlayPeg(m); err != nil {
			p.err = fmt.Errorf("side cannot play %v: %w", m, err)
			return false
		}
		p.stack = append(p.stack, p.side.String()+" "+m.String())
		ok := p.oppTurn(next, budget-1)
		p.stack = p.stack[:len(p.stack)-1]
		if p.err != nil || p.capped {
			return false
		}
		if ok {
			return true
		}
	}
	return false
}

// run is the search itself, from a position in which the anchor already stands
// and the opponent is to move.
func (p *tmplProver) run(g *game.Game) tmplProof {
	if p.hasPass {
		p.oppMoves = append(p.oppMoves, p.pass)
	}
	g.EachLegalPlacement(p.opp, func(q game.Point) bool {
		if !p.hasPass || q != p.pass {
			p.oppMoves = append(p.oppMoves, q)
		}
		return true
	})
	start := time.Now()
	proved := p.oppTurn(g, p.tmpl.need)
	return tmplProof{
		proved:     proved && !p.capped && p.err == nil,
		capped:     p.capped,
		decided:    p.decided,
		nodes:      p.nodes,
		elapsed:    time.Since(start),
		refutation: p.refute,
		err:        p.err,
	}
}

// tmplAnchorFor puts a template's anchor at its proper distance from its border
// and at the given position along the cross axis.
func tmplAnchorFor(t placedTemplate, n, cross int) game.Point {
	depth := t.depth
	if t.border == 1 {
		depth = n - 1 - depth
	}
	if t.side == 1 {
		return game.Point{Col: depth, Row: cross}
	}
	return game.Point{Col: cross, Row: depth}
}

// tmplCarrierPoints returns the anchor together with every carrier hole that
// exists on the board.
func tmplCarrierPoints(t placedTemplate, anchor game.Point, n int) []game.Point {
	out := []game.Point{anchor}
	for _, c := range slices.Concat(t.cells, t.guard) {
		q := game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow}
		if q.Col >= 0 && q.Col < n && q.Row >= 0 && q.Row < n {
			out = append(out, q)
		}
	}
	return out
}

// tmplParkHole finds a hole the opponent may use that is provably irrelevant:
// outside the carrier and not a knight's move from anything in it, so a peg
// there can neither take a hole the connection wants nor be one end of a link
// touching it. Playing it is the opponent's pass, and the proof has to survive
// it: a claim that only holds because the opponent was forced to interfere is
// not a claim about a template.
func tmplParkHole(t placedTemplate, anchor game.Point, n int, pl game.Player, taken []game.Point) (game.Point, bool) {
	carrier := tmplCarrierPoints(t, anchor, n)
	near := func(q game.Point) bool {
		for _, c := range slices.Concat(carrier, taken) {
			if c == q {
				return true
			}
			for d := range game.Dir(game.NumDirs) {
				if c.Add(d) == q {
					return true
				}
			}
		}
		return false
	}
	g := game.MustNew(smallRules(n))
	var found game.Point
	ok := false
	g.EachLegalPlacement(pl, func(q game.Point) bool {
		if near(q) {
			return true
		}
		found, ok = q, true
		return false
	})
	return found, ok
}

// newTmplProver sets a proof up on an empty board: the anchor placed, the
// opponent to move, and a pass hole identified.
//
// A template for Horizontal needs one extra move, because Vertical opens: the
// opponent spends it on a second irrelevant hole. That leaves the opponent a
// peg ahead of the claim rather than behind it, so the proof is if anything
// harder than the claim requires.
func newTmplProver(t *testing.T, tmpl placedTemplate, rs game.Ruleset, cross int) (*tmplProver, *game.Game) {
	t.Helper()
	n := rs.Size
	side := game.Player(tmpl.side + 1)
	anchor := tmplAnchorFor(tmpl, n, cross)
	p := &tmplProver{tmpl: tmpl, anchor: anchor, side: side, opp: side.Opponent(), n: n}
	for _, c := range tmpl.cells {
		q := game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow}
		if q.Col < 0 || q.Col >= n || q.Row < 0 || q.Row >= n {
			t.Fatalf("%s: cell %v falls off a %dx%d board with the anchor at %v", tmpl.name, c, n, n, anchor)
		}
		p.cells = append(p.cells, q)
	}
	park, ok := tmplParkHole(tmpl, anchor, n, p.opp, nil)
	if !ok {
		t.Fatalf("%s: no hole on a %dx%d board is far enough from the carrier to serve as the opponent's pass", tmpl.name, n, n)
	}
	p.pass, p.hasPass = park, true

	g := game.MustNew(rs)
	if side == game.Horizontal {
		opening, ok := tmplParkHole(tmpl, anchor, n, p.opp, []game.Point{park})
		if !ok {
			t.Fatalf("%s: no second irrelevant hole for the opening move", tmpl.name)
		}
		if _, err := g.PlayPeg(opening); err != nil {
			t.Fatalf("%s: opening peg %v: %v", tmpl.name, opening, err)
		}
	}
	if g.Turn() != side {
		t.Fatalf("%s: %s to move where the anchor's side %s was expected", tmpl.name, g.Turn(), side)
	}
	if _, err := g.PlayPeg(anchor); err != nil {
		t.Fatalf("%s: anchor %v: %v", tmpl.name, anchor, err)
	}
	return p, g
}

// tmplBase turns a corpus entry back into a placed template in the frame it was
// written in.
func tmplBase(base edgeTemplate) placedTemplate {
	base.guard = templateGuard(base)
	return placeTemplate(base, tmplSymmetries[0])
}

// tmplRulesets are the two rule choices that change what a proof means. The
// printed rules let a side's own links block each other, which can only make
// the connection harder; the paper-and-pencil rules let the opponent's links
// cross each other, which can only make the interference easier. Neither
// dominates the other, so both are proved.
func tmplRulesets(n int) []game.Ruleset {
	std, pp := game.Std, game.PP
	std.Size, pp.Size = n, n
	return []game.Ruleset{std, pp}
}

func tmplRulesName(rs game.Ruleset) string {
	if rs.OwnLinksMayCross {
		return "pp"
	}
	return "std"
}

// tmplCross is the coordinate along the border a template's anchor is placed
// at: the anchor's column for Vertical and its row for Horizontal.
func tmplCross(t placedTemplate, c tmplCell) int {
	if t.side == 1 {
		return c.dRow
	}
	return c.dCol
}

// tmplWalls returns the two extreme positions along the border at which every
// cell of a template is still a hole its side may use, which is what the
// evaluation checks before matching.
func tmplWalls(t placedTemplate, n int) (lo, hi int) {
	lo, hi = 1, n-2
	for _, c := range t.cells {
		if v := 1 - tmplCross(t, c); v > lo {
			lo = v
		}
		if v := n - 2 - tmplCross(t, c); v < hi {
			hi = v
		}
	}
	return lo, hi
}

// TestEdgeTemplatesAreProved is the corpus's licence to exist. Every entry the
// evaluation can match — all four orientations of every base template, and both
// reflections within each orientation — is proved here on two board sizes and
// under both rule choices, by an exhaustive search in which the opponent may
// play anywhere at all.
//
// Proving the derived orientations rather than only the frame they were written
// in is deliberate. templates.go rotates one proved claim into eight, which is
// sound because the board and the crossing rule are symmetric under those
// rotations; putting every rotation back through the prover is how that
// reasoning is checked instead of trusted.
func TestEdgeTemplatesAreProved(t *testing.T) {
	runs, entries := 0, 0
	for s := range 2 {
		for b := range 2 {
			for _, tmpl := range templateCorpus[s][b] {
				entries++
				for _, n := range []int{10, 12} {
					for _, rs := range tmplRulesets(n) {
						p, g := newTmplProver(t, tmpl, rs, n/2)
						got := p.run(g)
						if !got.proved {
							t.Fatalf("%s on %dx%d %s: NOT PROVED: %s", tmpl.name, n, n, tmplRulesName(rs), got.failure())
						}
						if got.decided != 0 {
							t.Fatalf("%s on %dx%d %s: %d branches ended in a won game, which cannot happen on an empty board and means the proof was not about the connection",
								tmpl.name, n, n, tmplRulesName(rs), got.decided)
						}
						runs++
						t.Logf("proved %-32s %dx%d %-3s anchor=%v need=%d cells=%d guard=%d opponent-moves=%d nodes=%d in %v",
							tmpl.name, n, n, tmplRulesName(rs), p.anchor, tmpl.need, len(tmpl.cells), len(tmpl.guard),
							len(p.oppMoves), got.nodes, got.elapsed.Round(time.Millisecond))
					}
				}
			}
		}
	}
	if want := tmplExpectedEntries(); entries != want || runs != entries*4 {
		t.Fatalf("proved %d runs over %d corpus entries, want %d entries and %d runs", runs, entries, want, want*4)
	}
}

// tmplMirrorInvariant reports whether a base template is unchanged by
// reflecting it across the line through its anchor, in which case the corpus
// holds one entry for it per orientation rather than two.
func tmplMirrorInvariant(base edgeTemplate) bool {
	base.guard = templateGuard(base)
	a := placeTemplate(base, tmplSymmetries[0])
	b := placeTemplate(base, tmplSymmetries[1])
	return slices.Equal(a.cells, b.cells) && slices.Equal(a.guard, b.guard)
}

// tmplExpectedEntries is how many entries the corpus should hold, derived from
// the base templates rather than read off the corpus it is checking.
func tmplExpectedEntries() int {
	per := 0
	for _, base := range baseTemplates {
		if tmplMirrorInvariant(base) {
			per++
		} else {
			per += 2
		}
	}
	return 4 * per
}

// TestEdgeTemplateProofSurvivesTheBoardEdge re-proves every entry with its
// anchor pushed against each of the two walls that run across its border. The
// proof on a central anchor generalises by translation, but only as far as the
// board's own edges, and against a wall part of the carrier falls off the
// board: those holes cannot be played by anybody, which takes options away from
// the opponent and none from the connection. That is the argument; this is the
// check.
func TestEdgeTemplateProofSurvivesTheBoardEdge(t *testing.T) {
	const n = 10
	rs := game.Std
	rs.Size = n
	for s := range 2 {
		for b := range 2 {
			for _, tmpl := range templateCorpus[s][b] {
				lo, hi := tmplWalls(tmpl, n)
				for _, cross := range []int{lo, hi} {
					p, g := newTmplProver(t, tmpl, rs, cross)
					got := p.run(g)
					if !got.proved || got.decided != 0 {
						t.Fatalf("%s against the wall at %d on %dx%d: %s (decided=%d)",
							tmpl.name, cross, n, n, got.failure(), got.decided)
					}
					t.Logf("proved %-32s at the wall anchor=%v nodes=%d in %v",
						tmpl.name, p.anchor, got.nodes, got.elapsed.Round(time.Millisecond))
				}
			}
		}
	}
}

// TestEdgeTemplateSymmetryIsAnIsometry checks the derivation machinery itself.
// Every corpus entry has to be the image of its base template under one of the
// eight symmetries of the square, and the four orientations have to hold the
// same entries as each other: a base that reached only three buckets, or an
// entry filed under the wrong border, would still be proved by the test above
// and would still be matched against the wrong border by the evaluation.
func TestEdgeTemplateSymmetryIsAnIsometry(t *testing.T) {
	counts := map[[2]int]map[string]int{}
	for s := range 2 {
		for b := range 2 {
			counts[[2]int{s, b}] = map[string]int{}
			for _, tmpl := range templateCorpus[s][b] {
				counts[[2]int{s, b}][tmpl.base]++
			}
			for _, tmpl := range templateCorpus[s][b] {
				var base edgeTemplate
				for _, cand := range baseTemplates {
					if cand.name == tmpl.base {
						base = cand
					}
				}
				if base.name == "" {
					t.Fatalf("%s names a base template that does not exist", tmpl.name)
				}
				base.guard = templateGuard(base)
				matched := ""
				for _, sym := range tmplSymmetries {
					img := placeTemplate(base, sym)
					if img.side == s && img.border == b && slices.Equal(img.cells, tmpl.cells) && slices.Equal(img.guard, tmpl.guard) {
						matched = sym.name
					}
				}
				if matched == "" {
					t.Fatalf("%s is filed under side %d border %d but is no symmetry image of %s that belongs there",
						tmpl.name, s, b, base.name)
				}
				if len(tmpl.cells) != len(base.cells) || len(tmpl.guard) != len(base.guard) ||
					tmpl.need != base.need || tmpl.depth != base.depth {
					t.Fatalf("%s does not have the shape of %s: cells %d/%d guard %d/%d need %d/%d depth %d/%d",
						tmpl.name, base.name, len(tmpl.cells), len(base.cells), len(tmpl.guard), len(base.guard),
						tmpl.need, base.need, tmpl.depth, base.depth)
				}
			}
		}
	}
	for key, byBase := range counts {
		for _, base := range baseTemplates {
			// A template that reflects onto itself across the line through its
			// anchor is one claim, not two; anything else has a genuine mirror
			// twin, and the evaluation needs both to cover both sides of the
			// anchor.
			want := 2
			if tmplMirrorInvariant(base) {
				want = 1
			}
			if got := byBase[base.name]; got != want {
				t.Fatalf("side %d border %d holds %d entries derived from %s, want %d",
					key[0], key[1], got, base.name, want)
			}
		}
		if len(byBase) != len(baseTemplates) {
			t.Fatalf("side %d border %d covers %d base templates, want all %d", key[0], key[1], len(byBase), len(baseTemplates))
		}
	}
}

// TestEdgeTemplateProverRejectsFalseTemplates is the prover's own control. A
// proof procedure that cannot fail proves nothing, so three claims that are
// false are put through it and all three have to come back refuted.
//
// The three are chosen to fail for three different reasons. The first has one
// intermediate hole where the connection needs a spare, and the opponent simply
// takes it. The second is the shipped r4 template with its two outer landing
// holes removed, which is refuted by an opponent link rather than by
// occupation, and which is the check that those two holes are load-bearing
// rather than decoration. The third names holes that do not join up at all, and
// has to be refuted by the opponent passing: a prover that credited the side
// with a connection it cannot build would pass every other test in this file.
func TestEdgeTemplateProverRejectsFalseTemplates(t *testing.T) {
	const n = 10
	rs := game.Std
	rs.Size = n
	for _, claim := range []edgeTemplate{
		{
			name:  "false r3, one intermediate",
			depth: 3, need: 2,
			cells: []tmplCell{{1, -2}, {-1, -3}},
		},
		{
			name:  "false r4, only the middle landing hole",
			depth: 4, need: 2,
			cells: []tmplCell{{-1, -2}, {1, -2}, {0, -4}},
		},
		{
			name:  "false r4, landing holes with no intermediate",
			depth: 4, need: 2,
			cells: []tmplCell{{-2, -4}, {0, -4}, {2, -4}},
		},
	} {
		tmpl := tmplBase(claim)
		p, g := newTmplProver(t, tmpl, rs, n/2)
		got := p.run(g)
		if got.err != nil || got.capped {
			t.Fatalf("%s: the prover errored (%v) or capped (%v) instead of deciding", claim.name, got.err, got.capped)
		}
		if got.proved {
			t.Fatalf("%s: the prover certified a false template, so its proofs are worthless", claim.name)
		}
		if len(got.refutation) == 0 {
			t.Fatalf("%s: refuted with no line to show for it", claim.name)
		}
		t.Logf("refuted %-42s after %5d nodes: %v", claim.name, got.nodes, got.refutation)
	}
}

// TestEdgeTemplateCarrierIsClosed checks the closure claim in templates.go by
// enumerating it again from the geometry, with no reference to crossTable or to
// templateGuard.
//
// The claim is: every link that crosses a link the connection might hold has
// both of its endpoints inside the carrier. If that holds, an empty carrier
// admits no crossing link at all, and a peg outside the carrier cannot become
// one end of one. The enumeration below is over real holes on a real board, so
// a crossing link with an endpoint that is not a hole is never built and never
// counted.
func TestEdgeTemplateCarrierIsClosed(t *testing.T) {
	const n = 16
	g := game.MustNew(smallRules(n))
	for s := range 2 {
		for b := range 2 {
			for _, tmpl := range templateCorpus[s][b] {
				anchor := tmplAnchorFor(tmpl, n, n/2)
				carrier := map[game.Point]bool{anchor: true}
				for _, c := range slices.Concat(tmpl.cells, tmpl.guard) {
					carrier[game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow}] = true
				}
				internal := tmplInternalLinks(tmpl, anchor)
				if len(internal) == 0 {
					t.Fatalf("%s: no internal links, so this check would be vacuous", tmpl.name)
				}
				checked := 0
				for row := range n {
					for col := range n {
						from := game.Point{Col: col, Row: row}
						if !g.Exists(from) {
							continue
						}
						for d := range game.Dir(game.NumDirs) {
							to := from.Add(d)
							if !g.InBounds(to) || !g.Exists(to) {
								continue
							}
							cand, ok := game.NewLink(from, to)
							if !ok {
								continue
							}
							for _, l := range internal {
								if !game.LinksCross(l, cand) {
									continue
								}
								checked++
								for _, end := range []game.Point{cand.From, cand.To()} {
									if !carrier[end] {
										t.Fatalf("%s: the link %v-%v crosses the connection's own %v-%v, but %v is outside the carrier: a peg there could block the connection and nothing checks it",
											tmpl.name, cand.From, cand.To(), l.From, l.To(), end)
									}
								}
							}
						}
					}
				}
				if checked == 0 {
					t.Fatalf("%s: found no crossing links at all, so this check proved nothing", tmpl.name)
				}
			}
		}
	}
}

// tmplInternalLinks is every link the connection might come to hold with its
// anchor at a given hole: the engine takes every link a placement offers, so it
// is every pair of cells, and every cell with the anchor, a knight's move
// apart.
func tmplInternalLinks(tmpl placedTemplate, anchor game.Point) []game.Link {
	holes := []game.Point{anchor}
	for _, c := range tmpl.cells {
		holes = append(holes, game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow})
	}
	var out []game.Link
	for i, a := range holes {
		for _, q := range holes[i+1:] {
			if l, ok := game.NewLink(a, q); ok {
				out = append(out, l)
			}
		}
	}
	return out
}

// TestEdgeTemplateGuardIsNotPadded is the other half of the closure check: the
// guard has to be the closure and not a comfortable superset of it. Every guard
// hole must be the endpoint of some link that really does cross one of the
// connection's own links, or it is a hole the evaluation demands be empty for
// no reason, which costs coverage and buys nothing.
func TestEdgeTemplateGuardIsNotPadded(t *testing.T) {
	const n = 16
	for _, base := range baseTemplates {
		tmpl := tmplBase(base)
		anchor := game.Point{Col: n / 2, Row: tmpl.depth}
		internal := tmplInternalLinks(tmpl, anchor)
		for _, c := range tmpl.guard {
			end := game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow}
			used := false
			for d := range game.Dir(game.NumDirs) {
				other := end.Add(d)
				if other.Row < 0 {
					continue // beyond the border row: not a hole, so not a link
				}
				cand, ok := game.NewLink(end, other)
				if !ok {
					continue
				}
				for _, l := range internal {
					if game.LinksCross(l, cand) {
						used = true
					}
				}
			}
			if !used {
				t.Errorf("%s: guard hole %v is an endpoint of no crossing link; it costs coverage and buys nothing", tmpl.name, c)
			}
		}
	}
}

// tmplBuild puts a position together from two lists of holes, played
// alternately from the opening move.
func tmplBuild(t *testing.T, n int, vs, hs []string) *game.Game {
	t.Helper()
	rs := game.Std
	rs.Size = n
	g := game.MustNew(rs)
	for i := 0; i < len(vs) || i < len(hs); i++ {
		for _, side := range []struct {
			names []string
			who   game.Player
		}{{vs, game.Vertical}, {hs, game.Horizontal}} {
			if i >= len(side.names) {
				continue
			}
			p, err := game.ParsePoint(side.names[i])
			if err != nil {
				t.Fatalf("%s: %v", side.names[i], err)
			}
			if g.Turn() != side.who {
				t.Fatalf("%s to move where %s was expected before %s", g.Turn(), side.who, side.names[i])
			}
			if _, err := g.PlayPeg(p); err != nil {
				t.Fatalf("%s: %v", side.names[i], err)
			}
		}
	}
	return g
}

// tmplSingleSteps returns, for side s, the holes that are the only candidate at
// their step of a cheapest chain — the holes summarise counts as bottlenecks.
// It is derived here from the sweeps rather than read out of the bottleneck
// count, so that a test can name the hole the count is about.
func tmplSingleSteps(a *analysis, s int) []game.Point {
	if a.need[s] <= 0 {
		return nil
	}
	best := int32(a.need[s])
	var out []game.Point
	for step := int32(1); step <= best; step++ {
		count, only := 0, 0
		for i := range a.span[s] {
			if a.span[s][i] == best && a.pegs[i] == game.NoPlayer && a.use[s][i] && a.dist[s][0][i] == step {
				count, only = count+1, i
			}
		}
		if count == 1 {
			out = append(out, game.Point{Col: only % a.n, Row: only / a.n})
		}
	}
	return out
}

// TestTemplateExemptsOnlyAProvedStep pins the integration to one position it
// can be read off by hand.
//
// Horizontal's peg at H2 stands two columns from its right border. Its two
// landing holes there are J1, which is a corner and so is not a hole at all,
// and J3. One landing hole left means the evaluation sees a step of its
// cheapest chain with a single candidate and calls it a bottleneck — and it is
// wrong, because J3 lies on Horizontal's own border column, where Vertical may
// never play, and one peg makes no link. With the lever on, that step is not
// counted; the peg count is untouched either way.
//
// The controls put a single Vertical peg into the carrier. One lands in the
// guard, where a Vertical peg could be one end of a link across the connection,
// and the template stops matching; the other lands far away and it goes on
// matching. Both are single pegs, so it is where the peg is and not that a peg
// arrived that decides.
func TestTemplateExemptsOnlyAProvedStep(t *testing.T) {
	const n = 10
	vs := []string{"I5", "H10", "I10"}
	hs := []string{"H2", "A3", "F6"}
	mine := sideIndex(game.Horizontal)

	var off, on analysis
	on.templates = true
	g := tmplBuild(t, n, vs, hs)
	off.load(g)
	on.load(g)

	if got := tmplSingleSteps(&off, mine); len(got) != 1 || got[0].String() != "J3" {
		t.Fatalf("the fixture no longer has J3 as its one forced step: %v", got)
	}
	if off.bottlenecks[mine] != 1 || on.bottlenecks[mine] != 0 {
		t.Fatalf("bottlenecks for Horizontal: lever off %d (want 1), lever on %d (want 0)",
			off.bottlenecks[mine], on.bottlenecks[mine])
	}
	if off.need != on.need {
		t.Fatalf("the lever moved the peg counts: %v -> %v; it may only change what is called a bottleneck", off.need, on.need)
	}

	// The exemption has to be backed by a template anchored where the position
	// says, and that template has to hold on this very board.
	hole := game.Point{Col: 9, Row: 2}
	tmpl, anchor, ok := on.templateMatch(mine, hole.Row*n+hole.Col, int32(on.need[mine]))
	if !ok {
		t.Fatalf("J3 was exempted by no template at all")
	}
	if got := (game.Point{Col: anchor % n, Row: anchor / n}); got.String() != "H2" {
		t.Fatalf("J3 was exempted by a template anchored at %v, want H2", got)
	}
	got := tmplProveMatch(t, g, *tmpl, game.Point{Col: anchor % n, Row: anchor / n})
	if !got.proved {
		t.Fatalf("the position the evaluation matched on does not hold up: %s", got.failure())
	}
	t.Logf("fixture match %s anchored at H2 re-proved on the board itself in %d nodes", tmpl.name, got.nodes)

	for _, ctrl := range []struct {
		at    string
		frees bool
	}{
		{at: "I3", frees: false}, // in the guard of the matching template
		{at: "B2", frees: true},  // nowhere near it
	} {
		c := tmplBuild(t, n, append(slices.Clone(vs), ctrl.at), hs)
		var coff, con analysis
		con.templates = true
		coff.load(c)
		con.load(c)
		if coff.bottlenecks[mine] != 1 {
			t.Fatalf("control %s: without the lever Horizontal has %d bottlenecks, want the fixture's 1",
				ctrl.at, coff.bottlenecks[mine])
		}
		want := 1
		if ctrl.frees {
			want = 0
		}
		if con.bottlenecks[mine] != want {
			t.Fatalf("control %s: with the lever Horizontal has %d bottlenecks, want %d",
				ctrl.at, con.bottlenecks[mine], want)
		}
	}
}

// tmplProveMatch runs the prover on a position the evaluation has matched a
// template on, with the opponent to move.
func tmplProveMatch(t *testing.T, g *game.Game, tmpl placedTemplate, anchor game.Point) tmplProof {
	t.Helper()
	n := g.Size()
	side := game.Player(tmpl.side + 1)
	p := &tmplProver{tmpl: tmpl, anchor: anchor, side: side, opp: side.Opponent(), n: n}
	for _, c := range tmpl.cells {
		p.cells = append(p.cells, game.Point{Col: anchor.Col + c.dCol, Row: anchor.Row + c.dRow})
	}
	probe := g.Clone()
	if probe.Turn() != p.opp {
		t.Fatalf("tmplProveMatch wants the opponent to move, but it is %s's turn", probe.Turn())
	}
	return p.run(probe)
}

// TestTemplateMatchIsProvedOnTheBoardItMatchedOn is the test that ties the two
// halves together, and it is the one that catches a wrong precondition.
//
// The proofs run on an empty board. The evaluation matches templates on boards
// with pegs all over them, under conditions written in templates.go — the
// carrier empty, the anchor at the template's own distance from its border and
// on a cheapest chain. Whether those conditions are enough is exactly the
// question, and the answer is not a matter of reading the code: it is settled
// by taking a position the evaluation actually matched on and putting it
// through the same exhaustive prover, opponent to move, opponent free to play
// anywhere on that cluttered board.
//
// An earlier draft of the match conditions left out the distance from the
// border, and matched a template on a peg four columns out whose cells then
// landed nowhere near the border line. This test refutes that draft.
func TestTemplateMatchIsProvedOnTheBoardItMatchedOn(t *testing.T) {
	const n = 10
	const want = 12
	rs := game.Std
	rs.Size = n
	src := rand.New(rand.NewPCG(5, 90210))
	var an analysis
	an.templates = true
	checked := 0
	for range 30000 {
		if checked >= want {
			break
		}
		g := randomGame(t, rs, 4+src.IntN(30), src)
		if g.Result().Over() {
			continue
		}
		an.load(g)
		for s := range 2 {
			me := game.Player(s + 1)
			if g.Turn() == me {
				// The claim is stated with the opponent to move; a position
				// where the side is to move is a tempo better for it, but the
				// prover needs the position as the claim describes it.
				continue
			}
			best := int32(an.need[s])
			for _, hole := range tmplSingleSteps(&an, s) {
				tmpl, anchor, ok := an.templateMatch(s, hole.Row*n+hole.Col, best)
				if !ok {
					continue
				}
				at := game.Point{Col: anchor % n, Row: anchor / n}
				got := tmplProveMatch(t, g, *tmpl, at)
				if !got.proved {
					t.Fatalf("the evaluation exempted %v on the strength of %s anchored at %v, but the position does not hold up: %s\n%s",
						hole, tmpl.name, at, got.failure(), g)
				}
				checked++
			}
		}
	}
	if checked < want {
		t.Fatalf("only %d matched positions were re-proved, want %d: the test is not exercising the integration", checked, want)
	}
	t.Logf("re-proved %d template matches on the boards the evaluation matched them on", checked)
}

// tmplClearAnchors reports which base templates have at least one anchor in
// this position: a peg of theirs on a cheapest chain, at the right distance
// from a border, with the whole carrier empty. It is everything the evaluation
// checks except the one condition it cannot control — that the hole in question
// is the only candidate at its step — so the gap between this and what actually
// fires says which half of the corpus is idle and why.
func tmplClearAnchors(a *analysis) map[string]bool {
	out := map[string]bool{}
	n := a.n
	for s := range 2 {
		if a.need[s] <= 0 {
			continue
		}
		best := int32(a.need[s])
		for i := range a.pegs {
			if a.pegs[i] != game.Player(s+1) || a.span[s][i] != best {
				continue
			}
			ac, ar := i%n, i/n
			for b := range 2 {
				for j := range templateCorpus[s][b] {
					tmpl := &templateCorpus[s][b][j]
					if borderDistance(s, b, n, ac, ar) != tmpl.depth || int(a.dist[s][b][i]) != tmpl.need {
						continue
					}
					if a.templateClear(s, tmpl, ac, ar) {
						out[tmpl.base] = true
					}
				}
			}
		}
	}
	return out
}

// TestTemplateExemptionIsNotInert measures what the corpus is worth to the
// evaluation, and fails if the answer is nothing.
//
// A template that never matches costs work and changes no score, and the
// difference between "rarely" and "never" is not visible in any other test
// here: every other one arranges a position it knows the corpus covers. This
// one takes an unarranged sample and counts. It caught the corpus going inert
// once already — the condition that pins an anchor to its template's distance
// from the border is right, and a first draft without it was matching four
// times as often on positions no proof covered.
//
// The invariants are checked on every position of the sample, not only the ones
// that fire: the lever may lower a bottleneck count and may never raise one,
// and it may never move a peg count at all.
func TestTemplateExemptionIsNotInert(t *testing.T) {
	var off, on analysis
	on.templates = true
	positions, firing, removed := 0, 0, 0
	byBase, anchored := map[string]int{}, map[string]int{}
	for _, n := range []int{10, 16, 24} {
		for _, rs := range tmplRulesets(n) {
			src := rand.New(rand.NewPCG(7, uint64(n)))
			for range 2000 {
				g := randomGame(t, rs, 4+src.IntN(4*n), src)
				off.load(g)
				on.load(g)
				positions++
				for base := range tmplClearAnchors(&on) {
					anchored[base]++
				}
				if off.need != on.need {
					t.Fatalf("the lever moved the peg counts %v -> %v on\n%s", off.need, on.need, g)
				}
				for s := range 2 {
					if on.bottlenecks[s] > off.bottlenecks[s] {
						t.Fatalf("the lever raised side %d's bottleneck count %d -> %d on\n%s",
							s, off.bottlenecks[s], on.bottlenecks[s], g)
					}
					removed += off.bottlenecks[s] - on.bottlenecks[s]
				}
				if off.bottlenecks == on.bottlenecks {
					continue
				}
				firing++
				for s := range 2 {
					for _, hole := range tmplSingleSteps(&on, s) {
						if tmpl, _, ok := on.templateMatch(s, hole.Row*n+hole.Col, int32(on.need[s])); ok {
							byBase[tmpl.base]++
						}
					}
				}
			}
		}
	}
	if firing == 0 || removed == 0 {
		t.Fatalf("the corpus changed nothing over %d positions: it is costing work and buying none", positions)
	}
	t.Logf("%d of %d sampled positions changed, %d bottlenecks dropped, by template: %v",
		firing, positions, removed, byBase)
	t.Logf("positions holding a matchable anchor with a clear carrier, by template: %v", anchored)
}
