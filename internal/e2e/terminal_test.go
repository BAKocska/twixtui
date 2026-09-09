package e2e

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// The tests in this file check the harness itself. A terminal harness that
// silently measures nothing is worse than none, so each capability has a test
// that would fail if the capability did not work, not merely a test that runs
// without error.
//
// The programs they drive are modes of this test binary, described in
// helper_test.go. They run identically on every platform the harness supports,
// which is what lets the same positive controls be the evidence on Unix and on
// Windows rather than a Unix-only suite with a Windows-shaped hole in it.

// Every test in this package calls t.Parallel, here and in the files beside
// this one. It is safe because nothing is shared: each test gets its own
// terminal, and the ones that drive the binary get their own configuration
// directory too. It is worth doing because these tests wait far more than they
// compute -- for a terminal to settle, for a program to print -- so they
// overlap almost perfectly. Run one at a time the package takes 39 seconds; run
// together it takes 5, and 9 on a machine with two cores. Thirty seconds is the
// difference between a suite people run before pushing and one they do not.

// startupTimeout bounds the wait for a freshly started program's first output.
// It is generous because that wait covers process creation, which on a loaded
// Windows runner is far slower than anything the program then does.
const startupTimeout = 20 * time.Second

func TestCaptureSeesProgramOutput(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 60, Height: 20}, "output", "HELLO-FROM-PROGRAM")
	tm.MustWaitFor("HELLO-FROM-PROGRAM", startupTimeout)
	if !tm.Alive() {
		t.Error("program should still be running")
	}
}

// TestWaitForCanFail is the positive control for WaitFor: if this passes, a
// successful WaitFor elsewhere means something.
//
// The program's own output is waited for first. Without that the short wait
// below could time out because nothing had started yet, which would pass this
// test for the wrong reason and would say nothing about a string that is never
// printed.
func TestWaitForCanFail(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 40, Height: 10}, "output", "something-else")
	tm.MustWaitFor("something-else", startupTimeout)

	_, err := tm.WaitFor("THIS-STRING-IS-NEVER-PRINTED", 700*time.Millisecond)
	if err == nil {
		t.Fatal("WaitFor returned success for a string the program never printed")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("error = %v, want a timeout", err)
	}
	if !strings.Contains(err.Error(), "something-else") {
		t.Error("the failure message should include the screen, to make failures diagnosable")
	}
}

// TestDetectsImmediateExit is the positive control against the commonest vacuous
// pass: a suite that succeeds because the program under test died at once and
// every assertion was made against an empty screen.
func TestDetectsImmediateExit(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 40, Height: 10}, "exit", "3")
	code, exited := tm.WaitExit(startupTimeout)
	if !exited {
		t.Fatal("ExitStatus never reported the program as exited")
	}
	if tm.Alive() {
		t.Fatal("a program that exited is still reported as alive")
	}
	if code != 3 {
		t.Errorf("exit status = %d, want 3", code)
	}
}

// TestSizeMatchesWhatTheProgramSees checks the harness reports the same size the
// program is told, so width assertions are meaningful.
//
// The program's first reading is waited for before the screen is allowed to
// settle. A screen that has not been written to yet is perfectly still, so
// settling on it would compare the harness's numbers against nothing at all.
func TestSizeMatchesWhatTheProgramSees(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 70, Height: 24}, "size-report")
	tm.MustWaitFor(helperSizePrefix, startupTimeout)
	screen := tm.WaitSettled(10 * time.Second)
	width, height := tm.Size()

	rows, cols, ok := helperLastSize(screen)
	if !ok {
		t.Fatalf("no size reading on screen:\n%s", screen)
	}
	if rows != height || cols != width {
		t.Errorf("harness reports %d rows and %d columns, program sees %d and %d", height, width, rows, cols)
	}
}

