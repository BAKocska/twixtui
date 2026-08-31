// Package e2e drives the compiled binary inside a real terminal so that
// rendering, key handling and resize behaviour can be asserted on the frames a
// user would actually see.
//
// It uses tmux as a scriptable terminal. Two tmux facts shape this package and
// were verified empirically rather than assumed:
//
//   - resize-window changes the size of a detached session's pseudo-terminal and
//     the process receives SIGWINCH. resize-pane does not: on a window with a
//     single pane it silently does nothing, so a resize test built on it would
//     pass while never resizing anything.
//   - capture-pane without a scrollback range captures the visible screen, which
//     for a full-screen program is the alternate screen buffer. Scrollback
//     captures do not contain alternate-screen output at all.
//
// Every Terminal runs on its own tmux server socket, so a test can never
// disturb a tmux session the user is working in.
package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var serverSeq atomic.Int64

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

// Terminal is a tmux-hosted terminal running one command.
type Terminal struct {
	t      *testing.T
	socket string
	dir    string
	env    []string
	closed bool
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

// Available reports whether tmux is usable, so tests can skip cleanly rather
// than fail on a machine without it.
func Available() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH: %w", err)
	}
	return nil
}

// Start launches command in a new terminal of the requested size. The command is
// run through the shell, so it may contain arguments. The Terminal is closed
// automatically when the test finishes.
func Start(t *testing.T, command string, opts Options) *Terminal {
	t.Helper()
	if err := Available(); err != nil {
		t.Skipf("skipping terminal test: %v", err)
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		t.Fatalf("Start: width and height must be positive, got %dx%d", opts.Width, opts.Height)
	}

	tm := &Terminal{
		t:      t,
		socket: fmt.Sprintf("twixtui-e2e-%d-%d", os.Getpid(), serverSeq.Add(1)),
		dir:    opts.Dir,
	}

	env := []string{"TERM=xterm-256color"}
	if !opts.Color {
		env = append(env, "NO_COLOR=1")
	} else {
		env = append(env, "COLORTERM=truecolor")
	}
	env = append(env, opts.Env...)
	tm.env = env

	args := []string{"new-session", "-d", "-s", "main",
		"-x", strconv.Itoa(opts.Width), "-y", strconv.Itoa(opts.Height)}
	if opts.Dir != "" {
		args = append(args, "-c", opts.Dir)
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, command)
	if out, err := tm.tmux(args...); err != nil {
		t.Fatalf("starting tmux session: %v\n%s", err, out)
	}

	// The status line steals a row and confuses size assertions, and a smaller
	// history keeps captures cheap.
	for _, set := range [][]string{
		{"set-option", "-g", "status", "off"},
		{"set-option", "-g", "remain-on-exit", "on"},
	} {
		if out, err := tm.tmux(set...); err != nil {
			t.Fatalf("configuring tmux: %v\n%s", err, out)
		}
	}
	// Applying status off changes the usable height, so restate the size.
	tm.Resize(opts.Width, opts.Height)

	t.Cleanup(tm.Close)
	return tm
}

func (tm *Terminal) tmux(args ...string) (string, error) {
	full := append([]string{"-L", tm.socket}, args...)
	cmd := exec.Command("tmux", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (tm *Terminal) mustTmux(what string, args ...string) string {
	tm.t.Helper()
	out, err := tm.tmux(args...)
	if err != nil {
		tm.t.Fatalf("%s: %v\n%s", what, err, out)
	}
	return out
}

// Close kills the tmux server backing this terminal.
func (tm *Terminal) Close() {
	if tm.closed {
		return
	}
	tm.closed = true
	_, _ = tm.tmux("kill-server")
}

// Resize changes the terminal size, which delivers SIGWINCH to the program.
func (tm *Terminal) Resize(width, height int) {
	tm.t.Helper()
	tm.mustTmux("resize-window",
		"resize-window", "-t", "main", "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
}

// Size returns the size tmux reports for the pane, which is the size the program
// actually sees. It is not necessarily the size passed to Start or Resize, so
// assertions about wrapping should use this.
func (tm *Terminal) Size() (width, height int) {
	tm.t.Helper()
	out := tm.mustTmux("display-message",
		"display-message", "-p", "-t", "main", "#{pane_width} #{pane_height}")
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		tm.t.Fatalf("unexpected size report %q", out)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		tm.t.Fatalf("unexpected size report %q", out)
	}
	return w, h
}

// SendKeys sends key names to the program. Names are tmux key names, so "Enter",
// "Escape", "C-c", "Up" and plain characters all work.
func (tm *Terminal) SendKeys(keys ...string) {
	tm.t.Helper()
	args := append([]string{"send-keys", "-t", "main"}, keys...)
	tm.mustTmux("send-keys", args...)
}

// SendText sends literal text, which is safer than SendKeys for characters tmux
// would interpret as key names.
func (tm *Terminal) SendText(text string) {
	tm.t.Helper()
	tm.mustTmux("send-keys", "send-keys", "-t", "main", "-l", text)
}

// Capture returns the visible screen as plain text with trailing blanks trimmed
// from each line.
func (tm *Terminal) Capture() string {
	tm.t.Helper()
	out := tm.mustTmux("capture-pane", "capture-pane", "-p", "-t", "main")
	return strings.TrimRight(out, "\n")
}

// CaptureANSI returns the visible screen including escape sequences, for
// assertions where styling is the thing under test.
func (tm *Terminal) CaptureANSI() string {
	tm.t.Helper()
	out := tm.mustTmux("capture-pane", "capture-pane", "-p", "-e", "-t", "main")
	return strings.TrimRight(out, "\n")
}

// Lines returns the visible screen split into lines.
func (tm *Terminal) Lines() []string {
	return strings.Split(tm.Capture(), "\n")
}

// Alive reports whether the program is still running.
func (tm *Terminal) Alive() bool {
	tm.t.Helper()
	out, err := tm.tmux("display-message", "-p", "-t", "main", "#{pane_dead}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "0"
}

// ExitStatus returns the program's exit status, and whether it has exited.
func (tm *Terminal) ExitStatus() (int, bool) {
	tm.t.Helper()
	out, err := tm.tmux("display-message", "-p", "-t", "main", "#{pane_dead}:#{pane_dead_status}")
	if err != nil {
		return 0, false
	}
	parts := strings.SplitN(strings.TrimSpace(out), ":", 2)
	if len(parts) != 2 || parts[0] != "1" {
		return 0, false
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, true
	}
	return code, true
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

// visibleWidth counts the cells a captured line occupies. tmux capture-pane
// without -e returns text with no escape sequences, so counting runes is right,
// except that wide runes occupy two cells.
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
