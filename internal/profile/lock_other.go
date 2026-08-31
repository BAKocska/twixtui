//go:build !unix

package profile

// lockFile does nothing on platforms without advisory whole-file locking. The
// Store's own mutex still serialises writers inside one process; two twixtui
// processes sharing a configuration directory on such a platform can interleave
// their read-modify-write cycles and lose the loser's update. The release
// targets (macOS and Linux) both take the unix implementation instead.
func lockFile(path string, exclusive bool) (func(), error) {
	return func() {}, nil
}
