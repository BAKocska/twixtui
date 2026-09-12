//go:build windows

package profile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAWriteThatCannotReplaceTheFileKeepsThePreviousProfiles covers the
// Windows way a save fails: something else has the profiles file open in a way
// that forbids renaming anything over it. That is what a text editor, a virus
// scanner mid-scan, or an older twixtui build whose reads did not allow
// deletion leaves behind, and os.Open is exactly that handle.
//
// The write has to fail, say so, and leave the file as it was — the previous
// profiles are the ones the player still has — and it has to fail soon enough
// that nobody is left in front of a frozen interface. The control at the end is
// what proves the refusal came from the replacement and not from the store
// having given up on writing altogether.
func TestAWriteThatCannotReplaceTheFileKeepsThePreviousProfiles(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	hostile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer hostile.Close()

	started := time.Now()
	if _, err := s.Create("Bea"); err == nil {
		t.Fatal("a profile was created although the store's file could not be replaced")
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("the refused write took %v to report, which is not a wait anyone should sit through", waited)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused write changed the file it could not replace")
	}
	if list := s.List(); len(list) != 1 || list[0].Name != "Ada" {
		t.Errorf("the store lists %+v, want only the profile that was stored", list)
	}
	// Nothing is left lying about either. A temporary file that survived would
	// be swept eventually, but until then it sits in the configuration
	// directory looking like part of the store.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), fileName+".tmp-") {
			t.Errorf("the refused write left %s behind", e.Name())
		}
	}

	if err := hostile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Bea"); err != nil {
		t.Errorf("the write was still refused once the file was closed: %v", err)
	}
}
