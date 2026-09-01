package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// Correspondence play shipped unusable: the identifier a new game was given was
// refused by the store that had to hold it, the game screen refused a remote
// seat with no session behind it, and no part of the interface produced or
// consumed a code. Nothing here reaches into the model to move a game on: every
// move is played at one player's keyboard, its code is read off that player's
// screen, and it is pasted into the other player's screen, which is the only
// path a real pair of players has.

// --- two players, two configuration directories -----------------------------

// corrPlayer is one end of a correspondence game: its own configuration
// directory, its own copy of the game, and the screen it is played on.
type corrPlayer struct {
	t    *testing.T
	deps Deps
	id   string
	side game.Player
	name string
	them string
	h    *gsHarness
}

// corrTable is the pair. They share nothing but the codes they pass.
type corrTable struct {
	t        *testing.T
	id       string
	rules    game.Ruleset
	host     *corrPlayer
	guest    *corrPlayer
	exchange int
}

// newCorrTable sets up a game the way the command line does: the host mints an
// invite, the guest reads it back out of the code, and both derive the same
// stored identifier from it.
//
// The derivation is the first of the three breaks: netplay mints its identifier
// in the alphabet its codes are written in, which is upper case, and the store
// names a file after it, which must be lower case. Deriving it here rather than
// taking it from the invite is what makes this test able to fail again.
func newCorrTable(t *testing.T, size int) *corrTable {
	t.Helper()
	rules := gsRules(size)
	invite, err := netplay.NewInvite(rules, game.Vertical, "ada")
	if err != nil {
		t.Fatalf("minting the invite: %v", err)
	}
	code, err := netplay.EncodeInvite(invite)
	if err != nil {
		t.Fatalf("encoding the invite: %v", err)
	}
	accepted, err := netplay.DecodeInvite(code)
	if err != nil {
		t.Fatalf("the guest could not read the invite: %v", err)
	}
	if accepted.ID != invite.ID {
		t.Fatalf("the invite arrived naming game %q, sent as %q", accepted.ID, invite.ID)
	}

	id := strings.ToLower(strings.TrimSpace(accepted.ID))
	if err := gamestore.ValidateID(id); err != nil {
		t.Fatalf("a game made from an invite cannot be stored: %v", err)
	}

	tb := &corrTable{t: t, id: id, rules: rules}
	tb.host = newCorrPlayer(t, id, rules, accepted.HostSide, "ada", "linus")
	tb.guest = newCorrPlayer(t, id, rules, accepted.GuestSide(), "linus", "ada")
	return tb
}

func newCorrPlayer(t *testing.T, id string, rules game.Ruleset, side game.Player, name, them string) *corrPlayer {
	t.Helper()
	p := &corrPlayer{t: t, deps: gsTestDeps(t), id: id, side: side, name: name, them: them}
	g, err := game.New(rules)
	if err != nil {
		t.Fatalf("starting %s's game: %v", name, err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatalf("recording %s's game: %v", name, err)
	}
	if err := p.deps.Games.Put(gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Correspondence,
		Player:   name,
		Side:     side.String(),
		Opponent: them,
		Record:   rec.Encode(),
	}); err != nil {
		t.Fatalf("saving %s's game: %v", name, err)
	}
	p.open()
	return p
}

// config is the configuration the command line builds for this end.
func (p *corrPlayer) config(resume *gamestore.Saved) GameConfig {
	return GameConfig{
		Kind:  gamestore.Correspondence,
		Rules: resumeRules(p.t, resume),
		Seats: map[game.Player]Seat{
			p.side:            {Profile: p.name, Label: p.name},
			p.side.Opponent(): {Remote: true, Label: p.them},
		},
		Codes:   true,
		Resume:  resume,
		StoreID: p.id,
	}
}

func resumeRules(t *testing.T, resume *gamestore.Saved) game.Ruleset {
	t.Helper()
	g, err := resume.Game()
	if err != nil {
		t.Fatalf("reading the saved game: %v", err)
	}
	return g.Rules()
}

// open puts this player at the board, reading the game back off their disk, so
// a test that closes a screen and opens it again travels the same path a player
// does between turns.
func (p *corrPlayer) open() {
	p.t.Helper()
	saved := p.saved()
	p.h = newGSHarness(p.t, p.deps, p.config(&saved), 80, 30)
}

// close leaves the screen the way a player does.
func (p *corrPlayer) close() {
	p.t.Helper()
	p.h.press("q")
	p.h = nil
}

func (p *corrPlayer) saved() gamestore.Saved {
	p.t.Helper()
	saved, err := p.deps.Games.Get(p.id)
	if err != nil {
		p.t.Fatalf("%s has no stored game %s: %v", p.name, p.id, err)
	}
	return saved
}

