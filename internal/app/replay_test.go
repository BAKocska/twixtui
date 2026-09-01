package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/ui"
)

// rpSaveGame stores a game with enough moves in it that stepping, jumping five
// and going to the ends are all distinguishable.
func rpSaveGame(t *testing.T, d Deps) gamestore.Saved {
	t.Helper()
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	// Middle holes only, so neither side is refused a border row or column, and
	// no two of them are a knight's move apart, so no link is ever offered.
	for _, mv := range []string{"D4", "F5", "D7", "F8", "H4", "J5", "H7", "J8"} {
		if err := g.PlayNotation(mv); err != nil {
			t.Fatalf("playing %s: %v", mv, err)
		}
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	sv := gamestore.Saved{
		ID:       gamestore.NewID(),
		Kind:     gamestore.VersusBot,
		Created:  time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		Player:   "Balint",
		Side:     "vertical",
		Opponent: "bot:beginner",
		Record:   rec.Encode(),
	}
	if err := d.Games.Put(sv); err != nil {
		t.Fatal(err)
	}
	return sv
}

// rpScreen opens a replay sized for a terminal and parked in the middle of the
// record, so a key that moves either way has room to show it.
func rpScreen(t *testing.T, d Deps, sv gamestore.Saved, w, h int) *ReplayScreen {
	t.Helper()
	scr, err := NewReplayScreen(d, sv)
	if err != nil {
		t.Fatalf("NewReplayScreen: %v", err)
	}
	s, ok := scr.(*ReplayScreen)
	if !ok {
		t.Fatalf("NewReplayScreen returned %T", scr)
	}
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	s.at = len(s.positions) / 2
	return s
}

// rpKeys is the alphabet a probe presses: everything the shared keymap binds on
// the board, the plain special keys, and a spread of letters and digits that
// nothing is expected to answer.
func rpKeys(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if seen[k] {
			return
		}
		if got := tutorialKeyMsg(k).String(); got != k {
			t.Fatalf("the probe cannot press %q: it builds as %q", k, got)
		}
		seen[k] = true
		keys = append(keys, k)
	}
	for _, b := range ui.DefaultKeymap() {
		for _, k := range b.Keys {
			add(k)
		}
	}
	for _, k := range []string{"left", "right", "up", "down", "enter", "space", "tab", "esc"} {
		add(k)
	}
	for r := 'a'; r <= 'z'; r++ {
		add(string(r))
		add(strings.ToUpper(string(r)))
	}
	for r := '0'; r <= '9'; r++ {
		add(string(r))
	}
	return keys
}

// rpFooterKeys reads back the keys a footer claims to answer. A part is one or
// more key labels followed by a word of description, and a label groups its
// keys with a slash, so the footer can be compared with what the screen does
// rather than with a remembered string.
func rpFooterKeys(t *testing.T, footer string) []string {
	t.Helper()
	glyphs := map[string]string{"←": "left", "→": "right", "↑": "up", "↓": "down"}
	var keys []string
	parts := strings.Split(footer, " · ")
	if len(parts) < 2 {
		t.Fatalf("the footer names no keys at all: %q", footer)
	}
	// The first part is where in the record the player is, not a key.
	for _, p := range parts[1:] {
		fields := strings.Fields(p)
		if len(fields) < 2 {
			t.Fatalf("footer part %q names a key without saying what it does", p)
		}
		for _, label := range fields[:len(fields)-1] {
			for _, k := range strings.Split(label, "/") {
				if named, ok := glyphs[k]; ok {
					k = named
				}
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// TestReplayFooterNamesExactlyTheKeysItAnswers is F23: the screen answered h, j,
// k and l as well as the arrows while its footer named only the arrow words. The
// property, rather than the wording, is that the two lists are the same one — a
// key that moves the replay is named, and a named key moves it.
func TestReplayFooterNamesExactlyTheKeysItAnswers(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpSaveGame(t, d)

	answers := map[string]bool{}
	for _, key := range rpKeys(t) {
		s := rpScreen(t, d, sv, 200, 60)
		before := s.at
		_, cmd := s.Update(tutorialKeyMsg(key))
		answers[key] = s.at != before || cmd != nil
	}

	footer := rpScreen(t, d, sv, 200, 60).status(200)
	named := map[string]bool{}
	for _, key := range rpFooterKeys(t, footer) {
		named[key] = true
		if !answers[key] {
			t.Errorf("the footer names %q, which the screen does not answer:\n%s", key, footer)
		}
	}
	for key, answered := range answers {
		if answered && !named[key] {
			t.Errorf("the screen answers %q without naming it:\n%s", key, footer)
		}
	}
}

// TestReplayVimKeysMatchTheArrows pins the reason the footer may name them
// together: each letter from the keymap has to do exactly what its arrow does.
func TestReplayVimKeysMatchTheArrows(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpSaveGame(t, d)
	for _, pair := range [][2]string{{"h", "left"}, {"l", "right"}, {"j", "down"}, {"k", "up"}} {
		letter, arrow := pair[0], pair[1]
		byLetter := rpScreen(t, d, sv, 80, 24)
		byArrow := rpScreen(t, d, sv, 80, 24)
		byLetter.Update(tutorialKeyMsg(letter))
		byArrow.Update(tutorialKeyMsg(arrow))
		if byLetter.at != byArrow.at {
			t.Errorf("%q moved to %d and %q to %d", letter, byLetter.at, arrow, byArrow.at)
		}
		if byLetter.at == len(byLetter.positions)/2 {
			t.Errorf("%q did not move the replay at all", letter)
		}
	}
}

// TestReplayFooterKeepsTheEssentialsWhenNarrowed holds the frame invariant on
// the screen the footer grew on: the line is assembled by importance and dropped
// from the end, so a narrow terminal keeps the counter and the stepping keys
// instead of losing the tail of the line mid-word.
func TestReplayFooterKeepsTheEssentialsWhenNarrowed(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpSaveGame(t, d)
	for _, size := range shellSizes {
		w, h := size[0], size[1]
		s := rpScreen(t, d, sv, w, h)
		shellAssertFits(t, "replay", s.View().Content, w, h)
		status := s.status(w)
		if got := ansi.StringWidth(status); got > w {
			t.Errorf("the footer is %d cells wide in a %d column terminal: %q", got, w, status)
		}
		// The footer counts record entries, not moves: a draw offer is an entry
		// that changes nothing on the board, and calling those moves disagreed
		// with every other surface's move count.
		if !strings.HasPrefix(status, "step ") {
			t.Errorf("at %dx%d the footer lost where in the record the player is: %q", w, h, status)
		}
	}
}
