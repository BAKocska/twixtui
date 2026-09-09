// Package e2e drives the compiled binary inside a real terminal so that
// rendering, key handling and resize behaviour can be asserted on the frames a
// user would actually see.
//
// The terminal itself is whatever the platform really offers. On Unix it is
// tmux, driven as a scriptable terminal; on Windows it is a ConPTY
// pseudoconsole owned by the test process, with the program's output parsed by
// a virtual terminal emulator. Both are real terminals in the sense that
// matters here: the program under test is talking to a pty or a console host,
// not to a pipe, so it turns colour on, enters the alternate screen, and is
// told its size. The platform files carry the facts each backend was built
// around, all of which were established from the platform's own documentation
// and behaviour rather than assumed.
//
// This file holds everything that is the same on both: the API tests use, and
// the waits, which are where a terminal test is won or lost. Every wait polls
// for a condition rather than sleeping for a guessed duration, because a slow
// machine makes a sleep too short, never too long.
package e2e

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Options configures a Terminal.
type Options struct {
	// Width and Height are the initial terminal size in character cells.
	Width, Height int
	// Dir is the working directory for the command.
	Dir string
	// Env holds extra environment entries in KEY=VALUE form. TERM and the
	// colour-related variables are set for you unless you override them.
	Env []string
	// Color leaves colour enabled. By default colour is switched off so that
	// captured frames are stable text.
	Color bool
}

// Terminal is one command running in a real terminal.
//
// Every method reports failure through the test rather than returning an
// error, except the waits that a test may legitimately expect to time out.
type Terminal struct {
	t      *testing.T
	be     backend
	closed bool
}

// backend is the platform's terminal. Exactly one implementation is compiled
// in: the tmux backend on Unix, the ConPTY backend on Windows.
//
// Methods return errors instead of failing the test themselves so that the
// wording of a failure is the same on every platform, and so that the waits
// can keep polling through a transient failure.
type backend interface {
	// resize changes the terminal size, which the program observes.
	resize(width, height int) error
	// size reports the size the program is being told, not the size last
	// requested: the two can differ.
	size() (width, height int, err error)
	// sendKeys sends key names, sendText literal text, and paste one
	// bracketed paste block.
	sendKeys(keys []string) error
	sendText(text string) error
	paste(text string) error
	// capture returns the visible screen as plain text, captureANSI the same
	// screen with the escape sequences that style it. Both trim trailing
	// blanks from each line and drop trailing blank lines.
	capture() (string, error)
	captureANSI() (string, error)
	// alive reports whether the program is still running.
	alive() bool
	// exitStatus returns the program's status, and whether it has exited and
	// the status is known. Those are two events, not one.
	exitStatus() (code int, exited bool)
	// unreapedReport describes the state where the program has demonstrably
	// exited but no status will ever arrive, so that a wait for the status can
	// fail with the reason rather than as a bare timeout. It reports false
	// when the platform cannot get into that state.
	unreapedReport() (report string, unreaped bool)
	// close tears the terminal down: the program and anything it started are
	// killed, and every handle is released.
	close()
}

const (
	// settleQuiet is how long the screen must stay unchanged before a frame is
	// considered finished rendering.
	settleQuiet = 120 * time.Millisecond
	// pollInterval is how often the screen is sampled.
	pollInterval = 25 * time.Millisecond
	// defaultTimeout bounds every wait.
	defaultTimeout = 15 * time.Second
)

// Start launches command in a new terminal of the requested size and returns
// once the program has been started.
//
// command is an argument vector, not a shell command line: command[0] is the
// executable and the rest are passed to it exactly as given, so a path or an
// argument containing spaces needs no quoting by the caller and no shell is
// involved. Pass an absolute path for command[0]: a relative one is resolved
// against a working directory that differs between the platforms, since one
// terminal starts the program itself and the other has the program started
// for it. The Terminal is closed automatically when the test finishes.
func Start(t *testing.T, command []string, opts Options) *Terminal {
	t.Helper()
	requireAvailable(t)
	if len(command) == 0 {
		t.Fatal("Start: command must contain at least the executable")
	}
	if command[0] == "" {
		t.Fatal("Start: command[0] must be the executable")
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		t.Fatalf("Start: width and height must be positive, got %dx%d", opts.Width, opts.Height)
	}

	env := []string{"TERM=xterm-256color"}
	if !opts.Color {
		env = append(env, "NO_COLOR=1")
	} else {
		env = append(env, "COLORTERM=truecolor")
	}
	env = append(env, opts.Env...)

	tm := &Terminal{t: t, be: startBackend(t, command, env, opts)}
	t.Cleanup(tm.Close)
	return tm
}

