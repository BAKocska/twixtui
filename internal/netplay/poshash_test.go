package netplay

import (
	"bytes"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// build plays a list of moves onto a fresh game, failing the test if any of
// them is illegal.
func build(t *testing.T, moves ...Entry) *game.Game {
	t.Helper()
	g, err := replay(testRules(), moves)
	if err != nil {
		t.Fatalf("building a position: %v", err)
	}
	return g
}

func v(move string) Entry { return Entry{Side: game.Vertical, Move: move} }
func h(move string) Entry { return Entry{Side: game.Horizontal, Move: move} }

// TestPositionHashIgnoresMoveOrder is the property the protocol relies on: two
// ends that reached the same board hash it alike, whatever route they took.
func TestPositionHashIgnoresMoveOrder(t *testing.T) {
	first := build(t, v("B1"), h("A2"), v("C3"), h("F2"))
	second := build(t, v("C3"), h("F2"), v("B1"), h("A2"))

	// The test would prove nothing if the two games were the same game, or if
	// the position had no links in it to be ordered differently.
	ta, err := first.Transcript()
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	tb, err := second.Transcript()
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if ta == tb {
		t.Fatalf("both games have the transcript %q, so nothing was reordered", ta)
	}
	link, ok := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	if !ok {
		t.Fatal("B1 and C3 are not a knight's move apart")
	}
	for name, g := range map[string]*game.Game{ta: first, tb: second} {
		if !g.HasLink(link) {
			t.Fatalf("%s: B1 and C3 are not linked, so the hash is not being asked about links", name)
		}
	}

	if a, b := PositionHash(first), PositionHash(second); a != b {
		t.Fatalf("the same position hashed to %s and %s", a, b)
	}
}

// TestPositionHashSurvivesReplayAndClone covers the two ways the protocol
// produces a second copy of a game: cloning it, and rebuilding it from the
// record the other end sent.
//
// The position holds one link and lacks another that was offered and declined,
// which is the part of a position the pegs alone do not state. Both copies have
// to carry that, and rebuilding from the same list of moves the original was
// built from would only be PositionHash of one call compared with itself.
func TestPositionHashSurvivesReplayAndClone(t *testing.T) {
	script := []Entry{v("B1"), h("A2"), v("C3 ~B1:C3"), h("F2"), v("D5")}
	live := build(t, script...)
	want := PositionHash(live)

	kept, ok := game.NewLink(game.Point{Col: 2, Row: 2}, game.Point{Col: 3, Row: 4})
	if !ok {
		t.Fatal("C3 and D5 are not a knight's move apart")
	}
	declined, ok := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	if !ok {
		t.Fatal("B1 and C3 are not a knight's move apart")
	}
	if !live.HasLink(kept) || live.HasLink(declined) {
		t.Fatal("the position does not hold one link and lack another, so neither copy is being asked about links")
	}

	if got := PositionHash(live.Clone()); got != want {
		t.Fatalf("a clone hashed to %s, the original to %s", got, want)
	}

	// The second copy comes the way the protocol makes one: each end records
	// the canonical notation its own engine wrote for the entry, and the far
	// end replays that, not whatever the player typed.
	written := make([]Entry, len(script))
	for i := range script {
		notation, err := live.MoveNotation(i)
		if err != nil {
			t.Fatalf("reading the notation of entry %d: %v", i+1, err)
		}
		written[i] = Entry{Side: script[i].Side, Move: notation}
	}
	rebuilt, err := replay(testRules(), written)
	if err != nil {
		t.Fatalf("replaying the written record: %v", err)
	}
	if got := PositionHash(rebuilt); got != want {
		t.Fatalf("a game replayed from its own record hashed to %s, the original to %s", got, want)
	}
}

// TestPositionHashDistinguishesPositions generates a family of positions that
// differ in one thing each and checks that no two of them share an encoding or
// a hash. The encoding is checked as well as the hash because it is the
// encoding that has to be injective; the hash only has to not collide.
func TestPositionHashDistinguishesPositions(t *testing.T) {
	positions := map[string]*game.Game{
		"empty": build(t),
	}

	// Every legal opening for vertical, and every legal reply to one of them.
	// These differ only in where a single peg sits.
	rs := testRules()
	fresh := build(t)
	for _, p := range fresh.LegalPlacements(game.Vertical) {
		positions["vertical opens "+p.String()] = build(t, v(p.String()))
	}
	opened := build(t, v("B1"))
	for _, p := range opened.LegalPlacements(game.Horizontal) {
		positions["horizontal replies "+p.String()] = build(t, v("B1"), h(p.String()))
	}

	// Positions that differ from "horizontal replies A2" only in the state the
	// board itself does not show: the result, why the game ended, and whose
	// draw offer is standing.
	base := []Entry{v("B1"), h("A2")}
	positions["vertical resigned"] = build(t, append(append([]Entry(nil), base...), v("resign"))...)
	positions["horizontal resigned"] = build(t, append(append([]Entry(nil), base...), h("resign"))...)
	positions["vertical offered a draw"] = build(t, append(append([]Entry(nil), base...), v("draw?"))...)
	positions["horizontal offered a draw"] = build(t, append(append([]Entry(nil), base...), h("draw?"))...)
	positions["draw agreed"] = build(t, append(append([]Entry(nil), base...), v("draw?"), h("draw!"))...)

	// The swap option moves a peg and changes hands, which no ordinary move can
	// do, so it gets its own entry.
	positions["swapped"] = build(t, v("B1"), h("swap"))

	// A pair whose pegs are identical and whose links are not. These rules let a
	// player decline a link the placement offers, so nothing on the peg board
	// tells the two apart; without this pair the family says nothing about
	// whether the encoding covers links at all, and two ends could agree on
	// boards that are not the same position.
	const linked, declined = "C3 taking the link to B1", "C3 declining the link to B1"
	positions[linked] = build(t, v("B1"), h("A2"), v("C3"))
	positions[declined] = build(t, v("B1"), h("A2"), v("C3 ~B1:C3"))
	bc, ok := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	if !ok {
		t.Fatal("B1 and C3 are not a knight's move apart")
	}
	if !positions[linked].HasLink(bc) || positions[declined].HasLink(bc) {
		t.Fatal("the pair does not differ in the B1-C3 link, so it isolates nothing")
	}
	for row := range positions[linked].Size() {
		for col := range positions[linked].Size() {
			p := game.Point{Col: col, Row: row}
			if positions[linked].At(p) != positions[declined].At(p) {
				t.Fatalf("the pair differs at %s as well, so the link is not the only difference", p)
			}
		}
	}

	if len(positions) < 40 {
		t.Fatalf("only %d positions were generated; the family is too small to say much", len(positions))
	}

	byHash := make(map[string]string, len(positions))
	byEncoding := make(map[string]string, len(positions))
	for name, g := range positions {
		if got := g.Rules(); got != rs {
			t.Fatalf("%s: built with the wrong rules", name)
		}
		encoding := string(encodePosition(nil, g))
		if other, clash := byEncoding[encoding]; clash {
			t.Errorf("%q and %q encode identically", name, other)
		}
		byEncoding[encoding] = name

		hash := PositionHash(g)
		if other, clash := byHash[hash]; clash {
			t.Errorf("%q and %q hash to the same %s", name, other, hash)
		}
		byHash[hash] = name
	}
	t.Logf("%d positions, %d distinct hashes", len(positions), len(byHash))
}

// TestPositionEncodingIsAppendOnly checks encodePosition appends rather than
// overwrites, since it is called with a shared buffer.
func TestPositionEncodingIsAppendOnly(t *testing.T) {
	g := build(t, v("B1"))
	prefix := []byte("keep me")
	out := encodePosition(prefix, g)
	if !bytes.HasPrefix(out, prefix) {
		t.Fatalf("encodePosition overwrote what it was given: %q", out)
	}
	if !bytes.Equal(out[len(prefix):], encodePosition(nil, g)) {
		t.Fatal("appending produced a different encoding from starting empty")
	}
}
