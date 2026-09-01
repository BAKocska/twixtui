package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Which profile is playing is a property of this machine rather than of any
// profile, so it lives in its own one-line file beside the store rather than as
// a flag inside it. It belongs here, next to the profiles themselves, because
// both the command line and the interface have to agree about it: when the
// interface owned its own copy, a profile chosen in the interface was not the one
// a later subcommand used, and the player was told nobody was playing.

const currentFileName = "current-profile"

// currentPath returns the file holding the chosen profile's name.
func (s *Store) currentPath() string {
	return filepath.Join(filepath.Dir(s.path), currentFileName)
}

// Current returns the profile last chosen on this machine.
//
// A name that no longer matches a profile reports as no choice rather than as an
// error: a deleted profile is an ordinary thing to find here, not a fault.
func (s *Store) Current() (Profile, bool) {
	raw, err := os.ReadFile(s.currentPath())
	if err != nil {
		return Profile{}, false
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		return Profile{}, false
	}
	return s.Get(name)
}

// SetCurrent records the profile that is playing. The name must already exist,
// so that the recorded choice cannot point at nothing.
func (s *Store) SetCurrent(name string) error {
	p, ok := s.Get(name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return atomicWrite(s.currentPath(), []byte(p.Name+"\n"))
}

// ClearCurrent forgets which profile is playing.
func (s *Store) ClearCurrent() error {
	err := os.Remove(s.currentPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// UseCurrent records the choice and marks the profile as used, which is the pair
// of things every caller wants when a player picks a name.
func (s *Store) UseCurrent(name string) (Profile, error) {
	p, ok := s.Get(name)
	if !ok {
		return Profile{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if err := s.Touch(p.Name); err != nil {
		return Profile{}, err
	}
	if err := s.SetCurrent(p.Name); err != nil {
		return Profile{}, err
	}
	updated, _ := s.Get(p.Name)
	return updated, nil
}
