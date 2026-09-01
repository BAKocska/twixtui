package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Correspondence play is the one mode with no second machine in it: the players
// are the transport. That makes it the mode most easily believed to work, and it
// shipped unusable. This test is the evidence that the compiled binary plays it,
// as opposed to the model underneath doing so: two configuration directories,
// two terminals, and a move each way carried by nothing but a code read off one
// screen and pasted into the other.
//
// One test, deliberately. The exhaustive coverage — refusals, closing between
// turns, a game played out — is at the model level in internal/app, where it is
// fast and precise. What only a terminal can show is that the code reaches the
// screen whole and that a bracketed paste arrives as a code.

// corrRun runs the binary non-interactively and returns what it printed.
func corrRun(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"--config", dir}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TWIXTUI_CONFIG_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("twixtui %s: %v\n%s", strings.Join(full, " "), err, out)
	}
	return string(out)
}

// corrPasteInto pastes text the way a terminal does when the player presses the
// paste key: as one bracketed block, not as a run of keystrokes. That is the
// path the interface has to handle, and send-keys would not exercise it.
func corrPasteInto(t *testing.T, tm *Terminal, text string) {
	t.Helper()
	tm.mustTmux("set-buffer", "set-buffer", "--", text)
	tm.mustTmux("paste-buffer", "paste-buffer", "-p", "-t", "main")
}

// corrCodeOnScreen returns the one line of the screen that is a move code and
// nothing else. A code the player cannot select in one gesture is not usable, so
// a line carrying anything besides the code fails here.
func corrCodeOnScreen(t *testing.T, screen string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(screen, "\n") {
		if strings.HasPrefix(line, "TWX-") {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the screen shows %d lines beginning with a code, want 1\n--- screen ---\n%s", len(found), screen)
	}
	return found[0]
}

// corrMoveOnScreen reads the move the exchange says its code carries, which is
// what the other end's board must then show as its last move.
func corrMoveOnScreen(t *testing.T, screen string) string {
	t.Helper()
	for _, line := range strings.Split(screen, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "your ")
		if !ok {
			continue
		}
		if move, _, ok := strings.Cut(rest, " "); ok {
			return move
		}
	}
	t.Fatalf("the screen does not say which move the code carries\n--- screen ---\n%s", screen)
	return ""
}

// corrGameID reads the identifier out of what the command line announced.
func corrGameID(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "Started correspondence game ")
	if !ok {
		t.Fatalf("no game identifier in:\n%s", out)
	}
	id, _, _ := strings.Cut(rest, ".")
	return strings.TrimSpace(id)
}

func corrInvite(t *testing.T, out string) string {
	t.Helper()
	for _, field := range strings.Fields(out) {
		if strings.HasPrefix(field, "TWXI-") {
			return field
		}
	}
	t.Fatalf("no invitation in:\n%s", out)
	return ""
}

// TestTwoTerminalsPlayByCode drives both ends of one correspondence game through
// the compiled binary and checks the two stores end up holding the same game.
func TestTwoTerminalsPlayByCode(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("skipping terminal test: %v", err)
	}
	bin := binary(t)
	hostDir, guestDir := t.TempDir(), t.TempDir()

	started := corrRun(t, bin, hostDir, "--profile", "ada",
		"play", "correspondence", "--new", "--side", "vertical", "--size", "12")
	id := corrGameID(t, started)
	joined := corrRun(t, bin, guestDir, "--profile", "linus",
		"play", "correspondence", "--join", corrInvite(t, started))
	if !strings.Contains(joined, id) {
		t.Fatalf("the guest joined a different game:\n%s", joined)
	}

	// A terminal each. Neither knows anything about the other.
	open := func(dir, player string) *Terminal {
		tm := Start(t, bin+" --config "+dir+" --profile "+player+" play correspondence",
			Options{Width: 100, Height: 30, Dir: repoRoot(t), Env: []string{"TWIXTUI_CONFIG_DIR=" + dir}})
		tm.MustWaitFor("correspondence", 20*time.Second)
		return tm
	}
	host := open(hostDir, "ada")
	guest := open(guestDir, "linus")

	// The host moves. Committing must put the code on the screen by itself.
	host.SendKeys("Space")
	host.SendKeys("Enter")
	screen := host.MustWaitFor("send this code to", 20*time.Second)
	code := corrCodeOnScreen(t, screen)
	opening := corrMoveOnScreen(t, screen)
	host.AssertFits()
	host.SendKeys("Escape")

	// The guest pastes it in and applies it.
	corrPasteInto(t, guest, code)
	guest.MustWaitFor("TWX-", 20*time.Second)
	guest.SendKeys("Enter")
	guest.MustWaitFor("last "+opening, 20*time.Second)

	// And answers, on a hole the host's peg is not already in.
	guest.SendKeys("j")
	guest.SendKeys("j")
	guest.SendKeys("Space")
	guest.SendKeys("Enter")
	screen = guest.MustWaitFor("send this code to", 20*time.Second)
	reply := corrCodeOnScreen(t, screen)
	answer := corrMoveOnScreen(t, screen)
	if answer == opening {
		t.Fatalf("both ends played %s, so nothing was exchanged", answer)
	}
	guest.AssertFits()
	guest.SendKeys("Escape")

	corrPasteInto(t, host, reply)
	host.MustWaitFor("TWX-", 20*time.Second)
	host.SendKeys("Enter")
	host.MustWaitFor("last "+answer, 20*time.Second)

	// Leaving saves, which is how a correspondence game is put down between
	// turns.
	for _, tm := range []*Terminal{host, guest} {
		tm.SendKeys("q")
	}
	for _, tm := range []*Terminal{host, guest} {
		waitForExit(t, tm)
	}

	// A record carries the ruleset, the moves and its own digests, and nothing
	// about who played them, so the two ends agreeing is byte equality.
	hostRecord := corrRun(t, bin, hostDir, "game", "export", id)
	guestRecord := corrRun(t, bin, guestDir, "game", "export", id)
	if hostRecord != guestRecord {
		t.Fatalf("the two ends hold different games\n--- ada ---\n%s\n--- linus ---\n%s", hostRecord, guestRecord)
	}
	for _, move := range []string{opening, answer} {
		if !strings.Contains(hostRecord, move) {
			t.Errorf("the stored record does not hold %s:\n%s", move, hostRecord)
		}
	}
}

// waitForExit gives the program time to save and go.
func waitForExit(t *testing.T, tm *Terminal) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !tm.Alive() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the program did not exit\n--- screen ---\n%s", tm.Capture())
}
