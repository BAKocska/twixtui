package leaderboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// day is a fixed clock for recorded results, so history ordering is decided by
// the test rather than by how fast the machine runs it.
func day(n int) time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

func result(player, opponent string, outcome Outcome, played time.Time) Result {
	return Result{
		Played:   played,
		Player:   player,
		Opponent: opponent,
		Outcome:  outcome,
		Side:     game.Vertical.String(),
		Moves:    42,
		Ruleset:  game.Std.Canonical(),
		Duration: 7 * time.Minute,
	}
}

func openBoard(t *testing.T, dir string) *Board {
	t.Helper()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	return b
}

func standing(t *testing.T, b *Board, name string) Standing {
	t.Helper()
	for _, s := range b.Standings() {
		if foldKey(s.Name) == foldKey(name) {
			return s
		}
	}
	t.Fatalf("no standing for %q in %+v", name, b.Standings())
	return Standing{}
}

func TestDefaultDirHonoursEnvironmentOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/twixtui-test-config")
	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if dir != "/tmp/twixtui-test-config" {
		t.Fatalf("DefaultDir = %q, want the override used verbatim", dir)
	}
}

func TestRecordRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	b := openBoard(t, dir)

	want := result("Balint", BotName("pro"), Win, day(1))
	if err := b.Record(want); err != nil {
		t.Fatalf("Record: %v", err)
	}

	reopened := openBoard(t, dir)
	history := reopened.History("Balint", 0)
	if len(history) != 1 {
		t.Fatalf("History after reopen has %d rows, want 1", len(history))
	}
	got := history[0]
	if !got.Played.Equal(want.Played) {
		t.Errorf("Played = %v, want %v", got.Played, want.Played)
	}
	if got.Player != want.Player || got.Opponent != want.Opponent {
		t.Errorf("participants = %q vs %q, want %q vs %q", got.Player, got.Opponent, want.Player, want.Opponent)
	}
	if got.Outcome != want.Outcome || got.Side != want.Side || got.Moves != want.Moves {
		t.Errorf("got %+v, want outcome/side/moves of %+v", got, want)
	}
	if got.Ruleset != want.Ruleset {
		t.Errorf("Ruleset = %q, want %q", got.Ruleset, want.Ruleset)
	}
	if got.Duration != want.Duration {
		t.Errorf("Duration = %v, want %v", got.Duration, want.Duration)
	}
}

func TestRecordValidation(t *testing.T) {
	b := openBoard(t, t.TempDir())
	cases := []struct {
		name  string
		build func() Result
		want  error
	}{
		{"no player", func() Result {
			r := result("", "bot:pro", Win, day(1))
			return r
		}, ErrNoPlayer},
		{"no opponent", func() Result {
			r := result("Balint", "   ", Win, day(1))
			return r
		}, ErrNoPlayer},
		{"self opponent", func() Result {
			r := result("Balint", "balint", Win, day(1))
			return r
		}, ErrSelfOpponent},
		{"unknown outcome", func() Result {
			r := result("Balint", "bot:pro", Outcome("victory"), day(1))
			return r
		}, ErrBadOutcome},
		{"unknown side", func() Result {
			r := result("Balint", "bot:pro", Win, day(1))
			r.Side = "red"
			return r
		}, ErrBadSide},
		{"empty side", func() Result {
			r := result("Balint", "bot:pro", Win, day(1))
			r.Side = ""
			return r
		}, ErrBadSide},
		{"negative moves", func() Result {
			r := result("Balint", "bot:pro", Win, day(1))
			r.Moves = -1
			return r
		}, ErrNegativeMoves},
	}
	for _, c := range cases {
		if err := b.Record(c.build()); !errors.Is(err, c.want) {
			t.Errorf("%s: Record = %v, want %v", c.name, err, c.want)
		}
	}
	if got := len(b.History("Balint", 0)); got != 0 {
		t.Fatalf("board has %d rows, want 0 after rejected results", got)
	}
}

func TestRecordDefaultsTheTimestamp(t *testing.T) {
	b := openBoard(t, t.TempDir())
	r := result("Balint", BotName("pro"), Win, time.Time{})
	before := time.Now().Add(-time.Second)
	if err := b.Record(r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := b.History("Balint", 0)[0].Played
	if got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("Played = %v, want it defaulted to now", got)
	}
}