// awaitSize waits until the size the program most recently read is the given
// one.
//
// The most recent reading is the assertion, not the presence of the text
// anywhere on the screen. Growing a terminal back to a size it held before
// brings the lines it printed at that size back into view with it, out of the
// terminal's own history, so a wait for that text can be satisfied by the
// program's past rather than by anything it has just observed. The last reading
// on screen is the one that cannot be old.
func awaitSize(t *testing.T, tm *Terminal, rows, cols int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var screen string
	for time.Now().Before(deadline) {
		screen = tm.Capture()
		if gotRows, gotCols, ok := helperLastSize(screen); ok && gotRows == rows && gotCols == cols {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("the program's latest reading is not %q after %s\n%s", helperSizeLine(rows, cols), timeout, screen)
}

// TestResizeReachesTheProgram is the central positive control for R4. It asserts
// on content that only the new size could produce, so a resize that was never
// delivered fails the test instead of passing quietly.
func TestResizeReachesTheProgram(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 100, Height: 30}, "size-report")
	tm.MustWaitFor(helperSizePrefix, startupTimeout)
	tm.WaitSettled(10 * time.Second)
	beforeW, beforeH := tm.Size()

	tm.Resize(48, 14)
	afterW, afterH := tm.Size()
	if afterW == beforeW && afterH == beforeH {
		t.Fatalf("pane size did not change: still %dx%d", afterW, afterH)
	}

	// The program prints the size it reads from the operating system, so its
	// reading becoming the new one proves the resize arrived and was observed.
	awaitSize(t, tm, afterH, afterW, startupTimeout)
}

// TestResizeSmallerThenBackRestoresSize covers the shrink-and-regrow cycle a
// herdr side pane produces.
func TestResizeSmallerThenBackRestoresSize(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 90, Height: 28}, "size-report")
	tm.MustWaitFor(helperSizePrefix, startupTimeout)
	tm.WaitSettled(10 * time.Second)
	originalW, originalH := tm.Size()

	tm.Resize(40, 12)
	shrunkW, shrunkH := tm.Size()
	if shrunkW >= originalW {
		t.Fatalf("shrink did not reduce width: %d then %d", originalW, shrunkW)
	}
	awaitSize(t, tm, shrunkH, shrunkW, startupTimeout)

	tm.Resize(90, 28)
	regrownW, regrownH := tm.Size()
	if regrownW != originalW || regrownH != originalH {
		t.Errorf("size after regrow = %dx%d, want %dx%d", regrownW, regrownH, originalW, originalH)
	}
	awaitSize(t, tm, regrownH, regrownW, startupTimeout)
}

// TestAlternateScreenIsCaptured proves captures see a full-screen program's
// output. Scrollback captures do not contain alternate-screen output, so a
// harness that read scrollback would show an empty screen for every TUI.
//
// The program prints a marker on the ordinary screen first and switches only
// when told to, and the assertion is that the marker is gone afterwards. Both
// halves are needed: a terminal that printed the escape sequence as text rather
// than acting on it — which is what a Windows console does until it is told
// otherwise — would still show the text inside it, and a test looking only for
// that text would pass having proved nothing.
func TestAlternateScreenIsCaptured(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 50, Height: 12}, "alt-screen")
	tm.MustWaitFor(helperPrimary, startupTimeout)
	tm.SendKeys("Enter")
	tm.MustWaitFor(helperAlt, startupTimeout)
	screen := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(screen, helperAlt) {
		t.Fatalf("the alternate screen's output is not in the capture:\n%s", screen)
	}
	if strings.Contains(screen, helperPrimary) {
		t.Fatalf("the ordinary screen is still on show, so the switch was printed rather than made:\n%s", screen)
	}
	tm.SendKeys("Enter")
	tm.MustWaitFor(helperPrimary, startupTimeout)
	restored := tm.WaitSettled(10 * time.Second)
	if strings.Contains(restored, helperAlt) {
		t.Fatalf("leaving the alternate screen did not restore the primary buffer:\n%s", restored)
	}
}

func TestSendTextAndKeys(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 50, Height: 12}, "readline")
	tm.MustWaitFor(helperReady, startupTimeout)
	tm.SendText("hjkl")
	tm.SendKeys("Enter")
	tm.MustWaitFor("GOT:[hjkl]", startupTimeout)
}

// TestWaitSettledReturnsTheFinalFrame checks the settle detector waits for output
// to stop rather than sampling mid-render.
//
// The gap between steps is derived from settleQuiet rather than written out. It
// was 80ms against a 120ms threshold, a margin of one and a half, which is a race
// and not a test: it held on one machine and failed on a macOS runner the first
// time one ran this suite. At a quarter of the threshold the relationship is
// explicit and cannot drift when either constant is retuned.
//
// The first step is waited for before settling. Process creation can outlast
// the quiet threshold on its own, so a settle started at launch would return
// the blank screen from before the program printed anything and would find no
// fault with a detector that never worked.
func TestWaitSettledReturnsTheFinalFrame(t *testing.T) {
	t.Parallel()
	gap := settleQuiet / 4
	tm := helperStart(t, Options{Width: 40, Height: 15}, "stepped-output",
		strconv.Itoa(int(gap.Milliseconds())), "5")
	tm.MustWaitFor("step-1", startupTimeout)
	screen := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(screen, helperFinal) {
		t.Errorf("settled frame does not contain the last output, so the detector sampled mid-stream:\n%s", screen)
	}
}

