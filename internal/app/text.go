package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// One wrapper and one truncator for the whole package.
//
// There were four wrappers, two of which called ansi.Wrap directly. That is not
// safe: x/ansi documents a hyphen as always being a break point and appends it
// without re-checking the limit, so Wrap returns lines one or two cells past the
// width it was given, depending on where the hyphens fall. Measured on the
// notation paragraph of docs/rules.md it overshoots at widths 76 and 77 among
// others. A line wider than the frame is then cut mid-word by the layout, which
// is how it reaches the player.
//
// Wrapping and truncating are also where a panel decides what a player can read,
// so having one of each means a fix lands everywhere rather than in whichever
// copy was remembered.

// wrapText breaks text to fit width, keeping whole words where it can. A word
// longer than the width is cut rather than allowed to overflow, because a line
// wider than the frame corrupts the display. The result is never wider than
// width.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			// An over-long word is cut into width-sized pieces.
			for ansi.StringWidth(word) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				head := ansi.Truncate(word, width, "")
				out = append(out, head)
				word = strings.TrimPrefix(word, head)
				if head == "" {
					// Truncate could not make progress, which would loop.
					word = ""
				}
			}
			if word == "" {
				continue
			}
			switch {
			case line == "":
				line = word
			case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// ellipsis marks text that did not fit. A single character is used rather than
// three dots so that one cell buys the whole signal on a narrow panel.
const ellipsis = "…"

// truncateText cuts text to width, marking the cut so the player can tell
// something was left out. Hard-clipping without a mark is what made panel lines
// read as though the product had mangled them: "bot: beginner · left-" looks like
// a bug, whereas "bot: beginner · left…" reads as a shortened line.
func truncateText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	if width == 1 {
		return ellipsis
	}
	return ansi.Truncate(text, width, ellipsis)
}

// fitLabelled shortens a line built from a subject and a parenthetical gloss,
// dropping the gloss before it starts cutting the subject.
//
// The panel's seat lines are the case: "bot: beginner · left-right" is a name
// and a reminder of which edges that side joins, and losing the reminder costs a
// player far less than losing half the opponent's name.
func fitLabelled(subject, gloss, separator string, width int) string {
	full := subject
	if gloss != "" {
		full = subject + separator + gloss
	}
	if ansi.StringWidth(full) <= width {
		return full
	}
	if ansi.StringWidth(subject) <= width {
		return subject
	}
	return truncateText(subject, width)
}
