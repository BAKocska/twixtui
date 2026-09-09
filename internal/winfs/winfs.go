// Package winfs holds the Windows filesystem primitives twixtui's stores need
// and the standard library does not offer: a whole-file lock, opening a file
// for reading in a way that does not block its replacement, and replacing a
// file in place.
//
// It is shared by internal/profile, internal/leaderboard and internal/gamestore
// deliberately. The three of them keep their own files in their own shapes, but
// what Windows requires of a lock and of a replacement is the same for all
// three and is not worth writing out three times.
//
// On every other platform this package is empty: only files ending in
// _windows.go carry anything, and only the stores' own _windows.go files refer
// to them, so the portable code never has to know this package exists.
package winfs
