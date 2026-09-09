//go:build windows

package winfstest

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// fileDeleteChild is FILE_DELETE_CHILD, the right to remove an entry from a
// directory, which x/sys/windows does not name. FILE_WRITE_DATA and
// FILE_APPEND_DATA are the same bits as FILE_ADD_FILE and
// FILE_ADD_SUBDIRECTORY, which is what those two mean on a directory.
const fileDeleteChild = 0x40

// DenyDirectoryWrites stops this process's own user from creating or removing
// entries in dir, and returns the function that puts the previous access
// control back.
//
// Only those rights are denied, so listing the directory and reading the files
// already in it go on working: that is the situation being covered, a store
// that may be read and not written. Denying anything on the files themselves
// would be beside the point, because a write here lands through a temporary
// file and a rename over the old one, and both of those need permission on the
// directory.
//
// Whether the denial actually bites is for the caller to check rather than
// assume — a process holding a privilege that overrides access control writes
// here anyway — which is why this reports what it did and not whether it
// worked.
func DenyDirectoryWrites(dir string) (func() error, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, fmt.Errorf("reading the access control of %s: %w", dir, err)
	}
	previous, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("reading the access control list of %s: %w", dir, err)
	}
	if previous == nil {
		return nil, fmt.Errorf("%s has no access control list to add an entry to", dir)
	}
	user, err := currentUser()
	if err != nil {
		return nil, err
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(user)
	denied := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
			windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild,
		AccessMode:  windows.DENY_ACCESS,
		Inheritance: windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}
	// An entry that denies is only worth anything where it is read before the
	// entries that allow, and putting an access control list in that order is
	// what SetEntriesInAcl — ACLFromEntries here — is for.
	dacl, err := windows.ACLFromEntries(denied, previous)
	if err != nil {
		return nil, fmt.Errorf("building an access control list for %s: %w", dir, err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return nil, fmt.Errorf("denying writes to %s: %w", dir, err)
	}
	return func() error {
		if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, previous, nil); err != nil {
			return fmt.Errorf("restoring the access control of %s: %w", dir, err)
		}
		return nil
	}, nil
}

// currentUser returns a copy of this process's user on Go's own heap, so that
// pinning it is all it takes to hand it to Windows.
func currentUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("looking up this process's user: %w", err)
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copying this process's user: %w", err)
	}
	return sid, nil
}
