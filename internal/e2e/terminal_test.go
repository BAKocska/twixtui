package e2e

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The tests in this file check the harness itself. A terminal harness that
// silently measures nothing is worse than none, so each capability has a test
// that would fail if the capability did not work, not merely a test that runs
// without error.

// sizeReporter is a shell loop that prints the terminal size it sees. It is the
// instrument used to prove a resize really reached the program.
const sizeReporter = `sh -c 'while :; do stty size; sleep 0.1; done'`

func TestCaptureSeesProgramOutput(t *testing.T) {
	tm := Start(t, `sh -c 'echo HELLO-FROM-PROGRAM; sleep 30'`, Options{Width: 60, Height: 20})
	tm.MustWaitFor("HELLO-FROM-PROGRAM", 5*time.Second)
	if !tm.Alive() {
		t.Error("program should still be running")
	}
}

// TestWaitForCanFail is the positive control for WaitFor: if this passes, a
// successful WaitFor elsewhere means something.
func TestWaitForCanFail(t *testing.T) {
	tm := Start(t, `sh -c 'echo something-else; sleep 30'`, Options{Width: 40, Height: 10})
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
	tm := Start(t, `sh -c 'exit 3'`, Options{Width: 40, Height: 10})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !tm.Alive() {
			break
		}
		time.Sleep(pollInterval)
	}
	if tm.Alive() {
		t.Fatal("a program that exited is still reported as alive")
	}
	code, exited := tm.ExitStatus()
	if !exited {
		t.Fatal("ExitStatus does not report the program as exited")
	}
	if code != 3 {
		t.Errorf("exit status = %d, want 3", code)
	}
}

// TestSizeMatchesWhatTheProgramSees checks the harness reports the same size the
// program is told, so width assertions are meaningful.
func TestSizeMatchesWhatTheProgramSees(t *testing.T) {
	tm := Start(t, sizeReporter, Options{Width: 70, Height: 24})
	tm.WaitSettled(5 * time.Second)
	width, height := tm.Size()

	screen := tm.Capture()
	lines := strings.Fields(strings.TrimSpace(strings.Split(strings.TrimSpace(screen), "\n")[0]))
	if len(lines) != 2 {
		t.Fatalf("unexpected stty output %q", screen)
	}
	want := lines[0] + " " + lines[1]
	got := itoa(height) + " " + itoa(width)
	if got != want {
		t.Errorf("harness reports %s (rows cols), program sees %s", got, want)
	}
}

// TestResizeReachesTheProgram is the central positive control for R4. It asserts
// on content that only the new size could produce, so a resize that was never
// delivered fails the test instead of passing quietly.
func TestResizeReachesTheProgram(t *testing.T) {
	tm := Start(t, sizeReporter, Options{Width: 100, Height: 30})
	tm.WaitSettled(5 * time.Second)

	before := tm.Capture()
	beforeW, beforeH := tm.Size()

	tm.Resize(48, 14)
	afterW, afterH := tm.Size()
	if afterW == beforeW && afterH == beforeH {
		t.Fatalf("pane size did not change: still %dx%d", afterW, afterH)
	}

	// The program prints the size it was told, so waiting for the new numbers
	// proves SIGWINCH arrived and the program observed it.
	want := itoa(afterH) + " " + itoa(afterW)
	screen, err := tm.WaitFor(want, 5*time.Second)
	if err != nil {
		t.Fatalf("the program never reported the new size %q after resize\nbefore:\n%s\nafter:\n%s", want, before, screen)
	}
}

// TestResizeSmallerThenBackRestoresSize covers the shrink-and-regrow cycle a
// herdr side pane produces.
func TestResizeSmallerThenBackRestoresSize(t *testing.T) {
	tm := Start(t, sizeReporter, Options{Width: 90, Height: 28})
	tm.WaitSettled(5 * time.Second)
	originalW, originalH := tm.Size()

	tm.Resize(40, 12)
	shrunkW, shrunkH := tm.Size()
	if shrunkW >= originalW {
		t.Fatalf("shrink did not reduce width: %d then %d", originalW, shrunkW)
	}
	tm.MustWaitFor(itoa(shrunkH)+" "+itoa(shrunkW), 5*time.Second)

	tm.Resize(90, 28)
	regrownW, regrownH := tm.Size()
	if regrownW != originalW || regrownH != originalH {
		t.Errorf("size after regrow = %dx%d, want %dx%d", regrownW, regrownH, originalW, originalH)
	}
	tm.MustWaitFor(itoa(regrownH)+" "+itoa(regrownW), 5*time.Second)
}

// TestAlternateScreenIsCaptured proves captures see a full-screen program's
// output. Scrollback captures do not contain alternate-screen output, so a
// harness that read scrollback would show an empty screen for every TUI.
func TestAlternateScreenIsCaptured(t *testing.T) {
	prog := `sh -c 'printf "\033[?1049h\033[H"; printf "INSIDE-ALT-SCREEN"; sleep 30'`
	tm := Start(t, prog, Options{Width: 50, Height: 12})
	tm.MustWaitFor("INSIDE-ALT-SCREEN", 5*time.Second)
}

func TestSendTextAndKeys(t *testing.T) {
	tm := Start(t, `sh -c 'read line; echo "GOT:[$line]"; sleep 30'`, Options{Width: 50, Height: 12})
	tm.WaitSettled(3 * time.Second)
	tm.SendText("hjkl")
	tm.SendKeys("Enter")
	tm.MustWaitFor("GOT:[hjkl]", 5*time.Second)
}

// TestWaitSettledReturnsTheFinalFrame checks the settle detector waits for output
// to stop rather than sampling mid-render.
func TestWaitSettledReturnsTheFinalFrame(t *testing.T) {
	prog := `sh -c 'for i in 1 2 3 4 5; do echo step-$i; sleep 0.08; done; echo FINAL; sleep 30'`
	tm := Start(t, prog, Options{Width: 40, Height: 15})
	screen := tm.WaitSettled(10 * time.Second)
	if !strings.Contains(screen, "FINAL") {
		t.Errorf("settled frame does not contain the last output:\n%s", screen)
	}
}

func TestEnvironmentIsPassedThrough(t *testing.T) {
	tm := Start(t, `sh -c 'echo "VAL=$TWIXTUI_TEST_VAR"; sleep 30'`,
		Options{Width: 40, Height: 10, Env: []string{"TWIXTUI_TEST_VAR=marker-9137"}})
	tm.MustWaitFor("VAL=marker-9137", 5*time.Second)
}

func TestNoColorIsSetByDefault(t *testing.T) {
	tm := Start(t, `sh -c 'echo "NC=[$NO_COLOR]"; sleep 30'`, Options{Width: 40, Height: 10})
	tm.MustWaitFor("NC=[1]", 5*time.Second)
}

func TestVisibleWidth(t *testing.T) {
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
