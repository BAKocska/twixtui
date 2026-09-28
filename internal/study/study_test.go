package study

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	testGame    = "k3m9x-study"
	testDigest  = "3f2a-record-one"
	otherDigest = "9c41-record-two"
	testEntries = 12
)

// finished is the target of a finished game with testEntries entries.
func finished() Target {
	return Target{GameID: testGame, RecordDigest: testDigest, Entries: testEntries, Finished: true}
}

func mustSet(t *testing.T, st *Store, tg Target, base int64, m Mark) Study {
	t.Helper()
	s, err := st.Set(tg, base, m)
	if err != nil {
		t.Fatalf("Set(%+v, %d, %+v): %v", tg, base, m, err)
	}
	return s
}

func mustLoad(t *testing.T, st *Store, tg Target) Study {
	t.Helper()
	s, err := st.Load(tg)
	if err != nil {
		t.Fatalf("Load(%+v): %v", tg, err)
	}
	return s
}

// readStored returns the bytes of a study file exactly as they are on disk.
func readStored(t *testing.T, st *Store, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(st.Dir(), id+".json"))
	if err != nil {
		t.Fatalf("reading the stored study: %v", err)
	}
	return data
}

// writeStored puts a study file on disk the way something other than Set might
// have left it.
func writeStored(t *testing.T, st *Store, id string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(st.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMissing(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists at %s (stat: %v), but nothing should have been written", what, path, err)
	}
}

func assertUnchanged(t *testing.T, st *Store, before []byte, what string) {
	t.Helper()
	if after := readStored(t, st, testGame); !bytes.Equal(after, before) {
		t.Fatalf("%s rewrote the stored study:\nbefore: %s\nafter:  %s", what, before, after)
	}
}

// storedDoc is a version 1 study of the test game at revision 3, holding the
// given JSON array of marks.
func storedDoc(marks string) string {
	return fmt.Sprintf("{\n  \"version\": 1,\n  \"game_id\": %q,\n  \"record_digest\": %q,\n  \"revision\": 3,\n  \"entries\": %s\n}\n",
		testGame, testDigest, marks)
}

func TestLoadingAnUnstudiedGameWritesNothing(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	st := Open(configDir)

	got := mustLoad(t, st, finished())
	if got.Version != Version || got.GameID != testGame || got.RecordDigest != testDigest || got.Revision != 0 || len(got.Marks) != 0 {
		t.Fatalf("Load of a game with no study = %+v, want an empty version %d study of %s/%s at revision 0",
			got, Version, testGame, testDigest)
	}
	assertMissing(t, configDir, "the configuration directory")

	// Once the directory exists, loading a second game's study still creates
	// neither its file nor its lock.
	mustSet(t, st, finished(), 0, Mark{Entry: 1, Bookmark: true})
	other := finished()
	other.GameID = "another-game"
	mustLoad(t, st, other)
	assertMissing(t, filepath.Join(st.Dir(), "another-game.json"), "a study file")
	assertMissing(t, filepath.Join(st.Dir(), "another-game.lock"), "a lock file")
}

func TestSavedMarkLoadsBackVerbatim(t *testing.T) {
	configDir := t.TempDir()
	st := Open(configDir)
	// Notes are text, kept exactly as typed: markup, quotes, line breaks and
	// even control characters, which only the display has to render inert.
	m := Mark{Entry: 3, Note: "Black's peg <cuts> the ladder & \"line\"\nsecond line \x1b[31m\x07 ✓", Bookmark: true}

	saved := mustSet(t, st, finished(), 0, m)
	if saved.Revision != 1 || saved.GameID != testGame || saved.RecordDigest != testDigest || saved.Version != Version {
		t.Fatalf("the first save returned %+v, want revision 1 of the version %d study of %s/%s", saved, Version, testGame, testDigest)
	}
	if got := saved.At(3); got != m {
		t.Fatalf("the saved study holds %+v on entry 3, want %+v", got, m)
	}

	// Another window opening the same configuration directory sees the save.
	loaded := mustLoad(t, Open(configDir), finished())
	if !reflect.DeepEqual(loaded, saved) {
		t.Fatalf("the study loaded back as %+v, want what the save returned, %+v", loaded, saved)
	}
	if got := loaded.At(4); got != (Mark{Entry: 4}) {
		t.Fatalf("an entry nobody marked reads %+v, want an empty mark", got)
	}
}

func TestEverySuccessfulSaveAdvancesTheRevisionOnce(t *testing.T) {
	st := Open(t.TempDir())
	var rev int64
	for i, m := range []Mark{
		{Entry: 0, Bookmark: true},
		{Entry: 0, Note: "the opening position"},
		{Entry: testEntries, Note: "the final entry", Bookmark: true},
		{Entry: 0},
	} {
		saved := mustSet(t, st, finished(), rev, m)
		if saved.Revision != rev+1 {
			t.Fatalf("save %d went from revision %d to %d, want %d", i, rev, saved.Revision, rev+1)
		}
		if loaded := mustLoad(t, st, finished()); loaded.Revision != saved.Revision {
			t.Fatalf("save %d returned revision %d but the stored study is at %d", i, saved.Revision, loaded.Revision)
		}
		rev = saved.Revision
	}
}

func TestClearingNoteAndBookmarkRemovesTheMark(t *testing.T) {
	st := Open(t.TempDir())
	s := mustSet(t, st, finished(), 0, Mark{Entry: 5, Note: "a slip", Bookmark: true})
	kept := Mark{Entry: 7, Note: "the winning link", Bookmark: true}
	s = mustSet(t, st, finished(), s.Revision, kept)

	// A save replaces the whole mark: keeping the bookmark alone drops the note.
	bare := Mark{Entry: 7, Bookmark: true}
	s = mustSet(t, st, finished(), s.Revision, bare)
	if got := s.At(7); got != bare {
		t.Fatalf("after keeping only the bookmark, entry 7 holds %+v, want %+v", got, bare)
	}

	s = mustSet(t, st, finished(), s.Revision, Mark{Entry: 5})
	if s.Revision != 4 {
		t.Fatalf("removing a mark left revision %d, want 4", s.Revision)
	}
	for _, got := range []Study{s, mustLoad(t, st, finished())} {
		if want := []Mark{bare}; !reflect.DeepEqual(got.Marks, want) {
			t.Fatalf("after clearing entry 5 the study holds %+v, want only %+v", got.Marks, want)
		}
		if m := got.At(5); m != (Mark{Entry: 5}) {
			t.Fatalf("the cleared entry reads %+v, want an empty mark", m)
		}
	}
}

func TestMarksStaySortedAndUnique(t *testing.T) {
	st := Open(t.TempDir())
	var s Study
	for _, m := range []Mark{
		{Entry: 9, Bookmark: true},
		{Entry: 2, Note: "first thought"},
		{Entry: testEntries, Note: "resigned"},
		{Entry: 0, Bookmark: true},
		{Entry: 2, Note: "second thought", Bookmark: true},
	} {
		s = mustSet(t, st, finished(), s.Revision, m)
	}
	want := []Mark{
		{Entry: 0, Bookmark: true},
		{Entry: 2, Note: "second thought", Bookmark: true},
		{Entry: 9, Bookmark: true},
		{Entry: testEntries, Note: "resigned"},
	}
	if !reflect.DeepEqual(s.Marks, want) {
		t.Fatalf("the saved study holds %+v, want %+v", s.Marks, want)
	}
	if got := mustLoad(t, st, finished()); !reflect.DeepEqual(got.Marks, want) {
		t.Fatalf("the stored study holds %+v, want %+v", got.Marks, want)
	}
}

func TestStaleSaveIsRefusedAndBothVersionsSurvive(t *testing.T) {
	configDir := t.TempDir()
	st := Open(configDir)
	// Two windows load the same study before either saves.
	first := mustLoad(t, st, finished())
	second := mustLoad(t, Open(configDir), finished())

	theirs := Mark{Entry: 4, Note: "saved by the first window"}
	saved := mustSet(t, st, finished(), first.Revision, theirs)
	before := readStored(t, st, testGame)

	mine := Mark{Entry: 4, Note: "typed in the second window", Bookmark: true}
	current, err := Open(configDir).Set(finished(), second.Revision, mine)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("saving against revision %d after revision %d was saved = %v, want ErrStale", second.Revision, saved.Revision, err)
	}
	assertUnchanged(t, st, before, "a stale save")
	if !reflect.DeepEqual(current, saved) {
		t.Fatalf("the stale refusal handed back %+v, want the stored study %+v", current, saved)
	}

	// A revision ahead of the stored one is just as wrong as one behind it.
	if _, err := st.Set(finished(), saved.Revision+5, mine); !errors.Is(err, ErrStale) {
		t.Fatalf("saving against a revision the study never reached = %v, want ErrStale", err)
	}
	assertUnchanged(t, st, before, "a save against a future revision")

	// The refusal is not a dead end: saving again against the revision it
	// reported replaces the first window's note deliberately.
	again := mustSet(t, st, finished(), current.Revision, mine)
	if again.Revision != saved.Revision+1 || again.At(4) != mine {
		t.Fatalf("saving again against the reported revision gave %+v, want revision %d holding %+v", again, saved.Revision+1, mine)
	}
}

