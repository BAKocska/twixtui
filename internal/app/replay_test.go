package app

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/ui"
)

// rpMoves is a game with enough moves in it that stepping, jumping five and
// going to the ends are all distinguishable. Middle holes only, so neither side
// is refused a border row or column, and no two of them are a knight's move
// apart, so no link is ever offered.
var rpMoves = []string{"D4", "F5", "D7", "F8", "H4", "J5", "H7", "J8"}

// rpRules is the ruleset rpMoves is played under.
func rpRules() game.Ruleset {
	rs := game.Std
	rs.Size = 12
	return rs
}

// rpSaveGame stores rpMoves as a finished record.
func rpSaveGame(t *testing.T, d Deps) gamestore.Saved {
	t.Helper()
	g := game.MustNew(rpRules())
	for _, mv := range rpMoves {
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

// rpOpen opens a replay on a stored game.
func rpOpen(t *testing.T, d Deps, sv gamestore.Saved) *ReplayScreen {
	t.Helper()
	scr, err := NewReplayScreen(d, sv)
	if err != nil {
		t.Fatalf("NewReplayScreen: %v", err)
	}
	s, ok := scr.(*ReplayScreen)
	if !ok {
		t.Fatalf("NewReplayScreen returned %T", scr)
	}
	return s
}

// rpScreen opens a replay sized for a terminal and parked in the middle of the
// record, so a key that moves either way has room to show it.
func rpScreen(t *testing.T, d Deps, sv gamestore.Saved, w, h int) *ReplayScreen {
	t.Helper()
	s := rpOpen(t, d, sv)
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	s.seek(s.entries() / 2)
	return s
}

// rpKeys is the alphabet a probe presses: everything km binds on the board, the
// plain special keys, the one this screen adds for itself, and a spread of
// letters and digits that nothing is expected to answer.
func rpKeys(t *testing.T, km ui.Keymap) []string {
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
	for _, b := range km {
		for _, k := range b.Keys {
			add(k)
		}
	}
	for _, k := range []string{"left", "right", "up", "down", "enter", "space", "tab", "esc", ":"} {
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

// rpAssertFooterMatchesAnswers holds the two lists to each other: a key that
// does something to the replay is named in the footer, and a key the footer
// names does something.
//
// What counts as doing something is the frame: the entry input answers ":" by
// showing itself rather than by moving, so a probe that only watched the step
// counter would call the key the footer advertises unanswered. Anything the
// player can see changing is an answer, and a command — the one way to leave —
// counts as well, since the screen that produced it is on its way out.
func rpAssertFooterMatchesAnswers(t *testing.T, d Deps, sv gamestore.Saved) {
	t.Helper()
	answers := map[string]bool{}
	for _, key := range rpKeys(t, shellKeymap(d)) {
		s := rpScreen(t, d, sv, 200, 60)
		before := s.View().Content
		_, cmd := s.Update(tutorialKeyMsg(key))
		answers[key] = cmd != nil || s.View().Content != before
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

// TestReplayFooterNamesExactlyTheKeysItAnswers is F23: the screen answered h, j,
// k and l as well as the arrows while its footer named only the arrow words. The
// property, rather than the wording, is that the two lists are the same one — a
// key that moves the replay is named, and a named key moves it.
func TestReplayFooterNamesExactlyTheKeysItAnswers(t *testing.T) {
	d := shellTestDeps(t)
	rpAssertFooterMatchesAnswers(t, d, rpSaveGame(t, d))
}

// TestReplayAnswersRemappedKeys is the other half of taking the keys from the
// shared keymap: a player who has rebound the board's movement keys has rebound
// this screen's, and the footer follows them there. The letters the default map
// uses must then do nothing, or the rebinding has only added keys.
func TestReplayAnswersRemappedKeys(t *testing.T) {
	d := shellTestDeps(t)
	remapped := ui.DefaultKeymap()
	moved := map[ui.Action][]string{
		ui.ActMoveRight:  {"]"},
		ui.ActMoveLeft:   {"["},
		ui.ActMoveDown:   {"f"},
		ui.ActMoveUp:     {"b"},
		ui.ActEdgeTop:    {"w"},
		ui.ActEdgeBottom: {"e"},
	}
	for i, bind := range remapped {
		if keys, ok := moved[bind.Action]; ok {
			remapped[i].Keys = keys
			remapped[i].Label = keys[0]
		}
	}
	d.Keymap = remapped
	sv := rpSaveGame(t, d)

	rpAssertFooterMatchesAnswers(t, d, sv)

	// The remapped keys have to do the things they were bound to, not merely
	// be named: a footer built from the same table would agree with a screen
	// that answered every one of them by going nowhere.
	for _, c := range []struct {
		key  string
		from int
		want int
	}{
		{"]", 4, 5}, {"[", 4, 3}, {"f", 1, 6}, {"b", 7, 2}, {"w", 4, 0}, {"e", 4, 8},
	} {
		s := rpOpen(t, d, sv)
		s.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
		s.seek(c.from)
		s.Update(tutorialKeyMsg(c.key))
		if s.step() != c.want {
			t.Errorf("%q from step %d reached step %d, want %d", c.key, c.from, s.step(), c.want)
		}
	}
	// And the default letters have to have gone with the rebinding.
	for _, key := range []string{"h", "j", "k", "l", "g", "G"} {
		s := rpScreen(t, d, sv, 200, 60)
		before := s.step()
		s.Update(tutorialKeyMsg(key))
		if s.step() != before {
			t.Errorf("%q still moves the replay after being rebound away: step %d, was %d", key, s.step(), before)
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
		parked := byLetter.step()
		byLetter.Update(tutorialKeyMsg(letter))
		byArrow.Update(tutorialKeyMsg(arrow))
		if byLetter.step() != byArrow.step() {
			t.Errorf("%q moved to %d and %q to %d", letter, byLetter.step(), arrow, byArrow.step())
		}
		if byLetter.step() == parked {
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

// rpSpecialMoves is a record made of the entries a replay has to be able to
// step over in both directions, rather than a row of ordinary placements: the
// swap, a link declined when the peg went down, a link added by hand and an
// older one taken off, a peg lifted with its links, and draw offers that stand
// until a move refuses them. Interior holes only, so neither side can complete
// a chain and the record ends where the fixture says it does.
//
// The entry each move produces is what the seeks cross, so the list is also the
// oracle: replaying the first n of them is the position after n entries.
var rpSpecialMoves = []string{
	// Vertical opens off the main diagonal, so the swap genuinely moves the
	// peg: it reflects onto D3 and changes hands.
	"C4",
	"swap",
	// Vertical starts again, having given its opening peg away. Horizontal
	// places a knight's move from D3 and declines the link it is offered.
	"F6",
	"E5 ~D3:E5",
	// Vertical keeps the links it is offered, so F6, G8 and H6 become a chain.
	"G8",
	"J3",
	"H6",
	// Horizontal links J3 by placing K5 next to it, and links D3 to E5 by hand
	// after declining that same link when the peg went down.
	"K5 +D3:E5",
	// Vertical takes an older link off the board before placing, and horizontal
	// lifts J3, which takes the link to K5 with it.
	"D9 -F6:G8",
	"B7 xJ3",
	// An offer is not a turn, so vertical is still to move after making one,
	// and the standing offer is whichever was made last.
	"v:draw?",
	"h:draw?",
	// Moving on refuses the opponent's offer, so horizontal asks again.
	"C6",
	"h:draw?",
}

// rpSpecialRules is what rpSpecialMoves needs: the box rules, plus peg removal,
// which no preset turns on.
func rpSpecialRules() game.Ruleset {
	rs := game.Std
	rs.Size = 12
	rs.PegRemoval = true
	return rs
}

// rpSpecialRecord stores rpSpecialMoves with the given last entry, and checks
// the fixture really contains what the seeks are meant to cross: a fixture that
// quietly stopped holding a swap or a peg removal would leave the seek tests
// passing over nothing but placements.
func rpSpecialRecord(t *testing.T, ending string) (gamestore.Saved, []string) {
	t.Helper()
	moves := append(append([]string(nil), rpSpecialMoves...), ending)
	g, err := game.ReplayTranscript(rpSpecialRules(), strings.Join(moves, "; "))
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	if g.Entries() != len(moves) {
		t.Fatalf("the fixture made %d entries out of %d moves", g.Entries(), len(moves))
	}
	kinds := map[game.MoveKind]int{}
	swept := 0
	for _, m := range g.History() {
		kinds[m.Kind]++
		swept += len(m.PegLinks)
	}
	if kinds[game.SwapMove] != 1 || kinds[game.DrawOfferMove] != 3 || swept == 0 {
		t.Fatalf("the fixture holds %d swaps, %d draw offers and %d links swept away with a peg",
			kinds[game.SwapMove], kinds[game.DrawOfferMove], swept)
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []string{"~", "+", "-", "x"} {
		if !strings.Contains(record.Moves, edit) {
			t.Fatalf("the fixture has no %q edit in it: %s", edit, record.Moves)
		}
	}
	return gamestore.Saved{
		ID: "replay-special", Kind: gamestore.Imported, Player: "Vertical",
		Opponent: "Horizontal", Side: "vertical", Record: record.Encode(),
	}, moves
}

// rpSamePosition compares two positions in full: the pegs and the links on the
// board, and the state that is not on it. Entry counts alone would agree with a
// cursor that had lost a draw offer, un-swapped a swap or left a link behind.
func rpSamePosition(t *testing.T, at int, got, want *game.Game) {
	t.Helper()
	if got.Entries() != want.Entries() || got.Ply() != want.Ply() {
		t.Fatalf("step %d: the cursor is %d entries and %d moves in, a fresh replay %d and %d",
			at, got.Entries(), got.Ply(), want.Entries(), want.Ply())
	}
	if got.Turn() != want.Turn() {
		t.Errorf("step %d: the cursor has %s to move, a fresh replay %s", at, got.Turn(), want.Turn())
	}
	if got.Result() != want.Result() {
		t.Errorf("step %d: the cursor's position is %q, a fresh replay %q",
			at, describeOutcome(got.Result()), describeOutcome(want.Result()))
	}
	if got.DrawOfferedBy() != want.DrawOfferedBy() {
		t.Errorf("step %d: the cursor has a draw offered by %v, a fresh replay by %v",
			at, got.DrawOfferedBy(), want.DrawOfferedBy())
	}
	if got.Swapped() != want.Swapped() {
		t.Errorf("step %d: the cursor says swapped=%t, a fresh replay %t", at, got.Swapped(), want.Swapped())
	}
	for row := range want.Size() {
		for col := range want.Size() {
			p := game.Point{Col: col, Row: row}
			if got.At(p) != want.At(p) || got.LinkMask(p) != want.LinkMask(p) {
				t.Fatalf("step %d: %s holds peg %v links %08b, a fresh replay peg %v links %08b",
					at, p, got.At(p), got.LinkMask(p), want.At(p), want.LinkMask(p))
			}
		}
	}
	if game.PositionDigest(got) != game.PositionDigest(want) {
		t.Errorf("step %d: the positions digest differently: %s and %s",
			at, game.PositionDigest(got), game.PositionDigest(want))
	}
}

func TestReplaySeekAcrossDrawOfferBeforeSwap(t *testing.T) {
	const transcript = "C4; v:draw?; swap; h:draw!"
	g, err := game.ReplayTranscript(game.Std, transcript)
	if err != nil {
		t.Fatal(err)
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	screen, err := NewReplayScreen(Deps{}, gamestore.Saved{ID: "swapdraw", Record: record.Encode()})
	if err != nil {
		t.Fatal(err)
	}
	replay := screen.(*ReplayScreen)
	labels := strings.Split(transcript, ";")
	for _, entry := range []int{3, 4, 0, 2, 4, 3, 1, 4} {
		replay.seek(entry)
		want, err := game.ReplayTranscript(game.Std, strings.Join(labels[:entry], ";"))
		if err != nil {
			t.Fatal(err)
		}
		rpSamePosition(t, entry, replay.current(), want)
	}
}

// TestReplaySeekAgreesWithAFreshReplay is what the cursor has to earn: a
// position reached by undoing and replaying in an arbitrary order has to be the
// position a fresh replay of that many entries reaches. Every entry is crossed
// in both directions, and the jumps afterwards are of every size and either
// sign, including past both ends of the record.
func TestReplaySeekAgreesWithAFreshReplay(t *testing.T) {
	for _, ending := range []string{"v:draw!", "h:resign"} {
		t.Run(ending, func(t *testing.T) {
			sv, moves := rpSpecialRecord(t, ending)
			replay := rpOpen(t, Deps{}, sv)
			last := replay.entries()
			if last != len(moves) {
				t.Fatalf("the replay has %d entries, the record %d", last, len(moves))
			}
			// A review opens on the position the record reaches.
			if replay.step() != last {
				t.Fatalf("the replay opened at step %d of %d", replay.step(), last)
			}

			prefix := make([]*game.Game, last+1)
			for n := range prefix {
				fresh, err := game.ReplayTranscript(rpSpecialRules(), strings.Join(moves[:n], "; "))
				if err != nil {
					t.Fatalf("replaying the first %d entries: %v", n, err)
				}
				prefix[n] = fresh
			}

			targets := make([]int, 0, 3*last)
			for i := last; i >= 0; i-- {
				targets = append(targets, i)
			}
			for i := range last + 1 {
				targets = append(targets, i)
			}
			targets = append(targets, last, 0, 7, 2, 13, 5, 1, 9, 4, 11, 3, -4, last+6, 8, 0, last)

			for _, to := range targets {
				replay.seek(to)
				want := min(max(to, 0), last)
				if replay.step() != want {
					t.Fatalf("seeking to %d stopped at step %d, want %d", to, replay.step(), want)
				}
				if replay.stuck != "" {
					t.Fatalf("seeking to %d reported %q on a record that replays", to, replay.stuck)
				}
				rpSamePosition(t, want, replay.current(), prefix[want])
			}

			// One review moving does not move another: each screen replays the
			// record for itself and owns the position it walks.
			other := rpOpen(t, Deps{}, sv)
			replay.seek(0)
			rpSamePosition(t, last, other.current(), prefix[last])
			rpSamePosition(t, 0, replay.current(), prefix[0])
		})
	}
}

// TestReplayReportsTheResultOfTheRecordNotOfTheStep is the trap in keeping one
// position: stepping back unwinds the game's own result, so a panel that asked
// the position on screen how the game ended would call a finished game
// unfinished as soon as the player looked at the middle of it.
func TestReplayReportsTheResultOfTheRecordNotOfTheStep(t *testing.T) {
	for _, c := range []struct{ ending, want string }{
		{"v:draw!", "drawn by agreement"},
		{"h:resign", "vertical won by resignation"},
	} {
		t.Run(c.ending, func(t *testing.T) {
			sv, _ := rpSpecialRecord(t, c.ending)
			replay := rpOpen(t, Deps{}, sv)
			for _, at := range []int{replay.entries(), 0, 6, replay.entries() - 1, 12} {
				replay.seek(at)
				panel := strings.Join(replay.panel(60, 24), "\n")
				if !strings.Contains(panel, "result: "+c.want) {
					t.Errorf("at step %d the panel does not say the record ended %q:\n%s", at, c.want, panel)
				}
			}
		})
	}
}

// TestReplayRefusesARecordAlteredAtItsLastEntry keeps the refusal where it was
// before the cursor existed. Nothing is replayed lazily: a record that does not
// hold up is refused when the screen is asked for, not when the player reaches
// the part of it that does not, by which time they are looking at a board the
// product cannot stand behind.
func TestReplayRefusesARecordAlteredAtItsLastEntry(t *testing.T) {
	sv, moves := rpSpecialRecord(t, "h:resign")
	sound, err := game.DecodeRecord(sv.Record)
	if err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	entries := strings.Split(sound.Moves, ";")
	if len(entries) != len(moves) {
		t.Fatalf("the fixture transcript has %d entries, want %d", len(entries), len(moves))
	}

	for _, c := range []struct{ name, last string }{
		// A hole vertical already has a peg in, so the entry is refused by the
		// rules rather than by the parser.
		{"occupied hole", " F6"},
		{"nonsense", " not-a-move"},
		{"dropped", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			altered := sound
			kept := append([]string(nil), entries[:len(entries)-1]...)
			if c.last != "" {
				kept = append(kept, c.last)
			}
			altered.Moves = strings.Join(kept, ";")
			damaged := sv
			damaged.Record = altered.Encode()

			screen, err := NewReplayScreen(Deps{}, damaged)
			if err == nil {
				t.Fatalf("the screen opened on a record whose last entry is %q", c.last)
			}
			if screen != nil {
				t.Errorf("a refused record still produced a %T", screen)
			}
		})
	}
}

// TestReplaySeekThatCannotBeMadeKeepsTheLastWholePosition covers the failure the
// cursor introduces. With a position per entry there was nothing to fail; with
// one position that moves, an entry that will not replay could leave a
// half-applied board on screen and nothing said about it. A record is validated
// when the screen opens, so the entry is damaged here on purpose.
func TestReplaySeekThatCannotBeMadeKeepsTheLastWholePosition(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpSaveGame(t, d)
	s := rpScreen(t, d, sv, 200, 60)
	s.seek(0)

	const broken = 3
	s.cursor.labels[broken] = "F8 +D4:F5" // placement succeeds; the cross-owner link fails

	s.seek(s.entries())
	if s.step() != broken {
		t.Fatalf("the cursor stopped at step %d, want the last entry it could make, %d", s.step(), broken)
	}
	fresh, err := game.ReplayTranscript(rpRules(), strings.Join(rpMoves[:broken], "; "))
	if err != nil {
		t.Fatal(err)
	}
	rpSamePosition(t, broken, s.current(), fresh)

	panel := strings.Join(s.panel(60, 24), " ")
	if !strings.Contains(panel, "cannot go there") || !strings.Contains(panel, "F8") {
		t.Errorf("the panel does not say the seek failed or why:\n%s", panel)
	}
	// A seek that can be made puts the screen back in ordinary service.
	s.seek(1)
	if s.step() != 1 {
		t.Fatalf("after a refused seek the screen stopped answering: it is at step %d", s.step())
	}
	if panel := strings.Join(s.panel(60, 24), " "); strings.Contains(panel, "cannot go there") {
		t.Errorf("the panel still complains about a seek that has been superseded:\n%s", panel)
	}
}

// rpConnectionRecord is a record whose entry numbers and ply numbers are
// different things and which ends in a chain that was actually made: draw
// offers stand between the moves, and vertical joins the top and bottom
// borders. It is what the entry list, the numeric jump and the winning chain
// are all about.
func rpConnectionRecord(t *testing.T) gamestore.Saved {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for i, move := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		// Two offers before each of the first five moves, which is what makes
		// entry 4 a draw offer and entry 17 the winning move.
		if i < 5 {
			for range 2 {
				if err := g.OfferDraw(g.Turn()); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := g.PlayNotation(move); err != nil {
			t.Fatalf("playing %s: %v", move, err)
		}
	}
	res := g.Result()
	if g.Entries() != 17 || g.Ply() != 7 || res.Reason != game.Connection || res.Winner() != game.Vertical {
		t.Fatalf("the fixture is %d entries and %d plies, ending %q: it no longer separates the two or wins by a chain",
			g.Entries(), g.Ply(), describeOutcome(res))
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return gamestore.Saved{
		ID: "replay-connection", Kind: gamestore.Imported, Player: "Vertical",
		Opponent: "Horizontal", Side: "vertical", Record: record.Encode(), Finished: true,
	}
}

// rpSized opens a replay on a terminal of its own, without moving it: a review
// opens at the end of the record, which is where several of these start.
func rpSized(t *testing.T, d Deps, sv gamestore.Saved, w, h int) *ReplayScreen {
	t.Helper()
	s := rpOpen(t, d, sv)
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return s
}

// rpType presses one key per character, which is what a terminal delivers both
// for typed digits and for text sent to it literally.
func rpType(t *testing.T, s *ReplayScreen, text string) {
	t.Helper()
	for _, r := range text {
		if _, cmd := s.Update(tutorialKeyMsg(string(r))); cmd != nil {
			t.Fatalf("typing %q into the entry input left the screen", text)
		}
	}
}

// rpJump does what a player does to reach an entry by its number: open the
// input, type the number, commit it.
func rpJump(t *testing.T, s *ReplayScreen, number string) {
	t.Helper()
	s.Update(tutorialKeyMsg(":"))
	if !s.jump.open {
		t.Fatal(`the ":" key did not open the entry input`)
	}
	rpType(t, s, number)
	if _, cmd := s.Update(tutorialKeyMsg("enter")); cmd != nil {
		t.Fatalf("committing the entry number %q left the screen", number)
	}
}

// rpRowPattern reads an entry row back: the marker, the entry's number, and the
// label beside it. It is anchored, so the panel's prose is not a row.
var rpRowPattern = regexp.MustCompile(`^(> |  )(\d+) +(\S.*?)\s*$`)

// rpRow is one row of the entry list as a reader sees it.
type rpRow struct {
	number int
	label  string
	marked bool
}

// rpEntryRows reads the list out of panel lines, so an assertion is about what
// is on screen rather than about the cursor's own fields.
func rpEntryRows(t *testing.T, lines []string) []rpRow {
	t.Helper()
	var rows []rpRow
	for _, l := range lines {
		m := rpRowPattern.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("entry row %q: %v", l, err)
		}
		rows = append(rows, rpRow{number: n, label: m[3], marked: m[1] == "> "})
	}
	return rows
}

// rpMarkedRow is the row the review is on, and there has to be exactly one.
func rpMarkedRow(t *testing.T, lines []string) rpRow {
	t.Helper()
	var found []rpRow
	for _, r := range rpEntryRows(t, lines) {
		if r.marked {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the list marks %d rows, want exactly one:\n%s", len(found), strings.Join(lines, "\n"))
	}
	return found[0]
}

// TestReplayJumpsToTheEntryNumberNotThePly is the whole point of numbering the
// list by entry: a record with draw offers in it has more entries than plies,
// and a jump to 4 has to reach the fourth entry — a draw offer, with one move
// played — rather than the fourth move.
func TestReplayJumpsToTheEntryNumberNotThePly(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	for _, c := range []struct {
		typed string
		step  int
		ply   int
		label string
	}{
		{"4", 4, 1, "h:draw?"},
		{"0", 0, 0, "opening position"},
		{"9", 9, 3, "C3"},
		{"17", 17, 7, "B6"},
		{"16", 16, 6, "F4"},
	} {
		rpJump(t, s, c.typed)
		if s.step() != c.step {
			t.Fatalf("jumping to %q reached step %d, want %d", c.typed, s.step(), c.step)
		}
		if got := s.current().Ply(); got != c.ply {
			t.Errorf("entry %s is %d moves in, want %d", c.typed, got, c.ply)
		}
		if s.jump.open {
			t.Errorf("the entry input is still up after jumping to %s", c.typed)
		}
		row := rpMarkedRow(t, s.panel(36, 29))
		if row.number != c.step || row.label != c.label {
			t.Errorf("the list marks row %d %q, want %d %q", row.number, row.label, c.step, c.label)
		}
	}
}

// TestReplayListIsDrawnAndSurvivesAResize asserts on the frame rather than on
// the panel: a list that is built and not drawn reviews nothing. The resize is
// the other half — the terminal a review is being read in changes size, and the
// entry it is on is not something to lose to that.
func TestReplayListIsDrawnAndSurvivesAResize(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "4")

	before := s.View().Content
	if !regexp.MustCompile(`>\s*4\s+h:draw\?`).MatchString(before) {
		t.Fatalf("the frame does not mark the entry being reviewed:\n%s", before)
	}
	if !strings.Contains(before, "step 4 of 17") || !strings.Contains(before, "moves played: 1") {
		t.Errorf("the frame does not say where in the record the review is:\n%s", before)
	}

	for _, size := range shellSizes {
		s.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frame := s.View().Content
		shellAssertFits(t, "replay", frame, size[0], size[1])
		if s.step() != 4 {
			t.Fatalf("resizing to %dx%d moved the review to step %d", size[0], size[1], s.step())
		}
		// The status line is the whole of the interface where there is no room
		// for a panel, so it has to keep saying where the review is.
		if status := s.status(size[0]); !strings.Contains(status, "step 4 of 17") {
			t.Errorf("at %dx%d the status line lost the counter: %q", size[0], size[1], status)
		}
	}

	s.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if after := s.View().Content; after != before {
		t.Errorf("the frame did not come back as it was after a resize\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestReplayPanelKeepsEntriesInAShortPanel is the panel under a board, which is
// four rows at its smallest. Prose fills a panel readily — a counter, a result,
// two names, a ruleset — and a review that spent every row on it would show a
// record with none of its entries in it.
func TestReplayPanelKeepsEntriesInAShortPanel(t *testing.T) {
	d := shellTestDeps(t)
	s := rpOpen(t, d, rpConnectionRecord(t))
	s.seek(9)
	for _, height := range []int{4, 6, 10, 29} {
		lines := s.panel(36, height)
		if len(lines) > height {
			t.Fatalf("a %d-row panel produced %d lines:\n%s", height, len(lines), strings.Join(lines, "\n"))
		}
		rows := rpEntryRows(t, lines)
		if want := min(height/2, s.entries()+1); len(rows) < want {
			t.Errorf("a %d-row panel shows %d entry rows, want at least %d:\n%s",
				height, len(rows), want, strings.Join(lines, "\n"))
		}
		if row := rpMarkedRow(t, lines); row.number != 9 {
			t.Errorf("a %d-row panel scrolled the marked row away: it marks %d", height, row.number)
		}
	}
}

// TestReplayEntryInputRefusesWhatCannotBeAnEntry is the input's whole contract.
// A number that names no entry is refused rather than clamped, what was wrong
// stays on screen until it is put right, and nothing typed at the input reaches
// the review behind it — including the key that would otherwise leave.
func TestReplayEntryInputRefusesWhatCannotBeAnEntry(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpConnectionRecord(t)
	// A seventeen-entry record numbers its entries in two digits, so two digits
	// is what the field takes.
	const digits = 2

	open := func(t *testing.T, at int) *ReplayScreen {
		t.Helper()
		s := rpSized(t, d, sv, 120, 30)
		s.seek(at)
		s.Update(tutorialKeyMsg(":"))
		if !s.jump.open {
			t.Fatal(`the ":" key did not open the entry input`)
		}
		return s
	}

	t.Run("escape leaves the input and not the screen", func(t *testing.T) {
		s := open(t, 4)
		rpType(t, s, "9")
		_, cmd := s.Update(tutorialKeyMsg("esc"))
		if cmd != nil {
			t.Error("escape out of the entry input left the review")
		}
		if s.jump.open || s.jump.edit.value() != "" {
			t.Errorf("escape left the input open with %q in it", s.jump.edit.value())
		}
		if s.step() != 4 {
			t.Errorf("escape moved the review to step %d", s.step())
		}
	})

	t.Run("an empty number is not a jump", func(t *testing.T) {
		s := open(t, 4)
		s.Update(tutorialKeyMsg("enter"))
		if !s.jump.open {
			t.Error("committing nothing closed the input")
		}
		if s.step() != 4 {
			t.Errorf("committing nothing moved the review to step %d", s.step())
		}
		if s.jump.problem == "" || !strings.Contains(s.View().Content, s.jump.problem) {
			t.Errorf("the empty-input refusal is not visible:\n%s", s.View().Content)
		}
	})

	t.Run("a number past the end of the record is refused, not clamped", func(t *testing.T) {
		s := open(t, 4)
		// Forty nines is what a stuck key or a pasted line looks like. The
		// field takes the digits it may and says why it took no more.
		rpType(t, s, strings.Repeat("9", 40))
		if got := s.jump.edit.value(); got != strings.Repeat("9", digits) {
			t.Fatalf("the field holds %q, want %d digits of it", got, digits)
		}
		tooLong := s.jump.problem
		if tooLong == "" || !strings.Contains(s.View().Content, tooLong) {
			t.Errorf("the excess-digit refusal is not visible:\n%s", s.View().Content)
		}
		s.Update(tutorialKeyMsg("enter"))
		if s.step() != 4 {
			t.Errorf("entry 99 of a 17-entry record moved the review to step %d", s.step())
		}
		if !s.jump.open {
			t.Error("a refused number closed the input, leaving nothing to correct")
		}
		frame := s.View().Content
		if s.jump.problem == "" || !strings.Contains(frame, s.jump.problem) || !strings.Contains(frame, "step 4 of 17") {
			t.Errorf("the frame does not say the number was refused and where the review still is:\n%s", frame)
		}
		// Out of range and too many digits are different complaints, and both
		// differ from what is said about text that is not a number at all.
		if s.jump.note() == tooLong {
			t.Error("a number past the end of the record is reported as too long")
		}
		// The message stands until the number is put right, and then the jump
		// is an ordinary one.
		s.Update(tutorialKeyMsg("ctrl+u"))
		if s.jump.problem != "" {
			t.Errorf("clearing the field left the complaint %q behind", s.jump.problem)
		}
		rpType(t, s, "0")
		s.Update(tutorialKeyMsg("enter"))
		if s.step() != 0 || s.jump.open {
			t.Errorf("the corrected number reached step %d with the input open=%v", s.step(), s.jump.open)
		}
	})

	t.Run("the review's own keys are text while the input is up", func(t *testing.T) {
		for _, key := range []string{"q", "l", "h", "j", "k", "g", "G", "n", "p"} {
			s := open(t, 4)
			_, cmd := s.Update(tutorialKeyMsg(key))
			if cmd != nil {
				t.Errorf("%q left the review while a number was being typed", key)
			}
			if !s.jump.open {
				t.Errorf("%q closed the entry input", key)
			}
			if s.step() != 4 {
				t.Errorf("%q moved the review to step %d while the input was up", key, s.step())
			}
			if note := s.jump.note(); !strings.Contains(note, "digits only") {
				t.Errorf("%q was taken as something other than a stray character: %q", key, note)
			}
		}
	})

	t.Run("a paste is a number or nothing", func(t *testing.T) {
		s := open(t, 4)
		s.Update(tea.PasteMsg{Content: " 9 \n"})
		if got := s.jump.edit.value(); got != "9" {
			t.Fatalf("a pasted number arrived as %q", got)
		}
		for _, c := range []struct{ name, content string }{
			{"letters", "a"},
			{"a whole file", strings.Repeat("9", 1<<20)},
			{"whitespace around a whole file", strings.Repeat(" ", 1<<20) + "1"},
		} {
			s.Update(tea.PasteMsg{Content: c.content})
			if got := s.jump.edit.value(); got != "9" {
				t.Errorf("pasting %s changed the field to %q", c.name, got)
			}
			if s.jump.problem == "" || !strings.Contains(s.View().Content, s.jump.problem) {
				t.Errorf("pasting %s has no visible refusal:\n%s", c.name, s.View().Content)
			}
		}
		s.Update(tutorialKeyMsg("enter"))
		if s.step() != 9 {
			t.Errorf("the pasted number reached step %d, want 9", s.step())
		}
		// A paste with no field to go into is not a number and not an error.
		s.Update(tea.PasteMsg{Content: "3"})
		if s.jump.open || s.step() != 9 {
			t.Errorf("a paste with the input closed opened it or moved the review to step %d", s.step())
		}
	})

	t.Run("the input survives a resize", func(t *testing.T) {
		s := open(t, 4)
		rpType(t, s, "1")
		for _, size := range shellSizes {
			s.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			shellAssertFits(t, "replay with the entry input up", s.View().Content, size[0], size[1])
			if !s.jump.open || s.jump.edit.value() != "1" {
				t.Fatalf("at %dx%d the input holds %q with open=%v", size[0], size[1], s.jump.edit.value(), s.jump.open)
			}
		}
		s.Update(tutorialKeyMsg("enter"))
		if s.step() != 1 {
			t.Errorf("the number typed before the resizes reached step %d, want 1", s.step())
		}
	})
}

func TestReplayJumpRefusalRemainsVisibleWithoutAPanel(t *testing.T) {
	for _, input := range []string{"", "99", "a"} {
		t.Run("input="+input, func(t *testing.T) {
			s := rpSized(t, shellTestDeps(t), rpConnectionRecord(t), 20, 8)
			s.seek(4)
			if ui.Arrange(20, 8, 6).Panel != ui.PanelNone {
				t.Fatal("fixture is not testing panel-free status")
			}
			s.Update(tutorialKeyMsg(":"))
			rpType(t, s, input)
			if input != "a" {
				s.Update(tutorialKeyMsg("enter"))
			}
			frame := s.View().Content
			if s.jump.problem == "" || !strings.Contains(frame, s.jump.problem) ||
				!strings.Contains(frame, caret) || s.step() != 4 || !s.jump.open {
				t.Fatalf("refusal or editable input was lost:\n%s", frame)
			}
			shellAssertFits(t, "jump refusal", frame, 20, 8)
		})
	}
}

// TestReplayMarksTheWinningChainOnlyWhereItWasMade is the review's half of the
// chain the game screen draws. The run of pegs exists in the final position and
// nowhere before it, so marking it at an earlier step would call out holes that
// are still empty — a connection the game had not made yet.
func TestReplayMarksTheWinningChainOnlyWhereItWasMade(t *testing.T) {
	d := shellTestDeps(t)
	s := rpSized(t, d, rpConnectionRecord(t), 120, 30)
	first := s.View().Content
	hasMark := func(frame string) bool {
		return strings.Contains(frame, "(●)") || strings.ContainsAny(frame, "□■▣△▲▽")
	}
	if !hasMark(first) {
		t.Fatalf("the first final-position frame has no winning highlights:\n%s", first)
	}
	chain := s.board.Highlights
	gsCheckChain(t, s.current(), game.Vertical, chain)

	for _, at := range []int{16, 15, 9, 3, 0} {
		s.seek(at)
		frame := s.View().Content
		if hasMark(frame) {
			t.Fatalf("step %d rendered stale winning highlights:\n%s", at, frame)
		}
		if marks := s.board.Highlights; len(marks) != 0 {
			t.Errorf("step %d of the record marks %v as the winning chain", at, marks)
		}
	}
	// Coming back to the end brings it back: the chain is the record's, not
	// something the screen used up on the way through.
	s.seek(s.entries())
	if frame := s.View().Content; !hasMark(frame) {
		t.Fatalf("returning to the final position did not render the chain:\n%s", frame)
	}
	if got := s.board.Highlights; len(got) != len(chain) {
		t.Errorf("the final position marks %v, want the chain %v", got, chain)
	}
}

// TestReplayMarksNoChainWhereNoneWasMade is the other reason the chain is not
// simply "the winner's pegs": a game given up or agreed drawn has a winner or a
// result without a connection, and there is nothing to trace.
func TestReplayMarksNoChainWhereNoneWasMade(t *testing.T) {
	d := shellTestDeps(t)
	for _, ending := range []string{"h:resign", "v:draw!"} {
		t.Run(ending, func(t *testing.T) {
			sv, _ := rpSpecialRecord(t, ending)
			s := rpSized(t, d, sv, 120, 30)
			for _, at := range []int{s.entries(), s.entries() - 1, 0} {
				s.seek(at)
				_ = s.View()
				if marks := s.board.Highlights; len(marks) != 0 {
					t.Errorf("a game that ended %q marks %v at step %d", ending, marks, at)
				}
			}
		})
	}
}

// TestReplayBringsADistantEntryIntoView is what a list of four hundred entries
// needs from the board: a jump to entry 1 of a 48-hole record is no use if the
// hole it played is off the viewport, and the viewport is only ever moved by a
// cursor. The cursor is drawn where it sits, so the frame is the evidence.
func TestReplayBringsADistantEntryIntoView(t *testing.T) {
	d := shellTestDeps(t)
	// The final peg is at U21, beyond a 40x14 viewport in both directions.
	s := rpSized(t, d, replayWorkload(t, 48, 400), 40, 14)
	final := s.View().Content
	if !rpCursorDrawn(final) {
		t.Fatalf("the entry the review opened on is not on screen:\n%s", final)
	}
	topEnd, leftEnd := s.board.Viewport()
	if topEnd == 0 && leftEnd == 0 {
		t.Fatal("fixture never scrolled away from the opening")
	}
	rpJump(t, s, "0")
	opening := s.View().Content
	if top, left := s.board.Viewport(); top != 0 || left != 0 {
		t.Fatalf("direct jump to the opening retained viewport %d,%d:\n%s", top, left, opening)
	}

	rpJump(t, s, "1")
	if s.step() != 1 {
		t.Fatalf("the jump reached step %d, want 1", s.step())
	}
	early := s.View().Content
	if top, left := s.board.Viewport(); top == topEnd && left == leftEnd {
		t.Errorf("the viewport stayed at %d,%d for entries at opposite ends of the board", top, left)
	}
	if !rpCursorDrawn(early) {
		t.Errorf("entry 1 is not on screen after jumping to it:\n%s", early)
	}
	// The opening position played nothing, so there is nothing to point at.
	rpJump(t, s, "0")
	if rpCursorDrawn(s.View().Content) {
		t.Errorf("the opening position points at a hole nothing was played in:\n%s", s.View().Content)
	}
}

// rpCursorDrawn reports whether the frame draws the board cursor, which the
// renderer does only for a hole inside the viewport. The glyphs are the board's
// own: brackets either side of the hole, or a mark on it where a link owns
// those cells.
func rpCursorDrawn(frame string) bool {
	return strings.ContainsAny(frame, "[]◇◆◈")
}

// TestReplayLeavesTheStoredGameAlone is the promise a review makes: it reads.
// Stepping is the engine's own undo and replay, which mutate a position, and the
// position it mutates has to be the screen's copy and not the file.
func TestReplayLeavesTheStoredGameAlone(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpSaveGame(t, d)
	before, err := d.Games.Get(sv.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := rpSized(t, d, sv, 120, 30)
	for _, key := range []string{"g", "l", "l", "j", "G", "h", "k"} {
		s.Update(tutorialKeyMsg(key))
	}
	rpJump(t, s, "3")
	rpJump(t, s, "0")
	_ = s.View()

	after, err := d.Games.Get(sv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Record != before.Record {
		t.Errorf("reviewing the game rewrote its record:\nbefore: %s\nafter:  %s", before.Record, after.Record)
	}
	if after.Finished != before.Finished || after.Updated != before.Updated {
		t.Errorf("reviewing the game touched its notes: finished %v/%v, updated %v/%v",
			before.Finished, after.Finished, before.Updated, after.Updated)
	}
}

// TestReplayDrawsNothingATerminalWouldActOn is about the notes around a record
// rather than the record: they are this machine's own JSON, a player may edit
// them, and nothing in the store checks them. A terminal acts on the control
// bytes in what it is asked to draw, so a name with an escape sequence in it
// would retitle the window or repaint it instead of appearing as a name.
func TestReplayDrawsNothingATerminalWouldActOn(t *testing.T) {
	d := shellTestDeps(t)
	sv := rpConnectionRecord(t)
	sv.ID = "game\x1b]0;stolen\x07id"
	sv.Player = "Bal\x1bint\u202e"
	sv.Opponent = "bot:beg\ninner"
	s := rpSized(t, d, sv, 120, 30)

	// The styles these tests draw with add no escape sequences of their own, so
	// anything unprintable in the frame came out of the store.
	frame := s.View().Content
	for _, r := range frame {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("the frame carries %U, which a terminal would act on:\n%q", r, frame)
		}
	}
	// The readable part of a mangled note is kept rather than the whole of it
	// thrown away: it is still whose game this is. What is left of an escape
	// sequence once its escape is gone is ordinary text, and drawn as such.
	for _, want := range []string{"Balint", "beg inner bot", "game]0;stolenid"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the frame does not carry %q:\n%s", want, frame)
		}
	}
}

// TestReplayLabelsAreDrawnWithoutTheirSpacing is the record's own text. Notation
// is allowed its own spacing — strings.Fields is what the parser splits an entry
// with — so a record written elsewhere can hold a tab inside an entry that
// replays perfectly and that a terminal acts on rather than draws. The cursor
// keeps what was validated; the list draws this.
func TestReplayLabelsAreDrawnWithoutTheirSpacing(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"K5 +D3:E5", "K5 +D3:E5"},
		{"K5\t+D3:E5", "K5 +D3:E5"},
		{"K5\v+D3:E5", "K5 +D3:E5"},
		{"K5\u0085+D3:E5", "K5 +D3:E5"},
		{"D9  -F6:G8", "D9 -F6:G8"},
		{"B7\txJ3\n", "B7 xJ3"},
		{"v:draw?", "v:draw?"},
	} {
		if got := displayLabels([]string{c.in})[0]; got != c.want {
			t.Errorf("the label %q is drawn as %q, want %q", c.in, got, c.want)
		}
	}
}

// replayWorkload builds a fixed interior-only record. Neither player touches
// a goal border, so fixture length does not depend on a random game ending.
// Row-major alternating pegs produce real knight links, not an empty-board
// benchmark. The canonical record is checked outside every timed loop.
func replayWorkload(t testing.TB, size, entries int) gamestore.Saved {
	t.Helper()
	rs := game.Std
	rs.Size = size
	g := game.MustNew(rs)
	span := 6
	if size == 48 {
		span = 20
	}
	if entries > span*span {
		t.Fatalf("%d entries exceed the fixed %dx%d interior", entries, span, span)
	}
	for i := range entries {
		p := game.Point{Col: 1 + i%span, Row: 1 + i/span}
		if result, err := g.PlayPeg(p); err != nil || result.Over() {
			t.Fatalf("fixture entry %d at %s: result=%v err=%v", i+1, p, result, err)
		}
	}
	links := 0
	for row := range size {
		for col := range size {
			if g.LinkMask(game.Point{Col: col, Row: row}) != 0 {
				links++
			}
		}
	}
	if g.Entries() != entries || g.Ply() != entries || links == 0 {
		t.Fatalf("replay workload shape changed: entries=%d ply=%d linked-pegs=%d",
			g.Entries(), g.Ply(), links)
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	checked, _, err := game.LoadRecord(record.Encode())
	if err != nil || checked == nil || checked.Entries() != entries ||
		game.PositionDigest(checked) != game.PositionDigest(g) {
		t.Fatalf("replay fixture did not round-trip: %v", err)
	}
	return gamestore.Saved{
		ID: "replay48", Kind: gamestore.Imported, Player: "Vertical",
		Opponent: "Horizontal", Side: "vertical", Record: record.Encode(),
	}
}

func TestReplayBenchmarkRecordsArePlayable(t *testing.T) {
	for _, c := range [][2]int{{12, 24}, {48, 400}} {
		t.Run(fmt.Sprintf("%dx%d-%d", c[0], c[0], c[1]), func(t *testing.T) {
			saved := replayWorkload(t, c[0], c[1])
			screen, err := NewReplayScreen(Deps{}, saved)
			if err != nil {
				t.Fatal(err)
			}
			replay := screen.(*ReplayScreen)
			if replay.current().Entries() != c[1] {
				t.Fatalf("replay starts after %d entries, want %d", replay.current().Entries(), c[1])
			}
			replay.seek(0)
			if replay.current().Entries() != 0 || replay.current().Ply() != 0 {
				t.Fatal("replay did not reach the empty initial position")
			}
		})
	}
}

var replayBenchmarkSink Screen
var replayPositionSink *game.Game

func BenchmarkReplayConstruction(b *testing.B) {
	for _, c := range [][2]int{{12, 24}, {48, 400}} {
		b.Run(fmt.Sprintf("%dx%d-%d", c[0], c[0], c[1]), func(b *testing.B) {
			saved := replayWorkload(b, c[0], c[1])
			b.ReportAllocs()
			for b.Loop() {
				var err error
				replayBenchmarkSink, err = NewReplayScreen(Deps{}, saved)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Traversal measures a complete backwards/forwards review, not construction.
func BenchmarkReplayTraversal(b *testing.B) {
	for _, c := range [][2]int{{12, 24}, {48, 400}} {
		b.Run(fmt.Sprintf("%dx%d-%d", c[0], c[0], c[1]), func(b *testing.B) {
			saved := replayWorkload(b, c[0], c[1])
			screen, err := NewReplayScreen(Deps{}, saved)
			if err != nil {
				b.Fatal(err)
			}
			replay := screen.(*ReplayScreen)
			b.ReportAllocs()
			for b.Loop() {
				for entry := c[1] - 1; entry >= 0; entry-- {
					replay.seek(entry)
					replayPositionSink = replay.current()
					if replayPositionSink.Entries() != entry {
						b.Fatalf("backward seek reached %d, want %d", replayPositionSink.Entries(), entry)
					}
				}
				for entry := 1; entry <= c[1]; entry++ {
					replay.seek(entry)
					replayPositionSink = replay.current()
					if replayPositionSink.Entries() != entry {
						b.Fatalf("forward seek reached %d, want %d", replayPositionSink.Entries(), entry)
					}
				}
				if replay.current().Entries() != c[1] {
					b.Fatal("traversal did not reach the final position")
				}
			}
		})
	}
}

func BenchmarkReplayLongSeek(b *testing.B) {
	saved := replayWorkload(b, 48, 400)
	screen, err := NewReplayScreen(Deps{}, saved)
	if err != nil {
		b.Fatal(err)
	}
	replay := screen.(*ReplayScreen)
	b.ReportAllocs()
	for b.Loop() {
		replay.seek(0)
		replayPositionSink = replay.current()
		if replayPositionSink.Entries() != 0 {
			b.Fatal("long seek did not reach the initial position")
		}
		replay.seek(400)
		if replay.current().Entries() != 400 {
			b.Fatal("long seek did not reach the final position")
		}
	}
}
