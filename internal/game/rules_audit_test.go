package game

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
)

// Adversarial audit of the rules engine.
//
// Every assertion here is derived from the documentary sources rather than from
// the engine: .work/research/rules-canon.md rules 1-16 and claims C1-C19, and
// .work/research/impl-openspiel-ludii.md claims C1-C8 for the second,
// machine-checked encoding (R29, R30). Where the sources do not force a
// behaviour the test says so and pins the engine's choice instead of dressing it
// up as a rule (R31); TestAuditUnforcedChoices collects those in one place.
//
// The geometry oracle, the connectivity walk, the legal-hole scan and the
// crossing scan below are written independently of the engine's own machinery.
// In particular the crossing scan never consults blockerTable, so an error in
// the table cannot make the audit agree with the engine.

// ---------------------------------------------------------------------------
// Independent geometry oracle
// ---------------------------------------------------------------------------

// auditSegmentsCross reports whether the open segments ab and cd share a point
// interior to both. It solves for the two intersection parameters explicitly in
// exact integer arithmetic, where the engine's own predicate compares
// orientation signs, so a sign-handling mistake cannot hide in both. Parallel
// and collinear pairs report false, matching the sources' reading that two links
// conflict only where their lines genuinely cross (rule 6) and the geometric
// fact that two knight links can never overlap collinearly.
func auditSegmentsCross(a, b, c, d Point) bool {
	rCol, rRow := b.Col-a.Col, b.Row-a.Row
	sCol, sRow := d.Col-c.Col, d.Row-c.Row
	den := rCol*sRow - rRow*sCol
	if den == 0 {
		return false
	}
	qCol, qRow := c.Col-a.Col, c.Row-a.Row
	tNum := qCol*sRow - qRow*sCol
	uNum := qCol*rRow - qRow*rCol
	if den < 0 {
		den, tNum, uNum = -den, -tNum, -uNum
	}
	return tNum > 0 && tNum < den && uNum > 0 && uNum < den
}

// auditCross reports whether two links cross, using only their endpoints.
func auditCross(a, b Link) bool {
	a1, a2 := a.Ends()
	b1, b2 := b.Ends()
	return auditSegmentsCross(a1, a2, b1, b2)
}

// TestAuditCrossOracleAgreesWithEngine calibrates the audit's own geometry
// against the engine's over every link pair in a neighbourhood wider than any
// crossing pair can span. If the two disagreed, every crossing assertion below
// would be measuring the wrong thing.
func TestAuditCrossOracleAgreesWithEngine(t *testing.T) {
	const radius = 5
	var links []Link
	for dCol := -radius; dCol <= radius; dCol++ {
		for dRow := -radius; dRow <= radius; dRow++ {
			for d := range Dir(NumDirs) {
				links = append(links, Link{From: Point{Col: dCol, Row: dRow}, Dir: d})
			}
		}
	}
	crossings := 0
	for _, a := range links {
		if auditCross(a, a) {
			t.Fatalf("%v crosses itself", a)
		}
		for _, b := range links {
			want := LinksCross(a, b)
			got := auditCross(a, b)
			if want != got {
				t.Fatalf("%v vs %v: engine says cross=%v, audit oracle says %v", a, b, want, got)
			}
			if got != auditCross(b, a) {
				t.Fatalf("%v vs %v: the oracle is not symmetric", a, b)
			}
			if got {
				crossings++
			}
		}
	}
	if crossings == 0 {
		t.Fatal("the oracle found no crossings at all, so it is not measuring anything")
	}
}

// ---------------------------------------------------------------------------
// Independent board readers
// ---------------------------------------------------------------------------

// auditBoardLinks lists every link on the board exactly once, reading the raw
// masks through the public accessor and naming each link from its canonical end.
func auditBoardLinks(g *Game) []Link {
	n := g.Size()
	out := make([]Link, 0, 32)
	for row := range n {
		for col := range n {
			p := Point{Col: col, Row: row}
			mask := g.LinkMask(p)
			if mask == 0 {
				continue
			}
			for d := range Dir(4) {
				if mask&(1<<d) != 0 {
					out = append(out, Link{From: p, Dir: d})
				}
			}
		}
	}
	return out
}

