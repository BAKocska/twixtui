package game

import (
	"math/rand/v2"
	"testing"
)

func at(s string) Point {
	p, err := ParsePoint(s)
	if err != nil {
		panic(err)
	}
	return p
}

// play runs a sequence of ordinary peg placements, failing the test on any
// rejected move.
func play(t *testing.T, g *Game, holes ...string) {
	t.Helper()
	for _, h := range holes {
		if _, err := g.PlayPeg(at(h)); err != nil {
			t.Fatalf("placing %s: %v", h, err)
		}
	}
}

func TestBoardTopology(t *testing.T) {
	g := MustNew(Std)
	if g.Size() != 24 {
		t.Fatalf("size = %d, want 24", g.Size())
	}
	corners := []Point{{0, 0}, {23, 0}, {0, 23}, {23, 23}}
	for _, c := range corners {
		if !g.IsCorner(c) {
			t.Errorf("%v should be a corner", c)
		}
		if g.Exists(c) {
			t.Errorf("%v should not exist as a hole", c)
		}
	}
	if g.IsCorner(at("B1")) {
		t.Error("B1 is not a corner")
	}
	if !g.Exists(at("A2")) {
		t.Error("A2 is an ordinary border hole and should exist")
	}
}

// TestLegalPlacementCounts checks the border-row exclusion, which every source
// agrees on: a player may use their own border rows but never the opponent's.
func TestLegalPlacementCounts(t *testing.T) {
	for _, size := range []int{6, 8, 24} {
		rs := Std
		rs.Size = size
		g := MustNew(rs)
		want := size * (size - 2)
		for _, pl := range []Player{Vertical, Horizontal} {
			got := len(g.LegalPlacements(pl))
			if got != want {
				t.Errorf("size %d, %s: %d legal holes, want %d", size, pl, got, want)
			}
		}
	}
}

func TestBorderRowRestrictions(t *testing.T) {
	g := MustNew(Std)
	n := g.Size()

	// Vertical owns the top and bottom rows and may use them.
	if err := g.CanPlace(Vertical, Point{Col: 5, Row: 0}); err != nil {
		t.Errorf("vertical in its own top row: %v", err)
	}
	// Vertical may not use the left or right columns, which belong to Horizontal.
	if err := g.CanPlace(Vertical, Point{Col: 0, Row: 5}); err != ErrOpponentBorder {
		t.Errorf("vertical in opponent border column: got %v, want %v", err, ErrOpponentBorder)
	}
	if err := g.CanPlace(Horizontal, Point{Col: 5, Row: n - 1}); err != ErrOpponentBorder {
		t.Errorf("horizontal in opponent border row: got %v, want %v", err, ErrOpponentBorder)
	}
	if err := g.CanPlace(Horizontal, Point{Col: 0, Row: 5}); err != nil {
		t.Errorf("horizontal in its own left column: %v", err)
	}
	if err := g.CanPlace(Vertical, Point{Col: 0, Row: 0}); err == nil {
		t.Error("corner accepted")
	}
	if err := g.CanPlace(Vertical, Point{Col: -1, Row: 3}); err != ErrOffBoard {
		t.Errorf("off-board: got %v", err)
	}
}

func TestOccupiedHoleRejected(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4")
	if err := g.CanPlace(Horizontal, at("D4")); err != ErrOccupied {
		t.Errorf("got %v, want %v", err, ErrOccupied)
	}
}

// TestAutoLinkTakesEveryOfferedLink checks that placing a peg links it to all of
// its own knight neighbours.
func TestAutoLinkTakesEveryOfferedLink(t *testing.T) {
	g := MustNew(Std)
	// Vertical builds two pegs a knight's move apart, Horizontal plays far away.
	play(t, g, "D4", "A6", "E6")
	l, ok := NewLink(at("D4"), at("E6"))
	if !ok {
		t.Fatal("D4 and E6 should be a knight's move apart")
	}
	if !g.HasLink(l) {
		t.Fatalf("expected automatic link %v", l)
	}
	if g.LinkOwner(l) != Vertical {
		t.Errorf("link owner = %v, want vertical", g.LinkOwner(l))
	}
}

