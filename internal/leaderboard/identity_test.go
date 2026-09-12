package leaderboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// The identities a finished game supplies: a game store identifier, and the
// digest internal/game puts on the record that was saved under it. Fixed
// values, so what a test asserts on is the identity it wrote rather than
// whatever a generator produced on the day.
const (
	gameA   = "a1b2c3d4"
	digestA = "0f1e2d3c4b5a6978"
	gameB   = "e5f60718"
	digestB = "1122334455667788"
	gameC   = "c3d4e5f6"
)

// linked is a result that names the saved game it came from, the way a
// finalisation records one once the record has been stored.
func linked(r Result, id, digest string) Result {
	r.GameID, r.RecordDigest = id, digest
	return r
}

// boardFile is the board's contents and the time the file was last written. A
// write that was recognised as one already made has to leave both alone: it may
// not rewrite a row that is already right, and it may not move the timestamp
// every reader reloads on.
func boardFile(t *testing.T, dir string) ([]byte, time.Time) {
	t.Helper()
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("describing %s: %v", path, err)
	}
	return data, info.ModTime()
}

// ageBoardFile moves the board file's timestamps back an hour, so a write that
// happens afterwards is visible as one. Whole seconds, because not every
// filesystem keeps more than that.
func ageBoardFile(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, fileName)
	when := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("ageing %s: %v", path, err)
	}
}

// storedDocument is the board file as this build reads it.
func storedDocument(t *testing.T, data []byte) document {
	t.Helper()
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("board file does not parse: %v", err)
	}
	return doc
}

// storedRows is the board file's results key by key, which is how a migration
// test sees a legacy row written back exactly as it was read. Decoding into
// Result cannot show that: a field this build added would be indistinguishable
// from one the file already carried.
func storedRows(t *testing.T, data []byte) []map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("board file does not parse: %v", err)
	}
	return doc.Results
}

func TestRecordRejectsAMalformedIdentity(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		digest string
	}{
		{"an identifier with no digest", gameA, ""},
		{"a digest with no identifier", "", digestA},
		{"an identifier that could name a file elsewhere", "../elsewhere", digestA},
		{"an identifier in upper case", "A1B2C3D4", digestA},
		{"a digest that is too short", gameA, "0f1e2d3c"},
		{"a digest that is too long", gameA, digestA + "0"},
		{"a digest in upper case", gameA, "0F1E2D3C4B5A6978"},
		{"a digest that is not hexadecimal", gameA, "0f1e2d3c4b5a697g"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			b := openBoard(t, dir)
			err := b.Record(linked(result("Balint", "Bernadett", Win, day(1)), c.id, c.digest))
			if !errors.Is(err, ErrBadIdentity) {
				t.Fatalf("Record = %v, want ErrBadIdentity", err)
			}
			if got := b.History("Balint", 0); len(got) != 0 {
				t.Fatalf("the refused result was recorded anyway: %+v", got)
			}
		})
	}
}

