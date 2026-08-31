package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/BAKocska/twixtui/internal/game"
)

// The explanation behind a hint is read out of the search, never composed
// alongside it. For the move the search chose, the evaluation is decomposed
// twice — once for the position as it stands and once for the position the move
// produces — and the difference between those two decompositions is the only
// input the prose has. Each template states a numeric claim about that
// difference; verifyReason re-checks the claim before the prose is handed out,
// so a template can never assert a cause the search did not measure.

// reason names why a move scored best.
type reason int

const (
	reasonWin reason = iota
	reasonDeadlock
	reasonSeal
	reasonSealedOut
	reasonOnlyDefence
	reasonDefence
	reasonBlock
	reasonAdvance
	reasonSetup
	reasonGround
	reasonBalanced
)

var reasonNames = [...]string{
	"win", "deadlock", "seal", "sealed-out", "only-defence", "defence",
	"block", "advance", "setup", "ground", "balanced",
}

func (r reason) String() string {
	if r < 0 || int(r) >= len(reasonNames) {
		return fmt.Sprintf("reason(%d)", int(r))
	}
	return reasonNames[r]
}

// deltas is the evaluation decomposition of one candidate move together with
// the threat context the search found around it.
type deltas struct {
	// Before and After are the terms for the side asking, on either side of
	// the move.
	Before, After Terms
	// Threatened records that the opponent was one peg from a finished chain
	// before the move.
	Threatened bool
	// Defences counts how many legal moves answered that threat.
	Defences int
	// Close records that the second-best move scored within a peg of the best,
	// which softens the wording: the move is strong, not the only way.
	Close bool
	// Partner and Carriers describe a setup the move creates: an own peg the
	// move now has two independent ways of linking to, and the holes those
	// links would run through.
	Partner  game.Point
	Carriers []game.Point
	// Gap names the setup's shape by its column-row span, e.g. "3-3".
	Gap string
	// HasSetup records whether Partner, Carriers and Gap are filled in.
	HasSetup bool
}

// advance is how many pegs the move takes off the asking side's own remaining
// chain.
func (d deltas) advance() int { return d.Before.Dist - d.After.Dist }

// block is how many pegs the move adds to the opponent's remaining chain.
func (d deltas) block() int { return d.After.OppDist - d.Before.OppDist }

// freed is how many fewer holes the asking side's plan now depends on: a move
// that removes a bottleneck gives the plan an alternative it did not have.
func (d deltas) freed() int { return d.Before.Bottlenecks - d.After.Bottlenecks }

// measured reports whether both sides still have a route after the move, which
// is what the arithmetic reasons need in order to mean anything.
func (d deltas) measured() bool {
	return d.After.Dist != NoChain && d.After.OppDist != NoChain &&
		d.Before.Dist != NoChain && d.Before.OppDist != NoChain
}

// chooseReason picks the reason by strict priority over the measured deltas.
func chooseReason(d deltas) reason {
	switch {
	case d.After.Dist == 0:
		return reasonWin
	case d.After.Dist == NoChain && d.After.OppDist == NoChain:
		return reasonDeadlock
	case d.After.OppDist == NoChain:
		return reasonSeal
	case d.After.Dist == NoChain:
		return reasonSealedOut
	case d.Threatened && d.After.OppDist >= 2 && d.Defences == 1:
		return reasonOnlyDefence
	case d.Threatened && d.After.OppDist >= 2:
		return reasonDefence
	case d.measured() && d.block() > 0 && d.block() > d.advance():
		return reasonBlock
	case d.measured() && d.advance() > 0 && d.advance() >= d.block():
		return reasonAdvance
	case d.measured() && d.advance() == 0 && d.block() <= 0 && d.freed() > 0:
		return reasonSetup
	case d.measured() && d.advance() == 0 && d.block() <= 0 && d.freed() <= 0 &&
		d.After.Ground > d.Before.Ground:
		return reasonGround
	default:
		return reasonBalanced
	}
}