// storedPosition is the position on this player's disk, which is what the other
// end has to agree with.
func (p *corrPlayer) storedPosition() string {
	p.t.Helper()
	g, err := p.saved().Game()
	if err != nil {
		p.t.Fatalf("%s's stored game will not load: %v", p.name, err)
	}
	return netplay.PositionHash(g)
}

// codeOnScreen reads the code this player has to send off their own screen.
//
// It insists on exactly one line that is nothing but a code, because that is
// the requirement: a player copies a code by selecting the line it is on, and a
// line carrying anything else, or a code broken across two of them, cannot be
// copied that way.
func (p *corrPlayer) codeOnScreen() string {
	p.t.Helper()
	frame := p.h.frame()
	var found []string
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "TWX") {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		p.t.Fatalf("%s's screen shows %d code lines, want exactly 1\n--- frame ---\n%s",
			p.name, len(found), frame)
	}
	line := found[0]
	if line != strings.TrimSpace(line) {
		p.t.Fatalf("the code line carries padding, so selecting it copies more than the code: %q", line)
	}
	if _, err := netplay.Inspect(line); err != nil {
		p.t.Fatalf("the line on screen is not a whole code: %v\nline: %q\n--- frame ---\n%s", err, line, frame)
	}
	return line
}

// applyCode pastes a code in the way a player does: bracketed paste delivers it
// as text, with whatever whitespace the chat it came from left on it.
func (p *corrPlayer) applyCode(code string) {
	p.t.Helper()
	p.h.feed(tea.PasteMsg{Content: "  " + code + "\n"})
	p.h.press("enter")
}

// move plays one turn at this player's keyboard and returns the code it made.
func (p *corrPlayer) move(at game.Point) string {
	p.t.Helper()
	p.h.playTurn(at)
	code := p.codeOnScreen()
	// Back to the board: the exchange owns the keyboard while it is showing.
	p.h.press("esc")
	return code
}

// toMove is whichever player the position is waiting for, and the other one.
func (tb *corrTable) toMove() (mover, other *corrPlayer) {
	tb.t.Helper()
	if tb.host.h.s.g.Turn() == tb.host.side {
		return tb.host, tb.guest
	}
	return tb.guest, tb.host
}

// play is one whole exchange: the player to move plays, the code comes off their
// screen, the other player pastes it in, and both ends are checked to have
// stored the same position.
func (tb *corrTable) play(at game.Point) {
	tb.t.Helper()
	mover, other := tb.toMove()
	code := mover.move(at)
	other.applyCode(code)
	tb.exchange++
	tb.assertAgree()
}

func (tb *corrTable) assertAgree() {
	tb.t.Helper()
	live := tb.host.h.s.g
	if got, want := netplay.PositionHash(tb.guest.h.s.g), netplay.PositionHash(live); got != want {
		tb.t.Fatalf("after exchange %d the two boards differ:\n--- ada ---\n%s\n--- linus ---\n%s",
			tb.exchange, live, tb.guest.h.s.g)
	}
	for _, p := range []*corrPlayer{tb.host, tb.guest} {
		if got, want := p.storedPosition(), netplay.PositionHash(p.h.s.g); got != want {
			tb.t.Fatalf("after exchange %d %s's stored game is not the one on screen", tb.exchange, p.name)
		}
	}
	if tb.host.storedPosition() != tb.guest.storedPosition() {
		tb.t.Fatalf("after exchange %d the two stored games differ", tb.exchange)
	}
}

// --- the round trip ---------------------------------------------------------

// TestTwoPlayersPlayAWholeGameByCode is the test whose absence let the mode ship
// dead: two separate stores, every move produced by one side and applied by the
// other, and both ends checked to agree after each one.
func TestTwoPlayersPlayAWholeGameByCode(t *testing.T) {
	tb := newCorrTable(t, 6)

	for _, at := range gsWinScript {
		tb.play(at)
	}

	res := tb.host.h.s.g.Result()
	if res.Outcome != game.VerticalWins || res.Reason != game.Connection {
		t.Fatalf("the host's game ended %v/%v, want VerticalWins by Connection", res.Outcome, res.Reason)
	}
	if got := tb.guest.h.s.g.Result(); got != res {
		t.Fatalf("the guest's game ended %v, the host's %v", got, res)
	}
	for _, p := range []*corrPlayer{tb.host, tb.guest} {
		saved := p.saved()
		if !saved.Finished {
			t.Errorf("%s's game is not stored as finished", p.name)
		}
		if saved.Kind != gamestore.Correspondence {
			t.Errorf("%s's game is stored as a %s game", p.name, saved.Kind)
		}
	}
	if got := len(tb.host.deps.Games.List()); got != 1 {
		t.Errorf("the host ended with %d stored games, want 1", got)
	}
}

