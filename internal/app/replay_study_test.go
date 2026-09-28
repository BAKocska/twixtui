package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/study"
	"github.com/BAKocska/twixtui/internal/ui"
)

// snDeps is the shell's test collaborators with a study store beside the games,
// in the same temporary configuration directory.
func snDeps(t *testing.T) Deps {
	t.Helper()
	d := shellTestDeps(t)
	d.Study = study.Open(d.ConfigDir)
	return d
}

// snTarget names a saved game's study the way the product does: by the game's
// identifier and the digest of the record it holds.
func snTarget(t *testing.T, sv gamestore.Saved) study.Target {
	t.Helper()
	rec, err := game.DecodeRecord(sv.Record)
	if err != nil {
		t.Fatal(err)
	}
	return study.Target{
		GameID:       sv.ID,
		RecordDigest: rec.Digest,
		Entries:      rec.Entries,
		Finished:     rec.Outcome != game.Ongoing,
	}
}

// snLoad reads a study back through a store of its own, which is what another
// window, or the next launch, sees.
func snLoad(t *testing.T, d Deps, target study.Target) study.Study {
	t.Helper()
	doc, err := study.Open(d.ConfigDir).Load(target)
	if err != nil {
		t.Fatalf("loading the study back: %v", err)
	}
	return doc
}

// snFile is the study file of one game, and snBytes its contents, nil when
// there is no such file.
func snFile(d Deps, id string) string { return filepath.Join(d.Study.Dir(), id+".json") }

func snBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snType presses one key per character, and fails the moment one of them does
// anything to the review behind the note input: leaves it, moves it, or closes
// the input.
func snType(t *testing.T, s *ReplayScreen, text string) {
	t.Helper()
	step := s.step()
	for _, r := range text {
		if _, cmd := s.Update(tutorialKeyMsg(string(r))); cmd != nil {
			t.Fatalf("typing %q into the note left the screen at %q", text, r)
		}
		if s.step() != step {
			t.Fatalf("typing %q into the note moved the review from step %d to %d at %q", text, step, s.step(), r)
		}
		if !s.notes.editor.open {
			t.Fatalf("typing %q into the note closed the input at %q", text, r)
		}
	}
}

// snWriteNote does what a player does to put a note on the entry on screen:
// open the input, clear what it holds, type, confirm.
func snWriteNote(t *testing.T, s *ReplayScreen, text string) {
	t.Helper()
	s.Update(tutorialKeyMsg("e"))
	if !s.notes.editor.open {
		t.Fatalf("e did not open the note input on step %d:\n%s", s.step(), s.View().Content)
	}
	s.Update(tutorialKeyMsg("ctrl+u"))
	snType(t, s, text)
	if _, cmd := s.Update(tutorialKeyMsg("enter")); cmd != nil {
		t.Fatal("saving the note left the screen")
	}
}

// snRow is one row of the entry list once the study's column is drawn in it.
type snRow struct {
	number   int
	marked   bool
	bookmark bool
	noted    bool
	label    string
}

// snRows reads the list out of panel lines when it carries the study column:
// the marker, the number padded to digits, the two mark cells, the label. A
// list without the column has no rows under this pattern, which is how a test
// tells the column is gone.
func snRows(t *testing.T, lines []string, digits int) []snRow {
	t.Helper()
	pattern := regexp.MustCompile(`^(> |  )(\d[\d ]{` + strconv.Itoa(digits-1) + `}) ([* ])([# ]) (\S.*?)\s*$`)
	var rows []snRow
	for _, l := range lines {
		m := pattern.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(m[2]))
		if err != nil {
			t.Fatalf("entry row %q: %v", l, err)
		}
		rows = append(rows, snRow{
			number: n, marked: m[1] == "> ", bookmark: m[3] == bookmarkGlyph, noted: m[4] == noteGlyph, label: m[5],
		})
	}
	return rows
}

// snAssertMarks holds a study to exactly the marks a scenario put in it.
func snAssertMarks(t *testing.T, doc study.Study, want ...study.Mark) {
	t.Helper()
	if !slices.Equal(doc.Marks, want) {
		t.Fatalf("the study holds %+v, want %+v", doc.Marks, want)
	}
}

