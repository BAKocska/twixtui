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
//
// What the prose may claim is bounded by what the search is allowed to do.
// Every hint carries PlacementOnlyPolicy, and the wording here is scoped to it:
// "no route left" means no route the sweep in eval.go can find for a side that
// places pegs and keeps the links they offer, and never that the position is
// proved lost or drawn under the printed rules, which let a turn join or
// withdraw a link as well. A claim that the game is over comes from a
// game.Result read back out of replaying the move, never from the evaluation.
//
// How many pegs the asking side already has is read off the board for the same
// reason. The decomposition holds distances, forced holes and reach, and has no
// notion of a side that has not started: it cannot tell a first peg apart from
// a chain being carried on, so a template that says "carry your chain on" needs
// the board's own count behind it and not a difference of distances. The swap
// option, which the second player's first turn may also have, is not searched
// at all; PlacementOnlyPolicy states that with every hint.

// reason names why a move scored best.
type reason int

const (
	reasonWin reason = iota
	reasonDeadlock
	reasonSeal
	reasonSealedOut
	reasonOnlyDefence
	reasonDefence
	reasonOpening
	reasonBlock
	reasonAdvance
	reasonSetup
	reasonGround
	reasonBalanced
)

var reasonNames = [...]string{
	"win", "deadlock", "seal", "sealed-out", "only-defence", "defence",
	"opening", "block", "advance", "setup", "ground", "balanced",
}

func (r reason) String() string {
	if r < 0 || int(r) >= len(reasonNames) {
		return fmt.Sprintf("reason(%d)", int(r))
	}
	return reasonNames[r]
}