// must turns a backend failure into a test failure. A backend error means the
// terminal itself misbehaved, which no test can meaningfully continue past.
func (tm *Terminal) must(what string, err error) {
	tm.t.Helper()
	if err != nil {
		tm.t.Fatalf("%s: %v", what, err)
	}
}

// Close tears down the terminal and everything running in it.
func (tm *Terminal) Close() {
	if tm.closed {
		return
	}
	tm.closed = true
	tm.be.close()
}

// Resize changes the terminal size, which the program observes as a resize.
func (tm *Terminal) Resize(width, height int) {
	tm.t.Helper()
	tm.must("resizing the terminal", tm.be.resize(width, height))
}

// Size returns the size the program actually sees. It is not necessarily the
// size passed to Start or Resize, so assertions about wrapping should use this.
func (tm *Terminal) Size() (width, height int) {
	tm.t.Helper()
	width, height, err := tm.be.size()
	tm.must("reading the terminal size", err)
	return width, height
}

// SendKeys sends key names to the program: "Enter", "Escape", "Space", "Tab",
// "BSpace", "Up", "Down", "Left", "Right", "Home", "End", "PageUp",
// "PageDown", "Insert", "Delete", "F1" to "F12", a single printable character,
// and those prefixed with "C-", "M-" or "S-" for control, alt and shift. Names
// are matched without regard to case.
//
// A name that is not one of those is a test failure rather than something sent
// literally: a typo that silently typed its own letters into the program would
// be found as a mysterious assertion failure much later. Use SendText for
// literal text.
func (tm *Terminal) SendKeys(keys ...string) {
	tm.t.Helper()
	tm.must("sending keys", tm.be.sendKeys(keys))
}

// SendText sends literal text, which is what to use for characters a key name
// would claim.
func (tm *Terminal) SendText(text string) {
	tm.t.Helper()
	tm.must("sending text", tm.be.sendText(text))
}

// Paste pastes text the way a terminal does when the user presses the paste
// key: as one bracketed block, not as a run of keystrokes. Programs that read
// pastes as a unit take a different path for it, and a run of SendKeys would
// not exercise that path.
func (tm *Terminal) Paste(text string) {
	tm.t.Helper()
	tm.must("pasting text", tm.be.paste(text))
}

// Capture returns the visible screen as plain text with trailing blanks
// trimmed from each line.
func (tm *Terminal) Capture() string {
	tm.t.Helper()
	screen, err := tm.be.capture()
	tm.must("capturing the screen", err)
	return screen
}

// CaptureANSI returns the visible screen including the escape sequences that
// style it, for assertions where styling is the thing under test.
func (tm *Terminal) CaptureANSI() string {
	tm.t.Helper()
	screen, err := tm.be.captureANSI()
	tm.must("capturing the styled screen", err)
	return screen
}

// Lines returns the visible screen split into lines.
func (tm *Terminal) Lines() []string {
	return strings.Split(tm.Capture(), "\n")
}

// Alive reports whether the program is still running.
func (tm *Terminal) Alive() bool {
	tm.t.Helper()
	return tm.be.alive()
}

// ExitStatus returns the program's exit status, and whether it has exited.
//
// A terminal is dead the moment the program closes it; the exit status arrives
// when the program is reaped, which is a separate event and can come later. In
// that window a backend can see a dead terminal with no status yet, and
// reading that as zero made an immediate `exit 3` report as a clean exit on a
// loaded runner. A dead terminal with no status yet is therefore not "exited"
// here: the caller keeps polling, and the answer it gets is the program's own.
func (tm *Terminal) ExitStatus() (int, bool) {
	tm.t.Helper()
	return tm.be.exitStatus()
}

// WaitExit blocks until the program has exited and its status is known, and
// returns that status. It is the wait to use before asserting on an exit code:
// Alive going false says the terminal closed, which is earlier than the status
// being known, so a caller that polls Alive and then reads ExitStatus once can
// read it in the gap.
//
// A program that has demonstrably exited without its status ever becoming
// available is reported through t.Fatalf with the backend's own view of it,
// rather than as a timeout: that state has a cause, and the cause is in that
// report rather than in the program under test.
func (tm *Terminal) WaitExit(timeout time.Duration) (int, bool) {
	tm.t.Helper()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		if code, exited := tm.be.exitStatus(); exited {
			return code, true
		}
		if !time.Now().Before(deadline) {
			if report, unreaped := tm.be.unreapedReport(); unreaped {
				tm.t.Fatalf("the program exited but no exit status became available within %s\n%s", timeout, report)
			}
			return 0, false
		}
		time.Sleep(pollInterval)
	}
}

