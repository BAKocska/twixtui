package game

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestCanonicalRulesetRoundTrip(t *testing.T) {
	variants := []Ruleset{Std, PP, Classic3M}
	pegs := Std
	pegs.PegRemoval = true
	variants = append(variants, pegs)
	for _, size := range []int{MinSize, 12, 24, MaxSize} {
		v := Std
		v.Size = size
		variants = append(variants, v)
	}
	for _, rs := range variants {
		got, err := ParseCanonicalRuleset(rs.Canonical())
		if err != nil {
			t.Errorf("%s: %v", rs.Canonical(), err)
			continue
		}
		if got != rs {
			t.Errorf("%s round-trips to %s", rs.Canonical(), got.Canonical())
		}
	}
	for _, bad := range []string{
		"",
		"size=24",
		"size=nope;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true",
		"size=24;deliberate=maybe;removal=true;pegremoval=false;owncross=false;swap=true",
		"size=2;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true",
		"size=24;deliberate=false;removal=true;pegremoval=false;owncross=false;swap=true",
		"size=24;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true;extra=1",
	} {
		if _, err := ParseCanonicalRuleset(bad); err == nil {
			t.Errorf("ParseCanonicalRuleset(%q) should fail", bad)
		}
	}
}

// playSample builds a short game that finishes, and that contains a declined
// link so the record has an annotation to tamper with.
func playSample(t *testing.T) *Game {
	t.Helper()
	rs := Std
	rs.Size = 8
	g := MustNew(rs)
	moves := []string{
		"D1", "A2", // vertical starts its ladder on its own top row
		"E3", "A3",
		"D5", "A4",
		"E7", "A5",
		"F5 ~F5:E3", "A6", // F5 is offered two links; one is declined
		"C8", // reaches the bottom row and completes the chain
	}
	for _, m := range moves {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if got := g.Result(); got.Outcome != VerticalWins || got.Reason != Connection {
		t.Fatalf("sample game should end in a vertical win by connection, got %+v", got)
	}
	return g
}

func TestRecordRoundTrip(t *testing.T) {
	g := playSample(t)
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	encoded := rec.Encode()

	back, err := DecodeRecord(encoded)
	if err != nil {
		t.Fatalf("decoding a record we just wrote: %v\n%s", err, encoded)
	}
	if back != rec {
		t.Errorf("record does not round-trip:\nwrote %+v\nread  %+v", rec, back)
	}
	replayed, err := back.Replay()
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if got, want := snapshot(replayed), snapshot(g); got != want {
		t.Error("replayed position differs from the recorded one")
	}
	if _, _, err := LoadRecord(encoded); err != nil {
		t.Errorf("LoadRecord: %v", err)
	}
	// Comments and blank lines are ignorable, so a record can be annotated.
	if _, err := DecodeRecord("# a game\n\n" + encoded); err != nil {
		t.Errorf("a commented record should still decode: %v", err)
	}
}

// TestRecordRejectsEveryTamper is the point of the format. A bare move list
// cannot tell a genuine game from an edited one, because the edited text is a
// legal record of a different game. Each mutation below is a plausible
// corruption, and every one must be refused rather than replayed into a
// different game that looks authentic.
func TestRecordRejectsEveryTamper(t *testing.T) {
	g := playSample(t)
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	original := rec.Encode()

	entries := strings.Split(rec.Moves, "; ")
	if len(entries) < 4 {
		t.Fatalf("sample game is too short to mutate meaningfully: %q", rec.Moves)
	}

	mutations := map[string]string{
		"drop the first move":     strings.Join(entries[1:], "; "),
		"drop the last move":      strings.Join(entries[:len(entries)-1], "; "),
		"drop a middle move":      strings.Join(append(append([]string{}, entries[:2]...), entries[3:]...), "; "),
		"duplicate a move":        strings.Join(append(append([]string{}, entries[:2]...), entries[1:]...), "; "),
		"reorder two moves":       strings.Join(append([]string{entries[1], entries[0]}, entries[2:]...), "; "),
		"substitute a hole":       strings.Replace(rec.Moves, "A3", "A6", 1),
		"strip a decline":         strings.Replace(rec.Moves, " ~E3:F5", "", 1),
		"truncate to nothing":     "",
		"append a plausible move": rec.Moves + "; B2",
	}

	for name, moves := range mutations {
		mutated := strings.Replace(original, "moves "+rec.Moves, "moves "+moves, 1)
		if mutated == original {
			t.Fatalf("%s: the mutation did not change the record", name)
		}
		// The digest covers the moves, so decoding must already refuse.
		if _, err := DecodeRecord(mutated); err == nil {
			t.Errorf("%s: an altered record decoded without complaint", name)
			continue
		}
		// And with the digest recomputed, so that the text is internally
		// consistent, the independent checks must still catch it. This is the
		// case a plain checksum would miss.
		repaired := rec
		repaired.Moves = moves
		repaired.Digest = repaired.digest()
		if _, err := DecodeRecord(repaired.Encode()); err != nil {
			t.Errorf("%s: recomputed record should decode cleanly, got %v", name, err)
			continue
		}
		if _, err := repaired.Replay(); err == nil {
			t.Errorf("%s: a record with a recomputed digest replayed to a different game without complaint", name)
		}
	}
}

func TestRecordRejectsAlteredFields(t *testing.T) {
	g := playSample(t)
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	original := rec.Encode()

	for name, mutated := range map[string]string{
		"result flipped":      strings.Replace(original, "vertical-wins", "horizontal-wins", 1),
		"reason changed":      strings.Replace(original, "connection", "resignation", 1),
		"ruleset changed":     strings.Replace(original, "removal=true", "removal=false", 1),
		"board size changed":  strings.Replace(original, "size=8", "size=12", 1),
		"position digest bad": strings.Replace(original, "position ", "position 0000", 1),
		"digest bad":          strings.Replace(original, "digest ", "digest 0000", 1),
	} {
		if mutated == original {
			t.Fatalf("%s: the mutation did not change the record", name)
		}
		if _, err := DecodeRecord(mutated); err == nil {
			t.Errorf("%s: decoded without complaint", name)
		}
	}

	// A result line the digest agrees with but the moves do not is caught by
	// replaying, not by the digest.
	lying := rec
	lying.Outcome = HorizontalWins
	lying.Digest = lying.digest()
	decoded, err := DecodeRecord(lying.Encode())
	if err != nil {
		t.Fatalf("a self-consistent record should decode: %v", err)
	}
	if _, err := decoded.Replay(); err == nil {
		t.Error("a record whose claimed result its moves do not reach replayed without complaint")
	}
}

func TestRecordRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"nonsense",
		"twixtui-record\n",
		"twixtui-record 99\nruleset size=8;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true\n",
		"twixtui-record 1\nruleset size=8;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true\nresult ongoing\n",
		"twixtui-record 1\nunknown-field x\n",
	} {
		if _, err := DecodeRecord(bad); err == nil {
			t.Errorf("DecodeRecord(%q) should fail", bad)
		}
	}
}

