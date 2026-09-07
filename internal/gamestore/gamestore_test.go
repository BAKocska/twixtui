package gamestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// sampleRecord returns an encoded record of a short finished game.
func sampleRecord(t *testing.T) (string, *game.Game) {
	t.Helper()
	rs := game.Std
	rs.Size = 8
	g := game.MustNew(rs)
	for _, m := range []string{"D1", "A2", "E3", "A3", "D5", "A4", "E7", "A5", "C8"} {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode(), g
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutGetRoundTrip(t *testing.T) {
	s := newStore(t)
	record, g := sampleRecord(t)
	sv := Saved{
		ID:       NewID(),
		Kind:     VersusBot,
		Player:   "Balint",
		Side:     "vertical",
		Opponent: "bot:pro",
		Record:   record,
		Finished: true,
	}
	if err := s.Put(sv); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Player != sv.Player || got.Kind != sv.Kind || got.Opponent != sv.Opponent {
		t.Errorf("stored game came back changed: %+v", got)
	}
	if got.Created.IsZero() || got.Updated.IsZero() {
		t.Error("timestamps were not stamped")
	}
	replayed, err := got.Game()
	if err != nil {
		t.Fatalf("rebuilding the position: %v", err)
	}
	if replayed.Result() != g.Result() {
		t.Errorf("result = %+v, want %+v", replayed.Result(), g.Result())
	}
	if replayed.Ply() != g.Ply() {
		t.Errorf("ply = %d, want %d", replayed.Ply(), g.Ply())
	}
}

// TestPutRefusesAnUnloadableRecord checks the store will not accept a game it
// could never load again, which is the difference between a store and a
// directory of files.
func TestPutRefusesAnUnloadableRecord(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"", "not a record", "twixtui-record 1\n"} {
		err := s.Put(Saved{ID: "abcd1234", Record: bad})
		if err == nil {
			t.Errorf("Put accepted an unloadable record %q", bad)
		}
	}
}

// TestGetRefusesATamperedFile is the reason games are stored as records: a file
// edited on disk must be refused, not replayed into a different game.
func TestGetRefusesATamperedFile(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	id := "tampered"
	if err := s.Put(Saved{ID: id, Kind: Hotseat, Record: record}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Change a hole in the stored move list, leaving the digest stale.
	edited := strings.Replace(string(raw), "A3", "A6", 1)
	if edited == string(raw) {
		t.Fatal("the fixture did not contain the hole being replaced")
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	sv, err := s.Get(id)
	if err != nil {
		t.Fatalf("the file should still parse as JSON: %v", err)
	}
	if _, err := sv.Game(); err == nil {
		t.Error("a tampered record was replayed without complaint")
	}
}

func TestListOrdersByUpdatedAndSkipsRubbish(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	ids := []string{"aaaa1111", "bbbb2222", "cccc3333"}
	for _, id := range ids {
		if err := s.Put(Saved{ID: id, Kind: Hotseat, Record: record}); err != nil {
			t.Fatal(err)
		}
	}
	// A damaged file must not hide the rest.
	if err := os.WriteFile(filepath.Join(s.Dir(), "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != len(ids) {
		t.Fatalf("got %d games, want %d", len(list), len(ids))
	}
	// Most recently updated first, and the last one written is the newest.
	if list[0].ID != "cccc3333" {
		t.Errorf("first listed game = %s, want cccc3333", list[0].ID)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Updated.Before(list[i].Updated) {
			t.Error("listing is not newest first")
		}
	}
}

func TestUnfinishedAndOfKind(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	if err := s.Put(Saved{ID: "done0001", Kind: VersusBot, Record: record, Finished: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Saved{ID: "open0001", Kind: Correspondence, Record: record}); err != nil {
		t.Fatal(err)
	}
	if got := s.Unfinished(); len(got) != 1 || got[0].ID != "open0001" {
		t.Errorf("Unfinished = %+v", got)
	}
	if got := s.OfKind(Correspondence); len(got) != 1 || got[0].ID != "open0001" {
		t.Errorf("OfKind(correspondence) = %+v", got)
	}
	if got := s.OfKind(Remote); len(got) != 0 {
		t.Errorf("OfKind(remote) = %+v, want none", got)
	}
}

func TestResolvePrefix(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	for _, id := range []string{"abcd1234", "abce5678", "zzzz9999"} {
		if err := s.Put(Saved{ID: id, Kind: Hotseat, Record: record}); err != nil {
			t.Fatal(err)
		}
	}
	if sv, err := s.Resolve("zzzz"); err != nil || sv.ID != "zzzz9999" {
		t.Errorf("Resolve(zzzz) = %v, %v", sv.ID, err)
	}
	if sv, err := s.Resolve("abcd1234"); err != nil || sv.ID != "abcd1234" {
		t.Errorf("an exact identifier should resolve: %v, %v", sv.ID, err)
	}
	if _, err := s.Resolve("abc"); err == nil {
		t.Error("an ambiguous prefix should be refused")
	} else if !strings.Contains(err.Error(), "abcd1234") {
		t.Errorf("the error should list the candidates, got %v", err)
	}
	if _, err := s.Resolve("nope"); err == nil {
		t.Error("an unknown prefix should be refused")
	}
	if _, err := s.Resolve(""); err == nil {
		t.Error("an empty prefix should be refused")
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	if err := s.Put(Saved{ID: "gone0001", Kind: Hotseat, Record: record}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("gone0001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("gone0001"); err == nil {
		t.Error("the game is still there after being deleted")
	}
	if err := s.Delete("gone0001"); err == nil {
		t.Error("deleting a game twice should report that it is not there")
	}
}

// TestValidateIDRejectsPathEscapes matters because the identifier becomes a file
// name.
func TestValidateIDRejectsPathEscapes(t *testing.T) {
	for _, bad := range []string{
		"", "../escape", "a/b", `a\b`, "Upper", "has space", "sym!bol",
		strings.Repeat("a", 33), ".", "..",
	} {
		if err := ValidateID(bad); err == nil {
			t.Errorf("ValidateID(%q) should fail", bad)
		}
	}
	for _, good := range []string{"abcd1234", "a", "with-hyphen", "0123456789"} {
		if err := ValidateID(good); err != nil {
			t.Errorf("ValidateID(%q) = %v", good, err)
		}
	}

	s := newStore(t)
	record, _ := sampleRecord(t)
	if err := s.Put(Saved{ID: "../escape", Record: record}); err == nil {
		t.Error("Put accepted an identifier that escapes the directory")
	}
}

func TestNewIDIsPlausible(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		id := NewID()
		if err := ValidateID(id); err != nil {
			t.Fatalf("NewID produced %q which is invalid: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q within 500 draws", id)
		}
		seen[id] = true
	}
}

// TestOpenRequiresADirectory pins both halves of what that directory is for.
// Open does not create it — that waits for the first write — so the only thing
// it can check up front is that it was given one. Everything the store then
// does has to stay inside it: a store that quietly fell back to a relative path
// would write games into whatever directory the program happened to be started
// from, and every configuration directory on the machine would share one pile
// of games while each still looked consistent with itself.
func TestOpenRequiresADirectory(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open accepted an empty directory")
	}

	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Dir(); !strings.HasPrefix(got, dir+string(filepath.Separator)) {
		t.Errorf("the store keeps its games in %q, which is not inside %q", got, dir)
	}
	record, _ := sampleRecord(t)
	if err := s.Put(Saved{ID: "inside01", Kind: Hotseat, Record: record}); err != nil {
		t.Fatal(err)
	}
	elsewhere, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := elsewhere.List(); len(got) != 0 {
		t.Errorf("a store opened on another directory can see %d of this one's games: %+v", len(got), got)
	}
}

func TestListOnAFreshInstall(t *testing.T) {
	s := newStore(t)
	if got := s.List(); len(got) != 0 {
		t.Errorf("a fresh store should be empty, got %+v", got)
	}
	if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
		t.Error("listing a fresh store should not create its directory")
	}
}

// TestDescribe covers the one-line summary a listing prints per stored game.
// Each of the four facts on the row is on it because a player choosing a game
// out of a list needs it: with the opponent gone several rows read alike, and
// with the side gone the row cannot say which end of the board is theirs.
func TestDescribe(t *testing.T) {
	sv := Saved{Player: "Balint", Opponent: "bot:pro", Side: "vertical"}
	got := sv.Describe()
	for _, want := range []string{"Balint", "bot:pro", "vertical", "in progress"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe = %q, which does not say %q", got, want)
		}
	}

	sv.Finished = true
	if got := sv.Describe(); !strings.Contains(got, "finished") || strings.Contains(got, "in progress") {
		t.Errorf("a finished game is described as %q", got)
	}

	// The side is the one field a stored game may not have — an imported record
	// names neither seat — so it is left out rather than printed empty.
	sv.Side = ""
	if got := sv.Describe(); strings.Contains(got, "()") {
		t.Errorf("a game with no side recorded is described as %q", got)
	}
}

// TestAFinishedGameCannotBeReopened covers the last line of defence against a
// finished game being played on. A result has been recorded and rated by the
// time a game is finished, so a later write carrying the position from before
// that result would both lose the result and contradict the rating log.
func TestAFinishedGameCannotBeReopened(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	done := Saved{
		ID: NewID(), Kind: Hotseat, Player: "Ann", Side: "vertical",
		Opponent: "Ben", Record: record, Finished: true,
	}
	if err := s.Put(done); err != nil {
		t.Fatalf("storing the finished game: %v", err)
	}

	reopened := done
	reopened.Finished = false
	err := s.Put(reopened)
	if err == nil {
		t.Fatal("the store accepted an in-progress write over a finished game")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("refusal does not say why: %v", err)
	}

	// The stored game is untouched.
	back, err := s.Get(done.ID)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if !back.Finished {
		t.Error("the stored game is no longer finished")
	}

	// Correcting a finished game is still allowed: only regressing it is not.
	if err := s.Put(done); err != nil {
		t.Errorf("rewriting the finished game was refused: %v", err)
	}
}

// ongoingRecord is the encoded record of a game that has not finished, for the
// tests that need a write carrying an earlier position.
func ongoingRecord(t *testing.T) string {
	t.Helper()
	rs := game.Std
	rs.Size = 8
	g, err := game.ReplayTranscript(rs, "D1; A2")
	if err != nil {
		t.Fatal(err)
	}
	if g.Result().Over() {
		t.Fatal("the fixture game is not still being played")
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode()
}

// TestPutStoresTheCheckedRecordAndNothingElse covers what the store keeps when
// a caller hands it bytes a decoder was lenient about. The reader tolerates
// carriage returns, comments, blank lines and fields in any order, so those
// bytes reach Put; storing them would hand them back out on the next export,
// and anything a record's digests do not cover has no business surviving a
// round trip through the store.
func TestPutStoresTheCheckedRecordAndNothingElse(t *testing.T) {
	s := newStore(t)
	canonical, _ := sampleRecord(t)

	// The same record, written the long way round: fields reversed, carriage
	// returns, a comment and a trailing blank line.
	lines := strings.Split(strings.TrimRight(canonical, "\n"), "\n")
	lenient := "# exported by hand\r\n"
	for i := len(lines) - 1; i >= 0; i-- {
		lenient += lines[i] + "\r\n"
	}
	lenient += "\r\n"
	if lenient == canonical {
		t.Fatal("the lenient fixture is the canonical encoding, so it proves nothing")
	}

	id := "canon001"
	if err := s.Put(Saved{ID: id, Kind: Imported, Record: lenient, Finished: true}); err != nil {
		t.Fatalf("a record written the long way round was refused: %v", err)
	}
	back, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if back.Record != canonical {
		t.Errorf("the store kept the bytes it was handed:\n%q\nwant the canonical encoding:\n%q", back.Record, canonical)
	}

	// Two records in one string are not one game, and the store is the last
	// place that could keep them.
	if err := s.Put(Saved{ID: "canon002", Record: canonical + canonical}); err == nil {
		t.Error("the store accepted two records as one game")
	}
}

// TestEditingTheFinishedLabelDoesNotReopenAGame covers the one contradiction in
// a stored game's local labelling that loses data rather than mislabelling a
// listing. The label saying a game is over is ordinary local state anyone may
// edit; the result inside the record is covered by a digest. So the store asks
// the record, and a write carrying the position from before the result is
// refused however the label reads.
func TestEditingTheFinishedLabelDoesNotReopenAGame(t *testing.T) {
	s := newStore(t)
	finished, _ := sampleRecord(t)
	id := "relabel1"
	if err := s.Put(Saved{ID: id, Kind: Hotseat, Player: "Ann", Opponent: "Ben", Record: finished, Finished: true}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(s.Dir(), id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"finished": true`, `"finished": false`, 1)
	if edited == string(raw) {
		t.Fatal("the stored game does not carry the label being edited")
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Put(Saved{ID: id, Kind: Hotseat, Player: "Ann", Opponent: "Ben", Record: ongoingRecord(t)}); err == nil {
		t.Error("an earlier position was written over a game whose record holds a result")
	}
	back, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	g, err := back.Game()
	if err != nil {
		t.Fatal(err)
	}
	if !g.Result().Over() {
		t.Error("the recorded result was lost")
	}
}

// TestARowThatNamesAnotherGameIsRefused covers the identifier, which is the one
// local label the program uses to name a file. A row claiming an identifier
// that is not its own file's cannot be resolved or replaced, and a write from it
// would land on whichever game it names.
func TestARowThatNamesAnotherGameIsRefused(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	id := "aaaa1111"
	if err := s.Put(Saved{ID: id, Kind: Hotseat, Record: record, Finished: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"id": "`+id+`"`, `"id": "bbbb2222"`, 1)
	if edited == string(raw) {
		t.Fatal("the stored game does not carry the identifier being edited")
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(id); err == nil {
		t.Error("a row calling itself another game was handed back")
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("the listing offers a game nothing could replace: %+v", got)
	}
	if _, err := s.Resolve("aaaa"); err == nil {
		t.Error("the abbreviation resolved to a row that cannot be written back")
	}
}
