package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BAKocska/twixtui/internal/profile"
)

// The chosen profile is remembered in its own one-line file rather than inside
// the profile store, because it is a property of this machine's session and not
// of any profile. A --profile flag overrides it without changing it, so a
// scripted game cannot silently retarget the next interactive one.

// currentProfile returns the profile to play as, and whether one is known. The
// flag wins over the remembered choice, and a remembered name that no longer
// exists is ignored rather than reported, since a deleted profile is not an
// error condition.
func (o *options) currentProfile(store *profile.Store) (string, bool) {
	if o.profile != "" {
		if name, err := resolveProfileName(store, o.profile); err == nil {
			return name, true
		}
		return "", false
	}
	dir, err := o.configPath()
	if err != nil {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, currentProfileFile))
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		return "", false
	}
	if p, ok := store.Get(name); ok {
		return p.Name, true
	}
	return "", false
}

// setCurrentProfile remembers the chosen profile.
func (o *options) setCurrentProfile(name string) error {
	dir, err := o.configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, currentProfileFile), []byte(name+"\n"), 0o644)
}

// requireProfile returns the profile to play as, creating it if the player named
// one that does not exist yet. It is what the game commands call so that a
// player can go straight from install to a game with a single command.
func (o *options) requireProfile() (*profile.Store, string, error) {
	store, err := o.openProfiles()
	if err != nil {
		return nil, "", err
	}
	if name, ok := o.currentProfile(store); ok {
		if err := store.Touch(name); err != nil {
			return nil, "", err
		}
		return store, name, nil
	}
	// An explicit --profile that does not exist yet is a request to create it.
	if o.profile != "" {
		if err := profile.ValidateName(o.profile); err != nil {
			return nil, "", err
		}
		p, err := store.Create(o.profile)
		if err != nil {
			return nil, "", err
		}
		if err := o.setCurrentProfile(p.Name); err != nil {
			return nil, "", err
		}
		return store, p.Name, nil
	}
	return store, "", nil
}