// TestAutoLinkOrderIndependent verifies the invariant the engine relies on: two
// links sharing the newly placed peg can never cross, so the set of links taken
// on placement does not depend on the order the directions are examined.
func TestAutoLinkOrderIndependent(t *testing.T) {
	// Surround a hole with own pegs at every knight offset, then place the
	// middle peg and check all eight links appear.
	rs := Std
	rs.Size = 12
	g := MustNew(rs)
	centre := Point{Col: 5, Row: 5}
	var neighbours []Point
	for d := range Dir(NumDirs) {
		neighbours = append(neighbours, centre.Add(d))
	}
	// Fill the neighbours with Vertical pegs, giving Horizontal throwaway moves.
	spare := []string{"A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9"}
	for i, nb := range neighbours {
		if _, err := g.PlayPeg(nb); err != nil {
			t.Fatalf("neighbour %v: %v", nb, err)
		}
		if _, err := g.PlayPeg(at(spare[i])); err != nil {
			t.Fatalf("spare %s: %v", spare[i], err)
		}
	}
	if _, err := g.PlayPeg(centre); err != nil {
		t.Fatalf("centre: %v", err)
	}
	for d := range Dir(NumDirs) {
		l, _ := NewLink(centre, centre.Add(d))
		if !g.HasLink(l) {
			t.Errorf("missing link %v in direction %v", l, d)
		}
	}
}

// TestOpponentLinkBlocks is the core blocking rule: a link may not cross an
// existing link of either colour.
func TestOpponentLinkBlocks(t *testing.T) {
	g := MustNew(Std)
	// Vertical links D4-E6. Horizontal then tries D6-E4, which crosses it.
	play(t, g, "D4", "D6", "E6")
	crossing, _ := NewLink(at("D6"), at("E4"))
	if _, blocked := g.LinkBlockedBy(crossing, Horizontal); !blocked {
		t.Fatal("expected the vertical link D4-E6 to block D6-E4")
	}
	// Horizontal places E4; the crossing link must not appear.
	if _, err := g.PlayPeg(at("E4")); err != nil {
		t.Fatalf("placing E4: %v", err)
	}
	if g.HasLink(crossing) {
		t.Error("a crossing link was created")
	}
}

// TestOwnLinkBlocksUnderStdRules pins the box rule that a player's own links
// also block, which is where implementations diverge.
func TestOwnLinkBlocksUnderStdRules(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4", "A6", "E6", "A7")
	// D6 and E4 are both Vertical's; the link between them crosses D4-E6.
	play(t, g, "D6", "A8")
	if _, err := g.PlayPeg(at("E4")); err != nil {
		t.Fatalf("placing E4: %v", err)
	}
	own, _ := NewLink(at("D6"), at("E4"))
	if g.HasLink(own) {
		t.Error("own crossing link was created under std rules")
	}
	// Hand back the move to Vertical so it can try to force the link itself.
	play(t, g, "A9")
	if err := g.AddLink(at("D6"), at("E4")); err != ErrLinkCrosses {
		t.Errorf("adding own crossing link by hand: got %v, want %v", err, ErrLinkCrosses)
	}
}

// TestOwnLinksMayCrossUnderPP pins the paper-and-pencil divergence.
func TestOwnLinksMayCrossUnderPP(t *testing.T) {
	g := MustNew(PP)
	play(t, g, "D4", "A6", "E6", "A7", "D6", "A8", "E4")
	own, _ := NewLink(at("D6"), at("E4"))
	if !g.HasLink(own) {
		t.Error("own crossing link should be allowed under pp rules")
	}
	// An opponent's link still blocks.
	other := MustNew(PP)
	play(t, other, "D4", "D6", "E6")
	crossing, _ := NewLink(at("D6"), at("E4"))
	if _, blocked := other.LinkBlockedBy(crossing, Horizontal); !blocked {
		t.Error("an opponent link must still block under pp rules")
	}
}

