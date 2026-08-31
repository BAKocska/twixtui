package profile

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameRunes bounds a profile name. Thirty-two characters is long enough for
// a full name with spaces and short enough that a list of profiles still fits
// in a narrow terminal pane alongside its rating.
const MaxNameRunes = 32

// Name rejection reasons. Callers match these with errors.Is to report the
// specific problem rather than a generic "invalid name".
var (
	ErrNameEmpty     = errors.New("name is empty")
	ErrNameTooLong   = errors.New("name is too long")
	ErrNamePadded    = errors.New("name has leading or trailing whitespace")
	ErrNameControl   = errors.New("name contains a control character")
	ErrNameInvisible = errors.New("name contains an invisible or bidirectional control character")
	ErrNameNotUTF8   = errors.New("name is not valid UTF-8")
)

// ValidateName reports whether a string is usable as a profile name.
//
// The rule: valid UTF-8, one to MaxNameRunes characters, no leading or
// trailing whitespace, no control characters, and no invisible or
// bidirectional formatting characters. Interior spaces and non-Latin scripts
// are fine — the name is a display identity, not a filename or a shell word.
//
// Invisible formatting characters are refused rather than stripped because a
// name that does not render as its own characters cannot be typed back by the
// person who chose it, which defeats the point of being able to find your
// profile again.
func ValidateName(name string) error {
	if !utf8.ValidString(name) {
		return ErrNameNotUTF8
	}
	runes := []rune(name)
	if len(runes) == 0 {
		return ErrNameEmpty
	}
	if len(runes) > MaxNameRunes {
		return fmt.Errorf("%d characters, limit is %d: %w", len(runes), MaxNameRunes, ErrNameTooLong)
	}
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return ErrNamePadded
	}
	for _, r := range runes {
		switch {
		case unicode.IsControl(r):
			return fmt.Errorf("character %U: %w", r, ErrNameControl)
		case unicode.Is(unicode.Cf, r):
			return fmt.Errorf("character %U: %w", r, ErrNameInvisible)
		}
	}
	return nil
}

// foldKey is the identity of a name for duplicate detection: lower-cased with
// runs of whitespace collapsed to a single space.
//
// Duplicate detection is deliberately looser than equality. A player who
// created "Balint" and comes back typing "balint" is the same player, and
// letting both exist would split their results across two profiles — the exact
// failure the fuzzy name lookup exists to prevent.
func foldKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// indexOf finds a profile by folded name, or reports -1.
func indexOf(profiles []Profile, name string) int {
	key := foldKey(name)
	for i := range profiles {
		if foldKey(profiles[i].Name) == key {
			return i
		}
	}
	return -1
}
