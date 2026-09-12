package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
)

// TestLeaderboardHistoryReplayFromTheMenu drives the whole of RM-06 in a real
// terminal, against the compiled program: the menu entry opens the standings,
// a player's line opens their games, and a game that still is the game it was
// recorded from opens the replay. Every step back lands on the list it was
// taken from, on the row it was taken from.
//
// The game that is opened is deliberately not the row the list opens on, so
// that a history rebuilt on the way back — which would come back on its first
// row — is a failure here rather than something this scenario cannot see.
//
// The control is the row beside it. One of the two recorded results carries no
// link — which is every row a board written before the link holds — and it must
// stay in the history, be readable, and refuse to open rather than guess which
// stored game it meant.
func TestLeaderboardHistoryReplayFromTheMenu(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	saved, record := lbSeed(t, cfg)

	tm := sessionIn(t, cfg, 120, 32)
	// A fresh profile meets the introduction first; this test is about what is
	// behind it.
	tm.MustWaitFor("skip", 20*time.Second)
	tm.SendKeys("q")
	menu := tm.MustWaitFor("Leaderboard", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of showing the menu:\n%s", menu)
	}

	// Down to the leaderboard entry with the letter the board moves by, one
	// step at a time, each one proved by the frame changing. The entry's own
	// explanation is what says the highlight has arrived.
	screen := tm.WaitSettled(10 * time.Second)
	for range 4 {
		tm.SendKeys("j")
		screen = tm.WaitChanged(screen, 10*time.Second)
	}
	if !strings.Contains(screen, "Ratings and results") {
		t.Fatalf("four steps down did not reach the leaderboard entry:\n%s", screen)
	}
	tm.AssertFits()

	// The standings: the two people ranked, and the tier they played listed
	// apart from them under a line saying it is not in the ranking.
	tm.SendKeys("Enter")
	standings := tm.MustWaitFor("Tester, on this machine", 20*time.Second)
	for _, want := range []string{"rating", "score", "Rival (remote)", "not ranked", "beginner bot"} {
		if !strings.Contains(standings, want) {
			t.Fatalf("the standings do not show %q:\n%s", want, standings)
		}
	}
	tm.AssertFits()

	// The highlighted line is Tester's, and it opens Tester's games.
	tm.SendKeys("Enter")
	history := lbWaitHistory(t, tm, "Tester")
	for _, want := range []string{"Rival (remote)", "beginner bot"} {
		if !strings.Contains(history, want) {
			t.Fatalf("the history does not show %q:\n%s", want, history)
		}
	}
	tm.AssertFits()

	// The list opens on the most recent game, which is the one with no link.
	// The linked game is the row below it, and the identifier under the list
	// is what says the highlight has arrived: no other row draws it.
	steady := tm.WaitSettled(10 * time.Second)
	tm.SendKeys("j")
	linked := tm.WaitChanged(steady, 20*time.Second)
	if !strings.Contains(linked, saved.ID) {
		t.Fatalf("the highlighted row does not name the game it would open:\n%s", linked)
	}

	// A resize while the games are on screen, down to the smallest terminal
	// the product supports and back up. Fitting is not the whole of it: what
	// the narrow frame has to keep is how the highlighted game ended and the
	// keys that open it and leave.
	small := tm.ResizeAndWait(20, 8, 10*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program died at 20x8:\n%s", small)
	}
	tm.AssertFits()
	if !strings.Contains(small, "Games") {
		t.Fatalf("at 20x8 the games are no longer on screen:\n%s", small)
	}
	if row := lbSelectedLine(t, small); !strings.Contains(row, "win") {
		t.Fatalf("at 20x8 the highlighted game %q does not say how it ended:\n%s", row, small)
	}
	if status := lbStatusLine(small); !strings.Contains(status, "enter") || !strings.Contains(status, "esc") {
		t.Fatalf("at 20x8 the status line %q does not name the keys that open and leave:\n%s", status, small)
	}
	restored := tm.ResizeAndWait(120, 32, 10*time.Second)
	if restored != linked {
		t.Fatalf("the history did not come back as it was\nbefore:\n%s\nafter:\n%s", linked, restored)
	}

	// The linked game opens for replay, at its final position.
	tm.SendKeys("Enter")
	replay := tm.MustWaitFor("step 7 of 7", 20*time.Second)
	if !strings.Contains(replay, "Tester") {
		t.Fatalf("the replay does not name the players:\n%s", replay)
	}
	tm.AssertFits()

	// Escape comes back to the games, on the row the replay was opened from:
	// the identifier is drawn under the highlighted row and no other.
	tm.SendKeys("Escape")
	lbWaitHistory(t, tm, "Tester")
	back := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(back, saved.ID) {
		t.Fatalf("leaving the replay lost the row it was opened from:\n%s", back)
	}

	// The control: the row above carries no link, and choosing it refuses
	// rather than finding a game that looks close enough.
	tm.SendKeys("k")
	legacy := tm.WaitChanged(back, 20*time.Second)
	if strings.Contains(legacy, saved.ID) {
		t.Fatalf("moving off the linked row still names its saved game:\n%s", legacy)
	}
	if !strings.Contains(legacy, "beginner bot") {
		t.Fatalf("the unlinked game is not in the history:\n%s", legacy)
	}
	tm.SendKeys("Enter")
	refused := tm.WaitChanged(legacy, 20*time.Second)
	if strings.Contains(refused, "step 7 of 7") {
		t.Fatalf("a result naming no saved game opened one anyway:\n%s", refused)
	}
	if !strings.Contains(refused, "Games") || !strings.Contains(refused, "Tester") {
		t.Fatalf("choosing an unlinked game left the history:\n%s", refused)
	}
	tm.AssertFits()

	// Out again, one list at a time: the games to the standings, the standings
	// to the menu.
	tm.SendKeys("Escape")
	tm.MustWaitFor("Tester, on this machine", 20*time.Second)
	tm.SendKeys("Escape")
	front := tm.MustWaitFor("Ratings and results", 20*time.Second)
	if !strings.Contains(front, "Play") || !strings.Contains(front, "Quit") {
		t.Fatalf("escaping the standings did not land on the front screen:\n%s", front)
	}

	tm.SendKeys("q")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("the program did not exit cleanly: exited=%v status=%d", exited, code)
	}

	// Reading a game is reading. The record the standings opened is byte for
	// byte the one that was stored, and so is the result log's link to it.
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Record != record.Encode() {
		t.Fatal("watching a game from the standings changed its stored record")
	}
	board, err := leaderboard.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rows := board.History("Tester", 0)
	if len(rows) != 2 {
		t.Fatalf("the log holds %d games for Tester, want the two that were seeded", len(rows))
	}
	// Most recent first, which is the row with no link: the linked game is
	// the one under it, and the one this scenario opened.
	if rows[0].GameID != "" || rows[0].RecordDigest != "" {
		t.Fatalf("the unlinked row gained an identity: %q/%q", rows[0].GameID, rows[0].RecordDigest)
	}
	if rows[1].GameID != saved.ID || rows[1].RecordDigest != record.Digest {
		t.Fatalf("the linked row now names %q/%q", rows[1].GameID, rows[1].RecordDigest)
	}
}

