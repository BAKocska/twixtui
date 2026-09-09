//go:build !windows

package e2e

import "errors"

// nativeMachineIdentity is only asked for on Windows, where a binary for one
// architecture may be run by a machine of another. It is here so the identity
// test compiles everywhere.
func nativeMachineIdentity() (process, native uint16, err error) {
	return 0, 0, errors.New("the machine's own architecture is only read on Windows")
}