// TestDeliberateOmission checks that a player can decline an offered link, which
// the printed rules explicitly allow and which matters because an unmade link is
// no barrier to the opponent.
func TestDeliberateOmission(t *testing.T) {
	g := MustNew(Std)
	// Vertical opens at D4; Horizontal puts a peg at D6, one half of a crossing
	// pair that Vertical's D4-E6 link would otherwise block.
	play(t, g, "D4", "D6")

	if err := g.PlacePeg(at("E6")); err != nil {
		t.Fatalf("placing E6: %v", err)
	}
	l, _ := NewLink(at("D4"), at("E6"))
	if !g.HasLink(l) {
		t.Fatal("link should be offered on placement")
	}
	if err := g.RemoveLink(at("D4"), at("E6")); err != nil {
		t.Fatalf("declining the offered link: %v", err)
	}
	if g.HasLink(l) {
		t.Error("declined link is still on the board")
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatalf("committing: %v", err)
	}
	if g.HasLink(l) {
		t.Error("declined link reappeared after commit")
	}

	// The gap Vertical left open lets Horizontal cross it.
	if _, err := g.PlayPeg(at("E4")); err != nil {
		t.Fatalf("placing E4: %v", err)
	}
	crossing, _ := NewLink(at("D6"), at("E4"))
	if !g.HasLink(crossing) {
		t.Error("with the link declined, the crossing link should be legal")
	}
	if g.LinkOwner(crossing) != Horizontal {
		t.Errorf("crossing link owner = %v, want horizontal", g.LinkOwner(crossing))
	}
}

// TestAutomaticLinkingRefusesEdits checks the pp ruleset gives no link control.
func TestAutomaticLinkingRefusesEdits(t *testing.T) {
	g := MustNew(PP)
	play(t, g, "D4", "A6")
	if err := g.PlacePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveLink(at("D4"), at("E6")); err != ErrLinkingLocked {
		t.Errorf("got %v, want %v", err, ErrLinkingLocked)
	}
	if err := g.AddLink(at("D4"), at("E6")); err != ErrLinkingLocked {
		t.Errorf("got %v, want %v", err, ErrLinkingLocked)
	}
}

// TestRemovalOfOlderLinkNeedsRuleset separates withdrawing a link offered this
// turn from removing one placed earlier.
func TestRemovalOfOlderLinkNeedsRuleset(t *testing.T) {
	rs := Std
	rs.LinkRemoval = false
	g := MustNew(rs)
	play(t, g, "D4", "A6", "E6", "A7")
	// D4-E6 was created on an earlier turn, so taking it off needs the ruleset
	// to allow link removal.
	if err := g.RemoveLink(at("D4"), at("E6")); err != ErrRemovalLocked {
		t.Errorf("got %v, want %v", err, ErrRemovalLocked)
	}

	// With removal enabled it succeeds, before the peg goes down.
	g2 := MustNew(Std)
	play(t, g2, "D4", "A6", "E6", "A7")
	if err := g2.RemoveLink(at("D4"), at("E6")); err != nil {
		t.Fatalf("removing an older link under std rules: %v", err)
	}
	if err := g2.PlacePeg(at("G4")); err != nil {
		t.Fatal(err)
	}
	if _, err := g2.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if l, _ := NewLink(at("D4"), at("E6")); g2.HasLink(l) {
		t.Error("the removed link is back on the board")
	}
}

