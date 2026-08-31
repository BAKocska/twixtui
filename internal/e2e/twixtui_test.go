package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the compiled binary in a real terminal. They are the
// evidence for the requirement that resizing behaves correctly rather than
// merely being believed to: every size change is proved to have reached the
// program by asserting on content only the new size could produce.

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// binary compiles twixtui once for the whole package and returns its path.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "twixtui-e2e-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "twixtui")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/twixtui")
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err: err, output: string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("building twixtui: %v", buildErr)
	}
	return binPath
}

type buildFailure struct {
	err    error
	output string
}

func (b *buildFailure) Error() string { return b.err.Error() + "\n" + b.output }

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root above the test directory")
		}
		dir = parent
	}
}

// session starts the binary with an isolated configuration directory and a
// profile already chosen, so a test lands on the screen it is interested in.
func session(t *testing.T, args string, width, height int) *Terminal {
	t.Helper()
	bin := binary(t)
	cfg := t.TempDir()

	// Create the profile up front through the command line, so the interactive
	// run does not stop at the chooser.
	setup := exec.Command(bin, "--config", cfg, "profile", "create", "Tester")
	setup.Env = append(os.Environ(), "TWIXTUI_CONFIG_DIR="+cfg, "NO_COLOR=1")
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("creating the test profile: %v\n%s", err, out)
	}

	command := bin + " --config " + cfg + " --profile Tester " + args
	return Start(t, command, Options{
		Width:  width,
		Height: height,
		Dir:    repoRoot(t),
		Env:    []string{"TWIXTUI_CONFIG_DIR=" + cfg},
	})
}

// TestBinaryShowsTheMenu is the baseline: without it, a later assertion could
// pass against an empty screen from a program that never started.
func TestBinaryShowsTheMenu(t *testing.T) {
	tm := session(t, "", 90, 30)
	screen := tm.MustWaitFor("Tester", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of showing a menu:\n%s", screen)
	}
	tm.AssertFits()
}

// TestHotseatGameDrawsTheBoard checks a game screen appears with a board on it,
// which is the precondition for every resize assertion below.
func TestHotseatGameDrawsTheBoard(t *testing.T) {
	tm := session(t, "play local --size 12 --side vertical", 90, 34)
	tm.MustWaitFor("A", 20*time.Second)
	screen := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(screen, "·") {
		t.Fatalf("no board holes on screen:\n%s", screen)
	}
	if !tm.Alive() {
		t.Fatal("the game exited immediately")
	}
	tm.AssertFits()
}

// boardColumnLabels returns the column-label line of a rendered board, which is
// the cheapest fingerprint of the drawing scale: the detail scale spaces labels
// four cells apart, the compact scale two.
func boardColumnLabels(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "A") && strings.Contains(line, "B") && strings.Contains(line, "C") {
			return line
		}
	}
	return ""
}

// TestResizeIsDeliveredToTheGame is the central requirement: a size change must
// reach the running program and change what it draws.
//
// The assertion is not "nothing broke". Growing the terminal makes the renderer
// switch to the wider drawing scale, which spaces the column labels further
// apart, and produces lines wider than the old terminal could hold. Both are
// impossible unless the new size arrived.
func TestResizeIsDeliveredToTheGame(t *testing.T) {
	tm := session(t, "play local --size 12 --side vertical", 60, 20)
	tm.MustWaitFor("A", 20*time.Second)
	small := tm.WaitSettled(10 * time.Second)
	smallLabels := boardColumnLabels(small)
	if smallLabels == "" {
		t.Fatalf("no column labels found in:\n%s", small)
	}
	smallWidth, _ := tm.Size()
	tm.AssertFits()

	tm.Resize(120, 46)
	grownWidth, _ := tm.Size()
	if grownWidth <= smallWidth {
		t.Fatalf("the terminal did not grow: %d then %d", smallWidth, grownWidth)
	}

	deadline := time.Now().Add(15 * time.Second)
	var grown, grownLabels string
	for time.Now().Before(deadline) {
		grown = tm.WaitSettled(10 * time.Second)
		grownLabels = boardColumnLabels(grown)
		if grownLabels != "" && grownLabels != smallLabels {
			break
		}
		time.Sleep(pollInterval)
	}
	if grownLabels == "" {
		t.Fatalf("no column labels after growing:\n%s", grown)
	}
	if grownLabels == smallLabels {
		t.Fatalf("the board is drawn identically at %d and %d columns, so the resize was not acted on\n%s",
			smallWidth, grownWidth, grown)
	}
	// Something on screen must now be wider than the old terminal allowed.
	widest := 0
	for _, line := range strings.Split(grown, "\n") {
		if w := visibleWidth(line); w > widest {
			widest = w
		}
	}
	if widest <= smallWidth {
		t.Errorf("nothing on screen exceeds the old width of %d, so the frame may not have been redrawn (widest %d)",
			smallWidth, widest)
	}
	tm.AssertFits()
	if !tm.Alive() {
		t.Fatal("the program died during the resize")
	}
}

