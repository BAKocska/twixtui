//go:build unix

package profile

import (
	"errors"
	"os"
	"path/filepath"
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
