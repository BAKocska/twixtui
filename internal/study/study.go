// Package study keeps a player's own notes and bookmarks on saved games.
//
// A study lives beside its game rather than inside it. The game's record is
// canonical and carries digests that say what the game was, so nothing written
// here ever touches it; a study is this machine's annotation of one record,
// kept in a directory of its own with one file per saved game, named by the
// game's identifier. Exporting a game therefore exports the record alone, and
// deleting a saved game leaves its study file where it was, unattached but
// recoverable.
//
// A study is tied to the record it was written against, not only to the game's
// identifier: it carries that record's digest. A note on entry 14 is about the
// position after the fourteenth entry of one particular record, so a study
// whose digest no longer matches the game's record is refused rather than
// moved onto a different position without anyone noticing.
//
// Every successful save increments the study's revision, and a save names the
// revision it was made against. Two windows editing the same study cannot
// overwrite each other: the second to save finds that the revision has moved
// on and is refused, which leaves the first window's save on disk and the
// second window's text in its editor, both still there to be reconciled.
package study

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/BAKocska/twixtui/internal/gamestore"
)

const (
	// Version is the study document format this build reads and writes.
	Version = 1
	// MaxNoteBytes bounds one note, counted in UTF-8 bytes.
	MaxNoteBytes = 8 << 10
	// MaxFileBytes bounds one study document on disk. Nothing this build
	// writes exceeds it, so anything that does was not written by twixtui.
	MaxFileBytes = 1 << 20
)

// subdir is the directory studies are kept in, inside the configuration
// directory.
const subdir = "study"

var (
	// ErrRecordChanged reports a stored study written against a different
	// record of the game. The study file is left as it is.
	ErrRecordChanged = errors.New("study belongs to a different record of this game")
	// ErrStale reports a save made against a revision the stored study has
	// since moved past.
	ErrStale = errors.New("study has been saved elsewhere since it was loaded")
	// ErrNewerVersion reports a study document written in a format newer than
	// this build knows.
	ErrNewerVersion = errors.New("study was written by a newer version of twixtui")
	// ErrCorrupt reports a study document that cannot be read as one.
	ErrCorrupt = errors.New("study file is damaged")
	// ErrUnfinished reports a save for a game that has no result yet.
	ErrUnfinished = errors.New("game is not finished")
	// ErrInvalid reports a mark that cannot be saved.
	ErrInvalid = errors.New("invalid study mark")
)

// Target identifies the saved game a study belongs to. The caller derives it
// from the saved game's decoded, digest-checked record.
type Target struct {
	// GameID is the saved game's identifier. It becomes a file name, so it is
	// checked with gamestore.ValidateID before it is used.
	GameID string
	// RecordDigest is the digest of the record the study is about.
	RecordDigest string
	// Entries is the number of transcript entries in the record. Marks may be
	// placed on entries 0 through Entries, where 0 is the initial position.
	Entries int
	// Finished reports whether the record has a result. Only a finished game's
	// study may be saved.
	Finished bool
}

// validate refuses a target that could not name a study file, or that could
// not tell one record's study from another's.
func (t Target) validate() error {
	if err := gamestore.ValidateID(t.GameID); err != nil {
		return fmt.Errorf("study: %w", err)
	}
	if t.RecordDigest == "" {
		return fmt.Errorf("study: game %s has no record digest to attach a study to", t.GameID)
	}
	if t.Entries < 0 {
		return fmt.Errorf("study: game %s cannot have %d entries", t.GameID, t.Entries)
	}
	return nil
}

// Mark is what a player keeps about one entry: a note, a bookmark, or both.
type Mark struct {
	Entry    int    `json:"entry"`
	Note     string `json:"note,omitempty"`
	Bookmark bool   `json:"bookmark,omitempty"`
}

// empty reports a mark that says nothing, which is how a mark is removed.
func (m Mark) empty() bool { return m.Note == "" && !m.Bookmark }

// check reports, wrapped in kind, why m cannot belong to a record with entries
// transcript entries, or nil when it can. Notes are kept verbatim, control
// characters included, since rendering them inert is the display's job; they
// only have to be text.
func (m Mark) check(entries int, kind error) error {
	switch {
	case m.Entry < 0 || m.Entry > entries:
		return fmt.Errorf("%w: entry %d is outside 0..%d", kind, m.Entry, entries)
	case len(m.Note) > MaxNoteBytes:
		return fmt.Errorf("%w: the note on entry %d is %d bytes, more than the %d a note may have", kind, m.Entry, len(m.Note), MaxNoteBytes)
	case !utf8.ValidString(m.Note):
		return fmt.Errorf("%w: the note on entry %d is not valid UTF-8", kind, m.Entry)
	}
	return nil
}

