// Package winfstest makes a directory refuse to be written to, which is what a
// test needs in order to cover a configuration directory on read-only media or
// belonging to somebody else.
//
// It exists because the unix way of arranging that says nothing on Windows: Go's
// os.Chmod there moves the read-only attribute and nothing else, and a directory
// carrying that attribute still accepts new files. Refusing a write on Windows
// means an entry in the directory's access control list, which takes enough code
// that the three stores should not each carry their own copy.
//
// On every other platform this package is empty.
package winfstest
