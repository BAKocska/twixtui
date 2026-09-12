//go:build windows

package gamestore

import "github.com/BAKocska/twixtui/internal/winfs"

// lockGame takes a lock covering one game and returns the release function.
// Deciding whether a game may be written means reading the stored one first,
// and that check is only worth anything while it stays true: two windows that
// both finish the same game would otherwise both pass it and the second write
// would replace the first result. Holding this lock across the read and the
// write makes the pair one step, so the second writer reads what the first one
// stored and is refused.
//
// Windows holds the lock per handle, so it serialises two Stores inside one
// process as well as two twixtui processes sharing a configuration directory.
// It is held on a lock file of its own rather than on the game's file, because
// writeFileAtomic replaces that file and a lock on the file that was replaced
// protects nobody. It is per game rather than per store because games are
// written one at a time and unrelated games have no reason to wait for each
// other.
//
// Writing is the only thing locked here. A reader needs no lock: every write
// lands through a replacement, so a reader sees one whole version of a game or
// another, never a half-written one.
func lockGame(path string) (func(), error) {
	return winfs.Lock(path, true)
}
