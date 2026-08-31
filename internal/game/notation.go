package game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Holes are named the way TwixT players name them: a column letter followed by a
// row number counted from 1 at the top, so A1 is the top-left corner hole and
// the swap reflection runs along the A1 diagonal. Columns past Z continue AA,
// AB and so on, which only matters on boards wider than 26.

// ColumnName returns the letter name of a zero-based column index.
func ColumnName(col int) string {
	if col < 0 {
		return "?"
	}
	name := ""
	for {
		name = string(rune('A'+col%26)) + name
		col = col/26 - 1
		if col < 0 {
			return name
		}
	}
}

// ParseColumn returns the zero-based index of a column letter name.
func ParseColumn(s string) (int, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0, errors.New("empty column name")
	}
	col := 0
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return 0, fmt.Errorf("invalid column name %q", s)
		}
		col = col*26 + int(r-'A') + 1
	}
	return col - 1, nil
}

// String renders a hole in player notation.
func (p Point) String() string {
	return ColumnName(p.Col) + strconv.Itoa(p.Row+1)
}

// ParsePoint reads a hole name such as "B4" or "aa12".
func ParsePoint(s string) (Point, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Point{}, errors.New("empty hole name")
	}
	split := 0
	for split < len(s) && !isDigit(s[split]) {
		split++
	}
	if split == 0 || split == len(s) {
		return Point{}, fmt.Errorf("malformed hole name %q: expected a column letter followed by a row number", s)
	}
	col, err := ParseColumn(s[:split])
	if err != nil {
		return Point{}, err
	}
	row, err := strconv.Atoi(s[split:])
	if err != nil {
		return Point{}, fmt.Errorf("malformed row number in %q", s)
	}
	if row < 1 {
		return Point{}, fmt.Errorf("row numbers start at 1, got %d", row)
	}
	return Point{Col: col, Row: row - 1}, nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// String renders a link as its two endpoints joined by a colon, lower endpoint
// first, which is how link edits appear in a move string.
func (l Link) String() string {
	return l.From.String() + ":" + l.To().String()
}

// ParseLink reads a link written as two hole names joined by a colon or dash.
func ParseLink(s string) (Link, error) {
	sep := strings.IndexAny(s, ":-")
	if sep < 0 {
		return Link{}, fmt.Errorf("malformed link %q: expected two holes joined by ':'", s)
	}
	a, err := ParsePoint(s[:sep])
	if err != nil {
		return Link{}, err
	}
	b, err := ParsePoint(s[sep+1:])
	if err != nil {
		return Link{}, err
	}
	l, ok := NewLink(a, b)
	if !ok {
		return Link{}, ErrNotKnightMove
	}
	return l, nil
}

// Move notation. An ordinary move is the hole the peg went into, optionally
// followed by the edits the player made:
//
//	D6              place a peg at D6, taking every link offered
//	D6 ~D6:E8       place at D6 but decline the link to E8
//	D6 +C4:E5       place at D6 and also link C4 to E5
//	D6 -C4:E5       place at D6 and take the existing link C4-E5 off
//	D6 xC4          place at D6 and lift your peg at C4
//
// The other entries a game record can hold are the single words swap, resign,
// draw? for an offer and draw! for an acceptance.
const (
	tokenSwap        = "swap"
	tokenResign      = "resign"
	tokenDrawOffer   = "draw?"
	tokenDrawAccept  = "draw!"
	declinePrefix    = '~'
	addLinkPrefix    = '+'
	removeLinkPrefix = '-'
	removePegPrefix  = 'x'
)

// Notation renders a committed move. declined lists the links that were offered
// on placement but withdrawn, which the Move itself does not store because it
// records what happened rather than what did not; use Game.MoveNotation for a
// move taken from a game's history.
func (m Move) Notation(declined []Link) string {
	switch m.Kind {
	case SwapMove:
		return tokenSwap
	case ResignMove:
		return tokenResign
	case DrawOfferMove:
		return tokenDrawOffer
	case DrawAcceptMove:
		return tokenDrawAccept
	}
	var b strings.Builder
	b.WriteString(m.Peg.String())
	for _, l := range declined {
		fmt.Fprintf(&b, " %c%s", declinePrefix, l)
	}
	for _, l := range m.Added {
		fmt.Fprintf(&b, " %c%s", addLinkPrefix, l)
	}
	for _, l := range m.Removed {
		fmt.Fprintf(&b, " %c%s", removeLinkPrefix, l)
	}
	for _, p := range m.RemovedPegs {
		fmt.Fprintf(&b, " %c%s", removePegPrefix, p)
	}
	return b.String()
}