// auditLegalHoles restates rule 1 and rule 4b from the source text: any vacant
// hole that is neither a corner nor part of the opponent's border pair, where
// the opponent's border pair is the two outer lines of the other axis. It shares
// no code with EachLegalPlacement.
func auditLegalHoles(g *Game, pl Player) []Point {
	n := g.Size()
	var out []Point
	for row := range n {
		for col := range n {
			p := Point{Col: col, Row: row}
			if (col == 0 || col == n-1) && (row == 0 || row == n-1) {
				continue // corner hole, rule 1, C1
			}
			switch pl {
			case Vertical:
				if col == 0 || col == n-1 {
					continue // horizontal's border columns, rule 4b, C2
				}
			case Horizontal:
				if row == 0 || row == n-1 {
					continue // vertical's border rows, rule 4b, C2
				}
			default:
				return nil
			}
			if g.At(p) != NoPlayer {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// auditChain returns a border-to-border chain of pl's linked pegs, or nil if
// there is none, by breadth-first search over the link graph with parent
// tracking so the chain can be checked peg by peg (rule 10, C12; OpenSpiel C5
// requires the same transitive reachability rather than two pegs each touching
// one border).
func auditChain(g *Game, pl Player) []Point {
	n := g.Size()
	prev := make([]int, n*n)
	seen := make([]bool, n*n)
	for i := range prev {
		prev[i] = -1
	}
	onFar := func(p Point) bool {
		if pl == Vertical {
			return p.Row == n-1
		}
		return p.Col == n-1
	}
	var queue []Point
	for i := range n {
		p := Point{Col: i, Row: 0}
		if pl == Horizontal {
			p = Point{Col: 0, Row: i}
		}
		if g.At(p) != pl {
			continue
		}
		seen[g.idx(p)] = true
		queue = append(queue, p)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if onFar(p) {
			var chain []Point
			for cur := p; ; {
				chain = append(chain, cur)
				i := prev[g.idx(cur)]
				if i < 0 {
					break
				}
				cur = Point{Col: i % n, Row: i / n}
			}
			for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
				chain[i], chain[j] = chain[j], chain[i]
			}
			return chain
		}
		mask := g.LinkMask(p)
		for d := range Dir(NumDirs) {
			if mask&(1<<d) == 0 {
				continue
			}
			q := p.Add(d)
			if !g.InBounds(q) || g.At(q) != pl || seen[g.idx(q)] {
				continue
			}
			seen[g.idx(q)] = true
			prev[g.idx(q)] = g.idx(p)
			queue = append(queue, q)
		}
	}
	return nil
}

// auditState renders everything a position consists of, for byte-identical
// comparison across abort and undo.
func auditState(g *Game) string {
	var b strings.Builder
	b.WriteString(snapshot(g))
	fmt.Fprintf(&b, "|turn=%v|out=%v|why=%v|swap=%v|ply=%d|entries=%d|offer=%v",
		g.Turn(), g.Result().Outcome, g.Result().Reason, g.Swapped(),
		g.Ply(), g.Entries(), g.DrawOfferedBy())
	s := g.Staged()
	fmt.Fprintf(&b, "|staged=%v/%v/%08b/%v/%v/%v/%v",
		s.PegPlaced, s.Peg, s.AutoLinks, s.Added, s.Removed, s.RemovedPegs, s.PegLinks)
	return b.String()
}

// auditDiff describes where two state strings first differ.
func auditDiff(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := max(0, i-32)
	return fmt.Sprintf("first difference at offset %d\n    want ...%s\n    got  ...%s",
		i, want[lo:min(len(want), i+32)], got[lo:min(len(got), i+32)])
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---------------------------------------------------------------------------
// The invariant set
// ---------------------------------------------------------------------------

// auditPosition asserts every rule the position itself can be held to. label is
// evaluated only on failure and must identify the game well enough to replay it.
func auditPosition(t *testing.T, g *Game, label func() string) {
	t.Helper()
	n := g.Size()
	rs := g.Rules()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Errorf("%s\n  %s", fmt.Sprintf(format, args...), label())
	}

	// Rule 1 and rule 4b: where pegs may stand.
	for row := range n {
		for col := range n {
			p := Point{Col: col, Row: row}
			owner := g.At(p)
			if owner == NoPlayer {
				continue
			}
			if owner != Vertical && owner != Horizontal {
				fail("hole %v holds unknown player %d", p, owner)
			}
			if g.IsCorner(p) {
				fail("peg on corner hole %v, which does not exist (rule 1, C1)", p)
			}
			if g.IsBorderRow(owner.Opponent(), p) {
				fail("%v peg on %v, in the opponent's border row (rule 4b, C2)", owner, p)
			}
		}
	}

	// Rule 5: what a link may join.
	links := auditBoardLinks(g)
	for _, l := range links {
		from, to := l.Ends()
		if !g.InBounds(to) {
			fail("link %v points off the board", l)
			continue
		}
		dCol, dRow := to.Col-from.Col, to.Row-from.Row
		if !((abs(dCol) == 1 && abs(dRow) == 2) || (abs(dCol) == 2 && abs(dRow) == 1)) {
			fail("link %v joins holes (%d,%d) apart, not a knight's move (rule 5, C3)", l, dCol, dRow)
		}
		a, b := g.At(from), g.At(to)
		if a == NoPlayer || b == NoPlayer {
			fail("link %v has an empty endpoint (%v holds %v, %v holds %v)", l, from, a, to, b)
		}
		if a != b {
			fail("link %v joins a %v peg to a %v peg; links join one colour (rule 5)", l, a, b)
		}
		if g.LinkMask(to)&(1<<l.Dir.Opposite()) == 0 {
			fail("link %v is recorded at %v but not at %v: the two masks disagree", l, from, to)
		}
		if !g.HasLink(l) {
			fail("link %v came out of the mask but HasLink says no", l)
		}
		if owner := g.LinkOwner(l); owner != a {
			fail("link %v owner = %v, endpoints hold %v", l, owner, a)
		}
		// Naming the same edge from its far end must describe the same edge.
		rev := Link{From: to, Dir: l.Dir.Opposite()}
		if rev.Canonical() != l || !g.HasLink(rev) {
			fail("link %v named from %v as %v is not recognised as the same edge", l, to, rev)
		}
	}

	// The two masks of one edge must agree, scanned over all eight directions
	// rather than the four canonical ones, so that a stray bit which exists only
	// at the non-canonical end is still visited.
	for row := range n {
		for col := range n {
			p := Point{Col: col, Row: row}
			mask := g.LinkMask(p)
			for d := range Dir(NumDirs) {
				if mask&(1<<d) == 0 {
					continue
				}
				q := p.Add(d)
				if !g.InBounds(q) {
					fail("hole %v carries a link bit towards %v, which is off the board", p, d)
					continue
				}
				if g.LinkMask(q)&(1<<d.Opposite()) == 0 {
					fail("hole %v carries a %v link bit but %v has no matching %v bit: a one-sided link",
						p, d, q, d.Opposite())
				}
				if g.At(p) == NoPlayer {
					fail("hole %v carries a %v link bit but holds no peg", p, d)
				}
			}
		}
	}

	// Rules 6 and 7: no two links cross, unless this ruleset lets a player's own
	// links cross (rule 16a, C6). Independent O(links^2) geometric scan.
	for i := range links {
		for j := i + 1; j < len(links); j++ {
			if !auditCross(links[i], links[j]) {
				continue
			}
			ownerI, ownerJ := g.At(links[i].From), g.At(links[j].From)
			if rs.OwnLinksMayCross && ownerI == ownerJ {
				continue
			}
			fail("links %v (%v) and %v (%v) cross, which %s forbids (rule 6/7, C4/C9)",
				links[i], ownerI, links[j], ownerJ, rs.Canonical())
		}
	}

	// Legal placements: the engine's enumeration against the rule as written.
	for _, pl := range []Player{Vertical, Horizontal} {
		want := auditLegalHoles(g, pl)
		got := g.LegalPlacements(pl)
		if len(want) != len(got) {
			fail("%v has %d legal holes by the rule, the engine offers %d", pl, len(want), len(got))
			continue
		}
		set := make(map[Point]bool, len(got))
		for _, p := range got {
			set[p] = true
		}
		for _, p := range want {
			if !set[p] {
				fail("%v may play %v by the rule but the engine omits it", pl, p)
			}
			if err := g.CanPlace(pl, p); err != nil {
				fail("%v may play %v by the rule but CanPlace says %v", pl, p, err)
			}
		}
		if g.HasLegalPlacement(pl) != (len(want) > 0) {
			fail("%v: HasLegalPlacement = %v with %d legal holes", pl, g.HasLegalPlacement(pl), len(want))
		}
	}

	// Connectivity, against two independent walks of the link graph.
	for _, pl := range []Player{Vertical, Horizontal} {
		chain := auditChain(g, pl)
		if got := g.Connected(pl); got != (chain != nil) {
			fail("%v: Connected = %v, an independent walk found a chain = %v", pl, got, chain != nil)
		}
		if got := floodFillConnected(g, pl); got != (chain != nil) {
			fail("%v: flood fill says %v, the chain walk says %v", pl, got, chain != nil)
		}
		if chain != nil {
			auditVerifyChain(t, g, pl, chain, label)
		}
	}

	// Result consistency, and rules 10 and 11 as the reason claims.
	res := g.Result()
	switch res.Outcome {
	case Ongoing:
		if res.Reason != NotOver {
			fail("ongoing game carries end reason %v", res.Reason)
		}
		if res.Over() || res.Winner() != NoPlayer {
			fail("ongoing game reports Over=%v Winner=%v", res.Over(), res.Winner())
		}
	case VerticalWins, HorizontalWins:
		w := res.Winner()
		if w == NoPlayer {
			fail("win outcome %v with no winner", res.Outcome)
			break
		}
		if res.Reason == Connection {
			if auditChain(g, w) == nil {
				fail("%v is declared the winner by connection with no border-to-border chain (rule 10, C12)", w)
			}
			if auditChain(g, w.Opponent()) != nil {
				fail("the losing side %v also holds a completed chain", w.Opponent())
			}
		}
	case Draw:
		if res.Reason == NoMovesLeft {
			if holes := auditLegalHoles(g, g.Turn()); len(holes) != 0 {
				fail("drawn for lack of moves but %v still has %d legal holes, first %v",
					g.Turn(), len(holes), holes[0])
			}
		}
		if res.Winner() != NoPlayer {
			fail("drawn game reports winner %v", res.Winner())
		}
	default:
		fail("unknown outcome %d", res.Outcome)
	}

	// Turn order alternates strictly over the entries that are turns (RD11).
	// Resignations and draw offers consume no turn, so they are skipped.
	if !res.Over() {
		for i := g.Entries() - 1; i >= 0; i-- {
			h := g.History()[i]
			if !h.Kind.ConsumesTurn() {
				continue
			}
			if g.Turn() != h.Player.Opponent() {
				fail("the last turn was %v's but it is %v's move", h.Player, g.Turn())
			}
			break
		}
	}

	// A finished game accepts nothing further: there is no pass (rule 15, C19)
	// and a finished game is finished. Probed on a clone so it cannot disturb
	// the position under test.
	if res.Over() {
		c := g.Clone()
		if holes := auditLegalHoles(c, c.Turn()); len(holes) > 0 {
			if _, err := c.PlayPeg(holes[0]); err != ErrGameOver {
				fail("placing %v after the game ended returned %v, want %v", holes[0], err, ErrGameOver)
			}
		}
		if err := c.Resign(c.Turn()); err != ErrGameOver {
			fail("resigning after the game ended returned %v, want %v", err, ErrGameOver)
		}
		if err := c.OfferDraw(c.Turn()); err != ErrGameOver {
			fail("offering a draw after the game ended returned %v, want %v", err, ErrGameOver)
		}
		if c.CanSwap() {
			fail("swap offered after the game ended")
		}
		if auditState(c) != auditState(g) {
			fail("moves rejected after the game ended still changed the position")
		}
	}
}

func auditVerifyChain(t *testing.T, g *Game, pl Player, chain []Point, label func() string) {
	t.Helper()
	n := g.Size()
	bad := func(format string, args ...any) {
		t.Helper()
		t.Errorf("%s\n  chain %v\n  %s", fmt.Sprintf(format, args...), chain, label())
	}
	if len(chain) == 0 {
		bad("%v: empty chain returned", pl)
		return
	}
	first, last := chain[0], chain[len(chain)-1]
	switch pl {
	case Vertical:
		if first.Row != 0 || last.Row != n-1 {
			bad("vertical chain runs %v..%v, which does not span the top and bottom rows", first, last)
		}
	case Horizontal:
		if first.Col != 0 || last.Col != n-1 {
			bad("horizontal chain runs %v..%v, which does not span the left and right columns", first, last)
		}
	}
	for i, p := range chain {
		if g.At(p) != pl {
			bad("chain hole %v holds %v, not %v", p, g.At(p), pl)
		}
		if i == 0 {
			continue
		}
		q := chain[i-1]
		l, ok := NewLink(q, p)
		if !ok {
			bad("chain step %v -> %v is not a knight's move", q, p)
			continue
		}
		if !g.HasLink(l) {
			bad("chain step %v -> %v is not linked", q, p)
		}
	}
}

// ---------------------------------------------------------------------------
// Ruleset matrix
// ---------------------------------------------------------------------------

// auditRulesets returns the presets plus non-preset option combinations over the
// given board sizes. Every combination is one the engine accepts, so every one
// of them has to obey the rules (R31).
func auditRulesets(sizes []int) []Ruleset {
	bases := []Ruleset{Std, PP, Classic3M,
		// Deliberate linking with permanent links: rule 4c without rule 9.
		{DeliberateLinking: true, LinkRemoval: false, Swap: true},
		// Peg removal, the reading only one transcription supports (RD7).
		{DeliberateLinking: true, LinkRemoval: true, PegRemoval: true, Swap: true},
		// Peg removal without link removal: a combination Validate accepts.
		{DeliberateLinking: true, LinkRemoval: false, PegRemoval: true, Swap: true},
		// The PP crossing rule without the PP linking rule.
		{DeliberateLinking: true, LinkRemoval: true, OwnLinksMayCross: true},
		// Automatic linking with own links blocking: the formalisation used by
		// the PSPACE-completeness paper and by OpenSpiel (C6, OpenSpiel C3).
		{DeliberateLinking: false, OwnLinksMayCross: false, Swap: true},
	}
	var out []Ruleset
	for _, size := range sizes {
		for _, rs := range bases {
			rs.Size = size
			if err := rs.Validate(); err != nil {
				continue
			}
			out = append(out, rs)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Turns as data, so a game can be replayed without the notation layer
// ---------------------------------------------------------------------------

// auditTurn is one turn expressed as the edits it makes, in the order the
// printed rules give them: removals, then the peg, then the link choices
// (rule 4a-c).
type auditTurn struct {
	removeLinks []Link
	removePegs  []Point
	peg         Point
	declines    []Link
	adds        []Link
}

func (a auditTurn) String() string {
	var b strings.Builder
	for _, l := range a.removeLinks {
		fmt.Fprintf(&b, "-%s ", l)
	}
	for _, p := range a.removePegs {
		fmt.Fprintf(&b, "x%s ", p)
	}
	b.WriteString(a.peg.String())
	for _, l := range a.declines {
		fmt.Fprintf(&b, " ~%s", l)
	}
	for _, l := range a.adds {
		fmt.Fprintf(&b, " +%s", l)
	}
	return b.String()
}

// stage applies everything up to but not including the commit.
func (a auditTurn) stage(g *Game) error {
	for _, l := range a.removeLinks {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			return fmt.Errorf("remove link %s: %w", l, err)
		}
	}
	for _, p := range a.removePegs {
		if err := g.RemovePeg(p); err != nil {
			return fmt.Errorf("remove peg %s: %w", p, err)
		}
	}
	if err := g.PlacePeg(a.peg); err != nil {
		return fmt.Errorf("place %s: %w", a.peg, err)
	}
	for _, l := range a.declines {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			return fmt.Errorf("decline %s: %w", l, err)
		}
	}
	for _, l := range a.adds {
		if err := g.AddLink(l.From, l.To()); err != nil {
			return fmt.Errorf("add %s: %w", l, err)
		}
	}
	return nil
}

// apply plays the whole turn and reports the first refusal.
func (a auditTurn) apply(g *Game) error {
	if err := a.stage(g); err != nil {
		return err
	}
	_, err := g.CommitTurn()
	return err
}

// auditGamePlan is a game recorded as the turns that built it.
type auditGamePlan struct {
	rs    Ruleset
	seed  [2]uint64
	turns []auditTurn
}

func (p *auditGamePlan) label() string {
	parts := make([]string, 0, len(p.turns))
	for _, t := range p.turns {
		parts = append(parts, t.String())
	}
	return fmt.Sprintf("ruleset %s\n  seed {%d,%d}\n  moves: %s",
		p.rs.Canonical(), p.seed[0], p.seed[1], strings.Join(parts, "; "))
}

func auditOwnLinks(g *Game, pl Player) []Link {
	var out []Link
	for _, l := range auditBoardLinks(g) {
		if g.At(l.From) == pl {
			out = append(out, l)
		}
	}
	return out
}

func auditOwnPegs(g *Game, pl Player) []Point {
	n := g.Size()
	var out []Point
	for row := range n {
		for col := range n {
			p := Point{Col: col, Row: row}
			if g.At(p) == pl {
				out = append(out, p)
			}
		}
	}
	return out
}

// auditAddableLinks lists links the player could add by hand: both endpoints
// their own, not already linked, and unblocked.
func auditAddableLinks(g *Game, pl Player) []Link {
	var out []Link
	seen := map[Link]bool{}
	for _, p := range auditOwnPegs(g, pl) {
		for d := range Dir(NumDirs) {
			q := p.Add(d)
			if !g.InBounds(q) || g.At(q) != pl {
				continue
			}
			l, ok := NewLink(p, q)
			if !ok || seen[l] || g.HasLink(l) {
				continue
			}
			if _, blocked := g.LinkBlockedBy(l, pl); blocked {
				continue
			}
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// auditPlanHead chooses the removals and the peg for the side to move.
func auditPlanHead(g *Game, rng *rand.Rand, allowEdits bool) (auditTurn, bool) {
	pl := g.Turn()
	holes := auditLegalHoles(g, pl)
	if len(holes) == 0 {
		return auditTurn{}, false
	}
	var turn auditTurn
	rs := g.Rules()
	if allowEdits && rs.DeliberateLinking {
		if rs.LinkRemoval {
			if own := auditOwnLinks(g, pl); len(own) > 0 && rng.IntN(5) == 0 {
				turn.removeLinks = append(turn.removeLinks, own[rng.IntN(len(own))])
			}
		}
		if rs.PegRemoval {
			if pegs := auditOwnPegs(g, pl); len(pegs) > 0 && rng.IntN(8) == 0 {
				turn.removePegs = append(turn.removePegs, pegs[rng.IntN(len(pegs))])
			}
		}
	}
	// Removals only free holes, so a hole legal now is still legal after them.
	turn.peg = holes[rng.IntN(len(holes))]
	return turn, true
}

// auditPlanTail fills in the choices that can only be made once the peg is on
// the board: which offered links to decline and which extra links to add. It
// applies them as it chooses them, so the recorded plan and the live game agree.
func auditPlanTail(g *Game, turn *auditTurn, rng *rand.Rand, allowEdits bool) error {
	if !allowEdits || !g.Rules().DeliberateLinking {
		return nil
	}
	offered := g.Staged().AutoLinks
	for d := range Dir(NumDirs) {
		if offered&(1<<d) == 0 || rng.IntN(4) != 0 {
			continue
		}
		l, ok := NewLink(turn.peg, turn.peg.Add(d))
		if !ok {
			continue
		}
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			return fmt.Errorf("declining the offered link %s: %w", l, err)
		}
		turn.declines = append(turn.declines, l)
	}
	if cands := auditAddableLinks(g, g.Turn()); len(cands) > 0 && rng.IntN(5) == 0 {
		l := cands[rng.IntN(len(cands))]
		if err := g.AddLink(l.From, l.To()); err != nil {
			return fmt.Errorf("adding %s: %w", l, err)
		}
		turn.adds = append(turn.adds, l)
	}
	return nil
}

// auditPlayRandomTurn plays one random turn, recording it in the plan.
func auditPlayRandomTurn(g *Game, plan *auditGamePlan, rng *rand.Rand, allowEdits bool) (bool, error) {
	turn, ok := auditPlanHead(g, rng, allowEdits)
	if !ok {
		return false, nil
	}
	for _, l := range turn.removeLinks {
		if err := g.RemoveLink(l.From, l.To()); err != nil {
			return false, fmt.Errorf("removing own link %s: %w", l, err)
		}
	}
	for _, p := range turn.removePegs {
		if err := g.RemovePeg(p); err != nil {
			return false, fmt.Errorf("removing own peg %s: %w", p, err)
		}
	}
	if err := g.PlacePeg(turn.peg); err != nil {
		return false, fmt.Errorf("placing %s: %w", turn.peg, err)
	}
	if err := auditPlanTail(g, &turn, rng, allowEdits); err != nil {
		return false, err
	}
	plan.turns = append(plan.turns, turn)
	if _, err := g.CommitTurn(); err != nil {
		return false, fmt.Errorf("committing %s: %w", turn, err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Random-game invariant fuzzing
// ---------------------------------------------------------------------------

// auditPlayRandomGame plays one game to its end, checking every invariant after
// every move, and returns the plan that built it.
func auditPlayRandomGame(t *testing.T, rs Ruleset, seed [2]uint64, allowEdits bool) *auditGamePlan {
	t.Helper()
	plan := &auditGamePlan{rs: rs, seed: seed}
	rng := rand.New(rand.NewPCG(seed[0], seed[1]))
	g := MustNew(rs)
	auditPosition(t, g, plan.label)
	// A turn always adds a peg unless the ruleset lets pegs be lifted, so only a
	// ruleset without peg removal is bound to fill the board and end. With peg
	// removal, random play can wander indefinitely; that is a fact about the
	// rules, not a defect, so the fuzz simply stops.
	limit := rs.Size*rs.Size + 8
	for !g.Result().Over() {
		if len(plan.turns) > limit {
			if rs.PegRemoval {
				return plan
			}
			t.Fatalf("game did not terminate in %d turns\n  %s", limit, plan.label())
		}
		ok, err := auditPlayRandomTurn(g, plan, rng, allowEdits)
		if err != nil {
			t.Fatalf("%v\n  %s", err, plan.label())
		}
		if !ok {
			t.Fatalf("no legal hole but the game is not over\n  %s", plan.label())
		}
		auditPosition(t, g, plan.label)
		if t.Failed() {
			return plan
		}
	}
	// Rules 10 and 11: a game ends for one of exactly two board reasons.
	switch res := g.Result(); res.Reason {
	case Connection:
		if res.Winner() == NoPlayer {
			t.Errorf("connection with no winner\n  %s", plan.label())
		}
	case NoMovesLeft:
		if res.Outcome != Draw {
			t.Errorf("no moves left but the outcome is %v\n  %s", res.Outcome, plan.label())
		}
	default:
		t.Errorf("random game ended with reason %v\n  %s", res.Reason, plan.label())
	}
	return plan
}

func auditSizes() []int {
	if testing.Short() {
		return []int{6, 7, 14}
	}
	return []int{6, 7, 8, 9, 10, 11, 12, 13, 14}
}

// TestAuditRandomGameInvariants fuzzes games that use the whole turn vocabulary
// across every board size from MinSize to 14 and every ruleset combination,
// asserting the full invariant set after every single move.
func TestAuditRandomGameInvariants(t *testing.T) {
	rulesets := auditRulesets(auditSizes())
	per := 1
	if !testing.Short() {
		per = 30
	}
	games, turns := 0, 0
	for i, rs := range rulesets {
		for trial := range per {
			seed := [2]uint64{uint64(i)*1000 + uint64(trial), 0x5eed}
			plan := auditPlayRandomGame(t, rs, seed, true)
			games++
			turns += len(plan.turns)
			if t.Failed() {
				return
			}
		}
	}
	t.Logf("%d games, %d committed turns, %d rulesets", games, turns, len(rulesets))
	if games < 20 {
		t.Fatalf("only %d games played, the fuzz is not exercising anything", games)
	}
}

// TestAuditPlacementOnlyGames is the volume pass: thousands of placement-only
// games, still with the full invariant set after every move.
func TestAuditPlacementOnlyGames(t *testing.T) {
	sizes := auditSizes()
	per := 3
	if !testing.Short() {
		per = 110
	}
	rulesets := auditRulesets(sizes)
	games := 0
	for i, rs := range rulesets {
		for trial := range per {
			seed := [2]uint64{uint64(i)*7919 + uint64(trial), 0xa11ce}
			auditPlayRandomGame(t, rs, seed, false)
			games++
			if t.Failed() {
				return
			}
		}
	}
	t.Logf("%d placement-only games over %d rulesets", games, len(rulesets))
}

// ---------------------------------------------------------------------------
// Staged edits: abort and undo over every combination
// ---------------------------------------------------------------------------

// auditEditBase builds a position holding everything a turn can operate on: own
// pegs with links from earlier turns, an own knight pair deliberately left
// unlinked, an own peg the next placement will link to, and opponent pegs and
// links that must stay untouchable.
func auditEditBase(t *testing.T) *Game {
	t.Helper()
	rs := Std
	rs.Size = 12
	rs.PegRemoval = true
	g := MustNew(rs)
	steps := []string{"E6", "A2", "G7", "B4", "D4", "A6", "C2 ~C2:D4", "B8"}
	for _, s := range steps {
		if err := g.PlayNotation(s); err != nil {
			t.Fatalf("building the base position at %q: %v", s, err)
		}
	}
	if g.Turn() != Vertical {
		t.Fatalf("base position: turn = %v, want vertical", g.Turn())
	}
	for _, pair := range [][2]string{{"E6", "G7"}, {"D4", "E6"}, {"A2", "B4"}, {"A6", "B8"}} {
		l, _ := NewLink(at(pair[0]), at(pair[1]))
		if !g.HasLink(l) {
			t.Fatalf("base position is missing link %s:%s", pair[0], pair[1])
		}
	}
	if l, _ := NewLink(at("C2"), at("D4")); g.HasLink(l) {
		t.Fatal("base position: C2:D4 should have been declined")
	}
	return g
}

// auditEdit is one thing a player can try inside a turn.
type auditEdit struct {
	name  string
	apply func(g *Game) error
}

// TestAuditStagedEditCombinations asserts that AbortTurn restores the position
// exactly after every combination of staged edits, and that committing the same
// combination and undoing it restores the position exactly too.
//
// Edits the engine refuses are skipped rather than failed. Rule 4 lists removals
// before the peg and link choices after, and the engine enforces that order; the
// audit's contribution is that whatever the engine accepts must be exactly
// reversible, and that a refusal must leave the board alone.
func TestAuditStagedEditCombinations(t *testing.T) {
	optional := []auditEdit{
		{"removeOldLink", func(g *Game) error { return g.RemoveLink(at("D4"), at("E6")) }},
		{"removeOldPeg", func(g *Game) error { return g.RemovePeg(at("E6")) }},
		{"removeLinkedNeighbourPeg", func(g *Game) error { return g.RemovePeg(at("G7")) }},
		{"declineOfferedLink", func(g *Game) error { return g.RemoveLink(at("F5"), at("D4")) }},
		{"addOlderPair", func(g *Game) error { return g.AddLink(at("C2"), at("D4")) }},
		{"reAddDeclinedLink", func(g *Game) error { return g.AddLink(at("F5"), at("D4")) }},
	}
	place := auditEdit{"placeF5", func(g *Game) error { return g.PlacePeg(at("F5")) }}

	base := auditEditBase(t)
	want := auditState(base)
	combos, committed := 0, 0

	for mask := range 1 << len(optional) {
		for pegAt := 0; pegAt <= len(optional); pegAt++ {
			var seq []auditEdit
			for i, e := range optional {
				if i == pegAt {
					seq = append(seq, place)
				}
				if mask&(1<<i) != 0 {
					seq = append(seq, e)
				}
			}
			if pegAt == len(optional) {
				seq = append(seq, place)
			}
			combos++
			names := make([]string, 0, len(seq))
			for _, e := range seq {
				names = append(names, e.name)
			}
			label := strings.Join(names, " > ")

			// Pass one: abort.
			g := base.Clone()
			accepted := auditApplySequence(t, g, seq, label)
			assertNoDanglingLinks(t, g)
			g.AbortTurn()
			assertNoDanglingLinks(t, g)
			if got := auditState(g); got != want {
				t.Errorf("AbortTurn did not restore the position\n  sequence: %s\n  accepted: %s\n  %s",
					label, strings.Join(accepted, ", "), auditDiff(want, got))
			}

			// Pass two: commit, then undo.
			g2 := base.Clone()
			accepted2 := auditApplySequence(t, g2, seq, label)
			if _, err := g2.CommitTurn(); err != nil {
				if err != ErrNoPegPlaced {
					t.Errorf("committing %s: %v", label, err)
				}
				g2.AbortTurn()
				if got := auditState(g2); got != want {
					t.Errorf("abort after a refused commit did not restore the position\n  sequence: %s\n  %s",
						label, auditDiff(want, got))
				}
				continue
			}
			committed++
			assertNoDanglingLinks(t, g2)
			auditPosition(t, g2, func() string { return "staged edit sequence: " + label })
			if err := g2.UndoLastMove(); err != nil {
				t.Fatalf("undoing %s: %v", label, err)
			}
			assertNoDanglingLinks(t, g2)
			if got := auditState(g2); got != want {
				t.Errorf("UndoLastMove did not restore the position\n  sequence: %s\n  accepted: %s\n  %s",
					label, strings.Join(accepted2, ", "), auditDiff(want, got))
			}
			if t.Failed() {
				return
			}
		}
	}
	t.Logf("%d edit sequences, %d reached a commit and were undone", combos, committed)
	if committed < 32 {
		t.Fatalf("only %d sequences reached a commit, the sweep is not exercising the engine", committed)
	}
}

// auditApplySequence applies each edit in turn, skipping the ones the engine
// refuses, and asserts that a refusal changes nothing.
func auditApplySequence(t *testing.T, g *Game, seq []auditEdit, label string) []string {
	t.Helper()
	var accepted []string
	for _, e := range seq {
		before := auditState(g)
		if err := e.apply(g); err != nil {
			if after := auditState(g); after != before {
				t.Errorf("refused edit %q (%v) still changed the position\n  sequence: %s\n  %s",
					e.name, err, label, auditDiff(before, after))
			}
			continue
		}
		accepted = append(accepted, e.name)
	}
	return accepted
}

// TestAuditRefusedEditsAreInert pins the edits that must always be refused, and
// that a refusal never touches the board.
func TestAuditRefusedEditsAreInert(t *testing.T) {
	base := auditEditBase(t)
	cases := []struct {
		name string
		want error
		try  func(g *Game) error
	}{
		{"opponent link", ErrNotOwnPeg, func(g *Game) error { return g.RemoveLink(at("A2"), at("B4")) }},
		{"opponent peg", ErrNotOwnPeg, func(g *Game) error { return g.RemovePeg(at("A2")) }},
		{"empty hole", ErrNotOwnPeg, func(g *Game) error { return g.RemovePeg(at("K11")) }},
		{"off-board peg", ErrOffBoard, func(g *Game) error { return g.RemovePeg(Point{Col: -1, Row: 4}) }},
		{"link to an opponent peg", ErrNotOwnPeg, func(g *Game) error { return g.AddLink(at("C2"), at("B4")) }},
		{"link to an empty hole", ErrNotOwnPeg, func(g *Game) error { return g.AddLink(at("D4"), at("F3")) }},
		{"non-knight link", ErrNotKnightMove, func(g *Game) error { return g.AddLink(at("D4"), at("E5")) }},
		{"existing link", ErrLinkExists, func(g *Game) error { return g.AddLink(at("D4"), at("E6")) }},
		{"absent link", ErrNoSuchLink, func(g *Game) error { return g.RemoveLink(at("C2"), at("D4")) }},
		{"opponent border row", ErrOpponentBorder, func(g *Game) error { return g.PlacePeg(at("A5")) }},
		{"corner", ErrCornerHole, func(g *Game) error { return g.PlacePeg(at("A1")) }},
		{"occupied", ErrOccupied, func(g *Game) error { return g.PlacePeg(at("D4")) }},
		{"off board", ErrOffBoard, func(g *Game) error { return g.PlacePeg(Point{Col: 12, Row: 3}) }},
	}
	for _, c := range cases {
		g := base.Clone()
		before := auditState(g)
		if err := c.try(g); err != c.want {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if after := auditState(g); after != before {
			t.Errorf("%s: a refused edit changed the position\n  %s", c.name, auditDiff(before, after))
		}
	}
	// Rule 4b: exactly one peg per turn, and the turn's own peg is not removable.
	g := base.Clone()
	if err := g.PlacePeg(at("F5")); err != nil {
		t.Fatal(err)
	}
	before := auditState(g)
	if err := g.PlacePeg(at("K5")); err != ErrPegAlreadySet {
		t.Errorf("second peg: got %v, want %v", err, ErrPegAlreadySet)
	}
	if err := g.RemovePeg(at("F5")); err == nil {
		t.Error("removing the peg placed this turn should be refused")
	}
	if after := auditState(g); after != before {
		t.Errorf("a refused edit changed the position\n  %s", auditDiff(before, after))
	}
	// An empty turn is not a turn either.
	g2 := base.Clone()
	if _, err := g2.CommitTurn(); err != ErrNoPegPlaced {
		t.Errorf("committing an empty turn: got %v, want %v", err, ErrNoPegPlaced)
	}
}

// TestAuditUndoUnderLinkEdits plays random games that decline offered links,
// remove older links and remove pegs, undoes every turn, and asserts each
// intermediate position comes back byte-identical. It then replays the same
// turns and asserts the final position is identical again, which is what a saved
// game and a networked opponent both rely on.
func TestAuditUndoUnderLinkEdits(t *testing.T) {
	variants := []struct {
		name string
		rs   Ruleset
	}{
		{"declines_only", withSize(Ruleset{DeliberateLinking: true, Swap: true}, 10)},
		{"link_removal", withSize(Std, 10)},
		{"peg_removal", withSize(Ruleset{DeliberateLinking: true, LinkRemoval: true, PegRemoval: true, Swap: true}, 10)},
		{"peg_removal_no_link_removal", withSize(Ruleset{DeliberateLinking: true, PegRemoval: true, Swap: true}, 10)},
		{"own_cross", withSize(Ruleset{DeliberateLinking: true, LinkRemoval: true, OwnLinksMayCross: true}, 9)},
		{"automatic", withSize(PP, 10)},
	}
	trials := 3
	if !testing.Short() {
		trials = 60
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			for trial := range trials {
				seed := [2]uint64{uint64(trial) + 1, 0xbeef}
				rng := rand.New(rand.NewPCG(seed[0], seed[1]))
				plan := &auditGamePlan{rs: v.rs, seed: seed}
				g := MustNew(v.rs)
				var states []string
				for range 40 {
					if g.Result().Over() {
						break
					}
					states = append(states, auditState(g))
					ok, err := auditPlayRandomTurn(g, plan, rng, true)
					if err != nil {
						t.Fatalf("%v\n  %s", err, plan.label())
					}
					if !ok {
						states = states[:len(states)-1]
						break
					}
					assertNoDanglingLinks(t, g)
				}
				final := auditState(g)
				for i := len(states) - 1; i >= 0; i-- {
					if err := g.UndoLastMove(); err != nil {
						t.Fatalf("undoing turn %d: %v\n  %s", i+1, err, plan.label())
					}
					assertNoDanglingLinks(t, g)
					if got := auditState(g); got != states[i] {
						t.Fatalf("undoing turn %d did not restore the position\n  %s\n  %s",
							i+1, auditDiff(states[i], got), plan.label())
					}
					for _, pl := range []Player{Vertical, Horizontal} {
						if g.Connected(pl) != floodFillConnected(g, pl) {
							t.Fatalf("after undoing turn %d, %v connectivity disagrees with a flood fill\n  %s",
								i+1, pl, plan.label())
						}
					}
				}
				if got, fresh := auditState(g), auditState(MustNew(v.rs)); got != fresh {
					t.Fatalf("the board is not empty after undoing everything\n  %s\n  %s",
						auditDiff(fresh, got), plan.label())
				}
				redo := MustNew(v.rs)
				for i, turn := range plan.turns {
					if err := turn.apply(redo); err != nil {
						t.Fatalf("replaying turn %d (%s): %v\n  %s", i+1, turn, err, plan.label())
					}
				}
				if got := auditState(redo); got != final {
					t.Fatalf("replaying the same turns gave a different position\n  %s\n  %s",
						auditDiff(final, got), plan.label())
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Connectivity: the incremental rollback path against a full rebuild
// ---------------------------------------------------------------------------

// auditConnectivityAgrees checks the engine's connectivity against two
// independent walks of the link graph.
func auditConnectivityAgrees(t *testing.T, g *Game, where string, plan *auditGamePlan) {
	t.Helper()
	for _, pl := range []Player{Vertical, Horizontal} {
		flood := floodFillConnected(g, pl)
		chain := auditChain(g, pl) != nil
		if got := g.Connected(pl); got != flood || got != chain {
			t.Fatalf("%s: %v connectivity: union-find %v, flood fill %v, chain walk %v\n  %s",
				where, pl, got, flood, chain, plan.label())
		}
	}
}

// TestAuditConnectivityRollbackPath attacks the incremental undo of
// connectivity. A turn that removes nothing is undone by rolling the merge log
// back; a turn that removes something forces a rebuild, which discards the log
// and so must invalidate every earlier mark. The two paths have to agree with
// each other and with an independent flood fill at every step, and the sequences
// below deliberately interleave them.
func TestAuditConnectivityRollbackPath(t *testing.T) {
	rs := withSize(Std, 10)
	rs.PegRemoval = true

	t.Run("hand-built interleaving", func(t *testing.T) {
		// Turns 1-6 place only, turn 7 removes a link, turn 11 lifts a peg.
		// Undoing all the way back makes the rollback path meet marks taken
		// before those rebuilds, which is what the generation guard is for.
		plan := &auditGamePlan{rs: rs}
		g := MustNew(rs)
		script := []auditTurn{
			{peg: at("E6")}, {peg: at("A2")},
			{peg: at("G7")}, {peg: at("A3")},
			{peg: at("F5")}, {peg: at("A4")},
			{removeLinks: []Link{mustLink(t, "E6", "G7")}, peg: at("C6")}, {peg: at("A5")},
			{peg: at("D8")}, {peg: at("A6")},
			{removePegs: []Point{at("F5")}, peg: at("H5")}, {peg: at("A7")},
			{peg: at("E4")}, {peg: at("A8")},
		}
		var states []string
		for i, turn := range script {
			states = append(states, auditState(g))
			plan.turns = append(plan.turns, turn)
			if err := turn.apply(g); err != nil {
				t.Fatalf("turn %d (%s): %v", i+1, turn, err)
			}
			auditConnectivityAgrees(t, g, fmt.Sprintf("after turn %d (%s)", i+1, turn), plan)
			assertNoDanglingLinks(t, g)
			auditPosition(t, g, plan.label)
			if t.Failed() {
				return
			}
		}
		for i := len(states) - 1; i >= 0; i-- {
			if err := g.UndoLastMove(); err != nil {
				t.Fatalf("undoing turn %d: %v", i+1, err)
			}
			auditConnectivityAgrees(t, g, fmt.Sprintf("after undoing turn %d", i+1), plan)
			if got := auditState(g); got != states[i] {
				t.Fatalf("undoing turn %d did not restore the position\n  %s", i+1, auditDiff(states[i], got))
			}
		}
	})

	t.Run("undo, branch, and replay", func(t *testing.T) {
		// Undo part way back, play different turns, undo those, then replay the
		// original ones. Every redo takes a fresh mark while older marks are
		// still in the record.
		plan := &auditGamePlan{rs: rs}
		g := MustNew(rs)
		opening := []auditTurn{
			{peg: at("E6")}, {peg: at("A2")}, {peg: at("G7")}, {peg: at("A3")},
		}
		tail := []auditTurn{
			{removeLinks: []Link{mustLink(t, "E6", "G7")}, peg: at("F5")}, {peg: at("A4")},
		}
		for _, turn := range append(append([]auditTurn{}, opening...), tail...) {
			plan.turns = append(plan.turns, turn)
			if err := turn.apply(g); err != nil {
				t.Fatalf("%s: %v", turn, err)
			}
		}
		mid := auditState(g)
		for range len(tail) {
			if err := g.UndoLastMove(); err != nil {
				t.Fatal(err)
			}
			auditConnectivityAgrees(t, g, "mid-undo", plan)
		}
		branch := auditState(g)
		for _, turn := range []auditTurn{{peg: at("D8")}, {peg: at("A5")}} {
			if err := turn.apply(g); err != nil {
				t.Fatalf("branch %s: %v", turn, err)
			}
			auditConnectivityAgrees(t, g, "on the branch", plan)
		}
		for range 2 {
			if err := g.UndoLastMove(); err != nil {
				t.Fatal(err)
			}
			auditConnectivityAgrees(t, g, "undoing the branch", plan)
		}
		if got := auditState(g); got != branch {
			t.Fatalf("undoing the branch did not restore the branch point\n  %s", auditDiff(branch, got))
		}
		for _, turn := range tail {
			if err := turn.apply(g); err != nil {
				t.Fatalf("replaying %s: %v", turn, err)
			}
			auditConnectivityAgrees(t, g, "replaying the original tail", plan)
		}
		if got := auditState(g); got != mid {
			t.Fatalf("replaying the same turns after a branch gave a different position\n  %s",
				auditDiff(mid, got))
		}
	})

	t.Run("abort between destructive and plain turns", func(t *testing.T) {
		plan := &auditGamePlan{rs: rs}
		g := MustNew(rs)
		for _, turn := range []auditTurn{
			{peg: at("E6")}, {peg: at("A2")}, {peg: at("G7")}, {peg: at("A3")},
		} {
			if err := turn.apply(g); err != nil {
				t.Fatalf("%s: %v", turn, err)
			}
		}
		before := auditState(g)
		// Stage a removal, abort it, and check nothing moved.
		if err := g.RemoveLink(at("E6"), at("G7")); err != nil {
			t.Fatal(err)
		}
		auditConnectivityAgrees(t, g, "after staging a removal", plan)
		g.AbortTurn()
		if got := auditState(g); got != before {
			t.Fatalf("aborting a staged removal did not restore the position\n  %s", auditDiff(before, got))
		}
		auditConnectivityAgrees(t, g, "after aborting a removal", plan)
		// Stage a placement that declines its offered link, then abort.
		if err := g.PlacePeg(at("F5")); err != nil {
			t.Fatal(err)
		}
		if err := g.RemoveLink(at("F5"), at("G7")); err != nil {
			t.Fatalf("declining the offered link: %v", err)
		}
		g.AbortTurn()
		if got := auditState(g); got != before {
			t.Fatalf("aborting a placement with a declined link did not restore the position\n  %s",
				auditDiff(before, got))
		}
		auditConnectivityAgrees(t, g, "after aborting a decline", plan)
		// A plain placement still commits and undoes cleanly afterwards.
		if _, err := g.PlayPeg(at("F5")); err != nil {
			t.Fatal(err)
		}
		auditConnectivityAgrees(t, g, "after a plain placement", plan)
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		if got := auditState(g); got != before {
			t.Fatalf("undoing the plain placement did not restore the position\n  %s", auditDiff(before, got))
		}
		auditConnectivityAgrees(t, g, "after undoing the plain placement", plan)
	})

	t.Run("random interleaving with immediate undo and redo", func(t *testing.T) {
		trials := 4
		if !testing.Short() {
			trials = 150
		}
		for trial := range trials {
			seed := [2]uint64{uint64(trial) + 100, 0x0117}
			rng := rand.New(rand.NewPCG(seed[0], seed[1]))
			plan := &auditGamePlan{rs: rs, seed: seed}
			g := MustNew(rs)
			var states []string
			for range 30 {
				if g.Result().Over() {
					break
				}
				states = append(states, auditState(g))
				ok, err := auditPlayRandomTurn(g, plan, rng, true)
				if err != nil {
					t.Fatalf("%v\n  %s", err, plan.label())
				}
				if !ok {
					states = states[:len(states)-1]
					break
				}
				auditConnectivityAgrees(t, g, "after a committed turn", plan)
				// Undo and redo the same turn immediately: the rollback path and
				// a fresh replay must land on the same position.
				after := auditState(g)
				last := plan.turns[len(plan.turns)-1]
				if err := g.UndoLastMove(); err != nil {
					t.Fatalf("immediate undo: %v\n  %s", err, plan.label())
				}
				auditConnectivityAgrees(t, g, "after an immediate undo", plan)
				if err := last.apply(g); err != nil {
					t.Fatalf("immediate redo of %s: %v\n  %s", last, err, plan.label())
				}
				if got := auditState(g); got != after {
					t.Fatalf("undo then redo of %s changed the position\n  %s\n  %s",
						last, auditDiff(after, got), plan.label())
				}
				auditConnectivityAgrees(t, g, "after an immediate redo", plan)
			}
			for i := len(states) - 1; i >= 0; i-- {
				if err := g.UndoLastMove(); err != nil {
					t.Fatalf("undoing turn %d: %v\n  %s", i+1, err, plan.label())
				}
				auditConnectivityAgrees(t, g, fmt.Sprintf("after undoing turn %d", i+1), plan)
				if got := auditState(g); got != states[i] {
					t.Fatalf("undoing turn %d did not restore the position\n  %s\n  %s",
						i+1, auditDiff(states[i], got), plan.label())
				}
			}
		}
	})
}

func mustLink(t *testing.T, a, b string) Link {
	t.Helper()
	l, ok := NewLink(at(a), at(b))
	if !ok {
		t.Fatalf("%s:%s is not a knight's move", a, b)
	}
	return l
}

// ---------------------------------------------------------------------------
// Peg removal: the invariant a dangling link would break
// ---------------------------------------------------------------------------

// TestAuditPegRemovalLeavesNoDanglingLink asserts the invariant directly rather
// than only through one sequence: after a turn that lifts a peg, and after
// aborting or undoing such a turn, no hole may carry a link bit while holding no
// peg and no link may join two holes of different colours.
func TestAuditPegRemovalLeavesNoDanglingLink(t *testing.T) {
	rs := withSize(Std, 10)
	rs.PegRemoval = true

	// The peg lifted is one the turn's own placement would have linked to, which
	// is the case where the staged bookkeeping of the two overlaps.
	t.Run("lifting a peg the placement would link to", func(t *testing.T) {
		g := MustNew(rs)
		for _, s := range []string{"E6", "A2", "G7", "B4"} {
			if err := g.PlayNotation(s); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
		}
		l := mustLink(t, "E6", "G7")
		if !g.HasLink(l) {
			t.Fatal("expected the link E6:G7")
		}
		before := auditState(g)

		// Rule 4a puts the removal before the placement.
		if err := g.RemovePeg(at("G7")); err != nil {
			t.Fatalf("lifting own peg G7: %v", err)
		}
		assertNoDanglingLinks(t, g)
		if g.HasLink(l) {
			t.Error("the link survived the removal of one of its pegs")
		}
		if err := g.PlacePeg(at("F5")); err != nil {
			t.Fatalf("placing F5: %v", err)
		}
		assertNoDanglingLinks(t, g)

		// Abort must bring back both the peg and its link.
		g.AbortTurn()
		assertNoDanglingLinks(t, g)
		if got := auditState(g); got != before {
			t.Errorf("aborting a peg-removal turn did not restore the position\n  %s", auditDiff(before, got))
		}
		if !g.HasLink(l) {
			t.Error("the link did not come back with the peg")
		}

		// Commit, then undo: same requirement.
		if err := g.RemovePeg(at("G7")); err != nil {
			t.Fatal(err)
		}
		if err := g.PlacePeg(at("F5")); err != nil {
			t.Fatal(err)
		}
		if _, err := g.CommitTurn(); err != nil {
			t.Fatal(err)
		}
		assertNoDanglingLinks(t, g)
		auditPosition(t, g, func() string { return "after lifting G7 and placing F5" })
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		assertNoDanglingLinks(t, g)
		if got := auditState(g); got != before {
			t.Errorf("undoing a peg-removal turn did not restore the position\n  %s", auditDiff(before, got))
		}
	})

	// The printed rules order removals before the placement (rule 4a-b). The
	// engine enforces that, so a removal afterwards must be refused and inert.
	t.Run("removal after the placement is refused and inert", func(t *testing.T) {
		g := MustNew(rs)
		for _, s := range []string{"E6", "A2", "G7", "B4"} {
			if err := g.PlayNotation(s); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
		}
		if err := g.PlacePeg(at("F5")); err != nil {
			t.Fatal(err)
		}
		before := auditState(g)
		if err := g.RemovePeg(at("G7")); err != ErrRemoveAfterPeg {
			t.Errorf("lifting a peg after the turn's peg is down: got %v, want %v", err, ErrRemoveAfterPeg)
		}
		if after := auditState(g); after != before {
			t.Errorf("the refusal changed the position\n  %s", auditDiff(before, after))
		}
		if err := g.RemoveLink(at("E6"), at("G7")); err != ErrRemoveAfterPeg {
			t.Errorf("removing an older link after the peg is down: got %v, want %v", err, ErrRemoveAfterPeg)
		}
		if after := auditState(g); after != before {
			t.Errorf("the refusal changed the position\n  %s", auditDiff(before, after))
		}
		// Withdrawing a link offered this turn is a choice, not a removal, and
		// stays legal at any point in the turn (rule 4c, C5).
		if err := g.RemoveLink(at("F5"), at("G7")); err != nil {
			t.Errorf("withdrawing a link offered this turn: %v", err)
		}
		g.AbortTurn()
		assertNoDanglingLinks(t, g)
	})

	// Random peg-removal games, checked for dangling links after every stage,
	// abort, commit, undo and redo.
	t.Run("random peg-removal games", func(t *testing.T) {
		trials := 3
		if !testing.Short() {
			trials = 80
		}
		for trial := range trials {
			seed := [2]uint64{uint64(trial) + 7, 0xda9e}
			rng := rand.New(rand.NewPCG(seed[0], seed[1]))
			plan := &auditGamePlan{rs: rs, seed: seed}
			g := MustNew(rs)
			for range 30 {
				if g.Result().Over() {
					break
				}
				before := auditState(g)
				// Stage a candidate turn, abort it, then play a real one.
				if turn, ok := auditPlanHead(g, rng, true); ok {
					if err := turn.stage(g); err == nil {
						assertNoDanglingLinks(t, g)
					}
					g.AbortTurn()
					assertNoDanglingLinks(t, g)
					if got := auditState(g); got != before {
						t.Fatalf("aborting %s did not restore the position\n  %s\n  %s",
							turn, auditDiff(before, got), plan.label())
					}
				}
				ok, err := auditPlayRandomTurn(g, plan, rng, true)
				if err != nil {
					t.Fatalf("%v\n  %s", err, plan.label())
				}
				if !ok {
					break
				}
				assertNoDanglingLinks(t, g)
				if err := g.UndoLastMove(); err != nil {
					t.Fatalf("undo: %v\n  %s", err, plan.label())
				}
				assertNoDanglingLinks(t, g)
				if got := auditState(g); got != before {
					t.Fatalf("undoing the last turn did not restore the position\n  %s\n  %s",
						auditDiff(before, got), plan.label())
				}
				if err := plan.turns[len(plan.turns)-1].apply(g); err != nil {
					t.Fatalf("redo: %v\n  %s", err, plan.label())
				}
				assertNoDanglingLinks(t, g)
				auditPosition(t, g, plan.label)
				if t.Failed() {
					return
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Swap
// ---------------------------------------------------------------------------

// TestAuditSwapWindow pins rule 12 and C14-C16: the option exists only in answer
// to the very first peg, and only when the ruleset carries it.
func TestAuditSwapWindow(t *testing.T) {
	t.Run("not before the first peg", func(t *testing.T) {
		g := MustNew(Std)
		if g.CanSwap() {
			t.Error("swap offered on an empty board")
		}
		if err := g.Swap(); err != ErrSwapUnavailable {
			t.Errorf("got %v, want %v", err, ErrSwapUnavailable)
		}
	})
	t.Run("only on the second ply", func(t *testing.T) {
		g := MustNew(Std)
		play(t, g, "D4")
		if !g.CanSwap() {
			t.Fatal("swap should be available to the second player")
		}
		play(t, g, "F6")
		if g.CanSwap() {
			t.Error("swap still offered after the second player answered normally")
		}
		if err := g.Swap(); err != ErrSwapUnavailable {
			t.Errorf("got %v, want %v", err, ErrSwapUnavailable)
		}
	})
	t.Run("refused after a swap", func(t *testing.T) {
		g := MustNew(Std)
		play(t, g, "D4")
		if err := g.Swap(); err != nil {
			t.Fatal(err)
		}
		if g.CanSwap() {
			t.Error("swap offered twice")
		}
		if err := g.Swap(); err != ErrSwapUnavailable {
			t.Errorf("second swap: got %v, want %v", err, ErrSwapUnavailable)
		}
	})
	t.Run("refused mid-turn", func(t *testing.T) {
		g := MustNew(Std)
		play(t, g, "D4")
		if err := g.PlacePeg(at("F6")); err != nil {
			t.Fatal(err)
		}
		if g.CanSwap() {
			t.Error("swap offered while a peg is staged")
		}
		g.AbortTurn()
		if !g.CanSwap() {
			t.Error("aborting the staged turn should hand the swap option back")
		}
	})
	t.Run("absent from the 1962 rules", func(t *testing.T) {
		g := MustNew(Classic3M)
		play(t, g, "D4")
		if g.CanSwap() {
			t.Error("classic rules offered swap (C14: no mention of swap for 3M sets)")
		}
	})
}

// TestAuditSwapSurvivesADrawOffer checks rule 12 against the venue conventions
// layered over it: an entry that consumes no turn cannot consume the swap window
// either.
func TestAuditSwapSurvivesADrawOffer(t *testing.T) {
	t.Run("offer before the opening", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		if err := g.OfferDraw(Horizontal); err != nil {
			t.Fatal(err)
		}
		play(t, g, "D4")
		if !g.CanSwap() {
			t.Error("swap denied on the second ply because a draw offer preceded the opening")
		}
	})
	t.Run("offer after the opening", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4")
		if err := g.OfferDraw(Horizontal); err != nil {
			t.Fatal(err)
		}
		if !g.CanSwap() {
			t.Error("swap denied after the second player offered a draw on their own turn")
		}
		if err := g.Swap(); err != nil {
			t.Errorf("swapping after offering a draw: %v", err)
		}
	})
	t.Run("several offers", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4")
		for range 3 {
			if err := g.OfferDraw(Horizontal); err != nil {
				t.Fatal(err)
			}
		}
		if got := g.Ply(); got != 1 {
			t.Errorf("Ply = %d after one turn and three offers, want 1", got)
		}
		if got := g.Entries(); got != 4 {
			t.Errorf("Entries = %d, want 4", got)
		}
		if !g.CanSwap() {
			t.Error("swap denied after repeated draw offers")
		}
	})
}

// TestAuditSwapReflectionIsInvolutive checks the reflection C16 describes: it
// maps a hole the first player may use onto one the second player may use,
// applying it twice returns the original hole, and undoing a swap is exact. It
// sweeps every legal opening on several board sizes including the boundary ones.
func TestAuditSwapReflectionIsInvolutive(t *testing.T) {
	sizes := []int{MinSize, MinSize + 1, 7, 12, 13, 24}
	if testing.Short() {
		sizes = []int{MinSize, 7}
	}
	for _, size := range sizes {
		rs := withSize(Std, size)
		for _, p := range auditLegalHoles(MustNew(rs), Vertical) {
			mirrored := Point{Col: p.Row, Row: p.Col}
			if err := MustNew(rs).CanPlace(Horizontal, mirrored); err != nil {
				t.Fatalf("size %d: %v reflects to %v, which horizontal may not use: %v", size, p, mirrored, err)
			}
			if back := (Point{Col: mirrored.Row, Row: mirrored.Col}); back != p {
				t.Fatalf("size %d: reflecting %v twice gave %v", size, p, back)
			}
			g := MustNew(rs)
			if _, err := g.PlayPeg(p); err != nil {
				t.Fatalf("size %d: opening at %v: %v", size, p, err)
			}
			before := auditState(g)
			if err := g.Swap(); err != nil {
				t.Fatalf("size %d: swapping after %v: %v", size, p, err)
			}
			if got := g.At(mirrored); got != Horizontal {
				t.Fatalf("size %d: after 1.%v swap, %v holds %v, want horizontal", size, p, mirrored, got)
			}
			if p != mirrored && g.At(p) != NoPlayer {
				t.Fatalf("size %d: after 1.%v swap, %v is still occupied", size, p, p)
			}
			if g.PegCount(Vertical) != 0 || g.PegCount(Horizontal) != 1 {
				t.Fatalf("size %d: after the swap the board holds %d vertical and %d horizontal pegs",
					size, g.PegCount(Vertical), g.PegCount(Horizontal))
			}
			if g.Turn() != Vertical {
				t.Fatalf("size %d: turn after the swap = %v, want vertical", size, g.Turn())
			}
			auditPosition(t, g, func() string { return fmt.Sprintf("size %d, 1.%v swap", size, p) })
			if err := g.UndoLastMove(); err != nil {
				t.Fatalf("size %d, %v: undoing the swap: %v", size, p, err)
			}
			if got := auditState(g); got != before {
				t.Fatalf("size %d: undoing the swap of 1.%v did not restore the position\n  %s",
					size, p, auditDiff(before, got))
			}
			if !g.CanSwap() || g.Swapped() {
				t.Fatalf("size %d: after undoing the swap of 1.%v, CanSwap=%v Swapped=%v",
					size, p, g.CanSwap(), g.Swapped())
			}
			if t.Failed() {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Boundary board sizes
// ---------------------------------------------------------------------------

// TestAuditBoundarySizes covers MinSize, MinSize+1 and odd sizes: the legal hole
// count, that a win is still reachable, and that nothing panics.
func TestAuditBoundarySizes(t *testing.T) {
	for _, size := range []int{MinSize, MinSize + 1, 9, 13} {
		rs := withSize(Std, size)
		g := MustNew(rs)
		want := size * (size - 2)
		for _, pl := range []Player{Vertical, Horizontal} {
			if got := len(g.LegalPlacements(pl)); got != want {
				t.Errorf("size %d, %v: %d legal holes, want %d", size, pl, got, want)
			}
			if got := len(auditLegalHoles(g, pl)); got != want {
				t.Errorf("size %d, %v: the independent scan found %d holes, want %d", size, pl, got, want)
			}
		}
		auditPosition(t, g, func() string { return fmt.Sprintf("empty board, size %d", size) })
		if !auditLadderWins(t, size) {
			t.Errorf("size %d: could not build a winning vertical chain", size)
		}
	}
	// Outside the bounds the ruleset is refused rather than producing a board a
	// knight cannot cross.
	if _, err := New(withSize(Std, MinSize-1)); err == nil {
		t.Errorf("size %d accepted", MinSize-1)
	}
	if _, err := New(withSize(Std, MaxSize+1)); err == nil {
		t.Errorf("size %d accepted", MaxSize+1)
	}
}

// auditLadderWins builds a vertical top-to-bottom chain on a board of the given
// size, answering horizontal in its own left column, and reports whether the
// engine declares the win exactly when the chain closes and not before.
func auditLadderWins(t *testing.T, size int) bool {
	t.Helper()
	g := MustNew(withSize(Std, size))
	col, row := 1, 0
	chain := []Point{{Col: col, Row: row}}
	for row < size-1 {
		if size-1-row >= 2 {
			if col+1 <= size-2 {
				col++
			} else {
				col--
			}
			row += 2
		} else {
			if col+2 <= size-2 {
				col += 2
			} else {
				col -= 2
			}
			row++
		}
		chain = append(chain, Point{Col: col, Row: row})
	}
	spare := auditLegalHoles(g, Horizontal)
	spareIdx := 0
	for i, p := range chain {
		res, err := g.PlayPeg(p)
		if err != nil {
			t.Errorf("size %d: vertical %v: %v", size, p, err)
			return false
		}
		if i == len(chain)-1 {
			if res.Outcome != VerticalWins || res.Reason != Connection {
				t.Errorf("size %d: result after closing the ladder at %v = %+v", size, p, res)
				return false
			}
			break
		}
		if res.Over() {
			t.Errorf("size %d: the game ended early after %v: %+v", size, p, res)
			return false
		}
		for spareIdx < len(spare) && g.At(spare[spareIdx]) != NoPlayer {
			spareIdx++
		}
		if spareIdx >= len(spare) {
			t.Errorf("size %d: ran out of spare horizontal moves", size)
			return false
		}
		if _, err := g.PlayPeg(spare[spareIdx]); err != nil {
			t.Errorf("size %d: horizontal %v: %v", size, spare[spareIdx], err)
			return false
		}
	}
	auditPosition(t, g, func() string { return fmt.Sprintf("ladder win, size %d, chain %v", size, chain) })
	return !t.Failed()
}

// ---------------------------------------------------------------------------
// Notation, adversarially
// ---------------------------------------------------------------------------

// auditHostileNotation collects malformed, absurd and hostile move strings.
func auditHostileNotation() []string {
	long := strings.Repeat("A", 5000)
	digits := strings.Repeat("9", 400)
	return []string{
		"", " ", "\t", "\n", "  \t ", "\u00a0", "\u200b",
		"D", "4", "0", "-", "+", "~", "x", ":", "::", "-:", "?",
		"A0", "A-1", "A+1", "-A1", "+A1", "~A1", "xA1", "A1x", "1B", "B1B", "B1.5",
		"??", "!!", "D4!", "D4?", "D4;D5", "D4,D5", "D4/D5", "D4\\D5",
		"D4 D5", "D4  ", " D4 ", "D4\tD5", "D4\nD5",
		"D4 +", "D4 -", "D4 ~", "D4 x", "D4 +D4", "D4 +D4:D4", "D4 +D4:D5",
		"D4 -A1:B3", "D4 xA1", "D4 xD4", "D4 ~D4:E6 ~D4:E6", "D4 +D4:E6 -D4:E6",
		"D4 zE6", "D4 +E6:D4 +D4:E6", "D4 ++D4:E6", "D4 :", "D4 xE6 xE6",
		"swap swap", "swap D4", "resign now", "RESIGN", "Resign", "draw", "draw!?",
		"draw??", "DRAW?", "draw ?", "d raw?", "swapx",
		"v:", ":resign", "v:resign", "h:resign", "V:RESIGN", "x:resign", "vv:resign",
		"v:draw?", "h:draw!", "v:swap", "v:D4", "v:resign:h", "h:", "v:draw",
		long, long + "1", "A" + digits, "A99999999999999999999",
		"A9223372036854775807", "A9223372036854775808", "A-9223372036854775808",
		"ZZZZZZZZZZZZZZ1", "AAAAAAAAAAAAAA1",
		"☃1", "Ω4", "D４", "Ｄ4", "д4", "D4\x00", "\x00D4", "D4\xff",
		"X1", "AA1", "ZZ99", "A1", "A24", "L1",
	}
}

// TestAuditNotationHostileInput requires that no input panics, and that a
// rejected move leaves the position byte-identical with nothing staged.
func TestAuditNotationHostileInput(t *testing.T) {
	rs := withSize(Std, 12)
	rs.PegRemoval = true
	for _, input := range auditHostileNotation() {
		for _, mid := range []bool{false, true} {
			g := MustNew(rs)
			if mid {
				for _, s := range []string{"E6", "A2", "G7", "B4", "D4", "A6"} {
					if err := g.PlayNotation(s); err != nil {
						t.Fatalf("building the mid-game position at %q: %v", s, err)
					}
				}
			}
			before := auditState(g)
			err := auditPlayNotation(t, g, input)
			after := auditState(g)
			if err != nil {
				if after != before {
					t.Errorf("rejected move %q changed the position (mid=%v)\n  %s",
						input, mid, auditDiff(before, after))
				}
				if s := g.Staged(); s.PegPlaced || len(s.Added) > 0 || len(s.Removed) > 0 ||
					len(s.RemovedPegs) > 0 || len(s.PegLinks) > 0 {
					t.Errorf("rejected move %q left staged edits: %+v", input, s)
				}
				continue
			}
			if after == before {
				t.Errorf("accepted move %q changed nothing", input)
			}
			auditPosition(t, g, func() string { return fmt.Sprintf("after accepting %q (mid=%v)", input, mid) })
			if t.Failed() {
				return
			}
		}
	}
}

// auditPlayNotation calls PlayNotation and turns a panic into a failure naming
// the input, since the requirement is that no input panics.
func auditPlayNotation(t *testing.T, g *Game, input string) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PlayNotation(%q) panicked: %v", input, r)
			err = fmt.Errorf("panic")
		}
	}()
	return g.PlayNotation(input)
}

// TestAuditNotationRandomFuzz throws random strings built from the notation
// alphabet at the parsers and at PlayNotation.
func TestAuditNotationRandomFuzz(t *testing.T) {
	const seedA, seedB = 0x7717, 0x4242
	rng := rand.New(rand.NewPCG(seedA, seedB))
	alphabet := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 \t:-+~xvh?!;,.\u00a0☃Ω\x00")
	cases := 4000
	if !testing.Short() {
		cases = 200000
	}
	rs := withSize(Std, 10)
	for i := range cases {
		n := 1 + rng.IntN(12)
		var b strings.Builder
		for range n {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		s := b.String()

		auditParsePoint(t, s, i)
		auditParseLink(t, s, i)

		g := MustNew(rs)
		before := auditState(g)
		err := auditPlayNotation(t, g, s)
		after := auditState(g)
		if err != nil && after != before {
			t.Fatalf("seed {%d,%d} case %d: rejected %q changed the position\n  %s",
				seedA, seedB, i, s, auditDiff(before, after))
		}
		if err == nil {
			auditPosition(t, g, func() string {
				return fmt.Sprintf("seed {%d,%d} case %d accepted %q", seedA, seedB, i, s)
			})
		}
		if t.Failed() {
			return
		}
	}
	// Long inputs get their own pass: the parsers must not choke on size.
	for _, n := range []int{100, 1000, 100000} {
		for _, body := range []string{"A", "9", ":", "~", "x", "-", "v"} {
			s := strings.Repeat(body, n)
			auditParsePoint(t, s, -n)
			auditParseLink(t, s, -n)
			g := MustNew(rs)
			before := auditState(g)
			if err := auditPlayNotation(t, g, s); err != nil {
				if after := auditState(g); after != before {
					t.Fatalf("rejected a %d-rune input of %q and still changed the position", n, body)
				}
			}
		}
	}
}

func auditParsePoint(t *testing.T, s string, i int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("case %d: ParsePoint(%q) panicked: %v", i, s, r)
		}
	}()
	p, err := ParsePoint(s)
	if err != nil {
		return
	}
	if p.Row < 0 || p.Col < 0 {
		t.Fatalf("ParsePoint(%q) returned %v, which is off any board", s, p)
	}
	// A parsed hole must be exactly the hole its own name denotes.
	if back, err := ParsePoint(p.String()); err != nil || back != p {
		t.Fatalf("ParsePoint(%q) = %v, whose name %q parses back to %v (%v)", s, p, p.String(), back, err)
	}
}

func auditParseLink(t *testing.T, s string, i int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("case %d: ParseLink(%q) panicked: %v", i, s, r)
		}
	}()
	l, err := ParseLink(s)
	if err != nil {
		return
	}
	if !l.Dir.IsCanonical() || l.Canonical() != l {
		t.Fatalf("ParseLink(%q) = %v, which is not in canonical form", s, l)
	}
	from, to := l.Ends()
	dCol, dRow := to.Col-from.Col, to.Row-from.Row
	if !((abs(dCol) == 1 && abs(dRow) == 2) || (abs(dCol) == 2 && abs(dRow) == 1)) {
		t.Fatalf("ParseLink(%q) = %v, whose endpoints are (%d,%d) apart", s, l, dCol, dRow)
	}
}

// auditBigColumnName renders n in the bijective base-26 numbering ColumnName and
// ParseColumn implement, in arbitrary precision so the name can be longer than
// an int can hold.
func auditBigColumnName(n *big.Int) string {
	twentySix := big.NewInt(26)
	one := big.NewInt(1)
	cur := new(big.Int).Set(n)
	rem := new(big.Int)
	var out []byte
	for cur.Sign() > 0 {
		cur.Sub(cur, one)
		cur.QuoRem(cur, twentySix, rem)
		out = append(out, byte('A'+rem.Int64()))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// auditBigColumnValue computes the exact zero-based index a column name denotes.
func auditBigColumnValue(name string) *big.Int {
	v := new(big.Int)
	twentySix := big.NewInt(26)
	d := new(big.Int)
	for _, r := range name {
		v.Mul(v, twentySix)
		d.SetInt64(int64(r-'A') + 1)
		v.Add(v, d)
	}
	return v.Sub(v, big.NewInt(1))
}

// TestAuditParseColumnDoesNotOverflow requires that a column name is either
// rejected or decoded to the index it actually denotes. Notation is how saved
// games and a networked opponent's moves arrive (C18), so a hostile name that
// wraps onto a real hole is a live hazard, not a curiosity.
func TestAuditParseColumnDoesNotOverflow(t *testing.T) {
	for col := range MaxSize {
		name := ColumnName(col)
		got, err := ParseColumn(name)
		if err != nil || got != col {
			t.Fatalf("ParseColumn(%q) = %d, %v, want %d", name, got, err, col)
		}
		if _, err := ParsePoint(name + "1"); err != nil {
			t.Fatalf("ParsePoint(%q) rejected a hole inside the largest legal board: %v", name+"1", err)
		}
	}
	// A name whose true value is 2^64 would wrap to column A in int arithmetic.
	wrap := new(big.Int).Lsh(big.NewInt(1), 64)
	witness := auditBigColumnName(new(big.Int).Add(wrap, big.NewInt(1)))
	want := auditBigColumnValue(witness)
	if got, err := ParseColumn(witness); err == nil {
		if want.Cmp(big.NewInt(int64(got))) != 0 {
			t.Errorf("ParseColumn(%q) = %d but the name denotes column %s: the index overflowed silently",
				witness, got, want)
		}
	}
	g := MustNew(withSize(Std, 12))
	hostile := witness + "5"
	if err := g.PlayNotation(hostile); err == nil {
		t.Errorf("PlayNotation(%q) placed a peg at %v: a %d-letter column name reached a real hole",
			hostile, g.History()[0].Peg, len(witness))
	}
	// Distinct names must not denote the same column.
	seen := map[int]string{}
	for _, n := range []int{1, 2, 3, 13, 60, 64, 65, 70, 100} {
		name := strings.Repeat("A", n)
		col, err := ParseColumn(name)
		if err != nil {
			continue
		}
		if prev, ok := seen[col]; ok {
			t.Errorf("ParseColumn(%q) and ParseColumn(%q) both give column %d", prev, name, col)
		}
		seen[col] = name
	}
}

// ---------------------------------------------------------------------------
// Transcripts
// ---------------------------------------------------------------------------

// TestAuditTranscriptRoundTrip requires that a game's own transcript replays to
// the same position and the same result, across every ruleset combination.
func TestAuditTranscriptRoundTrip(t *testing.T) {
	sizes := []int{6, 9, 12}
	per := 2
	if testing.Short() {
		sizes = []int{6}
	} else {
		per = 8
	}
	for i, rs := range auditRulesets(sizes) {
		for trial := range per {
			seed := [2]uint64{uint64(i)*31 + uint64(trial), 0xfeed}
			rng := rand.New(rand.NewPCG(seed[0], seed[1]))
			plan := &auditGamePlan{rs: rs, seed: seed}
			g := MustNew(rs)
			for range 30 {
				if g.Result().Over() {
					break
				}
				ok, err := auditPlayRandomTurn(g, plan, rng, true)
				if err != nil {
					t.Fatalf("%v\n  %s", err, plan.label())
				}
				if !ok {
					break
				}
			}
			transcript, err := g.Transcript()
			if err != nil {
				t.Fatalf("Transcript: %v\n  %s", err, plan.label())
			}
			replayed, err := ReplayTranscript(rs, transcript)
			if err != nil {
				t.Errorf("a game's own transcript does not replay: %v\n  transcript: %s\n  %s",
					err, transcript, plan.label())
				continue
			}
			if got, want := auditState(replayed), auditState(g); got != want {
				t.Errorf("replay diverged\n  %s\n  transcript: %s\n  %s",
					auditDiff(want, got), transcript, plan.label())
			}
			if replayed.Result() != g.Result() {
				t.Errorf("replay result %+v, original %+v\n  transcript: %s",
					replayed.Result(), g.Result(), transcript)
			}
			if t.Failed() {
				return
			}
		}
	}
}

// TestAuditTranscriptRecordsNonTurnEntries covers the part of a record that is
// not about the board. A resignation is normally made out of turn - a player
// concedes while the opponent is thinking - and the record has to survive it
// with the same result on replay, for both sides and for a draw offer too.
func TestAuditTranscriptRecordsNonTurnEntries(t *testing.T) {
	rs := withSize(Std, 10)
	cases := []struct {
		name    string
		build   func(g *Game) error
		outcome Outcome
		reason  Reason
	}{
		{"vertical resigns on its own turn", func(g *Game) error {
			auditPlay(g, "D4", "A6")
			return g.Resign(Vertical)
		}, HorizontalWins, Resignation},
		{"vertical resigns out of turn", func(g *Game) error {
			auditPlay(g, "D4")
			return g.Resign(Vertical)
		}, HorizontalWins, Resignation},
		{"horizontal resigns out of turn", func(g *Game) error {
			auditPlay(g, "D4", "A6")
			return g.Resign(Horizontal)
		}, VerticalWins, Resignation},
		{"horizontal resigns on its own turn", func(g *Game) error {
			auditPlay(g, "D4")
			return g.Resign(Horizontal)
		}, VerticalWins, Resignation},
		{"draw offered out of turn and accepted", func(g *Game) error {
			auditPlay(g, "D4", "A6")
			if err := g.OfferDraw(Horizontal); err != nil {
				return err
			}
			return g.AcceptDraw(Vertical)
		}, Draw, Agreement},
		{"draw offered on turn and accepted", func(g *Game) error {
			auditPlay(g, "D4", "A6")
			if err := g.OfferDraw(Vertical); err != nil {
				return err
			}
			return g.AcceptDraw(Horizontal)
		}, Draw, Agreement},
		{"offer left standing, then a move, then a resignation", func(g *Game) error {
			auditPlay(g, "D4")
			if err := g.OfferDraw(Vertical); err != nil {
				return err
			}
			auditPlay(g, "A6")
			return g.Resign(Horizontal)
		}, VerticalWins, Resignation},
		{"swap after an offer, then a resignation", func(g *Game) error {
			auditPlay(g, "B4")
			if err := g.OfferDraw(Horizontal); err != nil {
				return err
			}
			if err := g.Swap(); err != nil {
				return err
			}
			return g.Resign(Vertical)
		}, HorizontalWins, Resignation},
	}
	for _, c := range cases {
		g := MustNew(rs)
		if err := c.build(g); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := g.Result(); got.Outcome != c.outcome || got.Reason != c.reason {
			t.Fatalf("%s: result = %+v, want outcome %v reason %v", c.name, got, c.outcome, c.reason)
		}
		transcript, err := g.Transcript()
		if err != nil {
			t.Fatalf("%s: Transcript: %v", c.name, err)
		}
		replayed, err := ReplayTranscript(rs, transcript)
		if err != nil {
			t.Errorf("%s: replaying %q: %v", c.name, transcript, err)
			continue
		}
		if got := replayed.Result(); got != g.Result() {
			t.Errorf("%s: transcript %q replays to %+v, the game ended %+v",
				c.name, transcript, got, g.Result())
		}
		if got, want := auditState(replayed), auditState(g); got != want {
			t.Errorf("%s: transcript %q replays to a different position\n  %s",
				c.name, transcript, auditDiff(want, got))
		}
		// The record itself, entry by entry, must match.
		if len(replayed.History()) != len(g.History()) {
			t.Errorf("%s: the replayed record has %d entries, the original %d",
				c.name, len(replayed.History()), len(g.History()))
			continue
		}
		for i, want := range g.History() {
			if got := replayed.History()[i]; got.Kind != want.Kind || got.Player != want.Player {
				t.Errorf("%s: entry %d replayed as %v by %v, recorded as %v by %v (transcript %q)",
					c.name, i+1, got.Kind, got.Player, want.Kind, want.Player, transcript)
			}
		}
	}
}

// auditPlay plays ordinary placements inside a table fixture, where a refusal
// means the fixture is wrong rather than the engine.
func auditPlay(g *Game, holes ...string) {
	for _, h := range holes {
		if _, err := g.PlayPeg(at(h)); err != nil {
			panic(fmt.Sprintf("fixture move %s: %v", h, err))
		}
	}
}

// TestAuditTranscriptAloneCannotDetectTampering states the boundary between the
// two record formats and asserts both halves of it, so the test fails if either
// half stops being true.
//
// A transcript is a list of moves and nothing else: a human-readable projection
// of a game. A mutation that stays individually legal is therefore a valid
// record of a different game, and ReplayTranscript cannot distinguish it — drop
// an entry, substitute a hole or strip a declined-link annotation and the result
// replays cleanly to something else. Requiring a projection to detect its own
// truncation is a category error; the property that matters, that a record
// arriving from elsewhere cannot be altered or truncated without being caught,
// belongs to the artefact that is saved and sent, which is Record.
//
// This test pins both sides of that boundary. Every mutation that slips past the
// bare transcript is asserted to be refused once wrapped in a Record, with the
// digest left stale and again with it honestly recomputed. So it fails if
// someone later believes the bare format is self-validating, and it fails if the
// Record wrapper regresses.
//
// Three further checks separate the causes. Records that cannot describe any
// game at all must be refused even by the bare replay. Whatever a mutation does
// produce must obey the full invariant set. And every accepted record must
// re-record to a transcript replaying to the identical position, which is what
// rules out the engine mis-reading a record it accepted.
//
// History: the original requirement was stated over transcripts and this test
// was red against it. The requirement was retired by its author once Record
// existed, deliberately and on the record; see .work/reports/EngineQA.md.
func TestAuditTranscriptAloneCannotDetectTampering(t *testing.T) {
	rs := withSize(Std, 10)
	base := []string{"D4", "D6", "E6 ~D4:E6", "E4", "C6", "G4", "B4 -D4:C6", "H6", "v:draw?", "F8"}
	g := MustNew(rs)
	for _, s := range base {
		if err := g.PlayNotation(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	transcript, err := g.Transcript()
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	original, err := ReplayTranscript(rs, transcript)
	if err != nil {
		t.Fatalf("replaying the untampered transcript: %v", err)
	}
	want := auditState(original)
	if want != auditState(g) {
		t.Fatalf("the untampered transcript does not reproduce its own game\n  %s",
			auditDiff(auditState(g), want))
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	encoded := rec.Encode()

	moves := strings.Split(transcript, "; ")

	// Records that cannot describe any game must be refused outright.
	type tampered struct{ name, text string }
	mustReject := []tampered{
		{"the opening hole played twice", "D4; D4"},
		{"a hole reused later", "D4; D6; D6"},
		{"a peg in the opponent's border row", "A4"},
		{"a decline that is not a knight's move", "D4; D6; E6 ~D4:E7"},
		{"a decline of a link that was never offered", "D4; D6; E6 ~E6:G7"},
		{"a removal of a link that is not on the board", "D4; D6; E6 -D6:E4"},
		{"a peg lifted under a ruleset that forbids it", "D4; D6; E6 xD4"},
		{"a hole off the board", "D4; ZZ99"},
		{"an unknown edit", "D4; D6 !E4"},
		{"a swap outside its window", "D4; D6; swap"},
	}
	for i, m := range moves {
		if strings.HasPrefix(m, "v:") || strings.HasPrefix(m, "h:") || m == tokenSwap {
			continue // not a turn, so duplicating it names no hole
		}
		p := append([]string(nil), moves[:i+1:i+1]...)
		p = append(p, m)
		p = append(p, moves[i+1:]...)
		mustReject = append(mustReject, tampered{
			fmt.Sprintf("turn %d duplicated", i+1), strings.Join(p, "; "),
		})
	}
	for _, m := range mustReject {
		if _, err := ReplayTranscript(rs, m.text); err == nil {
			t.Errorf("%s: %q was accepted", m.name, m.text)
		}
	}

	// Every mutation from here on leaves a record that still parses. None of
	// them may pass as the original game.
	var mutations []tampered
	add := func(name string, parts []string) {
		mutations = append(mutations, tampered{name, strings.Join(parts, "; ")})
	}
	for i := 0; i+1 < len(moves); i++ {
		p := append([]string(nil), moves...)
		p[i], p[i+1] = p[i+1], p[i]
		add(fmt.Sprintf("swap entries %d and %d", i+1, i+2), p)
	}
	for i := range moves {
		p := append([]string(nil), moves...)
		p[i] = "F2"
		add(fmt.Sprintf("replace entry %d with a free hole", i+1), p)

		p2 := append([]string(nil), moves[:i:i]...)
		p2 = append(p2, moves[i+1:]...)
		add(fmt.Sprintf("drop entry %d", i+1), p2)
	}
	// Stripping a declined-link annotation is the mutation that would silently
	// re-create a link the player deliberately omitted (rule 4c, C5).
	for i, m := range moves {
		if strings.Contains(m, "~") {
			p := append([]string(nil), moves...)
			p[i] = strings.Fields(m)[0]
			add(fmt.Sprintf("strip the declined link from entry %d", i+1), p)
		}
		if strings.Contains(m, " -") {
			p := append([]string(nil), moves...)
			p[i] = strings.Fields(m)[0]
			add(fmt.Sprintf("strip the removal from entry %d", i+1), p)
		}
		if strings.Contains(m, ":draw?") || strings.Contains(m, ":resign") {
			bare := strings.TrimPrefix(strings.TrimPrefix(m, "v:"), "h:")
			p := append([]string(nil), moves...)
			p[i] = bare
			add(fmt.Sprintf("strip the side from entry %d", i+1), p)
			p2 := append([]string(nil), moves...)
			p2[i] = "h:" + bare
			add(fmt.Sprintf("change the side of entry %d", i+1), p2)
		}
	}
	if len(mutations) < 20 {
		t.Fatalf("only %d mutations generated", len(mutations))
	}

	accepted := 0
	var diverged []string
	for _, m := range mutations {
		replayed, err := ReplayTranscript(rs, m.text)
		if err != nil {
			continue
		}
		accepted++
		if got := auditState(replayed); got != want {
			diverged = append(diverged, m.name)
			// The same mutation wrapped in a Record must be refused, both with
			// its digest left stale and with the digest honestly recomputed so
			// that the record is internally consistent again.
			stale := strings.Replace(encoded, "moves "+rec.Moves, "moves "+m.text, 1)
			if stale == encoded {
				t.Errorf("%s: could not splice the mutated moves into a record", m.name)
			} else if _, err := DecodeRecord(stale); err == nil {
				t.Errorf("%s: a Record whose moves were edited without touching its digest was accepted",
					m.name)
			}
			forged := rec
			forged.Moves = m.text
			forged.Digest = forged.digest()
			if _, err := forged.Replay(); err == nil {
				t.Errorf("%s: a Record passing off a different game as this one was accepted\n  moves: %s",
					m.name, m.text)
			}
		}
		// Whatever it produced must still be a lawful position, and re-recording
		// it must describe the same game.
		auditPosition(t, replayed, func() string {
			return fmt.Sprintf("tampered transcript (%s): %s", m.name, m.text)
		})
		again, err := replayed.Transcript()
		if err != nil {
			t.Errorf("%s: an accepted record cannot be re-recorded: %v\n  tampered: %s",
				m.name, err, m.text)
			continue
		}
		twice, err := ReplayTranscript(rs, again)
		if err != nil {
			t.Errorf("%s: the re-recorded transcript %q does not replay: %v", m.name, again, err)
			continue
		}
		if a, b := auditState(replayed), auditState(twice); a != b {
			t.Errorf("%s: re-recording changed the game the record describes\n  tampered: %s\n  re-recorded: %s\n  %s",
				m.name, m.text, again, auditDiff(a, b))
		}
	}
	t.Logf("%d mutations, %d replayed without error, %d of those became a different game "+
		"and were caught only once wrapped in a Record",
		len(mutations), accepted, len(diverged))
	t.Logf("undetected by the bare transcript: %s", strings.Join(diverged, "; "))
	// Both halves of the boundary.
	if len(diverged) == 0 {
		t.Error("no mutation slipped past the bare transcript, so the gap this test documents no longer exists: revisit it")
	}
	if accepted == len(mutations) {
		t.Error("the bare transcript accepted every mutation, so it is not checking legality at all")
	}

	// The annotations carry the choices, and stripping one must change the game
	// the record describes. An unmade link is no barrier (rule 4c, C5), so a
	// record that cannot tell a declined link from a made one is worthless.
	declined := mustLink(t, "D4", "E6")
	if original.HasLink(declined) {
		t.Error("the recorded game declined D4:E6, yet its replay has the link")
	}
	stripped := strings.Replace(transcript, " ~"+declined.String(), "", 1)
	if stripped == transcript {
		t.Fatalf("no declined-link annotation to strip in %q", transcript)
	}
	if s, err := ReplayTranscript(rs, stripped); err != nil {
		t.Errorf("replaying the record without the decline: %v", err)
	} else if !s.HasLink(declined) {
		t.Error("stripping the declined-link annotation left the link off the board, so the annotation is not what carries the choice")
	}
	removedLink := mustLink(t, "C6", "D4")
	if original.HasLink(removedLink) {
		t.Error("the recorded game removed C6:D4, yet its replay has the link")
	}
	withoutRemoval := strings.Replace(transcript, " -"+removedLink.String(), "", 1)
	if withoutRemoval == transcript {
		t.Fatalf("no removal annotation to strip in %q", transcript)
	}
	if s, err := ReplayTranscript(rs, withoutRemoval); err != nil {
		t.Errorf("replaying the record without the removal: %v", err)
	} else if !s.HasLink(removedLink) {
		t.Error("stripping the removal annotation still took the link off the board")
	}
	// And the side tag decides who made an entry that is not a turn.
	offerAt := -1
	for i, m := range original.History() {
		if m.Kind != DrawOfferMove {
			continue
		}
		offerAt = i
		if m.Player != Vertical {
			t.Errorf("the record says vertical offered the draw, replay says %v", m.Player)
		}
	}
	if offerAt < 0 {
		t.Fatal("the replayed record has no draw offer")
	}
	flipped := strings.Replace(transcript, "v:"+tokenDrawOffer, "h:"+tokenDrawOffer, 1)
	if flipped == transcript {
		t.Fatalf("no side-tagged offer to flip in %q", transcript)
	}
	if s, err := ReplayTranscript(rs, flipped); err != nil {
		t.Errorf("replaying the flipped offer: %v", err)
	} else if got := s.History()[offerAt]; got.Player != Horizontal {
		t.Errorf("flipping the offer's side gave an entry by %v, want horizontal", got.Player)
	}
}

// TestAuditDrawOfferLifetime pins how long an offer stands, which no source
// specifies at all (C13, C19: the propose/accept vocabulary is a venue
// convention). The engine's rule has two halves, and both matter: an offer is
// answered, and so cleared, when the side it was made to plays a move instead
// of accepting, which stops a player banking an acceptance to cash in twenty
// turns later; and an offer survives its own maker's move, so that offering a
// draw along with your move works the way every venue lets it.
func TestAuditDrawOfferLifetime(t *testing.T) {
	g := MustNew(withSize(Std, 10))
	play(t, g, "D4", "A6")
	if err := g.OfferDraw(Horizontal); err != nil {
		t.Fatal(err)
	}
	if g.DrawOfferedBy() != Horizontal {
		t.Fatalf("DrawOfferedBy = %v right after the offer", g.DrawOfferedBy())
	}
	// Vertical answers with a move instead of accepting.
	if _, err := g.PlayPeg(at("F6")); err != nil {
		t.Fatal(err)
	}
	if got := g.DrawOfferedBy(); got != NoPlayer {
		t.Errorf("the offer still stands as %v after the side to move played instead of accepting", got)
	}
	if err := g.AcceptDraw(Vertical); err != ErrNoDrawOffer {
		t.Errorf("accepting a draw a turn after the offer: got %v, want %v", err, ErrNoDrawOffer)
	}
	if g.Result().Over() {
		t.Errorf("the game was drawn by a stale offer: %+v", g.Result())
	}
}

// TestAuditDrawOfferSurvivesItsMakersMove is the other half of the rule above.
func TestAuditDrawOfferSurvivesItsMakersMove(t *testing.T) {
	g := MustNew(withSize(Std, 10))
	play(t, g, "D4", "A6")
	// Vertical is to move, and offers a draw along with its own move.
	if err := g.OfferDraw(Vertical); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PlayPeg(at("F6")); err != nil {
		t.Fatal(err)
	}
	if got := g.DrawOfferedBy(); got != Vertical {
		t.Errorf("DrawOfferedBy = %v after the offerer played its own move; the offer should stand for the opponent to answer", got)
	}
	// The opponent can now accept it, and only the opponent can.
	if err := g.AcceptDraw(Vertical); err != ErrNoDrawOffer {
		t.Errorf("the offerer accepting its own offer: got %v, want %v", err, ErrNoDrawOffer)
	}
	if err := g.AcceptDraw(Horizontal); err != nil {
		t.Fatalf("the opponent accepting the standing offer: %v", err)
	}
	if got := g.Result(); got.Outcome != Draw || got.Reason != Agreement {
		t.Errorf("result = %+v, want a draw by agreement", got)
	}
}

// TestAuditTranscriptUnderPegRemoval covers the one turn shape no preset uses: a
// turn that lifts one of the player's own pegs (RD7). The resulting game must
// still be recordable and replayable, or a saved game is lost.
func TestAuditTranscriptUnderPegRemoval(t *testing.T) {
	for _, linkRemoval := range []bool{true, false} {
		rs := withSize(Std, 10)
		rs.PegRemoval = true
		rs.LinkRemoval = linkRemoval
		if err := rs.Validate(); err != nil {
			t.Fatalf("ruleset rejected: %v", err)
		}
		name := fmt.Sprintf("linkRemoval=%v", linkRemoval)
		g := MustNew(rs)
		for _, s := range []string{"E6", "A2", "G7", "B4"} {
			if err := g.PlayNotation(s); err != nil {
				t.Fatalf("%s: %q: %v", name, s, err)
			}
		}
		if !g.HasLink(mustLink(t, "E6", "G7")) {
			t.Fatalf("%s: expected the link E6:G7", name)
		}
		if err := g.RemovePeg(at("G7")); err != nil {
			t.Fatalf("%s: lifting own peg: %v", name, err)
		}
		if err := g.PlacePeg(at("F4")); err != nil {
			t.Fatalf("%s: placing F4: %v", name, err)
		}
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("%s: commit: %v", name, err)
		}
		if g.At(at("G7")) != NoPlayer {
			t.Errorf("%s: the peg is still on the board", name)
		}
		assertNoDanglingLinks(t, g)
		auditPosition(t, g, func() string { return name })
		transcript, err := g.Transcript()
		if err != nil {
			t.Errorf("%s: Transcript: %v", name, err)
			continue
		}
		replayed, err := ReplayTranscript(rs, transcript)
		if err != nil {
			t.Errorf("%s: the game's own transcript %q does not replay: %v", name, transcript, err)
			continue
		}
		if got, want := auditState(replayed), auditState(g); got != want {
			t.Errorf("%s: replay of %q diverged\n  %s", name, transcript, auditDiff(want, got))
		}
	}
}

// ---------------------------------------------------------------------------
// Clone independence
// ---------------------------------------------------------------------------

// TestAuditCloneIndependence mutates clones differently and asserts nothing is
// shared: pegs, links, the record, connectivity or staged edits.
func TestAuditCloneIndependence(t *testing.T) {
	rs := withSize(Std, 10)
	rs.PegRemoval = true
	base := MustNew(rs)
	for _, s := range []string{"E6", "A2", "G7", "B4", "D4", "A6"} {
		if err := base.PlayNotation(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	want := auditState(base)

	a, b := base.Clone(), base.Clone()
	if auditState(a) != want || auditState(b) != want {
		t.Fatal("a fresh clone differs from its original")
	}

	// Diverge: one clone builds, the other tears down.
	if err := a.PlayNotation("F5"); err != nil {
		t.Fatalf("clone a: %v", err)
	}
	if err := b.RemoveLink(at("E6"), at("G7")); err != nil {
		t.Fatalf("clone b: %v", err)
	}
	if err := b.RemovePeg(at("G7")); err != nil {
		t.Fatalf("clone b: %v", err)
	}
	if err := b.PlacePeg(at("C2")); err != nil {
		t.Fatalf("clone b: %v", err)
	}
	if _, err := b.CommitTurn(); err != nil {
		t.Fatalf("clone b: %v", err)
	}
	if got := auditState(base); got != want {
		t.Errorf("the original changed while its clones were mutated\n  %s", auditDiff(want, got))
	}
	if auditState(a) == auditState(b) {
		t.Error("two clones mutated differently ended up identical")
	}
	for _, g := range []*Game{a, b, base} {
		for _, pl := range []Player{Vertical, Horizontal} {
			if g.Connected(pl) != floodFillConnected(g, pl) {
				t.Errorf("connectivity disagrees with a flood fill for %v", pl)
			}
		}
		assertNoDanglingLinks(t, g)
	}
	if err := a.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	if got := auditState(a); got != want {
		t.Errorf("undo on the clone did not return it to the shared position\n  %s", auditDiff(want, got))
	}
	if got := auditState(base); got != want {
		t.Errorf("undo on a clone changed the original\n  %s", auditDiff(want, got))
	}

	// A clone taken mid-turn carries the staged edits without sharing them.
	mid := base.Clone()
	if err := mid.PlacePeg(at("F5")); err != nil {
		t.Fatal(err)
	}
	staged := mid.Clone()
	if auditState(staged) != auditState(mid) {
		t.Error("cloning mid-turn lost the staged edits")
	}
	if err := staged.RemoveLink(at("F5"), at("D4")); err != nil {
		t.Fatalf("declining on the clone: %v", err)
	}
	if !mid.HasLink(mustLink(t, "F5", "D4")) {
		t.Error("declining a link on the clone removed it from the original")
	}
	staged.AbortTurn()
	if got := auditState(staged); got != want {
		t.Errorf("aborting on the clone did not restore its own position\n  %s", auditDiff(want, got))
	}
	if !mid.Staged().PegPlaced {
		t.Error("aborting on the clone cleared the original's staged turn")
	}

	// The record must not alias: undoing on one clone and playing something else
	// must not rewrite another clone's entries.
	c, d := base.Clone(), base.Clone()
	if err := c.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	if err := c.PlayNotation("H5"); err != nil {
		t.Fatalf("clone c: %v", err)
	}
	dh := d.History()
	if len(dh) == 0 {
		t.Fatal("clone d has no record")
	}
	if got := dh[len(dh)-1].Peg; got != at("A6") {
		t.Errorf("clone d's last entry became %v after clone c diverged, want A6", got)
	}
}

// TestAuditCloneHistoryDoesNotAlias requires that a clone's record shares no
// storage with its original.
//
// Game.History documents that the returned slice must not be modified, and the
// engine never rewrites a committed entry itself, so the outer slice is safe.
// The per-entry slices are a different matter: they are exported fields on a
// value the method hands out, Clone copies the []Move and therefore the entry
// structs, but Added, Removed, PegLinks and RemovedPegs keep pointing at the
// original's backing arrays. A caller that transforms a record rather than only
// reading it - serialising a game for a remote opponent, trimming one for the
// leaderboard, rewriting coordinates - writes through those fields on what it
// believes is an independent copy and silently edits the other game too. A clone
// is supposed to be independent, so this is asserted regardless of whether a
// caller ought to be writing.
func TestAuditCloneHistoryDoesNotAlias(t *testing.T) {
	rs := withSize(Std, 10)
	rs.PegRemoval = true
	// One turn that fills every list an entry can carry: a deliberate removal, a
	// lifted peg that takes a link of its own with it, and a hand-added link. A
	// fresh fixture is built per case, because a poke that does alias corrupts
	// the record it reaches into and must not leak into the next check.
	build := func() *Game {
		t.Helper()
		g := MustNew(rs)
		for _, s := range []string{"E6", "A2", "G7", "B4", "D4", "A6", "B3 ~B3:D4", "A7"} {
			if err := g.PlayNotation(s); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
		}
		if err := g.RemoveLink(at("D4"), at("E6")); err != nil {
			t.Fatalf("removing an older link: %v", err)
		}
		if err := g.RemovePeg(at("G7")); err != nil {
			t.Fatalf("lifting own peg: %v", err)
		}
		if err := g.PlacePeg(at("C2")); err != nil {
			t.Fatalf("placing C2: %v", err)
		}
		if err := g.AddLink(at("B3"), at("D4")); err != nil {
			t.Fatalf("adding B3:D4: %v", err)
		}
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		entry := g.History()[g.Entries()-1]
		if len(entry.Removed) == 0 || len(entry.RemovedPegs) == 0 ||
			len(entry.PegLinks) == 0 || len(entry.Added) == 0 {
			t.Fatalf("the fixture entry does not carry every list: %+v", entry)
		}
		return g
	}

	// Every list is checked separately, so a failure names exactly which ones
	// share storage.
	cases := []struct {
		name string
		poke func(m *Move)
		read func(m Move) any
	}{
		// The written values name the corner hole A1, which no peg can occupy and
		// no link can start from, so a poke can never coincide with a real entry.
		{"Removed", func(m *Move) { m.Removed[0] = Link{From: Point{}, Dir: NNE} },
			func(m Move) any { return m.Removed[0] }},
		{"Added", func(m *Move) { m.Added[0] = Link{From: Point{}, Dir: ENE} },
			func(m Move) any { return m.Added[0] }},
		{"PegLinks", func(m *Move) { m.PegLinks[0] = Link{From: Point{}, Dir: ESE} },
			func(m Move) any { return m.PegLinks[0] }},
		{"RemovedPegs", func(m *Move) { m.RemovedPegs[0] = Point{} },
			func(m Move) any { return m.RemovedPegs[0] }},
	}
	for _, tc := range cases {
		g := build()
		last := g.Entries() - 1
		want := tc.read(g.History()[last])
		poked := g.Clone().History()[last]
		tc.poke(&poked)
		if got := tc.read(g.History()[last]); got != want {
			t.Errorf("Move.%s aliases between a game and its clone: writing %v on the clone's entry changed the original's from %v to %v",
				tc.name, tc.read(poked), want, got)
		}
	}

	// The same must hold across an undo and a fresh move, which is where the
	// outer slice is reused rather than the per-entry ones.
	g := build()
	last := g.Entries() - 1
	d := g.Clone()
	if err := d.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PlayPeg(at("H5")); err != nil {
		t.Fatalf("clone d: %v", err)
	}
	if got := g.History()[last]; got.Peg != at("C2") {
		t.Errorf("the original's last entry became %v after the clone diverged, want C2", got.Peg)
	}
	if got := len(g.History()[last].Removed); got != 1 {
		t.Errorf("the original's last entry now carries %d removals, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Non-turn entries
// ---------------------------------------------------------------------------

// TestAuditUndoOfNonTurnEntries requires that undoing an entry which consumed no
// turn leaves the move order exactly as it was. Getting this wrong lets one side
// move twice.
func TestAuditUndoOfNonTurnEntries(t *testing.T) {
	t.Run("draw offer", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4", "A6")
		before := auditState(g)
		if err := g.OfferDraw(Horizontal); err != nil {
			t.Fatal(err)
		}
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		if g.Turn() != Vertical {
			t.Errorf("undoing a draw offer handed the move to %v; vertical had not moved yet", g.Turn())
		}
		if got := auditState(g); got != before {
			t.Errorf("undoing a draw offer did not restore the position\n  %s", auditDiff(before, got))
		}
		if _, err := g.PlayPeg(at("F6")); err != nil {
			t.Errorf("vertical could not play after the offer was undone: %v", err)
		}
		if g.Turn() != Horizontal {
			t.Errorf("turn after vertical's move = %v", g.Turn())
		}
	})
	t.Run("resignation out of turn", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4")
		before := auditState(g)
		if err := g.Resign(Vertical); err != nil {
			t.Fatal(err)
		}
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		if g.Result().Over() {
			t.Errorf("the game is still over after undoing the resignation: %+v", g.Result())
		}
		if g.Turn() != Horizontal {
			t.Errorf("undoing vertical's out-of-turn resignation handed the move to %v, want horizontal", g.Turn())
		}
		if got := auditState(g); got != before {
			t.Errorf("undoing a resignation did not restore the position\n  %s", auditDiff(before, got))
		}
	})
	t.Run("accepted draw", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4", "A6")
		if err := g.OfferDraw(Horizontal); err != nil {
			t.Fatal(err)
		}
		before := auditState(g)
		if err := g.AcceptDraw(Vertical); err != nil {
			t.Fatal(err)
		}
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		if g.Result().Over() {
			t.Errorf("still drawn after undoing the acceptance: %+v", g.Result())
		}
		if got := auditState(g); got != before {
			t.Errorf("undoing an acceptance did not restore the position\n  %s", auditDiff(before, got))
		}
		if g.DrawOfferedBy() != Horizontal {
			t.Errorf("the standing offer was lost: DrawOfferedBy = %v", g.DrawOfferedBy())
		}
	})
}

// ---------------------------------------------------------------------------
// Choices the sources do not force
// ---------------------------------------------------------------------------

// TestAuditUnforcedChoices pins the behaviour the engine picks where no source
// forces it. Each one is a decision, not a rule; the report lists them all. If
// one of these changes, the change is deliberate and the test names the reading
// being abandoned.
func TestAuditUnforcedChoices(t *testing.T) {
	// C13: no source gives a procedure for declaring the draw rule 11 allows.
	// The engine draws the instant the side to move has no legal hole, which is
	// what OpenSpiel does (OpenSpiel C6), even while the other side still has
	// holes free.
	t.Run("draw fires when the side to move has no hole", func(t *testing.T) {
		g := MustNew(withSize(Std, 6))
		vertical := []string{"B1", "C1", "D1", "E1", "B3", "C3", "D3", "E3", "B6", "C6", "D6", "E6"}
		horizontal := []string{"B2", "C2", "D2", "E2", "B4", "C4", "D4", "E4", "B5", "C5", "D5", "E5"}
		var res Result
		for i := range vertical {
			if _, err := g.PlayPeg(at(vertical[i])); err != nil {
				t.Fatalf("vertical %s: %v", vertical[i], err)
			}
			var err error
			if res, err = g.PlayPeg(at(horizontal[i])); err != nil {
				t.Fatalf("horizontal %s: %v", horizontal[i], err)
			}
		}
		if res.Outcome != Draw || res.Reason != NoMovesLeft {
			t.Fatalf("result = %+v, want a draw for lack of moves", res)
		}
		if !g.HasLegalPlacement(Horizontal) {
			t.Error("this fixture is meant to leave horizontal with holes free")
		}
		auditPosition(t, g, func() string { return "forced draw on 6x6" })
	})

	// C16 records Little Golem's reflection across the main diagonal; OpenSpiel
	// instead rotates ninety degrees (OpenSpiel C7). The engine follows the
	// documented Little Golem example, 1.B4 swap -> D2.
	t.Run("swap reflects across the main diagonal", func(t *testing.T) {
		g := MustNew(Std)
		play(t, g, "B4")
		if err := g.Swap(); err != nil {
			t.Fatal(err)
		}
		if g.At(at("D2")) != Horizontal {
			t.Error("1.B4 swap should put the peg on D2, per the Little Golem example (C16)")
		}
		rotated := Point{Col: at("B4").Row, Row: g.Size() - 1 - at("B4").Col}
		if rotated != at("D2") && g.At(rotated) != NoPlayer {
			t.Errorf("the hole OpenSpiel's rotation would use, %v, is occupied too", rotated)
		}
	})

	// A hole on the diagonal reflects onto itself, so the swap keeps the hole and
	// only changes hands. No source discusses this case.
	t.Run("swapping a peg on the diagonal keeps the hole", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		p := Point{Col: 4, Row: 4}
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatal(err)
		}
		if err := g.Swap(); err != nil {
			t.Fatal(err)
		}
		if g.At(p) != Horizontal {
			t.Errorf("%v holds %v after the swap, want horizontal", p, g.At(p))
		}
		if g.PegCount(Vertical) != 0 {
			t.Error("the first player still has a peg")
		}
	})

	// Rule 4c makes linking a choice; the engine implements the choice as "every
	// legal link is offered and the player may withdraw any of them". An engine
	// that offered nothing and made the player ask would also satisfy rule 4c.
	t.Run("placement offers every legal link", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "E6", "A2", "G7", "A3")
		if err := g.PlacePeg(at("F4")); err != nil {
			t.Fatal(err)
		}
		if g.Staged().AutoLinks == 0 {
			t.Fatal("no link offered on placement")
		}
		for d := range Dir(NumDirs) {
			q := at("F4").Add(d)
			if !g.InBounds(q) || g.At(q) != Vertical {
				continue
			}
			l, _ := NewLink(at("F4"), q)
			if !g.HasLink(l) {
				t.Errorf("link %v to an own peg was not offered", l)
			}
		}
		g.AbortTurn()
	})

	// Rule 4 lists removals before the peg. No source says a different order is
	// illegal rather than merely undescribed; the engine refuses it, which is
	// what keeps a record replayable in one pass.
	t.Run("removals are refused once the peg is down", func(t *testing.T) {
		rs := withSize(Std, 10)
		rs.PegRemoval = true
		g := MustNew(rs)
		for _, s := range []string{"E6", "A2", "G7", "B4"} {
			if err := g.PlayNotation(s); err != nil {
				t.Fatal(err)
			}
		}
		if err := g.PlacePeg(at("C2")); err != nil {
			t.Fatal(err)
		}
		if err := g.RemovePeg(at("E6")); err != ErrRemoveAfterPeg {
			t.Errorf("lifting a peg after the placement: got %v, want %v", err, ErrRemoveAfterPeg)
		}
		if err := g.RemoveLink(at("E6"), at("G7")); err != ErrRemoveAfterPeg {
			t.Errorf("removing an older link after the placement: got %v, want %v", err, ErrRemoveAfterPeg)
		}
		g.AbortTurn()
	})

	// Rule 15 gives no pass. The engine additionally accepts a resignation or a
	// draw offer from the player who is not to move, which every online venue
	// allows and no rules text mentions.
	t.Run("resignation and draw offers are accepted out of turn", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4")
		if err := g.OfferDraw(Vertical); err != nil {
			t.Errorf("offering a draw out of turn: %v", err)
		}
		if err := g.Resign(Vertical); err != nil {
			t.Errorf("resigning out of turn: %v", err)
		}
		g2 := MustNew(withSize(Std, 10))
		if err := g2.Resign(NoPlayer); err != ErrNotYourTurn {
			t.Errorf("resigning as nobody: got %v, want %v", err, ErrNotYourTurn)
		}
		if err := g2.OfferDraw(NoPlayer); err != ErrNotYourTurn {
			t.Errorf("offering as nobody: got %v, want %v", err, ErrNotYourTurn)
		}
	})

	// A draw offer is not a turn, so it does not advance the ply count, and a
	// player may not accept their own offer. Neither is stated by any source;
	// both follow from treating the offer as a venue convention (C19).
	t.Run("a draw offer is not a turn", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4", "A6")
		ply, entries := g.Ply(), g.Entries()
		before := snapshot(g)
		if err := g.OfferDraw(Horizontal); err != nil {
			t.Fatal(err)
		}
		if g.Ply() != ply {
			t.Errorf("Ply went from %d to %d over a draw offer", ply, g.Ply())
		}
		if g.Entries() != entries+1 {
			t.Errorf("Entries went from %d to %d over a draw offer", entries, g.Entries())
		}
		if g.Turn() != Vertical {
			t.Errorf("offering a draw handed the move to %v", g.Turn())
		}
		if snapshot(g) != before {
			t.Error("offering a draw changed the board")
		}
		if err := g.AcceptDraw(Horizontal); err != ErrNoDrawOffer {
			t.Errorf("accepting one's own offer: got %v, want %v", err, ErrNoDrawOffer)
		}
	})

	// Rule 8 and C8: the academic formalisation calls a placement that offers two
	// mutually crossing links an unresolved case. On this geometry it cannot
	// arise, because two links sharing the placed peg never cross. The engine
	// relies on that, so pin it.
	t.Run("a placement can never offer two crossing links", func(t *testing.T) {
		centre := Point{Col: 6, Row: 6}
		for a := range Dir(NumDirs) {
			for b := range Dir(NumDirs) {
				if a == b {
					continue
				}
				la, okA := NewLink(centre, centre.Add(a))
				lb, okB := NewLink(centre, centre.Add(b))
				if !okA || !okB || la == lb {
					continue
				}
				if auditCross(la, lb) {
					t.Errorf("links %v and %v share the placed peg yet cross", la, lb)
				}
			}
		}
	})

	// The engine names sides by axis rather than colour, which sidesteps RD11's
	// colour disagreement. What every source agrees on is that the first mover
	// owns the top and bottom borders.
	t.Run("the first mover owns the top and bottom borders", func(t *testing.T) {
		g := MustNew(Std)
		if g.Turn() != Vertical {
			t.Errorf("first mover = %v", g.Turn())
		}
		n := g.Size()
		if !g.IsBorderRow(Vertical, Point{Col: 3, Row: 0}) || !g.IsBorderRow(Vertical, Point{Col: 3, Row: n - 1}) {
			t.Error("vertical does not own the top and bottom rows")
		}
		if !g.IsBorderRow(Horizontal, Point{Col: 0, Row: 3}) || !g.IsBorderRow(Horizontal, Point{Col: n - 1, Row: 3}) {
			t.Error("horizontal does not own the left and right columns")
		}
	})
}

// TestAuditLinkBlockedByAcceptsEitherNaming guards the one public entry point
// that takes a caller-built Link. Link.Dir is exported, so a caller can name an
// edge from either endpoint; both namings are the same edge and must be answered
// the same way.
func TestAuditLinkBlockedByAcceptsEitherNaming(t *testing.T) {
	g := MustNew(withSize(Std, 10))
	play(t, g, "D4", "D6", "E6", "A7")
	crossing := mustLink(t, "D6", "E4")
	if _, blocked := g.LinkBlockedBy(crossing, Horizontal); !blocked {
		t.Fatal("D4:E6 should block D6:E4")
	}
	reversed := Link{From: crossing.To(), Dir: crossing.Dir.Opposite()}
	if reversed.To() != crossing.From {
		t.Fatalf("%v is not %v seen from the other end", reversed, crossing)
	}
	if reversed.Canonical() != crossing {
		t.Errorf("%v canonicalises to %v, want %v", reversed, reversed.Canonical(), crossing)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("LinkBlockedBy(%v) panicked: %v", reversed, r)
			}
		}()
		if _, blocked := g.LinkBlockedBy(reversed, Horizontal); !blocked {
			t.Errorf("LinkBlockedBy(%v) says clear, but the same edge named %v is blocked", reversed, crossing)
		}
	}()
	// Every naming of every direction must survive the call.
	for d := range Dir(NumDirs) {
		l := Link{From: Point{Col: 5, Row: 5}, Dir: d}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("LinkBlockedBy(%v) panicked: %v", l, r)
				}
			}()
			g.LinkBlockedBy(l, Vertical)
		}()
	}
}

// ---------------------------------------------------------------------------
// Records: the self-validating wrapper around a transcript
// ---------------------------------------------------------------------------

// auditTamperMoves returns the same census of mutations TestAuditTranscriptTampering
// applies, given a transcript.
func auditTamperMoves(transcript string) []struct{ Name, Moves string } {
	moves := strings.Split(transcript, "; ")
	var out []struct{ Name, Moves string }
	add := func(name string, parts []string) {
		out = append(out, struct{ Name, Moves string }{name, strings.Join(parts, "; ")})
	}
	for i := 0; i+1 < len(moves); i++ {
		p := append([]string(nil), moves...)
		p[i], p[i+1] = p[i+1], p[i]
		add(fmt.Sprintf("swap entries %d and %d", i+1, i+2), p)
	}
	for i := range moves {
		p := append([]string(nil), moves...)
		p[i] = "F2"
		add(fmt.Sprintf("replace entry %d with a free hole", i+1), p)

		p2 := append([]string(nil), moves[:i:i]...)
		p2 = append(p2, moves[i+1:]...)
		add(fmt.Sprintf("drop entry %d", i+1), p2)

		p3 := append([]string(nil), moves[:i+1:i+1]...)
		p3 = append(p3, moves[i])
		p3 = append(p3, moves[i+1:]...)
		add(fmt.Sprintf("duplicate entry %d", i+1), p3)
	}
	for i, m := range moves {
		if strings.Contains(m, "~") {
			p := append([]string(nil), moves...)
			p[i] = strings.Fields(m)[0]
			add(fmt.Sprintf("strip the declined link from entry %d", i+1), p)
		}
		if strings.Contains(m, " -") {
			p := append([]string(nil), moves...)
			p[i] = strings.Fields(m)[0]
			add(fmt.Sprintf("strip the removal from entry %d", i+1), p)
		}
		if strings.HasPrefix(m, "v:") || strings.HasPrefix(m, "h:") {
			bare := strings.TrimPrefix(strings.TrimPrefix(m, "v:"), "h:")
			p := append([]string(nil), moves...)
			p[i] = bare
			add(fmt.Sprintf("strip the side from entry %d", i+1), p)
			p2 := append([]string(nil), moves...)
			p2[i] = "h:" + bare
			add(fmt.Sprintf("change the side of entry %d", i+1), p2)
		}
	}
	return out
}

// TestAuditRecordTampering runs the transcript census against Record, which is
// the layer that does claim to detect tampering. Each mutation is tried twice,
// because the two digests answer different attacks: left alone, the record's
// digest no longer covers its own contents and must be refused at decode; and
// recomputed, so that the record is internally consistent again, the moves no
// longer reach the position the record claims and must be refused at replay.
//
// A mutation is allowed to survive both only if it describes the identical game,
// which the test verifies rather than assumes.
func TestAuditRecordTampering(t *testing.T) {
	rs := withSize(Std, 10)
	g := MustNew(rs)
	for _, s := range []string{"D4", "D6", "E6 ~D4:E6", "E4", "C6", "G4", "B4 -D4:C6", "H6", "v:draw?", "F8"} {
		if err := g.PlayNotation(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	encoded := rec.Encode()

	// The untampered record must load back to the same game.
	loaded, back, err := LoadRecord(encoded)
	if err != nil {
		t.Fatalf("loading an untampered record: %v", err)
	}
	if got, want := auditState(loaded), auditState(g); got != want {
		t.Fatalf("an untampered record does not reload its own game\n  %s", auditDiff(want, got))
	}
	if back.Digest != rec.Digest || back.Position != rec.Position {
		t.Fatalf("digests changed across encode and decode: %+v vs %+v", back, rec)
	}

	mutations := auditTamperMoves(rec.Moves)
	if len(mutations) < 20 {
		t.Fatalf("only %d mutations generated", len(mutations))
	}
	staleAccepted, recomputedAccepted := 0, 0
	for _, m := range mutations {
		if m.Moves == rec.Moves {
			continue // a textual no-op is not a tamper
		}

		// Pass one: the digest is left as it was. Every textual change must be
		// refused at decode, because the digest covers the moves verbatim.
		stale := strings.Replace(encoded, "moves "+rec.Moves, "moves "+m.Moves, 1)
		if stale == encoded {
			t.Errorf("%s: could not splice the mutated moves into the record", m.Name)
			continue
		}
		if _, err := DecodeRecord(stale); err == nil {
			staleAccepted++
			t.Errorf("%s: a record whose moves were edited without touching its digest was accepted\n  moves: %s",
				m.Name, m.Moves)
		}

		// Pass two: the digest is honestly recomputed, so the record is
		// internally consistent and only the position check can catch it.
		fixed := rec
		fixed.Moves = m.Moves
		fixed.Digest = fixed.digest()
		if _, err := DecodeRecord(fixed.Encode()); err != nil {
			t.Errorf("%s: a record with a recomputed digest failed to decode: %v", m.Name, err)
			continue
		}
		replayed, err := fixed.Replay()
		if err != nil {
			continue // refused, which is the point
		}
		// Surviving both is only acceptable if the record describes the very
		// same game.
		if got, want := auditState(replayed), auditState(g); got != want {
			recomputedAccepted++
			t.Errorf("%s: a mutated record with a recomputed digest replayed to a DIFFERENT game\n  moves: %s\n  %s",
				m.Name, m.Moves, auditDiff(want, got))
		}
	}
	t.Logf("%d mutations checked twice; %d survived a stale digest, %d survived a recomputed one",
		len(mutations), staleAccepted, recomputedAccepted)

	// The other fields must be covered too, both ways round.
	for _, tc := range []struct {
		name string
		edit func(r *Record)
	}{
		{"the claimed outcome", func(r *Record) { r.Outcome = VerticalWins }},
		{"the claimed reason", func(r *Record) { r.Reason = Resignation }},
		{"the claimed position", func(r *Record) { r.Position = strings.Repeat("0", len(r.Position)) }},
		{"the ruleset", func(r *Record) { r.Ruleset = withSize(PP, 10) }},
		{"the board size", func(r *Record) { r.Ruleset = withSize(Std, 12) }},
	} {
		edited := rec
		tc.edit(&edited)
		if _, err := DecodeRecord(edited.Encode()); err == nil {
			t.Errorf("editing %s without touching the digest was accepted", tc.name)
		}
		edited.Digest = edited.digest()
		if _, err := DecodeRecord(edited.Encode()); err != nil {
			t.Errorf("editing %s with a recomputed digest failed to decode: %v", tc.name, err)
			continue
		}
		if _, err := edited.Replay(); err == nil {
			t.Errorf("editing %s and recomputing the digest produced a record that replayed cleanly", tc.name)
		}
	}

	// Truncation, at the record level rather than the move level.
	for _, tc := range []struct{ name, text string }{
		{"empty", ""},
		{"header only", "twixtui-record 1\n"},
		{"digest line removed", strings.Replace(encoded, "digest "+rec.Digest+"\n", "", 1)},
		{"position line removed", strings.Replace(encoded, "position "+rec.Position+"\n", "", 1)},
		{"moves line removed", strings.Replace(encoded, "moves "+rec.Moves+"\n", "", 1)},
		{"unknown field", encoded + "elo 1600\n"},
		{"wrong version", strings.Replace(encoded, "twixtui-record 1", "twixtui-record 2", 1)},
	} {
		if _, err := DecodeRecord(tc.text); err == nil {
			t.Errorf("a record with %s was accepted", tc.name)
		}
	}
}

// TestAuditPositionDigest attacks the digest netplay uses for its per-move
// divergence check. Two demands: it must be a function of the position alone, so
// the same position reached by different routes agrees, and it must separate
// positions that genuinely differ.
func TestAuditPositionDigest(t *testing.T) {
	// auditDigestKey names every input the digest is supposed to depend on, so a
	// disagreement in either direction is a defect: two games with the same key
	// must hash alike, and two with different keys must not collide.
	key := func(g *Game) string {
		return fmt.Sprintf("%s|%s|%v|%v|%v",
			g.Rules().Canonical(), snapshot(g), g.Result().Outcome, g.Result().Reason, g.Swapped())
	}

	t.Run("independent of the route taken", func(t *testing.T) {
		// The same four pegs, placed in two different orders, well apart so no
		// blocking can depend on the order.
		a := MustNew(withSize(Std, 12))
		play(t, a, "D4", "A3", "J9", "A8")
		b := MustNew(withSize(Std, 12))
		play(t, b, "J9", "A8", "D4", "A3")
		if snapshot(a) != snapshot(b) {
			t.Fatalf("the fixture does not reach the same board by both routes")
		}
		if PositionDigest(a) != PositionDigest(b) {
			t.Errorf("the same position reached by two move orders hashes differently: %s vs %s",
				PositionDigest(a), PositionDigest(b))
		}
	})

	t.Run("unchanged by undo and redo", func(t *testing.T) {
		g := MustNew(withSize(Std, 10))
		play(t, g, "D4", "A3", "E6", "A8")
		want := PositionDigest(g)
		if err := g.UndoLastMove(); err != nil {
			t.Fatal(err)
		}
		if _, err := g.PlayPeg(at("A8")); err != nil {
			t.Fatal(err)
		}
		if got := PositionDigest(g); got != want {
			t.Errorf("undoing and replaying the same move changed the digest: %s vs %s", got, want)
		}
		// And a clone agrees with its original.
		if got := PositionDigest(g.Clone()); got != want {
			t.Errorf("a clone hashes differently from its original: %s vs %s", got, want)
		}
	})

	t.Run("separates positions that differ", func(t *testing.T) {
		base := MustNew(withSize(Std, 10))
		play(t, base, "D4", "A3", "E6", "A8")
		want := PositionDigest(base)
		// A different peg, a different side to move, and a different link set
		// must each change the digest.
		other := MustNew(withSize(Std, 10))
		play(t, other, "D4", "A3", "E6", "A7")
		if PositionDigest(other) == want {
			t.Error("moving one peg did not change the digest")
		}
		fewer := MustNew(withSize(Std, 10))
		for _, s := range []string{"D4", "A3", "E6 ~D4:E6", "A8"} {
			if err := fewer.PlayNotation(s); err != nil {
				t.Fatal(err)
			}
		}
		if snapshot(fewer) == snapshot(base) {
			t.Fatal("the declined-link fixture did not change the board")
		}
		if PositionDigest(fewer) == want {
			t.Error("declining a link did not change the digest, so the digest ignores links")
		}
		half := MustNew(withSize(Std, 10))
		play(t, half, "D4", "A3", "E6")
		if PositionDigest(half) == want {
			t.Error("a different side to move did not change the digest")
		}
	})

	t.Run("well defined over many positions", func(t *testing.T) {
		// Every position from a batch of random games is indexed both ways. A
		// digest that ignored any component of the position would collide here
		// immediately, and a digest that depended on history would disagree.
		byDigest := map[string]string{}
		byKey := map[string]string{}
		positions := 0
		rulesets := auditRulesets([]int{6, 8, 10})
		per := 2
		if !testing.Short() {
			per = 12
		}
		for i, rs := range rulesets {
			for trial := range per {
				seed := [2]uint64{uint64(i)*613 + uint64(trial), 0xd19e57}
				rng := rand.New(rand.NewPCG(seed[0], seed[1]))
				plan := &auditGamePlan{rs: rs, seed: seed}
				g := MustNew(rs)
				for range 40 {
					if g.Result().Over() {
						break
					}
					ok, err := auditPlayRandomTurn(g, plan, rng, true)
					if err != nil {
						t.Fatalf("%v\n  %s", err, plan.label())
					}
					if !ok {
						break
					}
					positions++
					k, d := key(g), PositionDigest(g)
					if prev, ok := byDigest[d]; ok && prev != k {
						t.Fatalf("digest %s covers two different positions\n  %s\n  %s\n  %s",
							d, prev, k, plan.label())
					}
					if prev, ok := byKey[k]; ok && prev != d {
						t.Fatalf("one position hashed to %s and to %s\n  %s", prev, d, plan.label())
					}
					byDigest[d] = k
					byKey[k] = d
				}
			}
		}
		t.Logf("%d positions, %d distinct digests", positions, len(byDigest))
		if positions < 100 {
			t.Fatalf("only %d positions sampled", positions)
		}
	})
}
