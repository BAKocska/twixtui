// Package leaderboard records finished games and ranks the players who played
// them.
//
// The store is one JSON file of results under the user's configuration
// directory. Ratings are not stored: they are replayed from the result log
// whenever standings are asked for, so the log is the single source of truth and
// a change to the rating parameters cannot leave stale numbers behind.
package leaderboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// fileName is the board's file within the configuration directory.
const fileName = "leaderboard.json"

// boardVersion is the on-disk schema version. A file written by a newer twixtui
// is refused rather than parsed on a best-effort basis, so an older binary
// cannot silently drop fields it does not understand when it writes the file
// back.
const boardVersion = 1

// Recording errors. Callers match these with errors.Is.
var (
	ErrNoPlayer      = errors.New("result has no player")
	ErrSelfOpponent  = errors.New("result has the same player on both sides")
	ErrBadOutcome    = errors.New("result has an unknown outcome")
	ErrBadSide       = errors.New("result has an unknown side")
	ErrNegativeMoves = errors.New("result has a negative move count")
)

// Outcome is a game's result from the recording player's point of view.
type Outcome string

// Possible outcomes.
const (
	Win         Outcome = "win"
	Loss        Outcome = "loss"
	DrawOutcome Outcome = "draw"
)

// score is the Elo score an outcome is worth: a draw is worth half a game,
// which is the convention the rating maths assumes.
func (o Outcome) score() (float64, bool) {
	switch o {
	case Win:
		return 1, true
	case Loss:
		return 0, true
	case DrawOutcome:
		return 0.5, true
	}
	return 0, false
}

// Result is one finished game.
//
// A game produces exactly one Result, recorded from Player's point of view.
// Both sides are credited from that single row — the opponent's games, wins and
// rating all come from reading the row backwards — so recording a hotseat game
// twice, once per player, would count it twice.
type Result struct {
	Played   time.Time     `json:"played"`
	Player   string        `json:"player"`   // profile name
	Opponent string        `json:"opponent"` // profile name, or "bot:pro", or "remote:<name>"
	Outcome  Outcome       `json:"outcome"`
	Side     string        `json:"side"` // "vertical" or "horizontal"
	Moves    int           `json:"moves"`
	Ruleset  string        `json:"ruleset"` // game.Ruleset.Canonical()
	Duration time.Duration `json:"duration"`
}

// reversed is the same game as the other side played it: the two names swap,
// the outcome flips, and the side becomes the other axis. Everything else — when
// it was played, how long it took, how many moves it ran to — is a fact about
// the game rather than about either player, so it is untouched.
//
// An outcome this build cannot score is left as it is: there is nothing to flip
// it to, and inventing one would be worse than showing the word it was recorded
// under.
func (r Result) reversed() Result {
	r.Player, r.Opponent = r.Opponent, r.Player
	switch r.Outcome {
	case Win:
		r.Outcome = Loss
	case Loss:
		r.Outcome = Win
	}
	if side, err := game.ParsePlayer(r.Side); err == nil {
		r.Side = side.Opponent().String()
	}
	return r
}

// Standing is one participant's line on the board.
type Standing struct {
	Name   string
	Played int
	Won    int
	Lost   int
	Drawn  int
	Rating int
	// WinRate is the score rate: (wins + half the draws) / played. It is the
	// same quantity the rating is derived from, so a board sorted by rating
	// never disagrees with the rate beside it. Label it "score", not "wins".
	WinRate float64
}

// Standings is the board as it is meant to be read: the people, ranked against
// one another, and the bots they played, which are not ranked.
//
// The two are separate because a bot's rating is a program constant rather than
// something it won. Ranking a constant against an earned rating misleads in both
// directions: a player who has lost every game still sorts above any tier
// anchored below the seed, and a tier looks as though it gained hundreds of
// points the first time anybody played it. Neither number means what a ranking
// column implies it means, so they are not put in one column.
type Standings struct {
	// Players are the rated participants, best first. A networked opponent is
	// one of these: their rating moves with their results like anyone else's.
	Players []Standing
	// Bots are the fixed-rating opponents, strongest first. Their Rating is the
	// tier's anchor and never moves, whatever their record says.
	Bots []Standing
}

// document is the on-disk shape of the board.
type document struct {
	Version int      `json:"version"`
	Results []Result `json:"results"`
}

// Board is the recorded history of games on this machine.
//
// A Board is safe for concurrent use. Recording reloads the file inside an
// advisory lock before appending, so a second twixtui process cannot lose the
// first one's results; reads reload only when the file has changed underneath.
type Board struct {
	mu       sync.Mutex
	path     string
	lockPath string
	stamp    stamp
	results  []Result
}

