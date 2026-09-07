//go:build unix

package leaderboard

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

// TestAnUnlockedReadNeverPinsOldContentsToTheNewFile covers a writer landing in
// the middle of the read above. That read runs without the advisory lock — a
// directory nothing may write to cannot be given a lock file — so another
// process's rename can arrive between reading the file and describing it.
//
// Describing it by statting the path afterwards paired the contents of the file
// that had just been replaced with the stamp of the one replacing it. Nothing
// reloads while the stamp it holds matches the file on disk, and that stamp
// already did, so the board went on showing the old standings for as long as it
// was open: a game recorded by another process was not late, it was gone.
func TestAnUnlockedReadNeverPinsOldContentsToTheNewFile(t *testing.T) {
	// Two versions of the file, written by a board, so the fixture is what
	// this build actually stores rather than a hand-made document.
	source := t.TempDir()
	writer := openBoard(t, source)
	if err := writer.Record(result("Ada", "Ben", Win, day(1))); err != nil {
		t.Fatal(err)
	}
	old := fileBytes(t, filepath.Join(source, fileName))
	if err := writer.Record(result("Ada", "Ben", Loss, day(2))); err != nil {
		t.Fatal(err)
	}
	fresh := fileBytes(t, filepath.Join(source, fileName))

	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	// The read this is about is the unlocked one. A lock file this process may
	// not open is what a read-only directory leaves behind, and it is the case
	// where a reader has nothing but the file it read to tell it which version
	// it holds; with a shared lock, a writer taking the exclusive one could
	// not have renamed anything mid-read. So the fallback is forced rather
	// than assumed, and asserted before the read that relies on it.
	if os.Geteuid() == 0 {
		t.Skip("root opens a lock file whatever its permissions say")
	}
	lock := path + ".lock"
	if err := os.WriteFile(lock, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lock, 0o600) })
	if f, err := openLockFile(lock, false); err != nil || f != nil {
		if f != nil {
			f.Close()
		}
		t.Fatalf("the fixture did not force an unlocked read: openLockFile gave %v, %v", f, err)
	}
	// A named pipe in place of the board's file is what makes the ordering
	// deterministic rather than a race the test hopes to lose. Opening a pipe
	// for writing waits for a reader, so the writer below knows the board has
	// the old file open before it renames the new one over it, and the reader
	// sees the end of the pipe only once the writer closes, which is after the
	// rename.
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this filesystem has no named pipes: %v", err)
	}
	staged := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			staged <- err
			return
		}
		defer w.Close()
		if _, err := w.Write(old); err != nil {
			staged <- err
			return
		}
		tmp := filepath.Join(dir, "replacement")
		if err := os.WriteFile(tmp, fresh, 0o600); err != nil {
			staged <- err
			return
		}
		staged <- os.Rename(tmp, path)
	}()

	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-staged; err != nil {
		t.Fatal(err)
	}

	// The snapshot is the file that was there when the read started. That much
	// is expected, and it is also what makes the rest of this test mean
	// something: a board that had read the replacement instead would pass the
	// assertion below without the interleaving ever having happened.
	if len(b.results) != 1 {
		t.Fatalf("the fixture did not deliver the old file to the board; it read %d results", len(b.results))
	}
	if b.stamp.same(statStamp(path)) {
		t.Error("the read described its contents with the stamp of the file that replaced them, so nothing will ever reload")
	}
	if got := b.History("Ada", 0); len(got) != 2 {
		t.Errorf("the board shows %d games, want both: it is holding a file that is no longer on disk", len(got))
	}
}

// fileBytes is the contents of a file the test wrote through a board.
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
