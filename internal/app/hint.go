package app

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
)

// Fixed text of the advice block. These four strings, plus the engine's own
// Headline and Detail, are everything the interface ever says about a hint.
const (
	hintLabel     = "hint"
	hintSearching = "asking the engine"
	hintNoAdvice  = "no advice is available"
	hintOff       = "hints are not available in this game"
)

// hintMsg is the outcome of one hint search. gen names the search it answers,
// so a result the player has already moved past is dropped instead of shown
// against a position it was not computed for.
type hintMsg struct {
	gen  int
	hint bot.Hint
	err  error
}

// hintPanel is the advice state of a game screen.
//
// The explanation shown to the player is bot.Hint's Headline and Detail
// verbatim. The panel adds a label, wraps the text to the panel width and
// nothing else: the bot derives its prose from the search it actually ran, so
// any sentence composed here would be a claim with nothing behind it. When the
// engine reports an error, or returns an explanation with no text in it, the
// panel says so rather than filling the gap.
type hintPanel struct {
	gen     int
	running bool
	shown   bool
	hint    bot.Hint

	// unavailable is the sentence shown when there is no advice: the fixed
	// hintNoAdvice text, with the engine's own error appended when it gave one.
	unavailable string

	cancel context.CancelFunc
}

// ask starts a search. pos must be a position the caller no longer mutates,
// because the search runs on another goroutine.
func (h *hintPanel) ask(engine bot.Bot, pos *game.Game) tea.Cmd {
	h.stop()
	h.gen++
	gen := h.gen
	h.running = true
	h.shown = false
	h.unavailable = ""

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	return func() tea.Msg {
		hint, err := engine.Hint(ctx, pos)
		return hintMsg{gen: gen, hint: hint, err: err}
	}
}

// apply takes in a finished search. A message from an earlier search is
// ignored, which is what keeps a stale explanation off a newer position.
func (h *hintPanel) apply(msg hintMsg) {
	if msg.gen != h.gen {
		return
	}
	h.running = false
	h.stop()
	if msg.err != nil {
		h.unavailable = hintNoAdvice + ": " + msg.err.Error()
		return
	}
	if strings.TrimSpace(msg.hint.Headline) == "" && strings.TrimSpace(msg.hint.Detail) == "" {
		h.unavailable = hintNoAdvice
		return
	}
	h.hint = msg.hint
	h.shown = true
}

// clear drops the advice, which the screen does whenever the position changes:
// an explanation of a position that is no longer on the board is worse than no
// explanation at all.
func (h *hintPanel) clear() {
	h.stop()
	h.gen++
	h.running = false
	h.shown = false
	h.hint = bot.Hint{}
	h.unavailable = ""
}

// stop cancels a search in flight, which is how leaving the screen or moving on
// stops the engine working on advice nobody will read.
func (h *hintPanel) stop() {
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

// active reports whether the panel has anything to show.
func (h hintPanel) active() bool { return h.running || h.shown || h.unavailable != "" }

// highlights are the holes the advice refers to: the recommended move first,
// then the holes the engine's own explanation names.
func (h hintPanel) highlights() []game.Point {
	if !h.shown {
		return nil
	}
	out := make([]game.Point, 0, len(h.hint.Highlight)+1)
	out = append(out, h.hint.Move)
	for _, p := range h.hint.Highlight {
		if p != h.hint.Move {
			out = append(out, p)
		}
	}
	return out
}

// lines renders the advice block for a panel of the given width.
//
// The whole of the position-specific text is Headline and Detail as the engine
// wrote them, wrapped on spaces. gameScreen has no other route to the panel for
// hint text, so a claim the search did not make cannot appear here.
func (h hintPanel) lines(width int) []string {
	if !h.active() {
		return nil
	}
	out := []string{hintLabel}
	switch {
	case h.running:
		out = append(out, gsWrap(hintSearching, width)...)
	case h.unavailable != "":
		out = append(out, gsWrap(h.unavailable, width)...)
	default:
		out = append(out, gsWrap(h.hint.Headline, width)...)
		if strings.TrimSpace(h.hint.Detail) != "" {
			out = append(out, gsWrap(h.hint.Detail, width)...)
		}
	}
	return out
}

// statusText is the one-line form for a terminal too narrow for a panel.
func (h hintPanel) statusText() string {
	switch {
	case h.running:
		return hintLabel + ": " + hintSearching
	case h.unavailable != "":
		return h.unavailable
	case h.shown:
		return hintLabel + ": " + h.hint.Headline
	}
	return ""
}