// TestFirstWriteToAV1BoardKeepsItsRows is the migration. A board written before
// a result could name its saved game is read as it stands, and the first write
// that stamps the new version has to leave every row it loaded exactly as it
// found it — the rows and the ratings replayed from them.
//
// Inventing an identity for a legacy row would offer a replay of whichever
// saved game happened to match it; dropping or rewriting one would lose history
// somebody played. Neither is a migration.
func TestFirstWriteToAV1BoardKeepsItsRows(t *testing.T) {
	dir := t.TempDir()
	legacy := []Result{
		result("Balint", BotName("pro"), Win, day(1)),
		result("Balint", "Bernadett", Loss, day(2)),
		result("Bernadett", BotName("beginner"), Win, day(3)),
	}
	seeded, err := json.MarshalIndent(document{Version: 1, Results: legacy}, "", "  ")
	if err != nil {
		t.Fatalf("seeding a v1 board: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), append(seeded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the v1 board: %v", err)
	}

	b := openBoard(t, dir)
	before := map[string]Standing{}
	for _, name := range []string{"Balint", "Bernadett", BotName("pro"), BotName("beginner")} {
		before[name] = standing(t, b, name)
	}
	if err := b.Record(linked(result("Csilla", "Dora", Win, day(4)), gameA, digestA)); err != nil {
		t.Fatalf("recording a linked result onto a v1 board: %v", err)
	}

	data, _ := boardFile(t, dir)
	doc := storedDocument(t, data)
	if doc.Version != 2 {
		t.Errorf("the migrated file uses schema %d, want 2 so v1 readers refuse rather than strip identity", doc.Version)
	}
	if len(doc.Results) != len(legacy)+1 {
		t.Fatalf("the migrated file holds %d results, want %d", len(doc.Results), len(legacy)+1)
	}
	was, now := storedRows(t, seeded), storedRows(t, data)
	for i := range was {
		if !reflect.DeepEqual(was[i], now[i]) {
			t.Errorf("legacy row %d was written back as %v, want %v", i, now[i], was[i])
		}
	}
	for i, r := range doc.Results[:len(legacy)] {
		if r.GameID != "" || r.RecordDigest != "" || r.HasIdentity() {
			t.Errorf("legacy row %d was given an identity: %+v", i, r)
		}
	}
	if fresh := doc.Results[len(legacy)]; fresh.GameID != gameA || fresh.RecordDigest != digestA {
		t.Errorf("the new row names game %q/%q, want %q/%q", fresh.GameID, fresh.RecordDigest, gameA, digestA)
	}

	reopened := openBoard(t, dir)
	for name, want := range before {
		if got := standing(t, reopened, name); got != want {
			t.Errorf("%s stands at %+v after the migration, want %+v", name, got, want)
		}
	}
	if got := len(reopened.History("Balint", 0)); got != 2 {
		t.Errorf("Balint has %d games after the migration, want the 2 recorded before it", got)
	}
}

