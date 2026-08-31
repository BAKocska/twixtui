package game

import "testing"

// TestBlockerTableAgreesWithOpenSpiel compares the blocker set this engine
// derives from the geometric crossing rule against the table hand-written in
// DeepMind OpenSpiel's TwixT implementation
// (open_spiel/games/twixt/twixtboard.cc, kLinkDescriptorTable, entry NNE).
//
// The two encodings are independent: OpenSpiel enumerates the conflicting links
// by hand, this engine computes them from exact segment intersection. Agreement
// is therefore meaningful evidence that the crossing rule is implemented
// correctly, and this test fails if either the offsets or the geometry drift.
//
// OpenSpiel's board has y pointing up, this engine's rows point down, so the
// fixture is written in OpenSpiel's frame and converted on comparison.
func TestBlockerTableAgreesWithOpenSpiel(t *testing.T) {
	// Verbatim from kLinkDescriptorTable's NNE entry: offset of the blocking
	// link's start peg, and its direction.
	openSpielNNE := []struct {
		dx, dy int
		dir    Dir
	}{
		{0, 1, ENE},
		{-1, 0, ENE},
		{0, 2, ESE},
		{0, 1, ESE},
		{-1, 2, ESE},
		{-1, 1, ESE},
		{0, 1, SSE},
		{0, 2, SSE},
		{0, 3, SSE},
	}

	want := map[blocker]bool{}
	for _, e := range openSpielNNE {
		// y up in OpenSpiel, row down here.
		want[blocker{dCol: e.dx, dRow: -e.dy, dir: e.dir}] = true
	}

	got := map[blocker]bool{}
	for _, b := range blockerTable[NNE] {
		got[b] = true
	}

	for b := range want {
		if !got[b] {
			t.Errorf("OpenSpiel lists blocker %+v for NNE, this engine does not", b)
		}
	}
	for b := range got {
		if !want[b] {
			t.Errorf("this engine lists blocker %+v for NNE, OpenSpiel does not", b)
		}
	}
}
