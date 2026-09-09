//go:build windows

package e2e

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// The tests in this file cover the two pieces of the ConPTY backend that can
// be wrong while everything still runs: what a key name puts on the wire, and
// what the child's environment ends up being. Both fail quietly when they are
// wrong — a key that encodes to nothing looks like a program ignoring input,
// and a duplicated environment entry looks like a program ignoring its
// configuration — so both are asserted here rather than left to be diagnosed
// through a scenario.

// keyEncoding sends one key name through a real emulator and returns the bytes
// it put into the terminal's input, which is what the program would read.
func keyEncoding(t *testing.T, name string) string {
	t.Helper()
	event, err := parseKey(name)
	if err != nil {
		t.Fatalf("parseKey(%q): %v", name, err)
	}
	emulator := vt.NewSafeEmulator(20, 5)
	t.Cleanup(func() { _ = emulator.InputPipe().(io.Closer).Close() })

	// The emulator's input side is an unbuffered pipe, so the read has to be
	// waiting before the key is sent. It also means a key that encodes to
	// nothing does not block, which is exactly the case being looked for: the
	// answer then is the empty string, not a hang.
	sent := make(chan string, 1)
	go func() {
		buffer := make([]byte, 64)
		read, _ := emulator.Read(buffer)
		sent <- string(buffer[:read])
	}()
	emulator.SendKey(event)

	select {
	case sequence := <-sent:
		return sequence
	case <-time.After(2 * time.Second):
		return ""
	}
}

// TestKeyNamesSendWhatATerminalSends pins the bytes each documented key name
// produces. The failure it exists for is a name that encodes to nothing at
// all: the emulator only encodes the modifier combinations a terminal has a
// sequence for, and a combination it does not know is silently dropped, which
// in a scenario looks like the program under test ignoring the key.
func TestKeyNamesSendWhatATerminalSends(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Enter":    "\r",
		"enter":    "\r",
		"Escape":   "\x1b",
		"esc":      "\x1b",
		"Space":    " ",
		"space":    " ",
		"Tab":      "\t",
		"BSpace":   "\x7f",
		"C-c":      "\x03",
		"C-Space":  "\x00",
		"M-x":      "\x1bx",
		"S-Tab":    "\x1b[Z",
		"Up":       "\x1b[A",
		"Down":     "\x1b[B",
		"Right":    "\x1b[C",
		"Left":     "\x1b[D",
		"Home":     "\x1b[H",
		"End":      "\x1b[F",
		"PageUp":   "\x1b[5~",
		"PageDown": "\x1b[6~",
		"Insert":   "\x1b[2~",
		"Delete":   "\x1b[3~",
		"F1":       "\x1bOP",
		"F5":       "\x1b[15~",
		"F12":      "\x1b[24~",
		"g":        "g",
		"G":        "G",
		"S-g":      "G",
		"?":        "?",
		"4":        "4",
	} {
		if got := keyEncoding(t, name); got != want {
			t.Errorf("SendKeys(%q) sent %q, want %q", name, got, want)
		}
	}
}

// TestKeyNamesWithNothingToSendAreRefused is the other half of the same
// property: where there is no sequence to send, saying so is the only honest
// answer. Every name here would otherwise reach the emulator and produce no
// bytes.
func TestKeyNamesWithNothingToSendAreRefused(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"",       // nothing at all
		"Xyzzy",  // not a key name
		"j k",    // literal text, which is what SendText is for
		"C-4",    // control with a digit has no encoding
		"S-4",    // and shift with one depends on the keyboard
		"C-Up",   // control with an arrow has no encoding here
		"M-S-F1", // nor does this
	} {
		if event, err := parseKey(name); err == nil {
			t.Errorf("parseKey(%q) accepted a key it cannot send, as %+v", name, event)
		}
	}
}

// TestEnvironmentMergesRegardlessOfCase covers the Windows-specific part of
// building the child's environment. Names are case-insensitive there, so an
// override has to replace the entry that is already present whatever its case:
// a block with both Term and TERM in it leaves which one the child reads to
// chance, and the harness would be setting a variable it only appears to set.
func TestEnvironmentMergesRegardlessOfCase(t *testing.T) {
	t.Parallel()
	entries := environmentEntries(
		[]string{"Term=dumb", "PARENT_KEPT=yes", `=C:=C:\src`, "MALFORMED"},
		[]string{"TERM=xterm-256color", "NO_COLOR=1"},
	)

	byName := make(map[string]string, len(entries))
	var terms []string
	for _, entry := range entries {
		name, ok := environmentName(entry)
		if !ok {
			t.Errorf("entry %q has no name and should not be in the block", entry)
			continue
		}
		byName[strings.ToUpper(name)] = entry
		if strings.EqualFold(name, "TERM") {
			terms = append(terms, entry)
		}
	}

	if len(terms) != 1 || terms[0] != "TERM=xterm-256color" {
		t.Errorf("TERM entries = %q, want only the one the harness sets", terms)
	}
	if byName["PARENT_KEPT"] != "PARENT_KEPT=yes" {
		t.Errorf("the parent's own environment did not survive: %q", byName["PARENT_KEPT"])
	}
	if byName["NO_COLOR"] != "NO_COLOR=1" {
		t.Errorf("an added entry is missing: %q", byName["NO_COLOR"])
	}
	// Windows keeps a current directory per drive in a variable whose name
	// starts with '='. Dropping those changes where a relative path resolves
	// to in the child.
	if byName["=C:"] != `=C:=C:\src` {
		t.Errorf("the per-drive current directory was lost: %q", byName["=C:"])
	}
	if _, ok := byName["MALFORMED"]; ok {
		t.Error("an entry with no value was passed through")
	}

	// CreateProcess documents the block as sorted by name, case-insensitively.
	for i := 1; i < len(entries); i++ {
		previous, _ := environmentName(entries[i-1])
		current, _ := environmentName(entries[i])
		if strings.ToUpper(previous) > strings.ToUpper(current) {
			t.Errorf("the block is out of order: %q comes before %q", previous, current)
		}
	}
}
