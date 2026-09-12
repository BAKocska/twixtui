//go:build !windows

package gamestore

import "os"

// readWholeFile returns the contents of a stored game. Everywhere but Windows
// an open file is no obstacle to replacing it: the reader keeps the file it
// opened and the next open finds the new one.
func readWholeFile(path string) ([]byte, error) { return os.ReadFile(path) }

// replaceFile puts src in dst's place. Rename within a directory is atomic, so
// a reader sees one whole version of the game or the other.
func replaceFile(src, dst string) error { return os.Rename(src, dst) }

// reservedName reports an identifier that would name something other than a
// file. Only Windows has such names; everywhere else a file called "nul" is a
// file called "nul".
func reservedName(id string) bool { return false }
