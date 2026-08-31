package netplay

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// TestCorrespondenceFullGame plays the same complete game as the live
// transports, but the only thing passed between its two boards is a move code.
func TestCorrespondenceFullGame(t *testing.T) {
	id := "GAME-ONE"
	sender := game.MustNew(testRules())
	receiver := game.MustNew(testRules())
	for i, entry := range scriptedGame {
		if err := applyEntry(sender, entry.Side, entry.Move); err != nil {
			t.Fatalf("playing entry %d at the sender: %v", i+1, err)
		}
		code, err := EncodeLastMove(sender, id)
		if err != nil {
			t.Fatalf("encoding entry %d: %v", i+1, err)
		}
		if !strings.HasPrefix(code, movePrefix+"-") {
			t.Fatalf("entry %d produced %q", i+1, code)
		}
		info, err := Inspect(code)
		if err != nil {
			t.Fatalf("inspecting entry %d: %v", i+1, err)
		}
		if info.Game != GameDigest(id) || info.Entries != i || info.Side != entry.Side || info.Move != entry.Move {
			t.Fatalf("entry %d inspected as %+v", i+1, info)
		}
		decoded, err := DecodeMove(receiver, id, strings.ToLower(code))
		if err != nil {
			t.Fatalf("decoding entry %d: %v", i+1, err)
		}
		if decoded != entry.Move {
			t.Fatalf("entry %d decoded as %q", i+1, decoded)
		}
		// Decode validates fully but does not change the caller's game.
		if receiver.Entries() != i {
			t.Fatalf("DecodeMove changed the receiver to %d entries", receiver.Entries())
		}
		applied, err := ApplyMove(receiver, id, code)
		if err != nil {
			t.Fatalf("applying entry %d: %v", i+1, err)
		}
		if applied != entry.Move {
			t.Fatalf("entry %d applied as %q", i+1, applied)
		}
		if a, b := PositionHash(sender), PositionHash(receiver); a != b {
			t.Fatalf("after entry %d the sender has %s and receiver %s", i+1, a, b)
		}
	}
	if got := receiver.Result().Outcome; got != game.VerticalWins {
		t.Fatalf("the receiver's outcome is %v", got)
	}
}

// TestCorrespondenceCodeRejectsCorruption flips one base32 character. The
// checksum has to catch it before the changed payload can be believed as a move.
func TestCorrespondenceCodeRejectsCorruption(t *testing.T) {
	g := game.MustNew(testRules())
	code, err := EncodeMove(g, "GAME", "B1")
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	corrupted := corruptCode(code)
	_, err = DecodeMove(g, "GAME", corrupted)
	if !errors.Is(err, ErrBadCode) {
		t.Fatalf("decoding the corrupted code returned %v", err)
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("the error does not say the paste was corrupted: %v", err)
	}
	if g.Entries() != 0 {
		t.Fatalf("the corrupted code changed the game to %d entries", g.Entries())
	}
}

func corruptCode(code string) string {
	b := []byte(code)
	for i := len(movePrefix) + 1; i < len(b); i++ {
		if b[i] == '-' {
			continue
		}
		if b[i] != '0' {
			b[i] = '0'
		} else {
			b[i] = '1'
		}
		return string(b)
	}
	return code + "0"
}

func TestCorrespondenceCodeRejectsMissingGameID(t *testing.T) {
	g := game.MustNew(testRules())
	if _, err := EncodeMove(g, "", "B1"); err == nil || !strings.Contains(err.Error(), "game identifier") {
		t.Fatalf("EncodeMove returned %v", err)
	}
}

func TestCorrespondenceCodeRejectsHugePasteBeforeDecoding(t *testing.T) {
	g := game.MustNew(testRules())
	huge := movePrefix + strings.Repeat("A", maxCodeTextLen+1)
	_, err := DecodeMove(g, "GAME", huge)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "character limit") {
		t.Fatalf("DecodeMove returned %v", err)
	}
}

// TestCorrespondenceCodeIsCaseInsensitiveAndTypoResistant accepts Crockford's
// common substitutions I/L for 1 and O for 0 without weakening the checksum:
// those substitutions decode to the very same bytes.
func TestCorrespondenceCodeIsCaseInsensitiveAndTypoResistant(t *testing.T) {
	g := game.MustNew(testRules())
	var code string
	for i := range 256 {
		id := string(rune(i))
		var err error
		code, err = EncodeMove(g, id, "B1")
		if err != nil {
			t.Fatalf("encoding: %v", err)
		}
		if strings.ContainsAny(code, "01") {
			break
		}
	}
	if !strings.ContainsAny(code, "01") {
		t.Fatal("could not produce a code containing 0 or 1")
	}

	variant := strings.ToLower(code)
	variant = strings.ReplaceAll(variant, "0", "o")
	variant = strings.ReplaceAll(variant, "1", "l")
	info, err := Inspect(variant)
	if err != nil {
		t.Fatalf("inspecting %q: %v", variant, err)
	}
	original, err := Inspect(code)
	if err != nil {
		t.Fatalf("inspecting original: %v", err)
	}
	if info != original {
		t.Fatalf("variant inspected as %+v, original as %+v", info, original)
	}
}