// MoveNotation renders the move at the given index of the game's history,
// working out which offered links the player declined by replaying the position.
func (g *Game) MoveNotation(i int) (string, error) {
	if i < 0 || i >= len(g.history) {
		return "", fmt.Errorf("no move at index %d", i)
	}
	m := g.history[i]
	if m.Kind != PlaceMove {
		return m.Notation(nil), nil
	}
	replay, err := New(g.rs)
	if err != nil {
		return "", err
	}
	for _, prev := range g.history[:i] {
		if err := replay.apply(prev); err != nil {
			return "", err
		}
	}
	// Replay the removals and the placement, then compare the links the engine
	// offered against the ones the move kept.
	for _, l := range m.Removed {
		_ = replay.RemoveLink(l.From, l.To())
	}
	for _, p := range m.RemovedPegs {
		_ = replay.RemovePeg(p)
	}
	if err := replay.PlacePeg(m.Peg); err != nil {
		return "", err
	}
	offered := replay.staged.autoLinks
	replay.AbortTurn()
	var declined []Link
	for d := range Dir(NumDirs) {
		if offered&(1<<d) == 0 || m.AutoLinks&(1<<d) != 0 {
			continue
		}
		if l, ok := NewLink(m.Peg, m.Peg.Add(d)); ok {
			declined = append(declined, l)
		}
	}
	return m.Notation(declined), nil
}

// apply replays a recorded move onto a game, used for reconstructing positions
// from a record. It trusts the record and reports an error if the move does not
// fit the position, which is how a tampered or divergent record is caught.
func (g *Game) apply(m Move) error {
	switch m.Kind {
	case SwapMove:
		return g.Swap()
	case ResignMove:
		return g.Resign(m.Player)
	case DrawOfferMove:
		return g.OfferDraw(m.Player)
	case DrawAcceptMove:
		return g.AcceptDraw(m.Player)
	}
	for _, l := range m.Removed {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			return fmt.Errorf("replaying removal %s: %w", l, err)
		}
	}
	for _, p := range m.RemovedPegs {
		if err := g.RemovePeg(p); err != nil {
			return fmt.Errorf("replaying peg removal %s: %w", p, err)
		}
	}
	if err := g.PlacePeg(m.Peg); err != nil {
		return fmt.Errorf("replaying placement %s: %w", m.Peg, err)
	}
	// Withdraw any offered link the record did not keep.
	offered := g.staged.autoLinks
	for d := range Dir(NumDirs) {
		if offered&(1<<d) == 0 || m.AutoLinks&(1<<d) != 0 {
			continue
		}
		q := m.Peg.Add(d)
		if err := g.RemoveLink(m.Peg, q); err != nil {
			return fmt.Errorf("replaying declined link %s:%s: %w", m.Peg, q, err)
		}
	}
	for _, l := range m.Added {
		if err := g.AddLink(l.From, l.To()); err != nil {
			return fmt.Errorf("replaying addition %s: %w", l, err)
		}
	}
	_, err := g.CommitTurn()
	return err
}

// PlayNotation parses and plays one move written in player notation.
func (g *Game) PlayNotation(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("empty move")
	}
	switch strings.ToLower(s) {
	case tokenSwap:
		return g.Swap()
	case tokenResign:
		return g.Resign(g.turn)
	case tokenDrawOffer:
		return g.OfferDraw(g.turn)
	case tokenDrawAccept:
		// An offer does not consume a turn, so the acceptance necessarily comes
		// from the side that did not offer.
		return g.AcceptDraw(g.drawOfferedBy.Opponent())
	}
	fields := strings.Fields(s)
	peg, err := ParsePoint(fields[0])
	if err != nil {
		return err
	}
	// Removals come before the placement, so scan the edits first.
	var declines, adds, removes []Link
	var pegRemovals []Point
	for _, f := range fields[1:] {
		if len(f) < 2 {
			return fmt.Errorf("malformed move edit %q", f)
		}
		body := f[1:]
		switch f[0] {
		case declinePrefix, addLinkPrefix, removeLinkPrefix:
			l, err := ParseLink(body)
			if err != nil {
				return err
			}
			switch f[0] {
			case declinePrefix:
				declines = append(declines, l)
			case addLinkPrefix:
				adds = append(adds, l)
			default:
				removes = append(removes, l)
			}
		case removePegPrefix:
			p, err := ParsePoint(body)
			if err != nil {
				return err
			}
			pegRemovals = append(pegRemovals, p)
		default:
			return fmt.Errorf("unknown move edit %q", f)
		}
	}
	for _, l := range removes {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			g.AbortTurn()
			return err
		}
	}
	for _, p := range pegRemovals {
		if err := g.RemovePeg(p); err != nil {
			g.AbortTurn()
			return err
		}
	}
	if err := g.PlacePeg(peg); err != nil {
		g.AbortTurn()
		return err
	}
	for _, l := range declines {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			g.AbortTurn()
			return err
		}
	}
	for _, l := range adds {
		if err := g.AddLink(l.From, l.To()); err != nil {
			g.AbortTurn()
			return err
		}
	}
	_, err = g.CommitTurn()
	return err
}

// Transcript renders the whole game as a semicolon-separated move list.
func (g *Game) Transcript() (string, error) {
	parts := make([]string, 0, len(g.history))
	for i := range g.history {
		s, err := g.MoveNotation(i)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; "), nil
}

// ReplayTranscript builds a game from a ruleset and a transcript.
func ReplayTranscript(rs Ruleset, transcript string) (*Game, error) {
	g, err := New(rs)
	if err != nil {
		return nil, err
	}
	for i, part := range strings.Split(transcript, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if err := g.PlayNotation(part); err != nil {
			return nil, fmt.Errorf("move %d (%q): %w", i+1, part, err)
		}
	}
	return g, nil
}
