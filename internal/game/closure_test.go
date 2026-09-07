// This file covers the closure between the two directions of a record: what a
// reader accepts has to survive being written down again. It sits beside the
// record rather than beside the store because of where its fixture can be
// built — a record large enough to straddle the size limit is assembled field
// by field, and the digest that makes it a record is package-private — while
// the property it checks is the store's.
package game_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// recordLimitFixture is the size these fixtures are built around. It is a fixed
// number rather than an expression in game.MaxRecordBytes so that a build whose
// bound has moved cannot turn a fixture into a terabyte of padding; what the
// test then reports is the behaviour it found, not the constant it read.
const recordLimitFixture = 1 << 20

// straddlingRecord returns a record in the two spellings that matter: the one
// that arrives, inside the limit, and the canonical one it would be written
// back as, past it. The reader takes the ruleset's flags through
// strconv.ParseBool, so "swap=1" arrives where "swap=true" is written, and the
// difference is what carries a record over the limit on its way out.
//
// The bulk is empty transcript entries. A replay skips them, so the record
// still replays to the game it claims to be, and the digest is recomputed over
// what is actually there: this is a sound record of a real position, not a
// malformed one that any parser would refuse.
func straddlingRecord(t *testing.T) (accepted string, canonicalBytes int) {
	t.Helper()
	rs := game.Std
	rs.Size = 8
	rec, err := game.MustNew(rs).Record()
	if err != nil {
		t.Fatal(err)
	}
	full := rs.Canonical()
	short := strings.NewReplacer("true", "1", "false", "0").Replace(full)
	if len(short) >= len(full) {
		t.Fatalf("the ruleset %q has no shorter accepted spelling, so no fixture can straddle the limit", full)
	}
	pad := recordLimitFixture + 1 - len(rec.Encode())
	if pad <= 0 {
		t.Fatalf("an unplayed record is already %d bytes, so there is nothing to pad", len(rec.Encode()))
	}
	rec.Moves = strings.Repeat(";", pad)
	rec.Digest = game.Digest(rec)
	canonical := rec.Encode()
	accepted = strings.Replace(canonical, full, short, 1)
	if len(accepted) > recordLimitFixture || len(canonical) <= recordLimitFixture {
		t.Fatalf("the fixture is meant to be inside %d bytes as it arrives and past it once canonical; it is %d and %d",
			recordLimitFixture, len(accepted), len(canonical))
	}
	return accepted, len(canonical)
}

// soundRecord is the record of a game a few moves in, well inside the limit.
// It is a game still being played so that storing it twice under one
// identifier is an ordinary thing to do: a finished game cannot be reopened,
// and a refusal on that would stand in for the refusal this test is about.
func soundRecord(t *testing.T) string {
	t.Helper()
	rs := game.Std
	rs.Size = 8
	g := game.MustNew(rs)
	for _, m := range []string{"D1", "A2", "E3"} {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode()
}

// TestTheStoreRefusesARecordItCouldNotReadBack covers what used to happen to a
// record accepted inside the limit whose canonical encoding is past it: the
// store wrote it, said the game was saved, and handed back a file that nothing
// — this build included — could load again. The write is refused now, and a
// game already under that identifier is left as it was rather than replaced by
// something unreadable.
func TestTheStoreRefusesARecordItCouldNotReadBack(t *testing.T) {
	accepted, canonicalBytes := straddlingRecord(t)
	// The reader's own contract is unchanged: this is a record, and it loads.
	if _, _, err := game.LoadRecord(accepted); err != nil {
		t.Fatalf("a record of %d bytes was refused by the reader: %v", len(accepted), err)
	}

	dir := t.TempDir()
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const id = "closure"
	sound := soundRecord(t)
	if err := store.Put(gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Imported,
		Player:   "vertical",
		Opponent: "horizontal",
		Record:   sound,
	}); err != nil {
		t.Fatalf("a record inside the limit was refused: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(store.Dir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}

	err = store.Put(gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Imported,
		Player:   "vertical",
		Opponent: "horizontal",
		Record:   accepted,
	})
	if err == nil {
		t.Fatalf("the store accepted a record that canonicalises to %d bytes", canonicalBytes)
	}
	if !errors.Is(err, game.ErrRecordTooLarge) {
		t.Errorf("the refusal reads %q, which is not a refusal on size", err)
	}
	after, err := os.ReadFile(filepath.Join(store.Dir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the refused write replaced the game that was stored under that identifier")
	}
	had, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := had.Game(); err != nil {
		t.Errorf("the stored game no longer loads: %v", err)
	}

	// A store that refused every write would pass everything above, so the
	// same record is stored again under a fresh identifier.
	if err := store.Put(gamestore.Saved{
		ID:       "control",
		Kind:     gamestore.Imported,
		Player:   "vertical",
		Opponent: "horizontal",
		Record:   sound,
	}); err != nil {
		t.Fatalf("a second record inside the limit was refused: %v", err)
	}
}