// verifyReason reports whether the deltas support the claim the reason's prose
// makes. It is written separately from chooseReason on purpose: if a later
// change to the priority order stops matching what the templates say, this
// disagrees and the hint falls back to the wording that is true of every
// position.
func verifyReason(r reason, d deltas) error {
	switch r {
	case reasonWin:
		if d.After.Dist != 0 {
			return fmt.Errorf("claimed a completed chain but the distance afterwards is %s", pegsPhrase(d.After.Dist))
		}
	case reasonDeadlock:
		if d.After.Dist != NoChain || d.After.OppDist != NoChain {
			return fmt.Errorf("claimed neither side can connect but the distances are %s and %s",
				pegsPhrase(d.After.Dist), pegsPhrase(d.After.OppDist))
		}
	case reasonSeal:
		if d.After.OppDist != NoChain {
			return fmt.Errorf("claimed the opponent is shut out but they still need %s", pegsPhrase(d.After.OppDist))
		}
		if d.After.Dist == NoChain {
			return errors.New("claimed the opponent is shut out without saying that neither side can connect")
		}
	case reasonSealedOut:
		if d.After.Dist != NoChain {
			return fmt.Errorf("claimed no route is left but the distance afterwards is %s", pegsPhrase(d.After.Dist))
		}
		if d.After.OppDist == NoChain {
			return errors.New("claimed only this side is shut out when both are")
		}
	case reasonOnlyDefence:
		if !d.Threatened {
			return errors.New("claimed a forced defence with no threat on the board")
		}
		if d.After.OppDist == NoChain {
			break
		}
		if d.After.OppDist < 2 {
			return errors.New("claimed a defence that leaves the winning hole open")
		}
		if d.Defences != 1 {
			return fmt.Errorf("claimed the only defence but %d moves answer the threat", d.Defences)
		}
	case reasonDefence:
		if !d.Threatened {
			return errors.New("claimed a defence with no threat on the board")
		}
		if d.After.OppDist != NoChain && d.After.OppDist < 2 {
			return errors.New("claimed a defence that leaves the winning hole open")
		}
	case reasonBlock:
		if !d.measured() {
			return errors.New("claimed a measured block where one side has no route to measure")
		}
		if d.block() <= 0 {
			return fmt.Errorf("claimed a block that changes the opponent's distance by %d", d.block())
		}
		if d.block() <= d.advance() {
			return fmt.Errorf("claimed blocking dominates but advance is %d against block %d",
				d.advance(), d.block())
		}
	case reasonAdvance:
		if !d.measured() {
			return errors.New("claimed measured progress where one side has no route to measure")
		}
		if d.advance() <= 0 {
			return fmt.Errorf("claimed progress that changes own distance by %d", d.advance())
		}
		if d.advance() < d.block() {
			return fmt.Errorf("claimed advancing dominates but block is %d against advance %d",
				d.block(), d.advance())
		}
	case reasonSetup:
		if !d.measured() {
			return errors.New("claimed a freed plan where one side has no route to measure")
		}
		if d.advance() != 0 {
			return fmt.Errorf("claimed a spare route but the move changes own distance by %d", d.advance())
		}
		if d.block() > 0 {
			return fmt.Errorf("claimed a spare route but the move blocks by %d", d.block())
		}
		if d.freed() <= 0 {
			return fmt.Errorf("claimed a spare route but the plan's forced holes change by %d", d.freed())
		}
	case reasonGround:
		if !d.measured() {
			return errors.New("claimed a gain in reach where one side has no route to measure")
		}
		if d.advance() != 0 || d.block() > 0 {
			return fmt.Errorf("claimed reach decides but the move moves the peg counts by %d and %d",
				d.advance(), d.block())
		}
		if d.After.Ground <= d.Before.Ground {
			return fmt.Errorf("claimed a gain in reach but the balance moved from %d to %d",
				d.Before.Ground, d.After.Ground)
		}
	case reasonBalanced:
		return nil
	default:
		return fmt.Errorf("unknown reason %d", int(r))
	}
	return nil
}

// borderNames says which pair of edges a side is joining, so the prose can name
// the goal rather than the colour.
func borderNames(pl game.Player) string {
	if pl == game.Horizontal {
		return "the left and right edges"
	}
	return "the top and bottom edges"
}

// pegsPhrase renders a distance for a reader. Every template goes through it,
// so the "no route at all" value can never be printed as if it were a number of
// pegs.
func pegsPhrase(dist int) string {
	switch dist {
	case NoChain:
		return "no route at all"
	case 0:
		return "a finished chain"
	case 1:
		return "1 peg"
	}
	return fmt.Sprintf("%d pegs", dist)
}

