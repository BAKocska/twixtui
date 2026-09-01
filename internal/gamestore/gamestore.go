// Package gamestore keeps games on disk: finished games worth replaying, games
// interrupted part way through, and correspondence games waiting for the
// opponent's next move code.
//
// Each game is its own file, named by its identifier, because these are written
// one at a time and a single shared file would make two twixtui processes
// contend over unrelated games. The game itself is stored as an encoded
// game.Record, so a file that has been edited or truncated is refused when it is
// loaded rather than quietly producing a different position.
package gamestore

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// Kind says how a game is being played, which decides where it can be resumed.
type Kind string

// The kinds of game that can be stored.
const (
	// Hotseat is two players taking turns at this keyboard.
	Hotseat Kind = "hotseat"
	// VersusBot is one player against the built-in opponent.
	VersusBot Kind = "bot"
	// Remote is a live game over a network connection.
	Remote Kind = "remote"
	// Correspondence is a game played by exchanging move codes.
	Correspondence Kind = "correspondence"
	// Imported is a record read in from elsewhere with "game import". The
	// record format carries no names and no kind, so nobody on this machine is
	// known to have played it: it is kept to be shown and replayed, and a
	// listing that offers games to carry on with should leave it alone.
	Imported Kind = "imported"
)

// Saved is one stored game.
type Saved struct {
	ID      string    `json:"id"`
	Kind    Kind      `json:"kind"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`

	// Player is the local profile's name, and Side the axis it plays.
	Player string `json:"player"`
	Side   string `json:"side"`
	// Opponent is the other player: a profile name, a bot tier, or a remote name.
	Opponent string `json:"opponent"`

	// Record is an encoded game.Record, which carries its own integrity checks.
	Record string `json:"record"`

	// Finished is set once the game has a result, so a listing can separate
	// games that are waiting for a move from games that are over.
	Finished bool `json:"finished"`
}

// Game rebuilds the position, refusing a record that has been altered.
func (s Saved) Game() (*game.Game, error) {
	g, _, err := game.LoadRecord(s.Record)
	if err != nil {
		return nil, fmt.Errorf("saved game %s: %w", s.ID, err)
	}
	return g, nil
}

// Describe renders a one-line summary for a listing.
func (s Saved) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s vs %s", s.Player, s.Opponent)
	if s.Side != "" {
		fmt.Fprintf(&b, " (%s)", s.Side)
	}
	if s.Finished {
		b.WriteString(", finished")
	} else {
		b.WriteString(", in progress")
	}
	return b.String()
}

// Store is the collection of stored games in one directory.
type Store struct {
	dir string
}

const subdir = "games"

// Open prepares the store. The directory is created on the first write rather
// than here, so listing games on a fresh install does not leave empty
// directories behind.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("gamestore: no directory given")
	}
	return &Store{dir: filepath.Join(dir, subdir)}, nil
}

// Dir returns the directory games are kept in.
func (s *Store) Dir() string { return s.dir }

// idAlphabet avoids characters that are easy to confuse when a player reads an
// identifier off the screen and types it back.
const idAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// NewID returns a short random identifier.
func NewID() string {
	const n = 8
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice; falling back to a
		// time-derived identifier is better than refusing to start a game.
		now := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(now >> (8 * (i % 8)))
		}
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(out)
}

func (s *Store) path(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// ValidateID rejects an identifier that could escape the store's directory or
// collide with a shell pattern.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("game identifier is empty")
	}
	if len(id) > 32 {
		return fmt.Errorf("game identifier %q is too long", id)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("game identifier %q may only hold lower-case letters, digits and hyphens", id)
		}
	}
	return nil
}

// Put writes a game, replacing any earlier version of it. Updated is stamped
// here so that a caller cannot forget to.
//
// A finished game is final. Once a result has been recorded the game is over,
// it has been rated, and there is nothing left to play; reopening it and
// storing the position it had before the result would contradict the rating log
// and lose the result. Such a write is refused here rather than in the caller,
// because the store is the one place every writer passes through.
func (s *Store) Put(sv Saved) error {
	if sv.ID == "" {
		return errors.New("cannot store a game with no identifier")
	}
	path, err := s.path(sv.ID)
	if err != nil {
		return err
	}
	if sv.Created.IsZero() {
		sv.Created = time.Now()
	}
	if !sv.Finished {
		if old, err := s.Get(sv.ID); err == nil && old.Finished {
			return fmt.Errorf("game %s is finished and cannot be reopened", sv.ID)
		}
	}
	sv.Updated = time.Now()
	if _, _, err := game.LoadRecord(sv.Record); err != nil {
		return fmt.Errorf("refusing to store a game whose record does not load: %w", err)
	}

	body, err := json.MarshalIndent(sv, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return writeFileAtomic(path, body)
}

// Get reads one game.
func (s *Store) Get(id string) (Saved, error) {
	path, err := s.path(id)
	if err != nil {
		return Saved{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Saved{}, fmt.Errorf("no saved game %q", id)
	}
	if err != nil {
		return Saved{}, err
	}
	var sv Saved
	if err := json.Unmarshal(raw, &sv); err != nil {
		return Saved{}, fmt.Errorf("saved game %q is not readable: %w", id, err)
	}
	return sv, nil
}

// List returns every stored game, most recently updated first. A file that
// cannot be read is skipped rather than failing the whole listing, so one
// damaged game does not hide the rest.
func (s *Store) List() []Saved {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	out := make([]Saved, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sv Saved
		if err := json.Unmarshal(raw, &sv); err != nil {
			continue
		}
		out = append(out, sv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Unfinished returns the stored games still waiting for a move.
func (s *Store) Unfinished() []Saved {
	var out []Saved
	for _, sv := range s.List() {
		if !sv.Finished {
			out = append(out, sv)
		}
	}
	return out
}

// OfKind returns the stored games of one kind, most recently updated first.
func (s *Store) OfKind(k Kind) []Saved {
	var out []Saved
	for _, sv := range s.List() {
		if sv.Kind == k {
			out = append(out, sv)
		}
	}
	return out
}

// Delete removes a game.
func (s *Store) Delete(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no saved game %q", id)
		}
		return err
	}
	return nil
}

// Resolve turns a possibly abbreviated identifier into exactly one game, which
// is what lets a player type the first few characters they can see on screen.
func (s *Store) Resolve(prefix string) (Saved, error) {
	if prefix == "" {
		return Saved{}, errors.New("no game identifier given")
	}
	prefix = strings.ToLower(prefix)
	var found []Saved
	for _, sv := range s.List() {
		if sv.ID == prefix {
			return sv, nil
		}
		if strings.HasPrefix(sv.ID, prefix) {
			found = append(found, sv)
		}
	}
	switch len(found) {
	case 0:
		return Saved{}, fmt.Errorf("no saved game starts with %q", prefix)
	case 1:
		return found[0], nil
	}
	ids := make([]string, 0, len(found))
	for _, sv := range found {
		ids = append(ids, sv.ID)
	}
	return Saved{}, fmt.Errorf("%q matches several games: %s", prefix, strings.Join(ids, ", "))
}

// writeFileAtomic writes through a temporary file in the same directory so that
// an interrupted write cannot leave a half-written game behind.
func writeFileAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
