package study

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// readDocument returns the contents of the study file at path, or reports that
// there is none: a game nobody has taken notes on has no study yet, and that is
// not an error.
//
// The size is taken from the open file before any of it is read, so a file
// larger than a study may be is refused without being read into memory. The
// read is bounded as well, so a file that grows between the check and the read
// is refused the same way rather than read to its end.
func readDocument(path string) (data []byte, found bool, err error) {
	f, err := openRead(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if fi.Size() > MaxFileBytes {
		return nil, true, fmt.Errorf("%s: %w: it is %d bytes, more than the %d a study may be", path, ErrCorrupt, fi.Size(), MaxFileBytes)
	}
	data, err = io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) > MaxFileBytes {
		return nil, true, fmt.Errorf("%s: %w: it is more than the %d bytes a study may be", path, ErrCorrupt, MaxFileBytes)
	}
	return data, true, nil
}

// atomicWrite replaces path with data, or leaves the previous contents intact.
// The data goes to a temporary file in the same directory, is flushed to the
// device, and is then put in the target's place: a replacement within a
// directory takes effect all at once, so a crash partway through saving a note
// cannot truncate the notes that were already there. What each platform needs
// for that is in replaceFile.
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
	if err := replaceFile(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	name = ""
	syncDir(dir)
	return nil
}

// sweepStaleTemps removes leftovers from a run that died between creating a
// temporary file and renaming it. The caller holds the study's exclusive
// advisory lock, and the prefix names that study's file alone, so no other
// writer can have one of these in flight; the age bound covers the platforms
// where that lock does nothing. Failures are ignored — this is tidying, not
// part of the write.
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
