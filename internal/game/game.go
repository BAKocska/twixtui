package game

import (
	"errors"
	"fmt"
)

// Player identifies a side. The engine names sides by the axis they connect
// rather than by colour, because which colour plays which axis is a display
// choice that differs between editions and is picked by the player at setup.
type Player uint8

const (
	// NoPlayer marks an empty hole.
	NoPlayer Player = 0
	// Vertical connects the top and bottom border rows and moves first.
	Vertical Player = 1
	// Horizontal connects the left and right border columns.
	Horizontal Player = 2
)

// Opponent returns the other side.
func (p Player) Opponent() Player {
	switch p {
	case Vertical:
		return Horizontal
	case Horizontal:
		return Vertical
	}
	return NoPlayer
}

// String returns the axis name of the side.
func (p Player) String() string {
	switch p {
	case Vertical:
		return "vertical"
	case Horizontal:
		return "horizontal"
	}
	return "none"
}

// ParsePlayer reads a side name, accepting the full axis name or its initial.
func ParsePlayer(s string) (Player, error) {
	switch s {
	case "v", "V", "vertical", "Vertical":
		return Vertical, nil
	case "h", "H", "horizontal", "Horizontal":
		return Horizontal, nil
	}
	return NoPlayer, fmt.Errorf("unknown side %q (want vertical or horizontal)", s)
}

// Outcome is the state of a finished or unfinished game.
type Outcome uint8

// Possible outcomes.
const (
	Ongoing Outcome = iota
	VerticalWins
	HorizontalWins
	Draw
)

// Reason explains how a game ended.
type Reason uint8

// Possible end reasons.
const (
	NotOver Reason = iota
	// Connection means a border-to-border chain was completed.
	Connection
	// NoMovesLeft means the player to move had no legal placement.
	NoMovesLeft
	// Resignation means a player resigned.
	Resignation
	// Agreement means both players agreed to a draw.
	Agreement
)

// Result reports the state of the game.
type Result struct {
	Outcome Outcome
	Reason  Reason
}

// Over reports whether the game has finished.
func (r Result) Over() bool { return r.Outcome != Ongoing }

// Winner returns the winning side, or NoPlayer for an unfinished or drawn game.
func (r Result) Winner() Player {
	switch r.Outcome {
	case VerticalWins:
		return Vertical
	case HorizontalWins:
		return Horizontal
	}
	return NoPlayer
}

// MoveKind distinguishes the kinds of entry in a game record.
type MoveKind uint8

// Move kinds.
const (
	// PlaceMove is an ordinary turn: optional removals, one peg, optional link edits.
	PlaceMove MoveKind = iota
	// SwapMove is the second player exercising the swap option.
	SwapMove
	// ResignMove ends the game in favour of the opponent.
	ResignMove
	// DrawOfferMove offers a draw.
	DrawOfferMove
	// DrawAcceptMove accepts a standing draw offer.
	DrawAcceptMove
)

// ConsumesTurn reports whether an entry of this kind is a turn. A resignation or
// a draw offer is made whenever a player likes, including while the opponent is
// thinking, so it does not advance the move order and does not count as a ply.
func (k MoveKind) ConsumesTurn() bool { return k == PlaceMove || k == SwapMove }

// Move is one entry in the game record, holding enough to replay and to reverse
// it exactly.
type Move struct {
	Kind   MoveKind
	Player Player

	// Peg is the hole a peg was placed in, for PlaceMove and SwapMove.
	Peg Point
	// AutoLinks is the set of directions from Peg that were linked when the peg
	// was placed and still stood at the end of the turn, as a bitmask over Dir.
	AutoLinks uint8
	// Added lists links the player added by hand, beyond AutoLinks.
	Added []Link
	// Removed lists links the player deliberately took off the board before
	// placing their peg. These appear in the move's notation.
	Removed []Link
	// RemovedPegs lists the player's own pegs lifted off the board.
	RemovedPegs []Point
	// PegLinks lists the links that came away with those pegs. They are implied
	// by the removal rather than chosen, so they are not part of the notation,
	// but they are needed to reverse the move exactly.
	PegLinks []Link
}

