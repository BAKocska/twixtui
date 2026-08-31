//go:build unix

package leaderboard

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an advisory whole-file lock on path and returns the release
// function. Two twixtui processes sharing a configuration directory serialise
// their read-modify-write cycles through this, which is what stops one finished
// game from overwriting another's result.
//
// The lock is per open file description, so it also works between two Boards
// opened inside one process. It is held on a dedicated lock file rather than on
// the data file, because atomicWrite replaces the data file's inode and a lock
// held on the old inode would no longer be seen by anyone.
func lockFile(path string, exclusive bool) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
