//go:build windows

package leaderboard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BAKocska/twixtui/internal/winfs/winfstest"
)

// TestReadingABoardNothingMayWriteTo covers a configuration directory this
// process may read and not write: one on read-only media, or somebody else's
// that this user was let into. Reading the standings there used to fail on the
// lock file beside the board rather than on the board itself, because a shared
// lock opened that file for writing and created it when it was missing.
//
// Recording is checked in the same test: a read that copes by doing without the
// lock must not have made writing possible without one.
//
// The refusal is arranged with an access control entry rather than with chmod.
// Go's os.Chmod on Windows moves the read-only attribute and nothing else, and
// a directory carrying that attribute still accepts new files, so the unix
// version of this test would assert nothing here.
func TestReadingABoardNothingMayWriteTo(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	if err := b.Record(result("Ada", "Ben", Win, day(1))); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(dir, fileName+".lock")
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	denyWrites(t, dir)

	readonly, err := Open(dir)
	if err != nil {
		t.Fatalf("opening a board in a directory nothing may write to: %v", err)
	}
	if got := standing(t, readonly, "Ada"); got.Played != 1 {
		t.Errorf("Ada is shown with %d games, want the one recorded", got.Played)
	}
	if got := readonly.History("Ada", 0); len(got) != 1 {
		t.Errorf("History = %+v, want the one recorded game", got)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("reading created a lock file in a directory nothing may write to")
	}

	if err := readonly.Record(result("Ada", "Ben", Loss, day(2))); err == nil {
		t.Error("a result was recorded in a directory nothing may write to")
	}
	if got := readonly.History("Ada", 0); len(got) != 1 {
		t.Errorf("the refused write left %d games in the history", len(got))
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
