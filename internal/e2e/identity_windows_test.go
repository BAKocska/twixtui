//go:build windows

package e2e

import (
	"debug/pe"

	"golang.org/x/sys/windows"
)

// nativeMachineIdentity reports what this process is and what the machine
// underneath it is, as the two machine values a Windows executable header uses.
//
// IsWow64Process2 is the call that answers this: it reports the emulated
// architecture of the process, or IMAGE_FILE_MACHINE_UNKNOWN when the process
// is running natively, and separately the architecture of the machine itself.
// GetNativeSystemInfo answers the second half only, and answers it in a
// different vocabulary; this one call gives both in the vocabulary the rest of
// the check already uses.
func nativeMachineIdentity() (process, native uint16, err error) {
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &process, &native); err != nil {
		return 0, 0, err
	}
	if process == pe.IMAGE_FILE_MACHINE_UNKNOWN {
		// Not emulated: the process is running as the machine's own
		// architecture, which the caller reads from native.
		process = native
	}
	return process, native, nil
}
