//go:build windows

package winfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

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
func nativePath(path string) ([]uint16, error) {
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
	return windows.UTF16FromString(absolute)
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
	h, err := windows.CreateFile(&name[0], windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
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

// replaceAttempts and replaceMaxDelay bound retries for explicit sharing or lock
// violations: eight attempts, doubling from a millisecond and capped, so the
// whole loop gives up after about a tenth of a second. Other refusals return
// immediately; Windows does not identify every held-reader refusal as a sharing
// violation.
const (
	replaceAttempts = 8
	replaceMaxDelay = 32 * time.Millisecond
)

// Replace puts src in dst's place.
//
// FileRenameInfoEx with POSIX semantics replaces the directory entry without
// first deleting dst. Unlike MoveFileEx, it permits existing delete-sharing
// readers to keep their old version while new opens see the replacement.
// Filesystems without POSIX rename use ordinary rename, still without deleting
// dst first, but a held reader can prevent replacement on those filesystems.
// Callers sync the prepared contents before replacement. This operation does
// not supply the crash-durability guarantee of a POSIX directory fsync.
//
// The replacement retains the source's security descriptor. Callers create
// temporary files in the destination directory, inheriting that directory's
// ACL rather than preserving any separately customized destination-file ACL.
//
// Explicit sharing and lock violations are retried briefly. Windows can also
// report a destination held open without delete sharing as ERROR_ACCESS_DENIED,
// indistinguishable from an access-control refusal. Those errors return at once
// rather than retrying permission failures. Either way a refused replacement
// leaves dst's previous contents intact.
//
// https://learn.microsoft.com/windows/win32/api/winbase/ns-winbase-file_rename_info
// https://learn.microsoft.com/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information
func Replace(src, dst string) error {
	from, err := nativePath(src)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	to, err := nativePath(dst)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	h, err := windows.CreateFile(&from[0], windows.DELETE, shareAll, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	defer windows.CloseHandle(h)

	// FILE_RENAME_INFO has a variable-length UTF-16 tail; the native HANDLE
	// field determines alignment on both supported Windows architectures.
	type renameInfo struct {
		flags  uint32
		root   windows.Handle
		length uint32
		name   [1]uint16
	}
	var header renameInfo
	nameLen := len(to) - 1 // FileNameLength excludes the terminating NUL.
	// The Win32 wrapper converts a NUL-terminated path even though the native
	// FileNameLength field excludes that NUL. Reserve and copy both.
	buf := make([]byte, int(unsafe.Offsetof(header.name))+len(to)*2)
	info := (*renameInfo)(unsafe.Pointer(&buf[0]))
	info.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.length = uint32(nameLen * 2)
	copy(unsafe.Slice(&info.name[0], len(to)), to)

	delay := time.Millisecond
	class := uint32(windows.FileRenameInfoEx)
	for attempt := 1; ; attempt++ {
		err := windows.SetFileInformationByHandle(h, class, &buf[0], uint32(len(buf)))
		if class == windows.FileRenameInfoEx && (errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
			errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_INVALID_FUNCTION)) {
			// FAT and other filesystems may not implement POSIX rename.
			// Retrying ordinary rename must not weaken access-control errors.
			class = windows.FileRenameInfo
			info.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS
			err = windows.SetFileInformationByHandle(h, class, &buf[0], uint32(len(buf)))
		}
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

// shared reports explicit sharing or lock violations. Access-denied errors are
// ambiguous (a held reader or permissions) and deliberately not retried.
func shared(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
