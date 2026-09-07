//go:build unix

package profile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an advisory whole-file lock on path and returns the release
// function. Two twixtui processes sharing a configuration directory serialise
// their read-modify-write cycles through this, which is what stops the second
// writer from overwriting the first writer's new profile.
//
// The lock is per open file description, so it also works between two Stores
// opened inside one process. It is held on a dedicated lock file rather than on
// the data file, because atomicWrite replaces the data file's inode and a lock
// held on the old inode would no longer be seen by anyone.
//
// A shared lock is taken for reading, and reading must not depend on being able
// to write: a configuration directory can be on a read-only mount, or belong to
// another user who let this one look at it, and listing profiles there is a
// reasonable thing to do. So a shared lock opens the lock file for reading,
// creates it only when the directory allows it, and, where it can be neither
// opened nor created because nothing here may be written, reads without it.
// That last case gives up nothing this lock was protecting: every write lands
// through atomicWrite's rename, so a reader sees one whole version of the file
// or another, never a partial one, and a directory that refuses this process a
// lock file refuses it the write too. An exclusive lock is never softened, so a
// mutation on an unwritable directory still fails, and concurrent writers are
// still serialised.
func lockFile(path string, exclusive bool) (func(), error) {
	f, err := openLockFile(path, exclusive)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return func() {}, nil
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

// openLockFile opens the lock file, or reports that this process may not have
// one at all by returning no file and no error, which only happens for a shared
// lock on a directory nothing may be written to.
func openLockFile(path string, exclusive bool) (*os.File, error) {
	if !exclusive {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			if unwritable(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("opening lock file %s: %w", path, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if !exclusive && unwritable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	return f, nil
}

// unwritable reports the failures that mean "not allowed to write here" rather
// than "something is wrong": a directory or file this process may not write to,
// and a read-only filesystem.
func unwritable(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
}
