package app

import (
	"testing"

	"github.com/BAKocska/twixtui/internal/profile"
)

// A profile chosen in the interface has to be recorded where the command line
// reads it. It was not, and the effect was that after picking a name and playing
// a game, "twixtui play bot" reported that nobody was playing. These tests are
// the ones that would have caught it.

// reopenProfiles opens a second store over the same directory, which is what a
// later subcommand is.
func reopenProfiles(t *testing.T, d Deps) *profile.Store {
	t.Helper()
	s, err := profile.Open(d.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPickerRecordsTheChosenProfile(t *testing.T) {
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Profiles.Create("Katalin"); err != nil {
		t.Fatal(err)
	}
	if err := d.Profiles.ClearCurrent(); err != nil {
		t.Fatal(err)
	}

	var chosen string
	p := pkPicker(t, d, &chosen)
	pkType(t, p, "balint")
	shellSend(t, p, "enter")

	if chosen != "Balint" {
		t.Fatalf("the picker handed on %q, want Balint", chosen)
	}
	got, ok := d.Profiles.Current()
	if !ok {
		t.Fatal("choosing a profile did not record it, so a later subcommand will say nobody is playing")
	}
	if got.Name != "Balint" {
		t.Errorf("recorded choice = %q, want Balint", got.Name)
	}
}

// TestPickerRecordsAProfileItCreates covers the first-run path, where the name
// does not exist yet and is created on the way through.
func TestPickerRecordsAProfileItCreates(t *testing.T) {
	d := shellTestDeps(t)
	var chosen string
	p := pkPicker(t, d, &chosen)
	pkType(t, p, "Zsofia")
	shellSend(t, p, "enter")

	if chosen != "Zsofia" {
		t.Fatalf("the picker handed on %q, want Zsofia", chosen)
	}
	if _, ok := d.Profiles.Get("Zsofia"); !ok {
		t.Fatal("the profile was not created")
	}
	got, ok := d.Profiles.Current()
	if !ok || got.Name != "Zsofia" {
		t.Errorf("a newly created profile was not recorded as the current one: %q, %v", got.Name, ok)
	}
}

// TestPickerMakesTheChoiceVisibleToASecondStore is the property that actually
// matters: the choice has to be on disk, not merely in this store's memory.
func TestPickerMakesTheChoiceVisibleToASecondStore(t *testing.T) {
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("Balint"); err != nil {
		t.Fatal(err)
	}
	var chosen string
	p := pkPicker(t, d, &chosen)
	shellSend(t, p, "enter")
	if chosen == "" {
		t.Fatal("nothing was chosen")
	}

	got, ok := reopenProfiles(t, d).Current()
	if !ok {
		t.Fatal("a second store does not see the choice the picker made")
	}
	if got.Name != chosen {
		t.Errorf("second store sees %q, the picker chose %q", got.Name, chosen)
	}
}

// TestPickerMovesTheChosenProfileToTheFront checks the most-recently-played
// order is real, since that order is what makes the list browsable for someone
// who has forgotten their name.
func TestPickerMovesTheChosenProfileToTheFront(t *testing.T) {
	d := shellTestDeps(t)
	for _, name := range []string{"Balint", "Katalin", "Zsofia"} {
		if _, err := d.Profiles.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if first := d.Profiles.List()[0].Name; first != "Zsofia" {
		t.Fatalf("expected the last created profile to lead, got %q", first)
	}

	var chosen string
	p := pkPicker(t, d, &chosen)
	pkType(t, p, "balint")
	shellSend(t, p, "enter")

	if first := d.Profiles.List()[0].Name; first != "Balint" {
		t.Errorf("choosing Balint left %q at the front of the list", first)
	}
}
