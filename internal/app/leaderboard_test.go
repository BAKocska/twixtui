package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/ui"
)

// lbScreen opens the standings at a size and returns the screen itself, which
// is what the assertions about selection and identity are made against. What is
// drawn comes from View, so a test can assert on both without either standing
// in for the other.
func lbScreen(t *testing.T, d Deps, width, height int) *LeaderboardScreen {
	t.Helper()
	sc, err := NewLeaderboardScreen(d)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := sc.(*LeaderboardScreen)
	if !ok {
		t.Fatalf("NewLeaderboardScreen returned %T", sc)
	}
	s.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return s
}

// mnLeaderboardFrame opens the standings the way a player does — through the
// menu entry — and returns what is on screen. Going through the menu is what
// makes it evidence that the entry leads here at all.
func mnLeaderboardFrame(t *testing.T, d Deps) string {
	t.Helper()
	m := mnMenu(t, d, 100, 30)
	cmd := mnPick(t, m, "Leaderboard")
	if cmd == nil {
		t.Fatal("choosing the leaderboard produced no command")
	}
	open, ok := cmd().(OpenMsg)
	if !ok {
		t.Fatalf("choosing the leaderboard produced %T, want an OpenMsg", cmd())
	}
	s, ok := open.Screen.(*LeaderboardScreen)
	if !ok {
		t.Fatalf("the menu opened %T, want the leaderboard screen", open.Screen)
	}
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return s.View().Content
}

// lbSend delivers a key to the screen and returns the command it produced.
func lbSend(t *testing.T, s *LeaderboardScreen, key string) tea.Cmd {
	t.Helper()
	return shellSend(t, s, key)
}

// lbSelect moves the highlight onto the participant stored under name, with
// real keypresses, so that the row is proved reachable rather than assigned to.
func lbSelect(t *testing.T, s *LeaderboardScreen, stored string) int {
	t.Helper()
	want := -1
	for i, p := range s.people {
		if p.stored == stored {
			want = i
			break
		}
	}
	if want < 0 {
		names := make([]string, 0, len(s.people))
		for _, p := range s.people {
			names = append(names, p.stored)
		}
		t.Fatalf("no participant stored as %q; have %v", stored, names)
	}
	for range len(s.people) {
		if s.standSel == want {
			break
		}
		lbSend(t, s, "down")
	}
	if s.standSel != want {
		t.Fatalf("could not move the highlight onto %q with the down key", stored)
	}
	return want
}

// lbHistoryOf opens one participant's games.
func lbHistoryOf(t *testing.T, s *LeaderboardScreen, stored string) {
	t.Helper()
	lbSelect(t, s, stored)
	if cmd := lbSend(t, s, "enter"); cmd != nil {
		t.Fatalf("opening %q produced a command: %v", stored, cmd())
	}
	if !s.inHistory {
		t.Fatalf("enter on %q did not open their games: %q", stored, s.message)
	}
}

// lbRow finds the line of a frame containing want.
func lbRow(t *testing.T, frame, want string) (int, string) {
	t.Helper()
	for i, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, want) {
			return i, l
		}
	}
	t.Fatalf("no line naming %q on screen:\n%s", want, frame)
	return 0, ""
}

// lbCells splits a drawn row into its columns, without the highlight marker in
// front of it: the marker is two cells on every row, highlighted or not, and it
// is not one of the columns.
func lbCells(row string) []string {
	return strings.Fields(strings.TrimLeft(row, "> "))
}