// TestReplayNoteStaysOnTheEntryItWasWrittenOn is the note's whole identity: it
// belongs to an entry, and in a record with draw offers in it an entry is not a
// ply. Entry 4 of the fixture is a draw offer with one move played, so a note
// filed by ply lands on entry 1, and one filed a step either side of where it
// was written turns up on 3 or 5.
func TestReplayNoteStaysOnTheEntryItWasWrittenOn(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	if err := d.Games.Put(sv); err != nil {
		t.Fatal(err)
	}
	gameFile := filepath.Join(d.Games.Dir(), sv.ID+".json")
	gameBefore := snBytes(t, gameFile)
	if gameBefore == nil {
		t.Fatal("the saved game was not written where the store keeps it")
	}

	const note = "the offer, not the ply"
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "4")
	if s.current().Ply() == 4 {
		t.Fatal("the fixture's entry 4 is also its fourth move, which separates nothing")
	}
	snWriteNote(t, s, note)
	if s.notes.editor.open {
		t.Fatalf("the note input is still open after a save:\n%s", s.View().Content)
	}
	snAssertMarks(t, snLoad(t, d, snTarget(t, sv)), study.Mark{Entry: 4, Note: note})

	// A screen opened afresh, which is what leaving and coming back is, opens
	// at the end and finds the note on entry 4 and nowhere else.
	again := rpSized(t, d, sv, 120, 30)
	if strings.Contains(again.View().Content, note) {
		t.Errorf("the note is drawn at step %d, where it was not written", again.step())
	}
	for _, c := range []struct {
		entry string
		want  bool
	}{{"3", false}, {"4", true}, {"5", false}, {"1", false}} {
		rpJump(t, again, c.entry)
		if got := strings.Contains(again.View().Content, note); got != c.want {
			t.Errorf("at entry %s the note is drawn: %v, want %v:\n%s", c.entry, got, c.want, again.View().Content)
		}
	}
	rpJump(t, again, "4")
	lines := again.panel(36, 29)
	rows := snRows(t, lines, 2)
	if len(rows) == 0 {
		t.Fatalf("the list has no column for the study's marks:\n%s", strings.Join(lines, "\n"))
	}
	for _, r := range rows {
		if r.noted != (r.number == 4) || r.bookmark {
			t.Errorf("row %d is drawn noted=%v bookmarked=%v; only entry 4 has a note, and nothing a bookmark:\n%s",
				r.number, r.noted, r.bookmark, strings.Join(lines, "\n"))
		}
	}

	if after := snBytes(t, gameFile); !bytes.Equal(after, gameBefore) {
		t.Errorf("writing a note rewrote the saved game:\nbefore: %s\nafter:  %s", gameBefore, after)
	}
}

// TestReplayBookmarkTogglesAndMarksItsRow is the other half of a mark: one key
// on and the same key off, each saved at once, and the list showing it on the
// row it was put on in this window and the next.
func TestReplayBookmarkTogglesAndMarksItsRow(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)

	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "9")
	s.Update(tutorialKeyMsg("m"))
	snAssertMarks(t, snLoad(t, d, target), study.Mark{Entry: 9, Bookmark: true})

	check := func(what string, lines []string, cursor int) {
		t.Helper()
		rows := snRows(t, lines, 2)
		found := false
		for _, r := range rows {
			if r.bookmark != (r.number == 9) || r.noted {
				t.Errorf("%s: row %d is drawn bookmarked=%v noted=%v:\n%s", what, r.number, r.bookmark, r.noted, strings.Join(lines, "\n"))
			}
			if r.marked && r.number != cursor {
				t.Errorf("%s: the review is on %d but row %d is marked", what, cursor, r.number)
			}
			found = found || r.number == 9
		}
		if !found {
			t.Fatalf("%s: row 9 is not in the list:\n%s", what, strings.Join(lines, "\n"))
		}
	}
	check("the window that marked it", s.panel(36, 29), 9)

	// A new window opens at the final entry. The bookmark stays on row 9 and
	// does not follow the cursor.
	again := rpSized(t, d, sv, 120, 30)
	check("a new window", again.panel(36, 29), 17)

	rpJump(t, again, "9")
	again.Update(tutorialKeyMsg("m"))
	doc := snLoad(t, d, target)
	snAssertMarks(t, doc)
	if doc.Revision != 2 {
		t.Errorf("two saves left the study at revision %d, want 2", doc.Revision)
	}
	// With nothing marked the column is gone, and every row is its entry
	// again.
	for _, lines := range [][]string{again.panel(36, 29), rpSized(t, d, sv, 120, 30).panel(36, 29)} {
		if rows := snRows(t, lines, 2); len(rows) != 0 {
			t.Errorf("the list keeps a column of marks with none in it:\n%s", strings.Join(lines, "\n"))
		}
		for _, r := range rpEntryRows(t, lines) {
			if strings.HasPrefix(r.label, bookmarkGlyph) {
				t.Errorf("row %d still carries a bookmark: %q", r.number, r.label)
			}
		}
	}
}

