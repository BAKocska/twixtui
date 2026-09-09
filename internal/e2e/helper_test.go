package e2e

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

// The harness's own tests need programs that do one precisely defined thing to
// a terminal: print and stay, exit at once with a known status, report the size
// they were given, read a line, switch to the alternate screen, produce output
// in timed steps, or say what is in their environment.
//
// They used to be one-line shell scripts built out of sh, stty and perl. That
// is three producer dependencies, none of which a Windows runner has, and it
// made a suite about a terminal partly a suite about a shell: a fork of sleep
// that took longer than the settle threshold was once read as a fault in the
// settle detector. They are now modes of a single Go program with no
// dependencies at all — this test binary, re-executed.
//
// The guard is an environment variable rather than a flag so an ordinary run of
// the package cannot enter helper mode by accident: without it the entry point
// below does nothing.

const (
	// helperEnv is the guard. Only a process started by helperCommand has it.
	helperEnv = "TWIXTUI_E2E_HELPER"
	// helperHold is how long a mode that must stay on screen stays. Every wait
	// in this package is bounded far below it, and the harness kills the
	// terminal when the test ends, so this only bounds a process orphaned by a
	// harness that could not.
	helperHold = 120 * time.Second
	// helperReady is printed by the modes that then wait for input, so a test
	// never has to guess whether the program has reached the read.
	helperReady = "READY-FOR-INPUT"
	// helperPrimary is printed on the ordinary screen and must be gone once the
	// alternate screen is in use. A terminal that wrote the escape sequence out
	// as text instead of acting on it still shows it, which is how an
	// alternate-screen test passes while measuring nothing.
	helperPrimary = "PRIMARY-SCREEN-MARKER"
	// helperAlt is printed after the switch.
	helperAlt = "INSIDE-ALT-SCREEN"
	// helperFinal ends a run of stepped output.
	helperFinal = "FINAL"
	// helperStyled is the plain marker printed before the styled line, so a
	// test can wait for the program without waiting for the thing it measures.
	helperStyled = "STYLED-OUTPUT"
	// helperGlyphs is the run of characters the styled mode draws: the pegs,
	// the links and a hole, which is every kind of non-ASCII rune this
	// program's board is made of. A terminal that mangles them draws a broken
	// board, and on Windows the encoding they travel through is not the one
	// they travel through anywhere else.
	helperGlyphs = "◉◯·─│╱╲"
	// helperSizePrefix begins every line the size reporter prints.
	helperSizePrefix = "size "
)

// helperSizeLine is the one place the size reporter's line is formatted, so an
// assertion cannot drift from what the program prints. Rows come first, as they
// do in stty's output, which this replaced.
func helperSizeLine(rows, cols int) string {
	return fmt.Sprintf("%s%d %d", helperSizePrefix, rows, cols)
}

// helperLastSize reads the most recent complete size line from a captured
// screen. The last one is taken rather than the first: the top of a scrolling
// screen can hold a line that was torn by a capture landing mid-write, and the
// most recent reading is the one a resize assertion is about.
func helperLastSize(screen string) (rows, cols int, ok bool) {
	for _, line := range strings.Split(screen, "\n") {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), helperSizePrefix)
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 {
			continue
		}
		r, errR := strconv.Atoi(fields[0])
		c, errC := strconv.Atoi(fields[1])
		if errR != nil || errC != nil {
			continue
		}
		rows, cols, ok = r, c, true
	}
	return rows, cols, ok
}

// helperCommand builds the argument vector that re-executes this test binary in
// one of the modes below. The executable is this process's own, so there is
// nothing to build, nothing to install and nothing to find on PATH.
func helperCommand(t *testing.T, mode string, args ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating this test binary to re-execute as a helper: %v", err)
	}
	// The run filter is anchored: -test.run matches unanchored by default, and
	// an unanchored pattern would start whatever else happened to be named
	// after it.
	argv := []string{exe, "-test.run=^TestTerminalHelperProcess$", "--", mode}
	return append(argv, args...)
}

// helperStart runs a helper mode in a terminal.
func helperStart(t *testing.T, opts Options, mode string, args ...string) *Terminal {
	t.Helper()
	env := make([]string, 0, len(opts.Env)+1)
	env = append(env, opts.Env...)
	env = append(env, helperEnv+"=1")
	opts.Env = env
	return Start(t, helperCommand(t, mode, args...), opts)
}