// Open loads the board in dir, creating the directory on first use. An empty dir
// means the default configuration directory.
func Open(dir string) (*Board, error) {
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
	b := &Board{
		path:     filepath.Join(dir, fileName),
		lockPath: filepath.Join(dir, fileName+".lock"),
	}
	if err := b.loadShared(); err != nil {
		return nil, err
	}
	return b, nil
}

// Path reports the file the board reads and writes, for diagnostics.
func (b *Board) Path() string { return b.path }

// loadShared reloads the file under a shared advisory lock, so a read never
// observes a writer's temporary file or a partially replaced entry.
func (b *Board) loadShared() error {
	release, err := lockFile(b.lockPath, false)
	if err != nil {
		return err
	}
	defer release()
	return b.read()
}

// read replaces the in-memory snapshot from disk. The caller holds the advisory
// lock; nesting a second lock acquisition here would deadlock a writer.
func (b *Board) read() error {
	data, st, err := readFile(b.path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		b.results, b.stamp = nil, st
		return nil
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", b.path, err)
	}
	if doc.Version > boardVersion {
		return fmt.Errorf("%s was written by a newer twixtui (schema %d, this build understands %d)", b.path, doc.Version, boardVersion)
	}
	b.results, b.stamp = doc.Results, st
	return nil
}

// refresh reloads when another process has replaced the file. Read methods
// return no error, so a failed refresh keeps the last good snapshot rather than
// presenting an empty board.
func (b *Board) refresh() {
	if statStamp(b.path).same(b.stamp) {
		return
	}
	_ = b.loadShared()
}

// mutate applies fn to the stored results and writes the result. The reload
// inside the lock is what makes concurrent writers additive instead of
// last-write-wins.
func (b *Board) mutate(fn func(*[]Result) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	release, err := lockFile(b.lockPath, true)
	if err != nil {
		return err
	}
	defer release()
	if err := b.read(); err != nil {
		return err
	}
	results := append([]Result(nil), b.results...)
	if err := fn(&results); err != nil {
		return err
	}
	data, err := marshal(results)
	if err != nil {
		return err
	}
	if err := atomicWrite(b.path, data); err != nil {
		return err
	}
	b.results = results
	b.stamp = statStamp(b.path)
	return nil
}

func marshal(results []Result) ([]byte, error) {
	doc := document{Version: boardVersion, Results: results}
	if doc.Results == nil {
		doc.Results = []Result{}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding results: %w", err)
	}
	return append(data, '\n'), nil
}

// Record appends one finished game. Call it once per game, not once per player.
func (b *Board) Record(r Result) error {
	if err := normalise(&r); err != nil {
		return err
	}
	return b.mutate(func(rs *[]Result) error {
		*rs = append(*rs, r)
		return nil
	})
}

// normalise validates a result and fills in what can be defaulted. An unplayed
// timestamp becomes now: the caller that forgot to set it still gets a row in
// the right place in history rather than one dated to the zero year.
func normalise(r *Result) error {
	r.Player = strings.TrimSpace(r.Player)
	r.Opponent = strings.TrimSpace(r.Opponent)
	if r.Player == "" {
		return ErrNoPlayer
	}
	if r.Opponent == "" {
		return fmt.Errorf("opponent of %q is empty: %w", r.Player, ErrNoPlayer)
	}
	if foldKey(r.Player) == foldKey(r.Opponent) {
		return fmt.Errorf("%q: %w", r.Player, ErrSelfOpponent)
	}
	if _, ok := r.Outcome.score(); !ok {
		return fmt.Errorf("%q: %w", r.Outcome, ErrBadOutcome)
	}
	if r.Side != game.Vertical.String() && r.Side != game.Horizontal.String() {
		return fmt.Errorf("%q: %w", r.Side, ErrBadSide)
	}
	if r.Moves < 0 {
		return fmt.Errorf("%d moves: %w", r.Moves, ErrNegativeMoves)
	}
	if r.Played.IsZero() {
		r.Played = time.Now()
	}
	r.Played = r.Played.UTC()
	return nil
}

// Reset discards every recorded result.
func (b *Board) Reset() error {
	return b.mutate(func(rs *[]Result) error {
		*rs = nil
		return nil
	})
}