func TestStudyOfAnotherRecordIsRefusedAndKept(t *testing.T) {
	st := Open(t.TempDir())
	original := Mark{Entry: 2, Note: "about the first record"}
	saved := mustSet(t, st, finished(), 0, original)
	before := readStored(t, st, testGame)

	changed := finished()
	changed.RecordDigest = otherDigest
	got, err := st.Load(changed)
	if !errors.Is(err, ErrRecordChanged) {
		t.Fatalf("loading the study against another record = %v, want ErrRecordChanged", err)
	}
	if !reflect.DeepEqual(got, Study{}) {
		t.Fatalf("Load handed back %+v beside ErrRecordChanged, want nothing", got)
	}

	// The save names the stored revision, so only the record check stands
	// between it and moving the note onto a different record.
	got, err = st.Set(changed, saved.Revision, Mark{Entry: 2, Note: "about the second record"})
	if !errors.Is(err, ErrRecordChanged) {
		t.Fatalf("saving against another record = %v, want ErrRecordChanged", err)
	}
	if !reflect.DeepEqual(got, Study{}) {
		t.Fatalf("Set handed back %+v beside ErrRecordChanged, want nothing", got)
	}
	assertUnchanged(t, st, before, "a save against another record")
	if m := mustLoad(t, st, finished()).At(2); m != original {
		t.Fatalf("the original record's study now holds %+v on entry 2, want %+v", m, original)
	}
}