// TestTheCodeIsCopyableOffTheScreen is the affordance itself: the move a player
// has just made must appear as a code on a line of its own, whole, so that one
// selection copies it and nothing else.
func TestTheCodeIsCopyableOffTheScreen(t *testing.T) {
	tb := newCorrTable(t, 6)
	code := tb.host.move(game.Point{Col: 1, Row: 0})

	if _, err := netplay.Inspect(code); err != nil {
		t.Fatalf("what the screen showed is not a code: %v", err)
	}
	// The exchange is reopened rather than assumed still open, because the
	// player who went away to paste the code comes back to the board.
	tb.host.h.press("c")
	frame := tb.host.h.frame()
	if !strings.Contains(frame, "\n"+code+"\n") {
		t.Fatalf("the code is not on a line of its own:\n--- frame ---\n%s", frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, code) && line != code {
			t.Fatalf("the code shares its line with %q", line)
		}
	}
	if w, h := 80, 30; !frameFits(frame, w, h) {
		t.Errorf("the exchange does not fit an %dx%d terminal:\n%s", w, h, frame)
	}
}

// frameFits reports whether every line fits the terminal and there are no more
// lines than rows. A code longer than the terminal is wide is emitted whole on
// purpose, so this is only asserted at a width that holds one.
func frameFits(frame string, width, height int) bool {
	lines := strings.Split(frame, "\n")
	if len(lines) > height {
		return false
	}
	for _, l := range lines {
		if len([]rune(l)) > width {
			return false
		}
	}
	return true
}

// --- the game screen's own refusal ------------------------------------------

// TestARemoteSeatNeedsASessionOrCodes covers the second break: the screen
// refused the very configuration correspondence play has to hand it.
func TestARemoteSeatNeedsASessionOrCodes(t *testing.T) {
	d := gsTestDeps(t)
	remote := func() map[game.Player]Seat {
		return map[game.Player]Seat{
			game.Vertical:   {Profile: "ada", Label: "ada"},
			game.Horizontal: {Remote: true, Label: "linus"},
		}
	}
	local := func() map[game.Player]Seat {
		return map[game.Player]Seat{
			game.Vertical:   {Profile: "ada"},
			game.Horizontal: {Profile: "linus"},
		}
	}

	cases := []struct {
		name string
		cfg  GameConfig
		want string
	}{
		{
			name: "codes with a remote seat",
			cfg:  GameConfig{Rules: gsRules(6), Seats: remote(), Codes: true, StoreID: "abc123"},
		},
		{
			name: "a remote seat with neither",
			cfg:  GameConfig{Rules: gsRules(6), Seats: remote()},
			want: "a remote seat needs a session",
		},
		{
			name: "codes with no remote seat",
			cfg:  GameConfig{Rules: gsRules(6), Seats: local(), Codes: true, StoreID: "abc123"},
			want: "neither seat is remote",
		},
		{
			name: "codes with a session",
			cfg: GameConfig{Rules: gsRules(6), Seats: remote(), Codes: true, StoreID: "abc123",
				Session: newGSFakeSession(game.Vertical, gsRules(6))},
			want: "not both",
		},
		{
			name: "codes with no identifier to bind them to",
			cfg:  GameConfig{Rules: gsRules(6), Seats: remote(), Codes: true},
			want: "needs the identifier its codes are bound to",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen, err := NewGameScreen(d, c.cfg)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("the configuration was refused: %v", err)
			case c.want == "":
				gs, ok := screen.(*gameScreen)
				if !ok {
					t.Fatalf("NewGameScreen returned %T", screen)
				}
				if gs.corr == nil {
					t.Fatal("the game was accepted without an exchange, so no code can ever be made")
				}
				if gs.corr.id != c.cfg.StoreID {
					t.Errorf("codes are bound to %q, want the stored identifier %q", gs.corr.id, c.cfg.StoreID)
				}
			case err == nil:
				t.Fatalf("the configuration was accepted, want a refusal saying %q", c.want)
			case !strings.Contains(err.Error(), c.want):
				t.Fatalf("refused with %q, want it to say %q", err, c.want)
			}
		})
	}
}

// --- refusals a player has to be able to tell apart -------------------------

