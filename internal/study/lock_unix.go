//go:build unix

package study

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockStudy takes an exclusive advisory lock covering one game's study and
// returns the release function. A save reads the stored study, compares its
// revision with the one the editor loaded, and writes; the comparison is only
// worth anything while the revision cannot move underneath it. Two windows
// saving at once would otherwise both find the revision they loaded, and the
// second write would replace the first window's note without either of them
// being told. Holding this lock across the read and the write makes the cycle
// one step, so the second writer reads what the first one stored and is
// refused as stale.
//
// The lock is per open file description, so it serialises two Stores inside one
// process as well as two twixtui processes sharing a configuration directory.
// It is held on a lock file of its own rather than on the study's file, because
// atomicWrite replaces that file's inode and a lock on the old inode would be
// invisible to the next writer. It is per study rather than per store because
// studies are saved one at a time and unrelated games have no reason to wait
// for each other.
//
// Saving is the only thing locked here. A reader needs no lock: every save
// lands through a rename, so a reader sees one whole version of a study or
// another, never a half-written one. Loading must not write anything, and
// taking a lock would create the lock file.
func lockStudy(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
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
