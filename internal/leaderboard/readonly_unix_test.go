//go:build unix

package leaderboard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadingABoardNothingMayWriteTo covers a configuration directory this
// process may read and not write: a read-only mount, or somebody else's
// directory this user was let into. Reading the standings there used to fail on
// the lock file beside the board rather than on the board itself, because a
// shared lock opened that file for writing and created it when it was missing.
//
// Recording is checked in the same test: a read that copes by doing without the
// lock must not have made writing possible without one.
func TestReadingABoardNothingMayWriteTo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory whatever its permissions say")
	}
	dir := t.TempDir()
	b := openBoard(t, dir)
	if err := b.Record(result("Ada", "Ben", Win, day(1))); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(dir, fileName+".lock")
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

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
