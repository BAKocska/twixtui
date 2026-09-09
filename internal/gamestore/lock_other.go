//go:build !unix && !windows

package gamestore

// lockGame does nothing on a platform with no whole-file locking of its own, so
// two twixtui processes sharing a configuration directory there can interleave
// the check and the write and let the second finish of one game replace the
// first. The platforms twixtui is built and tested for — macOS, Linux and
// Windows — each have an implementation instead, so this is reached only by a
// build for somewhere nobody has looked yet.
func lockGame(path string) (func(), error) {
	return func() {}, nil
}