func TestCorrespondenceCodeRefusesWrongGame(t *testing.T) {
	g := game.MustNew(testRules())
	code, err := EncodeMove(g, "ALPHA", "B1")
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	_, err = DecodeMove(g, "BETA", code)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "different game") {
		t.Fatalf("got %v", err)
	}
}

// TestCorrespondenceCodeRefusesWrongPosition puts two games at the same record
// length but on different boards. The pre-move position hash, not merely the
// count, is what refuses the code.
func TestCorrespondenceCodeRefusesWrongPosition(t *testing.T) {
	id := "ONE-GAME"
	positionA := build(t, v("B1"))
	positionB := build(t, v("C3"))
	code, err := EncodeMove(positionA, id, "A2")
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	before := PositionHash(positionB)
	_, err = DecodeMove(positionB, id, code)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "different position") {
		t.Fatalf("got %v", err)
	}
	if got := PositionHash(positionB); got != before {
		t.Fatalf("decoding changed the wrong board from %s to %s", before, got)
	}
}

func TestCorrespondenceCodeRefusesStaleCode(t *testing.T) {
	id := "ONE-GAME"
	g := game.MustNew(testRules())
	code, err := EncodeMove(g, id, "B1")
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if _, err := ApplyMove(g, id, code); err != nil {
		t.Fatalf("first application: %v", err)
	}
	_, err = ApplyMove(g, id, code)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second application returned %v", err)
	}
}

// TestDecodeRejectsWrongResultingHash rebuilds a valid code with one result-hash
// byte changed and a new checksum. This is not a mangled paste: it is a hostile
// but checksummed claim about the result. Decode itself must trial-apply and
// refuse it, without modifying the game.
func TestDecodeRejectsWrongResultingHash(t *testing.T) {
	id := "ONE-GAME"
	g := game.MustNew(testRules())
	code, err := EncodeMove(g, id, "B1")
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	payload, err := parseCode(code, movePrefix)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	payload[12] ^= 0xff // the first byte of the resulting position hash
	body := payload[:len(payload)-codeChecksumLen]
	binary.BigEndian.PutUint32(payload[len(body):], crc32.ChecksumIEEE(body))
	hostile := formatCode(movePrefix, payload)

	before := PositionHash(g)
	_, err = DecodeMove(g, id, hostile)
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("DecodeMove returned %v", err)
	}
	if got := PositionHash(g); got != before || g.Entries() != 0 {
		t.Fatalf("DecodeMove changed the game: %s, %d entries", got, g.Entries())
	}
}

// TestEncodeLastMoveRebuildsOutOfTurnPreState covers the exact reason it cannot
// use UndoLastMove: a resignation from the side that is not to move.
func TestEncodeLastMoveRebuildsOutOfTurnPreState(t *testing.T) {
	id := "OUT-OF-TURN"
	sender := build(t, v("B1")) // horizontal to move
	if err := sender.Resign(game.Vertical); err != nil {
		t.Fatalf("vertical resigning: %v", err)
	}
	code, err := EncodeLastMove(sender, id)
	if err != nil {
		t.Fatalf("encoding the resignation: %v", err)
	}
	receiver := build(t, v("B1"))
	move, err := ApplyMove(receiver, id, code)
	if err != nil {
		t.Fatalf("applying the resignation: %v", err)
	}
	if move != "v:resign" {
		t.Fatalf("the move was %q", move)
	}
	if a, b := PositionHash(sender), PositionHash(receiver); a != b {
		t.Fatalf("sender %s, receiver %s", a, b)
	}
}