// TestRemovalsComeBeforeThePeg pins the turn order the printed rules give:
// removals first, then the peg, then link additions. Withdrawing a link the
// engine offered during this turn is not a removal and stays legal afterwards.
func TestRemovalsComeBeforeThePeg(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	g := MustNew(rs)
	play(t, g, "D4", "A6", "E6", "A7")

	if err := g.PlacePeg(at("F4")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveLink(at("D4"), at("E6")); err != ErrRemoveAfterPeg {
		t.Errorf("removing an older link after the peg: got %v, want %v", err, ErrRemoveAfterPeg)
	}
	if err := g.RemovePeg(at("D4")); err != ErrRemoveAfterPeg {
		t.Errorf("removing a peg after the peg: got %v, want %v", err, ErrRemoveAfterPeg)
	}
	// F4 is a knight's move from E6, so the engine offered that link; declining
	// it is a choice about this turn, not a removal, and stays legal.
	if l, _ := NewLink(at("F4"), at("E6")); !g.HasLink(l) {
		t.Fatal("expected F4-E6 to be offered")
	}
	if err := g.RemoveLink(at("F4"), at("E6")); err != nil {
		t.Errorf("declining a link offered this turn: %v", err)
	}
	if l, _ := NewLink(at("F4"), at("E6")); g.HasLink(l) {
		t.Error("the declined link is still on the board")
	}
}

func TestTurnMustPlaceExactlyOnePeg(t *testing.T) {
	g := MustNew(Std)
	if _, err := g.CommitTurn(); err != ErrNoPegPlaced {
		t.Errorf("committing an empty turn: got %v, want %v", err, ErrNoPegPlaced)
	}
	if err := g.PlacePeg(at("D4")); err != nil {
		t.Fatal(err)
	}
	if err := g.PlacePeg(at("D6")); err != ErrPegAlreadySet {
		t.Errorf("second peg: got %v, want %v", err, ErrPegAlreadySet)
	}
}

func TestAbortTurnRestoresPosition(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4", "A6")
	before := snapshot(g)
	if err := g.PlacePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveLink(at("D4"), at("E6")); err != nil {
		t.Fatal(err)
	}
	g.AbortTurn()
	if after := snapshot(g); after != before {
		t.Error("aborting a turn did not restore the position")
	}
	if g.Turn() != Vertical {
		t.Errorf("turn = %v, want vertical", g.Turn())
	}
}

// TestWinByConnection builds a real vertical chain on a small board and checks
// the win fires exactly when the chain closes, not before.
func TestWinByConnection(t *testing.T) {
	rs := Std
	rs.Size = 8
	g := MustNew(rs)

	// Vertical must join row 1 to row 8. Three (1,2) steps climb from row 1 to
	// row 7, then a (2,1) step reaches row 8:
	// D1 -> E3 -> D5 -> E7 -> C8.
	chain := []string{"D1", "E3", "D5", "E7", "C8"}
	// Horizontal answers in its own left column, far from the ladder.
	spare := []string{"A2", "A3", "A4", "A5"}

	for i, h := range chain {
		res, err := g.PlayPeg(at(h))
		if err != nil {
			t.Fatalf("vertical %s: %v", h, err)
		}
		if i < len(chain)-1 {
			if res.Over() {
				t.Fatalf("game ended early after %s: %+v", h, res)
			}
			if _, err := g.PlayPeg(at(spare[i])); err != nil {
				t.Fatalf("horizontal %s: %v", spare[i], err)
			}
			continue
		}
		if res.Outcome != VerticalWins || res.Reason != Connection {
			t.Fatalf("result after %s = %+v, want vertical win by connection", h, res)
		}
	}
	if !g.Connected(Vertical) {
		t.Error("Connected(Vertical) is false after a win")
	}
	if g.Connected(Horizontal) {
		t.Error("Connected(Horizontal) should be false")
	}
	// The chain really is linked end to end.
	for _, pair := range [][2]string{{"D1", "E3"}, {"E3", "D5"}, {"D5", "E7"}, {"E7", "C8"}} {
		l, ok := NewLink(at(pair[0]), at(pair[1]))
		if !ok {
			t.Fatalf("%s-%s is not a knight's move", pair[0], pair[1])
		}
		if !g.HasLink(l) {
			t.Errorf("missing chain link %s-%s", pair[0], pair[1])
		}
	}
}

// TestNoWinWithoutBothBorders checks that a long chain touching only one border
// does not win.
func TestNoWinWithoutBothBorders(t *testing.T) {
	rs := Std
	rs.Size = 8
	g := MustNew(rs)
	play(t, g, "D1", "A2", "E3", "A3", "D5", "A4")
	if g.Result().Over() {
		t.Fatal("game should still be running")
	}
	if g.Connected(Vertical) {
		t.Error("chain touches only the top border")
	}
}

// TestHorizontalWinsAcrossColumns checks the other axis, including that a peg in
// column A counts as reaching the left border.
func TestHorizontalWinsAcrossColumns(t *testing.T) {
	rs := Std
	rs.Size = 8
	g := MustNew(rs)
	// Vertical wastes moves on its own top row; Horizontal builds A..H.
	vSpare := []string{"B1", "C1", "D1", "E1", "F1"}
	hChain := []string{"A4", "C5", "E4", "G5", "H3"}
	for i, h := range hChain {
		if _, err := g.PlayPeg(at(vSpare[i])); err != nil {
			t.Fatalf("vertical %s: %v", vSpare[i], err)
		}
		res, err := g.PlayPeg(at(h))
		if err != nil {
			t.Fatalf("horizontal %s: %v", h, err)
		}
		if i == len(hChain)-1 {
			if res.Outcome != HorizontalWins || res.Reason != Connection {
				t.Fatalf("result = %+v, want horizontal win", res)
			}
		} else if res.Over() {
			t.Fatalf("game ended early after %s", h)
		}
	}
}

// TestForcedDraw plays a hand-built 6x6 game that fills every hole Vertical is
// allowed to use while leaving neither side connected, which is the position the
// draw rule exists for. Vertical keeps its middle pegs on one row so its chain
// can never span top to bottom, and Horizontal never touches column A or F so it
// can never span left to right.
func TestForcedDraw(t *testing.T) {
	rs := Std
	rs.Size = 6
	g := MustNew(rs)

	vertical := []string{
		"B1", "C1", "D1", "E1", // own top border row
		"B3", "C3", "D3", "E3", // one middle row only: reaches the top, never the bottom
		"B6", "C6", "D6", "E6", // own bottom border row, isolated
	}
	horizontal := []string{
		"B2", "C2", "D2", "E2",
		"B4", "C4", "D4", "E4",
		"B5", "C5", "D5", "E5",
	}
	if len(vertical) != len(horizontal) {
		t.Fatalf("the two move lists must alternate evenly: %d vs %d", len(vertical), len(horizontal))
	}

	for i := range vertical {
		res, err := g.PlayPeg(at(vertical[i]))
		if err != nil {
			t.Fatalf("vertical %s: %v", vertical[i], err)
		}
		if res.Over() {
			t.Fatalf("game ended early after vertical %s: %+v", vertical[i], res)
		}
		res, err = g.PlayPeg(at(horizontal[i]))
		if err != nil {
			t.Fatalf("horizontal %s: %v", horizontal[i], err)
		}
		last := i == len(vertical)-1
		if !last && res.Over() {
			t.Fatalf("game ended early after horizontal %s: %+v", horizontal[i], res)
		}
		if last {
			if res.Outcome != Draw || res.Reason != NoMovesLeft {
				t.Fatalf("final result = %+v, want a draw for lack of moves", res)
			}
		}
	}

	// The draw is genuine: Vertical has nowhere left, Horizontal still has its
	// own border columns free, and neither side is connected.
	if g.HasLegalPlacement(Vertical) {
		t.Error("Vertical still has a legal hole, so this is not the draw position")
	}
	if !g.HasLegalPlacement(Horizontal) {
		t.Error("Horizontal should still have its own border columns free")
	}
	if g.Connected(Vertical) || g.Connected(Horizontal) {
		t.Error("neither side should be connected in a draw")
	}
	if _, err := g.PlayPeg(at("C2")); err != ErrGameOver {
		t.Errorf("move after the game ended: got %v, want %v", err, ErrGameOver)
	}
}

// TestGamesAlwaysTerminate plays random games to the end and checks the engine
// always reaches a definite result with a consistent reason.
func TestGamesAlwaysTerminate(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 29))
	for _, rs := range []Ruleset{withSize(Std, 6), withSize(PP, 7), withSize(Std, 8)} {
		for trial := range 15 {
			g := MustNew(rs)
			moves := 0
			for !g.Result().Over() {
				ps := g.LegalPlacements(g.Turn())
				if len(ps) == 0 {
					t.Fatalf("%s trial %d: no legal placements but the game is not over", rs.Canonical(), trial)
				}
				if _, err := g.PlayPeg(ps[rng.IntN(len(ps))]); err != nil {
					t.Fatalf("%s trial %d move %d: %v", rs.Canonical(), trial, moves, err)
				}
				moves++
				if moves > rs.Size*rs.Size+10 {
					t.Fatalf("%s trial %d: game did not terminate", rs.Canonical(), trial)
				}
			}
			res := g.Result()
			switch res.Reason {
			case Connection:
				if res.Winner() == NoPlayer {
					t.Errorf("win by connection with no winner")
				}
				if !g.Connected(res.Winner()) {
					t.Errorf("declared winner %v is not connected", res.Winner())
				}
			case NoMovesLeft:
				if res.Outcome != Draw {
					t.Errorf("NoMovesLeft with outcome %v", res.Outcome)
				}
				if g.HasLegalPlacement(g.Turn()) {
					t.Errorf("drawn for lack of moves but %v still has one", g.Turn())
				}
			default:
				t.Errorf("unexpected end reason %v", res.Reason)
			}
		}
	}
}

