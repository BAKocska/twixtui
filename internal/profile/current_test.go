package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Which profile is playing has to be stored where both the command line and the
// interface read it. It was not, and a profile picked in the interface left a
// later subcommand reporting that nobody was playing, so these tests pin the
// behaviour rather than the implementation.

func TestCurrentIsRememberedAcrossStores(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, ok := s.Current(); ok {
		t.Fatal("a fresh store should have no current profile")
	}
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Katalin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent("Balint"); err != nil {
		t.Fatal(err)
	}

	// A second store over the same directory, which is what a later subcommand
	// is, must see the same choice.
	other := openStore(t, dir)
	got, ok := other.Current()
	if !ok {
		t.Fatal("a second store does not see the recorded choice")
	}
	if got.Name != "Balint" {
		t.Errorf("current = %q, want Balint", got.Name)
	}
}

func TestSetCurrentRefusesAnUnknownProfile(t *testing.T) {
	s := openStore(t, t.TempDir())
	err := s.SetCurrent("nobody")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetCurrent for an unknown profile = %v, want ErrNotFound", err)
	}
	if _, ok := s.Current(); ok {
		t.Error("a refused choice was recorded anyway")
	}
}

// TestCurrentIgnoresADeletedName covers the ordinary case of a profile that has
// been removed since it was chosen. That is not a fault and must not be
// reported as one.
func TestCurrentIgnoresADeletedName(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent("Balint"); err != nil {
		t.Fatal(err)
	}
	// Write a name straight into the file that no longer exists, which is what
	// an older version of the file would look like.
	path := filepath.Join(dir, currentFileName)
	if err := os.WriteFile(path, []byte("Vanished\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := openStore(t, dir).Current(); ok {
		t.Error("a name that matches no profile should report as no choice")
	}
}

func TestUseCurrentRecordsAndTouches(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Katalin"); err != nil {
		t.Fatal(err)
	}
	// Katalin was created last, so it leads the most-recently-used order.
	if first := s.List()[0].Name; first != "Katalin" {
		t.Fatalf("expected Katalin to lead, got %q", first)
	}

	p, err := s.UseCurrent("Balint")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Balint" {
		t.Errorf("UseCurrent returned %q", p.Name)
	}
	if p.LastUsed.IsZero() {
		t.Error("UseCurrent did not mark the profile as used")
	}
	if first := s.List()[0].Name; first != "Balint" {
		t.Errorf("using a profile did not move it to the front: %q leads", first)
	}
	if got, ok := s.Current(); !ok || got.Name != "Balint" {
		t.Errorf("UseCurrent did not record the choice: %q, %v", got.Name, ok)
	}
	if _, err := s.UseCurrent("nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UseCurrent for an unknown profile = %v, want ErrNotFound", err)
	}
}

// TestRenameFollowsTheCurrentProfile checks the stored choice cannot be left
// pointing at a name that no longer exists.
func TestRenameFollowsTheCurrentProfile(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Katalin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent("Balint"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("Balint", "Balazs"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Current()
	if !ok {
		t.Fatal("the choice was lost by the rename")
	}
	if got.Name != "Balazs" {
		t.Errorf("current = %q, want Balazs", got.Name)
	}

	// Renaming a profile that is not the current one leaves the choice alone.
	if err := s.Rename("Katalin", "Kata"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Current(); got.Name != "Balazs" {
		t.Errorf("an unrelated rename changed the choice to %q", got.Name)
	}
}

func TestDeleteClearsTheCurrentProfile(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Katalin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent("Balint"); err != nil {
		t.Fatal(err)
	}

	// Deleting someone else leaves the choice alone.
	if err := s.Delete("Katalin"); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Current(); !ok || got.Name != "Balint" {
		t.Errorf("an unrelated delete changed the choice: %q, %v", got.Name, ok)
	}

	if err := s.Delete("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Current(); ok {
		t.Error("deleting the current profile left the choice pointing at it")
	}
}

func TestClearCurrentIsIdempotent(t *testing.T) {
	s := openStore(t, t.TempDir())
	if err := s.ClearCurrent(); err != nil {
		t.Errorf("clearing when nothing is chosen: %v", err)
	}
	if _, err := s.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent("Balint"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearCurrent(); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearCurrent(); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
	if _, ok := s.Current(); ok {
		t.Error("the choice survived being cleared")
	}
}