// TestCodeBindsStandingDrawOfferToPrePosition proves a normal move code cannot
// be applied to a board where the standing draw offer differs. A move can clear
// that offer, so checking only the resulting hash would be insufficient.
func TestCodeBindsStandingDrawOfferToPrePosition(t *testing.T) {
	id := "DRAW-OFFER"
	sender := build(t, v("B1"))
	receiver := build(t, v("B1"))
	if err := sender.OfferDraw(game.Vertical); err != nil {
		t.Fatalf("offering draw: %v", err)
	}
	if err := receiver.OfferDraw(game.Vertical); err != nil {
		t.Fatalf("offering draw at receiver: %v", err)
	}
	if err := sender.PlayNotation("A2"); err != nil {
		t.Fatalf("horizontal moving: %v", err)
	}
	code, err := EncodeLastMove(sender, id)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if _, err := ApplyMove(receiver, id, code); err != nil {
		t.Fatalf("applying with the same offer: %v", err)
	}
	if got := receiver.DrawOfferedBy(); got != game.NoPlayer {
		t.Fatalf("the move did not clear the opponent's offer: %s", got)
	}

	wrong := build(t, v("B1"))
	if err := wrong.OfferDraw(game.Horizontal); err != nil {
		t.Fatalf("offering the wrong side's draw: %v", err)
	}
	_, err = DecodeMove(wrong, id, code)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "different position") {
		t.Fatalf("the code applied with a different standing offer: %v", err)
	}
}

func TestEncodeLastMoveOnFreshGameIsClear(t *testing.T) {
	_, err := EncodeLastMove(game.MustNew(testRules()), "GAME")
	if err == nil || !strings.Contains(err.Error(), "no moves yet") {
		t.Fatalf("got %v", err)
	}
}

// TestInviteRoundTrip carries the rules, the explicit host side, the name and a
// fresh game identifier into a compact code, and rejects a corrupted copy.
func TestInviteRoundTrip(t *testing.T) {
	inv, err := NewInvite(testRules(), game.Horizontal, "Ada Lovelace")
	if err != nil {
		t.Fatalf("new invite: %v", err)
	}
	code, err := EncodeInvite(inv)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if !strings.HasPrefix(code, invitePrefix+"-") {
		t.Fatalf("invite was %q", code)
	}
	got, err := DecodeInvite(strings.ToLower(code))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got != inv {
		t.Fatalf("decoded %+v, want %+v", got, inv)
	}
	if got.GuestSide() != game.Vertical {
		t.Fatalf("the guest side is %s", got.GuestSide())
	}

	if _, err := DecodeInvite(corruptCode(code)); !errors.Is(err, ErrBadCode) {
		t.Fatalf("corrupted invite returned %v", err)
	}
	if _, err := Inspect(code); !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "game invite") {
		t.Fatalf("using an invite as a move returned %v", err)
	}
}

func TestInviteRejectsUnknownRuleFlags(t *testing.T) {
	inv, err := NewInvite(testRules(), game.Vertical, "Ada")
	if err != nil {
		t.Fatalf("new invite: %v", err)
	}
	code, err := EncodeInvite(inv)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	payload, err := parseCode(code, invitePrefix)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	payload[2] |= 0x80
	body := payload[:len(payload)-codeChecksumLen]
	binary.BigEndian.PutUint32(payload[len(body):], crc32.ChecksumIEEE(body))
	hostile := formatCode(invitePrefix, payload)
	_, err = DecodeInvite(hostile)
	if !errors.Is(err, ErrRuleset) || !strings.Contains(err.Error(), "does not understand") {
		t.Fatalf("DecodeInvite returned %v", err)
	}
}

func TestCorrespondenceTranscriptRejectsHugeBlock(t *testing.T) {
	g := game.MustNew(testRules())
	block := strings.Repeat("\n", maxTranscriptBytes+1)
	_, err := ApplyTranscript(g, "GAME", block)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "transcript") {
		t.Fatalf("ApplyTranscript returned %v", err)
	}
}

// TestCorrespondenceTranscriptFallback round-trips the whole record as a block
// of codes. It is the disjoint fallback after a live connection cannot be
// restored: the players can pass the block over any text channel.
func TestCorrespondenceTranscriptFallback(t *testing.T) {
	id := "FULL-RECORD"
	block, err := EncodeTranscript(id, testRules(), scriptedGame)
	if err != nil {
		t.Fatalf("encoding transcript: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(block), "\n") + 1; got != len(scriptedGame) {
		t.Fatalf("block has %d lines, want %d", got, len(scriptedGame))
	}
	g := game.MustNew(testRules())
	added, err := ApplyTranscript(g, id, block)
	if err != nil {
		t.Fatalf("applying transcript: %v", err)
	}
	if len(added) != len(scriptedGame) {
		t.Fatalf("added %d entries", len(added))
	}
	want := build(t, scriptedGame...)
	if a, b := PositionHash(g), PositionHash(want); a != b {
		t.Fatalf("transcript led to %s, want %s", a, b)
	}
}