// Study is the stored document for one saved game.
type Study struct {
	Version      int    `json:"version"`
	GameID       string `json:"game_id"`
	RecordDigest string `json:"record_digest"`
	Revision     int64  `json:"revision"`
	// Marks is sorted by Entry, holds each entry at most once, and never holds
	// a mark with neither a note nor a bookmark.
	Marks []Mark `json:"entries"`
}

// At returns the mark on entry, or an empty mark for that entry when there is
// none.
func (s Study) At(entry int) Mark {
	if i, found := s.find(entry); found {
		return s.Marks[i]
	}
	return Mark{Entry: entry}
}

// find locates entry in the sorted marks, or where it would go.
func (s Study) find(entry int) (int, bool) {
	return slices.BinarySearchFunc(s.Marks, entry, func(m Mark, e int) int { return cmp.Compare(m.Entry, e) })
}

// with returns the marks with m in place of whatever entry m.Entry held, or
// without that entry when m is empty. The study's own marks are not touched.
func (s Study) with(m Mark) []Mark {
	marks := slices.Clone(s.Marks)
	i, found := s.find(m.Entry)
	switch {
	case m.empty() && found:
		marks = slices.Delete(marks, i, i+1)
	case m.empty():
	case found:
		marks[i] = m
	default:
		marks = slices.Insert(marks, i, m)
	}
	return marks
}

// Store is the collection of studies in one configuration directory.
type Store struct {
	dir string
}

// Open prepares the store of studies kept in configDir. The directory is
// created on the first save rather than here or on a load, so looking at a
// game nobody has taken notes on leaves nothing behind.
func Open(configDir string) *Store {
	return &Store{dir: filepath.Join(configDir, subdir)}
}

// Dir returns the directory studies are kept in.
func (st *Store) Dir() string { return st.dir }

// path is the study file for one game. The identifier has been validated, so
// it names a file inside the directory.
func (st *Store) path(id string) string {
	return filepath.Join(st.dir, id+".json")
}

// lockPath is the lock file for one game's study. An identifier holds only
// lower-case letters, digits and hyphens, so this can never name a study file.
// It is never removed: a writer may be waiting on it, and a new file under the
// same name would be a different lock.
func (st *Store) lockPath(id string) string {
	return filepath.Join(st.dir, id+".lock")
}

// Load reads the study of the game t names. A game with no study yet has an
// empty one at revision 0.
//
// A stored study written against a different record is refused with
// ErrRecordChanged, one from a newer format with ErrNewerVersion, and one that
// cannot be read as a study of this record with ErrCorrupt; each of them comes
// back with no study at all, so nothing from the refused file can be mistaken
// for this record's notes. Loading never writes, not even a lock file.
func (st *Store) Load(t Target) (Study, error) {
	if err := t.validate(); err != nil {
		return Study{}, err
	}
	return st.read(t)
}