// Errors reported by the engine. They are sentinel values so a caller can react
// to a specific rule violation instead of matching on message text.
var (
	ErrGameOver        = errors.New("the game is over")
	ErrNotYourTurn     = errors.New("not this player's turn")
	ErrOffBoard        = errors.New("hole is off the board")
	ErrCornerHole      = errors.New("corner holes do not exist")
	ErrOccupied        = errors.New("hole already holds a peg")
	ErrOpponentBorder  = errors.New("you may not place a peg in your opponent's border row")
	ErrPegAlreadySet   = errors.New("you have already placed a peg this turn")
	ErrNoPegPlaced     = errors.New("a turn must place exactly one peg")
	ErrNotKnightMove   = errors.New("pegs are not a knight's move apart")
	ErrNotOwnPeg       = errors.New("both pegs must be yours")
	ErrLinkExists      = errors.New("that link already exists")
	ErrNoSuchLink      = errors.New("there is no such link")
	ErrLinkCrosses     = errors.New("that link would cross an existing link")
	ErrLinkingLocked   = errors.New("this ruleset links automatically and does not allow link edits")
	ErrRemovalLocked   = errors.New("this ruleset does not allow removing links placed on an earlier turn")
	ErrPegRemovalOff   = errors.New("this ruleset does not allow removing pegs")
	ErrRemoveAfterPeg  = errors.New("removals come before the peg is placed, not after")
	ErrSwapUnavailable = errors.New("the swap option is not available")
	ErrNoDrawOffer     = errors.New("there is no draw offer to accept")
)

// ufSnapshot remembers a point in the connectivity structure's history. The
// generation guards against reuse across a rebuild, which discards the merge log
// and would make an older mark meaningless.
type ufSnapshot struct {
	mark int
	gen  int
}

// stagedTurn holds the uncommitted edits of the turn in progress.
type stagedTurn struct {
	pegPlaced   bool
	peg         Point
	autoLinks   uint8
	added       []Link
	removed     []Link
	removedPegs []Point
	pegLinks    []Link
}

func (s *stagedTurn) empty() bool {
	return !s.pegPlaced && len(s.added) == 0 && len(s.removed) == 0 &&
		len(s.removedPegs) == 0 && len(s.pegLinks) == 0
}

// destructive reports whether the turn took anything off the board, in which
// case connectivity has to be rebuilt rather than rolled back.
func (s *stagedTurn) destructive() bool {
	return len(s.removed) > 0 || len(s.removedPegs) > 0 || len(s.pegLinks) > 0
}

// Game is a TwixT position together with its rules and history.
type Game struct {
	rs Ruleset
	n  int

	pegs  []Player
	links []uint8

	turn    Player
	swapped bool
	result  Result

	drawOfferedBy Player

	history []Move
	staged  stagedTurn

	uf    unionFind
	ufGen int
	// turnMark is the connectivity state at the start of the turn in progress.
	turnMark ufSnapshot
	// moveMarks[i] is the connectivity state before history[i] was applied.
	moveMarks []ufSnapshot
}

// New returns a game at the initial position.
func New(rs Ruleset) (*Game, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}
	n := rs.Size
	g := &Game{
		rs:    rs,
		n:     n,
		pegs:  make([]Player, n*n),
		links: make([]uint8, n*n),
		turn:  Vertical,
	}
	g.uf.reset(n*n + 4)
	g.turnMark = g.snapshot()
	return g, nil
}

// MustNew is New for callers that know the ruleset is valid, such as tests.
func MustNew(rs Ruleset) *Game {
	g, err := New(rs)
	if err != nil {
		panic(err)
	}
	return g
}

func (g *Game) snapshot() ufSnapshot { return ufSnapshot{mark: g.uf.mark(), gen: g.ufGen} }

// Rules returns the ruleset in force.
func (g *Game) Rules() Ruleset { return g.rs }

// Size returns the side length of the board.
func (g *Game) Size() int { return g.n }

// Turn returns the side to move.
func (g *Game) Turn() Player { return g.turn }

// Ply returns the number of turns played. Resignations and draw offers are in
// the record but are not turns, so they do not count.
func (g *Game) Ply() int {
	n := 0
	for _, m := range g.history {
		if m.Kind.ConsumesTurn() {
			n++
		}
	}
	return n
}

// Entries returns the number of entries in the game record, including those that
// are not turns.
func (g *Game) Entries() int { return len(g.history) }

// Swapped reports whether the swap option was exercised.
func (g *Game) Swapped() bool { return g.swapped }

// Result returns the current result.
func (g *Game) Result() Result { return g.result }

// History returns the game record. The slice must not be modified.
func (g *Game) History() []Move { return g.history }

// DrawOfferedBy returns the player with a standing draw offer, if any.
func (g *Game) DrawOfferedBy() Player { return g.drawOfferedBy }

// virtual border node indices in the connectivity structure.
func (g *Game) nodeTop() int    { return g.n*g.n + 0 }
func (g *Game) nodeBottom() int { return g.n*g.n + 1 }
func (g *Game) nodeLeft() int   { return g.n*g.n + 2 }
func (g *Game) nodeRight() int  { return g.n*g.n + 3 }

func (g *Game) idx(p Point) int { return p.Row*g.n + p.Col }

