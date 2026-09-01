package profile

import (
	"os"
	"strings"
	"testing"
)

// The introduction flag is a field on the profile rather than a file beside the
// store, which buys three properties for nothing: it follows a rename, it goes
// with a deletion, and it is written through the same locked read-modify-write
// as everything else. These tests hold all three, because each of them is a
// reason the sidecar file was not written and would be quietly lost if the flag
// were ever moved out again.

func introTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestAProfileStartsNotIntroduced(t *testing.T) {
	s, _ := introTestStore(t)
	p, ok := s.Get("Ada")
	if !ok {
		t.Fatal("the profile that was just created is not there")
	}
	if p.Introduced {
		t.Error("a new profile is already marked as introduced")
	}
}

func TestMarkIntroducedSurvivesReopening(t *testing.T) {
	s, dir := introTestStore(t)
	if err := s.MarkIntroduced("Ada"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reopened.Get("Ada")
	if !ok {
		t.Fatal("Ada is gone after reopening")
	}
	if !p.Introduced {
		t.Error("the flag did not survive reopening the store")
	}
}

// TestMarkIntroducedTakesTheNameLoosely holds the same rule the rest of the
// store uses: somebody who typed "ada" is the player called "Ada", and a flag
// that disagreed with that would show the introduction again to a player who
// capitalised their own name differently.
func TestMarkIntroducedTakesTheNameLoosely(t *testing.T) {
	s, _ := introTestStore(t)
	if err := s.MarkIntroduced("  ADA  "); err != nil {
		t.Fatalf("marking %q: %v", "  ADA  ", err)
	}
	p, _ := s.Get("Ada")
	if !p.Introduced {
		t.Error("marking a differently spelled form of the name did not reach the profile")
	}
}

func TestMarkIntroducedRefusesANameThatIsNotThere(t *testing.T) {
	s, _ := introTestStore(t)
	if err := s.MarkIntroduced("Grace"); err == nil {
		t.Error("marking a profile that does not exist succeeded")
	}
}

// TestMarkIntroducedTouchesOneProfileOnly is the case the whole per-profile
// decision rests on: two people sharing a machine are two players, and the
// first one through the introduction must not consume it for the second.
func TestMarkIntroducedTouchesOneProfileOnly(t *testing.T) {
	s, _ := introTestStore(t)
	if _, err := s.Create("Grace"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkIntroduced("Ada"); err != nil {
		t.Fatal(err)
	}
	grace, _ := s.Get("Grace")
	if grace.Introduced {
		t.Error("marking Ada marked Grace as well, so a shared machine shows the introduction to one player only")
	}
	ada, _ := s.Get("Ada")
	if !ada.Introduced {
		t.Error("Ada is not marked")
	}
}

// TestTheFlagFollowsARename is one of the properties being a field buys. A
// player correcting the spelling of their own name is not a newcomer.
func TestTheFlagFollowsARename(t *testing.T) {
	s, _ := introTestStore(t)
	if err := s.MarkIntroduced("Ada"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("Ada", "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	p, ok := s.Get("Ada Lovelace")
	if !ok {
		t.Fatal("the renamed profile is not there")
	}
	if !p.Introduced {
		t.Error("renaming a profile lost the flag, so the player is offered the introduction again")
	}
}

// TestTheFlagGoesWithADeletedProfile is the other one, and it is the behaviour
// wanted rather than a side effect tolerated: a name deleted and created again
// is somebody starting over.
func TestTheFlagGoesWithADeletedProfile(t *testing.T) {
	s, _ := introTestStore(t)
	if err := s.MarkIntroduced("Ada"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("Ada")
	if p.Introduced {
		t.Error("a profile created after the old one was deleted inherited its flag")
	}
}

// TestTheStoreSchemaVersionDidNotMoveForTheFlag pins the compatibility decision
// the field was added under. Bumping the version would make an older binary
// refuse the whole store and show the player no profiles at all, which is a far
// worse failure than the one the guard would be preventing: an older binary
// dropping this field costs one keypress on the next launch.
func TestTheStoreSchemaVersionDidNotMoveForTheFlag(t *testing.T) {
	if storeVersion != 1 {
		t.Errorf("the store schema is at version %d; adding the introduction flag was not a reason to move it", storeVersion)
	}
	s, dir := introTestStore(t)
	if err := s.MarkIntroduced("Ada"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 1`) {
		t.Errorf("the file in %s does not declare schema 1:\n%s", dir, data)
	}
	if !strings.Contains(string(data), `"introduced": true`) {
		t.Errorf("the flag is not in the file:\n%s", data)
	}
}

// TestAnUnmarkedProfileCarriesNoFlagInTheFile keeps the file readable for
// somebody looking at it by hand: the common case writes nothing, so a
// directory of profiles that have all seen the introduction is the only one
// where the field appears at all.
func TestAnUnmarkedProfileCarriesNoFlagInTheFile(t *testing.T) {
	s, _ := introTestStore(t)
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "introduced") {
		t.Errorf("an unmarked profile writes the field out:\n%s", data)
	}
}