func withSize(rs Ruleset, size int) Ruleset {
	rs.Size = size
	return rs
}

// TestSwapReflectsAcrossDiagonal pins the swap mechanic to the convention used
// by the SGF game-record format and online venues.
func TestSwapReflectsAcrossDiagonal(t *testing.T) {
	g := MustNew(Std)
	if g.CanSwap() {
		t.Error("swap offered before the first move")
	}
	play(t, g, "B4")
	if !g.CanSwap() {
		t.Fatal("swap should be available to the second player")
	}
	if err := g.Swap(); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if g.At(at("B4")) != NoPlayer {
		t.Error("the original peg is still on the board")
	}
	if got := g.At(at("D2")); got != Horizontal {
		t.Errorf("D2 holds %v, want horizontal", got)
	}
	if g.Turn() != Vertical {
		t.Errorf("turn after swap = %v, want vertical", g.Turn())
	}
	if !g.Swapped() {
		t.Error("Swapped() is false after a swap")
	}
	if g.CanSwap() {
		t.Error("swap should only be available once")
	}
}

// TestSwapPreservesLegality checks the reflection maps a hole the first player
// may use onto a hole the second player may use, including on border rows.
func TestSwapPreservesLegality(t *testing.T) {
	g0 := MustNew(Std)
	n := g0.Size()
	for _, p := range g0.LegalPlacements(Vertical) {
		mirrored := Point{Col: p.Row, Row: p.Col}
		if err := g0.CanPlace(Horizontal, mirrored); err != nil {
			t.Fatalf("vertical hole %v reflects to %v which horizontal may not use: %v", p, mirrored, err)
		}
		if mirrored.Col < 0 || mirrored.Col >= n || mirrored.Row < 0 || mirrored.Row >= n {
			t.Fatalf("reflection of %v is off the board", p)
		}
	}
}

