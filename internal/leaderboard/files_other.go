//go:build !windows

package leaderboard

import "os"

// openRead opens the results file for reading. Everywhere but Windows an open
// file is no obstacle to replacing it: the reader keeps the file it opened and
// the next open finds the new one.
func openRead(path string) (*os.File, error) { return os.Open(path) }

// replaceFile puts src in dst's place. Rename within a directory is atomic, so
// a reader sees one whole version of the file or the other.
func replaceFile(src, dst string) error { return os.Rename(src, dst) }

// syncDir flushes a directory entry, which is what makes a replacement durable
// across a power loss. Not every filesystem permits this on a directory handle,
// and the rename has already succeeded either way, so a failure here is not
// worth failing a write over.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