// TestPositionDigestDistinguishesPositions checks the digest actually separates
// positions, since every other guarantee rests on it.
func TestPositionDigestDistinguishesPositions(t *testing.T) {
	rs := Std
	rs.Size = 10
	rng := rand.New(rand.NewPCG(3, 5))
	seen := map[string]string{}

	for trial := range 40 {
		g := MustNew(rs)
		for range 20 {
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
			d := PositionDigest(g)
			state := snapshot(g)
			if prev, ok := seen[d]; ok && prev != state {
				t.Fatalf("trial %d: two different positions share digest %s", trial, d)
			}
			seen[d] = state
		}
	}
	if len(seen) < 100 {
		t.Errorf("expected many distinct positions, saw %d", len(seen))
	}
}

// TestPositionDigestIsStableAcrossReplay checks the digest depends on the
// position and not on how it was reached, which is what makes it usable as a
// divergence check between two machines.
func TestPositionDigestIsStableAcrossReplay(t *testing.T) {
	g := playSample(t)
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := rec.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if a, b := PositionDigest(g), PositionDigest(replayed); a != b {
		t.Errorf("digest differs after replay: %s then %s", a, b)
	}
	// Undoing and redoing the last move must land on the same digest too.
	before := PositionDigest(g)
	last := g.History()[len(g.History())-1]
	if err := g.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	if PositionDigest(g) == before {
		t.Error("digest unchanged after undoing a move")
	}
	if _, err := g.PlayPeg(last.Peg); err != nil {
		t.Fatal(err)
	}
	if after := PositionDigest(g); after != before {
		t.Errorf("digest after undo and redo = %s, want %s", after, before)
	}
}