func TestSwapDisabledByRuleset(t *testing.T) {
	g := MustNew(Classic3M)
	play(t, g, "B4")
	if g.CanSwap() {
		t.Error("classic rules should not offer swap")
	}
	if err := g.Swap(); err != ErrSwapUnavailable {
		t.Errorf("got %v, want %v", err, ErrSwapUnavailable)
	}
}

func TestResignAndDrawAgreement(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4", "A6")
	if err := g.Resign(Vertical); err != nil {
		t.Fatal(err)
	}
	if got := g.Result(); got.Outcome != HorizontalWins || got.Reason != Resignation {
		t.Errorf("result = %+v, want horizontal win by resignation", got)
	}

	g2 := MustNew(Std)
	play(t, g2, "D4", "A6")
	if err := g2.AcceptDraw(Vertical); err != ErrNoDrawOffer {
		t.Errorf("accepting a draw with no offer: got %v", err)
	}
	if err := g2.OfferDraw(Horizontal); err != nil {
		t.Fatal(err)
	}
	if err := g2.AcceptDraw(Horizontal); err != ErrNoDrawOffer {
		t.Error("a player accepted their own offer")
	}
	if err := g2.AcceptDraw(Vertical); err != nil {
		t.Fatal(err)
	}
	if got := g2.Result(); got.Outcome != Draw || got.Reason != Agreement {
		t.Errorf("result = %+v, want draw by agreement", got)
	}
}

// snapshot renders the full board state as a comparable string.
func snapshot(g *Game) string {
	b := make([]byte, 0, len(g.pegs)*2+8)
	for i := range g.pegs {
		b = append(b, byte('0'+g.pegs[i]), byte('a'+g.links[i]%26), byte('a'+g.links[i]/26))
	}
	b = append(b, byte('0'+g.turn))
	return string(b)
}

