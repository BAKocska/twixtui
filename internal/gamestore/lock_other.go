//go:build !unix

package gamestore

// lockGame does nothing on platforms without advisory whole-file locking, so
// two twixtui processes sharing a configuration directory there can interleave
// the check and the write and let the second finish of one game replace the
// first. The release targets, macOS and Linux, both take the unix
// implementation instead.
func lockGame(path string) (func(), error) {
	return func() {}, nil
}