func TestUnfinishedGameIsNeverWritten(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	st := Open(configDir)
	unfinished := finished()
	unfinished.Finished = false

	if _, err := st.Load(unfinished); err != nil {
		t.Fatalf("loading an unfinished game's study: %v", err)
	}
	if _, err := st.Set(unfinished, 0, Mark{Entry: 1, Note: "too early"}); !errors.Is(err, ErrUnfinished) {
		t.Fatalf("saving a note on an unfinished game = %v, want ErrUnfinished", err)
	}
	assertMissing(t, configDir, "the configuration directory")
}

func TestInvalidMarksAreRefused(t *testing.T) {
	// é is two bytes, so this note has fewer characters than MaxNoteBytes but
	// one byte more than it: the bound is on bytes.
	tooLong := strings.Repeat("é", MaxNoteBytes/2) + "!"
	cases := []struct {
		name string
		mark Mark
	}{
		{"before the initial position", Mark{Entry: -1, Bookmark: true}},
		{"past the last entry", Mark{Entry: testEntries + 1, Bookmark: true}},
		{"clearing past the last entry", Mark{Entry: testEntries + 1}},
		{"a note one byte too long", Mark{Entry: 1, Note: tooLong}},
		{"a note that is not UTF-8", Mark{Entry: 1, Note: "fine so far \xff\xfe"}},
	}

	configDir := filepath.Join(t.TempDir(), "config")
	st := Open(configDir)
	for _, c := range cases {
		if _, err := st.Set(finished(), 0, c.mark); !errors.Is(err, ErrInvalid) {
			t.Errorf("saving %s on a new study = %v, want ErrInvalid", c.name, err)
		}
	}
	assertMissing(t, configDir, "the configuration directory")

	saved := mustSet(t, st, finished(), 0, Mark{Entry: 0, Note: "start"})
	before := readStored(t, st, testGame)
	for _, c := range cases {
		if _, err := st.Set(finished(), saved.Revision, c.mark); !errors.Is(err, ErrInvalid) {
			t.Errorf("saving %s on an existing study = %v, want ErrInvalid", c.name, err)
		}
	}
	assertUnchanged(t, st, before, "a refused mark")
}

func TestMarksAtTheBoundsAreAccepted(t *testing.T) {
	exact := strings.Repeat("é", MaxNoteBytes/2)
	if len(exact) != MaxNoteBytes {
		t.Fatalf("the fixture note is %d bytes, want %d", len(exact), MaxNoteBytes)
	}
	st := Open(t.TempDir())
	first := Mark{Entry: 0, Note: exact}
	last := Mark{Entry: testEntries, Bookmark: true}
	s := mustSet(t, st, finished(), 0, first)
	mustSet(t, st, finished(), s.Revision, last)

	loaded := mustLoad(t, st, finished())
	if got := loaded.At(0); got != first {
		t.Fatalf("the initial position's note of exactly %d bytes came back as %d bytes", MaxNoteBytes, len(got.Note))
	}
	if got := loaded.At(testEntries); got != last {
		t.Fatalf("the last entry holds %+v, want %+v", got, last)
	}
}