// TestUndoRestoresExactState plays a random game, undoes every move and checks
// the position is bit-identical to a fresh board, then replays to confirm the
// history survived. This catches undo bugs that a hand-written case would miss.
func TestUndoRestoresExactState(t *testing.T) {
	rs := Std
	rs.Size = 10
	rng := rand.New(rand.NewPCG(1, 2))

	for trial := range 20 {
		g := MustNew(rs)
		fresh := snapshot(MustNew(rs))
		var states []string
		for range 30 {
			if g.Result().Over() {
				break
			}
			ps := g.LegalPlacements(g.Turn())
			if len(ps) == 0 {
				break
			}
			states = append(states, snapshot(g))
			if _, err := g.PlayPeg(ps[rng.IntN(len(ps))]); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
		}
		for i := len(states) - 1; i >= 0; i-- {
			if err := g.UndoLastMove(); err != nil {
				t.Fatalf("trial %d undo: %v", trial, err)
			}
			if got := snapshot(g); got != states[i] {
				t.Fatalf("trial %d: state after undoing to move %d does not match", trial, i)
			}
		}
		if snapshot(g) != fresh {
			t.Fatalf("trial %d: board not empty after undoing everything", trial)
		}
		if g.Ply() != 0 {
			t.Fatalf("trial %d: ply = %d after full undo", trial, g.Ply())
		}
	}
}

// TestCloneIsIndependent checks that a cloned game shares no state.
func TestCloneIsIndependent(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4", "A6")
	c := g.Clone()
	play(t, c, "E6")
	if g.At(at("E6")) != NoPlayer {
		t.Error("a move on the clone changed the original")
	}
	if g.Ply() == c.Ply() {
		t.Error("clone and original have the same ply count after a divergent move")
	}
}

// TestConnectivityMatchesFloodFill cross-checks the incremental union-find
// against an independent breadth-first search over the link graph.
func TestConnectivityMatchesFloodFill(t *testing.T) {
	rs := Std
	rs.Size = 10
	rng := rand.New(rand.NewPCG(7, 11))
	for trial := range 30 {
		g := MustNew(rs)
		for range 40 {
			if g.Result().Over() {
				break
			}
			ps := g.LegalPlacements(g.Turn())
			if len(ps) == 0 {
				break
			}
			if _, err := g.PlayPeg(ps[rng.IntN(len(ps))]); err != nil {
				t.Fatal(err)
			}
			for _, pl := range []Player{Vertical, Horizontal} {
				want := floodFillConnected(g, pl)
				got := g.Connected(pl)
				if want != got {
					t.Fatalf("trial %d: %s connected: union-find says %v, flood fill says %v", trial, pl, got, want)
				}
			}
		}
	}
}

// floodFillConnected independently answers whether a player has joined both of
// their border lines, by walking the link graph.
func floodFillConnected(g *Game, pl Player) bool {
	n := g.Size()
	seen := make([]bool, n*n)
	var stack []Point
	// Seed from every own peg on the first border line.
	for i := range n {
		var p Point
		if pl == Vertical {
			p = Point{Col: i, Row: 0}
		} else {
			p = Point{Col: 0, Row: i}
		}
		if g.At(p) == pl {
			seen[g.idx(p)] = true
			stack = append(stack, p)
		}
	}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if pl == Vertical && p.Row == n-1 {
			return true
		}
		if pl == Horizontal && p.Col == n-1 {
			return true
		}
		mask := g.LinkMask(p)
		for d := range Dir(NumDirs) {
			if mask&(1<<d) == 0 {
				continue
			}
			q := p.Add(d)
			if !g.InBounds(q) || seen[g.idx(q)] || g.At(q) != pl {
				continue
			}
			seen[g.idx(q)] = true
			stack = append(stack, q)
		}
	}
	return false
}

// TestRulesetFingerprintDistinguishesRules makes sure the handshake check cannot
// pass for two different rulesets.
func TestRulesetFingerprintDistinguishesRules(t *testing.T) {
	seen := map[string]string{}
	variants := []Ruleset{Std, PP, Classic3M}
	for _, base := range []Ruleset{Std, PP} {
		for _, size := range []int{12, 24} {
			v := base
			v.Size = size
			variants = append(variants, v)
		}
	}
	pegRemoval := Std
	pegRemoval.PegRemoval = true
	variants = append(variants, pegRemoval)

	for _, rs := range variants {
		fp := rs.Fingerprint()
		if prev, ok := seen[fp]; ok && prev != rs.Canonical() {
			t.Errorf("fingerprint collision between %q and %q", prev, rs.Canonical())
		}
		seen[fp] = rs.Canonical()
	}
	if len(seen) < 5 {
		t.Errorf("expected distinct fingerprints, got %d", len(seen))
	}
}

