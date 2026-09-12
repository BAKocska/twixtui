//go:build unix || windows

package leaderboard

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	crossProcessDirEnv  = "TWIXTUI_BOARD_HELPER_DIR"
	crossProcessModeEnv = "TWIXTUI_BOARD_HELPER_MODE"
	helperStartedLine   = "board-helper: started"
	helperOpenedLine    = "board-helper: opened"
	helperDoneLine      = "board-helper: done"
	helperRetryMode     = "retry"
	helperConflictMode  = "conflict"
	// goAheadFile is how the other process is told to go on. It lives beside
	// the board and is not a board file, so nothing that reads or sweeps the
	// directory touches it.
	goAheadFile = "helper-go-ahead"
	// The participants the two processes agree on. Both build the same result
	// from them, which is what makes the second recording a retry of the first
	// rather than a different game.
	helperPlayer   = "Balint"
	helperOpponent = "Bernadett"
)

// The child opens an empty board. The parent then writes the first result while
// holding the shared write lock. The child's Record must reload that result
// rather than decide from its original empty snapshot, across process boundaries.
func TestAnotherProcessRecordingTheSameGame(t *testing.T) {
	for _, mode := range []string{helperRetryMode, helperConflictMode} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			b := openBoard(t, dir)
			first := linked(result(helperPlayer, helperOpponent, Win, day(1)), gameA, digestA)

			helper := startBoardHelper(t, dir, mode)
			// The other process now holds the board it opened: no results at
			// all, and so no reason of its own to think this game is recorded.
			helper.await(t, helper.opened, "opened the board")

			release, err := lockFile(b.lockPath, true)
			if err != nil {
				t.Fatalf("taking the board's lock: %v", err)
			}
			released := false
			defer func() {
				if !released {
					release()
				}
			}()

			if err := os.WriteFile(filepath.Join(dir, goAheadFile), nil, 0o600); err != nil {
				t.Fatalf("writing the go-ahead: %v", err)
			}
			select {
			case <-helper.done:
				t.Fatalf("the other process decided about the game while this one held the lock: %s", helper.stop())
			case <-time.After(time.Second):
			}

			// The first row lands while the other process is waiting for the
			// lock, so the only way it can know about it is to read the file
			// again once it has the lock.
			data, err := marshal([]Result{first})
			if err != nil {
				t.Fatalf("encoding the first result: %v", err)
			}
			if err := atomicWrite(filepath.Join(dir, fileName), data); err != nil {
				t.Fatalf("writing the first result: %v", err)
			}

			released = true
			release()
			helper.await(t, helper.done, "finished recording")
			// The other process asserted the outcome it was started for: a
			// recognised retry, or a refused contradiction.
			if err := helper.cmd.Wait(); err != nil {
				t.Fatalf("the other process did not see the recorded game as it should: %v\n%s", err, helper.stop())
			}

			rows := openBoard(t, dir).History(helperPlayer, 0)
			if len(rows) != 1 {
				t.Fatalf("the board holds %d rows for one game, want 1: %+v", len(rows), rows)
			}
			got := rows[0]
			if got.GameID != gameA || got.RecordDigest != digestA {
				t.Errorf("the stored row names %q/%q, want the first recording's %q/%q", got.GameID, got.RecordDigest, gameA, digestA)
			}
			if !got.Played.Equal(first.Played) || got.Duration != first.Duration {
				t.Errorf("the stored row was played %v for %v, want the first recording's %v for %v",
					got.Played, got.Duration, first.Played, first.Duration)
			}
			if got.Outcome != first.Outcome {
				t.Errorf("the stored row reads %q, want the first recording's %q", got.Outcome, first.Outcome)
			}
		})
	}
}

// boardHelper is the other twixtui process and the lines it has reached.
type boardHelper struct {
	cmd                   *exec.Cmd
	complaints            *strings.Builder
	opened, done, scanned chan struct{}
}

// startBoardHelper starts this test binary again as the other process.
func startBoardHelper(t *testing.T, dir, mode string) *boardHelper {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestBoardHelperRecordsOneGame$")
	cmd.Env = append(os.Environ(), crossProcessDirEnv+"="+dir, crossProcessModeEnv+"="+mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &boardHelper{
		cmd:        cmd,
		complaints: &strings.Builder{},
		opened:     make(chan struct{}),
		done:       make(chan struct{}),
		scanned:    make(chan struct{}),
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.stop() })

	started := make(chan struct{})
	go func() {
		defer close(h.scanned)
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			switch strings.TrimSpace(lines.Text()) {
			case helperStartedLine:
				close(started)
			case helperOpenedLine:
				close(h.opened)
			case helperDoneLine:
				close(h.done)
			default:
				h.complaints.WriteString(lines.Text())
				h.complaints.WriteByte('\n')
			}
		}
		if err := lines.Err(); err != nil {
			fmt.Fprintf(h.complaints, "reading helper output: %v\n", err)
		}
	}()
	h.await(t, started, "started")
	return h
}

// await waits for one of the other process's steps, or says which step it never
// reached and what it complained about.
func (h *boardHelper) await(t *testing.T, step <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-step:
	case <-h.scanned:
		select {
		case <-step:
			return
		default:
			t.Fatalf("the other process exited before it %s: %s", what, h.stop())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the other process never %s: %s", what, h.stop())
	}
}

// stop joins the process and its output scanner before reading diagnostics.
func (h *boardHelper) stop() string {
	if h.cmd.ProcessState == nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
	<-h.scanned
	return h.complaints.String()
}

// TestBoardHelperRecordsOneGame is not a test of its own: it is the other
// process TestAnotherProcessRecordingTheSameGame needs, and it does nothing
// unless that test starts it. It goes through Open and Record, so the waiting it
// does and the decision it reaches are the ones twixtui reaches.
func TestBoardHelperRecordsOneGame(t *testing.T) {
	dir := os.Getenv(crossProcessDirEnv)
	if dir == "" {
		t.Skip("not the process this helper is for")
	}
	fmt.Println(helperStartedLine)
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("opening the board: %v", err)
	}
	fmt.Println(helperOpenedLine)
	waitForGoAhead(t, filepath.Join(dir, goAheadFile))

	// The retry of a finalisation: the same game and the same record, with the
	// later clock and longer session the second attempt has.
	attempt := linked(result(helperPlayer, helperOpponent, Win, day(9)), gameA, digestA)
	attempt.Duration = 4 * time.Hour
	switch mode := os.Getenv(crossProcessModeEnv); mode {
	case helperRetryMode:
		if err := b.Record(attempt); err != nil {
			t.Fatalf("recording the same game the other process recorded: %v", err)
		}
	case helperConflictMode:
		attempt.RecordDigest = digestB
		if err := b.Record(attempt); !errors.Is(err, ErrIdentityConflict) {
			t.Fatalf("recording another record for the same game = %v, want ErrIdentityConflict", err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	fmt.Println(helperDoneLine)
}

// waitForGoAhead blocks until the process that started this one has taken the
// board's lock, so that recording runs into it.
func waitForGoAhead(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the go-ahead at %s never arrived", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
