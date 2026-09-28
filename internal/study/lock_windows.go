//go:build windows

package study

import "github.com/BAKocska/twixtui/internal/winfs"

// lockStudy takes an exclusive lock covering one game's study and returns the
// release function. A save reads the stored study, compares its revision with
// the one the editor loaded, and writes; the comparison is only worth anything
// while the revision cannot move underneath it. Two windows saving at once
// would otherwise both find the revision they loaded, and the second write
// would replace the first window's note without either of them being told.
// Holding this lock across the read and the write makes the cycle one step, so
// the second writer reads what the first one stored and is refused as stale.
//
// Windows holds the lock per handle, so it serialises two Stores inside one
// process as well as two twixtui processes sharing a configuration directory.
// It is held on a lock file of its own rather than on the study's file, because
// atomicWrite replaces that file and a lock on the file that was replaced
// protects nobody. It is per study rather than per store because studies are
// saved one at a time and unrelated games have no reason to wait for each
// other.
//
// Saving is the only thing locked here. A reader needs no lock: every save
// lands through a replacement, so a reader sees one whole version of a study
// or another, never a half-written one. Loading must not write anything, and
// taking a lock would create the lock file.
func lockStudy(path string) (func(), error) {
	return winfs.Lock(path, true)
}
