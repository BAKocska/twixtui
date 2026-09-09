//go:build windows

package winfs

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// allBytes is the largest range LockFileEx can be given, which is how a
// whole-file lock is spelled here: the lock covers every offset a file could
// ever have, so two callers always contend even though the lock file is empty.
const allBytes = ^uint32(0)

// The synchronization file must keep its identity while locked. Only data-file
// readers share deletion; no caller needs to replace or delete a live lock file.
const shareLock = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE

// Lock takes a whole-file lock on the lock file at path and returns the
// function that releases it.
//
// Windows holds byte-range locks per handle rather than per process, so this
// serialises two stores opened inside one twixtui as well as two twixtui
// processes sharing a configuration directory — the same guarantee flock gives
// on unix. It waits for a lock somebody else holds rather than failing: a
// writer that finds another writer in the middle of a read-modify-write cycle
// has to follow it, not abandon the write.
//
// A shared lock must not depend on being able to write, because reading a
// configuration directory this user may not write to — somebody else's, or one
// on read-only media — is a reasonable thing to do. So a shared lock asks only
// for read access, creates the lock file only where the directory allows it,
// and reads without a lock where the file can be neither opened nor created
// because nothing here may be written. That gives up nothing the lock was
// protecting: every write lands through Replace, so a reader sees one whole
// version of a file or another, and a directory that refuses this process a
// lock file refuses it the write too. An exclusive lock is never softened, so a
// mutation there still fails.
//
// Anything else — most importantly another program holding the lock file open
// in a way that excludes this one — is reported as an error rather than
// quietly skipped. A lock that silently failed to be taken is worse than a
// write that fails and says why.
func Lock(path string, exclusive bool) (func(), error) {
	h, err := openLockFile(path, exclusive)
	if err != nil {
		return nil, err
	}
	if h == windows.InvalidHandle {
		return func() {}, nil
	}
	var flags uint32
	if exclusive {
		flags = windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	// Without LOCKFILE_FAIL_IMMEDIATELY this waits for the range to come free.
	// The handle is a synchronous one, so the wait happens here rather than
	// through the overlapped structure, which is needed only to carry the
	// offset the lock starts at.
	if err := windows.LockFileEx(h, flags, 0, allBytes, allBytes, new(windows.Overlapped)); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() {
		// Closing the handle would drop the lock on its own; unlocking first
		// keeps the two steps separate, so the lock is gone before anything
		// else can go wrong with the close.
		windows.UnlockFileEx(h, 0, allBytes, allBytes, new(windows.Overlapped))
		windows.CloseHandle(h)
	}, nil
}

// openLockFile opens the lock file, or reports that this process may not have
// one at all by returning no handle and no error, which happens only for a
// shared lock in a directory nothing may be written to.
//
// The handle is not inheritable — passing no security attributes is what makes
// that so — because twixtui starts child processes, and a copy of this handle
// in a child that knows nothing about it would keep the lock file open behind
// the store's back.
func openLockFile(path string, exclusive bool) (windows.Handle, error) {
	name, err := nativePath(path)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if !exclusive {
		h, err := windows.CreateFile(name, windows.GENERIC_READ, shareLock, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			if unwritable(err) {
				return windows.InvalidHandle, nil
			}
			return windows.InvalidHandle, fmt.Errorf("opening lock file %s: %w", path, err)
		}
	}
	// An exclusive lock is taken in order to write, so it asks for write access
	// and fails here when the file or the directory denies it, rather than
	// after the caller has prepared the new contents.
	access := uint32(windows.GENERIC_READ)
	if exclusive {
		access |= windows.GENERIC_WRITE
	}
	// Create a missing lock file only after a shared read-open found it absent.
	h, err := windows.CreateFile(name, access, shareLock, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if !exclusive && unwritable(err) {
			return windows.InvalidHandle, nil
		}
		return windows.InvalidHandle, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	return h, nil
}

// unwritable reports the refusals that mean "nothing may be written here"
// rather than "something is wrong": access control that denies this user, a
// file carrying the read-only attribute, and read-only media.
func unwritable(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, windows.ERROR_WRITE_PROTECT) ||
		errors.Is(err, windows.ERROR_FILE_READ_ONLY)
}