// TestReplayEscapeAbandonsANoteWithoutWriting is the input's way out: escape
// writes nothing at all — no file where there was none, and not a byte changed
// where there was one — and leaves the note that was stored on screen.
func TestReplayEscapeAbandonsANoteWithoutWriting(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "2")

	s.Update(tutorialKeyMsg("e"))
	snType(t, s, "a first draft")
	if _, cmd := s.Update(tutorialKeyMsg("esc")); cmd != nil {
		t.Fatal("escape left the review instead of the note input")
	}
	if s.notes.editor.open || s.step() != 2 {
		t.Fatalf("escape left the input open=%v at step %d", s.notes.editor.open, s.step())
	}
	if _, err := os.Stat(d.Study.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an abandoned note left something in %s: %v", d.Study.Dir(), err)
	}

	snWriteNote(t, s, "kept")
	path := snFile(d, sv.ID)
	before := snBytes(t, path)
	if before == nil {
		t.Fatal("a saved note left no study file")
	}
	s.Update(tutorialKeyMsg("e"))
	snType(t, s, " and more")
	s.Update(tutorialKeyMsg("esc"))
	if after := snBytes(t, path); !bytes.Equal(after, before) {
		t.Fatalf("escape rewrote the study:\nbefore: %s\nafter:  %s", before, after)
	}
	frame := s.View().Content
	if !strings.Contains(frame, "kept") || strings.Contains(frame, "and more") {
		t.Errorf("after escape the frame should show the stored note and not the abandoned one:\n%s", frame)
	}
	snAssertMarks(t, snLoad(t, d, snTarget(t, sv)), study.Mark{Entry: 2, Note: "kept"})
}

// TestReplayStaleSaveKeepsBothVersions is two windows on one game. The second
// saves between the first one loading and saving, and the first one's save has
// to be refused rather than laid over it: the file keeps the other window's
// note, the input keeps this one's, both are on screen, and only a second
// confirmation — made with both in view — replaces the stored one.
func TestReplayStaleSaveKeepsBothVersions(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	path := snFile(d, sv.ID)

	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "4")
	other := study.Open(d.ConfigDir)
	if _, err := other.Set(target, 0, study.Mark{Entry: 4, Note: "theirs"}); err != nil {
		t.Fatal(err)
	}
	stored := snBytes(t, path)

	s.Update(tutorialKeyMsg("e"))
	snType(t, s, "mine")
	s.Update(tutorialKeyMsg("enter"))
	if !s.notes.editor.open {
		t.Fatal("a save refused as stale closed the note input")
	}
	if got := s.notes.editor.edit.value(); got != "mine" {
		t.Fatalf("a refused save left the input holding %q, want what was typed", got)
	}
	if after := snBytes(t, path); !bytes.Equal(after, stored) {
		t.Fatalf("a stale save overwrote the other window's note:\nbefore: %s\nafter:  %s", stored, after)
	}
	frame := s.View().Content
	if !strings.Contains(frame, "mine") || !strings.Contains(frame, "theirs") {
		t.Fatalf("the refused save does not show both versions:\n%s", frame)
	}

	// Confirming again is the decision, taken with both on screen, and it is
	// made against the revision that is stored now.
	s.Update(tutorialKeyMsg("enter"))
	if s.notes.editor.open {
		t.Fatalf("the second save was refused as well:\n%s", s.View().Content)
	}
	doc := snLoad(t, d, target)
	snAssertMarks(t, doc, study.Mark{Entry: 4, Note: "mine"})
	if doc.Revision != 2 {
		t.Errorf("the study is at revision %d after two saves, want 2", doc.Revision)
	}

	// A bookmark pressed against a revision another window has moved past is
	// refused the same way, and the window then shows what is stored.
	if _, err := other.Set(target, 2, study.Mark{Entry: 9, Bookmark: true}); err != nil {
		t.Fatal(err)
	}
	stored = snBytes(t, path)
	rpJump(t, s, "9")
	before := s.View().Content
	s.Update(tutorialKeyMsg("m"))
	if after := snBytes(t, path); !bytes.Equal(after, stored) {
		t.Fatalf("a stale bookmark rewrote the study:\nbefore: %s\nafter:  %s", stored, after)
	}
	if s.View().Content == before {
		t.Error("a refused bookmark changed nothing on screen")
	}
	rows := snRows(t, s.panel(36, 29), 2)
	if i := slices.IndexFunc(rows, func(r snRow) bool { return r.number == 9 }); i < 0 || !rows[i].bookmark {
		t.Errorf("after the refusal the list does not show the bookmark that is stored on entry 9: %+v", rows)
	}
	if doc := snLoad(t, d, target); doc.Revision != 3 {
		t.Errorf("the study moved to revision %d on a refused bookmark, want 3", doc.Revision)
	}
}

