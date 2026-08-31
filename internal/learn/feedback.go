package learn

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/BAKocska/twixtui/internal/game"
)

// Shared diagnosis. Every wrong answer in the tutorial is explained by working
// out what the learner's move would actually have done, so the feedback cannot
// drift away from the rules the engine enforces.

// pt parses a hole name written in this package's own content. A failure means
// a typo in a lesson, so it panics during initialisation rather than shipping a
// tutorial that half loads.
func pt(name string) game.Point {
	p, err := game.ParsePoint(name)
	if err != nil {
		panic("learn: bad hole name " + name + ": " + err.Error())
	}
	return p
}

func pts(names ...string) []game.Point {
	out := make([]game.Point, 0, len(names))
	for _, n := range names {
		out = append(out, pt(n))
	}
	return out
}

// ownBorders returns every hole in a side's two border lines, corners aside.
func ownBorders(pl game.Player) []game.Point {
	n := tutorialSize
	out := make([]game.Point, 0, 2*(n-2))
	for i := 1; i < n-1; i++ {
		switch pl {
		case game.Vertical:
			out = append(out, game.Point{Col: i, Row: 0}, game.Point{Col: i, Row: n - 1})
		case game.Horizontal:
			out = append(out, game.Point{Col: 0, Row: i}, game.Point{Col: n - 1, Row: i})
		}
	}
	return out
}

// sideName is a side's name as the tutorial's prose spells it.
func sideName(pl game.Player) string {
	switch pl {
	case game.Vertical:
		return "Vertical"
	case game.Horizontal:
		return "Horizontal"
	}
	return "nobody"
}

// borderName describes a side's own pair of border lines.
func borderName(pl game.Player) string {
	switch pl {
	case game.Vertical:
		return "top and bottom rows"
	case game.Horizontal:
		return "left and right columns"
	}
	return "border lines"
}

// placementProblem explains why the side to move may not put a peg in p, or
// returns the empty string when it may.
func placementProblem(g *game.Game, p game.Point) string {
	err := g.CanPlace(g.Turn(), p)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, game.ErrOffBoard):
		return fmt.Sprintf("%s is off the board.", p)
	case errors.Is(err, game.ErrCornerHole):
		return fmt.Sprintf("%s is a corner, and corner holes do not exist: a corner lies in a border line of each player at once, so the board leaves it out.", p)
	case errors.Is(err, game.ErrOpponentBorder):
		return fmt.Sprintf("%s is in %s's border line, and you may never place a peg in your opponent's border line. Your own %s are open to you, and you have to reach both of them to win.",
			p, sideName(g.Turn().Opponent()), borderName(g.Turn()))
	case errors.Is(err, game.ErrOccupied):
		return fmt.Sprintf("%s already holds a %s peg.", p, sideName(g.At(p)))
	}
	return fmt.Sprintf("%s is not a legal placement: %v.", p, err)
}

// offsetProblem explains why two holes cannot be linked, and assumes they are
// not a knight's move apart.
func offsetProblem(anchor, p game.Point) string {
	dc, dr := abs(p.Col-anchor.Col), abs(p.Row-anchor.Row)
	switch {
	case dc == 0 && dr == 0:
		return fmt.Sprintf("%s is the hole you are measuring from.", p)
	case dc == 1 && dr == 1:
		return fmt.Sprintf("%s is a diagonal neighbour of %s, and diagonal neighbours do not link. A link is a knight's move: two holes one way and one hole the other.", p, anchor)
	case dc+dr == 1:
		return fmt.Sprintf("%s is right beside %s, and holes side by side do not link. A link is a knight's move: two holes one way and one hole the other.", p, anchor)
	case (dc == 0 && dr == 2) || (dc == 2 && dr == 0):
		return fmt.Sprintf("%s is two holes from %s in a straight line, which is not a knight's move: it has to be two one way and one the other, not two straight on.", p, anchor)
	case dc == 2 && dr == 2:
		return fmt.Sprintf("%s is two columns and two rows from %s, which is a long diagonal rather than a knight's move. No hole links that pair either, which is why it is a shape to avoid.", p, anchor)
	}
	desc := fmt.Sprintf("%s and %s", plural(dc, "column"), plural(dr, "row"))
	switch {
	case dc == 0:
		desc = fmt.Sprintf("in the same column, %s away", plural(dr, "row"))
	case dr == 0:
		desc = fmt.Sprintf("in the same row, %s away", plural(dc, "column"))
	}
	return fmt.Sprintf("The step from %s to %s is %s. A link needs one column and two rows, or two columns and one row, so those two pegs cannot be joined directly.",
		anchor, p, desc)
}

