package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/study"
)

// snNote is the note the scenario writes. Every letter in it is a key the
// replay answers — h and l step, e opens a note, q leaves — so a note input
// that let any of them through would move the review or leave it part way
// through the typing, and the scenario would fail there.
const snNote = "hold the ladder q"

// TestStudyNoteSurvivesLeavingAndResizing drives RM-01.b in a real terminal,
// against the compiled program: Watch a finished game, jump to an entry that is
// a draw offer rather than a move, write a note on it and bookmark it, leave,
// come back, and find both on that entry and not the one beside it; then resize
// the terminal around them. What is on disk afterwards is the study file with
// that one mark in it, and the saved game byte for byte as it was seeded.
func TestStudyNoteSurvivesLeavingAndResizing(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	saved, record := snSeed(t, cfg)
	gameFile := filepath.Join(cfg, "games", saved.ID+".json")
	gameBefore, err := os.ReadFile(gameFile)
	if err != nil {
		t.Fatalf("the seeded game is not where the store keeps it: %v", err)
	}

	tm := sessionIn(t, cfg, 120, 32)
	// A fresh profile meets the introduction first; this test is about what is
	// behind it.
	tm.MustWaitFor("skip", 20*time.Second)
	tm.SendKeys("q")
	tm.MustWaitFor("Watch a finished game", 20*time.Second)
	screen := tm.WaitSettled(10 * time.Second)
	for range 2 {
		tm.SendKeys("j")
		screen = tm.WaitChanged(screen, 10*time.Second)
	}
	if !strings.Contains(screen, "Step through a finished") {
		t.Fatalf("two steps down did not reach Watch a finished game:\n%s", screen)
	}
	tm.SendKeys("Enter")
	chooser := tm.MustWaitFor("Tester vs Rival", 20*time.Second)
	if strings.Contains(chooser, "step 17 of 17") {
		t.Fatalf("the watch list was skipped:\n%s", chooser)
	}
	tm.SendKeys("Enter")
	tm.MustWaitFor("step 17 of 17", 20*time.Second)
	end := tm.WaitSettled(10 * time.Second)
	tm.AssertFits()

	// Entry 4 is a draw offer with one move played, so a note filed by ply
	// would land on entry 1.
	tm.SendKeys(":")
	tm.WaitChanged(end, 10*time.Second)
	tm.SendKeys("4", "Enter")
	tm.MustWaitFor("step 4 of 17", 10*time.Second)
	at4 := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(at4, "moves played: 1") {
		t.Fatalf("the jump did not land on the draw offer:\n%s", at4)
	}

	tm.SendKeys("e")
	tm.MustWaitFor("note on entry 4", 10*time.Second)
	tm.SendText(snNote)
	typed := tm.MustWaitFor(snNote, 10*time.Second)
	if !strings.Contains(typed, "step 4 of 17") || !tm.Alive() {
		t.Fatalf("typing the note moved or left the review:\n%s", typed)
	}
	tm.SendKeys("Enter")
	noted := tm.MustWaitFor("note: "+snNote, 10*time.Second)
	if strings.Contains(noted, "note on entry 4") {
		t.Fatalf("the note input is still open after saving:\n%s", noted)
	}
	tm.SendKeys("m")
	marked := tm.MustWaitFor("bookmarked", 10*time.Second)
	row := regexp.MustCompile(`>\s*4\s+\*#\s+h:draw\?`)
	if !row.MatchString(marked) {
		t.Fatalf("the list does not mark entry 4 with its bookmark and note:\n%s", marked)
	}
	tm.AssertFits()

	// Leave the review and open the game again: it opens at the final entry,
	// where there is no note, and the list still carries the marks on row 4.
	// Leaving a screen opened from the menu drops the menu's open form, so
	// escape lands on the front list with Watch still highlighted, and the
	// list of finished games is chosen again from there.
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("Escape")
	menu := tm.MustWaitFor("> Watch a finished game", 10*time.Second)
	if strings.Contains(menu, "step 4 of 17") {
		t.Fatalf("escape did not leave the review:\n%s", menu)
	}
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("Enter")
	chooser = tm.MustWaitFor("Tester vs Rival", 20*time.Second)
	if strings.Contains(chooser, "step 17 of 17") {
		t.Fatalf("the watch list was skipped on the way back:\n%s", chooser)
	}
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("Enter")
	tm.MustWaitFor("step 17 of 17", 20*time.Second)
	reopened := tm.WaitSettled(10 * time.Second)
	if strings.Contains(reopened, snNote) {
		t.Fatalf("the note is drawn on the final entry, where it was not written:\n%s", reopened)
	}
	if !regexp.MustCompile(`\b4\s+\*#\s+h:draw\?`).MatchString(reopened) {
		t.Fatalf("the reopened list lost the marks on entry 4:\n%s", reopened)
	}

	tm.SendKeys(":")
	tm.WaitChanged(reopened, 10*time.Second)
	tm.SendKeys("4", "Enter")
	tm.MustWaitFor("step 4 of 17", 10*time.Second)
	back := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(back, "note: "+snNote) || !strings.Contains(back, "bookmarked") {
		t.Fatalf("the note and bookmark are not on entry 4 after reopening:\n%s", back)
	}
	tm.SendKeys("l")
	tm.MustWaitFor("step 5 of 17", 10*time.Second)
	next := tm.WaitSettled(10 * time.Second)
	if strings.Contains(next, snNote) || strings.Contains(next, "bookmarked") {
		t.Fatalf("entry 5 shows the note or bookmark written on entry 4:\n%s", next)
	}
	tm.SendKeys("h")
	tm.MustWaitFor("step 4 of 17", 10*time.Second)
	back = tm.WaitSettled(10 * time.Second)

	// A narrower terminal keeps a smaller panel with the note in it, the
	// smallest one keeps only the status line, and the way back to the size the
	// scenario started at comes back to the same frame.
	narrow := tm.ResizeAndWait(60, 16, 10*time.Second)
	tm.AssertFits()
	if !strings.Contains(narrow, "note: "+snNote) || !strings.Contains(narrow, "step 4 of 17") {
		t.Fatalf("at 60x16 the note on entry 4 is no longer drawn:\n%s", narrow)
	}
	small := tm.ResizeAndWait(20, 8, 10*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program died at 20x8:\n%s", small)
	}
	tm.AssertFits()
	restored := tm.ResizeAndWait(120, 32, 10*time.Second)
	if restored != back {
		t.Fatalf("the review did not come back as it was after resizing\nbefore:\n%s\nafter:\n%s", back, restored)
	}

	// Out: the review to the menu, and the menu's quit letter to the shell.
	tm.SendKeys("q")
	tm.MustWaitFor("> Watch a finished game", 10*time.Second)
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("q")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("the program did not exit cleanly: exited=%v status=%d", exited, code)
	}

	if after, err := os.ReadFile(gameFile); err != nil || !bytes.Equal(after, gameBefore) {
		t.Fatalf("taking notes changed the saved game (err %v):\nbefore: %s\nafter:  %s", err, gameBefore, after)
	}
	raw, err := os.ReadFile(filepath.Join(cfg, "study", saved.ID+".json"))
	if err != nil {
		t.Fatalf("the study was not written where it is kept: %v", err)
	}
	var onDisk study.Study
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("the study file is not JSON: %v\n%s", err, raw)
	}
	want := []study.Mark{{Entry: 4, Note: snNote, Bookmark: true}}
	if onDisk.Version != study.Version || onDisk.GameID != saved.ID || onDisk.RecordDigest != record.Digest ||
		onDisk.Revision != 2 || !slices.Equal(onDisk.Marks, want) {
		t.Fatalf("the study file holds %+v, want version %d of game %s, record %s, revision 2, marks %+v\n%s",
			onDisk, study.Version, saved.ID, record.Digest, want, raw)
	}
	// And the program reads it back as the same study, which is what the next
	// launch does.
	loaded, err := study.Open(cfg).Load(study.Target{
		GameID: saved.ID, RecordDigest: record.Digest, Entries: record.Entries, Finished: true,
	})
	if err != nil || !slices.Equal(loaded.Marks, want) {
		t.Fatalf("the study loads back as %+v (err %v), want %+v", loaded.Marks, err, want)
	}
}

// snSeed stores one finished game whose record separates entries from moves:
// two draw offers before each of the first five moves, seventeen entries and
// seven moves in all, won by a chain. It is written through the store the
// program itself writes games with, so the fixture cannot drift from what a
// real game leaves behind.
func snSeed(t *testing.T, cfg string) (gamestore.Saved, game.Record) {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for i, move := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
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
	if g.Entries() != 17 || g.Ply() != 7 || !g.Result().Over() {
		t.Fatalf("the fixture is %d entries and %d moves, over=%v: it no longer separates the two or finishes",
			g.Entries(), g.Ply(), g.Result().Over())
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
		Kind:     gamestore.Hotseat,
		Created:  time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC),
		Player:   "Tester",
		Side:     "vertical",
		Opponent: "Rival",
		Record:   record.Encode(),
		Finished: true,
	}
	if err := store.Put(saved); err != nil {
		t.Fatal(err)
	}
	return saved, record
}
