//go:build unix

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestReadingAStoreNothingMayWriteTo covers a configuration directory this
// process may read and not write: a read-only mount, or a directory belonging
// to somebody else who let this user look at it. Listing the profiles there is
// an ordinary thing to want, and it used to fail — not on the profiles file,
// which is only read, but on the lock file beside it, which a read opened for
// writing and created if it was missing.
//
// The write path is checked in the same test, because a read that works by
// abandoning the lock would be worth nothing if it also let a mutation through
// without one.
func TestReadingAStoreNothingMayWriteTo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory whatever its permissions say")
	}
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}

	// Remove the lock file so the read has to cope with it being absent, which
	// is the case a read-only directory cannot fix for itself.
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
		t.Fatalf("opening a store in a directory nothing may write to: %v", err)
	}
	list := readonly.List()
	if len(list) != 1 || list[0].Name != "Ada" {
		t.Errorf("List = %+v, want the one stored profile", list)
	}
	if p, ok := readonly.Get("ada"); !ok || p.Name != "Ada" {
		t.Errorf("Get = %+v, %v, want the stored profile", p, ok)
	}
	if got := readonly.Search("ada"); len(got) != 1 || got[0].Profile.Name != "Ada" {
		t.Errorf("Search = %+v, want the one stored profile", got)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("reading created a lock file in a directory nothing may write to")
	}

	if _, err := readonly.Create("Bea"); err == nil {
		t.Error("a profile was created in a directory nothing may write to")
	}
	if list := readonly.List(); len(list) != 1 {
		t.Errorf("the refused write left %d profiles behind", len(list))
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
// already did, so the store went on showing the old profiles for as long as it
// was open: an update from another process was not late, it was gone.
func TestAnUnlockedReadNeverPinsOldContentsToTheNewFile(t *testing.T) {
	// Two versions of the file, written by a store, so the fixture is what
	// this build actually stores rather than a hand-made document.
	source := t.TempDir()
	writer := openStore(t, source)
	if _, err := writer.Create("Ada"); err != nil {
		t.Fatal(err)
	}
	old := fileBytes(t, filepath.Join(source, fileName))
	if _, err := writer.Create("Bea"); err != nil {
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
	// A named pipe in place of the profiles file is what makes the ordering
	// deterministic rather than a race the test hopes to lose. Opening a pipe
	// for writing waits for a reader, so the writer below knows the store has
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

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-staged; err != nil {
		t.Fatal(err)
	}

	// The snapshot is the file that was there when the read started. That much
	// is expected, and it is also what makes the rest of this test mean
	// something: a store that had read the replacement instead would pass the
	// assertion below without the interleaving ever having happened.
	if names := profileNames(s.profiles); len(names) != 1 || names[0] != "Ada" {
		t.Fatalf("the fixture did not deliver the old file to the store; it read %v", names)
	}
	if s.stamp.same(statStamp(path)) {
		t.Error("the read described its contents with the stamp of the file that replaced them, so nothing will ever reload")
	}
	if names := profileNames(s.List()); len(names) != 2 {
		t.Errorf("the store lists %v, want both profiles: it is holding a file that is no longer on disk", names)
	}
}

// fileBytes is the contents of a file the test wrote through a store.
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func profileNames(profiles []Profile) []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}
