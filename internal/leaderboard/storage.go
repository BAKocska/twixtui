package leaderboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnvConfigDir names an environment variable that replaces the default
// configuration directory outright (it is used as-is, with no "twixtui"
// component appended). Tests and the end-to-end harness set it so a run never
// touches the results of the person running it.
const EnvConfigDir = "TWIXTUI_CONFIG_DIR"

// DefaultDir returns the directory twixtui keeps player state in.
func DefaultDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvConfigDir)); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config directory: %w", err)
	}
	return filepath.Join(base, "twixtui"), nil
}

// stamp identifies the version of a file that a board has in memory. Reads stat
// the file and reload only when the stamp has moved, so a long-lived UI picks up
// another process's writes without re-reading on every render.
//
// Size and modification time can in principle both repeat, which would leave a
// reader one revision stale until the next change. Writers do not rely on this:
// they reload unconditionally inside the advisory lock, so a missed stamp can
// never lose data, only delay a display refresh.
type stamp struct {
	exists bool
	size   int64
	mod    time.Time
}

func (s stamp) same(other stamp) bool {
	return s.exists == other.exists && s.size == other.size && s.mod.Equal(other.mod)
}

func statStamp(path string) stamp {
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{exists: true, size: fi.Size(), mod: fi.ModTime()}
}

// readFile returns the contents of path, or no contents at all when the file
// does not exist yet. A first run is not an error.
func readFile(path string) ([]byte, stamp, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, stamp{}, nil
	}
	if err != nil {
		return nil, stamp{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, statStamp(path), nil
}

// atomicWrite replaces path with data, or leaves the previous contents intact.
// The data goes to a temporary file in the same directory, is flushed to the
// device, and is then renamed over the target: rename within a directory is
// atomic, so a crash partway through recording a result cannot truncate the
// history that was already there.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	prefix := filepath.Base(path) + ".tmp-"
	sweepStaleTemps(dir, prefix)
	tmp, err := os.CreateTemp(dir, prefix+"*")
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer func() {
		if name != "" {
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flushing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	name = ""
	// Flushing the directory entry makes the rename itself durable across a
	// power loss. Not every filesystem permits fsync on a directory handle,
	// and the rename has already succeeded either way, so a failure here is
	// not worth failing the write over.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// sweepStaleTemps removes leftovers from a run that died between creating a
// temporary file and renaming it. The caller holds the exclusive advisory lock,
// so no other writer can have one in flight; the age bound covers the platforms
// where that lock does nothing. Failures are ignored — this is tidying, not part
// of the write.
func sweepStaleTemps(dir, prefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Minute)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}
