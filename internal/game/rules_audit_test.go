package game

import (
	"fmt"
	"testing"
)

func TestProbeAbortAfterPegRemovalOfLinkedNeighbour(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	g := MustNew(rs)
	play(t, g, "E6", "A6")
	before := snapshot(g)
	if err := g.PlacePeg(at("D4")); err != nil {
		t.Fatal(err)
	}
	l, _ := NewLink(at("D4"), at("E6"))
	if !g.HasLink(l) {
		t.Fatal("expected auto link D4:E6")
	}
	if err := g.RemovePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	g.AbortTurn()
	t.Logf("before=%q\nafter =%q", before, snapshot(g))
	t.Logf("pegs D4=%v E6=%v hasLink=%v mask(D4)=%b mask(E6)=%b",
		g.At(at("D4")), g.At(at("E6")), g.HasLink(l), g.LinkMask(at("D4")), g.LinkMask(at("E6")))
	if snapshot(g) != before {
		t.Errorf("PROBE: AbortTurn did not restore the position")
	}
}

func TestProbeUndoAfterPegRemovalOfLinkedNeighbour(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	g := MustNew(rs)
	play(t, g, "E6", "A6")
	before := snapshot(g)
	if err := g.PlacePeg(at("D4")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemovePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	l, _ := NewLink(at("D4"), at("E6"))
	t.Logf("after undo: D4=%v E6=%v hasLink(D4:E6)=%v", g.At(at("D4")), g.At(at("E6")), g.HasLink(l))
	if snapshot(g) != before {
		t.Errorf("PROBE: undo did not restore the position")
	}
}

func TestProbeResignTranscript(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4")
	if err := g.Resign(Vertical); err != nil {
		t.Fatal(err)
	}
	tr, err := g.Transcript()
	if err != nil {
		t.Fatal(err)
	}
	g2, err := ReplayTranscript(Std, tr)
	if err != nil {
		t.Fatalf("replay %q: %v", tr, err)
	}
	t.Logf("transcript=%q original=%+v replayed=%+v", tr, g.Result(), g2.Result())
	if g2.Result() != g.Result() {
		t.Errorf("PROBE: replay changed the result")
	}
}

func TestProbeUndoDrawOfferTurn(t *testing.T) {
	g := MustNew(Std)
	play(t, g, "D4", "A6")
	if g.Turn() != Vertical {
		t.Fatalf("turn = %v", g.Turn())
	}
	if err := g.OfferDraw(Horizontal); err != nil {
		t.Fatal(err)
	}
	if g.Turn() != Vertical {
		t.Fatalf("offer changed the turn to %v", g.Turn())
	}
	if err := g.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	t.Logf("turn after undoing the offer = %v", g.Turn())
	if g.Turn() != Vertical {
		t.Errorf("PROBE: undoing a draw offer handed the move to %v", g.Turn())
	}
}

func TestProbeSwapAfterDrawOffer(t *testing.T) {
	g := MustNew(Std)
	if err := g.OfferDraw(Horizontal); err != nil {
		t.Fatal(err)
	}
	play(t, g, "D4")
	t.Logf("CanSwap after an offer preceding the opening = %v", g.CanSwap())
	if !g.CanSwap() {
		t.Errorf("PROBE: swap denied on the second ply")
	}
}

func TestProbePegRemovalTranscript(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	g := MustNew(rs)
	play(t, g, "E6", "A6")
	if err := g.PlacePeg(at("D4")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemovePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	tr, err := g.Transcript()
	t.Logf("transcript=%q err=%v", tr, err)
	if err != nil {
		t.Errorf("PROBE: Transcript failed")
		return
	}
	g2, err := ReplayTranscript(rs, tr)
	if err != nil {
		t.Errorf("PROBE: replay of %q failed: %v", tr, err)
		return
	}
	if snapshot(g2) != snapshot(g) {
		t.Errorf("PROBE: replay diverged")
	}
}

func TestProbePegRemovalWithoutLinkRemoval(t *testing.T) {
	rs := Std
	rs.PegRemoval = true
	rs.LinkRemoval = false
	if err := rs.Validate(); err != nil {
		t.Fatalf("ruleset rejected: %v", err)
	}
	g := MustNew(rs)
	play(t, g, "D4", "A6", "E6", "A7")
	l, _ := NewLink(at("D4"), at("E6"))
	if !g.HasLink(l) {
		t.Fatal("expected the link")
	}
	if err := g.PlacePeg(at("G4")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemovePeg(at("E6")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	tr, err := g.Transcript()
	t.Logf("transcript=%q err=%v", tr, err)
	if err != nil {
		t.Errorf("PROBE: Transcript failed: %v", err)
		return
	}
	if _, err := ReplayTranscript(rs, tr); err != nil {
		t.Errorf("PROBE: replay of %q failed: %v", tr, err)
	}
}

func TestProbeColumnOverflow(t *testing.T) {
	for _, n := range []int{13, 14, 20, 64, 65, 70, 100} {
		s := ""
		for range n {
			s += "A"
		}
		col, err := ParseColumn(s)
		t.Logf("len=%d col=%d err=%v inBounds24=%v", n, col, err, col >= 0 && col < 24)
	}
	// Search for a hostile letter string that lands inside a 24-wide board.
	found := ""
	for _, prefix := range []string{"A", "B", "C", "D", "E", "F", "Z"} {
		for n := 60; n <= 70 && found == ""; n++ {
			s := prefix
			for range n {
				s += "A"
			}
			if col, err := ParseColumn(s); err == nil && col >= 0 && col < 24 {
				found = fmt.Sprintf("%s -> %d", s, col)
			}
		}
	}
	t.Logf("hostile in-bounds column: %q", found)
}

func TestProbeLinkBlockedByNonCanonical(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("PROBE: LinkBlockedBy panicked on a non-canonical direction: %v", r)
		}
	}()
	g := MustNew(Std)
	l := Link{From: at("D4"), Dir: SSW}
	blocker, blocked := g.LinkBlockedBy(l, Vertical)
	t.Logf("no panic: %v %v", blocker, blocked)
}
