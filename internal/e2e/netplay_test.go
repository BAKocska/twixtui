package e2e

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/gamestore"
)

// Two machines playing one game is the mode with the most that can go wrong on
// a platform change and the least that a single-process test can see: a
// listener, a connection, two terminals and two stores that have to end up
// holding the same game. These tests play it for real, with both ends compiled
// and both ends driven through a terminal.
//
// Nothing here touches a network beyond this machine. The host binds
// 127.0.0.1 and a port the operating system picks, the relay does the same, and
// every address a client is given is read out of what the program printed
// rather than assumed — so a run cannot depend on a fixed port being free, on
// an interface existing, or on a firewall letting anything in.

// npWait bounds the waits in this file. Two programs, a handshake and a
// terminal each make these the slowest tests in the package.
const npWait = 30 * time.Second

var (
	npListening  = regexp.MustCompile(`Listening on (127\.0\.0\.1:\d+)`)
	npRelayReady = regexp.MustCompile(`relay listening on (127\.0\.0\.1:\d+)`)
	npPairing    = regexp.MustCompile(`Pairing code:\s+(\S+)`)
	npLastMove   = regexp.MustCompile(`last ([A-Z]{1,2}\d{1,2})`)
)

// npTerminal starts the compiled binary as one player, in its own terminal and
// against its own configuration directory.
func npTerminal(t *testing.T, cfg, player string, args ...string) *Terminal {
	t.Helper()
	command := append([]string{binary(t), "--config", cfg, "--profile", player}, args...)
	return Start(t, command, Options{
		Width:  100,
		Height: 30,
		Dir:    repoRoot(t),
		Env:    []string{"TWIXTUI_CONFIG_DIR=" + cfg},
	})
}

// npMatch pulls one thing the program printed off the screen.
func npMatch(t *testing.T, re *regexp.Regexp, screen, what string) string {
	t.Helper()
	found := re.FindStringSubmatch(screen)
	if found == nil {
		t.Fatalf("no %s on the screen:\n%s", what, screen)
	}
	return found[1]
}

// npAwaitBoard waits for a game screen with a board on it, which is what both
// ends draw once the connection exists. Nothing either program prints before
// then — the address, the pairing code, the promise to wait — holds a board
// hole, so this is the point at which the two are playing.
func npAwaitBoard(t *testing.T, tm *Terminal) string {
	t.Helper()
	screen := tm.MustWaitFor("·", npWait)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of playing:\n%s", screen)
	}
	return screen
}