// InBounds reports whether the point is inside the grid.
func (g *Game) InBounds(p Point) bool {
	return p.Col >= 0 && p.Col < g.n && p.Row >= 0 && p.Row < g.n
}

// IsCorner reports whether the point is one of the four corner holes, which do
// not exist on a TwixT board.
func (g *Game) IsCorner(p Point) bool {
	return (p.Col == 0 || p.Col == g.n-1) && (p.Row == 0 || p.Row == g.n-1)
}

// Exists reports whether a peg could ever stand in this hole.
func (g *Game) Exists(p Point) bool { return g.InBounds(p) && !g.IsCorner(p) }

// At returns the occupant of a hole.
func (g *Game) At(p Point) Player {
	if !g.InBounds(p) {
		return NoPlayer
	}
	return g.pegs[g.idx(p)]
}

// LinkMask returns the bitmask of link directions leaving a hole.
func (g *Game) LinkMask(p Point) uint8 {
	if !g.InBounds(p) {
		return 0
	}
	return g.links[g.idx(p)]
}

// HasLink reports whether a link is on the board.
func (g *Game) HasLink(l Link) bool {
	if !g.InBounds(l.From) || !g.InBounds(l.To()) {
		return false
	}
	return g.links[g.idx(l.From)]&(1<<l.Dir) != 0
}

// LinkOwner returns the side owning a link, or NoPlayer if it is not present.
func (g *Game) LinkOwner(l Link) Player {
	if !g.HasLink(l) {
		return NoPlayer
	}
	return g.pegs[g.idx(l.From)]
}

// IsBorderRow reports whether the point lies on one of the player's own two
// border lines.
func (g *Game) IsBorderRow(pl Player, p Point) bool {
	switch pl {
	case Vertical:
		return p.Row == 0 || p.Row == g.n-1
	case Horizontal:
		return p.Col == 0 || p.Col == g.n-1
	}
	return false
}

// CanPlace reports why a player may not place a peg in a hole, or nil if they
// may. A player may use their own border rows but never their opponent's.
func (g *Game) CanPlace(pl Player, p Point) error {
	if !g.InBounds(p) {
		return ErrOffBoard
	}
	if g.IsCorner(p) {
		return ErrCornerHole
	}
	if g.IsBorderRow(pl.Opponent(), p) {
		return ErrOpponentBorder
	}
	if g.pegs[g.idx(p)] != NoPlayer {
		return ErrOccupied
	}
	return nil
}

// LegalPlacement reports whether the side to move may place a peg in a hole.
func (g *Game) LegalPlacement(p Point) bool {
	return g.CanPlace(g.turn, p) == nil
}

// LegalPlacements returns every hole the given player may place a peg in.
func (g *Game) LegalPlacements(pl Player) []Point {
	out := make([]Point, 0, g.n*(g.n-2))
	g.EachLegalPlacement(pl, func(p Point) bool {
		out = append(out, p)
		return true
	})
	return out
}

// EachLegalPlacement calls fn for every hole the player may use, stopping early
// if fn returns false. It allocates nothing, which matters inside search.
func (g *Game) EachLegalPlacement(pl Player, fn func(Point) bool) {
	colLo, colHi := 0, g.n-1
	rowLo, rowHi := 0, g.n-1
	switch pl {
	case Vertical:
		colLo, colHi = 1, g.n-2
	case Horizontal:
		rowLo, rowHi = 1, g.n-2
	default:
		return
	}
	for row := rowLo; row <= rowHi; row++ {
		base := row * g.n
		for col := colLo; col <= colHi; col++ {
			if g.pegs[base+col] != NoPlayer {
				continue
			}
			if !fn(Point{Col: col, Row: row}) {
				return
			}
		}
	}
}

// HasLegalPlacement reports whether the player has anywhere left to play.
func (g *Game) HasLegalPlacement(pl Player) bool {
	found := false
	g.EachLegalPlacement(pl, func(Point) bool {
		found = true
		return false
	})
	return found
}

// linkBlockedBy returns the first link on the board that forbids l, or false if
// l is unobstructed. Under a ruleset where own links may cross, links belonging
// to owner are ignored.
func (g *Game) linkBlockedBy(l Link, owner Player) (Link, bool) {
	for _, b := range blockerTable[l.Dir] {
		cand := Link{
			From: Point{Col: l.From.Col + b.dCol, Row: l.From.Row + b.dRow},
			Dir:  b.dir,
		}
		if !g.HasLink(cand) {
			continue
		}
		if g.rs.OwnLinksMayCross && g.pegs[g.idx(cand.From)] == owner {
			continue
		}
		return cand, true
	}
	return Link{}, false
}

// LinkBlockedBy returns the link that prevents l from being created, if any. The
// link is canonicalised first, so a caller that built one by hand with a
// non-canonical direction still gets the right answer.
func (g *Game) LinkBlockedBy(l Link, owner Player) (Link, bool) {
	return g.linkBlockedBy(l.Canonical(), owner)
}

