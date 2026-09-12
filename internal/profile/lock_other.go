//go:build !unix && !windows

package profile

// lockFile does nothing on a platform with no whole-file locking of its own.
// The platforms twixtui is built and tested for — macOS, Linux and Windows —
// each have an implementation instead, so this is reached only by a build for
// somewhere nobody has looked yet. There the Store's own mutex still serialises
// writers inside one process, and two twixtui processes sharing a configuration
// directory can interleave their read-modify-write cycles and lose the loser's
// update.
func lockFile(path string, exclusive bool) (func(), error) {
	return func() {}, nil
}