// TestARefusedCodeSaysWhichKindOfWrongItIs is the difference between a mode a
// player can use and one they cannot: the four ways a code can be wrong call for
// four different things to do about it.
func TestARefusedCodeSaysWhichKindOfWrongItIs(t *testing.T) {
	tb := newCorrTable(t, 6)
	opening := game.Point{Col: 1, Row: 0}
	code := tb.host.move(opening)
	tb.guest.applyCode(code)

	// The guest is now to move, with one entry on the record.
	otherGame := corrCode(t, "some-other-game", tb.rules, []game.Point{opening})
	// A game of the same name whose opening went elsewhere: the same entry
	// count, a different board.
	otherPosition := corrCode(t, tb.id, tb.rules,
		[]game.Point{{Col: 4, Row: 0}, {Col: 5, Row: 1}})
	mangled := corrMangle(t, code)

	cases := []struct {
		name     string
		code     string
		category string
		detail   string
	}{
		{
			name:     "a code for another game",
			code:     otherGame,
			category: "that code belongs to a different game:",
			detail:   "belongs to a different game",
		},
		{
			name:     "a code already applied",
			code:     code,
			category: "that code has already been applied:",
			detail:   "it has been applied already",
		},
		{
			name:     "a code for another position",
			code:     otherPosition,
			category: "that code was made for a different position:",
			detail:   "made for a different position",
		},
		{
			name:     "a code that did not survive the trip",
			code:     mangled,
			category: "that code did not arrive intact:",
			detail:   "did not survive being copied",
		},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := netplay.PositionHash(tb.guest.h.s.g)
			entries := tb.guest.h.s.g.Entries()

			tb.guest.applyCode(c.code)

			note := tb.guest.h.s.corr.note
			if !strings.HasPrefix(note, c.category) {
				t.Fatalf("the refusal reads %q, want it to start %q", note, c.category)
			}
			if !strings.Contains(note, c.detail) {
				t.Errorf("the refusal does not carry netplay's own explanation %q: %q", c.detail, note)
			}
			if seen[c.category] {
				t.Errorf("two different refusals give the same message %q", c.category)
			}
			seen[c.category] = true

			if got := netplay.PositionHash(tb.guest.h.s.g); got != before {
				t.Error("the refused code changed the position")
			}
			if got := tb.guest.h.s.g.Entries(); got != entries {
				t.Errorf("the refused code left %d entries, want %d", got, entries)
			}
			if !strings.Contains(tb.guest.h.frame(), strings.TrimSuffix(c.category, ":")) {
				t.Errorf("the refusal is not on screen:\n%s", tb.guest.h.frame())
			}
			// The refused code stays in the field, so the player can see what
			// was refused rather than being left with an empty box. The next
			// case starts from an empty one.
			if got := tb.guest.h.s.corr.edit.value(); !strings.Contains(got, "TWX") {
				t.Errorf("the refused code was thrown away: the field holds %q", got)
			}
			tb.guest.h.feed(corrCtrlKey(t, "ctrl+u"))
			if got := tb.guest.h.s.corr.edit.value(); got != "" {
				t.Fatalf("clearing the field left %q", got)
			}
		})
	}
}

// TestACodeAppliedTwiceLeavesTheGameAlone is the one refusal with a stored
// consequence: a code that was already played must not advance the game a second
// time, and must not damage what is on disk either.
func TestACodeAppliedTwiceLeavesTheGameAlone(t *testing.T) {
	tb := newCorrTable(t, 6)
	code := tb.host.move(game.Point{Col: 1, Row: 0})

	tb.guest.applyCode(code)
	after := tb.guest.saved()
	position := netplay.PositionHash(tb.guest.h.s.g)

	tb.guest.h.feed(tea.PasteMsg{Content: code})
	tb.guest.h.press("enter")

	if got := tb.guest.h.s.corr.note; !strings.HasPrefix(got, "that code has already been applied:") {
		t.Fatalf("the second paste was answered with %q", got)
	}
	if got := netplay.PositionHash(tb.guest.h.s.g); got != position {
		t.Error("the second paste moved the game on")
	}
	if got := tb.guest.h.s.g.Entries(); got != 1 {
		t.Errorf("the record holds %d entries after the same code twice, want 1", got)
	}
	if got := tb.guest.saved(); got.Record != after.Record {
		t.Error("the second paste rewrote the stored game")
	}
}

// --- closed between turns ---------------------------------------------------