// setLink puts a link on the board and merges the two pegs' groups.
func (g *Game) setLink(l Link) {
	to := l.To()
	g.links[g.idx(l.From)] |= 1 << l.Dir
	g.links[g.idx(to)] |= 1 << l.Dir.Opposite()
	g.uf.union(g.idx(l.From), g.idx(to))
}

// clearLink takes a link off the board. Connectivity is repaired by the caller.
func (g *Game) clearLink(l Link) {
	to := l.To()
	g.links[g.idx(l.From)] &^= 1 << l.Dir
	g.links[g.idx(to)] &^= 1 << l.Dir.Opposite()
}

// restoreLink puts a link back without touching connectivity, for use while
// reversing a turn.
func (g *Game) restoreLink(l Link) {
	to := l.To()
	g.links[g.idx(l.From)] |= 1 << l.Dir
	g.links[g.idx(to)] |= 1 << l.Dir.Opposite()
}

// setPeg puts a peg on the board and attaches it to its own border lines.
func (g *Game) setPeg(pl Player, p Point) {
	i := g.idx(p)
	g.pegs[i] = pl
	switch pl {
	case Vertical:
		if p.Row == 0 {
			g.uf.union(i, g.nodeTop())
		}
		if p.Row == g.n-1 {
			g.uf.union(i, g.nodeBottom())
		}
	case Horizontal:
		if p.Col == 0 {
			g.uf.union(i, g.nodeLeft())
		}
		if p.Col == g.n-1 {
			g.uf.union(i, g.nodeRight())
		}
	}
}

// autoLink creates every legal link between a newly placed peg and its
// same-coloured knight neighbours, returning the mask of directions taken.
// Links sharing an endpoint can never cross each other, so the result does not
// depend on the order the directions are tried; TestAutoLinkOrderIndependent
// checks that invariant holds.
func (g *Game) autoLink(pl Player, p Point) uint8 {
	var mask uint8
	for d := range Dir(NumDirs) {
		q := p.Add(d)
		if !g.InBounds(q) || g.pegs[g.idx(q)] != pl {
			continue
		}
		l, ok := NewLink(p, q)
		if !ok {
			continue
		}
		if g.HasLink(l) {
			continue
		}
		if _, blocked := g.linkBlockedBy(l, pl); blocked {
			continue
		}
		g.setLink(l)
		mask |= 1 << d
	}
	return mask
}

// rebuildConnectivity recomputes the connectivity structure from the board. It
// is used after a removal, which cannot be undone incrementally. Doing so
// discards the merge log, so the generation is bumped to invalidate every
// snapshot taken before this point.
func (g *Game) rebuildConnectivity() {
	g.uf.reset(g.n*g.n + 4)
	g.ufGen++
	for row := range g.n {
		for col := range g.n {
			p := Point{Col: col, Row: row}
			i := g.idx(p)
			pl := g.pegs[i]
			if pl == NoPlayer {
				continue
			}
			switch pl {
			case Vertical:
				if row == 0 {
					g.uf.union(i, g.nodeTop())
				}
				if row == g.n-1 {
					g.uf.union(i, g.nodeBottom())
				}
			case Horizontal:
				if col == 0 {
					g.uf.union(i, g.nodeLeft())
				}
				if col == g.n-1 {
					g.uf.union(i, g.nodeRight())
				}
			}
			for d := range Dir(4) {
				if g.links[i]&(1<<d) == 0 {
					continue
				}
				q := p.Add(d)
				if g.InBounds(q) {
					g.uf.union(i, g.idx(q))
				}
			}
		}
	}
}

// restoreConnectivity returns the connectivity structure to the state described
// by snap, rolling the merge log back when that is still valid and rebuilding
// from the board when it is not.
func (g *Game) restoreConnectivity(snap ufSnapshot, destructive bool) {
	if !destructive && snap.gen == g.ufGen && snap.mark <= g.uf.mark() {
		g.uf.rollback(snap.mark)
		return
	}
	g.rebuildConnectivity()
}

// connected reports whether a player has joined both of their border lines.
func (g *Game) connected(pl Player) bool {
	switch pl {
	case Vertical:
		return g.uf.connected(g.nodeTop(), g.nodeBottom())
	case Horizontal:
		return g.uf.connected(g.nodeLeft(), g.nodeRight())
	}
	return false
}

// Connected reports whether the player currently has a completed chain.
func (g *Game) Connected(pl Player) bool { return g.connected(pl) }

// beginStaging records the connectivity state at the first edit of a turn.
func (g *Game) beginStaging() {
	if g.staged.empty() {
		g.turnMark = g.snapshot()
	}
}