// groundPhrase renders a reach balance, which Terms carries in parts per
// thousand, as a percentage of the board with the side it favours named.
func groundPhrase(ground int, me game.Player) string {
	switch {
	case ground > 0:
		return fmt.Sprintf("%d%% of the board yours", (ground+5)/10)
	case ground < 0:
		return fmt.Sprintf("%d%% of it %s's", (-ground+5)/10, me.Opponent())
	}
	return "level"
}

// describe renders the reason as a headline and a detail, using only numbers
// that verifyReason has agreed to.
func describe(r reason, d deltas, me game.Player, move game.Point) (headline, detail string) {
	opp := me.Opponent()
	lead := "Play " + move.String()
	// The softened lead only fits reasons that are recommending one move among
	// several. A verdict about the position itself is not "one strong option".
	switch r {
	case reasonWin, reasonOnlyDefence, reasonDeadlock, reasonSealedOut:
	default:
		if d.Close {
			lead = "One strong option is " + move.String()
		}
	}

	switch r {
	case reasonWin:
		return lead + " and the game is yours.",
			fmt.Sprintf("It closes the last gap in your chain, joining %s.", borderNames(me))
	case reasonDeadlock:
		return lead + "; the game is already drawn.",
			fmt.Sprintf("Neither side has a route left — the links on the board seal both %s and %s — so no further play can win it.",
				borderNames(me), borderNames(opp))
	case reasonSeal:
		return lead + " to shut " + opp.String() + " out for good.",
			fmt.Sprintf("Afterwards no chain of theirs can reach from edge to edge at all, while yours still needs %s.",
				pegsPhrase(d.After.Dist))
	case reasonSealedOut:
		return lead + " and hope for a mistake.",
			fmt.Sprintf("%s's links already seal every route of yours, so no chain of yours can reach %s; they need %s to finish. This is the most testing reply left.",
				titled(opp), borderNames(me), pegsPhrase(d.After.OppDist))
	case reasonOnlyDefence:
		return "Play " + move.String() + ": it is the only defence.",
			fmt.Sprintf("%s is one peg from joining %s, and this is the single reply that stops it. Afterwards they need %s.",
				titled(opp), borderNames(opp), pegsPhrase(d.After.OppDist))
	case reasonDefence:
		return lead + " to stop " + opp.String() + " finishing.",
			fmt.Sprintf("%s was one peg from joining %s; this pushes them back to %s. One of %d replies does that.",
				titled(opp), borderNames(opp), pegsPhrase(d.After.OppDist), d.Defences)
	case reasonBlock:
		return lead + " to cut " + opp.String() + "'s cheapest route.",
			fmt.Sprintf("It lengthens their remaining chain from %s to %s, while yours still needs %s.",
				pegsPhrase(d.Before.OppDist), pegsPhrase(d.After.OppDist), pegsPhrase(d.After.Dist))
	case reasonAdvance:
		return lead + " to carry your chain on.",
			fmt.Sprintf("Your cheapest route to %s drops from %s to %s; %s still needs %s.",
				borderNames(me), pegsPhrase(d.Before.Dist), pegsPhrase(d.After.Dist),
				titled(opp), pegsPhrase(d.After.OppDist))
	case reasonSetup:
		forced := "no hole your plan depends on"
		if d.After.Bottlenecks == 1 {
			forced = "one hole your plan depends on"
		} else if d.After.Bottlenecks > 1 {
			forced = fmt.Sprintf("%d holes your plan depends on", d.After.Bottlenecks)
		}
		if d.HasSetup {
			return lead + fmt.Sprintf(" for a %s gap with %s.", d.Gap, d.Partner.String()),
				fmt.Sprintf("That is a setup: %s and %s can be joined through %s, so blocking one of them does not break the link. Your chain still needs %s, and there is now %s instead of %d.",
					move.String(), d.Partner.String(), joinPoints(d.Carriers),
					pegsPhrase(d.After.Dist), forced, d.Before.Bottlenecks)
		}
		return lead + " to loosen your chain.",
			fmt.Sprintf("Your chain still needs %s, but there is now %s instead of %d, so a single peg no longer sets the plan back.",
				pegsPhrase(d.After.Dist), forced, d.Before.Bottlenecks)
	case reasonGround:
		return lead + " to take ground.",
			fmt.Sprintf("Neither chain gets shorter, but more of the board comes within your cheaper reach: the balance moves from %s to %s. Your chain still needs %s.",
				groundPhrase(d.Before.Ground, me), groundPhrase(d.After.Ground, me), pegsPhrase(d.After.Dist))
	default:
		return lead + ".",
			fmt.Sprintf("No single factor decides here. It is the best balance the search found between your route of %s and holding %s to %s.",
				pegsPhrase(d.After.Dist), opp.String(), pegsPhrase(d.After.OppDist))
	}
}

