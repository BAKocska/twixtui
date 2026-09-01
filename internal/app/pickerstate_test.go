package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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

// pkMarkedRow returns the list row the frame marks as chosen. The query line
// above the list carries the same marker, and is the one line that ends in the
// caret, so the rows are read from below the blank that separates them.
func pkMarkedRow(t *testing.T, p *Picker) string {
	t.Helper()
	frame := p.View().Content
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		if ansi.Strip(line) != "" {
			continue
		}
		for _, row := range lines[i+1:] {
			if rest, ok := strings.CutPrefix(ansi.Strip(row), "> "); ok {
				return strings.TrimSpace(rest)
			}
		}
		break
	}
	t.Fatalf("no row of the list is marked as chosen:\n%s", frame)
	return ""
}

// The menu's lists answer the letters the board moves by. This one deliberately
// does not, and the reason is the query line: a typed "j" has to be the first
// letter of a name. Both mistakes are possible and they are not equally bad — a
// letter that filters when a step was meant shows a shorter list, which
// backspace undoes, whereas a letter that steps when a name was being typed
// searches for the rest of the name and leaves no clue why the profile has
// gone. The keys that move a list with a text field are therefore the arrows
// and the emacs pair, which no field claims.
func TestPickerKeepsTheMovementLettersAsSearchCharacters(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Kata", "Vera", "Jozsef")
	p := pkPicker(t, d, nil)
	if got := pkNames(p); len(got) != 3 {
		t.Fatalf("the browsable list is %v, want the three profiles", got)
	}

	for i, key := range []string{"j", "k"} {
		pkType(t, p, key)
		want := "j"[:1]
		if i == 1 {
			want = "jk"
		}
		if got := p.edit.value(); got != want {
			t.Fatalf("after typing %q the query is %q, want %q: the letter moved the list instead of searching", key, got, want)
		}
		if frame := ansi.Strip(p.View().Content); !strings.Contains(frame, want+caret) {
			t.Errorf("the query line does not show %q as typed:\n%s", want, frame)
		}
	}
	// The query, not a movement, is what the letters changed: "jk" matches no
	// stored name, so the list is down to the offer to create it.
	for _, name := range pkNames(p) {
		if !strings.HasPrefix(name, "+") {
			t.Errorf("the list still offers the stored profile %q, so the letters did not reach the query", name)
		}
	}

	// What does move the list has to keep working while a query is being typed,
	// or the letters would have taken the only way through it.
	q := pkPicker(t, d, nil)
	pkType(t, q, "a")
	if got := pkNames(q); len(got) < 2 {
		t.Fatalf("the query \"a\" leaves %v, too few rows to move between", got)
	}
	first := pkMarkedRow(t, q)
	for _, key := range []string{"down", keyNext, "up", keyPrev} {
		before := pkMarkedRow(t, q)
		shellSend(t, q, key)
		if after := pkMarkedRow(t, q); after == before {
			t.Errorf("%s does not move the list while a query is typed: still on %q", key, before)
		}
	}
	if got := pkMarkedRow(t, q); got != first {
		t.Errorf("moving down and back up left the list on %q, want %q", got, first)
	}
}
