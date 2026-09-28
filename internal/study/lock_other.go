//go:build !unix && !windows

package study

// lockStudy does nothing on a platform with no whole-file locking of its own,
// so two saves of one study there — from two windows of one twixtui or from
// two twixtui processes sharing a configuration directory — can interleave the
// revision check and the write, and the later save can replace the earlier one
// without being refused as stale. The platforms twixtui is built and tested for
// — macOS, Linux and Windows — each have an implementation instead, so this is
// reached only by a build for somewhere nobody has looked yet.
func lockStudy(path string) (func(), error) {
	return func() {}, nil
}