// lbWaitHistory waits for one player's games to be on screen. The title is
// matched on its parts rather than as the whole line, so that a terminal which
// draws the dash between them as something else fails on what this test is
// about rather than on a character.
func lbWaitHistory(t *testing.T, tm *Terminal, who string) string {
	t.Helper()
	screen := tm.MustWaitFor("Games", 20*time.Second)
	if !strings.Contains(screen, who) {
		t.Fatalf("the games on screen are not %s's:\n%s", who, screen)
	}
	return screen
}

// lbSelectedLine is the line the highlight is on. It is the only row the list
// says anything more about, so it is what says where the player is.
func lbSelectedLine(t *testing.T, screen string) string {
	t.Helper()
	for _, line := range strings.Split(screen, "\n") {
		if strings.HasPrefix(line, "> ") {
			return line
		}
	}
	t.Fatalf("no row is highlighted on screen:\n%s", screen)
	return ""
}

// lbStatusLine is the bottom row of the screen, which is where the keys are
// named. A capture has its trailing blank lines trimmed, so the last line of
// it is the status line the program pinned there.
func lbStatusLine(screen string) string {
	lines := strings.Split(screen, "\n")
	return lines[len(lines)-1]
}

// lbSeed writes the machine's state this scenario is about: one finished game
// with the result that was recorded from it, and one result from before saved
// games were linked at all, which is the more recent of the two.
//
// It is written through the packages the program itself writes it with, so the
// fixture cannot drift from what a real game leaves behind.
func lbSeed(t *testing.T, cfg string) (gamestore.Saved, game.Record) {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for _, move := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		if err := g.PlayNotation(move); err != nil {
			t.Fatalf("playing %s: %v", move, err)
		}
	}
	if !g.Result().Over() || g.Entries() != 7 {
		t.Fatalf("the fixture game is %d entries and over=%v, want a finished seven-entry game",
			g.Entries(), g.Result().Over())
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}

	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved := gamestore.Saved{
		ID:       gamestore.NewID(),
		Kind:     gamestore.Remote,
		Created:  time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC),
		Player:   "Tester",
		Side:     "vertical",
		Opponent: leaderboard.RemoteName("Rival"),
		Record:   record.Encode(),
		Finished: true,
	}
	if err := store.Put(saved); err != nil {
		t.Fatal(err)
	}

	board, err := leaderboard.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The linked result is the older of the two, so the games do not open on
	// it: the row has to be moved onto, and a list rebuilt on the way back
	// from the replay would come back on the other one.
	if err := board.Record(leaderboard.Result{
		Played: time.Date(2026, 2, 2, 9, 30, 0, 0, time.UTC),
		Player: "Tester", Opponent: leaderboard.RemoteName("Rival"),
		Outcome: leaderboard.Win, Side: "vertical", Moves: 7,
		Ruleset: rs.Canonical(), GameID: saved.ID, RecordDigest: record.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	// Both games are wins, so Tester is the highest-rated person on the board
	// whatever the tier's anchor is: the standings then open on a known row
	// rather than on one that depends on the rating parameters. This one is
	// also the more recent, so it is the row the games open on.
	if err := board.Record(leaderboard.Result{
		Played: time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC),
		Player: "Tester", Opponent: leaderboard.BotName("beginner"),
		Outcome: leaderboard.Win, Side: "horizontal", Moves: 12,
		Ruleset: rs.Canonical(),
	}); err != nil {
		t.Fatal(err)
	}
	return saved, record
}