// requireTurn checks the game is running and it is the mover's turn.
func (g *Game) requireTurn() error {
	if g.result.Over() {
		return ErrGameOver
	}
	return nil
}

// PlacePeg places the peg for the turn in progress and takes every link the
// ruleset offers. It does not end the turn: call CommitTurn.
func (g *Game) PlacePeg(p Point) error {
	if err := g.requireTurn(); err != nil {
		return err
	}
	if g.staged.pegPlaced {
		return ErrPegAlreadySet
	}
	if err := g.CanPlace(g.turn, p); err != nil {
		return err
	}
	g.beginStaging()
	g.setPeg(g.turn, p)
	g.staged.pegPlaced = true
	g.staged.peg = p
	g.staged.autoLinks = g.autoLink(g.turn, p)
	return nil
}

// AddLink links two of the current player's pegs. Under a ruleset that links
// automatically there is nothing to add by hand and this is refused.
func (g *Game) AddLink(a, b Point) error {
	if err := g.requireTurn(); err != nil {
		return err
	}
	if !g.rs.DeliberateLinking {
		return ErrLinkingLocked
	}
	l, ok := NewLink(a, b)
	if !ok {
		return ErrNotKnightMove
	}
	if !g.InBounds(a) || !g.InBounds(b) {
		return ErrOffBoard
	}
	if g.pegs[g.idx(a)] != g.turn || g.pegs[g.idx(b)] != g.turn {
		return ErrNotOwnPeg
	}
	if g.HasLink(l) {
		return ErrLinkExists
	}
	if _, blocked := g.linkBlockedBy(l, g.turn); blocked {
		return ErrLinkCrosses
	}
	g.beginStaging()
	g.setLink(l)
	g.recordLinkAddition(l)
	return nil
}

// recordLinkAddition updates the staged bookkeeping when a link appears.
func (g *Game) recordLinkAddition(l Link) {
	// A link withdrawn earlier in this turn and then put back cancels out.
	if i := indexOfLink(g.staged.removed, l); i >= 0 {
		g.staged.removed = append(g.staged.removed[:i], g.staged.removed[i+1:]...)
		return
	}
	if d, ok := dirFrom(g.staged.peg, l); ok && g.staged.pegPlaced {
		g.staged.autoLinks |= 1 << d
		return
	}
	g.staged.added = append(g.staged.added, l)
}

// forgetLink un-records a link that has just been taken off the board.
//
// A link that came into being during this turn simply never happened, so it is
// dropped from whichever staged list holds it. A link that predates the turn has
// to be remembered so the turn can be reversed, and incidental decides which
// list remembers it: links swept away with a removed peg are implied by the
// removal, so they stay out of the notation while deliberate removals go into
// it. Skipping the reconciliation leaves a link recorded as both created and
// destroyed, which reappears attached to nothing when the turn is reversed.
func (g *Game) forgetLink(l Link, incidental bool) {
	if d, ok := dirFrom(g.staged.peg, l); ok && g.staged.pegPlaced && g.staged.autoLinks&(1<<d) != 0 {
		g.staged.autoLinks &^= 1 << d
		return
	}
	if i := indexOfLink(g.staged.added, l); i >= 0 {
		g.staged.added = append(g.staged.added[:i], g.staged.added[i+1:]...)
		return
	}
	if incidental {
		g.staged.pegLinks = append(g.staged.pegLinks, l)
		return
	}
	g.staged.removed = append(g.staged.removed, l)
}

// stagedThisTurn reports whether the link came into being during this turn, in
// which case withdrawing it is part of choosing links rather than a removal.
func (g *Game) stagedThisTurn(l Link) bool {
	if d, ok := dirFrom(g.staged.peg, l); ok && g.staged.pegPlaced && g.staged.autoLinks&(1<<d) != 0 {
		return true
	}
	return indexOfLink(g.staged.added, l) >= 0
}

// RemoveLink takes one of the current player's links off the board.
//
// Withdrawing a link that came into being this turn is always allowed when
// linking is deliberate, because choosing not to have a link is a choice the
// printed rules grant. Removing a link placed on an earlier turn is a different
// act: it needs Ruleset.LinkRemoval, and the printed rules put it before the
// peg is placed, so it is refused afterwards.
func (g *Game) RemoveLink(a, b Point) error {
	if err := g.requireTurn(); err != nil {
		return err
	}
	if !g.rs.DeliberateLinking {
		return ErrLinkingLocked
	}
	l, ok := NewLink(a, b)
	if !ok {
		return ErrNotKnightMove
	}
	if !g.HasLink(l) {
		return ErrNoSuchLink
	}
	if g.pegs[g.idx(l.From)] != g.turn {
		return ErrNotOwnPeg
	}
	if !g.stagedThisTurn(l) {
		if !g.rs.LinkRemoval {
			return ErrRemovalLocked
		}
		if g.staged.pegPlaced {
			return ErrRemoveAfterPeg
		}
	}
	g.beginStaging()
	g.clearLink(l)
	g.forgetLink(l, false)
	g.rebuildConnectivity()
	return nil
}

