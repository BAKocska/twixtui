//go:build windows

package gamestore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BAKocska/twixtui/internal/winfs/winfstest"
)

// TestReadingAStoreNothingMayWriteTo covers browsing saved games in a
// configuration directory this process may read and not write: one on read-only
// media, or somebody else's that this user was let into. Nothing on the way to a
// listing may create a file — which is why the store's directory is made on the
// first write rather than when it is opened — and a write must still fail
// rather than appear to succeed.
//
// The refusal is arranged with an access control entry rather than with chmod.
// Go's os.Chmod on Windows moves the read-only attribute and nothing else, and
// a directory carrying that attribute still accepts new files, so the unix
// version of this test would assert nothing here.
func TestReadingAStoreNothingMayWriteTo(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	id := "readonly"
	if err := s.Put(Saved{ID: id, Kind: Hotseat, Player: "Ann", Opponent: "Ben", Record: record, Finished: true}); err != nil {
		t.Fatal(err)
	}

	games := s.Dir()
	config := filepath.Dir(games)
	denyWrites(t, games)
	denyWrites(t, config)

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

// denyWrites stops this process creating or removing entries in dir for the
// rest of the test. The refusal is asserted rather than assumed: a process
// holding a privilege that overrides access control writes there whatever the
// directory says, and a test that went on would then cover nothing.
func denyWrites(t *testing.T, dir string) {
	t.Helper()
	restore, err := winfstest.DenyDirectoryWrites(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Error(err)
		}
	})
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err == nil {
		os.Remove(probe)
		t.Skipf("this process creates files in %s whatever its access control says", dir)
	}
}