// History returns the games a participant played, most recent first, each read
// from that participant's own side of the board. A limit of zero or less returns
// all of them. The name matches either side of a result, so a hotseat opponent
// sees the game too.
//
// A game is recorded once, from its Player's point of view, and the other half
// of it is that same row read backwards — the reading the standings already use
// to credit both sides. Turning the row round belongs here and not in the
// caller: a caller that forgets shows a player as their own opponent, with the
// other side's result and the other side's axis.
func (b *Board) History(name string, limit int) []Result {
	key := foldKey(name)
	b.mu.Lock()
	b.refresh()
	out := make([]Result, 0, len(b.results))
	for _, r := range b.results {
		switch key {
		case foldKey(r.Player):
			out = append(out, r)
		case foldKey(r.Opponent):
			out = append(out, r.reversed())
		}
	}
	b.mu.Unlock()

	sort.SliceStable(out, func(i, j int) bool { return out[i].Played.After(out[j].Played) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// tally accumulates one participant's record while the log is replayed.
type tally struct {
	name    string
	played  int
	won     int
	lost    int
	drawn   int
	rating  float64
	rated   bool // false for bots, whose rating is a fixed anchor
	games   int  // games played so far, which selects the K factor
	arrived int  // first appearance, for a deterministic tie-break
}

// Standings replays the log and returns the board: rated players ranked best
// first, and the fixed-rating bots they met, listed apart from them.
//
// Ratings are replayed in the order the games were played. Both sides of a
// result are updated when both are rated; a bot keeps its anchor rating and only
// its human opponent moves. Which side of the table a participant lands on is
// decided by the same fact the rating maths uses, so the split cannot drift away
// from what the numbers mean.
func (b *Board) Standings() Standings {
	b.mu.Lock()
	b.refresh()
	results := append([]Result(nil), b.results...)
	b.mu.Unlock()

	sort.SliceStable(results, func(i, j int) bool { return results[i].Played.Before(results[j].Played) })

	byName := make(map[string]*tally, len(results))
	order := make([]*tally, 0, len(results))
	participant := func(name string) *tally {
		key := foldKey(name)
		if t, ok := byName[key]; ok {
			return t
		}
		t := &tally{name: name, rating: StartRating, rated: true, arrived: len(order)}
		if anchor, fixed := anchorRating(name); fixed {
			t.rating, t.rated = float64(anchor), false
		}
		byName[key] = t
		order = append(order, t)
		return t
	}

	for _, r := range results {
		score, ok := r.Outcome.score()
		if !ok {
			// A row with an outcome this build does not understand cannot be
			// scored. Skipping it keeps the rest of the board readable.
			continue
		}
		player := participant(r.Player)
		opponent := participant(r.Opponent)

		player.played++
		opponent.played++
		switch r.Outcome {
		case Win:
			player.won++
			opponent.lost++
		case Loss:
			player.lost++
			opponent.won++
		case DrawOutcome:
			player.drawn++
			opponent.drawn++
		}

		// Both deltas come from the ratings as they were before this game, so
		// the order the two sides are applied in cannot matter.
		expected := expectedScore(player.rating, opponent.rating)
		playerDelta := kFactor(player.games) * (score - expected)
		opponentDelta := kFactor(opponent.games) * (expected - score)
		if player.rated {
			player.rating += playerDelta
		}
		if opponent.rated {
			opponent.rating += opponentDelta
		}
		player.games++
		opponent.games++
	}

	var out Standings
	for _, t := range order {
		s := Standing{
			Name:   t.name,
			Played: t.played,
			Won:    t.won,
			Lost:   t.lost,
			Drawn:  t.drawn,
			Rating: int(math.Round(t.rating)),
		}
		if t.played > 0 {
			s.WinRate = (float64(t.won) + 0.5*float64(t.drawn)) / float64(t.played)
		}
		if t.rated {
			out.Players = append(out.Players, s)
		} else {
			out.Bots = append(out.Bots, s)
		}
	}
	arrived := func(s Standing) int { return byName[foldKey(s.Name)].arrived }
	sort.SliceStable(out.Players, func(i, j int) bool {
		x, y := out.Players[i], out.Players[j]
		if x.Rating != y.Rating {
			return x.Rating > y.Rating
		}
		if x.WinRate != y.WinRate {
			return x.WinRate > y.WinRate
		}
		if x.Played != y.Played {
			return x.Played > y.Played
		}
		return arrived(x) < arrived(y)
	})
	// Bots are ordered by the one thing that distinguishes them: how hard they
	// play. That is the anchor, so this is a list of tiers, not a ranking of
	// results.
	sort.SliceStable(out.Bots, func(i, j int) bool {
		x, y := out.Bots[i], out.Bots[j]
		if x.Rating != y.Rating {
			return x.Rating > y.Rating
		}
		return arrived(x) < arrived(y)
	})
	return out
}

// foldKey is the identity of a participant name: lower-cased with runs of
// whitespace collapsed. It matches the profile store's duplicate detection, so
// results recorded under "Balint" and "balint" belong to one player.
func foldKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