// npCommit plays the peg under the cursor and returns the move the board then
// reports.
//
// Every key is waited out with the frame it produced rather than with a settle.
// A settle can return the frame from before the key arrived — a still screen is
// still whether or not anything was typed at it — and on a slow machine that
// hands the next key to a screen that has not moved yet, so a cursor ends up
// somewhere else and a peg is staged in the wrong hole.
//
// The commit is then waited for by the reported move differing from the one
// given, rather than by a move being reported at all: on a resumed game the
// board already names the move played before this one, so a wait for "a move"
// would be satisfied by the old one and the commit never observed.
func npCommit(t *testing.T, tm *Terminal, previous string, cursor ...string) string {
	t.Helper()
	frame := tm.WaitSettled(10 * time.Second)
	for _, key := range append(append([]string{}, cursor...), "Space") {
		tm.SendKeys(key)
		frame = tm.WaitChanged(frame, npWait)
	}
	tm.SendKeys("Enter")

	deadline := time.Now().Add(npWait)
	for time.Now().Before(deadline) {
		if found := npLastMove.FindStringSubmatch(tm.Capture()); found != nil && found[1] != previous {
			return found[1]
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("no committed move appeared on the board after the previous one, %q:\n%s", previous, tm.Capture())
	return ""
}

// npQuit leaves both ends and checks each went cleanly. Leaving is what saves
// an unfinished game, so a program that fell over here has not stored the game
// the assertions below are about.
func npQuit(t *testing.T, terminals ...*Terminal) {
	t.Helper()
	for _, tm := range terminals {
		tm.SendKeys("q")
	}
	for i, tm := range terminals {
		code, exited := tm.WaitExit(npWait)
		if !exited || code != 0 {
			t.Fatalf("player %d did not leave cleanly: exited=%v status=%d\n%s", i+1, exited, code, tm.Capture())
		}
	}
}

// npOnlyGame is the single game a configuration directory holds. That there is
// exactly one is part of what is being checked: a continued game goes back into
// the row it came out of rather than beside it.
func npOnlyGame(t *testing.T, cfg string) gamestore.Saved {
	t.Helper()
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatalf("opening the store in %s: %v", cfg, err)
	}
	saved := store.List()
	if len(saved) != 1 {
		t.Fatalf("%s holds %d saved games, want 1: %+v", cfg, len(saved), saved)
	}
	return saved[0]
}

// npExport reads a stored game back out through the program.
func npExport(t *testing.T, bin, cfg, id string) string {
	t.Helper()
	res := cliRun(t, bin, cfg, "", "game", "export", id)
	if res.code != 0 {
		t.Fatalf("exporting %s exited %d: %s%s", id, res.code, res.stdout, res.stderr)
	}
	return res.stdout
}

// TestDirectNetworkGamePlaysAndResumes plays a game over a direct connection
// between two compiled programs, puts it down, and picks it up again on a new
// connection.
//
// The evidence is the record at each end, exported by the program itself: a
// record carries the ruleset, the moves and its own digests and nothing about
// who played them, so the two ends agreeing is byte equality. Everything else
// here — the address, the pairing, the transcript coming back after a resume —
// is only the way that comparison is arrived at.
func TestDirectNetworkGamePlaysAndResumes(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	hostCfg, guestCfg := t.TempDir(), t.TempDir()

	host := npTerminal(t, hostCfg, "ada",
		"play", "host", "--bind", "127.0.0.1", "--port", "0", "--size", "12", "--side", "vertical")
	addr := npMatch(t, npListening, host.MustWaitFor("Waiting for them to connect. Press ctrl+c to give up.", npWait), "address the host bound")
	guest := npTerminal(t, guestCfg, "linus", "play", "join", addr)

	npAwaitBoard(t, host)
	npAwaitBoard(t, guest)

	opening := npCommit(t, host, "")
	guest.MustWaitFor("last "+opening, npWait)
	// Two rows down, so the answer is not the hole the opening peg is in.
	answer := npCommit(t, guest, opening, "j", "j")
	if answer == opening {
		t.Fatalf("both ends report %s as the last move, so nothing crossed the connection", answer)
	}
	host.MustWaitFor("last "+answer, npWait)
	host.AssertFits()
	guest.AssertFits()

	npQuit(t, host, guest)

	hostGame, guestGame := npOnlyGame(t, hostCfg), npOnlyGame(t, guestCfg)
	if hostGame.Kind != gamestore.Remote {
		t.Errorf("the host stored a %s game, want %s", hostGame.Kind, gamestore.Remote)
	}
	played := npExport(t, bin, hostCfg, hostGame.ID)
	if guestRecord := npExport(t, bin, guestCfg, guestGame.ID); played != guestRecord {
		t.Fatalf("the two ends hold different games\n--- ada ---\n%s\n--- linus ---\n%s", played, guestRecord)
	}
	for _, move := range []string{opening, answer} {
		if !strings.Contains(played, move) {
			t.Errorf("the stored record does not hold %s:\n%s", move, played)
		}
	}

	// Picking it up again. Which end waits is a choice made each time, so the
	// same two players take the same roles here only because it is simpler to
	// read, not because the saved game decided it.
	hostAgain := npTerminal(t, hostCfg, "ada",
		"play", "host", "--bind", "127.0.0.1", "--port", "0", "--resume", hostGame.ID)
	addr = npMatch(t, npListening, hostAgain.MustWaitFor("Waiting for them to connect. Press ctrl+c to give up.", npWait), "address the resumed host bound")
	guestAgain := npTerminal(t, guestCfg, "linus", "play", "join", addr, "--resume", guestGame.ID)

	// The game that comes back is the game that was played, at both ends.
	hostAgain.MustWaitFor("last "+answer, npWait)
	guestAgain.MustWaitFor("last "+answer, npWait)

	// Two rows up from the middle: a hole neither of the first two moves used.
	third := npCommit(t, hostAgain, answer, "k", "k")
	guestAgain.MustWaitFor("last "+third, npWait)
	npQuit(t, hostAgain, guestAgain)

	resumed := npExport(t, bin, hostCfg, hostGame.ID)
	if resumed == played {
		t.Fatal("the continued game was not saved back into the game it continued")
	}
	if !strings.Contains(resumed, third) {
		t.Errorf("the record does not hold the move played after the resume:\n%s", resumed)
	}
	if guestRecord := npExport(t, bin, guestCfg, guestGame.ID); resumed != guestRecord {
		t.Fatalf("the two ends disagree after resuming\n--- ada ---\n%s\n--- linus ---\n%s", resumed, guestRecord)
	}
	// Continuing a game does not leave a second one behind.
	npOnlyGame(t, hostCfg)
	npOnlyGame(t, guestCfg)
}

// TestRelayedNetworkGamePlays covers the other route: neither end listens, and
// a relay both can reach passes the bytes between them. It is the route two
// players behind home routers use, and the one where three processes have to
// come up in the right order.
func TestRelayedNetworkGamePlays(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	relay := npRelay(t, bin)
	hostCfg, guestCfg := t.TempDir(), t.TempDir()

	host := npTerminal(t, hostCfg, "ada",
		"play", "host", "--relay", relay, "--size", "12", "--side", "vertical")
	code := npMatch(t, npPairing, host.MustWaitFor("Waiting for them to join. Press ctrl+c to give up.", npWait), "pairing code")
	guest := npTerminal(t, guestCfg, "linus", "play", "join", "--relay", relay, code)

	npAwaitBoard(t, host)
	npAwaitBoard(t, guest)

	opening := npCommit(t, host, "")
	guest.MustWaitFor("last "+opening, npWait)
	answer := npCommit(t, guest, opening, "j", "j")
	if answer == opening {
		t.Fatalf("both ends report %s as the last move, so nothing crossed the relay", answer)
	}
	host.MustWaitFor("last "+answer, npWait)

	npQuit(t, host, guest)

	hostRecord := npExport(t, bin, hostCfg, npOnlyGame(t, hostCfg).ID)
	guestRecord := npExport(t, bin, guestCfg, npOnlyGame(t, guestCfg).ID)
	if hostRecord != guestRecord {
		t.Fatalf("the two ends hold different games\n--- ada ---\n%s\n--- linus ---\n%s", hostRecord, guestRecord)
	}
	for _, move := range []string{opening, answer} {
		if !strings.Contains(hostRecord, move) {
			t.Errorf("the stored record does not hold %s:\n%s", move, hostRecord)
		}
	}
}

// npRelay runs a relay and returns the address it is listening on.
//
// The address is read from the line the relay prints once it is bound, which is
// the readiness signal the command was written to give. Waiting for the process
// to exist instead would start the players against a socket that is not there
// yet, and a connection refused a millisecond too early looks exactly like a
// relay that does not work.
func npRelay(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(bin, "--config", t.TempDir(), "serve", "--addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("reading the relay's output: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the relay: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if match := npRelayReady.FindStringSubmatch(scanner.Text()); match != nil {
				select {
				case found <- match[1]:
				default:
				}
			}
		}
	}()
	select {
	case addr := <-found:
		return addr
	case <-time.After(npWait):
		t.Fatal("the relay never reported an address it was listening on")
		return ""
	}
}
