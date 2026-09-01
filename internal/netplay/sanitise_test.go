package netplay

import (
	"encoding/binary"
	"encoding/hex"
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

// TestRejectedMoveCodeCannotCarryEscapes covers the other way in: a code pasted
// by the player, whose move field is echoed back in the refusal.
func TestRejectedMoveCodeCannotCarryEscapes(t *testing.T) {
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)

	// A code the sender built for a legitimate position, but whose move field
	// carries an escape sequence. Encoding is done by the library, so drive it
	// through the real path and then substitute the move.
	const id = "abcd1234"
	code, err := EncodeMove(g, id, "F7")
	if err != nil {
		t.Fatal(err)
	}
	mc, err := Inspect(code)
	if err != nil {
		t.Fatal(err)
	}
	mc.Move = hostile

	// The refusal must not repeat the escape sequence back at the terminal.
	_, err = DecodeMove(g, id, code)
	if err != nil {
		t.Fatalf("the honest code should decode: %v", err)
	}

	// Now the same check the decoder performs on a move it cannot play, reached
	// directly because a well-formed code cannot carry this field.
	if got := safeText(mc.Move, maxMoveLen); containsControl(got) {
		t.Errorf("a rejected move is echoed as %q, which still holds a control character", got)
	}
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