func TestNewerVersionIsRefusedAndKept(t *testing.T) {
	st := Open(t.TempDir())
	// A later format may carry fields and shapes this build has never seen;
	// all it has to recognise is the version.
	data := []byte(fmt.Sprintf("{\n  \"version\": 2,\n  \"game_id\": %q,\n  \"record_digest\": %q,\n  \"revision\": 4,\n  \"entries\": {\"1\": \"x\"},\n  \"threads\": []\n}\n",
		testGame, testDigest))
	writeStored(t, st, testGame, data)

	if _, err := st.Load(finished()); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("loading a version 2 study = %v, want ErrNewerVersion", err)
	}
	if _, err := st.Set(finished(), 4, Mark{Entry: 1, Note: "x"}); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("saving over a version 2 study = %v, want ErrNewerVersion", err)
	}
	assertUnchanged(t, st, data, "a save over a newer version")
}

func TestDamagedStudyIsRefusedAndKept(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"not JSON", "{\n  \"version\": 1,\n  \"game_id\": "},
		{"no version", strings.Replace(storedDoc(`[]`), "\"version\": 1,", "", 1)},
		{"version zero", strings.Replace(storedDoc(`[]`), "\"version\": 1", "\"version\": 0", 1)},
		{"another game's study", strings.Replace(storedDoc(`[]`), testGame, "another-game", 1)},
		{"a mark before the initial position", storedDoc(`[{"entry": -1, "bookmark": true}]`)},
		{"a mark past the last entry", storedDoc(fmt.Sprintf(`[{"entry": %d, "bookmark": true}]`, testEntries+1))},
		{"marks out of order", storedDoc(`[{"entry": 4, "bookmark": true}, {"entry": 2, "bookmark": true}]`)},
		{"one entry marked twice", storedDoc(`[{"entry": 2, "bookmark": true}, {"entry": 2, "note": "again"}]`)},
		{"a mark with nothing in it", storedDoc(`[{"entry": 2}]`)},
		{"a note too long", storedDoc(fmt.Sprintf(`[{"entry": 2, "note": %q}]`, strings.Repeat("a", MaxNoteBytes+1)))},
		{"bytes that are not UTF-8", storedDoc("[{\"entry\": 2, \"note\": \"\xff\"}]")},
		{"a second document after the first", storedDoc(`[]`) + storedDoc(`[]`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := Open(t.TempDir())
			data := []byte(c.data)
			writeStored(t, st, testGame, data)

			got, err := st.Load(finished())
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("loading = %v, want ErrCorrupt", err)
			}
			if !reflect.DeepEqual(got, Study{}) {
				t.Fatalf("Load handed back %+v beside ErrCorrupt, want nothing", got)
			}
			if _, err := st.Set(finished(), 3, Mark{Entry: 1, Note: "new"}); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("saving over it = %v, want ErrCorrupt", err)
			}
			assertUnchanged(t, st, data, "a save over a damaged study")
		})
	}
}

func TestStudyFileSizeBound(t *testing.T) {
	st := Open(t.TempDir())
	doc := storedDoc(`[{"entry": 2, "note": "fits"}]`)

	// Whitespace after the document leaves one document, so this file is valid
	// and exactly as large as a study may be.
	exact := doc + strings.Repeat(" ", MaxFileBytes-len(doc))
	writeStored(t, st, testGame, []byte(exact))
	if got := mustLoad(t, st, finished()).At(2).Note; got != "fits" {
		t.Fatalf("a study of exactly MaxFileBytes loaded note %q, want %q", got, "fits")
	}

	over := []byte(exact + " ")
	writeStored(t, st, testGame, over)
	if _, err := st.Load(finished()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("loading a study one byte over MaxFileBytes = %v, want ErrCorrupt", err)
	}
	if _, err := st.Set(finished(), 3, Mark{Entry: 1, Note: "new"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("saving over a study one byte over MaxFileBytes = %v, want ErrCorrupt", err)
	}
	assertUnchanged(t, st, over, "a save over an oversize study")
}

func TestSaveThatWouldOutgrowTheFileIsRefused(t *testing.T) {
	st := Open(t.TempDir())
	tg := finished()
	tg.Entries = 400
	note := strings.Repeat("a", MaxNoteBytes)

	// Fill a study with full notes until one more would not fit.
	full := Study{Version: Version, GameID: testGame, RecordDigest: testDigest, Revision: 1}
	var data []byte
	for entry := 0; ; entry++ {
		next := full
		next.Marks = append(slices.Clone(full.Marks), Mark{Entry: entry, Note: note})
		encoded, err := encode(next)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > MaxFileBytes {
			break
		}
		full, data = next, encoded
	}
	writeStored(t, st, testGame, data)
	if got := mustLoad(t, st, tg); len(got.Marks) != len(full.Marks) {
		t.Fatalf("the full study loaded %d marks, want %d", len(got.Marks), len(full.Marks))
	}

	_, err := st.Set(tg, full.Revision, Mark{Entry: len(full.Marks), Note: note})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("saving a note the file has no room for = %v, want ErrInvalid", err)
	}
	assertUnchanged(t, st, data, "a save the file has no room for")
}

