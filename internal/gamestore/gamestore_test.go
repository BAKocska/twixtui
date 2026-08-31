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

func TestOpenRequiresADirectory(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open accepted an empty directory")
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

func TestDescribe(t *testing.T) {
	sv := Saved{Player: "Balint", Opponent: "bot:pro", Side: "vertical"}
	if got := sv.Describe(); !strings.Contains(got, "Balint") || !strings.Contains(got, "in progress") {
		t.Errorf("Describe = %q", got)
	}
	sv.Finished = true
	if got := sv.Describe(); !strings.Contains(got, "finished") {
		t.Errorf("Describe = %q", got)
	}
}
