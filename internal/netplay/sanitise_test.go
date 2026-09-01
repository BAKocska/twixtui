package netplay

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/BAKocska/twixtui/internal/game"
)

// hostile is a string that makes a terminal act rather than draw: it sets the
// window title, then turns the text red. A security review found it reaching the
// screen through both an opponent's name and a rejected move code.
const hostile = "E5 \x1b]0;OWNED\x07\x1b[31mred"

// containsControl reports whether s holds anything a terminal would act on.
func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

func TestSafeTextRemovesEverythingATerminalWouldActOn(t *testing.T) {
	cases := []string{
		hostile,
		"\x1b[2J\x1b[H",                  // clear the screen and home the cursor
		"\x1b]0;title\x07",               // set the window title
		"\x1b[6n",                        // ask the terminal to report the cursor, which it types back
		"name\x00with\x01control\x7f",    // C0 and delete
		"bidi\u202eoverride",             // reverse the display order of what follows
		"zero\u200bwidth",                // invisible
		"carriage\rreturn\nand\ttabs",    // whitespace that moves the cursor
		"\xff\xfe invalid utf-8",         // bytes that are not text
		strings.Repeat("\x1b[31m", 1000), // a lot of it
	}
	for _, in := range cases {
		out := safeText(in, 64)
		if containsControl(out) {
			t.Errorf("safeText(%q) = %q, which still holds a control character", in, out)
		}
		if !utf8.ValidString(out) {
			t.Errorf("safeText(%q) = %q, which is not valid text", in, out)
		}
		if len(out) > 64 {
			t.Errorf("safeText(%q) returned %d bytes, past the bound", in, len(out))
		}
	}
}

func TestSafeTextKeepsOrdinaryNames(t *testing.T) {
	for _, name := range []string{
		"Balint", "Zsófia", "Jean-Luc", "O'Brien", "山田", "Ada Lovelace",
	} {
		if got := safeText(name, 64); got != name {
			t.Errorf("safeText(%q) = %q, which changed an ordinary name", name, got)
		}
	}
	// Runs of whitespace collapse, which is what a name field wants.
	if got := safeText("  Ada   Lovelace  ", 64); got != "Ada Lovelace" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
	// The bound must not split a rune.
	long := strings.Repeat("é", 100)
	got := safeText(long, 11)
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

// TestOpponentNameCannotCarryEscapes checks the sanitiser is actually wired into
// the handshake, not merely available. An opponent chooses their own name, and it
// is drawn on the player's terminal.
func TestOpponentNameCannotCarryEscapes(t *testing.T) {
	if got := cleanName(hostile); containsControl(got) {
		t.Errorf("cleanName(%q) = %q, which still holds a control character", hostile, got)
	}
	if got := cleanName("\x1b]0;title\x07Mallory"); !strings.Contains(got, "Mallory") {
		t.Errorf("cleanName threw away the readable part of the name: %q", got)
	}
}

// TestRejectedMoveCodeCannotCarryEscapes covers the other way in: a code the
// player pasted because a stranger sent it. The length byte of a move code
// allows 255 bytes of anything in the move field, and that field leaves the
// package twice: through Inspect, whose result callers print to say what a code
// holds, and inside the refusal when the move turns out to be unplayable.
//
// The code is built by hand. EncodeMove would refuse this notation, and it is
// the paste that is being modelled, not our own output: the sender is not
// obliged to have used our encoder.
func TestRejectedMoveCodeCannotCarryEscapes(t *testing.T) {
	const id = "abcd1234"
	g := game.MustNew(testRules())
	code := forgedMoveCode(t, g, id, hostile)

	mc, err := Inspect(code)
	if err != nil {
		t.Fatalf("the forged code should still parse: %v", err)
	}
	assertSafeForATerminal(t, "the move Inspect returned", mc.Move, maxNotationLen)
	if !strings.Contains(mc.Move, "red") {
		t.Errorf("the whole move was thrown away rather than filtered: %q", mc.Move)
	}

	_, err = DecodeMove(g, id, code)
	if !errors.Is(err, ErrBadCode) || !strings.Contains(err.Error(), "cannot be played here") {
		t.Fatalf("DecodeMove returned %v, want a refusal naming the move it could not play", err)
	}
	assertSafeForATerminal(t, "the refusal", err.Error(), 512)
	if g.Entries() != 0 {
		t.Fatalf("the forged code changed the game to %d entries", g.Entries())
	}
}

// forgedMoveCode carries notation that EncodeMove would not write. Everything
// before the length byte binds the code to this game and this position, which
// is what gets the move as far as being tried.
func forgedMoveCode(t *testing.T, g *game.Game, id, notation string) string {
	t.Helper()
	honest, err := EncodeMove(g, id, "B1")
	if err != nil {
		t.Fatalf("encoding the code whose header is reused: %v", err)
	}
	payload, err := parseCode(honest, movePrefix)
	if err != nil {
		t.Fatalf("parsing that code: %v", err)
	}
	body := append([]byte(nil), payload[:moveCodeHeaderLen-1]...)
	body = append(body, byte(len(notation)))
	body = append(body, notation...)
	return formatCode(movePrefix, binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body)))
}

// TestDecodedInviteNameCannotCarryEscapes checks the sanitiser runs when an
// invite is read, not only when one is written.
//
// The encoder filters the name it writes, so a hostile invite cannot be produced
// through it. That is exactly why the decoder has to filter too: the sender is
// not obliged to use our encoder. The code below is forged the way such a sender
// would forge one, with a correct checksum over an unfiltered name.
func TestDecodedInviteNameCannotCarryEscapes(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	fingerprint, err := hex.DecodeString(rs.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	const id = "abcd1234"

	var flags byte
	for _, f := range []struct {
		on   bool
		mask byte
	}{
		{rs.DeliberateLinking, flagDeliberate},
		{rs.LinkRemoval, flagLinkRemoval},
		{rs.PegRemoval, flagPegRemoval},
		{rs.OwnLinksMayCross, flagOwnCross},
		{rs.Swap, flagSwap},
	} {
		if f.on {
			flags |= f.mask
		}
	}

	payload := []byte{codeVersion, byte(rs.Size), flags, byte(game.Vertical)}
	payload = append(payload, fingerprint...)
	payload = append(payload, byte(len(id)))
	payload = append(payload, id...)
	payload = append(payload, byte(len(hostile)))
	payload = append(payload, hostile...)
	payload = binary.BigEndian.AppendUint32(payload, crc32.ChecksumIEEE(payload))
	code := formatCode(invitePrefix, payload)

	got, err := DecodeInvite(code)
	if err != nil {
		t.Fatalf("the forged invite should still decode: %v", err)
	}
	if containsControl(got.HostName) {
		t.Errorf("decoded host name = %q, which still holds a control character", got.HostName)
	}
	if got.HostName == "" {
		t.Error("the whole name was thrown away rather than filtered")
	}
}
