//go:build !windows

package e2e

// helperEnableVirtualTerminal has nothing to do where a terminal has always
// interpreted escape sequences.
func helperEnableVirtualTerminal() error { return nil }
