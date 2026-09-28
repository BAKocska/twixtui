package e2e

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// raeCandidateRow is one of the analysis block's candidate rows on the six-hole
// board the scenario reviews: a hole, then what its score is. Only these rows
// put a bound word straight after a hole name, so a match is the block and
// nothing else on screen.
var raeCandidateRow = regexp.MustCompile(`\b[A-F][1-6]\s+(?:[+-]\d+ exact|at most [+-]\d+|at least [+-]\d+|unscored)\b`)

// TestReplayAnalysisFromTheMenu drives RM-01.c in a real terminal against the
// compiled program: a finished game is opened from Watch a finished game, the
// entry input reaches the middle of it, ? reads that position with the real
// engine, and the next step puts the reading away.
//
// The machine's hint preference is off. Advice during a game is that game's
// setting; a review is a different act, asked for with its own key, and the
// preference must not reach it. Nothing is written while the analysis runs:
// the configuration directory is listed before and after, and the stored
// record is compared byte for byte at the end.
func TestReplayAnalysisFromTheMenu(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	saved, record := raeSeed(t, cfg)

	tm := sessionIn(t, cfg, 120, 50)
	tm.MustWaitFor("skip", 20*time.Second)
	tm.SendKeys("q")
	menu := tm.MustWaitFor("Watch a finished game", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of showing the menu:\n%s", menu)
	}

	// Down to the watch entry one step at a time, each step proved by the frame
	// changing; the entry's own explanation says the highlight has arrived.
	screen := tm.WaitSettled(10 * time.Second)
	for range 2 {
		tm.SendKeys("j")
		screen = tm.WaitChanged(screen, 10*time.Second)
	}
	if !strings.Contains(screen, "Step through a finished") {
		t.Fatalf("two steps down did not reach the watch entry:\n%s", screen)
	}
	tm.SendKeys("Enter")
	tm.MustWaitFor("Tester vs max bot", 20*time.Second)
	tm.SendKeys("Enter")
	end := tm.MustWaitFor("step 7 of 7", 20*time.Second)
	if status := lbStatusLine(end); !strings.Contains(status, "? analyse") {
		t.Fatalf("the replay's status line %q does not offer the analysis key:\n%s", status, end)
	}

	// The middle of the game, reached by its entry number.
	tm.SendKeys(":")
	tm.WaitChanged(end, 10*time.Second)
	tm.SendKeys("4", "Enter")
	tm.MustWaitFor("step 4 of 7", 10*time.Second)
	middle := tm.WaitSettled(10 * time.Second)
	// The control: before ? there is no reading on screen, so the one that
	// appears is the key's doing.
	if strings.Contains(middle, "engine's choice") || raeCandidateRow.MatchString(middle) {
		t.Fatalf("an analysis is on screen before one was asked for:\n%s", middle)
	}
	files := raeFiles(t, cfg)

	tm.SendKeys("?")
	tm.MustWaitFor("engine's choice", 30*time.Second)
	read := tm.WaitSettled(10 * time.Second)
	if !tm.Alive() {
		t.Fatalf("the program died while analysing:\n%s", read)
	}
	for _, want := range []string{"step 4 of 7", "placement-only", "not a proven", "completed work"} {
		if !strings.Contains(read, want) {
			t.Fatalf("the analysis does not show %q:\n%s", want, read)
		}
	}
	if rows := raeCandidateRow.FindAllString(read, -1); len(rows) == 0 || len(rows) > 4 {
		t.Fatalf("the analysis lists %d candidate rows, want one to four:\n%s", len(rows), read)
	}
	tm.AssertFits()

	// The next entry is a different position, and the reading goes with the
	// one it was about.
	tm.SendKeys("l")
	tm.MustWaitFor("step 5 of 7", 10*time.Second)
	next := tm.WaitSettled(10 * time.Second)
	if strings.Contains(next, "engine's choice") || raeCandidateRow.MatchString(next) {
		t.Fatalf("the analysis of step 4 is still on screen at step 5:\n%s", next)
	}
	if after := raeFiles(t, cfg); !maps.Equal(files, after) {
		t.Fatalf("analysing a replay wrote to the configuration directory:\nbefore %v\nafter  %v", files, after)
	}

	tm.SendKeys("q")
	back := tm.MustWaitFor("Watch a finished game", 20*time.Second)
	if strings.Contains(back, "step 5 of 7") {
		t.Fatalf("q did not leave the replay:\n%s", back)
	}
	tm.SendKeys("C-c")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("the program did not exit cleanly: exited=%v status=%d", exited, code)
	}

	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Record != record.Encode() {
		t.Fatal("analysing a replay changed the stored record")
	}
}

// raeSeed stores one finished game and turns the machine's hint preference
// off. Both go through the files the program itself reads, so the fixture is
// what a real machine holds.
func raeSeed(t *testing.T, cfg string) (gamestore.Saved, game.Record) {
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
		Kind:     gamestore.VersusBot,
		Created:  time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC),
		Player:   "Tester",
		Side:     "vertical",
		Opponent: "bot:max",
		Record:   record.Encode(),
		Finished: true,
	}
	if err := store.Put(saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "defaults.json"), []byte("{\"hints\": false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return saved, record
}

// raeFiles lists everything under dir with its size and modification time, so
// two listings differ if anything was created, removed or written.
func raeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("dir=%t size=%d mod=%d", e.IsDir(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
