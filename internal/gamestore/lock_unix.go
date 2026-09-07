//go:build unix

package gamestore

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockGame takes an advisory lock covering one game and returns the release
// function. Deciding whether a game may be written means reading the stored one
// first, and that check is only worth anything while it stays true: two windows
// that both finish the same game would otherwise both pass it and the second
// write would replace the first result. Holding this lock across the read and
// the write makes the pair one step, so the second writer reads what the first
// one stored and is refused.
//
// The lock is per open file description, so it serialises two Stores inside one
// process as well as two twixtui processes sharing a configuration directory.
// It is held on a lock file of its own rather than on the game's file, because
// writeFileAtomic replaces that file's inode and a lock on the old inode would
// be invisible to the next writer. It is per game rather than per store because
// games are written one at a time and unrelated games have no reason to wait
// for each other.
//
// Writing is the only thing locked here. A reader needs no lock: every write
// lands through a rename, so a reader sees one whole version of a game or
// another, never a half-written one.
func lockGame(path string) (func(), error) {
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