// TestHistoryKeepsTheIdentityOnBothSides: a game is recorded once, from one
// player's point of view, and the opponent's half of it is that row read
// backwards. Which side is reading decides the names, the outcome and the axis;
// it does not decide which saved game the row came from, so a player whose
// opponent recorded the game can still replay it.
func TestHistoryKeepsTheIdentityOnBothSides(t *testing.T) {
	b := openBoard(t, t.TempDir())
	if err := b.Record(linked(result("Balint", "Bernadett", Win, day(1)), gameA, digestA)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	for _, name := range []string{"Balint", "Bernadett"} {
		rows := b.History(name, 0)
		if len(rows) != 1 {
			t.Fatalf("%s has %d rows, want the one game", name, len(rows))
		}
		got := rows[0]
		if got.GameID != gameA || got.RecordDigest != digestA {
			t.Errorf("%s reads the game as %q/%q, want %q/%q", name, got.GameID, got.RecordDigest, gameA, digestA)
		}
		if !got.HasIdentity() {
			t.Errorf("%s is offered no replay of a game recorded with an identity: %+v", name, got)
		}
	}
	// The control: the two sides really are different readings of one row, so
	// the identity above was carried through a reversal rather than read from a
	// row that was never turned round.
	if mine, theirs := b.History("Balint", 0)[0], b.History("Bernadett", 0)[0]; mine.Outcome == theirs.Outcome {
		t.Errorf("both sides read the outcome as %q, want opposite outcomes", mine.Outcome)
	}
}

// TestRetryingTheSameFinalResultWritesNothing is what the identity is for. A
// finalisation that saved its game and then failed to record the result can be
// retried, and the retry has to be recognised rather than counted: the game was
// played once. It arrives with a later clock — the second attempt happens later
// and the screen had been open longer — and under whatever spelling of the
// names the profiles carry now, and it is still that game.
func TestRetryingTheSameFinalResultWritesNothing(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	for _, earlier := range []Result{
		result("Csilla", "Dora", DrawOutcome, day(1)),
		linked(result("Emese", "Ferenc", Win, day(1)), gameB, digestB),
	} {
		if err := b.Record(earlier); err != nil {
			t.Fatal(err)
		}
	}
	first := linked(result("Balint", "Bernadett", Win, day(1)), gameA, digestA)
	if err := b.Record(first); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ageBoardFile(t, dir)
	before, beforeMod := boardFile(t, dir)

	retry := linked(result("  balint ", "BERNADETT", Win, day(9)), gameA, digestA)
	retry.Duration = 4 * time.Hour
	if err := b.Record(retry); err != nil {
		t.Fatalf("retrying the same final result: %v", err)
	}

	after, afterMod := boardFile(t, dir)
	if !bytes.Equal(before, after) {
		t.Errorf("the recognised retry rewrote the board as\n%s\nwant\n%s", after, before)
	}
	if !afterMod.Equal(beforeMod) {
		t.Errorf("the recognised retry replaced the board file: modified %v, was %v", afterMod, beforeMod)
	}

	rows := b.History("Balint", 0)
	if len(rows) != 1 {
		t.Fatalf("the board holds %d rows for one game: %+v", len(rows), rows)
	}
	got := rows[0]
	if !got.Played.Equal(first.Played) {
		t.Errorf("Played = %v, want the first recording's %v", got.Played, first.Played)
	}
	if got.Duration != first.Duration {
		t.Errorf("Duration = %v, want the first recording's %v", got.Duration, first.Duration)
	}
	if got.Player != first.Player {
		t.Errorf("Player = %q, want the name the game was recorded under, %q", got.Player, first.Player)
	}
	if played := standing(t, b, "Balint").Played; played != 1 {
		t.Errorf("Balint is credited with %d games, want the one game played", played)
	}
}

// TestADifferentResultForARecordedGameIsRefused: one game has one result. A
// second, different one for the same saved game is a contradiction — a record
// that is not the one that was stored, or a result that does not describe the
// game that was recorded — and the store refuses it rather than choosing
// between them or keeping both. The recorded row is what a rating has already
// been replayed from, so it is left exactly as it is.
func TestADifferentResultForARecordedGameIsRefused(t *testing.T) {
	otherRuleset := result("Balint", "Bernadett", Win, day(1))
	otherRuleset.Ruleset = game.Classic3M.Canonical()
	otherSide := result("Balint", "Bernadett", Win, day(1))
	otherSide.Side = game.Horizontal.String()
	otherMoves := result("Balint", "Bernadett", Win, day(1))
	otherMoves.Moves = 7

	cases := []struct {
		name    string
		attempt Result
	}{
		{"another record for the same game", linked(result("Balint", "Bernadett", Win, day(1)), gameA, digestB)},
		{"the other outcome", linked(result("Balint", "Bernadett", Loss, day(1)), gameA, digestA)},
		{"another player", linked(result("Csilla", "Bernadett", Win, day(1)), gameA, digestA)},
		{"another opponent", linked(result("Balint", "Csilla", Win, day(1)), gameA, digestA)},
		{"another move count", linked(otherMoves, gameA, digestA)},
		{"another ruleset", linked(otherRuleset, gameA, digestA)},
		{"the other side", linked(otherSide, gameA, digestA)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			b := openBoard(t, dir)
			for _, earlier := range []Result{
				result("Csilla", "Dora", DrawOutcome, day(1)),
				linked(result("Emese", "Ferenc", Win, day(1)), gameB, digestB),
			} {
				if err := b.Record(earlier); err != nil {
					t.Fatal(err)
				}
			}
			first := linked(result("Balint", "Bernadett", Win, day(1)), gameA, digestA)
			if err := b.Record(first); err != nil {
				t.Fatalf("Record: %v", err)
			}
			ageBoardFile(t, dir)
			before, beforeMod := boardFile(t, dir)

			if err := b.Record(c.attempt); !errors.Is(err, ErrIdentityConflict) {
				t.Fatalf("Record = %v, want ErrIdentityConflict", err)
			}

			after, afterMod := boardFile(t, dir)
			if !bytes.Equal(before, after) {
				t.Errorf("the refused result changed the board to\n%s\nwant\n%s", after, before)
			}
			if !afterMod.Equal(beforeMod) {
				t.Errorf("the refused result replaced the board file: modified %v, was %v", afterMod, beforeMod)
			}
			rows := storedDocument(t, after).Results
			if len(rows) != 3 {
				t.Fatalf("the board holds %d rows, want the three original games: %+v", len(rows), rows)
			}
			if rows[2].RecordDigest != digestA || rows[2].Outcome != first.Outcome {
				t.Errorf("the stored row is %+v, want the one first recorded", rows[2])
			}
		})
	}
}