// lbFinishedRecord plays a game to a real result and returns its record, so
// that a replay link points at something the replay screen will actually
// accept. The fixture checks itself: a record that no longer ended the game
// would make every link test pass for the wrong reason.
//
// offers prepends that many draw offers, which are entries that change nothing
// on the board. Two records of the same game differing only in them are what a
// digest has to be able to tell apart, and it is how a test makes a second
// record without making a second, differently played game.
func lbFinishedRecord(t *testing.T, offers int) game.Record {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for range offers {
		if err := g.OfferDraw(g.Turn()); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("playing %s: %v", m, err)
		}
	}
	if !g.Result().Over() {
		t.Fatal("the replay fixture no longer finishes the game")
	}
	if got := g.Entries(); got != 7+offers {
		t.Fatalf("the fixture makes %d entries, want %d", got, 7+offers)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// lbSaveFinished stores a finished game and returns it with its record.
func lbSaveFinished(t *testing.T, d Deps, player, opponent string, offers int) (gamestore.Saved, game.Record) {
	t.Helper()
	rec := lbFinishedRecord(t, offers)
	sv := gamestore.Saved{
		ID:       gamestore.NewID(),
		Kind:     gamestore.VersusBot,
		Created:  time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		Player:   player,
		Side:     "vertical",
		Opponent: opponent,
		Record:   rec.Encode(),
		Finished: true,
	}
	if err := d.Games.Put(sv); err != nil {
		t.Fatal(err)
	}
	return sv, rec
}

// lbRecordWin records a win for player, linked to a saved game or not.
func lbRecordWin(t *testing.T, d Deps, player, opponent, id, digest string, played time.Time) {
	t.Helper()
	if err := d.Board.Record(leaderboard.Result{
		Played: played, Player: player, Opponent: opponent,
		Outcome: leaderboard.Win, Side: "vertical", Moves: 7,
		Ruleset: game.Std.Canonical(), GameID: id, RecordDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
}

// lbStoreRecord writes text straight into a stored game's file as its record.
// It goes to the file rather than through the store because that is what a
// hand edit, a restored backup or a half-finished write leaves behind, and
// because it changes the game under a list that is already on screen, which is
// the case the checks made on opening exist for.
func lbStoreRecord(t *testing.T, d Deps, sv gamestore.Saved, record string) {
	t.Helper()
	sv.Record = record
	body, err := json.Marshal(sv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lbStoredPath(d, sv.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// lbDamageRecord replaces a stored game's record with text that will not
// decode, which is what an edited or truncated record looks like from here.
func lbDamageRecord(t *testing.T, d Deps, sv gamestore.Saved) {
	t.Helper()
	lbStoreRecord(t, d, sv, "twixtui-record\nnot a record at all\n")
}

// lbDamageFile replaces the whole of a stored game's file with something that
// is not the JSON the store writes. It is a different failure from a record
// that will not decode and a different one again from a game that is not
// there: the file exists, so a row reporting it as gone would be telling the
// player their game had been deleted.
func lbDamageFile(t *testing.T, d Deps, id string) {
	t.Helper()
	if err := os.WriteFile(lbStoredPath(d, id), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lbStoredPath(d Deps, id string) string {
	return filepath.Join(d.Games.Dir(), id+".json")
}

// lbStoredBytes is a saved game's file as it stands, or "" where there is no
// file. Reading a game is reading: refusing to open one must not write to it.
func lbStoredBytes(t *testing.T, d Deps, id string) string {
	t.Helper()
	if id == "" {
		return ""
	}
	body, err := os.ReadFile(lbStoredPath(d, id))
	if err != nil {
		return ""
	}
	return string(body)
}

// lbSelectedRow is the line the highlight is on, which is the only row the
// panel says anything more about.
func lbSelectedRow(t *testing.T, frame string) string {
	t.Helper()
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "> ") {
			return l
		}
	}
	t.Fatalf("no row is highlighted on screen:\n%s", frame)
	return ""
}

// lbStatus is the bottom row of a frame, which is where the keys are named.
func lbStatus(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[len(lines)-1]
}

// lbNameCell is a standings row read as a name: the position in front of it
// and the numbers after it taken off, which leaves the part of the row a
// player reads to tell two participants apart.
func lbNameCell(row string) string {
	cells := lbCells(row)
	if len(cells) > 0 && lbNumeric(cells[0]) {
		cells = cells[1:]
	}
	for len(cells) > 0 && lbNumeric(cells[len(cells)-1]) {
		cells = cells[:len(cells)-1]
	}
	return strings.Join(cells, " ")
}

// lbNumeric reports whether a cell is one of the numeric columns: a position,
// a rating, a count of games or a score.
func lbNumeric(cell string) bool {
	cell = strings.TrimSuffix(cell, "%")
	if cell == "" {
		return false
	}
	return strings.IndexFunc(cell, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// lbAssertReasonVisible requires what stops a row being opened to be readable
// on screen. The words are the state's own rather than this test's, so the
// reason may be reworded freely; what is pinned is that all of it reaches the
// frame at the width in hand, which is what the narrowest pane used to lose.
func lbAssertReasonVisible(t *testing.T, frame string, state linkState, width int) {
	t.Helper()
	for _, word := range strings.Fields(state.why()) {
		if !strings.Contains(frame, word) {
			t.Errorf("at %d columns %q of the reason %q is not on screen:\n%s", width, word, state.why(), frame)
		}
	}
}

// TestLeaderboardDoesNotCallALoserTheBest is F4 on the screen that replaced the
// static panel: a bot's rating is a fixed number, so ranking it with people made
// a player who had lost their only game come out first, above the bot that had
// just beaten them. The player must not be given a position at all while they
// are the only one, the bot must be below the ranking under a line saying it is
// not part of it, and the bot must not be somewhere a history opens from.
func TestLeaderboardDoesNotCallALoserTheBest(t *testing.T) {
	d := shellTestDeps(t)
	beginner := leaderboard.BotName("beginner")
	mnRecordLoss(t, d, "Balint", beginner, time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

	frame := mnLeaderboardFrame(t, d)
	playerAt, playerRow := lbRow(t, frame, "Balint")
	botAt, botRow := lbRow(t, frame, leaderboard.DisplayName(beginner))
	if playerAt > botAt {
		t.Errorf("the bot is listed above the player it beat:\n%s", frame)
	}
	if cells := lbCells(playerRow); len(cells) == 0 || cells[0] != "Balint" {
		t.Errorf("the only player is given a position on a board of one: %q", playerRow)
	}
	unrankedAt, _ := lbRow(t, frame, "not ranked")
	if unrankedAt < playerAt || unrankedAt > botAt {
		t.Errorf("nothing between the player and the bot says the bot is not ranked:\n%s", frame)
	}
	// The score is read off the row it belongs to. The bot's own 100% holds
	// the "0%" any frame is searched for, so a frame containing both proves
	// nothing about either of them.
	if cells := lbCells(playerRow); cells[len(cells)-1] != "0%" {
		t.Errorf("the player's score reads %q, want the 0%% of the game they lost: %q",
			cells[len(cells)-1], playerRow)
	}
	if cells := lbCells(botRow); cells[len(cells)-1] != "100%" {
		t.Errorf("the bot's score reads %q, want the 100%% of the game it won: %q",
			cells[len(cells)-1], botRow)
	}

	// The rate counts half of every draw, so it is a score and not a win rate.
	if !strings.Contains(frame, "score") {
		t.Errorf("the rate column is not labelled score:\n%s", frame)
	}
	if strings.Contains(frame, "wins") || strings.Contains(frame, "win rate") {
		t.Errorf("the rate column is labelled as wins, but it counts half of every draw:\n%s", frame)
	}

	// And the tier is not a place a history can be opened from: the only thing
	// the list offers is the person.
	s := lbScreen(t, d, 100, 30)
	if len(s.people) != 1 || s.people[0].stored != "Balint" {
		t.Fatalf("the choosable participants are %+v, want the one person", s.people)
	}
	for range 6 {
		lbSend(t, s, "down")
		if s.people[s.standSel].stored != "Balint" {
			t.Fatalf("the highlight reached %q, which is not a person", s.people[s.standSel].stored)
		}
	}

	// The bots are a block under the list rather than rows in it, and a short
	// pane is where that block went missing: a board of one person needs one
	// row of list, and what is left is enough for the tier and for the line
	// saying it is not in the ranking.
	short := lbScreen(t, d, 40, 10)
	shortFrame := short.View().Content
	shellAssertFits(t, "standings in a short pane", shortFrame, 40, 10)
	for _, want := range []string{"Balint", leaderboard.DisplayName(beginner), "not ranked"} {
		if !strings.Contains(shortFrame, want) {
			t.Errorf("at 40x10 the standings no longer show %q:\n%s", want, shortFrame)
		}
	}
}

// TestLeaderboardNumbersPlayersOnceThereAreTwo: the position column is withheld
// only while it would be vacuous. With two people it is a ranking again, and it
// must agree with the ratings.
func TestLeaderboardNumbersPlayersOnceThereAreTwo(t *testing.T) {
	d := shellTestDeps(t)
	mnRecordLoss(t, d, "Balint", "Reka", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

	frame := mnLeaderboardFrame(t, d)
	rekaAt, rekaRow := lbRow(t, frame, "Reka")
	balintAt, balintRow := lbRow(t, frame, "Balint")
	if rekaAt > balintAt {
		t.Errorf("the winner is listed below the loser:\n%s", frame)
	}
	if got := lbCells(rekaRow)[0]; got != "1" {
		t.Errorf("the winner's position is %q, want 1: %q", got, rekaRow)
	}
	if got := lbCells(balintRow)[0]; got != "2" {
		t.Errorf("the loser's position is %q, want 2: %q", got, balintRow)
	}
}

func TestLeaderboardSeparatesFourDigitRanksFromNames(t *testing.T) {
	d := shellTestDeps(t)
	results := make([]leaderboard.Result, 1000)
	for i := range results {
		results[i] = leaderboard.Result{
			Played: d.Clock(), Player: fmt.Sprintf("Player%04d", i),
			Opponent: leaderboard.BotName("beginner"), Outcome: leaderboard.Win,
			Side: "vertical", Moves: 1, Ruleset: game.Std.Canonical(),
		}
	}
	data, err := json.Marshal(map[string]any{"version": 1, "results": results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.Board.Path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := lbScreen(t, d, 100, 34)
	lbSend(t, s, "up")
	_, row := lbRow(t, s.View().Content, "Player0999")
	cells := lbCells(row)
	if len(cells) < 2 || cells[0] != "1000" || cells[1] != "Player0999" {
		t.Fatalf("the rank and participant name are not distinct cells: %q", row)
	}
}

// TestLeaderboardShowsEachPlayerTheirOwnSideOfTheGame is the failure RM-06 names
// first: one row is recorded from one side, and the other player's history is
// that row read backwards. Showing it as recorded makes a player their own
// opponent, with the other side's result and the other side's axis.
func TestLeaderboardShowsEachPlayerTheirOwnSideOfTheGame(t *testing.T) {
	d := shellTestDeps(t)
	lbRecordWin(t, d, "Balint", "Reka", "", "", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

	for _, tc := range []struct {
		who, opponent, outcome, side string
	}{
		{"Balint", "Reka", "win", "vertical"},
		{"Reka", "Balint", "loss", "horizontal"},
	} {
		t.Run(tc.who, func(t *testing.T) {
			s := lbScreen(t, d, 100, 30)
			lbHistoryOf(t, s, tc.who)
			if len(s.games) != 1 {
				t.Fatalf("%s has %d games, want the one they played", tc.who, len(s.games))
			}
			got := s.games[0].res
			if got.Player != tc.who || got.Opponent != tc.opponent {
				t.Errorf("%s's row is %s against %s", tc.who, got.Player, got.Opponent)
			}
			if string(got.Outcome) != tc.outcome || got.Side != tc.side {
				t.Errorf("%s's row says %s on the %s side, want %s on %s",
					tc.who, got.Outcome, got.Side, tc.outcome, tc.side)
			}
			frame := s.View().Content
			if !strings.Contains(frame, tc.who) {
				t.Errorf("the games are not headed by whose they are:\n%s", frame)
			}
			_, row := lbRow(t, frame, tc.opponent)
			if !strings.Contains(row, tc.outcome) || !strings.Contains(row, tc.side) {
				t.Errorf("the row shown to %s is %q, want %s on %s", tc.who, row, tc.outcome, tc.side)
			}
		})
	}
}

// TestLeaderboardTellsTwoPlayersShownUnderOneNameApart is the identity failure:
// a networked opponent called Bea is shown as "Bea (remote)", and so is a local
// profile somebody named "Bea (remote)". Two rows of identical text are a choice
// the player cannot make, so each row has to say which participant it is — and
// choosing one has to open that one's games, which the opponents in them prove.
func TestLeaderboardTellsTwoPlayersShownUnderOneNameApart(t *testing.T) {
	d := shellTestDeps(t)
	const collide = "Bea (remote)"
	if _, err := d.Profiles.Create(collide); err != nil {
		t.Fatal(err)
	}
	remote := leaderboard.RemoteName("Bea")
	lbRecordWin(t, d, collide, "Cy", "", "", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))
	lbRecordWin(t, d, "Ada", remote, "", "", time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC))

	s := lbScreen(t, d, 100, 30)
	var local, net participant
	for _, p := range s.people {
		switch p.stored {
		case collide:
			local = p
		case remote:
			net = p
		}
	}
	if local.stored == "" || net.stored == "" {
		t.Fatalf("the standings hold %+v, want both the local profile and the networked opponent", s.people)
	}
	if local.shown == net.shown {
		t.Fatalf("both participants are drawn as %q, so the player is choosing between two identical lines", local.shown)
	}
	// Telling them apart has to survive a pane too narrow to draw either name
	// whole, so neither row may be the other with something added to its end:
	// what says which participant it is goes in front, and the networked one
	// carries the namespace the board stores it under.
	if !strings.Contains(net.shown, leaderboard.RemotePrefix) {
		t.Errorf("the networked opponent is drawn as %q, which does not say that is what it is", net.shown)
	}
	if strings.HasPrefix(local.shown, net.shown) || strings.HasPrefix(net.shown, local.shown) {
		t.Errorf("the rows are %q and %q, one of which begins with the other, so a pane that cuts them draws them alike",
			local.shown, net.shown)
	}
	frame := s.View().Content
	for _, want := range []string{local.shown, net.shown} {
		if !strings.Contains(frame, want) {
			t.Errorf("the row %q is not on screen:\n%s", want, frame)
		}
	}

	// Each row opens its own participant's games, which the opponent in them
	// says: nothing here resolves a selection out of the text that was drawn.
	for _, tc := range []struct{ stored, opponent string }{
		{collide, "Cy"},
		{remote, "Ada"},
	} {
		fresh := lbScreen(t, d, 100, 30)
		lbHistoryOf(t, fresh, tc.stored)
		if len(fresh.games) != 1 {
			t.Fatalf("%q has %d games, want one", tc.stored, len(fresh.games))
		}
		if got := fresh.games[0].res.Opponent; got != tc.opponent {
			t.Errorf("%q was shown a game against %q, want %q", tc.stored, got, tc.opponent)
		}
	}
}

// TestLeaderboardKeepsCollidingNamesApartWhereItIsTightest: two participants
// drawn under one name have to stay apart where a row has least room, and in
// the opponent column of the games, which is the same collision seen from
// inside. A qualification added to the end of a name does neither: the cut a
// narrow pane makes takes it off, and the two rows are one line of text again.
func TestLeaderboardKeepsCollidingNamesApartWhereItIsTightest(t *testing.T) {
	d := shellTestDeps(t)
	const collide = "Bea (remote)"
	if _, err := d.Profiles.Create(collide); err != nil {
		t.Fatal(err)
	}
	remote := leaderboard.RemoteName("Bea")
	// The two of them played each other, so the board holds nobody else and
	// each one's history is a game against the other.
	lbRecordWin(t, d, collide, remote, "", "", time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))

	for _, size := range shellSizes {
		w, h := size[0], size[1]
		s := lbScreen(t, d, w, h)
		drawn := make(map[string]string, 2)
		for _, stored := range []string{collide, remote} {
			lbSelect(t, s, stored)
			frame := s.View().Content
			shellAssertFits(t, "standings", frame, w, h)
			name := lbNameCell(lbSelectedRow(t, frame))
			if name == "" {
				t.Fatalf("at %dx%d the row highlighted for %q carries no name:\n%s", w, h, stored, frame)
			}
			if other, ok := drawn[name]; ok {
				t.Errorf("at %dx%d %q and %q are both drawn as %q:\n%s", w, h, other, stored, name, frame)
			}
			drawn[name] = stored
		}
	}

	// The opponent column is a participant's name as well, and one of these
	// two named there as the other is the same choice the standings had to
	// stop offering.
	said := make(map[string]string, 2)
	for _, tc := range []struct{ stored, opponent string }{
		{collide, remote},
		{remote, collide},
	} {
		s := lbScreen(t, d, 100, 30)
		lbHistoryOf(t, s, tc.stored)
		if len(s.games) != 1 {
			t.Fatalf("%q has %d games, want the one they played", tc.stored, len(s.games))
		}
		g := s.games[0]
		if g.res.Opponent != tc.opponent {
			t.Errorf("%q was shown a game against %q, want %q", tc.stored, g.res.Opponent, tc.opponent)
		}
		if other, ok := said[g.opponent]; ok {
			t.Errorf("the games of %q and %q both call their opponent %q", other, tc.stored, g.opponent)
		}
		said[g.opponent] = tc.stored
		if row := lbSelectedRow(t, s.View().Content); !strings.Contains(row, g.opponent) {
			t.Errorf("the row %q does not name the opponent %q", row, g.opponent)
		}
	}
}

// TestLeaderboardOpensOnlyTheGameTheResultNames covers every way a row can fail
// to be the game it points at. Each of them stays in the list and says why,
// rather than guessing a game or disappearing.
func TestLeaderboardOpensOnlyTheGameTheResultNames(t *testing.T) {
	cases := []struct {
		name string
		// seed sets the store up and returns the identity to record with.
		seed func(t *testing.T, d Deps) (id, digest string)
		want linkState
	}{
		{
			name: "the game it was recorded from",
			seed: func(t *testing.T, d Deps) (string, string) {
				sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
				return sv.ID, rec.Digest
			},
			want: linkReady,
		},
		{
			name: "a board written before the link",
			seed: func(*testing.T, Deps) (string, string) { return "", "" },
			want: linkNone,
		},
		{
			name: "a game that is no longer on this machine",
			seed: func(t *testing.T, d Deps) (string, string) {
				sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
				if err := d.Games.Delete(sv.ID); err != nil {
					t.Fatal(err)
				}
				return sv.ID, rec.Digest
			},
			want: linkMissing,
		},
		{
			name: "a game holding a different record",
			seed: func(t *testing.T, d Deps) (string, string) {
				sv, _ := lbSaveFinished(t, d, "Balint", "Reka", 0)
				// Another game's digest: the identifier still names a stored
				// game, and that stored game is not the one that was rated.
				return sv.ID, lbFinishedRecord(t, 2).Digest
			},
			want: linkChanged,
		},
		{
			name: "a game whose record will not load",
			seed: func(t *testing.T, d Deps) (string, string) {
				sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
				lbDamageRecord(t, d, sv)
				return sv.ID, rec.Digest
			},
			want: linkUnreadable,
		},
		{
			name: "a game whose file will not parse",
			seed: func(t *testing.T, d Deps) (string, string) {
				sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
				// The file is there and the store cannot read it, which is
				// neither a game that is gone nor a record that will not
				// decode. Reporting it as gone would tell the player their
				// saved game had been deleted when it is still on the disk.
				lbDamageFile(t, d, sv.ID)
				return sv.ID, rec.Digest
			},
			want: linkUnreadable,
		},
	}
	// How each failure is worded is the product's business, but two different
	// failures worded the same leave the player unable to tell a game that is
	// gone from one that changed. The refusals are therefore compared with
	// each other rather than with sentences written here.
	said := make(map[linkState]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := shellTestDeps(t)
			id, digest := tc.seed(t, d)
			lbRecordWin(t, d, "Balint", "Reka", id, digest, time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

			s := lbScreen(t, d, 100, 30)
			lbHistoryOf(t, s, "Balint")
			if len(s.games) != 1 {
				t.Fatalf("%d rows, want the one recorded game", len(s.games))
			}
			if got := s.games[0].link; got != tc.want {
				t.Errorf("the row reads as %v, want %v", got, tc.want)
			}
			// However it ends, the game is still history: a row that cannot be
			// opened is not a row that is hidden.
			frame := s.View().Content
			if !strings.Contains(frame, "Reka") {
				t.Errorf("the game against Reka is not listed at all:\n%s", frame)
			}

			stored := lbStoredBytes(t, d, id)
			cmd := lbSend(t, s, "enter")
			if tc.want == linkReady {
				if cmd == nil {
					t.Fatal("the linked game did not open")
				}
				open, ok := cmd().(OpenMsg)
				if !ok {
					t.Fatalf("opening produced %T, want an OpenMsg", cmd())
				}
				if _, ok := open.Screen.(*ReplayScreen); !ok {
					t.Fatalf("opened %T, want the replay screen", open.Screen)
				}
				return
			}
			if cmd != nil {
				t.Fatalf("a row that is not its game produced %v", cmd())
			}
			if s.message == "" {
				t.Fatal("the row was refused with nothing said about why")
			}
			said[tc.want] = s.message
			// Refusing to open a game is still only reading it.
			if after := lbStoredBytes(t, d, id); after != stored {
				t.Errorf("the refusal rewrote the saved game:\nbefore:\n%s\nafter:\n%s", stored, after)
			}
			lbAssertReasonVisible(t, s.View().Content, tc.want, 100)

			// And it is readable in the narrowest pane the product supports,
			// where the explanation gets two lines and nothing else.
			narrow := lbScreen(t, d, ui.MinWidth, 8)
			lbHistoryOf(t, narrow, "Balint")
			lbSend(t, narrow, "enter")
			shellAssertFits(t, "history at the minimum width", narrow.View().Content, ui.MinWidth, 8)
			lbAssertReasonVisible(t, narrow.View().Content, tc.want, ui.MinWidth)
		})
	}
	for state, message := range said {
		for other, alike := range said {
			if state != other && message == alike {
				t.Errorf("%v and %v are both refused with %q, so the two cannot be told apart", state, other, message)
			}
		}
	}
}

// lbAssertRefusesToOpen chooses the highlighted row and requires the list to
// keep it, in the state the check has just found, saying why.
func lbAssertRefusesToOpen(t *testing.T, s *LeaderboardScreen, want linkState) {
	t.Helper()
	at := s.histSel
	if cmd := lbSend(t, s, "enter"); cmd != nil {
		t.Fatalf("a row that is not its game opened one: %v", cmd())
	}
	if got := s.games[at].link; got != want {
		t.Errorf("the row reads as %v after it was opened, want %v", got, want)
	}
	if s.message == "" {
		t.Error("the row was refused with nothing said about why")
	}
	frame := s.View().Content
	lbAssertReasonVisible(t, frame, want, 100)
	if row := lbSelectedRow(t, frame); !strings.Contains(row, want.mark()) {
		t.Errorf("the row %q is not marked as one that will not open:\n%s", row, frame)
	}
}

// lbAssertOpensReplay chooses the highlighted row and requires the game.
func lbAssertOpensReplay(t *testing.T, s *LeaderboardScreen) {
	t.Helper()
	at := s.histSel
	cmd := lbSend(t, s, "enter")
	if cmd == nil {
		t.Fatalf("the game did not open: %q", s.message)
	}
	open, ok := cmd().(OpenMsg)
	if !ok {
		t.Fatalf("opening produced %T, want an OpenMsg", cmd())
	}
	if _, ok := open.Screen.(*ReplayScreen); !ok {
		t.Fatalf("opened %T, want the replay screen", open.Screen)
	}
	if got := s.games[at].link; got != linkReady {
		t.Errorf("the row reads as %v after opening it worked", got)
	}
}

// TestLeaderboardChecksTheGameAgainWhenItIsOpened: a list is a snapshot, and
// what it says about a game can stop being true while the player is reading
// it. A game can go away and come back, and it can be replaced under the list
// by another game that is every bit as valid — a hand edit, a restored backup,
// a file written again under the same name. That last one is what a row
// trusting what the listing found would open under this row's label, which is
// the one thing a link must not do.
//
// It is driven from both sides of the game: the participant it was recorded
// from, and the opponent, whose row is that same result read backwards.
func TestLeaderboardChecksTheGameAgainWhenItIsOpened(t *testing.T) {
	for _, who := range []string{"Balint", "Reka"} {
		t.Run(who, func(t *testing.T) {
			d := shellTestDeps(t)
			sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
			lbRecordWin(t, d, "Balint", "Reka", sv.ID, rec.Digest, time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

			s := lbScreen(t, d, 100, 30)
			lbHistoryOf(t, s, who)
			if len(s.games) != 1 {
				t.Fatalf("%s has %d games, want the one they played", who, len(s.games))
			}
			if got := s.games[0].link; got != linkReady {
				t.Fatalf("the row starts as %v, want a game that can be opened", got)
			}

			// Deleted under the list that is already on screen.
			if err := d.Games.Delete(sv.ID); err != nil {
				t.Fatal(err)
			}
			lbAssertRefusesToOpen(t, s, linkMissing)

			// Put back, and the same row opens: the check is made again rather
			// than remembered, in either direction.
			if err := d.Games.Put(sv); err != nil {
				t.Fatal(err)
			}
			lbAssertOpensReplay(t, s)

			// Replaced by a game that decodes and is not this one. Nothing
			// about the row has changed, so only a check made now catches it.
			other := lbFinishedRecord(t, 2)
			if other.Digest == rec.Digest {
				t.Fatal("the second record is the same game, so putting it in the file proves nothing")
			}
			lbStoreRecord(t, d, sv, other.Encode())
			lbAssertRefusesToOpen(t, s, linkChanged)

			// And written back as it was, the row opens again.
			lbStoreRecord(t, d, sv, rec.Encode())
			lbAssertOpensReplay(t, s)
		})
	}
}

// TestLeaderboardWalksBackThroughTheStack is the back stack RM-06 asks for:
// the replay returns to the history it was opened from, the history to the
// standings, and the standings to the menu — each of them where the player left
// it rather than rebuilt.
func TestLeaderboardWalksBackThroughTheStack(t *testing.T) {
	d := shellTestDeps(t)
	sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
	// The linked game is deliberately not the most recent, so that the row it
	// is opened from is not the row a rebuilt list would highlight.
	lbRecordWin(t, d, "Balint", "Reka", "", "", time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC))
	lbRecordWin(t, d, "Balint", "Reka", sv.ID, rec.Digest, time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC))
	lbRecordWin(t, d, "Reka", leaderboard.BotName("pro"), "", "", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

	s := NewShell(d, NewMenu(d, "Balint"))
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	lbShellRun(t, s, mnPick(t, lbTopMenu(t, s), "Leaderboard"))

	board := lbTopLeaderboard(t, s)
	// Reka rather than whoever is first, so a rebuilt screen would show up as a
	// highlight that moved back to the top.
	lbSelect(t, board, "Reka")
	standings := s.View().Content

	lbShellSend(t, s, "enter")
	if !board.inHistory {
		t.Fatalf("enter did not open the games: %q", board.message)
	}
	// Down onto the linked game, which is Reka's second row: recorded from
	// Balint's side and read here from hers.
	lbShellSend(t, s, "down")
	at := board.histSel
	if at != 1 {
		t.Fatalf("the highlight is on row %d, want the second row", at)
	}
	history := s.View().Content

	lbShellRun(t, s, lbShellSend(t, s, "enter"))
	if _, ok := s.top().(*ReplayScreen); !ok {
		t.Fatalf("the linked game opened %T, want the replay screen (%q)", s.top(), board.message)
	}

	// Escape from the replay comes back to the history, on the row it was
	// opened from.
	lbShellRun(t, s, lbShellSend(t, s, "esc"))
	if s.top() != Screen(board) {
		t.Fatalf("leaving the replay landed on %T", s.top())
	}
	if board.histSel != at {
		t.Errorf("the history came back on row %d, want the row %d it was left on", board.histSel, at)
	}
	if got := s.View().Content; got != history {
		t.Errorf("the history was rebuilt rather than revealed:\nbefore:\n%s\nafter:\n%s", history, got)
	}

	// Escape from the history comes back to the standings, likewise.
	lbShellSend(t, s, "esc")
	if board.inHistory {
		t.Fatal("escape did not leave the history")
	}
	if got := s.View().Content; got != standings {
		t.Errorf("the standings lost their place:\nbefore:\n%s\nafter:\n%s", standings, got)
	}

	// And escape from the standings comes back to the menu.
	lbShellRun(t, s, lbShellSend(t, s, "esc"))
	m := lbTopMenu(t, s)
	if m.form != nil {
		t.Errorf("the menu came back showing %T rather than its front list", m.form)
	}
	if !strings.Contains(s.View().Content, "Leaderboard") {
		t.Errorf("the front list is not on screen:\n%s", s.View().Content)
	}
}

// lbShellSend delivers a key to the shell and returns what it produced.
func lbShellSend(t *testing.T, s *Shell, key string) tea.Cmd {
	t.Helper()
	return shellSend(t, s, key)
}

// lbShellRun feeds a screen's command back into the shell, which is what the
// Bubble Tea runtime does with it.
func lbShellRun(t *testing.T, s *Shell, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("the command produced no message")
	}
	s.Update(msg)
}

func lbTopMenu(t *testing.T, s *Shell) *Menu {
	t.Helper()
	m, ok := s.top().(*Menu)
	if !ok {
		t.Fatalf("the screen on top is %T, want the menu", s.top())
	}
	return m
}

func lbTopLeaderboard(t *testing.T, s *Shell) *LeaderboardScreen {
	t.Helper()
	b, ok := s.top().(*LeaderboardScreen)
	if !ok {
		t.Fatalf("the screen on top is %T, want the leaderboard", s.top())
	}
	return b
}

// TestLeaderboardFitsEverySize is R3 for both of its lists: no line wider than
// the terminal and no more lines than it has rows, in the narrow forms of the
// rows that produces.
//
// It is about the shape of the frame and nothing else — a frame that fits by
// dropping what the row said fits too, and what has to survive being narrowed
// is TestLeaderboardKeepsWhatARowSaysAtEverySize below. The long name here is
// a fitting case for that reason: it is not required to be shown whole at
// twenty columns, only never to break the frame it is drawn in.
func TestLeaderboardFitsEverySize(t *testing.T) {
	d := shellTestDeps(t)
	const longName = "Bernadett-with-a-very-long-name"
	sv, rec := lbSaveFinished(t, d, "Balint", leaderboard.BotName("pro"), 0)
	lbRecordWin(t, d, "Balint", leaderboard.BotName("pro"), sv.ID, rec.Digest,
		time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))
	lbRecordWin(t, d, longName, leaderboard.RemoteName("Csilla"), "", "",
		time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC))

	for _, size := range shellSizes {
		w, h := size[0], size[1]
		s := lbScreen(t, d, w, h)
		shellAssertFits(t, "standings", s.View().Content, w, h)
		for range len(s.people) {
			lbSend(t, s, "down")
			shellAssertFits(t, "standings", s.View().Content, w, h)
		}
		// One history with a game that opens, one with a game that does not:
		// the two draw different rows and different explanations.
		for _, who := range []string{"Balint", longName} {
			fresh := lbScreen(t, d, w, h)
			lbHistoryOf(t, fresh, who)
			shellAssertFits(t, "history of "+who, fresh.View().Content, w, h)
			for range len(fresh.games) {
				lbSend(t, fresh, "down")
				shellAssertFits(t, "history of "+who, fresh.View().Content, w, h)
			}
			lbSend(t, fresh, "enter")
			shellAssertFits(t, "history of "+who+" after enter", fresh.View().Content, w, h)
		}
	}
}

// TestLeaderboardKeepsWhatARowSaysAtEverySize is the other half of R3. A
// history row that fits by losing how the game ended, or standings that fit by
// losing every number beside the player, are lists nobody can read — and both
// are what the minimum size produced.
//
// The names are short so that what the narrow pane leaves out is this screen's
// decision rather than a fixture no width could draw.
func TestLeaderboardKeepsWhatARowSaysAtEverySize(t *testing.T) {
	d := shellTestDeps(t)
	lbRecordWin(t, d, "Bea", "Ada", "", "", time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))

	for _, size := range shellSizes {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			s := lbScreen(t, d, w, h)
			at := lbSelect(t, s, "Ada")
			p := s.people[at]
			frame := s.View().Content
			shellAssertFits(t, "standings", frame, w, h)

			row := lbSelectedRow(t, frame)
			if !strings.Contains(row, p.shown) {
				t.Errorf("the highlighted row %q does not name %q:\n%s", row, p.shown, frame)
			}
			if cells := lbCells(row); p.rank > 0 && cells[0] != strconv.Itoa(p.rank) {
				t.Errorf("the highlighted row %q opens with %q, want the position %d", row, cells[0], p.rank)
			}
			if rating := strconv.Itoa(p.st.Rating); !strings.Contains(frame, rating) {
				t.Errorf("the rating %s of the highlighted player is nowhere on screen:\n%s", rating, frame)
			}

			lbHistoryOf(t, s, "Ada")
			g := s.games[s.histSel]
			frame = s.View().Content
			shellAssertFits(t, "history", frame, w, h)
			row = lbSelectedRow(t, frame)
			if outcome := replayLabel(string(g.res.Outcome)); !strings.Contains(row, outcome) {
				t.Errorf("the highlighted game %q does not say it was a %s:\n%s", row, outcome, frame)
			}
			if !strings.Contains(row, g.opponent) {
				t.Errorf("the highlighted game %q does not say it was against %q:\n%s", row, g.opponent, frame)
			}
		})
	}
}

// TestLeaderboardKeepsItsPlaceAcrossAResize: a resize changes the frame's size
// and nothing else. A screen that rebuilt its lists would lose which
// participant and which game the player was looking at.
func TestLeaderboardKeepsItsPlaceAcrossAResize(t *testing.T) {
	d := shellTestDeps(t)
	for i, name := range []string{"Ada", "Bea", "Cy", "Dora"} {
		lbRecordWin(t, d, name, leaderboard.BotName("pro"), "", "",
			time.Date(2026, 2, 1, 9, i, 0, 0, time.UTC))
	}
	s := lbScreen(t, d, 100, 30)
	lbSelect(t, s, "Cy")
	wide := s.View().Content

	s.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	narrow := s.View().Content
	shellAssertFits(t, "standings", narrow, 24, 8)
	if narrow == wide {
		t.Fatal("the frame did not change at all, so the narrow form is not being drawn")
	}
	if s.people[s.standSel].stored != "Cy" {
		t.Errorf("narrowing moved the highlight to %q", s.people[s.standSel].stored)
	}

	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if back := s.View().Content; back != wide {
		t.Errorf("the standings did not come back as they were:\nbefore:\n%s\nafter:\n%s", wide, back)
	}

	lbHistoryOf(t, s, "Cy")
	full := s.View().Content
	s.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	shellAssertFits(t, "history", s.View().Content, 24, 8)
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if back := s.View().Content; back != full {
		t.Errorf("the history did not come back as it was:\nbefore:\n%s\nafter:\n%s", full, back)
	}
}

// TestLeaderboardMovesWithTheKeymapsLetters is the difference between answering
// the keymap and hardcoding two letters that happen to match it today. It is
// also what stops this screen growing a movement convention of its own.
func TestLeaderboardMovesWithTheKeymapsLetters(t *testing.T) {
	d := shellTestDeps(t)
	km := ui.DefaultKeymap()
	for i := range km {
		switch km[i].Action {
		case ui.ActMoveUp:
			km[i].Keys, km[i].Label = []string{"y", "up"}, "y/↑"
		case ui.ActMoveDown:
			km[i].Keys, km[i].Label = []string{"z", "down"}, "z/↓"
		}
	}
	d.Keymap = km
	for i, name := range []string{"Ada", "Bea", "Cy"} {
		lbRecordWin(t, d, name, leaderboard.BotName("pro"), "", "",
			time.Date(2026, 2, 1, 9, i, 0, 0, time.UTC))
	}
	s := lbScreen(t, d, 100, 30)

	first := s.standSel
	lbSend(t, s, "z")
	if s.standSel == first {
		t.Errorf("the standings ignore %q, which this keymap binds to moving down", "z")
	}
	lbSend(t, s, "y")
	if s.standSel != first {
		t.Errorf("%q did not move back up: now on %d", "y", s.standSel)
	}
	lbSend(t, s, "j")
	if s.standSel != first {
		t.Errorf("j still moves the list although this keymap binds it to nothing: now on %d", s.standSel)
	}
	// ctrl+p and ctrl+n are the pair no text field claims, and every list in
	// the program answers them.
	lbSend(t, s, keyNext)
	if s.standSel == first {
		t.Errorf("%s did not move the standings", keyNext)
	}
	lbSend(t, s, keyPrev)
	if s.standSel != first {
		t.Errorf("%s did not move back", keyPrev)
	}

	frame := s.View().Content
	if !strings.Contains(frame, "y/z") {
		t.Errorf("the status line does not name this keymap's letters:\n%s", frame)
	}
	if strings.Contains(frame, "k/j") {
		t.Errorf("the status line still offers k/j, which do nothing here:\n%s", frame)
	}
}

// lbConfirmKey is the key a keymap chooses with, and the name the product
// gives it. Both come from the keymap rather than from this test, so a line
// that names a key can be held against the key that actually works.
func lbConfirmKey(t *testing.T, km ui.Keymap) (key, label string) {
	t.Helper()
	b, ok := km.ByAction(ui.CtxBoard, ui.ActConfirm)
	if !ok || len(b.Keys) == 0 {
		t.Fatal("this keymap binds nothing to choosing")
	}
	return b.Keys[0], keyLabel(b.Keys...)
}

// lbAssertNamesKeys requires the bottom row to name the key that chooses and
// the key that goes back. What those keys do there is left to the product to
// word; that the player is told which keys they are is not.
func lbAssertNamesKeys(t *testing.T, what, frame, choose string) {
	t.Helper()
	status := lbStatus(frame)
	if !strings.Contains(status, choose) {
		t.Errorf("%s: the status line %q does not name %q, which is what chooses here", what, status, choose)
	}
	if !strings.Contains(status, "esc") {
		t.Errorf("%s: the status line %q does not name the key that goes back", what, status)
	}
}

// lbAssertSilentAbout requires a frame not to offer a key the screen does not
// answer. The saved game's identifier is taken out of the frame first: it is
// eight random letters and may spell anything.
func lbAssertSilentAbout(t *testing.T, frame, key, id string) {
	t.Helper()
	if id != "" {
		frame = strings.ReplaceAll(frame, id, "")
	}
	if strings.Contains(frame, key) {
		t.Errorf("the screen offers %q, which this keymap binds to nothing:\n%s", key, frame)
	}
}

// TestLeaderboardNamesTheKeysThatChooseAndGoBack: what a player needs from the
// status line is which key opens what is highlighted and which key goes back,
// at every size the product supports — a pane narrow enough to cut the line
// down to two items is exactly where both were lost. Which key that is comes
// from the keymap rather than from this screen, so the line has to name the
// bound key, and the key it names has to be the one that works.
func TestLeaderboardNamesTheKeysThatChooseAndGoBack(t *testing.T) {
	seed := func(t *testing.T, d Deps) gamestore.Saved {
		t.Helper()
		sv, rec := lbSaveFinished(t, d, "Ada", "Bea", 0)
		lbRecordWin(t, d, "Ada", "Bea", sv.ID, rec.Digest, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
		return sv
	}

	t.Run("the keymap as it ships", func(t *testing.T) {
		d := shellTestDeps(t)
		seed(t, d)
		key, label := lbConfirmKey(t, shellKeymap(d))
		for _, size := range shellSizes {
			w, h := size[0], size[1]
			s := lbScreen(t, d, w, h)
			lbSelect(t, s, "Ada")
			lbAssertNamesKeys(t, fmt.Sprintf("the standings at %dx%d", w, h), s.View().Content, label)
			if cmd := lbSend(t, s, key); cmd != nil {
				t.Fatalf("at %dx%d choosing a player produced %v", w, h, cmd())
			}
			if !s.inHistory {
				t.Fatalf("at %dx%d %q did not open the games: %q", w, h, key, s.message)
			}
			lbAssertNamesKeys(t, fmt.Sprintf("the games at %dx%d", w, h), s.View().Content, label)
		}
	})

	t.Run("a keymap that chooses with space", func(t *testing.T) {
		d := shellTestDeps(t)
		km := ui.DefaultKeymap()
		for i := range km {
			if km[i].Action == ui.ActConfirm {
				km[i].Keys, km[i].Label = []string{"space"}, "space"
			}
		}
		d.Keymap = km
		sv := seed(t, d)
		key, label := lbConfirmKey(t, km)
		if key == "enter" {
			t.Fatalf("the rebound keymap still chooses with %q, so nothing here is being tested", key)
		}

		for _, size := range shellSizes {
			w, h := size[0], size[1]
			at := fmt.Sprintf("%dx%d", w, h)
			s := lbScreen(t, d, w, h)
			lbSelect(t, s, "Ada")
			lbAssertNamesKeys(t, "the standings at "+at, s.View().Content, label)
			lbAssertSilentAbout(t, s.View().Content, "enter", sv.ID)
			// The key the line no longer names does nothing at all, rather
			// than going on working and leaving the line wrong about it.
			if cmd := lbSend(t, s, "enter"); cmd != nil {
				t.Fatalf("at %s enter produced %v although this keymap binds it to nothing", at, cmd())
			}
			if s.inHistory {
				t.Fatalf("at %s enter opened the games although this keymap binds it to nothing", at)
			}
			if cmd := lbSend(t, s, key); cmd != nil {
				t.Fatalf("at %s choosing a player produced %v", at, cmd())
			}
			if !s.inHistory {
				t.Fatalf("at %s %q did not open the games: %q", at, key, s.message)
			}
			frame := s.View().Content
			lbAssertNamesKeys(t, "the games at "+at, frame, label)
			lbAssertSilentAbout(t, frame, "enter", sv.ID)
		}

		// And where there is room to say which game the highlighted row would
		// open, the key it offers is the bound one — and it is the key that
		// opens it.
		s := lbScreen(t, d, 100, 30)
		lbSelect(t, s, "Ada")
		lbSend(t, s, key)
		frame := s.View().Content
		if _, detail := lbRow(t, frame, sv.ID); !strings.Contains(detail, label) {
			t.Errorf("the row that can be replayed says %q, which does not name %q", detail, label)
		}
		if cmd := lbSend(t, s, "enter"); cmd != nil {
			t.Fatalf("enter replayed the game although this keymap binds it to nothing: %v", cmd())
		}
		cmd := lbSend(t, s, key)
		if cmd == nil {
			t.Fatalf("%q did not replay the game: %q", key, s.message)
		}
		open, ok := cmd().(OpenMsg)
		if !ok {
			t.Fatalf("opening produced %T, want an OpenMsg", cmd())
		}
		if _, ok := open.Screen.(*ReplayScreen); !ok {
			t.Fatalf("opened %T, want the replay screen", open.Screen)
		}
	})
}

// TestLeaderboardEmptyStates: a board with nothing on it, and a participant the
// log holds no games for, both say so rather than showing an empty list.
func TestLeaderboardEmptyStates(t *testing.T) {
	d := shellTestDeps(t)
	frame := mnLeaderboardFrame(t, d)
	if !strings.Contains(frame, "No games recorded yet") {
		t.Errorf("an empty board does not say it is empty:\n%s", frame)
	}
	s := lbScreen(t, d, 100, 30)
	if cmd := lbSend(t, s, "enter"); cmd != nil {
		t.Fatalf("enter on an empty board produced %v", cmd())
	}
	if s.inHistory {
		t.Error("enter on an empty board opened a history of nobody")
	}
	// Escape still leaves, which is the only thing there is to do here.
	cmd := lbSend(t, s, "esc")
	if cmd == nil {
		t.Fatal("escape produced no command")
	}
	if _, ok := cmd().(DoneMsg); !ok {
		t.Fatalf("escape produced %T, want a DoneMsg", cmd())
	}
}

// TestLeaderboardRunsInTheProgramLoop drives the real Bubble Tea runtime, which
// is what executes the commands these screens return: the menu to the standings
// to one player's games to the game itself, and back out again. The tests above
// call Update directly, so this is the one that proves the wiring works with
// nothing hand-fed.
func TestLeaderboardRunsInTheProgramLoop(t *testing.T) {
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	// The introduction is what a first run meets first; this test is about
	// what is behind it.
	if err := d.Profiles.MarkIntroduced("Balint"); err != nil {
		t.Fatal(err)
	}
	sv, rec := lbSaveFinished(t, d, "Balint", "Reka", 0)
	lbRecordWin(t, d, "Balint", "Reka", sv.ID, rec.Digest, time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC))

	tm := teatest.NewTestModel(t, NewShell(d, NewMenu(d, "Balint")), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Leaderboard")
	}, teatest.WithDuration(5*time.Second))

	for _, o := range menuEntries() {
		if o.label == "Leaderboard" {
			break
		}
		tm.Send(shellKeyPress("down"))
	}
	tm.Send(shellKeyPress("enter"))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "rating")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(shellKeyPress("enter"))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Games — Balint")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(shellKeyPress("enter"))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "step 7 of 7")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(shellKeyPress("esc"))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Games — Balint")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(shellKeyPress("ctrl+c"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	// Reading a game is reading: the record that was replayed is byte for byte
	// the one that was stored.
	after, err := d.Games.Get(sv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Record != rec.Encode() {
		t.Error("opening a game from the standings changed its stored record")
	}
}
