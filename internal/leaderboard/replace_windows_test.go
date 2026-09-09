//go:build windows

package leaderboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestARecordThatCannotReplaceTheFileKeepsThePreviousResults covers the Windows
// way a save fails: something else has the results file open in a way that
// forbids renaming anything over it. That is what a text editor, a virus
// scanner mid-scan, or an older twixtui build whose reads did not allow
// deletion leaves behind, and os.Open is exactly that handle.
//
// Recording has to fail and say so, the history that was already there has to
// survive intact, and the refusal has to arrive soon enough that nobody is left
// in front of a frozen interface. The control at the end is what proves the
// refusal came from the replacement rather than from recording having stopped
// working altogether.
func TestARecordThatCannotReplaceTheFileKeepsThePreviousResults(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	if err := b.Record(result("Ada", "Ben", Win, day(1))); err != nil {
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
	if err := b.Record(result("Ada", "Ben", Loss, day(2))); err == nil {
		t.Fatal("a result was recorded although the board's file could not be replaced")
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
	if got := b.History("Ada", 0); len(got) != 1 {
		t.Errorf("the history holds %d games, want only the recorded one", len(got))
	}
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
	if err := b.Record(result("Ada", "Ben", Loss, day(2))); err != nil {
		t.Errorf("recording was still refused once the file was closed: %v", err)
	}
}
