package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
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

// binaryEnv names an already-built twixtui to test instead of building one.
// Verifying a release means driving the artefact that was published, not a
// fresh build of the same source: a build made here would test the toolchain
// on this machine and say nothing about what was downloaded.
const binaryEnv = "TWIXTUI_E2E_BINARY"

// binary returns the twixtui under test, compiling one once for the whole
// package unless binaryEnv names one.
//
// A binaryEnv that cannot be used is a failure and never a quiet fall back to
// building: a release verification that silently tested a local build instead
// of the artefact would report a pass for something nobody shipped.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if named, present := os.LookupEnv(binaryEnv); present {
			binPath, buildErr = prebuiltBinary(named)
			return
		}
		dir, err := os.MkdirTemp("", "twixtui-e2e-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		// Windows will not execute a file without the extension, and the go
		// tool does not add one to an explicit -o.
		binPath = filepath.Join(dir, "twixtui"+exeSuffix)
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/twixtui")
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err: err, output: string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("the twixtui under test is not usable: %v", buildErr)
	}
	return binPath
}

// exeSuffix is what an executable is called on this platform.
var exeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()

// prebuiltBinary resolves and checks the binary binaryEnv names. The path is
// made absolute because the terminal starts the program from the module root
// rather than from wherever the suite was invoked.
func prebuiltBinary(named string) (string, error) {
	abs, err := filepath.Abs(named)
	if err != nil {
		return "", fmt.Errorf("%s names %q, which has no absolute path: %w", binaryEnv, named, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s names %s, which cannot be read: %w", binaryEnv, abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s names %s, which is not a file", binaryEnv, abs)
	}
	// LookPath is the executable test rather than a mode bit: on Windows what
	// makes a file runnable is its extension, so an artefact unpacked without
	// one is refused here with a name rather than at the terminal with a
	// failure to start.
	if _, err := exec.LookPath(abs); err != nil {
		return "", fmt.Errorf("%s names %s, which cannot be executed: %w", binaryEnv, abs, err)
	}
	return abs, nil
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
//
// The arguments are an argument vector rather than a command line. Nothing
// here is handed to a shell, so a configuration directory with a space in its
// path — which is where Windows puts a temporary directory — needs no quoting
// and cannot be split.
func session(t *testing.T, width, height int, args ...string) *Terminal {
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

	return startIn(t, cfg, width, height, args...)
}

// sessionIn is session with a configuration directory the caller keeps, so two
// launches can share one machine's state. The introduction is shown once per
// profile, and proving that needs a second launch against the first one's store.
func sessionIn(t *testing.T, cfg string, width, height int, args ...string) *Terminal {
	t.Helper()
	bin := binary(t)
	setup := exec.Command(bin, "--config", cfg, "profile", "create", "Tester")
	setup.Env = append(os.Environ(), "TWIXTUI_CONFIG_DIR="+cfg, "NO_COLOR=1")
	// A second launch finds the profile already there, which is not an error.
	_ = setup.Run()
	return startIn(t, cfg, width, height, args...)
}

func startIn(t *testing.T, cfg string, width, height int, args ...string) *Terminal {
	t.Helper()
	command := append([]string{binary(t), "--config", cfg, "--profile", "Tester"}, args...)
	return Start(t, command, Options{
		Width:  width,
		Height: height,
		Dir:    repoRoot(t),
		Env:    []string{"TWIXTUI_CONFIG_DIR=" + cfg},
	})
}

// hotseatGame is the game the resize tests drive: one fixed board, so a frame
// captured at one size can be compared with the frame at another.
var hotseatGame = []string{"play", "local", "--size", "12", "--side", "vertical"}

// TestBinaryShowsTheMenu is the baseline: without it, a later assertion could
// pass against an empty screen from a program that never started.
func TestBinaryShowsTheMenu(t *testing.T) {
	t.Parallel()
	tm := session(t, 90, 30)

	// A brand-new machine meets the introduction before the menu, which is the
	// point of the introduction, so this walks the path a first-time player
	// actually walks: skip it, then the menu. Asserting the introduction appears
	// first is deliberate rather than incidental — a regression that dropped it
	// would otherwise show up here as a pass.
	intro := tm.MustWaitFor("skip", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of showing the introduction:\n%s", intro)
	}
	tm.AssertFits()
	tm.SendKeys("q")

	// Keyed on an entry rather than on the profile name: the introduction leaves
	// a note about the tutorial, which takes the top row on this one frame, so
	// the title is not what a first run settles on. The entries are.
	screen := tm.MustWaitFor("Play", 20*time.Second)
	if !strings.Contains(screen, "Quit") {
		t.Fatalf("the front screen has no way out on it:\n%s", screen)
	}
	if !tm.Alive() {
		t.Fatalf("the program exited instead of showing a menu:\n%s", screen)
	}
	tm.AssertFits()
}

// TestTheIntroductionIsNotShownTwice is the other half: having skipped it once,
// the next launch on the same machine goes straight to the menu. A first-run
// screen that returns every launch is the thing players complain about.
func TestTheIntroductionIsNotShownTwice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := sessionIn(t, dir, 90, 30)
	first.MustWaitFor("skip", 20*time.Second)
	first.SendKeys("q")
	first.MustWaitFor("Play", 20*time.Second)
	first.SendKeys("q")
	for range 40 {
		if !first.Alive() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	second := sessionIn(t, dir, 90, 30)
	screen := second.MustWaitFor("Play", 20*time.Second)
	if strings.Contains(screen, "skip") {
		t.Fatalf("the introduction came back on the second launch:\n%s", screen)
	}
	// With no note in the way, the front screen names who is playing. Games and
	// standings are recorded against that profile, so it belongs on the screen
	// the player starts from.
	if !strings.Contains(screen, "Tester") {
		t.Errorf("the front screen does not say which profile is playing:\n%s", screen)
	}
	second.AssertFits()
}

// TestHotseatGameDrawsTheBoard checks a game screen appears with a board on it,
// which is the precondition for every resize assertion below.
func TestHotseatGameDrawsTheBoard(t *testing.T) {
	t.Parallel()
	tm := session(t, 90, 34, hotseatGame...)
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
	t.Parallel()
	tm := session(t, 60, 20, hotseatGame...)
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
	t.Parallel()
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
	tm := session(t, sizes[0][0], sizes[0][1], hotseatGame...)
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
	t.Parallel()
	const (
		w, h = 100, 34
	)
	tm := session(t, w, h, hotseatGame...)
	tm.MustWaitFor("A", 20*time.Second)
	frame := tm.WaitSettled(10 * time.Second)

	// Play a move so there is state to lose, and move the cursor off it so the
	// cursor position is part of what has to survive.
	//
	// Each key is waited out with the frame it produced. Settling alone would
	// not do: a screen that has not received the key yet is as still as one
	// that has finished redrawing, so on a slow machine the next key would go
	// to the frame before it and the sequence would come apart.
	for _, key := range []string{"space", "enter", "l", "j"} {
		tm.SendKeys(key)
		frame = tm.WaitChanged(frame, 20*time.Second)
	}
	before := frame
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

	after := tm.ResizeAndWait(w, h, 20*time.Second)
	if after != before {
		t.Errorf("the frame changed across a shrink and regrow cycle\n--- before ---\n%s\n--- after ---\n%s",
			before, after)
	}

	// And the game is still playable: the key has to reach it and change the
	// screen, which is what WaitChanged fails on if it does not.
	tm.SendKeys("space")
	tm.WaitChanged(after, 20*time.Second)
}

// TestTooSmallStateIsExplicit checks that a terminal below the supported size
// says so rather than drawing a broken board.
func TestTooSmallStateIsExplicit(t *testing.T) {
	t.Parallel()
	tm := session(t, 80, 24, hotseatGame...)
	tm.MustWaitFor("A", 20*time.Second)
	screen := tm.ResizeAndWait(18, 5, 20*time.Second)
	if !tm.Alive() {
		t.Fatal("the program died in a very small terminal")
	}
	if strings.TrimSpace(screen) == "" {
		t.Fatal("a very small terminal produced a blank screen with no explanation")
	}
	tm.AssertFits()
	// Growing back must recover a board.
	tm.Resize(90, 30)
	recovered, err := tm.WaitFor("·", 20*time.Second)
	if err != nil {
		t.Fatalf("the board did not come back after growing: %v", err)
	}
	if !strings.Contains(recovered, "·") {
		t.Errorf("the board did not come back after growing:\n%s", recovered)
	}
}

// TestTutorialResizes covers the other screen with a board on it, whose panel
// holds wrapped prose and so has a different failure mode from the game's.
func TestTutorialResizes(t *testing.T) {
	t.Parallel()
	tm := session(t, 100, 34, "learn", "board")
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)
	for _, size := range [][2]int{{60, 20}, {40, 14}, {20, 8}, {100, 34}} {
		tm.ResizeAndWait(size[0], size[1], 20*time.Second)
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
	t.Parallel()
	tm := session(t, 80, 24, hotseatGame...)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)
	if !tm.Alive() {
		t.Fatal("the program was not running before quit was sent")
	}

	tm.SendKeys("q")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited {
		t.Fatalf("the program is still running after quit, or stopped without reporting an exit status\n%s", tm.Capture())
	}
	if code != 0 {
		t.Errorf("exit status = %d, want 0\n%s", code, tm.Capture())
	}
}

