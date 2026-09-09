//go:build windows

package leaderboard

import (
	"os"

	"github.com/BAKocska/twixtui/internal/winfs"
)

// Reading and replacing a file need more from Windows than the standard library
// asks for, all of it about a file being replaced while somebody has it open;
// internal/winfs says what and why.

// openRead opens the results file for reading.
func openRead(path string) (*os.File, error) { return winfs.OpenRead(path) }

// replaceFile puts src in dst's place.
func replaceFile(src, dst string) error { return winfs.Replace(src, dst) }

// Windows does not offer the directory-fsync operation used on Unix.
// Replace requests write-through; filesystem-specific crash durability is
// not asserted to be equivalent to a successful POSIX directory flush.
func syncDir(dir string) {}
