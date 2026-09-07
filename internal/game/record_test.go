package game

import (
	"fmt"
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

// TestDecodeRejectsMoreThanOneRecord covers the file holding two records. It
// used to read as whichever record came last, with the bytes of the others kept
// beside it as though they had been checked — and handed back out again by
// whatever exported the game afterwards.
func TestDecodeRejectsMoreThanOneRecord(t *testing.T) {
	sound := encodeSample(t, playSample(t))
	rs := Std
	rs.Size = 24
	fresh := encodeSample(t, MustNew(rs))

	// The positive control: one record on its own is still read, so a decoder
	// that refused everything could not pass this test.
	if _, err := DecodeRecord(sound); err != nil {
		t.Fatalf("a single sound record was refused: %v", err)
	}

	for name, body := range map[string]string{
		"the same record twice":     sound + sound,
		"a second, different game":  sound + fresh,
		"three records then a wrap": sound + sound + sound + fresh,
		"one field written twice":   sound + "moves D1\n",
	} {
		if _, err := DecodeRecord(body); err == nil {
			t.Errorf("%s was read as one record", name)
		} else if !strings.Contains(err.Error(), "twice") {
			t.Errorf("%s was refused for something else: %v", name, err)
		}
		if _, _, err := LoadRecord(body); err == nil {
			t.Errorf("%s loaded as a game", name)
		}
	}

	// The same must hold inside the ruleset, which is its own field list.
	doubled := strings.Replace(sound, "ruleset size=8;", "ruleset size=8;size=8;", 1)
	if doubled == sound {
		t.Fatal("the fixture does not contain the ruleset field being doubled")
	}
	if _, err := DecodeRecord(doubled); err == nil {
		t.Error("a ruleset naming the board size twice was accepted")
	}
}

// TestDecodeRequiresTheMovesField pins the field that used to be optional. A
// record without it was refused for its digest, which reads as tampering rather
// than as the missing field it is; an empty move list, which is what a game
// nobody has played yet has, must still be read.
func TestDecodeRequiresTheMovesField(t *testing.T) {
	sound := encodeSample(t, playSample(t))
	var kept []string
	for _, line := range strings.Split(sound, "\n") {
		if !strings.HasPrefix(line, "moves ") {
			kept = append(kept, line)
		}
	}
	stripped := strings.Join(kept, "\n")
	if stripped == sound {
		t.Fatal("the fixture has no moves line to strip")
	}
	_, err := DecodeRecord(stripped)
	if err == nil {
		t.Fatal("a record with no moves field was accepted")
	}
	if !strings.Contains(err.Error(), "moves") {
		t.Errorf("the refusal reads %q, which does not name the missing field", err)
	}

	rs := Std
	rs.Size = 12
	unplayed := encodeSample(t, MustNew(rs))
	if _, _, err := LoadRecord(unplayed); err != nil {
		t.Errorf("a record of a game with no moves yet was refused: %v", err)
	}
}

// TestOversizedInputIsRefusedBeforeItIsHeld covers what a record loader does
// with something that is not a record: a device that never ends, or a file
// large enough that reading it whole is the problem. The bound has to apply
// while reading, so the assertion is on how much was read as well as on the
// refusal.
func TestOversizedInputIsRefusedBeforeItIsHeld(t *testing.T) {
	// A run of junk is refused by the parser whatever the limit is, so the
	// oversized case has to be a record the parser would otherwise accept: a
	// sound one padded past the limit with comment lines, which decoding
	// skips. Only the size check can refuse this.
	sound := encodeSample(t, playSample(t))
	padding := strings.Repeat("# padding\n", 1+(MaxRecordBytes-len(sound))/len("# padding\n"))
	oversized := sound + padding
	if len(oversized) <= MaxRecordBytes {
		t.Fatalf("the padded record is %d bytes, which does not exceed the %d-byte limit", len(oversized), MaxRecordBytes)
	}
	if _, err := DecodeRecord(oversized); err == nil {
		t.Error("a record larger than the limit was decoded")
	} else if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("the refusal reads %q, which does not name the size", err)
	}
	if _, _, err := ReadRecord(strings.NewReader(oversized)); err == nil {
		t.Error("a stream larger than the limit was read as a record")
	}
	// The same record inside the limit still loads, so the refusal above is
	// the size and not the padding.
	if _, _, err := LoadRecord(sound + "# padding\n"); err != nil {
		t.Errorf("a commented record inside the limit was refused: %v", err)
	}

	endless := &countingReader{}
	if _, _, err := ReadRecord(endless); err == nil {
		t.Error("an input that never ends was read as a record")
	}
	if endless.read > MaxRecordBytes+1 {
		t.Errorf("ReadRecord took %d bytes from an endless input, which is past the %d-byte limit",
			endless.read, MaxRecordBytes+1)
	}

	if _, _, err := ReadRecord(strings.NewReader(sound)); err != nil {
		t.Errorf("a sound record read from a stream was refused: %v", err)
	}
}