// read loads the stored study for a validated target.
func (st *Store) read(t Target) (Study, error) {
	path := st.path(t.GameID)
	data, found, err := readDocument(path)
	if err != nil {
		return Study{}, err
	}
	if !found {
		return Study{Version: Version, GameID: t.GameID, RecordDigest: t.RecordDigest}, nil
	}
	s, err := decode(data, t)
	if err != nil {
		return Study{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Set saves m in place of whatever mark its entry held, or removes that mark
// when m has neither a note nor a bookmark. baseRevision is the revision the
// caller loaded and edited.
//
// The target and the mark are checked before anything is created, so a refused
// save of an unfinished game or of a bad mark leaves no trace. The stored study
// is then read again under the study's exclusive lock, which is held until the
// write is done: comparing revisions is only worth anything while nobody else
// can move them. A stored revision other than baseRevision means somebody else
// saved first; the save is refused with ErrStale and the current stored study
// is returned, so the caller can show what is there beside its own unsaved
// text. Otherwise the revision advances by one and the study is replaced
// atomically. A save that would make the study larger than MaxFileBytes is
// refused with ErrInvalid, since the file could not be loaded again.
func (st *Store) Set(t Target, baseRevision int64, m Mark) (Study, error) {
	if err := t.validate(); err != nil {
		return Study{}, err
	}
	if !t.Finished {
		return Study{}, fmt.Errorf("%w: notes on game %s can be saved once it has a result", ErrUnfinished, t.GameID)
	}
	if err := m.check(t.Entries, ErrInvalid); err != nil {
		return Study{}, err
	}
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		return Study{}, fmt.Errorf("creating %s: %w", st.dir, err)
	}
	release, err := lockStudy(st.lockPath(t.GameID))
	if err != nil {
		return Study{}, err
	}
	defer release()

	cur, err := st.read(t)
	if err != nil {
		return Study{}, err
	}
	if cur.Revision != baseRevision {
		return cur, fmt.Errorf("%w: game %s is at revision %d, and this edit was made against revision %d",
			ErrStale, t.GameID, cur.Revision, baseRevision)
	}
	next := cur
	next.Version = Version
	next.Revision = cur.Revision + 1
	next.Marks = cur.with(m)
	data, err := encode(next)
	if err != nil {
		return Study{}, err
	}
	if len(data) > MaxFileBytes {
		return Study{}, fmt.Errorf("%w: the study of game %s would take %d bytes, more than the %d a study may",
			ErrInvalid, t.GameID, len(data), MaxFileBytes)
	}
	if err := atomicWrite(st.path(t.GameID), data); err != nil {
		return Study{}, err
	}
	return next, nil
}

// encode renders a study the way it is stored: indented and ending in a
// newline, like the other files twixtui keeps. HTML characters are left as
// they are rather than escaped, because a note is plain text, and escaping
// every "<" into six bytes would spend the file's size bound on nothing.
func encode(s Study) ([]byte, error) {
	if s.Marks == nil {
		s.Marks = []Mark{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encoding study of game %s: %w", s.GameID, err)
	}
	return buf.Bytes(), nil
}

// decode reads a stored study and checks it against the target it was loaded
// for.
//
// The version is read on its own first, so a document from a newer format is
// reported as that rather than as damage to this one. Within version 1 the
// document has to hold exactly the fields version 1 has: a field this build
// does not know would be dropped by its next save, so such a file is refused
// rather than rewritten without it. Anything from disk is quoted when it
// appears in an error, because an error is shown to the player and the file
// may hold anything.
func decode(data []byte, t Target) (Study, error) {
	if !utf8.Valid(data) {
		return Study{}, fmt.Errorf("%w: it is not UTF-8 text", ErrCorrupt)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Study{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	switch {
	case head.Version > Version:
		return Study{}, fmt.Errorf("%w: it has version %d, and this build reads version %d", ErrNewerVersion, head.Version, Version)
	case head.Version < 1:
		return Study{}, fmt.Errorf("%w: it has no valid version", ErrCorrupt)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Study
	if err := dec.Decode(&s); err != nil {
		return Study{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Study{}, fmt.Errorf("%w: it holds more than one document", ErrCorrupt)
	}
	switch {
	case s.GameID != t.GameID:
		return Study{}, fmt.Errorf("%w: it calls itself the study of game %q, not %s", ErrCorrupt, s.GameID, t.GameID)
	case s.RecordDigest == "":
		return Study{}, fmt.Errorf("%w: it names no record", ErrCorrupt)
	case s.RecordDigest != t.RecordDigest:
		return Study{}, fmt.Errorf("%w: it was written against record %q, and game %s now has record %s",
			ErrRecordChanged, s.RecordDigest, t.GameID, t.RecordDigest)
	case s.Revision < 1:
		// A study is only ever written by a save, and a save leaves it at
		// revision 1 or later. Revision 0 means "nothing saved yet", and a file
		// claiming it would let an editor that loaded before the file existed
		// overwrite it without being told.
		return Study{}, fmt.Errorf("%w: it has revision %d", ErrCorrupt, s.Revision)
	}
	for i, m := range s.Marks {
		if err := m.check(t.Entries, ErrCorrupt); err != nil {
			return Study{}, err
		}
		if m.empty() {
			return Study{}, fmt.Errorf("%w: entry %d has neither a note nor a bookmark", ErrCorrupt, m.Entry)
		}
		if i > 0 && m.Entry <= s.Marks[i-1].Entry {
			return Study{}, fmt.Errorf("%w: entry %d is out of order or repeated", ErrCorrupt, m.Entry)
		}
	}
	return s, nil
}
