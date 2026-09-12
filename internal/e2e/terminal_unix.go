//go:build !windows

// This file is the Unix terminal: tmux, driven as a scriptable terminal. Two
// tmux facts shape it and were verified empirically rather than assumed:
//
//   - resize-window changes the size of a detached session's pseudo-terminal
//     and the process receives SIGWINCH. resize-pane does not: on a window
//     with a single pane it silently does nothing, so a resize test built on
//     it would pass while never resizing anything.
//   - capture-pane without a scrollback range captures the visible screen,
//     which for a full-screen program is the alternate screen buffer.
//     Scrollback captures do not contain alternate-screen output at all.
//
// Every Terminal runs on its own tmux server socket, so a test can never
// disturb a tmux session the user is working in, and the server's paste buffer
// belongs to that one terminal.

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

var serverSeq atomic.Int64

// placeholderCommand keeps the session alive while the options are applied. It
// must not exit and must not draw anything.
const placeholderCommand = "sh -c 'while :; do sleep 3600; done'"

// tmuxTerminal is a terminal hosted by a private tmux server.
type tmuxTerminal struct {
	socket string
}

// Available reports whether tmux is usable, so tests can skip cleanly rather
// than fail on a machine without it.
func Available() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH: %w", err)
	}
	return nil
}

// requireAvailable skips the test when there is no terminal to run it in.
// tmux is not part of a Unix installation, so a machine without it is a
// machine that cannot run these tests, not a failure.
func requireAvailable(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		t.Skipf("skipping terminal test: %v", err)
	}
}

func startBackend(t *testing.T, command, env []string, opts Options) backend {
	t.Helper()
	tm := &tmuxTerminal{socket: fmt.Sprintf("twixtui-e2e-%d-%d", os.Getpid(), serverSeq.Add(1))}

	// The session is created running a placeholder that simply waits, so that
	// the options can be applied before the command under test starts.
	// Setting them afterwards is a race: a command that exits immediately
	// takes the window with it and remain-on-exit is never applied, which is
	// exactly the case the exit-status assertions need.
	args := []string{"new-session", "-d", "-s", "main",
		"-x", strconv.Itoa(opts.Width), "-y", strconv.Itoa(opts.Height)}
	if opts.Dir != "" {
		args = append(args, "-c", opts.Dir)
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, placeholderCommand)
	if _, err := tm.run(args...); err != nil {
		t.Fatalf("starting the tmux session: %v", err)
	}

	// The status line steals a row and confuses size assertions; keeping a
	// dead pane is what lets a program's exit status be read.
	for _, set := range [][]string{
		{"set-option", "-g", "status", "off"},
		{"set-option", "-g", "remain-on-exit", "on"},
	} {
		if _, err := tm.run(set...); err != nil {
			t.Fatalf("configuring tmux: %v", err)
		}
	}
	// Applying status off changes the usable height, so restate the size
	// before the program starts and sees it.
	if err := tm.resize(opts.Width, opts.Height); err != nil {
		t.Fatalf("sizing the tmux window: %v", err)
	}

	respawn := []string{"respawn-pane", "-k", "-t", "main"}
	if opts.Dir != "" {
		respawn = append(respawn, "-c", opts.Dir)
	}
	for _, e := range env {
		respawn = append(respawn, "-e", e)
	}
	respawn = append(respawn, tmuxCommand(command)...)
	if _, err := tm.run(respawn...); err != nil {
		t.Fatalf("starting the command under test: %v", err)
	}
	return tm
}

// tmuxCommand renders an argument vector as tmux arguments.
//
// tmux takes `shell-command [argument ...]`, and arguments given separately
// arrive at the program exactly as given: an argument containing spaces stays
// one argument and nothing re-splits it. A lone argument is the one form tmux
// hands to the shell, so it is quoted; that is the case of a bare executable
// path, where quoting is the whole of what is needed.
func tmuxCommand(argv []string) []string {
	if len(argv) > 1 {
		return argv
	}
	return []string{shellQuote(argv[0])}
}