// ErrTimeout is returned when a wait gives up.
var ErrTimeout = errors.New("timed out waiting for the terminal")

// WaitFor blocks until the screen contains substr. It returns the screen that
// matched.
func (tm *Terminal) WaitFor(substr string, timeout time.Duration) (string, error) {
	tm.t.Helper()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = tm.Capture()
		if strings.Contains(last, substr) {
			return last, nil
		}
		time.Sleep(pollInterval)
	}
	return last, fmt.Errorf("%w: %q never appeared\n--- last screen ---\n%s", ErrTimeout, substr, last)
}

// MustWaitFor is WaitFor with the error turned into a test failure.
func (tm *Terminal) MustWaitFor(substr string, timeout time.Duration) string {
	tm.t.Helper()
	screen, err := tm.WaitFor(substr, timeout)
	if err != nil {
		tm.t.Fatalf("%v", err)
	}
	return screen
}

// WaitSettled blocks until the screen has stopped changing, and returns it.
// Polling for a stable frame is deterministic where sleeping for a guessed
// duration is not: a slow machine makes a sleep too short, never too long.
func (tm *Terminal) WaitSettled(timeout time.Duration) string {
	tm.t.Helper()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)
	previous := tm.Capture()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		current := tm.Capture()
		if current != previous {
			previous = current
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= settleQuiet {
			return current
		}
	}
	tm.t.Fatalf("screen never settled within %s\n--- last screen ---\n%s", timeout, previous)
	return previous
}

// WaitChanged blocks until the screen differs from previous and has then
// stopped changing, and returns the new screen.
//
// This is the wait to use after anything that should redraw. WaitSettled alone
// is not enough: it returns as soon as the screen has held still for a moment,
// and immediately after a resize the screen is still the old one and perfectly
// still, so a caller can be handed the frame from before the change and assert
// against it. That is not a hypothetical — it is how a genuine recovery from
// the too-small state looked like a failure to recover.
func (tm *Terminal) WaitChanged(previous string, timeout time.Duration) string {
	tm.t.Helper()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tm.Capture() != previous {
			return tm.WaitSettled(time.Until(deadline))
		}
		time.Sleep(pollInterval)
	}
	tm.t.Fatalf("the screen never changed within %s\n--- screen ---\n%s", timeout, tm.Capture())
	return previous
}

// ResizeAndWait changes the size and waits for the program to redraw, which is
// the only safe way to assert on a frame after a resize.
func (tm *Terminal) ResizeAndWait(width, height int, timeout time.Duration) string {
	tm.t.Helper()
	before := tm.Capture()
	tm.Resize(width, height)
	return tm.WaitChanged(before, timeout)
}

// AssertFits checks the invariant every frame must satisfy: no line wider than
// the terminal and no more lines than it has rows. A frame that breaks this
// corrupts the display and is the most common resize bug.
func (tm *Terminal) AssertFits() {
	tm.t.Helper()
	width, height := tm.Size()
	lines := tm.Lines()
	if len(lines) > height {
		tm.t.Errorf("frame has %d lines but the terminal has %d rows", len(lines), height)
	}
	for i, line := range lines {
		if w := visibleWidth(line); w > width {
			tm.t.Errorf("line %d is %d cells wide, terminal is %d: %q", i+1, w, width, line)
		}
	}
}

// visibleWidth counts the cells a captured line occupies. A plain capture
// carries no escape sequences, so counting runes is right, except that wide
// runes occupy two cells.
func visibleWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeCells(r)
	}
	return n
}

// runeCells reports how many terminal cells a rune occupies. Only the ranges
// this project's rendering can produce are treated as wide.
func runeCells(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK radicals through Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // pictographs and emoticons
		r >= 0x1F900 && r <= 0x1F9FF:
		return 2
	}
	return 1
}

// trimScreen puts a captured screen into the shape assertions are written
// against: trailing blanks removed from each line, and the blank lines at the
// bottom of a part-filled screen removed altogether.
func trimScreen(screen string) string {
	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