// TestReplayStaleNoteIsReplacedOnlyWhenShownWhole is the refused save at the
// sizes a player may be reading it in. A second confirm is a choice between
// two versions only while both are on screen, so at each size either every
// word of the other window's note is in the frame and confirming again
// replaces it, or some of it is not and confirming again writes nothing, keeps
// what was typed and says why. The sizes are the supported ones — the smallest
// has no panel at all, the next a panel too short for the note — and two
// panels under the board narrower than the one beside it, where the note wraps
// onto more rows than anywhere else.
func TestReplayStaleNoteIsReplacedOnlyWhenShownWhole(t *testing.T) {
	const theirs = "theirs-1 theirs-2 theirs-3 theirs-4 theirs-5 theirs-6 theirs-7"
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	// unseen is the first word of the other note the frame does not show, or
	// nothing when it shows all of them.
	unseen := func(frame string) string {
		for _, word := range strings.Fields(theirs) {
			if !strings.Contains(frame, word) {
				return word
			}
		}
		return ""
	}
	var shown, hidden int
	for _, size := range append(slices.Clone(shellSizes), [2]int{24, 30}, [2]int{20, 40}) {
		w, h := size[0], size[1]
		d := snDeps(t)
		path := snFile(d, sv.ID)
		s := rpSized(t, d, sv, w, h)
		rpJump(t, s, "4")
		if _, err := study.Open(d.ConfigDir).Set(target, 0, study.Mark{Entry: 4, Note: theirs}); err != nil {
			t.Fatal(err)
		}
		stored := snBytes(t, path)

		s.Update(tutorialKeyMsg("e"))
		snType(t, s, "mine")
		s.Update(tutorialKeyMsg("enter"))
		if !s.notes.editor.open {
			t.Fatalf("at %dx%d a save refused as stale closed the note input", w, h)
		}
		frame := s.View().Content
		shellAssertFits(t, "a note refused as stale", frame, w, h)
		missing := unseen(frame)

		s.Update(tutorialKeyMsg("enter"))
		if missing == "" {
			shown++
			if s.notes.editor.open {
				t.Errorf("at %dx%d the other note is on screen whole and the second save was refused:\n%s", w, h, s.View().Content)
				continue
			}
			snAssertMarks(t, snLoad(t, d, target), study.Mark{Entry: 4, Note: "mine"})
			continue
		}
		hidden++
		if after := snBytes(t, path); !bytes.Equal(after, stored) {
			t.Errorf("at %dx%d %q of the other note is not on screen, and a second save replaced it:\n%s", w, h, missing, frame)
			continue
		}
		if !s.notes.editor.open || s.notes.editor.edit.value() != "mine" {
			t.Errorf("at %dx%d the refused save did not leave the input open holding what was typed", w, h)
			continue
		}
		again := s.View().Content
		shellAssertFits(t, "a note refused for want of room", again, w, h)
		if !strings.Contains(again, "enlarge") {
			t.Errorf("at %dx%d the save was refused without saying why:\n%s", w, h, again)
		}

		// Enlarging the terminal is the way on the refusal names: the same
		// input, given room for the other note, shows it whole and takes the
		// save.
		s.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		if missing := unseen(s.View().Content); missing != "" {
			t.Fatalf("at 120x30 %q of the other note is still not on screen:\n%s", missing, s.View().Content)
		}
		s.Update(tutorialKeyMsg("enter"))
		if s.notes.editor.open {
			t.Fatalf("enlarged from %dx%d, the save is still refused:\n%s", w, h, s.View().Content)
		}
		snAssertMarks(t, snLoad(t, d, target), study.Mark{Entry: 4, Note: "mine"})
	}
	if shown == 0 || hidden == 0 {
		t.Fatalf("the other note was on screen whole at %d sizes and not at %d, which tries one side only", shown, hidden)
	}
}

