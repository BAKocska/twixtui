// Package profile stores the local player identities twixtui plays under.
//
// There are no passwords: a profile is a name and two timestamps, kept in one
// JSON file under the user's configuration directory. The store's job is to let
// a returning player pick the identity they used last time even when they
// misremember how they spelled it, which is what Search is for.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fileName is the store's file within the configuration directory.
const fileName = "profiles.json"

// storeVersion is the on-disk schema version. A file written by a newer
// twixtui is refused rather than parsed on a best-effort basis, so an older
// binary cannot silently drop fields it does not understand when it writes the
// file back.
const storeVersion = 1

// Store errors. Callers match these with errors.Is.
var (
	ErrNotFound = errors.New("profile not found")
	ErrExists   = errors.New("profile already exists")
)

// Profile is one local player identity.
type Profile struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used"`
	// Introduced records that this player has been through the short
	// introduction the interface offers on a first run. See introduction.go for
	// why it is a property of the profile rather than of the machine, and for
	// why it is added without moving storeVersion.
	Introduced bool `json:"introduced,omitempty"`
}

// document is the on-disk shape of the store.
type document struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

// Store is the set of profiles on this machine.
//
// A Store is safe for concurrent use. Mutations reload the file inside an
// advisory lock before applying, so a second twixtui process cannot lose the
// first one's writes; reads reload only when the file has changed underneath.
type Store struct {
	mu       sync.Mutex
	path     string
	lockPath string
	stamp    stamp
	profiles []Profile

	// now is the clock. Tests replace it to get deterministic ordering.
	now func() time.Time
}

// Open loads the profile store in dir, creating the directory on first use. An
// empty dir means the default configuration directory.
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		def, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = def
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	s := &Store{
		path:     filepath.Join(dir, fileName),
		lockPath: filepath.Join(dir, fileName+".lock"),
		now:      time.Now,
	}
	if err := s.loadShared(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path reports the file the store reads and writes, for diagnostics.
func (s *Store) Path() string { return s.path }

// loadShared reloads the file under a shared advisory lock, so a read never
// observes a writer's temporary file or a partially replaced entry.
func (s *Store) loadShared() error {
	release, err := lockFile(s.lockPath, false)
	if err != nil {
		return err
	}
	defer release()
	return s.read()
}

// read replaces the in-memory snapshot from disk. The caller holds the advisory
// lock; nesting a second lock acquisition here would deadlock a writer.
func (s *Store) read() error {
	data, st, err := readFile(s.path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		s.profiles, s.stamp = nil, st
		return nil
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", s.path, err)
	}
	if doc.Version > storeVersion {
		return fmt.Errorf("%s was written by a newer twixtui (schema %d, this build understands %d)", s.path, doc.Version, storeVersion)
	}
	s.profiles, s.stamp = doc.Profiles, st
	return nil
}

// refresh reloads when another process has replaced the file. Read methods
// return no error, so a failed refresh keeps the last good snapshot rather than
// presenting an empty store.
func (s *Store) refresh() {
	if statStamp(s.path).same(s.stamp) {
		return
	}
	_ = s.loadShared()
}

// mutate applies fn to the stored profiles and writes the result. The reload
// inside the lock is what makes concurrent writers additive instead of
// last-write-wins.
func (s *Store) mutate(fn func(*[]Profile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := lockFile(s.lockPath, true)
	if err != nil {
		return err
	}
	defer release()
	if err := s.read(); err != nil {
		return err
	}
	profiles := append([]Profile(nil), s.profiles...)
	if err := fn(&profiles); err != nil {
		return err
	}
	data, err := marshal(profiles)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.path, data); err != nil {
		return err
	}
	s.profiles = profiles
	s.stamp = statStamp(s.path)
	return nil
}

func marshal(profiles []Profile) ([]byte, error) {
	doc := document{Version: storeVersion, Profiles: profiles}
	if doc.Profiles == nil {
		doc.Profiles = []Profile{}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding profiles: %w", err)
	}
	return append(data, '\n'), nil
}

// List returns every profile, most recently used first.
func (s *Store) List() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	out := append([]Profile(nil), s.profiles...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastUsed.Equal(out[j].LastUsed) {
			return out[i].LastUsed.After(out[j].LastUsed)
		}
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return foldKey(out[i].Name) < foldKey(out[j].Name)
	})
	return out
}

// Get looks a profile up by name, ignoring case and whitespace differences the
// same way duplicate detection does.
func (s *Store) Get(name string) (Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if i := indexOf(s.profiles, name); i >= 0 {
		return s.profiles[i], true
	}
	return Profile{}, false
}

// Create adds a profile. The name is validated and must not collide with an
// existing one, case and interior spacing ignored.
func (s *Store) Create(name string) (Profile, error) {
	if err := ValidateName(name); err != nil {
		return Profile{}, fmt.Errorf("%q: %w", name, err)
	}
	var created Profile
	err := s.mutate(func(ps *[]Profile) error {
		if i := indexOf(*ps, name); i >= 0 {
			return fmt.Errorf("%q collides with %q: %w", name, (*ps)[i].Name, ErrExists)
		}
		now := s.now().UTC()
		created = Profile{Name: name, Created: now, LastUsed: now}
		*ps = append(*ps, created)
		return nil
	})
	if err != nil {
		return Profile{}, err
	}
	return created, nil
}

// Touch records that a profile was just used, which is what orders the list the
// player sees at launch.
func (s *Store) Touch(name string) error {
	return s.mutate(func(ps *[]Profile) error {
		i := indexOf(*ps, name)
		if i < 0 {
			return fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		(*ps)[i].LastUsed = s.now().UTC()
		return nil
	})
}

// Rename changes a profile's name, keeping its timestamps. Changing only the
// capitalisation of an existing name is allowed; colliding with a different
// profile is not.
func (s *Store) Rename(oldName, newName string) error {
	if err := ValidateName(newName); err != nil {
		return fmt.Errorf("%q: %w", newName, err)
	}
	wasCurrent := false
	if cur, ok := s.Current(); ok && foldKey(cur.Name) == foldKey(oldName) {
		wasCurrent = true
	}
	err := s.mutate(func(ps *[]Profile) error {
		i := indexOf(*ps, oldName)
		if i < 0 {
			return fmt.Errorf("%q: %w", oldName, ErrNotFound)
		}
		if j := indexOf(*ps, newName); j >= 0 && j != i {
			return fmt.Errorf("%q collides with %q: %w", newName, (*ps)[j].Name, ErrExists)
		}
		(*ps)[i].Name = newName
		return nil
	})
	if err != nil {
		return err
	}
	// The recorded choice follows the rename, so it cannot be left pointing at
	// a name that no longer exists.
	if wasCurrent {
		return s.SetCurrent(newName)
	}
	return nil
}

// Delete removes a profile. Recorded results are not touched: the leaderboard
// keeps its own history, and deleting an identity is not meant to rewrite the
// record of games that were played.
func (s *Store) Delete(name string) error {
	wasCurrent := false
	if cur, ok := s.Current(); ok && foldKey(cur.Name) == foldKey(name) {
		wasCurrent = true
	}
	err := s.mutate(func(ps *[]Profile) error {
		i := indexOf(*ps, name)
		if i < 0 {
			return fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		*ps = append((*ps)[:i], (*ps)[i+1:]...)
		return nil
	})
	if err != nil {
		return err
	}
	if wasCurrent {
		return s.ClearCurrent()
	}
	return nil
}
