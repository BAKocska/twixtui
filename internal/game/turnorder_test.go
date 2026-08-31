package game

import "testing"

// TestDeclineThenAddReachesTheOtherOrder pins a property the interface relies on.
//
// The engine permits adding a link before the turn's peg is placed, but the game
// screen requires the peg first, so that a turn always has one shape. That is
// only safe if no position becomes unreachable. Placing the peg first can
// auto-create a link that blocks one the player wanted, which looks like a loss,
// but declining the offered link and then adding the wanted one is legal within
// the same turn, so the position is still reachable. If this ever stops holding,
// the interface is restricting the game rather than only its own ordering.
func TestDeclineThenAddReachesTheOtherOrder(t *testing.T) {
	rs := Std
	rs.Size = 12
	// Vertical owns D4 and E6 unlinked (declined), and F4.
	build := func(t *testing.T) *Game {
		t.Helper()
		g := MustNew(rs)
		if err := g.PlayNotation("D4"); err != nil {
			t.Fatal(err)
		}
		if err := g.PlayNotation("A6"); err != nil {
			t.Fatal(err)
		}
		if err := g.PlayNotation("E6 ~D4:E6"); err != nil {
			t.Fatal(err)
		}
		if err := g.PlayNotation("A7"); err != nil {
			t.Fatal(err)
		}
		return g
	}

	// D6 will be the new peg. Its auto-links and the link E6:F4... choose a
	// concrete crossing pair: D4:E6 (declined) vs D6:E4.
	g := build(t)
	// Vertical to move. Place D6, which auto-links to nothing yet.
	if err := g.PlacePeg(at("D6")); err != nil {
		t.Fatal(err)
	}
	// Now add the previously-declined D4:E6 by hand, after the peg. Allowed.
	if err := g.AddLink(at("D4"), at("E6")); err != nil {
		t.Fatalf("adding a link after the peg: %v", err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	l, _ := NewLink(at("D4"), at("E6"))
	if !g.HasLink(l) {
		t.Fatal("the hand-added link is missing")
	}

	// The other direction: an auto-link that blocks a link the player wants can
	// be declined, and the wanted link then added, in the same turn.
	g2 := build(t)
	// Give Vertical E4 so that placing F6 auto-links F6:E4, and separately
	// D4:E6 was declined. Verify decline-then-add of a crossing pair.
	if err := g2.PlacePeg(at("E4")); err != nil {
		t.Fatal(err)
	}
	// E4 auto-links to D6? D6 is empty. To F6? empty. To D2/F2? empty.
	if _, err := g2.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if err := g2.PlayNotation("A8"); err != nil {
		t.Fatal(err)
	}
	// Vertical places D6: auto-links D6:E4 (knight) and D6:E8/C8 (empty).
	if err := g2.PlacePeg(at("D6")); err != nil {
		t.Fatal(err)
	}
	auto, _ := NewLink(at("D6"), at("E4"))
	if !g2.HasLink(auto) {
		t.Fatal("expected D6:E4 to be offered")
	}
	// D4:E6 crosses D6:E4, so it must be refused while the auto-link stands.
	if err := g2.AddLink(at("D4"), at("E6")); err != ErrLinkCrosses {
		t.Fatalf("expected the crossing refusal, got %v", err)
	}
	// Declining the auto-link makes it addable: no position is unreachable.
	if err := g2.RemoveLink(at("D6"), at("E4")); err != nil {
		t.Fatalf("declining the offered link: %v", err)
	}
	if err := g2.AddLink(at("D4"), at("E6")); err != nil {
		t.Fatalf("after declining, the wanted link should be addable: %v", err)
	}
	if _, err := g2.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if !g2.HasLink(l) || g2.HasLink(auto) {
		t.Error("the committed position is not the one the player chose")
	}
}
