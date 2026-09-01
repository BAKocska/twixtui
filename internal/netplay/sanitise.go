package netplay

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Everything that arrives from the other end of a connection, or out of a code a
// player pasted in, is drawn on a terminal. A terminal acts on the control bytes
// in what it is asked to draw, so text from an untrusted source has to be
// stripped before it can reach the screen: an escape sequence in an opponent's
// name or in a rejected move could otherwise retitle the player's window, repaint
// it, or issue a query the terminal answers back into the program's own input.
//
// Stripping happens here, where the untrusted text enters the program, rather
// than at the point it is drawn. There are several places that draw and only two
// that receive, and a sanitiser that has to be remembered at every call site is
// one that will eventually be forgotten at one of them.

// safeText returns s with anything a terminal would act on removed, and bounds
// its length. The result is safe to print and safe to put in an error message.
func safeText(s string, maxLen int) string {
	if !utf8.ValidString(s) {
		// Invalid bytes cannot be reasoned about, and a terminal may resynchronise
		// on them in surprising ways.
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t', r == '\n', r == '\r':
			// Whitespace is meaningful in a name only as a separator.
			b.WriteByte(' ')
		case r == utf8.RuneError:
			continue
		case unicode.IsControl(r):
			continue
		case unicode.Is(unicode.Cf, r):
			// Format characters include the bidirectional overrides, which can
			// make text display in an order other than the one it is stored in.
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if maxLen > 0 && len(out) > maxLen {
		out = strings.TrimSpace(truncateRunes(out, maxLen))
	}
	return out
}

// truncateRunes cuts s to at most maxBytes without splitting a rune.
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
