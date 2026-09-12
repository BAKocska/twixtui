//go:build windows

package winfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAReplacementLandsWhileTheFileIsBeingRead is the reason this package
// exists. A file Go's os.Open has open cannot be renamed over on Windows, and
// every store here saves by renaming a new file over the old one; the readers
// that run without a lock — a listing of games, a store in a directory nothing
// may be written to — would then make a concurrent save fail for as long as
// they were reading. A reader opened through OpenRead does not, and it keeps
// the file it opened, the way a unix reader keeps reading an unlinked inode.
func TestAReplacementLandsWhileTheFileIsBeingRead(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRead(target)
	if err != nil {
		t.Fatalf("opening the file for reading: %v", err)
	}
	defer reader.Close()

	replacement := filepath.Join(dir, "profiles.json.tmp-new")
	if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(replacement, target); err != nil {
		t.Fatalf("replacing a file that is being read: %v", err)
	}

	held, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading on through the replacement: %v", err)
	}
	if string(held) != "old" {
		t.Errorf("the reader saw %q, want the contents of the file it opened", held)
	}
	fresh, err := ReadFile(target)
	if err != nil {
		t.Fatalf("reading the file after the replacement: %v", err)
	}
	if string(fresh) != "new" {
		t.Errorf("a fresh read saw %q, want the replacement", fresh)
	}
}

// TestAReplacementIsRefusedWhileDeletionIsForbidden covers the other program:
// an editor, a virus scanner, an older twixtui build, holding the destination
// open the way os.Open does. Windows will not rename anything over that, and
// the write has to fail — bounded, so nobody sits in front of a frozen
// interface — with the previous contents and the prepared replacement both
// still there.
func TestAReplacementIsRefusedWhileDeletionIsForbidden(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostile, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer hostile.Close()

	replacement := filepath.Join(dir, "profiles.json.tmp-new")
	if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = Replace(replacement, target)
	waited := time.Since(started)
	if err == nil {
		t.Fatal("a file was replaced while another handle forbade deleting it")
	}
	t.Logf("held-reader refusal: %v; elapsed %s", err, waited)
	if waited > 5*time.Second {
		t.Errorf("the refusal took %v, which is not a wait anyone should sit through", waited)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "old" {
		t.Errorf("the refused replacement left %q (%v), want the previous contents", got, err)
	}
	if _, err := os.Stat(replacement); err != nil {
		t.Errorf("the refused replacement lost the file it was going to install: %v", err)
	}
}

// TestOpenReadReportsAMissingFileTheWayOsOpenDoes matters because the stores
// read a file that does not exist yet on every first run, and they tell that
// apart from a real failure by matching os.ErrNotExist.
func TestOpenReadReportsAMissingFileTheWayOsOpenDoes(t *testing.T) {
	_, err := OpenRead(filepath.Join(t.TempDir(), "profiles.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OpenRead on a missing file = %v, want an error os.ErrNotExist matches", err)
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "profiles.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile on a missing file = %v, want an error os.ErrNotExist matches", err)
	}
}

// TestPathsWithSpacesAndNonAsciiCharacters covers the names a real
// configuration directory has: a Windows user directory holds the account name,
// which has spaces and accents in it as often as not. These calls convert the
// path themselves rather than going through the standard library, so the
// conversion is worth a test of its own.
func TestPathsWithSpacesAndNonAsciiCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Bálint Kocska's Data", "twixtui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(target, []byte(`{"név":"Bálint"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(target)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	if string(got) != `{"név":"Bálint"}` {
		t.Errorf("read %q, want what was written", got)
	}

	replacement := filepath.Join(dir, "profiles.json.tmp-új")
	if err := os.WriteFile(replacement, []byte(`{"név":"Réka"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(replacement, target); err != nil {
		t.Fatalf("replacing %s: %v", target, err)
	}
	if got, err := ReadFile(target); err != nil || string(got) != `{"név":"Réka"}` {
		t.Errorf("after the replacement the file holds %q (%v)", got, err)
	}
}

func TestLongPathsPreserveReplacementAndLocking(t *testing.T) {
	dir := t.TempDir()
	for range 4 {
		dir = filepath.Join(dir, strings.Repeat("segment", 10))
	}
	if len(dir) <= 260 {
		t.Fatal("fixture did not exceed the legacy path limit")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target, source := filepath.Join(dir, "data.json"), filepath.Join(dir, "replacement.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(filepath.Join(dir, "data.lock"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	reader, err := OpenRead(target)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := Replace(source, target); err != nil {
		t.Fatal(err)
	}
	old, err := io.ReadAll(reader)
	if err != nil || string(old) != "old" {
		t.Fatalf("existing reader lost its version: %q %v", old, err)
	}
	current, err := ReadFile(target)
	if err != nil || string(current) != "new" {
		t.Fatalf("replacement did not read back: %q %v", current, err)
	}
}
