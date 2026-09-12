//go:build windows

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BAKocska/twixtui/internal/winfs/winfstest"
)

// TestReadingAStoreNothingMayWriteTo covers a configuration directory this
// process may read and not write: one on read-only media, or somebody else's
// that this user was let into. Listing the profiles there is an ordinary thing
// to want, and it used to fail — not on the profiles file, which is only read,
// but on the lock file beside it, which a read opened for writing and created
// if it was missing.
//
// The write path is checked in the same test, because a read that works by
// abandoning the lock would be worth nothing if it also let a mutation through
// without one.
//
// The refusal is arranged with an access control entry rather than with chmod.
// Go's os.Chmod on Windows moves the read-only attribute and nothing else, and
// a directory carrying that attribute still accepts new files, so the unix
// version of this test would assert nothing here.
func TestReadingAStoreNothingMayWriteTo(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}

	// Remove the lock file so the read has to cope with it being absent, which
	// is the case a directory nothing may write to cannot fix for itself.
	lock := filepath.Join(dir, fileName+".lock")
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	denyWrites(t, dir)

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