func TestEnvironmentIsPassedThrough(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 40, Height: 10, Env: []string{"TWIXTUI_TEST_VAR=marker-9137"}},
		"env", "TWIXTUI_TEST_VAR")
	tm.MustWaitFor("TWIXTUI_TEST_VAR=[marker-9137]", startupTimeout)
}

func TestNoColorIsSetByDefault(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 40, Height: 10}, "env", "NO_COLOR")
	tm.MustWaitFor("NO_COLOR=[1]", startupTimeout)
}

// TestStyledGlyphsAreCapturedBothWays is the positive control for the two
// captures, and for the characters the board is drawn with.
//
// The product's own tests run with colour switched off, so nothing else here
// would notice a terminal that dropped styling on the floor or a capture that
// silently returned the same thing twice. Both matter: CaptureANSI is what an
// assertion about a theme is made against, and Capture is what every other
// assertion is made against, so a Capture leaking escape sequences would break
// them all in ways that read as content bugs.
//
// The glyphs are the ones the board is drawn with. They travel from a Go string
// through the terminal's encoding and back, which on Windows is a different
// path from anywhere else, and a board rendered as question marks is a broken
// board however well the layout works.
func TestStyledGlyphsAreCapturedBothWays(t *testing.T) {
	t.Parallel()
	tm := helperStart(t, Options{Width: 40, Height: 10, Color: true}, "styled")
	tm.MustWaitFor(helperStyled, startupTimeout)
	plain := tm.WaitSettled(10 * time.Second)

	if !strings.Contains(plain, helperGlyphs) {
		t.Fatalf("the board's own glyphs did not survive the terminal:\n%q", plain)
	}
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("the plain capture carries escape sequences, so every assertion made against it is against markup:\n%q", plain)
	}
	styled := tm.CaptureANSI()
	if !strings.Contains(styled, "\x1b[") {
		t.Fatalf("the styled capture has no styling in it, so a theme cannot be asserted on:\n%q", styled)
	}
	if styled == plain {
		t.Fatal("both captures returned the same thing, so one of them is not doing what it says")
	}
	if !strings.Contains(stripSGR(styled), helperGlyphs) {
		t.Errorf("the styled capture lost the glyphs between its escape sequences:\n%q", styled)
	}
	// Decode the captured attributes on every glyph. A reset prefix alone
	// differs from plain text but preserves no foreground colour.
	emulator := vt.NewEmulator(40, 10)
	defer emulator.Close()
	if _, err := emulator.Write([]byte(strings.ReplaceAll(styled, "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}
	colored := 0
	for y := range 10 {
		for x := range 40 {
			cell := emulator.CellAt(x, y)
			if cell == nil || cell.Content == "" || !strings.Contains(helperGlyphs, cell.Content) {
				continue
			}
			if cell.Style.Fg == nil {
				t.Fatalf("glyph %q lost its foreground colour", cell.Content)
			}
			r, g, b, _ := cell.Style.Fg.RGBA()
			if r>>8 != 211 || g>>8 != 54 || b>>8 != 130 {
				t.Fatalf("glyph %q has RGB(%d,%d,%d), want (211,54,130)", cell.Content, r>>8, g>>8, b>>8)
			}
			colored++
		}
	}
	if colored != len([]rune(helperGlyphs)) {
		t.Fatalf("captured %d colored glyphs, want %d", colored, len([]rune(helperGlyphs)))
	}
}

// sgr matches a select-graphic-rendition sequence, which is the styling a
// capture with escapes in it carries over a plain one.
var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// stripSGR removes that styling, leaving the text it was applied to.
func stripSGR(s string) string { return sgr.ReplaceAllString(s, "") }

func TestVisibleWidth(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"":       0,
		"abc":    3,
		"a b":    3,
		"\u4e2d": 2, // a wide CJK rune occupies two cells
		"◯◉":     2, // the geometric shapes used for pegs are single width
		"─│╱╲":   4,
	}
	for in, want := range cases {
		if got := visibleWidth(in); got != want {
			t.Errorf("visibleWidth(%q) = %d, want %d", in, got, want)
		}
	}
}