// RemovePeg lifts one of the current player's pegs, and every link attached to
// it, off the board. The printed rules place removals before the peg is placed,
// so this is refused once the turn's peg is down.
func (g *Game) RemovePeg(p Point) error {
	if err := g.requireTurn(); err != nil {
		return err
	}
	if !g.rs.PegRemoval {
		return ErrPegRemovalOff
	}
	if g.staged.pegPlaced {
		return ErrRemoveAfterPeg
	}
	if !g.InBounds(p) {
		return ErrOffBoard
	}
	if g.pegs[g.idx(p)] != g.turn {
		return ErrNotOwnPeg
	}
	g.beginStaging()
	i := g.idx(p)
	for d := range Dir(NumDirs) {
		if g.links[i]&(1<<d) == 0 {
			continue
		}
		l, _ := NewLink(p, p.Add(d))
		g.clearLink(l)
		// Links swept away with the peg are implied by the removal rather than
		// chosen, so they are remembered apart from deliberate removals and stay
		// out of the notation. One the player added earlier in this turn is
		// reconciled away instead of being recorded as both added and lost.
		g.forgetLink(l, true)
	}
	g.pegs[i] = NoPlayer
	g.staged.removedPegs = append(g.staged.removedPegs, p)
	g.rebuildConnectivity()
	return nil
}

// StagedTurn describes the uncommitted edits of the turn in progress, for a
// caller that needs to show the player what they have done so far.
type StagedTurn struct {
	PegPlaced   bool
	Peg         Point
	AutoLinks   uint8
	Added       []Link
	Removed     []Link
	RemovedPegs []Point
	PegLinks    []Link
}

// Staged returns the turn in progress. The slices are copies, so a caller may
// hold the value across further staging calls without watching it change
// underneath; a board view asks for this every frame and the lists are almost
// always empty.
func (g *Game) Staged() StagedTurn {
	return StagedTurn{
		PegPlaced:   g.staged.pegPlaced,
		Peg:         g.staged.peg,
		AutoLinks:   g.staged.autoLinks,
		Added:       cloneLinks(g.staged.added),
		Removed:     cloneLinks(g.staged.removed),
		RemovedPegs: clonePoints(g.staged.removedPegs),
		PegLinks:    cloneLinks(g.staged.pegLinks),
	}
}

func cloneLinks(in []Link) []Link {
	if len(in) == 0 {
		return nil
	}
	return append([]Link(nil), in...)
}

func clonePoints(in []Point) []Point {
	if len(in) == 0 {
		return nil
	}
	return append([]Point(nil), in...)
}

// AbortTurn discards every uncommitted edit, restoring the position to the start
// of the turn.
func (g *Game) AbortTurn() {
	if g.staged.empty() {
		return
	}
	s := g.staged
	g.staged = stagedTurn{}
	g.undoBoard(s.pegPlaced, s.peg, s.autoLinks, s.added, s.removed, s.removedPegs, s.pegLinks, g.turn)
	g.restoreConnectivity(g.turnMark, s.destructive())
}

// undoBoard reverses one turn's worth of board changes. Order matters: links the
// turn created go first, then its peg, then the pegs it removed come back, and
// only then the links that went with them, because restoring a link needs both
// of its pegs to be present.
func (g *Game) undoBoard(pegPlaced bool, peg Point, autoLinks uint8, added, removed []Link, removedPegs []Point, pegLinks []Link, owner Player) {
	if pegPlaced {
		for d := range Dir(NumDirs) {
			if autoLinks&(1<<d) == 0 {
				continue
			}
			l, _ := NewLink(peg, peg.Add(d))
			g.clearLink(l)
		}
	}
	for _, l := range added {
		g.clearLink(l)
	}
	if pegPlaced {
		g.pegs[g.idx(peg)] = NoPlayer
	}
	for _, p := range removedPegs {
		g.pegs[g.idx(p)] = owner
	}
	for _, l := range pegLinks {
		g.restoreLink(l)
	}
	for _, l := range removed {
		g.restoreLink(l)
	}
}

