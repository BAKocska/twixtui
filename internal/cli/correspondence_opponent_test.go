package cli

import (
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/gamestore"
)

// The host of a correspondence game never learns who accepted the invitation:
// the invitation is open and no move code carries a name. Whatever stands in
// for that name is therefore permanent on the host's side, and it lands in a
// slot that names a person, so it has to read like one.

// TestTheHostsUnnamedOpponentIsNotAddressedToTheReader pins the wording
// property. "Ada vs your opponent (vertical)" reads as a name that went
// missing: a listing that turns round and addresses the reader where a
// player's name belongs looks like a bug in the listing, and the player goes
// looking for a name that was never there to lose.
func TestTheHostsUnnamedOpponentIsNotAddressedToTheReader(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "--profile", "ada", "play", "correspondence", "--new", "--side", "vertical")
	if err != nil {
		t.Fatalf("starting a correspondence game: %v\n%s", err, out)
	}
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(idIn(t, out))
	if err != nil {
		t.Fatalf("the game was announced but not stored: %v", err)
	}
	if saved.Opponent == "" {
		t.Fatal("the host's game leaves the opponent's place empty")
	}

	line := describeSaved(saved)
	if !strings.Contains(line, saved.Opponent) {
		t.Fatalf("the stand-in %q is not what the listing shows: %q", saved.Opponent, line)
	}
	for _, word := range strings.Fields(strings.ToLower(saved.Opponent)) {
		switch strings.Trim(word, ".,;:") {
		case "you", "your", "yours", "yourself":
			t.Errorf("the opponent's place addresses the reader rather than describing them: %q", line)
		}
	}
}
