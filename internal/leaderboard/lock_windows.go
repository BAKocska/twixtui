//go:build windows

package leaderboard

import "github.com/BAKocska/twixtui/internal/winfs"

// lockFile takes a whole-file lock on path and returns the release function.
// Two twixtui processes sharing a configuration directory serialise their
// read-modify-write cycles through this, which is what stops one finished game
// from overwriting another's result.
//
// Windows holds the lock per handle, so it serialises two Boards opened inside
// one process as well. It is held on a dedicated lock file rather than on the
// results file, because a write replaces that file and a lock on the file that
// was replaced protects nobody. What a shared lock does in a directory nothing
// may be written to, and why an exclusive one still fails there, is in
// winfs.Lock.
func lockFile(path string, exclusive bool) (func(), error) {
	return winfs.Lock(path, exclusive)
}