// CommitTurn ends the turn, evaluates the position and passes the move to the
// opponent. A turn must place exactly one peg.
func (g *Game) CommitTurn() (Result, error) {
	if g.result.Over() {
		return g.result, ErrGameOver
	}
	if !g.staged.pegPlaced {
		return g.result, ErrNoPegPlaced
	}
	m := Move{
		Kind:      PlaceMove,
		Player:    g.turn,
		Peg:       g.staged.peg,
		AutoLinks: g.staged.autoLinks,
	}
	if len(g.staged.added) > 0 {
		m.Added = append([]Link(nil), g.staged.added...)
	}
	if len(g.staged.removed) > 0 {
		m.Removed = append([]Link(nil), g.staged.removed...)
	}
	if len(g.staged.removedPegs) > 0 {
		m.RemovedPegs = append([]Point(nil), g.staged.removedPegs...)
	}
	if len(g.staged.pegLinks) > 0 {
		m.PegLinks = append([]Link(nil), g.staged.pegLinks...)
	}
	g.record(m, g.turnMark)
	g.staged = stagedTurn{}
	// Moving on refuses a draw the opponent had offered. An offer the mover made
	// themselves stands, so that offering a draw along with your move works.
	if g.drawOfferedBy == m.Player.Opponent() {
		g.drawOfferedBy = NoPlayer
	}
	g.finishTurn(m.Player)
	return g.result, nil
}

// record appends an entry to the game record along with the connectivity state
// that preceded it.
func (g *Game) record(m Move, snap ufSnapshot) {
	g.history = append(g.history, m)
	g.moveMarks = append(g.moveMarks, snap)
}

// finishTurn evaluates the position after mover's turn and hands over the move.
func (g *Game) finishTurn(mover Player) {
	if g.connected(mover) {
		g.result = Result{Outcome: winFor(mover), Reason: Connection}
		return
	}
	next := mover.Opponent()
	g.turn = next
	g.turnMark = g.snapshot()
	if !g.HasLegalPlacement(next) {
		g.result = Result{Outcome: Draw, Reason: NoMovesLeft}
	}
}

func winFor(pl Player) Outcome {
	if pl == Vertical {
		return VerticalWins
	}
	return HorizontalWins
}

// PlayPeg places a peg and commits the turn, taking the links the ruleset
// offers. It is the whole of an ordinary move and the only entry point search
// and replay need.
func (g *Game) PlayPeg(p Point) (Result, error) {
	if err := g.PlacePeg(p); err != nil {
		return g.result, err
	}
	return g.CommitTurn()
}

// CanSwap reports whether the side to move may take the swap option, which
// exists only in answer to the very first peg.
func (g *Game) CanSwap() bool {
	if !g.rs.Swap || g.swapped || g.result.Over() || !g.staged.empty() {
		return false
	}
	// Exactly one turn has been played, and it was an ordinary placement.
	// Entries that are not turns, such as a draw offer, must not count.
	turns := 0
	var first Move
	for _, m := range g.history {
		if !m.Kind.ConsumesTurn() {
			continue
		}
		if turns == 0 {
			first = m
		}
		turns++
	}
	return turns == 1 && first.Kind == PlaceMove
}

// Swap exercises the swap option: the opening peg changes hands and reflects
// across the board's main diagonal, so it now stands on a hole that is legal for
// its new owner. The reflection is the convention used by the SGF game-record
// format and by online venues.
func (g *Game) Swap() error {
	if !g.CanSwap() {
		return ErrSwapUnavailable
	}
	var original Point
	for _, m := range g.history {
		if m.Kind == PlaceMove {
			original = m.Peg
			break
		}
	}
	mirrored := Point{Col: original.Row, Row: original.Col}
	swapper := g.turn
	snap := g.snapshot()

	g.pegs[g.idx(original)] = NoPlayer
	g.links[g.idx(original)] = 0
	g.setPeg(swapper, mirrored)
	g.rebuildConnectivity()

	g.swapped = true
	g.record(Move{Kind: SwapMove, Player: swapper, Peg: mirrored}, snap)
	g.finishTurn(swapper)
	return nil
}

// Resign concedes the game. A player may resign at any time, including while the
// opponent is thinking, so this does not depend on whose turn it is.
func (g *Game) Resign(pl Player) error {
	if g.result.Over() {
		return ErrGameOver
	}
	if pl != Vertical && pl != Horizontal {
		return ErrNotYourTurn
	}
	g.AbortTurn()
	g.record(Move{Kind: ResignMove, Player: pl}, g.snapshot())
	g.result = Result{Outcome: winFor(pl.Opponent()), Reason: Resignation}
	return nil
}

// OfferDraw records a draw offer from a player. It does not consume a turn.
func (g *Game) OfferDraw(pl Player) error {
	if g.result.Over() {
		return ErrGameOver
	}
	if pl != Vertical && pl != Horizontal {
		return ErrNotYourTurn
	}
	g.drawOfferedBy = pl
	g.record(Move{Kind: DrawOfferMove, Player: pl}, g.snapshot())
	return nil
}