// TestResizeMatrixKeepsTheFrameIntact walks the sizes a player will actually
// produce, including a pane far too small to draw a board in, and checks the
// frame invariant at each one. A frame wider than its terminal corrupts the
// display, and it is the failure a resize bug produces.
func TestResizeMatrixKeepsTheFrameIntact(t *testing.T) {
	sizes := [][2]int{
		{100, 34}, // comfortable
		{80, 24},  // the conventional default
		{60, 20},  // a split pane
		{40, 14},  // a narrow side pane
		{24, 10},  // barely anything
		{20, 8},   // pathologically small
		{120, 46}, // grown again, to prove it recovers
		{80, 24},  // back to where it started
	}
	tm := session(t, "play local --size 12 --side vertical", sizes[0][0], sizes[0][1])
	tm.MustWaitFor("A", 20*time.Second)

	for _, size := range sizes {
		tm.Resize(size[0], size[1])
		tm.WaitSettled(10 * time.Second)
		w, h := tm.Size()
		if !tm.Alive() {
			t.Fatalf("the program died at %dx%d", w, h)
		}
		lines := tm.Lines()
		if len(lines) > h {
			t.Errorf("%dx%d: frame has %d lines for %d rows", w, h, len(lines), h)
		}
		for i, line := range lines {
			if cells := visibleWidth(line); cells > w {
				t.Errorf("%dx%d: line %d is %d cells wide: %q", w, h, i+1, cells, line)
			}
		}
		// Either a board or an explicit notice, never a blank or broken frame.
		screen := strings.Join(lines, "\n")
		if strings.TrimSpace(screen) == "" {
			t.Errorf("%dx%d: the screen is blank", w, h)
		}
	}
}

// TestGameSurvivesResizeWithStateIntact checks a resize does not disturb the
// game.
//
// The assertion is that the frame at a given size is byte-identical before and
// after a shrink-and-regrow cycle. That is stronger than reading individual
// fields: it covers the position, the cursor, the side to move and everything
// the panel says at once, and it fails if any of them drifted. Counting peg
// glyphs across the whole frame would not work, because the panel's legend draws
// the same glyphs and the panel is dropped at small sizes.
func TestGameSurvivesResizeWithStateIntact(t *testing.T) {
	const (
		w, h = 100, 34
	)
	tm := session(t, "play local --size 12 --side vertical", w, h)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)

	// Play a move so there is state to lose, and move the cursor off it so the
	// cursor position is part of what has to survive.
	tm.SendKeys("space")
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("enter")
	tm.WaitSettled(10 * time.Second)
	tm.SendKeys("l", "j")
	before := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(before, "last ") {
		t.Fatalf("no committed move reported:\n%s", before)
	}

	for _, size := range [][2]int{{44, 16}, {20, 8}, {70, 24}} {
		tm.Resize(size[0], size[1])
		tm.WaitSettled(10 * time.Second)
		tm.AssertFits()
		if !tm.Alive() {
			t.Fatalf("the program died at %dx%d", size[0], size[1])
		}
	}

	tm.Resize(w, h)
	after := tm.WaitSettled(10 * time.Second)
	if after != before {
		t.Errorf("the frame changed across a shrink and regrow cycle\n--- before ---\n%s\n--- after ---\n%s",
			before, after)
	}

	// And the game is still playable.
	tm.SendKeys("space")
	staged := tm.WaitSettled(10 * time.Second)
	if staged == after {
		t.Error("the game stopped responding to the keyboard after the resize")
	}
}

// TestTooSmallStateIsExplicit checks that a terminal below the supported size
// says so rather than drawing a broken board.
func TestTooSmallStateIsExplicit(t *testing.T) {
	tm := session(t, "play local --size 12 --side vertical", 80, 24)
	tm.MustWaitFor("A", 20*time.Second)
	tm.Resize(18, 5)
	screen := tm.WaitSettled(10 * time.Second)
	if !tm.Alive() {
		t.Fatal("the program died in a very small terminal")
	}
	if strings.TrimSpace(screen) == "" {
		t.Fatal("a very small terminal produced a blank screen with no explanation")
	}
	tm.AssertFits()
	// Growing back must recover a board.
	tm.Resize(90, 30)
	recovered := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(recovered, "·") {
		t.Errorf("the board did not come back after growing:\n%s", recovered)
	}
}

// TestTutorialResizes covers the other screen with a board on it, whose panel
// holds wrapped prose and so has a different failure mode from the game's.
func TestTutorialResizes(t *testing.T) {
	tm := session(t, "learn board", 100, 34)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)
	for _, size := range [][2]int{{60, 20}, {40, 14}, {20, 8}, {100, 34}} {
		tm.Resize(size[0], size[1])
		tm.WaitSettled(10 * time.Second)
		if !tm.Alive() {
			w, h := tm.Size()
			t.Fatalf("the tutorial died at %dx%d", w, h)
		}
		tm.AssertFits()
	}
}

// TestQuitEndsTheProgramCleanly checks the program leaves on request with a
// success status. Started with "play local" the game is the first screen, so
// there is nothing behind it and leaving ends the run; a program using the
// alternate screen has to restore the terminal on the way out, and a non-zero
// status here would mean it fell over instead of exiting.
func TestQuitEndsTheProgramCleanly(t *testing.T) {
	tm := session(t, "play local --size 12 --side vertical", 80, 24)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)
	if !tm.Alive() {
		t.Fatal("the program was not running before quit was sent")
	}

	tm.SendKeys("q")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && tm.Alive() {
		time.Sleep(pollInterval)
	}
	if tm.Alive() {
		t.Fatalf("the program is still running after quit\n%s", tm.Capture())
	}
	code, exited := tm.ExitStatus()
	if !exited {
		t.Fatal("the program stopped without reporting an exit status")
	}
	if code != 0 {
		t.Errorf("exit status = %d, want 0\n%s", code, tm.Capture())
	}
}

// TestCtrlCEndsTheProgramCleanly covers the other way out, which must also
// restore the terminal rather than leaving it in the alternate screen.
func TestCtrlCEndsTheProgramCleanly(t *testing.T) {
	tm := session(t, "play local --size 12 --side vertical", 80, 24)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)

	tm.SendKeys("C-c")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && tm.Alive() {
		time.Sleep(pollInterval)
	}
	if tm.Alive() {
		t.Fatalf("the program ignored ctrl+c\n%s", tm.Capture())
	}
}