// linkProblem explains why a peg placed at p would not end up linked to anchor,
// or returns the empty string when the link would be made. The side to move
// owns both pegs.
func linkProblem(g *game.Game, anchor, p game.Point) string {
	l, ok := game.NewLink(anchor, p)
	if !ok {
		return offsetProblem(anchor, p)
	}
	blocker, blocked := g.LinkBlockedBy(l, g.Turn())
	if !blocked {
		return ""
	}
	if g.LinkOwner(blocker) == g.Turn() {
		return fmt.Sprintf("A peg on %s is a legal move, but no link would appear: your own link %s crosses the line from %s to %s, and no link may cross another, not even one of your own. That is the rule newcomers get wrong most often.",
			p, blocker, anchor, p)
	}
	return fmt.Sprintf("A peg on %s is a legal move, but no link would appear: %s's link %s crosses the line from %s to %s, and no link may cross another. A peg with nothing linked to it has bought you nothing.",
		p, sideName(g.LinkOwner(blocker)), blocker, anchor, p)
}

// after plays p for the side to move on a copy of g, so a checker can inspect
// the position the learner's move would produce without touching the original.
func after(g *game.Game, p game.Point) (*game.Game, game.Result, error) {
	c := g.Clone()
	res, err := c.PlayPeg(p)
	return c, res, err
}

// linked reports whether a and b are joined by a link on the board.
func linked(g *game.Game, a, b game.Point) bool {
	l, ok := game.NewLink(a, b)
	return ok && g.HasLink(l)
}

// joiningHoles lists the empty holes that would link both a and b for owner,
// taking the links already on the board into account.
func joiningHoles(g *game.Game, owner game.Player, a, b game.Point) []game.Point {
	var out []game.Point
	for d := range game.Dir(game.NumDirs) {
		x := a.Add(d)
		if !g.Exists(x) || g.CanPlace(owner, x) != nil {
			continue
		}
		first, ok := game.NewLink(a, x)
		if !ok {
			continue
		}
		second, ok := game.NewLink(x, b)
		if !ok {
			continue
		}
		if _, blocked := g.LinkBlockedBy(first, owner); blocked {
			continue
		}
		if _, blocked := g.LinkBlockedBy(second, owner); blocked {
			continue
		}
		out = append(out, x)
	}
	return readingOrder(out)
}

// readingOrder sorts holes the way a player reads the board, top row first and
// left to right within a row, so prose that lists them is stable and does not
// jump about.
func readingOrder(ps []game.Point) []game.Point {
	slices.SortFunc(ps, func(a, b game.Point) int {
		if a.Row != b.Row {
			return a.Row - b.Row
		}
		return a.Col - b.Col
	})
	return ps
}

// borderReach lists the holes in pl's border line that p could still link to:
// empty, and with the link unobstructed.
func borderReach(g *game.Game, pl game.Player, p game.Point) []game.Point {
	var out []game.Point
	for d := range game.Dir(game.NumDirs) {
		q := p.Add(d)
		if !g.Exists(q) || !g.IsBorderRow(pl, q) || g.At(q) != game.NoPlayer {
			continue
		}
		l, ok := game.NewLink(p, q)
		if !ok {
			continue
		}
		if _, blocked := g.LinkBlockedBy(l, pl); !blocked {
			out = append(out, q)
		}
	}
	return readingOrder(out)
}

// immediateWin returns a move that wins on the spot for the side to move.
func immediateWin(g *game.Game) (game.Point, bool) {
	pl := g.Turn()
	var found game.Point
	ok := false
	g.EachLegalPlacement(pl, func(p game.Point) bool {
		c := g.Clone()
		res, err := c.PlayPeg(p)
		if err == nil && res.Winner() == pl {
			found, ok = p, true
			return false
		}
		return true
	})
	return found, ok
}

// names renders a list of holes as prose: "A5", "A5 and A7", "A5, A7 and B4".
func names(ps []game.Point) string {
	switch len(ps) {
	case 0:
		return "nowhere"
	case 1:
		return ps[0].String()
	}
	out := ""
	for i, p := range ps[:len(ps)-1] {
		if i > 0 {
			out += ", "
		}
		out += p.String()
	}
	return out + " and " + ps[len(ps)-1].String()
}

// adjacent reports whether two holes are touching, diagonally included.
func adjacent(a, b game.Point) bool {
	return abs(a.Col-b.Col) <= 1 && abs(a.Row-b.Row) <= 1
}

// plural renders a count and its noun. Small numbers are spelled out because
// every one of these counts ends up inside a sentence.
func plural(n int, noun string) string {
	words := [...]string{"no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	word := strconv.Itoa(n)
	if n >= 0 && n < len(words) {
		word = words[n]
	}
	if n == 1 {
		return word + " " + noun
	}
	return word + " " + noun + "s"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