func TestRulesetValidation(t *testing.T) {
	bad := Ruleset{Size: 3, DeliberateLinking: true}
	if err := bad.Validate(); err == nil {
		t.Error("tiny board accepted")
	}
	bad2 := Ruleset{Size: 24, DeliberateLinking: false, LinkRemoval: true}
	if err := bad2.Validate(); err == nil {
		t.Error("automatic linking with link removal accepted")
	}
	for _, name := range PresetNames() {
		rs, err := Preset(name)
		if err != nil {
			t.Fatalf("preset %q: %v", name, err)
		}
		if err := rs.Validate(); err != nil {
			t.Errorf("preset %q is invalid: %v", name, err)
		}
		if rs.PresetName() != name {
			t.Errorf("preset %q round-trips to %q", name, rs.PresetName())
		}
		if PresetSummary(name) == "" {
			t.Errorf("preset %q has no summary", name)
		}
	}
	if _, err := Preset("nope"); err == nil {
		t.Error("unknown preset accepted")
	}
}

func TestPegRemoval(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	g := MustNew(rs)
	play(t, g, "D4", "A6", "E6", "A7")
	l, _ := NewLink(at("D4"), at("E6"))
	if !g.HasLink(l) {
		t.Fatal("expected the link before removal")
	}
	// Removals happen at the start of the turn, before the peg.
	if err := g.RemovePeg(at("E6")); err != nil {
		t.Fatalf("removing own peg: %v", err)
	}
	if g.At(at("E6")) != NoPlayer {
		t.Error("peg still present")
	}
	if g.HasLink(l) {
		t.Error("link attached to a removed peg survived")
	}
	if err := g.PlacePeg(at("G4")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if g.At(at("E6")) != NoPlayer || g.HasLink(l) {
		t.Error("the removal did not survive the commit")
	}

	// Aborting a turn that removed a peg puts the peg and its links back, and
	// leaves no link attached to an empty hole.
	g4 := MustNew(rs)
	play(t, g4, "D4", "A6", "E6", "A7")
	if err := g4.RemovePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	g4.AbortTurn()
	if g4.At(at("E6")) != Vertical {
		t.Error("aborting did not restore the removed peg")
	}
	if !g4.HasLink(l) {
		t.Error("aborting did not restore the link that came away with the peg")
	}
	assertNoDanglingLinks(t, g4)

	// Off by default, and never applies to the opponent's pegs.
	g2 := MustNew(Std)
	play(t, g2, "D4", "A6")
	if err := g2.RemovePeg(at("D4")); err != ErrPegRemovalOff {
		t.Errorf("got %v, want %v", err, ErrPegRemovalOff)
	}

	g3 := MustNew(rs)
	play(t, g3, "D4")
	if err := g3.RemovePeg(at("D4")); err != ErrNotOwnPeg {
		t.Errorf("removing an opponent peg: got %v, want %v", err, ErrNotOwnPeg)
	}
}

// assertNoDanglingLinks checks the invariant that a link always joins two pegs
// of one colour. A link left attached to an empty hole would block future links
// for the rest of the game.
func assertNoDanglingLinks(t *testing.T, g *Game) {
	t.Helper()
	for row := range g.Size() {
		for col := range g.Size() {
			p := Point{Col: col, Row: row}
			mask := g.LinkMask(p)
			if mask == 0 {
				continue
			}
			owner := g.At(p)
			if owner == NoPlayer {
				t.Errorf("hole %s has links %08b but no peg", p, mask)
				continue
			}
			for d := range Dir(NumDirs) {
				if mask&(1<<d) == 0 {
					continue
				}
				q := p.Add(d)
				if !g.InBounds(q) {
					t.Errorf("hole %s has a link pointing off the board (%v)", p, d)
					continue
				}
				if g.At(q) != owner {
					t.Errorf("link %s-%s joins %v to %v", p, q, owner, g.At(q))
				}
			}
		}
	}
}
