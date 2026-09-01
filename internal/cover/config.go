package cover

import (
	"fmt"
	"os"
	"strings"
)

// The environment is the configuration surface here because the cover is
// decoration: a player who cares exports a line in their shell profile, and
// nothing else in the program has to know. The interface can still offer the
// same choices behind a settings screen later without either variable
// changing meaning.

// EnvImage names an image file for Photo to project instead of the shipped
// picture.
//
//	TWIXTUI_COVER_IMAGE=/path/to/scan.jpg twixtui
const EnvImage = "TWIXTUI_COVER_IMAGE"

// EnvArt overrides which artwork Best answers with: "photo" or "homage".
//
//	TWIXTUI_COVER_ART=homage twixtui
const EnvArt = "TWIXTUI_COVER_ART"

// FromEnvironment configures the photograph from EnvImage. It reports whether
// one was configured; an unset variable is normal life, not an error, but a
// set variable naming an unreadable or undecodable file is reported, because
// silently falling back to the shipped picture would leave the player staring
// at the wrong picture with nothing to debug.
func FromEnvironment() (bool, error) {
	path := os.Getenv(EnvImage)
	if path == "" {
		return false, nil
	}
	if err := SetPhoto(path); err != nil {
		return false, err
	}
	return true, nil
}

// ParseEnvironment applies both environment variables and returns every
// complaint it has, so the command line can report them once, before the program
// switches the terminal to its alternate screen. Nothing on a drawing path
// reports anything: Best is called for every frame, so a diagnostic there would
// repeat for as long as the menu is open and would be written over the picture.
//
// A bad value is reported and then ignored, rather than being fatal. Somebody
// who mistypes the name of an artwork wants to play the game, not to be stopped
// by it.
func ParseEnvironment() []error {
	var problems []error
	switch v := strings.TrimSpace(os.Getenv(EnvArt)); strings.ToLower(v) {
	case "":
		setArtOverride(Homage, false)
	case "homage":
		setArtOverride(Homage, true)
	case "photo":
		setArtOverride(Photo, true)
	default:
		setArtOverride(Homage, false)
		problems = append(problems, fmt.Errorf("%s is %q, which is neither \"homage\" nor \"photo\"; using the artwork the terminal size suggests", EnvArt, v))
	}
	if _, err := FromEnvironment(); err != nil {
		problems = append(problems, err)
	}
	return problems
}