// snUnfinishedImport is an imported record without a result: one the replay
// shows and the notes refuse.
func snUnfinishedImport(t *testing.T) gamestore.Saved {
	t.Helper()
	g := game.MustNew(rpRules())
	for _, mv := range rpMoves {
		if err := g.PlayNotation(mv); err != nil {
			t.Fatal(err)
		}
	}
	if g.Result().Over() {
		t.Fatal("the fixture finished, which refuses nothing")
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return gamestore.Saved{ID: "unfinished-import", Kind: gamestore.Imported, Player: "Vertical", Opponent: "Horizontal", Record: rec.Encode()}
}

// TestReplayUnfinishedGameRefusesNotes is the import that reaches the replay
// without a result: it can be watched, but notes are for finished games, and
// asking for one is answered on screen and leaves nothing on disk.
func TestReplayUnfinishedGameRefusesNotes(t *testing.T) {
	d := snDeps(t)
	sv := snUnfinishedImport(t)

	s := rpSized(t, d, sv, 120, 30)
	s.seek(4)
	before := s.View().Content
	s.Update(tutorialKeyMsg("m"))
	marked := s.View().Content
	if marked == before {
		t.Error("m on an unfinished game was refused in silence")
	}
	s.Update(tutorialKeyMsg("e"))
	if s.notes.editor.open {
		t.Fatal("the note input opened on a game whose notes cannot be saved")
	}
	if s.View().Content == marked {
		t.Error("e on an unfinished game was refused in silence")
	}
	if rows := snRows(t, s.panel(36, 29), 1); len(rows) != 0 {
		t.Errorf("the list of an unfinished game grew a column of marks: %+v", rows)
	}
	if _, err := os.Stat(d.Study.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused notes left something in %s: %v", d.Study.Dir(), err)
	}
}

// TestReplayChangedRecordLeavesItsNotesAlone is a study written against one
// record meeting another under the same identifier. The notes are about moves
// that are not these moves, so they are neither shown here nor moved onto this
// record, the refusal is on screen, and the file stays byte for byte what it
// was — still the notes of the record they were written for.
func TestReplayChangedRecordLeavesItsNotesAlone(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)

	// The same moves without the draw offers: a finished record of its own.
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for _, mv := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		if err := g.PlayNotation(mv); err != nil {
			t.Fatal(err)
		}
	}
	oldRec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	oldTarget := study.Target{GameID: sv.ID, RecordDigest: oldRec.Digest, Entries: oldRec.Entries, Finished: true}
	if oldTarget.RecordDigest == snTarget(t, sv).RecordDigest {
		t.Fatal("the two records digest alike, which changes nothing")
	}
	const old = "written for the other record"
	if _, err := d.Study.Set(oldTarget, 0, study.Mark{Entry: 3, Note: old, Bookmark: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Study.Load(snTarget(t, sv)); !errors.Is(err, study.ErrRecordChanged) {
		t.Fatalf("the store does not refuse the study for the changed record: %v", err)
	}
	path := snFile(d, sv.ID)
	before := snBytes(t, path)

	s := rpSized(t, d, sv, 120, 30)
	frame := s.View().Content
	control := rpSized(t, snDeps(t), sv, 120, 30).View().Content
	if frame == control {
		t.Errorf("the refusal is not on screen: the frame is the one a game with no notes shows:\n%s", frame)
	}
	rpJump(t, s, "3")
	if frame := s.View().Content; strings.Contains(frame, old) {
		t.Errorf("notes for another record are shown on this one:\n%s", frame)
	}
	if rows := snRows(t, s.panel(36, 29), 2); len(rows) != 0 {
		t.Errorf("the list carries marks from another record: %+v", rows)
	}
	s.Update(tutorialKeyMsg("m"))
	s.Update(tutorialKeyMsg("e"))
	if s.notes.editor.open {
		t.Error("the note input opened over notes that belong to another record")
	}
	if after := snBytes(t, path); !bytes.Equal(after, before) {
		t.Fatalf("the study of the other record was rewritten:\nbefore: %s\nafter:  %s", before, after)
	}
	doc, err := d.Study.Load(oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	snAssertMarks(t, doc, study.Mark{Entry: 3, Note: old, Bookmark: true})
}

// TestReplayStudyRefusalsAreReadWithoutAPanel is the smallest terminal, where
// the board and the status line are all there is. Every way the notes refuse m,
// e or a save — a game with no result, a caller that keeps no notes, notes
// written for another record, by a newer twixtui or damaged, a write that
// fails, and notes another window changed first — is answered on that line
// with its reason, rather than in a panel that is not drawn.
func TestReplayStudyRefusalsAreReadWithoutAPanel(t *testing.T) {
	const w, h = 20, 8
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	studyFile := func(data string) func(*testing.T, Deps) {
		return func(t *testing.T, d Deps) {
			t.Helper()
			if err := os.MkdirAll(d.Study.Dir(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(snFile(d, sv.ID), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A directory where the lock file goes: the notes read as they should,
	// and every save of them fails before anything is written.
	unwritable := func(t *testing.T, d Deps) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(d.Study.Dir(), sv.ID+".lock"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		deps func(*testing.T) Deps
		sv   gamestore.Saved
		// before runs before the screen opens, and meanwhile once it has,
		// which is when another window gets its save in.
		before, meanwhile func(*testing.T, Deps)
		// typed is pressed first and asks nothing of the notes; each key of
		// refused is then pressed on a screen of its own.
		typed, refused []string
		// reason is a word the status line has to say why with.
		reason string
	}{
		{name: "an unfinished game", sv: snUnfinishedImport(t), refused: []string{"m", "e"}, reason: "unfinished"},
		{name: "a caller with no store", deps: shellTestDeps, refused: []string{"m", "e"}, reason: "no notes"},
		{name: "notes for another record", before: func(t *testing.T, d Deps) {
			t.Helper()
			other := target
			other.RecordDigest = "another-record"
			if _, err := d.Study.Set(other, 0, study.Mark{Entry: 3, Note: "about other moves"}); err != nil {
				t.Fatal(err)
			}
		}, refused: []string{"m", "e"}, reason: "record"},
		{name: "notes from a newer twixtui", before: studyFile(`{"version": 99}`), refused: []string{"m", "e"}, reason: "newer"},
		{name: "a damaged notes file", before: studyFile(`{`), refused: []string{"m", "e"}, reason: "damaged"},
		{name: "a bookmark that cannot be written", before: unwritable, refused: []string{"m"}, reason: "error"},
		{name: "a note that cannot be written", before: unwritable, typed: []string{"e", "x"}, refused: []string{"enter"}, reason: "error"},
		{name: "notes changed elsewhere", meanwhile: func(t *testing.T, d Deps) {
			t.Helper()
			if _, err := study.Open(d.ConfigDir).Set(target, 0, study.Mark{Entry: 3, Note: "theirs"}); err != nil {
				t.Fatal(err)
			}
		}, refused: []string{"m"}, reason: "elsewhere"},
	}
	for _, c := range cases {
		for _, key := range c.refused {
			deps, saved := snDeps, sv
			if c.deps != nil {
				deps = c.deps
			}
			if c.sv.ID != "" {
				saved = c.sv
			}
			d := deps(t)
			if c.before != nil {
				c.before(t, d)
			}
			s := rpSized(t, d, saved, w, h)
			if arr := ui.Arrange(w, h, s.current().Size()); arr.TooSmall || arr.Panel != ui.PanelNone {
				t.Fatalf("%s: a %dx%d terminal has room for a panel, which is not what this is about", c.name, w, h)
			}
			if c.meanwhile != nil {
				c.meanwhile(t, d)
			}
			for _, k := range c.typed {
				s.Update(tutorialKeyMsg(k))
			}
			before := s.View().Content
			s.Update(tutorialKeyMsg(key))
			frame := s.View().Content
			shellAssertFits(t, c.name, frame, w, h)
			status := frame[strings.LastIndexByte(frame, '\n')+1:]
			if frame == before || !strings.Contains(status, c.reason) {
				t.Errorf("%s: %s is refused without the status line saying why: %q", c.name, key, status)
			}
		}
	}
}

// TestReplayStoredNoteIsDrawnInert is the study file as untrusted text. Another
// program or a hand edit can put anything in a note, and a terminal obeys the
// control characters it is asked to draw, so none may reach the frame — not in
// the panel, and not in the input that holds the note unchanged for editing.
// What is readable around them is kept.
func TestReplayStoredNoteIsDrawnInert(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	const hostile = "\x1b]0;pwned\x07 \x1b[2Jclear\u202eflip\ttab\nline\u009bcsi"
	if _, err := d.Study.Set(snTarget(t, sv), 0, study.Mark{Entry: 17, Note: hostile}); err != nil {
		t.Fatal(err)
	}
	s := rpSized(t, d, sv, 120, 30)

	assertInert := func(what, frame string) {
		t.Helper()
		for _, r := range frame {
			if r == '\n' {
				continue
			}
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("%s: the frame carries %U, which a terminal would act on:\n%q", what, r, frame)
			}
		}
	}
	frame := s.View().Content
	assertInert("the stored note", frame)
	for _, want := range []string{"]0;pwned", "clearflip"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the readable part %q of the note is not drawn:\n%s", want, frame)
		}
	}

	s.Update(tutorialKeyMsg("e"))
	if got := s.notes.editor.edit.value(); got != hostile {
		t.Fatalf("the input changed the stored note before anything was typed: %q", got)
	}
	assertInert("the note input", s.View().Content)
	for _, size := range shellSizes {
		s.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frame := s.View().Content
		assertInert("the note input", frame)
		shellAssertFits(t, "note input", frame, size[0], size[1])
	}
}

// TestReplayNoteInputTakesTheReviewKeysAsText is the input being modal. Every
// letter the review answers is a letter of a note while the input is up — q
// does not leave, h does not step, : does not open the entry input — and the
// note saved is exactly what was typed, on the entry the input was opened on.
func TestReplayNoteInputTakesTheReviewKeysAsText(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "9")
	s.Update(tutorialKeyMsg("e"))
	const typed = "qhjklgGnpme:?$0 x Q"
	snType(t, s, typed)
	if s.jump.open {
		t.Fatal("typing : into the note opened the entry input")
	}
	if got := s.notes.editor.edit.value(); got != typed {
		t.Fatalf("the input holds %q, want %q", got, typed)
	}
	s.Update(tutorialKeyMsg("enter"))
	if s.notes.editor.open || s.step() != 9 {
		t.Fatalf("saving left the input open=%v at step %d", s.notes.editor.open, s.step())
	}
	snAssertMarks(t, snLoad(t, d, snTarget(t, sv)), study.Mark{Entry: 9, Note: typed})
}

// TestReplayNotePasteIsBoundedBeforeItIsKept is the note's size limit at the
// point text arrives. A paste is text from anywhere; one that would take the
// note past its bound is refused whole, before any of it is kept, and a byte
// that is not UTF-8 is counted as the three-byte character it becomes rather
// than as the one byte it was.
func TestReplayNotePasteIsBoundedBeforeItIsKept(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "9")
	s.Update(tutorialKeyMsg("e"))

	before := s.View().Content
	s.Update(tea.PasteMsg{Content: strings.Repeat("a", study.MaxNoteBytes+1)})
	if got := s.notes.editor.edit.value(); got != "" {
		t.Fatalf("an oversized paste left %d bytes in the input", len(got))
	}
	if s.View().Content == before {
		t.Error("an oversized paste was refused in silence")
	}

	// Line breaks and the rest of the control characters in a paste are not
	// kept as such in a one-line note.
	s.Update(tea.PasteMsg{Content: "a\x1b[31mb\nc\td"})
	if got, want := s.notes.editor.edit.value(), "a[31mb c d"; got != want {
		t.Fatalf("a paste with control characters arrived as %q, want %q", got, want)
	}

	// Filling the note to its bound exactly is allowed; one byte more is not.
	filled := study.MaxNoteBytes - len(s.notes.editor.edit.value())
	s.Update(tea.PasteMsg{Content: strings.Repeat("x", filled)})
	if got := len(s.notes.editor.edit.value()); got != study.MaxNoteBytes {
		t.Fatalf("a paste that fills the note exactly left it %d bytes, want %d", got, study.MaxNoteBytes)
	}
	s.Update(tutorialKeyMsg("y"))
	if got := len(s.notes.editor.edit.value()); got != study.MaxNoteBytes {
		t.Fatalf("a keypress took a full note to %d bytes", got)
	}
	// Two bytes of room, and one byte that becomes three once kept.
	s.Update(shellKeyPress("backspace"))
	s.Update(shellKeyPress("backspace"))
	s.Update(tea.PasteMsg{Content: "\xff"})
	if got := len(s.notes.editor.edit.value()); got != study.MaxNoteBytes-2 {
		t.Fatalf("a byte that is not UTF-8 took the note to %d bytes", got)
	}

	s.Update(tutorialKeyMsg("enter"))
	if s.notes.editor.open {
		t.Fatalf("a note inside the bound was not saved:\n%s", s.View().Content)
	}
	note := snLoad(t, d, snTarget(t, sv)).At(9).Note
	if len(note) != study.MaxNoteBytes-2 || !utf8.ValidString(note) || !strings.HasPrefix(note, "a[31mb c d") {
		t.Errorf("the saved note is %d bytes, valid=%v, starting %q", len(note), utf8.ValidString(note), note[:min(len(note), 12)])
	}
}

// TestReplayNotePasteIsMeasuredBeforeItIsCleaned is the bound applied to what
// arrived rather than to what would be kept of it. A paste longer than the
// room left is refused whole even when cleaning it would leave almost nothing:
// measuring what is kept is the second check, and a paste of control
// characters that is let through to it has already been scanned and copied in
// full, which is the cost measuring what arrived is there to refuse.
func TestReplayNotePasteIsMeasuredBeforeItIsCleaned(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	s := rpSized(t, d, sv, 120, 30)
	rpJump(t, s, "9")
	s.Update(tutorialKeyMsg("e"))
	snType(t, s, "kept")
	before := s.View().Content

	// One byte more than the room left, and all but one of them bytes the
	// field drops: cleaned, it is a single letter, well inside the bound.
	room := study.MaxNoteBytes - len("kept")
	paste := "x" + strings.Repeat("\x01", room)
	if kept := noteText(paste); len(kept) > room {
		t.Fatalf("the paste cleans to %d bytes, which the second check refuses as well", len(kept))
	}
	s.Update(tea.PasteMsg{Content: paste})
	if got := s.notes.editor.edit.value(); got != "kept" {
		t.Fatalf("a paste longer than the room left arrived, leaving %q in the input", got)
	}
	bound := strconv.Itoa(study.MaxNoteBytes>>10) + " KiB"
	if frame := s.View().Content; frame == before || !strings.Contains(frame, bound) {
		t.Errorf("the refused paste is not explained on screen:\n%s", frame)
	}
}

// TestReplayStudyKeysFollowARemappedKeymap is the study keys beside a keymap
// that has moved the review's own keys. The footer still names exactly what
// the screen answers, the rebound movement reaches the entry that is marked,
// and the note is saved by the key the keymap confirms with — the default one
// is no longer it.
func TestReplayStudyKeysFollowARemappedKeymap(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	rpAssertFooterMatchesAnswers(t, d, sv)

	d = snDeps(t)
	remapped := ui.DefaultKeymap()
	moved := map[ui.Action][]string{
		ui.ActMoveRight: {"]"},
		ui.ActMoveLeft:  {"["},
		ui.ActConfirm:   {"tab"},
	}
	for i, bind := range remapped {
		if keys, ok := moved[bind.Action]; ok {
			remapped[i].Keys = keys
			remapped[i].Label = keys[0]
		}
	}
	d.Keymap = remapped
	rpAssertFooterMatchesAnswers(t, d, sv)

	// The probe above pressed m on a screen of its own, so the scenario gets a
	// study of its own as well.
	keymap := d.Keymap
	d = snDeps(t)
	d.Keymap = keymap
	s := rpSized(t, d, sv, 120, 30)
	for range 3 {
		s.Update(tutorialKeyMsg("["))
	}
	if s.step() != 14 {
		t.Fatalf("three presses of the rebound back key reached step %d, want 14", s.step())
	}
	s.Update(tutorialKeyMsg("m"))
	s.Update(tutorialKeyMsg("e"))
	snType(t, s, "left of it")
	s.Update(tutorialKeyMsg("enter"))
	if !s.notes.editor.open {
		t.Fatal("enter saved the note after confirm was rebound away from it")
	}
	s.Update(tutorialKeyMsg("tab"))
	if s.notes.editor.open {
		t.Fatalf("the rebound confirm key did not save the note:\n%s", s.View().Content)
	}
	snAssertMarks(t, snLoad(t, d, snTarget(t, sv)), study.Mark{Entry: 14, Note: "left of it", Bookmark: true})
	s.Update(tutorialKeyMsg("l"))
	if s.step() != 14 {
		t.Errorf("l moved the review after being rebound away: step %d", s.step())
	}
}

// TestReplayStudyKeepsTheListInANarrowPanel holds the panel's promise with the
// study in it: a long note, a bookmark and the open input all fit the rows and
// columns they are given, the list keeps its share of the rows with the marks
// in their column, and the input stays on screen at every size — in the panel
// where there is one, and on the status line where there is not.
func TestReplayStudyKeepsTheListInANarrowPanel(t *testing.T) {
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	long := strings.Repeat("a long note about the ladder ", 60)
	rev := int64(0)
	for _, m := range []study.Mark{{Entry: 9, Note: long, Bookmark: true}, {Entry: 10, Bookmark: true}, {Entry: 8, Note: "short"}} {
		doc, err := d.Study.Set(target, rev, m)
		if err != nil {
			t.Fatal(err)
		}
		rev = doc.Revision
	}

	for _, editing := range []bool{false, true} {
		s := rpOpen(t, d, sv)
		s.seek(9)
		if editing {
			s.Update(tutorialKeyMsg("e"))
			if !s.notes.editor.open {
				t.Fatal("e did not open the note input")
			}
		}
		for _, height := range []int{4, 6, 10, 29} {
			for _, width := range []int{20, 24, 36} {
				lines := s.panel(width, height)
				all := strings.Join(lines, "\n")
				if len(lines) > height {
					t.Fatalf("editing=%v: a %dx%d panel produced %d lines:\n%s", editing, width, height, len(lines), all)
				}
				for _, l := range lines {
					if w := ansi.StringWidth(l); w > width {
						t.Errorf("editing=%v: a %dx%d panel has a line %d cells wide: %q", editing, width, height, w, l)
					}
				}
				rows := snRows(t, lines, 2)
				if want := min(height/2, s.entries()+1); len(rows) < want {
					t.Errorf("editing=%v: a %dx%d panel shows %d entry rows, want at least %d:\n%s",
						editing, width, height, len(rows), want, all)
				}
				marked := slices.IndexFunc(rows, func(r snRow) bool { return r.marked })
				if marked < 0 || rows[marked].number != 9 || !rows[marked].bookmark || !rows[marked].noted {
					t.Errorf("editing=%v: a %dx%d panel does not mark entry 9 with its bookmark and note:\n%s",
						editing, width, height, all)
				}
				field := slices.ContainsFunc(lines, func(l string) bool {
					return strings.HasPrefix(l, "> ") && strings.HasSuffix(l, caret)
				})
				if field != editing {
					t.Errorf("editing=%v: a %dx%d panel draws the note input: %v:\n%s", editing, width, height, field, all)
				}
			}
		}
		for _, size := range shellSizes {
			w, h := size[0], size[1]
			s.Update(tea.WindowSizeMsg{Width: w, Height: h})
			shellAssertFits(t, "replay with a study", s.View().Content, w, h)
			status := s.status(w)
			if got := ansi.StringWidth(status); got > w {
				t.Errorf("editing=%v: the status line is %d cells wide at %d columns: %q", editing, got, w, status)
			}
			if editing && !strings.Contains(status, caret) {
				t.Errorf("at %dx%d the status line lost the note input: %q", w, h, status)
			}
		}
	}
}
