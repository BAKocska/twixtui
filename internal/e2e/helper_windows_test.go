//go:build windows

package e2e

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// helperEnableVirtualTerminal makes escape sequences mean something to the
// console this process was given.
//
// A Windows console does not interpret them until it is told to, and what it
// does instead is print them. That matters for exactly one reason here: an
// alternate-screen test whose escape sequence was printed rather than acted on
// still finds the text it was looking for, sitting on the ordinary screen
// beside the escape that should have hidden it. So this is a hard requirement
// of the helper rather than a best effort — a console that will not enable it
// makes the assertion meaningless, and the helper stops instead.
//
// The program under test does not need this: bubbletea negotiates the console
// mode itself. The helper is a plain Go program and has to do it here.
func helperEnableVirtualTerminal() error {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return fmt.Errorf("reading the console mode of standard output: %w", err)
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return nil
	}
	if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return fmt.Errorf("enabling virtual terminal processing on standard output: %w", err)
	}
	return nil
}