// countingReader is an input that never ends, like a character device, and
// remembers how much of it was taken.
type countingReader struct{ read int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'Z'
	}
	r.read += len(p)
	return len(p), nil
}

// TestDiagnosticsDoNotRepeatTheInput covers every quoted diagnostic a record
// can reach. A record comes from a file or a pipe somebody else wrote, so an
// error that echoes the offending text back in full is a copy of the input —
// and escaping expands unprintable bytes several times over on the way to the
// terminal.
func TestDiagnosticsDoNotRepeatTheInput(t *testing.T) {
	const bulk = 16 << 10
	junk := strings.Repeat("Z", bulk)
	rs := Std
	rs.Size = 8
	played, err := playSample(t).Record()
	if err != nil {
		t.Fatal(err)
	}
	sound := played.Encode()

	// A record whose digest matches its contents, so that replay does the
	// refusing and its own diagnostics are reached at all.
	consistent := func(r Record) string {
		r.Version = RecordVersion
		r.Digest = r.digest()
		return r.Encode()
	}
	// swap replaces one piece of the sound record, failing the test rather than
	// quietly producing a valid record if the fixture ever stops containing it.
	swap := func(old, new string) string {
		body := strings.Replace(sound, old, new, 1)
		if body == sound {
			t.Fatalf("the fixture does not contain %q", old)
		}
		return body
	}

	cases := []struct{ name, body string }{
		{"an unknown field", junk + "\n"},
		{"unprintable bytes", strings.Repeat("\x00", bulk)},
		{"a bad version", "twixtui-record " + junk + "\n"},
		{"a bad entry count", swap(fmt.Sprintf("entries %d", played.Entries), "entries "+junk)},
		{"a stale digest", swap("digest ", "digest "+junk)},
		{"a bad ruleset value", swap("size=8;", "size="+junk+";")},
		{"an unknown outcome", swap("result "+outcomeNames[played.Outcome], "result "+junk)},
		{"an unknown end reason", swap(reasonNames[played.Reason], junk)},
		{"a move that is junk", consistent(Record{Ruleset: rs, Moves: junk, Outcome: Ongoing, Reason: NotOver, Position: "0", Entries: 0})},
		{"a claimed position", consistent(Record{Ruleset: rs, Moves: "", Outcome: Ongoing, Reason: NotOver, Position: junk, Entries: 0})},
	}
	for _, c := range cases {
		_, _, err := LoadRecord(c.body)
		if err == nil {
			t.Errorf("%s was accepted", c.name)
			continue
		}
		if len(err.Error()) > 512 {
			t.Errorf("%s produced a %d-byte diagnostic:\n%.200s…", c.name, len(err.Error()), err)
		}
		if strings.Contains(err.Error(), strings.Repeat("Z", 200)) {
			t.Errorf("%s repeated its input back: %.200s…", c.name, err)
		}
	}
}

// TestAnOrdinaryFullBoardRecordFitsTheLimit is the control on MaxRecordBytes:
// the limit exists to stop a loader materialising something that is not a
// record, and it is worth nothing if it also refuses a real game on the widest
// board this build offers. The projection from a sampled game's own density is
// what makes this a check on the limit rather than on the sample: the sample is
// a few hundred entries, a filled board is 2304.
func TestAnOrdinaryFullBoardRecordFitsTheLimit(t *testing.T) {
	rs := Std
	rs.Size = MaxSize
	g := MustNew(rs)
	rng := rand.New(rand.NewPCG(11, 13))
	for range 200 {
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
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	encoded := rec.Encode()
	if len(encoded) >= MaxRecordBytes {
		t.Fatalf("a %d-entry game on a %dx%d board encodes to %d bytes, past the %d-byte limit",
			rec.Entries, rs.Size, rs.Size, len(encoded), MaxRecordBytes)
	}
	if _, _, err := LoadRecord(encoded); err != nil {
		t.Fatalf("the sampled record does not load: %v", err)
	}

	if rec.Entries == 0 {
		t.Fatal("the sample played no entries, so there is nothing to project from")
	}
	perEntry := (len(rec.Moves) + rec.Entries - 1) / rec.Entries
	overhead := len(encoded) - len(rec.Moves)
	filled := overhead + perEntry*rs.Size*rs.Size
	if filled >= MaxRecordBytes {
		t.Errorf("at %d bytes an entry, a filled %dx%d board projects to %d bytes, which the %d-byte limit would refuse",
			perEntry, rs.Size, rs.Size, filled, MaxRecordBytes)
	}
}

// encodeSample is the encoded record of a game, for the tests that then damage
// it in one specific way.
func encodeSample(t *testing.T, g *Game) string {
	t.Helper()
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode()
}