// TestAGameClosedBetweenTurnsCarriesOn is the whole point of the mode: a player
// opens the game, makes one move, and closes it, perhaps for days.
func TestAGameClosedBetweenTurnsCarriesOn(t *testing.T) {
	tb := newCorrTable(t, 6)

	first := tb.host.move(game.Point{Col: 1, Row: 0})
	tb.host.close()

	tb.guest.applyCode(first)
	reply := tb.guest.move(game.Point{Col: 5, Row: 1})
	tb.guest.close()

	tb.host.open()
	if got := tb.host.h.s.g.Entries(); got != 1 {
		t.Fatalf("the reopened game holds %d entries, want the 1 that was saved", got)
	}
	tb.host.applyCode(reply)
	if got := tb.host.h.s.g.Entries(); got != 2 {
		t.Fatalf("the reopened game holds %d entries after the reply, want 2", got)
	}

	tb.guest.open()
	if got, want := tb.host.storedPosition(), tb.guest.storedPosition(); got != want {
		t.Fatal("the two ends disagree after being closed and opened between turns")
	}

	// The move the host now makes has to follow on from the one they made
	// before the game was ever closed, which is the check that reopening kept
	// the record rather than merely the board.
	third := tb.host.move(game.Point{Col: 2, Row: 2})
	if mc, err := netplay.Inspect(third); err != nil {
		t.Fatalf("the code made after reopening is unreadable: %v", err)
	} else if mc.Entries != 2 {
		t.Errorf("the code follows entry %d, want 2", mc.Entries)
	}
	tb.guest.applyCode(third)
	if got, want := tb.host.storedPosition(), tb.guest.storedPosition(); got != want {
		t.Fatal("the two ends disagree after a move made on a reopened game")
	}
}

// TestClosingBeforeCopyingTheCodeDoesNotLoseIt covers the mistake this mode
// invites: the code lives only on screen, and a player who shuts the terminal
// before copying it would otherwise leave the game unable to go on at either
// end.
func TestClosingBeforeCopyingTheCodeDoesNotLoseIt(t *testing.T) {
	tb := newCorrTable(t, 6)
	want := tb.host.move(game.Point{Col: 1, Row: 0})
	tb.host.close()

	tb.host.open()
	tb.host.h.press("c")
	if got := tb.host.codeOnScreen(); got != want {
		t.Fatalf("the reopened game offers %q, want the code for the move it holds, %q", got, want)
	}
	tb.guest.applyCode(want)
	if got, want := tb.guest.h.s.g.Entries(), 1; got != want {
		t.Fatalf("the recovered code applied to %d entries, want %d", got, want)
	}
}

// --- codes made elsewhere ---------------------------------------------------

// corrCode builds the code a game somewhere else would send, which is how a test
// produces a code that is perfectly valid and still wrong for the game it is
// pasted into.
func corrCode(t *testing.T, id string, rules game.Ruleset, moves []game.Point) string {
	t.Helper()
	g, err := game.New(rules)
	if err != nil {
		t.Fatalf("building the other game: %v", err)
	}
	for _, at := range moves {
		if err := g.PlacePeg(at); err != nil {
			t.Fatalf("placing %v in the other game: %v", at, err)
		}
		if _, err := g.CommitTurn(); err != nil {
			t.Fatalf("committing %v in the other game: %v", at, err)
		}
	}
	code, err := netplay.EncodeLastMove(g, id)
	if err != nil {
		t.Fatalf("encoding the other game's move: %v", err)
	}
	return code
}

// corrMangle changes one character of a code to another the alphabet allows, so
// that what is refused is the checksum rather than the shape.
//
// The character is taken from the middle. The last one carries the padding bits
// of the base32, which netplay checks before the checksum and reports as a
// truncated code, and a truncated code is a different accident from one that
// arrived complete and wrong.
func corrMangle(t *testing.T, code string) string {
	t.Helper()
	runes := []rune(code)
	for i := len(runes) / 2; i < len(runes)-1; i++ {
		if runes[i] == '-' {
			continue
		}
		if runes[i] == '2' {
			runes[i] = '3'
		} else {
			runes[i] = '2'
		}
		return string(runes)
	}
	t.Fatalf("there is nothing to change in %q", code)
	return ""
}

// corrCtrlKey spells a control key the shared rig has no case for. It checks,
// as that rig does, that the message really encodes as the string the field
// binds, so a test cannot prove a binding against a key it invented.
func corrCtrlKey(t *testing.T, key string) tea.KeyPressMsg {
	t.Helper()
	letter, ok := strings.CutPrefix(key, "ctrl+")
	if !ok || len([]rune(letter)) != 1 {
		t.Fatalf("the test rig cannot encode the key %q", key)
	}
	msg := tea.KeyPressMsg(tea.Key{Code: []rune(letter)[0], Mod: tea.ModCtrl})
	if got := msg.String(); got != key {
		t.Fatalf("the constructed key encodes as %q, want %q", got, key)
	}
	return msg
}