// TestTerminalHelperProcess is the helper program. Run as part of the package
// it does nothing; run with the guard set it becomes the mode named by its
// first argument and never returns to the test framework.
func TestTerminalHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		return
	}
	args := flag.Args()
	if len(args) == 0 {
		helperDie("no mode given")
	}
	// Every mode writes to the terminal, and on Windows a console does not
	// interpret escape sequences until it is told to. A helper that could not
	// enable it would print escapes as text, which reads as a pass to any
	// assertion looking for the text beside them, so it stops instead.
	if err := helperEnableVirtualTerminal(); err != nil {
		helperDie("enabling virtual terminal processing: " + err.Error())
	}
	mode, rest := args[0], args[1:]
	switch mode {
	case "output":
		for _, line := range rest {
			fmt.Fprintln(os.Stdout, line)
		}
		helperWait()
	case "exit":
		if len(rest) != 1 {
			helperDie("exit takes one status")
		}
		code, err := strconv.Atoi(rest[0])
		if err != nil {
			helperDie("exit status " + rest[0] + " is not a number")
		}
		os.Exit(code)
	case "size-report":
		helperReportSize()
	case "readline":
		fmt.Fprintln(os.Stdout, helperReady)
		line := helperReadLine()
		fmt.Fprintf(os.Stdout, "GOT:[%s]\n", line)
		helperWait()
	case "alt-screen":
		fmt.Fprintf(os.Stdout, "\x1b[5;1H%s\x1b[1;1H", helperPrimary)
		helperReadLine()
		// Switch to the alternate screen and home the cursor, which is what a
		// full-screen program does on the way in.
		fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[H")
		fmt.Fprintln(os.Stdout, helperAlt)
		helperReadLine()
		fmt.Fprint(os.Stdout, "\x1b[?1049l")
		helperWait()
	case "stepped-output":
		if len(rest) != 2 {
			helperDie("stepped-output takes a gap in milliseconds and a number of steps")
		}
		gap, errGap := strconv.Atoi(rest[0])
		steps, errSteps := strconv.Atoi(rest[1])
		if errGap != nil || errSteps != nil || gap < 0 || steps < 1 {
			helperDie("stepped-output was given " + strings.Join(rest, " "))
		}
		// The gaps are this process sleeping. They were a shell loop running
		// sleep once per step, and on a loaded runner one fork and exec of
		// sleep outlasted the whole quiet threshold, so the stream stopped for
		// reasons of its own in a test about output stopping.
		for i := 1; i <= steps; i++ {
			fmt.Fprintf(os.Stdout, "step-%d\n", i)
			time.Sleep(time.Duration(gap) * time.Millisecond)
		}
		fmt.Fprintln(os.Stdout, helperFinal)
		helperWait()
	case "styled":
		// A plain marker first, so a test waits for the program with an
		// assertion that is not the one under examination. Then the glyphs
		// under one 24-bit foreground colour, closed by a reset: one attribute
		// run, so a capture that keeps styling keeps it as a run rather than
		// as an escape per cell.
		fmt.Fprintln(os.Stdout, helperStyled)
		fmt.Fprintf(os.Stdout, "\x1b[38;2;211;54;130m%s\x1b[0m\n", helperGlyphs)
		helperWait()
	case "duplex":
		state, err := term.MakeRaw(os.Stdin.Fd())
		if err != nil {
			helperDie("raw duplex input: " + err.Error())
		}
		defer term.Restore(os.Stdin.Fd(), state)
		fmt.Fprintln(os.Stdout, "DUPLEX-READY")
		n, err := io.CopyN(os.Stdout, os.Stdin, 1<<20)
		if err != nil || n != 1<<20 {
			helperDie(fmt.Sprintf("duplex copied %d bytes: %v", n, err))
		}
		fmt.Fprintln(os.Stdout, "\r\nDUPLEX-DONE")
		helperWait()
	case "env":
		for _, name := range rest {
			fmt.Fprintf(os.Stdout, "%s=[%s]\n", name, os.Getenv(name))
		}
		helperWait()
	default:
		helperDie("unknown mode " + mode)
	}
}

// helperReportSize prints the size of the terminal the process was given, over
// and over, so that a resize shows up as new output rather than having to be
// asked for.
//
// The size is read from the operating system's own record of the terminal —
// the window size of the pseudo-terminal on Unix, the console screen buffer on
// Windows — which is the same thing the program under test reads. A helper that
// echoed back a size it was told at startup would agree with every assertion
// and prove nothing.
func helperReportSize() {
	file, width, height, err := helperTerminalSize()
	if err != nil {
		helperDie("reading the terminal size: " + err.Error())
	}
	// Bounded by the same hold as every other mode, so a reporter that outlived
	// the terminal it was reporting on does not print for ever.
	deadline := time.Now().Add(helperHold)
	for time.Now().Before(deadline) {
		fmt.Fprintln(os.Stdout, helperSizeLine(height, width))
		time.Sleep(100 * time.Millisecond)
		if width, height, err = term.GetSize(file.Fd()); err != nil {
			helperDie("reading the terminal size: " + err.Error())
		}
	}
	os.Exit(0)
}

// helperTerminalSize finds the handle that answers a size query and returns it
// with the first reading, so every later reading comes from the same handle.
//
// Only the two output handles are tried. Under a Windows pseudo-console the
// size lives on the screen buffer, and standard input is the console's input
// buffer, which the query fails on: a chain that reached it would report a
// failure to read the size as a size of nothing.
func helperTerminalSize() (*os.File, int, int, error) {
	var last error
	for _, file := range []*os.File{os.Stdout, os.Stderr} {
		width, height, err := term.GetSize(file.Fd())
		if err != nil {
			last = err
			continue
		}
		if width <= 0 || height <= 0 {
			last = fmt.Errorf("%s reports a size of %dx%d", file.Name(), width, height)
			continue
		}
		return file, width, height, nil
	}
	if last == nil {
		last = fmt.Errorf("no standard handle is a terminal")
	}
	return nil, 0, 0, last
}

// helperReadLine reads one line from standard input, which is how a test hands
// the helper a cue at a moment of its choosing.
func helperReadLine() string {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		helperDie("reading a line from the terminal: " + err.Error())
	}
	return strings.TrimRight(line, "\r\n")
}

// helperWait keeps the program on screen. It ends by exiting rather than
// returning, so the test framework's own PASS line never lands on a screen a
// test is reading.
func helperWait() {
	time.Sleep(helperHold)
	os.Exit(0)
}

// helperDie reports a helper that could not do what it was asked. The message
// goes to the terminal, where it lands in the capture a failing wait prints, so
// the failure names itself instead of arriving as a timeout.
func helperDie(reason string) {
	fmt.Fprintln(os.Stdout, "HELPER-ERROR: "+reason)
	os.Exit(1)
}
