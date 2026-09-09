//go:build windows

package winfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// shareAll lets other openers read, write and delete the file this process has
// open. Deletion is the one that matters: Windows refuses to rename a file over
// another one while a handle that did not allow deletion is open, and every
// store here replaces its files by renaming a new one over the old. Go's
// os.Open asks for read and write sharing only, so a reader holding a file open
// through it makes a concurrent writer's replacement fail for as long as the
// read lasts.
const shareAll = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// nativePath keeps the long-path behavior supplied by os.Open/os.Rename when
// calling Win32 directly. Extended paths must be absolute and normalized;
// existing extended/device prefixes are already in the caller's chosen form.
func nativePath(path string) (*uint16, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if len(absolute) >= 248 && !strings.HasPrefix(absolute, `\\?\`) && !strings.HasPrefix(absolute, `\\.\`) {
		if strings.HasPrefix(absolute, `\\`) {
			absolute = `\\?\UNC\` + absolute[2:]
		} else {
			absolute = `\\?\` + absolute
		}
	}
	return windows.UTF16PtrFromString(absolute)
}

// OpenRead opens path for reading without standing in the way of its
// replacement. The file goes on being readable through the returned handle
// after another writer has renamed a new version over it — Windows keeps a
// file alive until the last handle closes, exactly as unix keeps an unlinked
// inode alive — and the next open sees the new file.
//
// Failures carry the shape os.Open's do, an *os.PathError wrapping the
// underlying error, so callers go on matching os.ErrNotExist.
func OpenRead(path string) (*os.File, error) {
	name, err := nativePath(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// ReadFile returns the whole of path, read through OpenRead. It is os.ReadFile
// for a file somebody else may be replacing.
func ReadFile(path string) ([]byte, error) {
	f, err := OpenRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if fi, err := f.Stat(); err == nil {
		// One allocation for the whole file, with room for the read that
		// reports the end of it. A size this build cannot hold in an int is
		// not a store file, and growing on demand handles it either way.
		if size := fi.Size(); size > 0 && size < 1<<31 {
			buf.Grow(int(size) + bytes.MinRead)
		}
	}
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// replaceAttempts and replaceMaxDelay bound the wait for a destination another
// program has open: eight attempts, doubling from a millisecond and capped, so
// the whole loop gives up after about a tenth of a second. Somebody is waiting
// for the interface at the other end of this, so a replacement that cannot
// happen has to be reported rather than waited out.
const (
	replaceAttempts = 8
	replaceMaxDelay = 32 * time.Millisecond
)

// Replace puts src in dst's place.
//
// MoveFileEx with MOVEFILE_REPLACE_EXISTING is the replacement Windows offers:
// dst is never removed first, so a reader opening it finds the old file or the
// new one and never a delete-then-create gap. MOVEFILE_WRITE_THROUGH requests
// completion of the operation before returning; this is not a claim that every
// filesystem provides POSIX directory-fsync durability.
//
// The replacement retains the source's security descriptor. Callers create
// temporary files in the destination directory, inheriting that directory's
// ACL rather than preserving any separately customized destination-file ACL.
//
// A replacement can fail because something else has dst open without allowing
// deletion: a scanner mid-scan, an editor, an older twixtui build whose reads
// did not allow it. That passes, so it is retried a few times before it is
// reported. Access control does not pass, so a refusal that is not about
// sharing is reported at once. Either way the write fails with dst's previous
// contents intact, which is what unix does when a rename cannot happen.
func Replace(src, dst string) error {
	from, err := nativePath(src)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	to, err := nativePath(dst)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	delay := time.Millisecond
	for attempt := 1; ; attempt++ {
		err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil {
			return nil
		}
		if attempt == replaceAttempts || !shared(err) {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
		}
		time.Sleep(delay)
		if delay < replaceMaxDelay {
			delay *= 2
		}
	}
}

// shared reports a refusal that came from somebody else having the file open,
// which is the transient one: the other program closes its handle and the
// replacement then lands.
func shared(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