// AcceptDraw accepts the opponent's standing draw offer.
func (g *Game) AcceptDraw(pl Player) error {
	if g.result.Over() {
		return ErrGameOver
	}
	if g.drawOfferedBy == NoPlayer || g.drawOfferedBy == pl {
		return ErrNoDrawOffer
	}
	g.AbortTurn()
	g.record(Move{Kind: DrawAcceptMove, Player: pl}, g.snapshot())
	g.result = Result{Outcome: Draw, Reason: Agreement}
	return nil
}

// UndoLastMove reverses the most recent record entry, discarding any turn in
// progress.
func (g *Game) UndoLastMove() error {
	g.AbortTurn()
	if len(g.history) == 0 {
		return errors.New("no move to undo")
	}
	last := len(g.history) - 1
	m := g.history[last]
	snap := g.moveMarks[last]
	g.history = g.history[:last]
	g.moveMarks = g.moveMarks[:last]

	destructive := false
	switch m.Kind {
	case PlaceMove:
		destructive = len(m.Removed) > 0 || len(m.PegLinks) > 0 || len(m.RemovedPegs) > 0
		g.undoBoard(true, m.Peg, m.AutoLinks, m.Added, m.Removed, m.RemovedPegs, m.PegLinks, m.Player)
	case SwapMove:
		// Undo the reflection and hand the peg back to the opener.
		original := Point{Col: m.Peg.Row, Row: m.Peg.Col}
		g.pegs[g.idx(m.Peg)] = NoPlayer
		g.links[g.idx(m.Peg)] = 0
		g.pegs[g.idx(original)] = m.Player.Opponent()
		g.swapped = false
		destructive = true
	case ResignMove, DrawOfferMove, DrawAcceptMove:
		// Nothing on the board changed.
	}

	// Only an entry that was a turn moved the turn on, so only such an entry
	// hands it back. Undoing a resignation or a draw offer must leave the move
	// order exactly as it was.
	if m.Kind.ConsumesTurn() {
		g.turn = m.Player
	}
	g.result = Result{}
	g.drawOfferedBy = NoPlayer
	for _, h := range g.history {
		switch {
		case h.Kind == DrawOfferMove:
			g.drawOfferedBy = h.Player
		case h.Kind.ConsumesTurn() && g.drawOfferedBy == h.Player.Opponent():
			g.drawOfferedBy = NoPlayer
		}
	}
	// An entry that changed nothing on the board needs no connectivity work, and
	// rolling back to a mark taken while some other turn was staged would
	// truncate the merge log below its current extent.
	if m.Kind == PlaceMove || m.Kind == SwapMove {
		g.restoreConnectivity(snap, destructive)
	}
	g.turnMark = g.snapshot()
	return nil
}

// Clone returns an independent copy of the game.
func (g *Game) Clone() *Game {
	c := &Game{
		rs:            g.rs,
		n:             g.n,
		pegs:          append([]Player(nil), g.pegs...),
		links:         append([]uint8(nil), g.links...),
		turn:          g.turn,
		swapped:       g.swapped,
		result:        g.result,
		drawOfferedBy: g.drawOfferedBy,
		history:       append([]Move(nil), g.history...),
		ufGen:         g.ufGen,
		turnMark:      g.turnMark,
		moveMarks:     append([]ufSnapshot(nil), g.moveMarks...),
	}
	c.uf.copyFrom(&g.uf)
	c.staged = stagedTurn{
		pegPlaced:   g.staged.pegPlaced,
		peg:         g.staged.peg,
		autoLinks:   g.staged.autoLinks,
		added:       append([]Link(nil), g.staged.added...),
		removed:     append([]Link(nil), g.staged.removed...),
		removedPegs: append([]Point(nil), g.staged.removedPegs...),
		pegLinks:    append([]Link(nil), g.staged.pegLinks...),
	}
	return c
}

// PegCount returns how many pegs a player has on the board.
func (g *Game) PegCount(pl Player) int {
	n := 0
	for _, v := range g.pegs {
		if v == pl {
			n++
		}
	}
	return n
}

// String renders the position as text, for debugging and test failure output.
func (g *Game) String() string {
	return fmt.Sprintf("twixt %dx%d ply=%d turn=%s result=%v", g.n, g.n, g.Ply(), g.turn, g.result.Outcome)
}

func indexOfLink(ls []Link, l Link) int {
	for i, x := range ls {
		if x == l {
			return i
		}
	}
	return -1
}

// dirFrom returns the direction of link l as seen from p, if p is an endpoint.
func dirFrom(p Point, l Link) (Dir, bool) {
	if l.From == p {
		return l.Dir, true
	}
	if l.To() == p {
		return l.Dir.Opposite(), true
	}
	return 0, false
}