// TestCtrlCEndsTheProgramCleanly covers the other way out, which must also
// restore the terminal rather than leaving it in the alternate screen.
func TestCtrlCEndsTheProgramCleanly(t *testing.T) {
	t.Parallel()
	tm := session(t, 80, 24, hotseatGame...)
	tm.MustWaitFor("A", 20*time.Second)
	tm.WaitSettled(10 * time.Second)

	tm.SendKeys("C-c")
	if _, exited := tm.WaitExit(20 * time.Second); !exited {
		t.Fatalf("the program ignored ctrl+c\n%s", tm.Capture())
	}
}

// Finished boards still support inspection and leaving, but must not advertise
// turn edits. Drive the compiled program so a correct helper that is not wired
// into the rendered screen cannot satisfy this regression.
func TestFinishedBoardHelpAndInspectionSurviveResize(t *testing.T) {
	t.Parallel()
	tm := session(t, 140, 44, hotseatGame...)
	tm.MustWaitFor("vertical to move", 20*time.Second)
	before := tm.WaitSettled(10 * time.Second)
	mutationKeys := regexp.MustCompile(`(?m)(?:^| {2,})(?:space|x|a|d|r|\?)\s+\S`)
	if !mutationKeys.MatchString(before) || !tm.Alive() {
		t.Fatalf("no live turn-editing controls before resignation:\n%s", before)
	}

	tm.SendKeys("r")
	tm.MustWaitFor("y/n", 10*time.Second)
	tm.SendKeys("y")
	tm.MustWaitFor("game over", 10*time.Second)
	finished := tm.WaitSettled(10 * time.Second)
	if mutationKeys.MatchString(finished) {
		t.Fatalf("finished board still advertises turn edits:\n%s", finished)
	}
	tm.AssertFits()
	cursorCell := func(screen string) (int, int) {
		t.Helper()
		for row, line := range strings.Split(screen, "\n") {
			if at := strings.Index(line, "[·]"); at >= 0 {
				return visibleWidth(line[:at]), row
			}
		}
		t.Fatalf("empty-board inspection cursor is missing:\n%s", screen)
		return 0, 0
	}
	oldX, oldY := cursorCell(finished)

	tm.SendKeys("l")
	moved := tm.WaitChanged(finished, 10*time.Second)
	newX, newY := cursorCell(moved)
	if newX != oldX+4 || newY != oldY {
		t.Fatalf("right key moved the detailed-board cursor from (%d,%d) to (%d,%d), want (%d,%d)",
			oldX, oldY, newX, newY, oldX+4, oldY)
	}
	small := tm.ResizeAndWait(40, 14, 10*time.Second)
	if boardColumnLabels(small) == boardColumnLabels(moved) {
		t.Fatal("shrink did not change board scale or viewport")
	}
	tm.AssertFits()
	restored := tm.ResizeAndWait(140, 44, 10*time.Second)
	if restored != moved {
		t.Fatalf("finished position or help changed after resize\nbefore:\n%s\nafter:\n%s", moved, restored)
	}
	if !tm.Alive() || mutationKeys.MatchString(restored) {
		t.Fatalf("finished board lost its inspection state:\n%s", restored)
	}

	tm.SendKeys("Enter")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("Enter did not leave the finished game cleanly: exit=%v code=%d\n%s", exited, code, tm.Capture())
	}
}