// shellQuote wraps s so that a POSIX shell reproduces it verbatim. Single
// quotes protect everything except a single quote, which has to leave the
// quoting, emit an escaped quote, and re-enter it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tmux runs a tmux command against this terminal's own server and returns its
// combined output.
func (tm *tmuxTerminal) tmux(args ...string) (string, error) {
	full := append([]string{"-L", tm.socket}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	return string(out), err
}

// run is tmux with the command and its output folded into the error, because
// tmux says why it refused on stderr and that message is the diagnosis.
func (tm *tmuxTerminal) run(args ...string) (string, error) {
	out, err := tm.tmux(args...)
	if err != nil {
		return out, fmt.Errorf("tmux %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return out, nil
}

func (tm *tmuxTerminal) close() {
	_, _ = tm.tmux("kill-server")
}

// resize changes the terminal size, which delivers SIGWINCH to the program.
func (tm *tmuxTerminal) resize(width, height int) error {
	_, err := tm.run("resize-window", "-t", "main",
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	return err
}

// size returns the size tmux reports for the pane, which is the size the
// program was told. tmux clamps a size it cannot give, so this is not
// necessarily the size that was asked for.
func (tm *tmuxTerminal) size() (int, int, error) {
	out, err := tm.run("display-message", "-p", "-t", "main", "#{pane_width} #{pane_height}")
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected size report %q", out)
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("unexpected size report %q", out)
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("unexpected size report %q", out)
	}
	return width, height, nil
}

// sendKeys sends key names. tmux key names are the names this harness
// documents, so they are passed straight through, and tmux rejects one it does
// not know rather than typing it.
func (tm *tmuxTerminal) sendKeys(keys []string) error {
	_, err := tm.run(append([]string{"send-keys", "-t", "main"}, keys...)...)
	return err
}

func (tm *tmuxTerminal) sendText(text string) error {
	_, err := tm.run("send-keys", "-t", "main", "-l", text)
	return err
}

// paste puts the text in this server's buffer and pastes it with -p, which
// brackets it if the program asked for bracketed paste.
func (tm *tmuxTerminal) paste(text string) error {
	if _, err := tm.run("set-buffer", "--", text); err != nil {
		return err
	}
	_, err := tm.run("paste-buffer", "-p", "-t", "main")
	return err
}

func (tm *tmuxTerminal) capture() (string, error) {
	out, err := tm.run("capture-pane", "-p", "-t", "main")
	if err != nil {
		return "", err
	}
	return trimScreen(out), nil
}

func (tm *tmuxTerminal) captureANSI() (string, error) {
	out, err := tm.run("capture-pane", "-p", "-e", "-t", "main")
	if err != nil {
		return "", err
	}
	return trimScreen(out), nil
}

func (tm *tmuxTerminal) alive() bool {
	out, err := tm.tmux("display-message", "-p", "-t", "main", "#{pane_dead}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "0"
}

// exitStatus returns the program's exit status, and whether it has exited.
//
// A program ended by a signal has no exit status; tmux reports the signal
// instead, and that is returned the way a shell would, as 128 plus the number.
//
// tmux built with utempter, which is how Debian and Ubuntu package it, can
// leave the pane's process a zombie without ever reaping it (tmux issue 4559).
// The program has exited then, and the only place its status still exists is
// the kernel's record of the zombie, which Linux exposes in /proc. That is
// read as a last resort; where /proc does not exist the situation has not been
// seen.
func (tm *tmuxTerminal) exitStatus() (int, bool) {
	dead, status, signal, ok := tm.deathReport()
	if !ok || !dead {
		return 0, false
	}
	if code, err := strconv.Atoi(status); err == nil {
		return code, true
	}
	if sig, err := strconv.Atoi(signal); err == nil {
		return 128 + sig, true
	}
	if pid, err := tm.tmux("display-message", "-p", "-t", "main", "#{pane_pid}"); err == nil {
		if code, ok := zombieStatus(strings.TrimSpace(pid)); ok {
			return code, true
		}
	}
	return 0, false
}

// unreapedReport describes a pane that is dead while tmux still has no status
// for it: the child was not reaped, and the reason is in tmux's own view of the
// pane and in the process table.
func (tm *tmuxTerminal) unreapedReport() (string, bool) {
	dead, status, signal, ok := tm.deathReport()
	if !ok || !dead {
		return "", false
	}
	pid, _ := tm.tmux("display-message", "-p", "-t", "main", "#{pane_pid}")
	pid = strings.TrimSpace(pid)
	ps, _ := exec.Command("ps", "-o", "pid,ppid,stat,comm", "-p", pid).CombinedOutput()
	return fmt.Sprintf("tmux reports the pane dead with status=%q signal=%q pane_pid=%s\n%s",
		status, signal, pid, ps), true
}

// deathReport reads what tmux knows about the pane's process: whether the pane
// is dead, and the exit status or signal once the child has been reaped.
func (tm *tmuxTerminal) deathReport() (dead bool, status, signal string, ok bool) {
	out, err := tm.tmux("display-message", "-p", "-t", "main",
		"#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}")
	if err != nil {
		return false, "", "", false
	}
	parts := strings.SplitN(strings.TrimSpace(out), ":", 3)
	if len(parts) != 3 {
		return false, "", "", false
	}
	return parts[0] == "1", parts[1], parts[2], true
}

// zombieStatus reads the wait status of an exited but unreaped process from
// /proc/<pid>/stat, whose last field is the exit code in waitpid form. It
// reports false unless the process is a zombie: a running process has no exit
// code yet, and a reaped one has no /proc entry.
func zombieStatus(pid string) (int, bool) {
	raw, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return 0, false
	}
	// The command name is in parentheses and may contain spaces, so the
	// fields are read from after the closing parenthesis.
	rest := string(raw)
	if i := strings.LastIndexByte(rest, ')'); i >= 0 {
		rest = rest[i+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 || fields[0] != "Z" {
		return 0, false
	}
	wait, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return 0, false
	}
	if sig := wait & 0x7f; sig != 0 {
		return 128 + sig, true
	}
	return (wait >> 8) & 0xff, true
}
