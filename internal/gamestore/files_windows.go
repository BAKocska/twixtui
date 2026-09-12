//go:build windows

package gamestore

import (
	"strings"

	"github.com/BAKocska/twixtui/internal/winfs"
)

// Reading and replacing a file need more from Windows than the standard library
// asks for, all of it about a file being replaced while somebody has it open;
// internal/winfs says what and why. A listing reads every stored game without
// a lock, so a save landing in the middle of one used to be the case that
// failed.

// readWholeFile returns the contents of a stored game.
func readWholeFile(path string) ([]byte, error) { return winfs.ReadFile(path) }

// replaceFile puts src in dst's place.
func replaceFile(src, dst string) error { return winfs.Replace(src, dst) }

// reservedName reports an identifier whose files Windows would resolve to a
// device instead of to a file in the store's directory. "con", "prn", "aux",
// "nul" and the com and lpt names are devices wherever they appear, and a game
// saved as one of them would be written to nothing at all and read back as
// nothing, with the player having been told it was saved.
//
// The set is matched here rather than asked of the system. filepath.IsLocal
// hands a name that carries an extension to ntdll, which answers for the
// Windows the store happens to be running on, and a store whose identifiers are
// legal on one Windows and not on the next is worse than one that refuses a
// handful of names everywhere: no game has to be called "nul".
//
// An identifier that reaches here holds only lower-case letters, digits and
// hyphens, so the folding and the cut below are for whoever asks this about
// something else later. Windows reads a name up to its extension and ignores
// trailing spaces, so "con.json" and "con " are the device just as "con" is.
func reservedName(id string) bool {
	base := id
	if cut := strings.IndexAny(base, ".:"); cut >= 0 {
		base = base[:cut]
	}
	base = strings.ToLower(strings.TrimRight(base, " "))
	switch base {
	case "con", "prn", "aux", "nul", "conin$", "conout$", "clock$":
		return true
	}
	// com1 to com9 and lpt1 to lpt9. com0 and com10 are not devices, and
	// refusing them would be refusing an identifier that names a file.
	if len(base) == 4 && base[3] >= '1' && base[3] <= '9' {
		switch base[:3] {
		case "com", "lpt":
			return true
		}
	}
	return false
}