// deltas is the evaluation decomposition of one candidate move together with
// the context the prose needs around it: the threat the search found, and the
// two readings taken off the game itself — what the move did to the result,
// and whether the asking side has a peg down at all.
type deltas struct {
	// Before and After are the terms for the side asking, on either side of
	// the move.
	Before, After Terms
	// Threatened records that the opponent was one peg from a finished chain
	// before the move.
	Threatened bool
	// Defences counts the peg placements that answer the threat, or -1 when
	// enumeration was interrupted. Link edits are not in it: the search does
	// not try them, so this is never a count of every legal answer.
	Defences int
	// Close records that the second-best move scored within a peg of the best,
	// which softens the wording: the move is strong, not the only way.
	Close bool
	// Won records that replaying the move ended the game in the asking side's
	// favour. It is read from the game the move produces rather than from the
	// evaluation: a Dist of zero is the placement sweep's own reading of the
	// board, and a reading cannot license the claim that the game is over.
	Won bool
	// OwnPegs counts the asking side's pegs on the board before the move. It
	// is read from the game rather than from the evaluation, which measures
	// walks and knows nothing about whether the side has started, and it is
	// what separates a first peg from a chain being carried on.
	OwnPegs int
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
	case d.After.Dist == 0 && d.Won:
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
	// A side with no peg on the board has no chain, so the arithmetic reasons
	// below cannot speak for it: each of them compares the route left with the
	// one the side was already following. This case sits under everything that
	// outranks a first peg — a result read back off the game, a reading that
	// finds no route for a side, and an opponent one peg from a finished chain
	// — and it claims nothing beyond the two counts its prose prints.
	case d.OwnPegs == 0 && !d.Won && !d.Threatened &&
		d.After.Dist > 0 && d.After.OppDist > 0:
		return reasonOpening
	case d.measured() && d.block() > 0 && d.block() > d.advance():
		return reasonBlock
	// The own-peg count belongs to the advance claim itself and not only to the
	// case above: "carry your chain on" is false of a first peg however far the
	// distance fell, and the opening case is passed over whenever a threat or a
	// result outranks it.
	case d.measured() && d.OwnPegs > 0 && d.advance() > 0 && d.advance() >= d.block():
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
		if !d.Won {
			return errors.New("claimed the game is won without a result from replaying the move")
		}
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
	case reasonOpening:
		if d.OwnPegs != 0 {
			return fmt.Errorf("claimed a first peg with %d of the asking side's pegs already on the board", d.OwnPegs)
		}
		if d.Won {
			return errors.New("claimed a route is being started by a move that ended the game")
		}
		if d.Threatened {
			return errors.New("claimed a route is being started while the opponent is one peg from a finished chain")
		}
		if d.After.Dist <= 0 {
			return fmt.Errorf("claimed a route to start but the distance afterwards is %s", pegsPhrase(d.After.Dist))
		}
		if d.After.OppDist <= 0 {
			return fmt.Errorf("claimed a route to start while the opponent's distance is %s", pegsPhrase(d.After.OppDist))
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
		if d.OwnPegs <= 0 {
			return errors.New("claimed a chain carried on with no peg of the asking side on the board")
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

// defencePhrase renders how many placements answered a threat.
//
// An interrupted enumeration renders as nothing at all: what came back is a
// prefix of the list and not a count, and "3 answer it" out of a list that was
// cut short would be a number the search never established. An empty list
// renders as nothing for the same reason from the other end — a threat with no
// answer at all, alongside a move recommended for answering it, is a count that
// describes some other position. What it does print is a count of peg
// placements, because peg placements are all the search enumerates: a turn may
// also add or withdraw a link, and that is not in it.
func defencePhrase(defences int) string {
	switch {
	case defences <= 0:
		return ""
	case defences == 1:
		return " One peg placement answers the threat."
	}
	return fmt.Sprintf(" Of the peg placements it counted, %d answer the threat.", defences)
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
		return lead + "; the placement-only evaluation finds neither route.",
			fmt.Sprintf("It finds no route joining %s or %s. That reading does not establish a draw; where the rules permit link edits, those unsearched turns may change the routes.",
				borderNames(me), borderNames(opp))
	case reasonSeal:
		return lead + " to leave " + opp.String() + " with no route in this evaluation.",
			fmt.Sprintf("The placement-only evaluation finds no route for them, while yours costs %s. Where the rules permit link edits, an unsearched turn may change that.",
				pegsPhrase(d.After.Dist))
	case reasonSealedOut:
		return lead + "; the placement-only evaluation finds no route for you.",
			fmt.Sprintf("It finds no route of yours joining %s; %s's route costs %s. Where the rules permit link edits, an unsearched turn may change that.",
				borderNames(me), opp.String(), pegsPhrase(d.After.OppDist))
	case reasonOnlyDefence:
		return "Play " + move.String() + ": it is the only placement-only defence.",
			fmt.Sprintf("%s is one peg from joining %s; this is the single defence among placements that keep their offered links, leaving their route at %s. Link edits, where permitted, and swaps were not searched.",
				titled(opp), borderNames(opp), pegsPhrase(d.After.OppDist))
	case reasonDefence:
		return lead + " to stop " + opp.String() + " finishing.",
			fmt.Sprintf("%s was one peg from joining %s; this pushes them back to %s.%s",
				titled(opp), borderNames(opp), pegsPhrase(d.After.OppDist), defencePhrase(d.Defences))
	case reasonOpening:
		// Two counts and the fact of having no peg down. Which hole among the
		// first pegs is the search's own ordering, and nothing here measures
		// the centre, a shape or a plan, so nothing here says so.
		return lead + " to start a route.",
			fmt.Sprintf("You have no peg on the board yet, so there is no chain to carry on: this is a first peg. From here your cheapest route joining %s costs %s, while %s needs %s.",
				borderNames(me), pegsPhrase(d.After.Dist), titled(opp), pegsPhrase(d.After.OppDist))
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

// Hint is the analysis path answering the question a player asks: one move,
// with the explanation the search's own numbers support. It refuses a finished
// game rather than describing one, which is what separates it from Analyze.
func (e *engine) Hint(ctx context.Context, g *game.Game) (Hint, error) {
	if g != nil && g.Result().Over() {
		return Hint{}, game.ErrGameOver
	}
	res, _, _, err := analyzeOn(ctx, e.hintSearcher(), g, nil)
	if err != nil {
		return Hint{}, err
	}
	return res.hint(), nil
}

// hintSearcher is the searcher hints run on, kept between requests like the
// playing searcher: a hint is asked for repeatedly in one game. Its levers are
// the same whatever tier is playing, so it is built on demand and not per
// tier.
func (e *engine) hintSearcher() *searcher {
	if e.hint == nil {
		e.hint = newSearcher(hintParams())
	}
	return e.hint
}
