package cli

import (
	"github.com/BAKocska/twixtui/internal/profile"
)

// Which profile is playing is stored by the profile store, so that the command
// line and the interface read and write the same thing. They previously kept
// separate notions of it, and a profile picked in the interface was not the one a
// later subcommand used: the player was told nobody was playing.
//
// A --profile flag overrides the stored choice without changing it, so a scripted
// game cannot silently retarget the next interactive one.

// currentProfile returns the profile to play as, and whether one is known.
func (o *options) currentProfile(store *profile.Store) (string, bool) {
	if o.profile != "" {
		if name, err := resolveProfileName(store, o.profile); err == nil {
			return name, true
		}
		return "", false
	}
	p, ok := store.Current()
	if !ok {
		return "", false
	}
	return p.Name, true
}

// requireProfile returns the profile to play as, creating it if the player named
// one that does not exist yet, which is what lets a new player go from install to
// a game in a single command.
func (o *options) requireProfile() (*profile.Store, string, error) {
	store, err := o.openProfiles()
	if err != nil {
		return nil, "", err
	}
	if name, ok := o.currentProfile(store); ok {
		if _, err := store.UseCurrent(name); err != nil {
			return nil, "", err
		}
		return store, name, nil
	}
	// An explicit --profile naming something that does not exist is a request to
	// create it.
	if o.profile != "" {
		if err := profile.ValidateName(o.profile); err != nil {
			return nil, "", err
		}
		p, err := store.Create(o.profile)
		if err != nil {
			return nil, "", err
		}
		if _, err := store.UseCurrent(p.Name); err != nil {
			return nil, "", err
		}
		return store, p.Name, nil
	}
	return store, "", nil
}