// TestEveryGameIsCreditedEvenWhenTwoLookAlike: the identifier is what says
// which game a row is about. A rematch allocates its own saved game and can
// replay to a record identical to the first game's, so two rows can share a
// record digest while being two games; two games with no identity at all can
// likewise be alike in every field a result holds. Neither may be folded into
// one, and neither is a conflict.
func TestEveryGameIsCreditedEvenWhenTwoLookAlike(t *testing.T) {
	b := openBoard(t, t.TempDir())
	shape := result("Balint", "Bernadett", Win, day(1))
	games := []Result{
		linked(shape, gameA, digestA),
		linked(shape, gameB, digestA),
		linked(shape, gameC, digestB),
	}
	for i, r := range games {
		if err := b.Record(r); err != nil {
			t.Fatalf("recording game %d: %v", i, err)
		}
	}
	if got := len(b.History("Balint", 0)); got != len(games) {
		t.Fatalf("the board holds %d rows, want %d distinct games", got, len(games))
	}
	if played := standing(t, b, "Balint").Played; played != len(games) {
		t.Errorf("Balint is credited with %d games, want %d", played, len(games))
	}
	seen := map[string]int{}
	for _, r := range b.History("Balint", 0) {
		seen[r.GameID]++
	}
	for _, want := range []string{gameA, gameB, gameC} {
		if seen[want] != 1 {
			t.Errorf("game %s appears %d times, want once: %v", want, seen[want], seen)
		}
	}
}

// Legacy/API rows without identity remain separate even when their fields match.
func TestResultsWithoutIdentityAreStillAppended(t *testing.T) {
	b := openBoard(t, t.TempDir())
	for i := range 2 {
		if err := b.Record(result("Balint", BotName("pro"), Win, day(1))); err != nil {
			t.Fatalf("recording game %d: %v", i, err)
		}
	}
	if got := len(b.History("Balint", 0)); got != 2 {
		t.Fatalf("the board holds %d rows, want both games", got)
	}
	if played := standing(t, b, "Balint").Played; played != 2 {
		t.Errorf("Balint is credited with %d games, want 2", played)
	}
}

// Malformed file-backed identity remains readable history, but cannot authorize
// replay or a second credited result for the ID it claims.
func TestAMalformedStoredIdentityIsHistoryWithoutAReplay(t *testing.T) {
	dir := t.TempDir()
	half := linked(result("Balint", "Bernadett", Win, day(1)), gameA, "")
	garbage := linked(result("Balint", "Csilla", Loss, day(2)), "not an identifier", "zzzz")
	data, err := json.MarshalIndent(document{Version: boardVersion, Results: []Result{half, garbage}}, "", "  ")
	if err != nil {
		t.Fatalf("seeding the board: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("writing the board: %v", err)
	}

	b := openBoard(t, dir)
	rows := b.History("Balint", 0)
	if len(rows) != 2 {
		t.Fatalf("the board holds %d rows, want both malformed ones read as history", len(rows))
	}
	for _, r := range rows {
		if r.HasIdentity() {
			t.Errorf("%+v is offered for replay although its identity is malformed", r)
		}
	}

	// The half-written row still occupies its game: a properly identified
	// result for it cannot be appended beside it on the strength of the half
	// that is there, because one of the two rows would be a second credit for a
	// game that was played once.
	if err := b.Record(linked(result("Balint", "Bernadett", Win, day(1)), gameA, digestA)); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("Record for a half-recorded game = %v, want ErrIdentityConflict", err)
	}
	// The control: a malformed row does not make the board unwritable.
	if err := b.Record(linked(result("Balint", "Dora", Win, day(3)), gameB, digestB)); err != nil {
		t.Fatalf("recording an unrelated game onto a board with malformed rows: %v", err)
	}
	if got := len(b.History("Balint", 0)); got != 3 {
		t.Fatalf("the board holds %d rows, want the two malformed ones and the new game", got)
	}
}