func TestStudyAtTheLastRevisionTakesNoSave(t *testing.T) {
	// storedDoc at another revision, with a note to show the study survives.
	at := func(rev int64) []byte {
		return []byte(strings.Replace(storedDoc(`[{"entry": 2, "note": "kept"}]`),
			`"revision": 3`, fmt.Sprintf(`"revision": %d`, rev), 1))
	}

	// No run of saves gets this far, but a file written by something else can,
	// and it loads: the revision is a valid one. Advancing it would wrap around
	// to a negative revision that the next Load refuses along with every note.
	st := Open(t.TempDir())
	data := at(math.MaxInt64)
	writeStored(t, st, testGame, data)
	loaded := mustLoad(t, st, finished())
	if loaded.Revision != math.MaxInt64 {
		t.Fatalf("the study loaded at revision %d, want %d", loaded.Revision, int64(math.MaxInt64))
	}
	if _, err := st.Set(finished(), loaded.Revision, Mark{Entry: 1, Note: "new"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("saving over a study at revision MaxInt64 = %v, want ErrCorrupt", err)
	}
	assertUnchanged(t, st, data, "a save over a study at the last revision")
	if got := mustLoad(t, st, finished()); got.Revision != math.MaxInt64 || got.At(2).Note != "kept" {
		t.Fatalf("after the refused save the study loads as %+v, want revision %d still holding its note", got, int64(math.MaxInt64))
	}

	// One revision short, the save is still taken, and the study it leaves at
	// the last revision loads.
	st = Open(t.TempDir())
	writeStored(t, st, testGame, at(math.MaxInt64-1))
	saved := mustSet(t, st, finished(), math.MaxInt64-1, Mark{Entry: 1, Note: "new"})
	if saved.Revision != math.MaxInt64 {
		t.Fatalf("the save one revision short of the last reached revision %d, want %d", saved.Revision, int64(math.MaxInt64))
	}
	if got := mustLoad(t, st, finished()); !reflect.DeepEqual(got, saved) {
		t.Fatalf("the study saved at the last revision loaded as %+v, want %+v", got, saved)
	}
}

func TestUnusableTargetsAreRefused(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	st := Open(configDir)

	var targets []Target
	for _, id := range []string{"", "../escaped", "..", "a/b", `a\b`, "Upper", "with.dot", "nul\x00", strings.Repeat("a", 33)} {
		tg := finished()
		tg.GameID = id
		targets = append(targets, tg)
	}
	noDigest := finished()
	noDigest.RecordDigest = ""
	negative := finished()
	negative.Entries = -1
	targets = append(targets, noDigest, negative)

	for _, tg := range targets {
		if _, err := st.Load(tg); err == nil {
			t.Errorf("Load(%+v) succeeded, want a refusal", tg)
		}
		if _, err := st.Set(tg, 0, Mark{Entry: 0, Note: "x"}); err == nil {
			t.Errorf("Set(%+v) succeeded, want a refusal", tg)
		}
	}
	assertMissing(t, configDir, "the configuration directory")
	assertMissing(t, filepath.Join(root, "escaped.json"), "a study outside the store")
}

func TestSavedStudyIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files do not carry Unix permission bits")
	}
	st := Open(t.TempDir())
	mustSet(t, st, finished(), 0, Mark{Entry: 1, Note: "only mine"})
	for _, path := range []string{st.Dir(), filepath.Join(st.Dir(), testGame+".json")} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s has mode %v, want nothing for group or others", path, perm)
		}
	}
}