func TestHintScopesNoRouteClaimsOnWideAndNarrowBoards(t *testing.T) {
	t.Parallel()
	// This is a legal committed std position, not a manually assembled board.
	// Both placement proxies see no route, but E2 plus the absent C3-B5 link wins.
	const record = `twixtui-record 1
ruleset size=6;deliberate=true;removal=true;pegremoval=false;owncross=false;swap=true
result ongoing not-over
position fc88569b91a76ea3
entries 30
moves B1; A2; C1; A3; D1; A4; E1; A5; B6; F2; C6; F3; D6; F4; E6; F5; B2 ~B2:D1; D3 ~D3:F2 ~D3:F4; C2 ~C2:E1; B4 ~B4:D3 ~A2:B4; D2 ~B1:D2; D4 ~D4:F3 ~D4:F5; B3 ~B3:C1 ~B3:D2; E4 ~E4:F2; C3 ~C3:D1 ~B1:C3; C5 ~C5:D3 ~C5:E4 ~A4:C5; C4 ~C4:D2 ~C4:D6 ~B6:C4 ~B2:C4; D5 ~D5:F4 ~B4:D5; B5 ~B5:C3 +B1:C3; E5 ~E5:F3 ~D3:E5
digest 58bd5b8fa99be99f
`
	g, rec, err := game.LoadRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if g.Result().Over() {
		t.Fatal("counterexample is already terminal")
	}
	proof := g.Clone()
	if err := proof.PlayNotation("E2 +B5:C3"); err != nil {
		t.Fatal(err)
	}
	if proof.Result().Winner() != game.Vertical {
		t.Fatal("the legal deliberate-link turn no longer refutes a draw")
	}

	cfg := t.TempDir()
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := gamestore.NewID()
	if err := store.Put(gamestore.Saved{
		ID: id, Kind: gamestore.VersusBot, Player: "Tester",
		Side: "vertical", Opponent: "bot:max", Record: rec.Encode(),
	}); err != nil {
		t.Fatal(err)
	}
	tm := sessionIn(t, cfg, 120, 36)
	tm.MustWaitFor("introduction", 20*time.Second)
	tm.SendKeys("q")
	tm.MustWaitFor("Continue a saved game", 10*time.Second)
	tm.SendKeys("j", "Enter")
	tm.MustWaitFor("Tester vs max bot", 10*time.Second)
	tm.SendKeys("Enter")
	tm.MustWaitFor("move 31", 10*time.Second)
	tm.SendKeys("?")
	tm.MustWaitFor("placement-only", 15*time.Second)
	wide := tm.WaitSettled(10 * time.Second)
	recommendation := regexp.MustCompile(`\bE[23]\b`)
	check := func(screen string) {
		t.Helper()
		if !tm.Alive() || !strings.Contains(screen, "placement-only") || !recommendation.MatchString(screen) {
			t.Fatalf("hint lost its move or limited-policy label:\n%s", screen)
		}
		for _, claim := range []string{"already drawn", "no further play can win", "out for good"} {
			if strings.Contains(screen, claim) {
				t.Fatalf("heuristic advice makes the false terminal claim %q:\n%s", claim, screen)
			}
		}
		tm.AssertFits()
	}
	check(wide)
	check(tm.ResizeAndWait(40, 14, 10*time.Second))
	check(tm.ResizeAndWait(40, 17, 10*time.Second)) // short bottom panel
	check(tm.ResizeAndWait(80, 8, 10*time.Second))  // short side panel
	check(tm.ResizeAndWait(20, 14, 10*time.Second)) // minimum supported width
	check(tm.ResizeAndWait(120, 36, 10*time.Second))
	tm.SendKeys("C-c")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("leaving analysis failed: exited=%v code=%d", exited, code)
	}
	saved, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Record != rec.Encode() || saved.Finished {
		t.Fatal("asking for advice changed or finished the saved game")
	}
}

