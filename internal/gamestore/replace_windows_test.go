//go:build windows

package gamestore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestASaveThatCannotReplaceTheGameKeepsTheStoredOne covers the Windows way a
// save fails: something else has the game's file open in a way that forbids
// renaming anything over it. That is what a text editor, a virus scanner
// mid-scan, or an older twixtui build whose reads did not allow deletion leaves
// behind, and os.Open is exactly that handle.
//
// The save has to fail and say so, the stored game has to survive as it was —
// it is the game the player can still resume — and the refusal has to arrive
// soon enough that nobody is left in front of a frozen interface. The control
// at the end is what proves the refusal came from the replacement rather than
// from saving having stopped working altogether.
func TestASaveThatCannotReplaceTheGameKeepsTheStoredOne(t *testing.T) {
	s := newStore(t)
	record, _ := sampleRecord(t)
	id := "hostile1"
	stored := Saved{ID: id, Kind: Hotseat, Player: "Ann", Opponent: "Ben", Record: record, Finished: true}
	if err := s.Put(stored); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), id+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	hostile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer hostile.Close()

	relabelled := stored
	relabelled.Opponent = "Béla"
	started := time.Now()
	if err := s.Put(relabelled); err == nil {
		t.Fatal("a game was saved although its file could not be replaced")
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("the refused save took %v to report, which is not a wait anyone should sit through", waited)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused save changed the file it could not replace")
	}
	if got, err := s.Get(id); err != nil || got.Opponent != "Ben" {
		t.Errorf("the stored game reads back as %+v (%v), want the game that was stored", got, err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("the refused save left %s behind", e.Name())
		}
	}

	if err := hostile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(relabelled); err != nil {
		t.Errorf("saving was still refused once the file was closed: %v", err)
	}
}