func titled(pl game.Player) string {
	s := pl.String()
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

func joinPoints(ps []game.Point) string {
	switch len(ps) {
	case 0:
		return "no hole"
	case 1:
		return ps[0].String()
	}
	out := ""
	for i, p := range ps {
		switch {
		case i == 0:
			out = p.String()
		case i == len(ps)-1:
			out += " or " + p.String()
		default:
			out += ", " + p.String()
		}
	}
	return out
}

// setupOffsets are the two-peg gaps that can be joined in more than one way:
// offsets between two holes that share at least two knight neighbours. They are
// derived from the link geometry rather than transcribed, and come out as the
// shapes TwixT players name after their column-row span — 0-4, 4-0, 1-3, 3-1
// and 3-3.
var setupOffsets = func() [][2]int {
	var out [][2]int
	for dCol := -4; dCol <= 4; dCol++ {
		for dRow := -4; dRow <= 4; dRow++ {
			if dCol == 0 && dRow == 0 {
				continue
			}
			shared := 0
			for _, a := range dirDelta {
				for _, b := range dirDelta {
					if a[0] == dCol+b[0] && a[1] == dRow+b[1] {
						shared++
					}
				}
			}
			if shared >= 2 {
				out = append(out, [2]int{dCol, dRow})
			}
		}
	}
	return out
}()

// findSetup looks for a setup the move creates: an own peg that the new peg can
// reach two independent ways, through holes that are still empty and still
// linkable. It reads the position after the move.
func findSetup(after *analysis, me game.Player, move game.Point) (partner game.Point, carriers []game.Point, gap string, ok bool) {
	n := after.n
	s := sideIndex(me)
	for _, off := range setupOffsets {
		p := game.Point{Col: move.Col + off[0], Row: move.Row + off[1]}
		if p.Col < 0 || p.Col >= n || p.Row < 0 || p.Row >= n {
			continue
		}
		if after.pegs[p.Row*n+p.Col] != me {
			continue
		}
		var live []game.Point
		for d := range game.Dir(game.NumDirs) {
			c := game.Point{Col: move.Col + dirDelta[d][0], Row: move.Row + dirDelta[d][1]}
			if c.Col < 0 || c.Col >= n || c.Row < 0 || c.Row >= n {
				continue
			}
			ci := c.Row*n + c.Col
			if after.pegs[ci] != game.NoPlayer || !after.use[s][ci] {
				continue
			}
			// The carrier must be a knight's move from the partner too, and
			// both links must still be creatable.
			dir2, joins := dirFromTo(c, p)
			if !joins {
				continue
			}
			if !after.linkOpen(s, move.Row*n+move.Col, d, ci) {
				continue
			}
			if !after.linkOpen(s, ci, dir2, p.Row*n+p.Col) {
				continue
			}
			live = append(live, c)
		}
		if len(live) < 2 {
			continue
		}
		sortPoints(live)
		return p, live, fmt.Sprintf("%d-%d", abs(off[0]), abs(off[1])), true
	}
	return game.Point{}, nil, "", false
}

// dirFromTo returns the link direction from a to b when they are a knight's
// move apart.
func dirFromTo(a, b game.Point) (game.Dir, bool) {
	dCol, dRow := b.Col-a.Col, b.Row-a.Row
	for d := range game.Dir(game.NumDirs) {
		if dirDelta[d][0] == dCol && dirDelta[d][1] == dRow {
			return d, true
		}
	}
	return 0, false
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// maxHighlight bounds how many holes a hint marks, so that a wide-open position
// does not light up half the board.
const maxHighlight = 10

// highlightFor collects the holes the explanation refers to.
func highlightFor(r reason, d deltas, before, after *analysis, me game.Player, move game.Point) []game.Point {
	out := []game.Point{move}
	switch r {
	case reasonWin:
		out = append(out, linkedNeighbours(after, me, move)...)
	case reasonOnlyDefence, reasonDefence, reasonBlock:
		// The route the move takes away: the holes the opponent's cheapest
		// chain ran through before it.
		out = append(out, cheapestHoles(before, me.Opponent(), move)...)
	case reasonSetup:
		if d.HasSetup {
			out = append(out, d.Partner)
			out = append(out, d.Carriers...)
			break
		}
		out = append(out, cheapestHoles(after, me, move)...)
	default:
		out = append(out, cheapestHoles(after, me, move)...)
	}
	if len(out) > maxHighlight {
		out = out[:maxHighlight]
	}
	return out
}

// cheapestHoles lists the empty holes lying on at least one of a side's
// cheapest chains, skipping the move itself.
func cheapestHoles(a *analysis, pl game.Player, skip game.Point) []game.Point {
	s := sideIndex(pl)
	want := int32(a.need[s])
	var out []game.Point
	for i := range a.span[s] {
		if a.span[s][i] != want || a.pegs[i] != game.NoPlayer || !a.use[s][i] {
			continue
		}
		p := game.Point{Col: i % a.n, Row: i / a.n}
		if p == skip {
			continue
		}
		out = append(out, p)
		if len(out) >= maxHighlight {
			break
		}
	}
	return out
}

// linkedNeighbours lists the own pegs the move actually linked to.
func linkedNeighbours(a *analysis, pl game.Player, move game.Point) []game.Point {
	n := a.n
	mask := a.link[move.Row*n+move.Col]
	var out []game.Point
	for d := range game.Dir(game.NumDirs) {
		if mask&(1<<d) == 0 {
			continue
		}
		p := game.Point{Col: move.Col + dirDelta[d][0], Row: move.Row + dirDelta[d][1]}
		if p.Col < 0 || p.Col >= n || p.Row < 0 || p.Row >= n {
			continue
		}
		if a.pegs[p.Row*n+p.Col] == pl {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

func (e *engine) Hint(ctx context.Context, g *game.Game) (Hint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g == nil {
		return Hint{}, errors.New("bot: no game")
	}
	if g.Result().Over() {
		return Hint{}, game.ErrGameOver
	}
	if !g.HasLegalPlacement(g.Turn()) {
		return Hint{}, ErrNoMove
	}
	h, _, _, err := e.explain(ctx, g)
	return h, err
}

// explain is Hint with the working parts exposed, so that a test can compare
// the prose against the decomposition it claims to describe.
func (e *engine) explain(ctx context.Context, g *game.Game) (Hint, reason, deltas, error) {
	if e.hint == nil {
		e.hint = newSearcher(hintParams())
	}
	res, err := e.hint.root(ctx, g)
	if err != nil {
		return Hint{}, reasonBalanced, deltas{}, err
	}
	me := g.Turn()

	next := g.Clone()
	if _, err := next.PlayPeg(res.best); err != nil {
		return Hint{}, reasonBalanced, deltas{}, fmt.Errorf("bot: hint move %v is not playable: %w", res.best, err)
	}
	var after analysis
	after.load(next)

	d := deltas{
		Before:     res.terms,
		After:      after.terms(me),
		Threatened: res.threatened,
		Defences:   res.defences,
		Close:      len(res.moves) > 1 && res.moves[0].score-res.moves[1].score < distWeight,
	}
	if partner, carriers, gap, ok := findSetup(&after, me, res.best); ok {
		d.Partner, d.Carriers, d.Gap, d.HasSetup = partner, carriers, gap, true
	}

	r := chooseReason(d)
	if err := verifyReason(r, d); err != nil {
		// The priority order and the templates have drifted apart. Say the one
		// thing that is true of every position rather than a specific claim the
		// numbers do not support.
		r = reasonBalanced
	}
	headline, detail := describe(r, d, me, res.best)
	return Hint{
		Move:      res.best,
		Headline:  headline,
		Detail:    detail,
		Highlight: highlightFor(r, d, res.an, &after, me, res.best),
	}, r, d, nil
}