func TestReplayEntryJumpPreservesRecordAcrossResize(t *testing.T) {
	t.Parallel()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for i, move := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		if i < 5 {
			for range 2 {
				if err := g.OfferDraw(g.Turn()); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := g.PlayNotation(move); err != nil {
			t.Fatal(err)
		}
	}
	if g.Entries() != 17 || g.Ply() != 7 || g.Result().Winner() != game.Vertical {
		t.Fatal("replay fixture no longer separates entries, plies and a connection result")
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := gamestore.NewID()
	if err := store.Put(gamestore.Saved{
		ID: id, Kind: gamestore.Imported, Player: "Vertical", Opponent: "Horizontal",
		Record: record.Encode(), Finished: true,
	}); err != nil {
		t.Fatal(err)
	}
	tm := sessionIn(t, cfg, 120, 30, "game", "replay", id)
	tm.MustWaitFor("step 17 of 17", 20*time.Second)
	end := tm.WaitSettled(10 * time.Second)
	entry := regexp.MustCompile(`>\s*17\s+B6`)
	if !tm.Alive() || !entry.MatchString(end) {
		t.Fatalf("replay does not expose its selected final entry:\n%s", end)
	}
	tm.SendKeys(":")
	tm.WaitChanged(end, 10*time.Second)
	tm.SendKeys("4", "Enter")
	tm.MustWaitFor("step 4 of 17", 10*time.Second)
	middle := tm.WaitSettled(10 * time.Second)
	if !regexp.MustCompile(`>\s*4\s+h:draw\?`).MatchString(middle) ||
		!strings.Contains(middle, "moves played: 1") {
		t.Fatalf("numeric jump counted plies instead of entries:\n%s", middle)
	}
	tm.ResizeAndWait(20, 8, 10*time.Second)
	tm.AssertFits()
	restored := tm.ResizeAndWait(120, 30, 10*time.Second)
	if restored != middle {
		t.Fatalf("replay lost entry or list position after resize\nbefore:\n%s\nafter:\n%s", middle, restored)
	}
	tm.SendKeys(":")
	tm.WaitChanged(restored, 10*time.Second)
	tm.SendText("9999999999999999999999999999999999999999")
	tm.SendKeys("Enter")
	tm.MustWaitFor("range 0..17", 10*time.Second)
	rejected := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(rejected, "step 4 of 17") || !strings.Contains(rejected, "99") {
		t.Fatalf("out-of-range input changed the replay or lost the correctable field:\n%s", rejected)
	}
	tm.SendKeys("Escape")
	cancelled := tm.WaitChanged(rejected, 10*time.Second)
	if cancelled != restored {
		t.Fatalf("cancelling the rejected jump did not restore the prior frame:\n%s", cancelled)
	}
	tm.SendKeys("g")
	tm.MustWaitFor("step 0 of 17", 10*time.Second)
	tm.SendKeys("G")
	tm.MustWaitFor("step 17 of 17", 10*time.Second)
	tm.SendKeys("q")
	code, exited := tm.WaitExit(20 * time.Second)
	if !exited || code != 0 {
		t.Fatalf("replay did not exit cleanly: exited=%v status=%d", exited, code)
	}
	saved, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Record != record.Encode() {
		t.Fatal("read-only replay changed the canonical game record")
	}
}
