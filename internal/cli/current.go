package cli

import (
	"github.com/BAKocska/twixtui/internal/profile"
)

// Which profile is playing is stored by the profile store, so that the command
// line and the interface read and write the same thing. They previously kept
// separate notions of it, and a profile picked in the interface was not the one a
// later subcommand used: the player was told nobody was playing.
//
// A --profile flag overrides the stored choice for one run without changing it,
// so a scripted game cannot silently retarget the next interactive one. The one
// profile the flag may still create is the first: on a machine with no profiles
// there is no stored choice to retarget, and no other name the player could
// have meant, which is what lets a new player go from install to a game in a
// single command. Once a profile exists the flag resolves against exactly the
// rules "profile use" applies and is refused wherever that would be refused.
// The cost is that a second player on the machine has to run "profile create"
// before their first game; the benefit is that a typo the loose search cannot
// rescue can no longer split a player's history across two identities.

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

// requireProfile returns the profile to play as. See the note above for what
// the --profile flag is allowed to write and what it is not.
func (o *options) requireProfile() (*profile.Store, string, error) {
	store, err := o.openProfiles()
	if err != nil {
		return nil, "", err
	}
	if o.profile == "" {
		p, ok := store.Current()
		if !ok {
			return store, "", nil
		}
		if _, err := store.UseCurrent(p.Name); err != nil {
			return nil, "", err
		}
		return store, p.Name, nil
	}
	name, resolveErr := resolveProfileName(store, o.profile)
	if resolveErr == nil {
		// Having played is a fact about the profile, so record it; which
		// profile this machine plays as by default is not the flag's to
		// rewrite. Touch is UseCurrent without that second half.
		if err := store.Touch(name); err != nil {
			return nil, "", err
		}
		return store, name, nil
	}
	// The refusal is passed on as it stands rather than reworded, because the
	// point is that the flag and "profile use" answer the same question the
	// same way; a second wording here would be a second rule to keep in step.
	if len(store.List()) > 0 {
		return nil, "", resolveErr
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
