//go:build unix

package gamestore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadingAStoreNothingMayWriteTo covers browsing saved games in a
// configuration directory this process may read and not write: a read-only
// mount, or somebody else's directory this user was let into. Nothing on the
// way to a listing may create a file — which is why the store's directory is
// made on the first write rather than when it is opened — and a write must
// still fail rather than appear to succeed.
func TestReadingAStoreNothingMayWriteTo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory whatever its permissions say")
	}
	s := newStore(t)
	record, _ := sampleRecord(t)
	id := "readonly"
	if err := s.Put(Saved{ID: id, Kind: Hotseat, Player: "Ann", Opponent: "Ben", Record: record, Finished: true}); err != nil {
		t.Fatal(err)
	}

	games := s.Dir()
	config := filepath.Dir(games)
	for _, dir := range []string{games, config} {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
	}

	readonly, err := Open(config)
	if err != nil {
		t.Fatalf("opening a store in a directory nothing may write to: %v", err)
	}
	if saved := readonly.List(); len(saved) != 1 || saved[0].ID != id {
		t.Errorf("List = %+v, want the one stored game", saved)
	}
	sv, err := readonly.Resolve("read")
	if err != nil {
		t.Fatalf("resolving an abbreviation: %v", err)
	}
	if _, err := sv.Game(); err != nil {
		t.Errorf("rebuilding the position: %v", err)
	}

	if err := readonly.Put(sv); err == nil {
		t.Error("a game was written to a directory nothing may write to")
	}
}