func TestHistoryIsMostRecentFirstAndHonoursLimit(t *testing.T) {
	b := openBoard(t, t.TempDir())
	// Recorded out of chronological order on purpose: history is ordered by
	// when the game was played, not by when it was written.
	for _, n := range []int{2, 5, 1, 4, 3} {
		if err := b.Record(result("Balint", BotName("pro"), Win, day(n))); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	history := b.History("balint", 0)
	if len(history) != 5 {
		t.Fatalf("History has %d rows, want 5", len(history))
	}
	for i := range history[1:] {
		if history[i].Played.Before(history[i+1].Played) {
			t.Fatalf("History not most recent first: %v then %v", history[i].Played, history[i+1].Played)
		}
	}
	if !history[0].Played.Equal(day(5)) {
		t.Fatalf("History[0] played %v, want %v", history[0].Played, day(5))
	}
	if got := b.History("Balint", 2); len(got) != 2 || !got[0].Played.Equal(day(5)) {
		t.Fatalf("History with limit 2 = %+v, want the two most recent", got)
	}
	if got := b.History("nobody", 0); len(got) != 0 {
		t.Fatalf("History for an unknown name = %+v, want nothing", got)
	}
}

func TestHistoryFindsThePlayerOnEitherSide(t *testing.T) {
	b := openBoard(t, t.TempDir())
	if err := b.Record(result("Balint", "Bernadett", Win, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := len(b.History("Bernadett", 0)); got != 1 {
		t.Fatalf("History for the opponent has %d rows, want 1", got)
	}
}

func TestStandingsCreditBothSidesOfOneRow(t *testing.T) {
	b := openBoard(t, t.TempDir())
	rows := []Result{
		result("Balint", "Bernadett", Win, day(1)),
		result("Balint", "Bernadett", Loss, day(2)),
		result("Balint", "Bernadett", DrawOutcome, day(3)),
	}
	for _, r := range rows {
		if err := b.Record(r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	balint := standing(t, b, "Balint")
	if balint.Played != 3 || balint.Won != 1 || balint.Lost != 1 || balint.Drawn != 1 {
		t.Fatalf("Balint = %+v, want 3 played 1/1/1", balint)
	}
	bernadett := standing(t, b, "Bernadett")
	if bernadett.Played != 3 || bernadett.Won != 1 || bernadett.Lost != 1 || bernadett.Drawn != 1 {
		t.Fatalf("Bernadett = %+v, want the mirrored record", bernadett)
	}
	if balint.WinRate != 0.5 || bernadett.WinRate != 0.5 {
		t.Fatalf("score rates = %v and %v, want 0.5 each", balint.WinRate, bernadett.WinRate)
	}
	// Rating exchanges between two rated players are equal and opposite, so the
	// pair's total is conserved whatever the results were. The individual
	// ratings do not come exactly back to the seed: Elo is order-dependent,
	// because the loss was played at a different rating gap than the win.
	if sum := balint.Rating + bernadett.Rating; sum < 2*StartRating-1 || sum > 2*StartRating+1 {
		t.Fatalf("ratings %d and %d total %d, want %d conserved between two rated players",
			balint.Rating, bernadett.Rating, sum, 2*StartRating)
	}
	for _, s := range []Standing{balint, bernadett} {
		if s.Rating < StartRating-ProvisionalK || s.Rating > StartRating+ProvisionalK {
			t.Fatalf("%s rating = %d, want an even record to stay within one K of the seed", s.Name, s.Rating)
		}
	}
}

func TestStandingsRankBestFirst(t *testing.T) {
	b := openBoard(t, t.TempDir())
	rows := []Result{
		result("Winner", "Loser", Win, day(1)),
		result("Winner", "Loser", Win, day(2)),
		result("Winner", "Loser", Win, day(3)),
	}
	for _, r := range rows {
		if err := b.Record(r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got := b.Standings()
	if len(got) != 2 {
		t.Fatalf("Standings has %d rows, want 2", len(got))
	}
	if got[0].Name != "Winner" || got[1].Name != "Loser" {
		t.Fatalf("Standings order = %q, %q; want the higher rating first", got[0].Name, got[1].Name)
	}
	if got[0].Rating <= got[1].Rating {
		t.Fatalf("ratings %d and %d are not ordered", got[0].Rating, got[1].Rating)
	}
}

func TestBotRatingIsAnAnchorAndDoesNotDrift(t *testing.T) {
	b := openBoard(t, t.TempDir())
	for n := range 20 {
		if err := b.Record(result("Balint", BotName("pro"), Win, day(n))); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	bot := standing(t, b, BotName("pro"))
	if bot.Rating != botRatings["pro"] {
		t.Fatalf("pro bot rating = %d after twenty losses, want the fixed anchor %d", bot.Rating, botRatings["pro"])
	}
	if bot.Played != 20 || bot.Lost != 20 {
		t.Fatalf("pro bot record = %+v, want 20 played and 20 lost", bot)
	}
	if human := standing(t, b, "Balint"); human.Rating <= StartRating {
		t.Fatalf("player rating = %d after twenty wins over the pro bot, want it above %d", human.Rating, StartRating)
	}
}

func TestUnknownBotTierIsAnchoredAtTheSeed(t *testing.T) {
	b := openBoard(t, t.TempDir())
	if err := b.Record(result("Balint", BotName("grandmaster"), Win, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	bot := standing(t, b, BotName("grandmaster"))
	if bot.Rating != StartRating {
		t.Fatalf("unknown tier rating = %d, want the seed %d", bot.Rating, StartRating)
	}
}

func TestRemoteOpponentIsRated(t *testing.T) {
	b := openBoard(t, t.TempDir())
	if err := b.Record(result("Balint", RemoteName("kata"), Loss, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	remote := standing(t, b, RemoteName("kata"))
	if remote.Rating <= StartRating {
		t.Fatalf("remote opponent rating = %d after a win, want it above the seed", remote.Rating)
	}
	if DisplayName(remote.Name) != "kata" {
		t.Fatalf("DisplayName(%q) = %q, want kata", remote.Name, DisplayName(remote.Name))
	}
	if IsBot(remote.Name) {
		t.Fatalf("IsBot(%q) = true, want false", remote.Name)
	}
}

func TestReset(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	if err := b.Record(result("Balint", BotName("pro"), Win, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := b.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := b.Standings(); len(got) != 0 {
		t.Fatalf("Standings after Reset = %+v, want nothing", got)
	}
	if got := openBoard(t, dir).History("Balint", 0); len(got) != 0 {
		t.Fatalf("History after reopening a reset board = %+v, want nothing", got)
	}
}

// TestRepeatedOpenWriteCyclesKeepEveryResult is the durability check: every
// cycle opens the board fresh, records one game and closes, which is what a run
// of twixtui does. Nothing may be lost or corrupted along the way.
func TestRepeatedOpenWriteCyclesKeepEveryResult(t *testing.T) {
	dir := t.TempDir()
	const cycles = 60
	for i := range cycles {
		b, err := Open(dir)
		if err != nil {
			t.Fatalf("cycle %d: Open: %v", i, err)
		}
		if got := len(b.History("Balint", 0)); got != i {
			t.Fatalf("cycle %d: board has %d rows, want %d", i, got, i)
		}
		if err := b.Record(result("Balint", BotName("pro"), Win, day(i))); err != nil {
			t.Fatalf("cycle %d: Record: %v", i, err)
		}
	}

	final := openBoard(t, dir)
	if got := len(final.History("Balint", 0)); got != cycles {
		t.Fatalf("final board has %d rows, want %d", got, cycles)
	}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("reading board file: %v", err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("board file does not parse: %v", err)
	}
	if doc.Version != boardVersion {
		t.Fatalf("board file version = %d, want %d", doc.Version, boardVersion)
	}
	if len(doc.Results) != cycles {
		t.Fatalf("board file holds %d results, want %d", len(doc.Results), cycles)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temporary file %q left behind", e.Name())
		}
	}
}

// TestConcurrentRecordAcrossBoards exercises the advisory lock: each goroutine
// has its own Board over the same directory, which is how two twixtui processes
// see it, so the in-process mutex alone would not save the results.
func TestConcurrentRecordAcrossBoards(t *testing.T) {
	dir := t.TempDir()
	const (
		writers = 8
		each    = 5
	)
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			b, err := Open(dir)
			if err != nil {
				errs <- err
				return
			}
			for i := range each {
				r := result(fmt.Sprintf("player%d", w), BotName("pro"), Win, day(w*each+i))
				if err := b.Record(r); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Record: %v", err)
	}

	final := openBoard(t, dir)
	for w := range writers {
		name := fmt.Sprintf("player%d", w)
		if got := len(final.History(name, 0)); got != each {
			t.Fatalf("%s has %d results, want %d: a concurrent writer lost rows", name, got, each)
		}
	}
	if got := standing(t, final, BotName("pro")).Played; got != writers*each {
		t.Fatalf("bot played %d games, want %d", got, writers*each)
	}
}

func TestConcurrentRecordOnOneBoard(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	const writers = 16
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			if err := b.Record(result(fmt.Sprintf("player%d", w), BotName("pro"), Win, day(w))); err != nil {
				t.Errorf("Record: %v", err)
			}
		}(w)
	}
	wg.Wait()
	if got := standing(t, b, BotName("pro")).Played; got != writers {
		t.Fatalf("bot played %d games, want %d", got, writers)
	}
}

func TestReadsSeeAnotherBoardsWrites(t *testing.T) {
	dir := t.TempDir()
	reader := openBoard(t, dir)
	writer := openBoard(t, dir)

	if got := len(reader.Standings()); got != 0 {
		t.Fatalf("fresh board has %d standings, want 0", got)
	}
	if err := writer.Record(result("Balint", BotName("pro"), Win, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := len(reader.History("Balint", 0)); got != 1 {
		t.Fatalf("reader saw %d rows, want the other board's write", got)
	}
}

func TestCorruptFileIsReportedNotDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seeding corrupt file: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open on a corrupt file succeeded, want an error rather than a silent reset")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt file was removed: %v", err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	data, err := json.Marshal(document{Version: boardVersion + 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o600); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	_, err = Open(dir)
	if err == nil {
		t.Fatal("Open on a newer schema succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "newer twixtui") {
		t.Fatalf("error = %v, want it to name the version mismatch", err)
	}
}

func TestWriteSweepsStaleTempsButNotFreshOnes(t *testing.T) {
	dir := t.TempDir()
	b := openBoard(t, dir)
	stale := filepath.Join(dir, fileName+".tmp-crashed")
	fresh := filepath.Join(dir, fileName+".tmp-inflight")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("residue"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", p, err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("ageing %s: %v", stale, err)
	}

	if err := b.Record(result("Balint", BotName("pro"), Win, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary file survived a write: %v", err)
	}
	// A temporary file that could still belong to a writer in flight must be
	// left alone.
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent temporary file was removed: %v", err)
	}
}
