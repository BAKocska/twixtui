package bot

// This file is the effort experiment: the opt-in, reproducible measurement of
// what the search actually costs and what the alternatives buy. Nothing here
// runs in an ordinary test run, and nothing here is production code.
//
// It answers two different questions with two different modes, because they
// need different discipline:
//
//   - positions: fixed, seeded midgame positions, each candidate given the same
//     position and its own work horizon. This is where a semantics-preserving
//     claim is settled: at equal depth the answers and values have to agree,
//     and only the node and evaluation counts may differ. Timing here is a
//     single-process, single-worker, sequential measurement.
//   - match: paired openings played out, each opening played from both sides.
//     This is where a strength claim is settled, and the statistical unit is
//     the opening pair, never the individual game.
//
// Both modes write one self-contained JSON artifact: the protocol, the guards
// every contender actually ran under, every game's moves, digest, winner and
// end reason, and per move the nodes, evaluations, depth, search-reported
// elapsed time and stop reason. A bot error or an illegal move aborts the run
// and is recorded as an abort, together with the games and moves that had
// already been played: it is never converted into a loss or a draw, and the
// receipts of what did happen are never thrown away with it. A game that hits
// the ply cap is recorded as truncated and is left out of the score entirely,
// because an unfinished game is not a draw and its winner is unknown.
//
// In positions mode a requested ply count the setup game never reaches is
// recorded as the terminal position the game ended in, under the ply that was
// requested, and nothing is measured on it. A run in which no position was
// measurable is an error, not a result.
//
// Limits are configurable per candidate, so two tiers can be compared at their
// own horizons and two search variants at one shared horizon. Every search
// carries several guards at once, and only the stop reason recorded with a
// search says which of them ended it.
//
// Example runs (the gate variable has to be set for the experiment to do
// anything at all):
//
//	# smoke: two openings, one small board, shallow deterministic horizon
//	TWIXT_BOT_EFFORT=1 TWIXT_BOT_EFFORT_SIZES=10 \
//	  TWIXT_BOT_EFFORT_MIDGAME_PLIES=10 TWIXT_BOT_EFFORT_SEED_COUNT=2 \
//	  TWIXT_BOT_EFFORT_DEPTH=3 \
//	  go test ./internal/bot -run TestBotEffortExperiment -count=1 -timeout 30m
//
//	# development positions, work-bounded, deterministic
//	TWIXT_BOT_EFFORT=1 TWIXT_BOT_EFFORT_MODE=positions \
//	  TWIXT_BOT_EFFORT_CANDIDATES=plain,pvs TWIXT_BOT_EFFORT_SIZES=10,16,24 \
//	  TWIXT_BOT_EFFORT_MIDGAME_PLIES=10,16,24 \
//	  TWIXT_BOT_EFFORT_SEED_START=1 TWIXT_BOT_EFFORT_SEED_COUNT=12 \
//	  TWIXT_BOT_EFFORT_DEPTH=6 TWIXT_BOT_EFFORT_TIME=1h \
//	  TWIXT_BOT_EFFORT_OUT=.work/effort-dev-positions.json \
//	  go test ./internal/bot -run TestBotEffortExperiment -count=1 -timeout 6h
//
//	# development match, work-bounded, per-side horizons need not be equal
//	TWIXT_BOT_EFFORT=1 TWIXT_BOT_EFFORT_MODE=match \
//	  TWIXT_BOT_EFFORT_CANDIDATES=pro,max TWIXT_BOT_EFFORT_SIZES=10,16,24 \
//	  TWIXT_BOT_EFFORT_SEED_START=1 TWIXT_BOT_EFFORT_SEED_COUNT=12 \
//	  TWIXT_BOT_EFFORT_TIME='*=1h' TWIXT_BOT_EFFORT_DEPTH='pro=6,max=8' \
//	  TWIXT_BOT_EFFORT_OUT=.work/effort-dev-match.json \
//	  go test ./internal/bot -run TestBotEffortExperiment -count=1 -timeout 12h
//
//	# monte-carlo candidate, bounded by its simulation count
//	TWIXT_BOT_EFFORT=1 TWIXT_BOT_EFFORT_MODE=match \
//	  TWIXT_BOT_EFFORT_CANDIDATES=pvs,mcts TWIXT_BOT_EFFORT_SIZES=10 \
//	  TWIXT_BOT_EFFORT_NODES='pvs=200000' TWIXT_BOT_EFFORT_TIME='*=1h' \
//	  TWIXT_BOT_EFFORT_ITERATIONS='mcts=2000' \
//	  go test ./internal/bot -run TestBotEffortExperiment -count=1 -timeout 6h
//
//	# those two bounds are different counters: a node cap on the alpha-beta
//	# candidate and a simulation cap on the monte-carlo one are not a matched
//	# work horizon, and the artifact records both as what they are rather than
//	# equating them
//
//	# holdout: the same command with a disjoint opening seed range and nothing
//	# retuned against it
//	TWIXT_BOT_EFFORT_SEED_START=101 TWIXT_BOT_EFFORT_SEED_COUNT=12
//
// The frozen-baseline comparison is deliberately not done from here: the
// baseline tree does not have the levers this file sets, so the baseline
// evaluation and search outputs are captured separately with an overlaid probe
// and compared against the pvs-disabled candidate.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

const (
	// ebGate is the variable that has to be 1 for the experiment to run.
	ebGate = "TWIXT_BOT_EFFORT"
	// ebSchema versions the artifact so a consumer can tell two runs apart.
	ebSchema = "twixt-bot-effort/1"

	ebEnvMode       = "TWIXT_BOT_EFFORT_MODE"
	ebEnvCandidates = "TWIXT_BOT_EFFORT_CANDIDATES"
	ebEnvSizes      = "TWIXT_BOT_EFFORT_SIZES"
	ebEnvSeedStart  = "TWIXT_BOT_EFFORT_SEED_START"
	ebEnvSeedCount  = "TWIXT_BOT_EFFORT_SEED_COUNT"
	ebEnvNodes      = "TWIXT_BOT_EFFORT_NODES"
	ebEnvDepth      = "TWIXT_BOT_EFFORT_DEPTH"
	ebEnvTime       = "TWIXT_BOT_EFFORT_TIME"
	ebEnvIterations = "TWIXT_BOT_EFFORT_ITERATIONS"
	ebEnvMidgame    = "TWIXT_BOT_EFFORT_MIDGAME_PLIES"
	ebEnvPlyCap     = "TWIXT_BOT_EFFORT_PLY_CAP"
	ebEnvOut        = "TWIXT_BOT_EFFORT_OUT"
)

// ebEnvNames is every variable the experiment reads, so the artifact can record
// exactly what it was asked for.
var ebEnvNames = []string{
	ebGate, ebEnvMode, ebEnvCandidates, ebEnvSizes, ebEnvSeedStart, ebEnvSeedCount,
	ebEnvNodes, ebEnvDepth, ebEnvTime, ebEnvIterations, ebEnvMidgame,
	ebEnvPlyCap, ebEnvOut,
}

const (
	// ebNoClock is the guard a work-bounded run leaves the clock at. The clock
	// is not abolished: a pathological position still has to stop, but an hour
	// is far beyond any horizon this harness sets, so the node and depth bounds
	// are what actually decide the work and the result is reproducible.
	ebNoClock = time.Hour
	// ebProbeDepth is the depth a position probe falls back to when no node or
	// depth bound was asked for. A probe with neither would inherit the tier's
	// nominal ceiling and spend its whole clock, which is the one thing a
	// deterministic comparison must not do.
	ebProbeDepth = 4
	// ebSeedOffset separates the two sides' seeds inside one game.
	ebSeedOffset = 1_000_003
	// ebOpeningKey keys the opening permutation. It is a constant and not a
	// seed on purpose: the permutation must not move when the seed range moves,
	// or a holdout range would not be comparable with a development range.
	ebOpeningKey = 0x7bb1_2a6f_c001_d10e
	// ebConfidence is the 2/alpha of the paired bound below, for alpha = 0.05.
	ebConfidence = 40.0
)

type ebMode string

const (
	ebModePositions ebMode = "positions"
	ebModeMatch     ebMode = "match"
)

// ebKind is how a candidate is built. It matters to the artifact: a candidate
// built through the exported constructor measures the shipped path, and one
// built from tier params with a lever moved measures a lever.
type ebKind string

const (
	ebKindPreset ebKind = "exported NewWithLimits"
	ebKindTuned  ebKind = "tier params with one lever moved"
	ebKindMCTS   ebKind = "monte-carlo experiment"
)

// ebCandidateSpec names one contender.
type ebCandidateSpec struct {
	name string
	tier Tier
	kind ebKind
	// tune moves the levers that define a tuned candidate. It runs after the
	// configured limits are applied, so a candidate's own lever always wins.
	tune func(*params)
}

// ebCandidates is the whole roster the experiment can run. The four preset
// candidates measure the shipped tiers; plain and pvs are the same tier with
// the principal-variation lever off and on, which is the pair a
// semantics-preserving claim is made from; pvs-templates and pvs-notemplates
// are the same tier with the proved edge-template corpus in and out of the
// evaluation, which is the pair a strength claim about the corpus is made from;
// mcts is the different architecture.
//
// Both sides of the templates pair set the lever rather than one of them
// leaning on the tier's own value. No tier ships it on, so a candidate that
// took the tier's value would silently make the pair two copies of the same
// contender, and the match would report a dead heat as a result.
func ebCandidates() []ebCandidateSpec {
	return []ebCandidateSpec{
		{name: "beginner", tier: Beginner, kind: ebKindPreset},
		{name: "intermediate", tier: Intermediate, kind: ebKindPreset},
		{name: "pro", tier: Pro, kind: ebKindPreset},
		{name: "max", tier: Max, kind: ebKindPreset},
		{name: "plain", tier: Pro, kind: ebKindTuned, tune: func(p *params) { p.pvs = false }},
		{name: "pvs", tier: Pro, kind: ebKindTuned, tune: func(p *params) { p.pvs = true }},
		{name: "pvs-templates", tier: Pro, kind: ebKindTuned, tune: func(p *params) { p.pvs, p.templates = true, true }},
		{name: "pvs-notemplates", tier: Pro, kind: ebKindTuned, tune: func(p *params) { p.pvs, p.templates = true, false }},
		{name: "mcts", tier: Pro, kind: ebKindMCTS},
	}
}

func ebCandidateNames() []string {
	all := ebCandidates()
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, c.name)
	}
	return out
}

func ebLookupCandidate(name string) (ebCandidateSpec, error) {
	for _, c := range ebCandidates() {
		if c.name == name {
			return c, nil
		}
	}
	return ebCandidateSpec{}, fmt.Errorf("unknown candidate %q, want one of %s",
		name, strings.Join(ebCandidateNames(), ", "))
}

// ebOverride is a setting that may be given once for every candidate, or per
// candidate, or both. Per-candidate limits are the point: comparing two tiers
// at their own horizons is a different question from comparing two search
// variants at one horizon, and both have to be expressible.
//
// The syntax is a comma list of items. An item without "=" or written as
// "*=v" is the value for every candidate; an item "name=v" overrides that one
// candidate.
type ebOverride[T any] struct {
	def    T
	hasDef bool
	per    map[string]T
}

func ebParseOverride[T any](raw, envName string, parse func(string) (T, error)) (ebOverride[T], error) {
	out := ebOverride[T]{per: map[string]T{}}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, text, split := strings.Cut(item, "=")
		name, text = strings.TrimSpace(name), strings.TrimSpace(text)
		if !split {
			name, text = "*", name
		}
		v, err := parse(text)
		if err != nil {
			return out, fmt.Errorf("%s: %q: %w", envName, item, err)
		}
		if name == "*" {
			if out.hasDef {
				return out, fmt.Errorf("%s: two values for every candidate in %q", envName, raw)
			}
			out.def, out.hasDef = v, true
			continue
		}
		if _, err := ebLookupCandidate(name); err != nil {
			return out, fmt.Errorf("%s: %w", envName, err)
		}
		if _, dup := out.per[name]; dup {
			return out, fmt.Errorf("%s: two values for candidate %q in %q", envName, name, raw)
		}
		out.per[name] = v
	}
	return out, nil
}

// value returns the setting for one candidate, or the fallback when neither a
// per-candidate nor a global value was given.
func (o ebOverride[T]) value(candidate string, fallback T) T {
	if v, ok := o.per[candidate]; ok {
		return v
	}
	if o.hasDef {
		return o.def
	}
	return fallback
}

func (o ebOverride[T]) given(candidate string) bool {
	if _, ok := o.per[candidate]; ok {
		return true
	}
	return o.hasDef
}

// ebConfig is one fully resolved experiment request.
type ebConfig struct {
	Mode         ebMode
	Candidates   []string
	Sizes        []int
	SeedStart    int
	SeedCount    int
	MidgamePlies []int
	// PlyCap is the number of turns a game may take before it is called
	// truncated. Zero means the default, which is size*size.
	PlyCap int
	Out    string

	limits map[string]Limits
	sims   map[string]int
	// defaulted records, per candidate, why its limits are not exactly what
	// was requested. The sentence is built where the decision is made and
	// from the values that were actually applied, so the artifact never has
	// to guess which clock or ceiling the harness supplied.
	defaulted map[string]string
	raw       map[string]string
}

func (c ebConfig) plyCap(size int) int {
	if c.PlyCap > 0 {
		return c.PlyCap
	}
	return size * size
}

func (c ebConfig) limitsFor(name string) Limits { return c.limits[name] }
func (c ebConfig) simsFor(name string) int      { return c.sims[name] }

// ebParseConfig reads the request from the environment. It takes the lookup as
// an argument so that the validation is testable without touching the process
// environment.
func ebParseConfig(get func(string) string) (ebConfig, error) {
	cfg := ebConfig{raw: map[string]string{}, defaulted: map[string]string{}}
	for _, name := range ebEnvNames {
		if v := strings.TrimSpace(get(name)); v != "" {
			cfg.raw[name] = v
		}
	}

	switch mode := strings.ToLower(strings.TrimSpace(get(ebEnvMode))); mode {
	case "", string(ebModePositions):
		cfg.Mode = ebModePositions
	case string(ebModeMatch):
		cfg.Mode = ebModeMatch
	default:
		return cfg, fmt.Errorf("%s=%q: want %s or %s", ebEnvMode, mode, ebModePositions, ebModeMatch)
	}

	names, err := ebParseNameList(get(ebEnvCandidates), "plain,pvs")
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", ebEnvCandidates, err)
	}
	for _, name := range names {
		if _, err := ebLookupCandidate(name); err != nil {
			return cfg, fmt.Errorf("%s: %w", ebEnvCandidates, err)
		}
	}
	if cfg.Mode == ebModeMatch && len(names) < 2 {
		return cfg, fmt.Errorf("%s: match mode needs at least two candidates, got %s",
			ebEnvCandidates, strings.Join(names, ", "))
	}
	cfg.Candidates = names

	if cfg.Sizes, err = ebParseIntList(get(ebEnvSizes), "10,16,24", ebEnvSizes); err != nil {
		return cfg, err
	}
	for _, size := range cfg.Sizes {
		if err := ebRules(size).Validate(); err != nil {
			return cfg, fmt.Errorf("%s: %dx%d: %w", ebEnvSizes, size, size, err)
		}
	}
	if cfg.MidgamePlies, err = ebParseIntList(get(ebEnvMidgame), "10,16,24", ebEnvMidgame); err != nil {
		return cfg, err
	}
	for _, plies := range cfg.MidgamePlies {
		if plies < 1 {
			return cfg, fmt.Errorf("%s: %d plies: a midgame needs at least one ply", ebEnvMidgame, plies)
		}
	}

	if cfg.SeedStart, err = ebParseInt(get(ebEnvSeedStart), 1, ebEnvSeedStart); err != nil {
		return cfg, err
	}
	if cfg.SeedStart < 1 {
		return cfg, fmt.Errorf("%s=%d: opening seeds start at 1", ebEnvSeedStart, cfg.SeedStart)
	}
	if cfg.SeedCount, err = ebParseInt(get(ebEnvSeedCount), 2, ebEnvSeedCount); err != nil {
		return cfg, err
	}
	if cfg.SeedCount < 1 {
		return cfg, fmt.Errorf("%s=%d: need at least one opening", ebEnvSeedCount, cfg.SeedCount)
	}
	if cfg.PlyCap, err = ebParseInt(get(ebEnvPlyCap), 0, ebEnvPlyCap); err != nil {
		return cfg, err
	}
	if cfg.PlyCap < 0 {
		return cfg, fmt.Errorf("%s=%d: a ply cap cannot be negative", ebEnvPlyCap, cfg.PlyCap)
	}
	cfg.Out = strings.TrimSpace(get(ebEnvOut))

	nodes, err := ebParseOverride(get(ebEnvNodes), ebEnvNodes, ebAtoi64)
	if err != nil {
		return cfg, err
	}
	depth, err := ebParseOverride(get(ebEnvDepth), ebEnvDepth, strconv.Atoi)
	if err != nil {
		return cfg, err
	}
	budget, err := ebParseOverride(get(ebEnvTime), ebEnvTime, time.ParseDuration)
	if err != nil {
		return cfg, err
	}
	sims, err := ebParseOverride(get(ebEnvIterations), ebEnvIterations, strconv.Atoi)
	if err != nil {
		return cfg, err
	}

	cfg.limits = map[string]Limits{}
	cfg.sims = map[string]int{}
	for _, name := range cfg.Candidates {
		spec, err := ebLookupCandidate(name)
		if err != nil {
			return cfg, err
		}
		l := Limits{
			Nodes: nodes.value(name, 0),
			Depth: depth.value(name, 0),
			Time:  budget.value(name, 0),
		}
		switch {
		case l.Nodes < 0:
			return cfg, fmt.Errorf("%s: candidate %s: %d nodes: a node bound cannot be negative", ebEnvNodes, name, l.Nodes)
		case l.Depth < 0:
			return cfg, fmt.Errorf("%s: candidate %s: depth %d: a depth bound cannot be negative", ebEnvDepth, name, l.Depth)
		case l.Depth > MaxDepth:
			return cfg, fmt.Errorf("%s: candidate %s: depth %d is beyond the %d plies the search can hold", ebEnvDepth, name, l.Depth, MaxDepth)
		case l.Time < 0:
			return cfg, fmt.Errorf("%s: candidate %s: %v: a clock cannot be negative", ebEnvTime, name, l.Time)
		}
		// A position probe with no work bound at all would inherit the tier's
		// nominal ceiling and spend its whole clock, which makes the numbers a
		// statement about the machine rather than about the search. Give it a
		// deterministic horizon instead, and record what was actually applied
		// rather than what the harness would have supplied: a candidate given
		// an explicit clock keeps it. The Monte-Carlo candidate is left alone:
		// these levers are the alpha-beta search's, and it is bounded by its
		// simulation count.
		if cfg.Mode == ebModePositions && spec.kind != ebKindMCTS && !nodes.given(name) && !depth.given(name) {
			l.Depth = ebProbeDepth
			why := fmt.Sprintf("harness default: depth %d, because neither %s nor %s asked this candidate for a work bound",
				ebProbeDepth, ebEnvNodes, ebEnvDepth)
			if budget.given(name) {
				why += fmt.Sprintf("; the clock is the requested %v", l.Time)
			} else {
				l.Time = ebNoClock
				why += fmt.Sprintf("; the clock is the harness guard of %v, because none was requested", ebNoClock)
			}
			cfg.defaulted[name] = why
		}
		cfg.limits[name] = l

		n := sims.value(name, 0)
		if n < 0 {
			return cfg, fmt.Errorf("%s: candidate %s: %d simulations: cannot be negative", ebEnvIterations, name, n)
		}
		cfg.sims[name] = n
	}
	return cfg, nil
}

func ebParseNameList(raw, def string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	var out []string
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if seen[item] {
			return nil, fmt.Errorf("%q is listed twice", item)
		}
		seen[item] = true
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, errors.New("empty list")
	}
	return out, nil
}

func ebParseIntList(raw, def, envName string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	var out []int
	seen := map[int]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		v, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", envName, item, err)
		}
		if seen[v] {
			return nil, fmt.Errorf("%s: %d is listed twice", envName, v)
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: empty list", envName)
	}
	return out, nil
}

func ebParseInt(raw string, def int, envName string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %q: %w", envName, raw, err)
	}
	return v, nil
}

func ebAtoi64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

// ebRules is the ruleset every measurement uses: the printed box rules on the
// requested board with the swap option off. Swap is off explicitly because
// which side gains from swapping is a confound on the very thing a paired match
// varies, so the opening is varied instead.
func ebRules(size int) game.Ruleset {
	rs := game.Std
	rs.Size = size
	rs.Swap = false
	return rs
}

// ebOpeningPool permutes every hole Vertical may open with, once, under a fixed
// key. The permutation is a function of the board size alone.
func ebOpeningPool(size int) ([]game.Point, error) {
	rs := ebRules(size)
	if err := rs.Validate(); err != nil {
		return nil, fmt.Errorf("%dx%d: %w", size, size, err)
	}
	g, err := game.New(rs)
	if err != nil {
		return nil, err
	}
	pool := g.LegalPlacements(game.Vertical)
	if len(pool) == 0 {
		return nil, fmt.Errorf("%dx%d: no legal opening", size, size)
	}
	src := rand.New(rand.NewPCG(ebOpeningKey, uint64(size)))
	for i := len(pool) - 1; i > 0; i-- {
		j := src.IntN(i + 1)
		pool[i], pool[j] = pool[j], pool[i]
	}
	return pool, nil
}

// ebOpening maps an opening seed to Vertical's first move. Because the
// permutation depends only on the board size, two runs agree on every seed they
// share: a holdout range 101..112 is directly comparable with a development
// range 1..12, and neither depends on which other seeds were in the run.
func ebOpening(size, seed int) (game.Point, error) {
	if seed < 1 {
		return game.Point{}, fmt.Errorf("opening seed %d: seeds start at 1", seed)
	}
	pool, err := ebOpeningPool(size)
	if err != nil {
		return game.Point{}, err
	}
	return pool[(seed-1)%len(pool)], nil
}

// ebOpeningRef is one opening of a run: the seed that names it and the hole it
// resolved to.
type ebOpeningRef struct {
	Seed  int
	Point game.Point
}

// ebOpenings resolves a whole seed range and refuses a range that would repeat
// an opening. A repeat would replay the same game pair and enter the sample
// twice, which is exactly the dependence the paired statistics assume away.
func ebOpenings(size, start, count int) ([]ebOpeningRef, error) {
	pool, err := ebOpeningPool(size)
	if err != nil {
		return nil, err
	}
	if start < 1 {
		return nil, fmt.Errorf("opening seed %d: seeds start at 1", start)
	}
	// A range that names no opening, or one whose end does not fit in an int,
	// is a request that would run to completion having measured nothing, and a
	// run that measured nothing must not be able to report a result.
	if count < 1 {
		return nil, fmt.Errorf("opening seed count %d: a run needs at least one opening", count)
	}
	if start > math.MaxInt-count {
		return nil, fmt.Errorf("opening seeds %d..+%d: the range does not fit in an int", start, count)
	}
	out := make([]ebOpeningRef, 0, count)
	seen := make(map[game.Point]int, count)
	for i := range count {
		seed := start + i
		hole := pool[(seed-1)%len(pool)]
		if prev, dup := seen[hole]; dup {
			return nil, fmt.Errorf("%dx%d: opening seeds %d and %d both resolve to %v; %d holes are available, so a range of %d repeats games",
				size, size, prev, seed, hole, len(pool), count)
		}
		seen[hole] = seed
		out = append(out, ebOpeningRef{Seed: seed, Point: hole})
	}
	return out, nil
}

// ebMCTSBot wraps the Monte-Carlo experiment as a Bot so that the same match
// and probe machinery can drive it. It is a harness adapter, not a shipped
// player: the experiment function is stateless, so the bot carries only the
// per-game seed, its simulation bound and the counters from its last move.
//
// The Monte-Carlo candidate is bounded by simulations alone. That is not the
// same horizon as a node or depth bound on the alpha-beta candidate, so the
// artifact records each bound as what it is and never equates the two.
type ebMCTSBot struct {
	seed        int64
	simulations int
	// p is the lever set every move of this bot runs under. It is held here
	// rather than rebuilt inside Move so that the protocol can report the
	// values the search was actually given: a lever that moves has to move
	// the artifact with it, or the artifact describes a run that never
	// happened.
	p       mctsParams
	stats   mctsStats
	elapsed time.Duration
}

func (*ebMCTSBot) Tier() Tier { return Pro }

func (*ebMCTSBot) Hint(context.Context, *game.Game) (Hint, error) {
	return Hint{}, errors.New("bot: the monte-carlo experiment gives no hints")
}

func (m *ebMCTSBot) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	move, stats, err := experimentMCTSStats(ctx, g, m.seed, m.simulations, m.p)
	m.elapsed = time.Since(start)
	m.stats = stats
	if err != nil {
		return game.Point{}, err
	}
	return move, nil
}

// ebStats is what one search cost, as the artifact records it. The
// search-reported elapsed time is kept apart from the harness's own wall
// measurement of the call, and depth, nodes and evaluations are kept apart from
// both: a tier bounded by its clock and a tier bounded by its depth are not
// comparable on any single one of them.
//
// The two architectures fill different fields, and the artifact leaves the ones
// they do not have at zero rather than inventing them. Evaluations is the one
// counter they share, and it is the same unit in both: every position analysis
// of either architecture goes through the same searcher, tactical probes
// included. It is therefore the axis their cost is comparable on, which the
// depths and node counts are not.
type ebStats struct {
	Nodes       int64  `json:"nodes"`
	Evaluations int64  `json:"evaluations"`
	Depth       int    `json:"depth"`
	SearchNS    int64  `json:"search_ns"`
	StopReason  string `json:"stop_reason,omitempty"`
	// RequestedSims is the simulation count the Monte-Carlo candidate was
	// asked for. It is what it was given rather than what it spent: the
	// experiment answers a root immediate win without simulating at all. The
	// name says requested for that reason.
	RequestedSims int64  `json:"requested_simulations,omitempty"`
	Simulations   int    `json:"simulations,omitempty"`
	TreeNodes     int    `json:"tree_nodes,omitempty"`
	TreeDepth     int    `json:"tree_depth,omitempty"`
	Source        string `json:"source"`
}

// ebPlayer is one candidate instantiated for one game or one probe.
type ebPlayer struct {
	spec ebCandidateSpec
	bot  Bot
	// own is the searcher behind the bot when the harness built it and can read
	// its counters directly, which is more reliable than asking a wrapper.
	own *searcher
	// mcts is the same for the Monte-Carlo adapter.
	mcts *ebMCTSBot
	// direct marks a candidate whose probe calls the search root instead of the
	// bot: that is how the value as well as the move is measured.
	direct bool
	eff    *params
	source string
}

func ebNewPlayer(spec ebCandidateSpec, seed int64, cfg ebConfig) (*ebPlayer, error) {
	limits := cfg.limitsFor(spec.name)
	switch spec.kind {
	case ebKindPreset:
		b, err := NewWithLimits(spec.tier, seed, limits)
		if err != nil {
			return nil, fmt.Errorf("candidate %s: %w", spec.name, err)
		}
		pl := &ebPlayer{spec: spec, bot: b, source: "stats_of"}
		// The constructor hands back the engine itself today, which makes the
		// counters readable at the source; a wrapper would fall back to
		// StatsOf, which is what the source field in the artifact records.
		if e, ok := b.(*engine); ok && e.play != nil {
			p := e.p
			pl.own, pl.eff, pl.source = e.play, &p, "searcher"
		}
		return pl, nil
	case ebKindTuned:
		// The horizon is applied with the production Limits, not a copy of its
		// rules: a tuned candidate has to be narrowed exactly the way the
		// exported constructor narrows a preset one, or the comparison is
		// between two different meanings of the same bound.
		var applyErr error
		e := tunedEngine(spec.tier, seed, func(p *params) {
			if applyErr = limits.apply(p); applyErr != nil {
				return
			}
			if spec.tune != nil {
				spec.tune(p)
			}
		})
		if applyErr != nil {
			return nil, fmt.Errorf("candidate %s: %w", spec.name, applyErr)
		}
		p := e.p
		return &ebPlayer{spec: spec, bot: e, own: e.play, eff: &p, direct: true, source: "searcher"}, nil
	case ebKindMCTS:
		sims := cfg.simsFor(spec.name)
		if sims < 1 {
			return nil, fmt.Errorf("candidate %s: %s must give a positive simulation count",
				spec.name, ebEnvIterations)
		}
		m := &ebMCTSBot{seed: seed, simulations: sims, p: defaultMCTSParams()}
		return &ebPlayer{spec: spec, bot: m, mcts: m, source: "mcts"}, nil
	}
	return nil, fmt.Errorf("candidate %s: unknown construction %q", spec.name, spec.kind)
}

// stats reports what the player's last move cost.
func (pl *ebPlayer) stats() ebStats {
	switch {
	case pl.own != nil:
		return ebStats{
			Nodes:       pl.own.nodes,
			Evaluations: pl.own.evaluations,
			Depth:       pl.own.lastDepth,
			SearchNS:    pl.own.elapsed.Nanoseconds(),
			StopReason:  pl.own.stopReason,
			Source:      "searcher",
		}
	case pl.mcts != nil:
		// Keep simulation/tree work separate from alpha-beta nodes and depth.
		// The actual stop reason comes from the search, not an inference from
		// how many evaluations it happened to perform.
		return ebStats{
			Evaluations:   pl.mcts.stats.Evaluations,
			SearchNS:      pl.mcts.elapsed.Nanoseconds(),
			RequestedSims: int64(pl.mcts.simulations),
			Simulations:   pl.mcts.stats.Simulations,
			TreeNodes:     pl.mcts.stats.Nodes,
			TreeDepth:     pl.mcts.stats.MaxDepth,
			StopReason:    pl.mcts.stats.StopReason,
			Source:        "mcts",
		}
	default:
		s := StatsOf(pl.bot)
		return ebStats{
			Nodes:       s.Nodes,
			Evaluations: s.Evaluations,
			Depth:       s.Depth,
			SearchNS:    s.Elapsed.Nanoseconds(),
			StopReason:  s.StopReason,
			Source:      "stats_of",
		}
	}
}

// ebParamsReport is the whole lever set a candidate actually ran with.
type ebParamsReport struct {
	Budget      string  `json:"budget"`
	BudgetNS    int64   `json:"budget_ns"`
	MaxDepth    int     `json:"max_depth"`
	Width       int     `json:"width"`
	RootWidth   int     `json:"root_width"`
	FullEval    bool    `json:"full_eval"`
	UseTable    bool    `json:"use_table"`
	Extend      int     `json:"extend"`
	Temperature float64 `json:"temperature"`
	NodeLimit   int64   `json:"node_limit"`
	PVS         bool    `json:"pvs"`
	Templates   bool    `json:"templates"`
}

func ebReportParams(p params) ebParamsReport {
	return ebParamsReport{
		Budget:      p.budget.String(),
		BudgetNS:    p.budget.Nanoseconds(),
		MaxDepth:    p.maxDepth,
		Width:       p.width,
		RootWidth:   p.rootWidth,
		FullEval:    p.fullEval,
		UseTable:    p.useTable,
		Extend:      p.extend,
		Temperature: p.temperature,
		NodeLimit:   p.nodeLimit,
		PVS:         p.pvs,
		Templates:   p.templates,
	}
}

type ebLimitsReport struct {
	Nodes int64  `json:"nodes"`
	Depth int    `json:"depth"`
	Time  string `json:"time"`
	TimeN int64  `json:"time_ns"`
}

func ebReportLimits(l Limits) ebLimitsReport {
	return ebLimitsReport{Nodes: l.Nodes, Depth: l.Depth, Time: l.Time.String(), TimeN: l.Time.Nanoseconds()}
}

// ebContextGuard is what the harness's context contributes as a guard. The
// harness sets no deadline, so only a cancellation ends a search through it.
const ebContextGuard = "a context with no deadline, which ends a search only if it is cancelled"

// ebGuardReport is every guard one contender actually ran under. A search ends
// at whichever of them trips first, so all of them are recorded and none of
// them is named as the binding one: only the stop reason recorded with an
// individual search says which guard ended that search.
type ebGuardReport struct {
	Nodes       int64  `json:"node_ceiling,omitempty"`
	Depth       int    `json:"depth_ceiling,omitempty"`
	Clock       string `json:"clock,omitempty"`
	ClockNS     int64  `json:"clock_ns,omitempty"`
	Simulations int    `json:"simulation_ceiling,omitempty"`
	EvalLimit   int64  `json:"evaluation_ceiling,omitempty"`
	Context     string `json:"context"`
	Source      string `json:"source"`
}

// ebMCTSParamsReport is the lever set the Monte-Carlo contender ran with.
// mctsParams' fields are unexported, so they are named here one at a time
// rather than reflected over: the mapping is what ties the protocol to the
// parameters, and a lever that moves has to move this report with it.
type ebMCTSParamsReport struct {
	Exploration  float64 `json:"exploration"`
	FPU          float64 `json:"fpu"`
	PriorTau     float64 `json:"prior_tau"`
	Width        int     `json:"width"`
	RootWidth    int     `json:"root_width"`
	WidenBase    float64 `json:"widen_base"`
	RolloutPlies int     `json:"rollout_plies"`
	RolloutTop   int     `json:"rollout_top"`
	RolloutTau   float64 `json:"rollout_tau"`
	ValueScale   float64 `json:"value_scale"`
	FullEval     bool    `json:"full_eval"`
	EvalLimit    int64   `json:"eval_limit"`
}

func ebReportMCTSParams(p mctsParams) ebMCTSParamsReport {
	return ebMCTSParamsReport{
		Exploration:  p.exploration,
		FPU:          p.fpu,
		PriorTau:     p.priorTau,
		Width:        p.width,
		RootWidth:    p.rootWidth,
		WidenBase:    p.widenBase,
		RolloutPlies: p.rolloutPlies,
		RolloutTop:   p.rolloutTop,
		RolloutTau:   p.rolloutTau,
		ValueScale:   p.valueScale,
		FullEval:     p.fullEval,
		EvalLimit:    p.evalLimit,
	}
}

type ebCandidateReport struct {
	Name         string `json:"name"`
	Tier         string `json:"tier"`
	Construction string `json:"construction"`
	// Limits is the alpha-beta horizon, and it is absent for a contender
	// these levers do not reach: reporting a node, depth or clock bound that
	// was never applied would be a claim about a search that did not happen.
	Limits       *ebLimitsReport     `json:"limits,omitempty"`
	LimitsSource string              `json:"limits_source"`
	Guards       ebGuardReport       `json:"guards"`
	Simulations  int                 `json:"simulations,omitempty"`
	Params       *ebParamsReport     `json:"params,omitempty"`
	MCTSParams   *ebMCTSParamsReport `json:"mcts_params,omitempty"`
	StatsSource  string              `json:"stats_source"`
}

type ebRulesReport struct {
	Size        int    `json:"size"`
	Canonical   string `json:"canonical"`
	Fingerprint string `json:"fingerprint"`
	Swap        bool   `json:"swap"`
	PlyCap      int    `json:"ply_cap"`
}

type ebProtocol struct {
	Generated    string              `json:"generated_utc"`
	GoVersion    string              `json:"go_version"`
	GOOS         string              `json:"goos"`
	GOARCH       string              `json:"goarch"`
	GOMAXPROCS   int                 `json:"gomaxprocs"`
	Workers      int                 `json:"workers"`
	Sequential   bool                `json:"timing_sequential"`
	Mode         string              `json:"mode"`
	Env          map[string]string   `json:"env"`
	Sizes        []int               `json:"sizes"`
	SeedStart    int                 `json:"seed_start"`
	SeedCount    int                 `json:"seed_count"`
	MidgamePlies []int               `json:"midgame_plies,omitempty"`
	Interval     string              `json:"paired_interval"`
	Candidates   []ebCandidateReport `json:"candidates"`
	Rules        []ebRulesReport     `json:"rules"`
}

type ebArtifact struct {
	Schema        string                 `json:"schema"`
	Mode          string                 `json:"mode"`
	Protocol      ebProtocol             `json:"protocol"`
	Caveats       []string               `json:"caveats"`
	Aborted       bool                   `json:"aborted"`
	Error         string                 `json:"error,omitempty"`
	ElapsedNS     int64                  `json:"elapsed_ns"`
	Positions     []ebPositionRun        `json:"positions,omitempty"`
	PositionStats *ebPositionSummary     `json:"position_summary,omitempty"`
	Terminals     []ebTerminalSlot       `json:"terminal_position_slots,omitempty"`
	Comparisons   []ebPositionComparison `json:"position_comparisons,omitempty"`
	Matches       []ebMatch              `json:"matches,omitempty"`
}

type ebProbeRecord struct {
	Candidate string  `json:"candidate"`
	Move      string  `json:"move"`
	Score     *int    `json:"score,omitempty"`
	RootDepth int     `json:"root_depth"`
	WallNS    int64   `json:"wall_ns"`
	Stats     ebStats `json:"stats"`
	Path      string  `json:"path"`
}

type ebPositionRun struct {
	Size int `json:"size"`
	// MidgamePlies is the ply count that was requested and Plies is the ply
	// the position is actually at. They differ only when the setup game ended
	// before the requested horizon, and both are kept so that a missing
	// horizon is visible as a missing horizon.
	MidgamePlies int      `json:"midgame_plies"`
	Seed         int      `json:"seed"`
	Opening      string   `json:"opening"`
	Setup        []string `json:"setup_moves"`
	Digest       string   `json:"digest"`
	Turn         string   `json:"turn"`
	Plies        int      `json:"plies"`
	// Terminal marks a slot whose position is already over. There is no move
	// to make in it, so no candidate is asked for one: Probes is empty and
	// the slot enters no comparison. A horizon the setup game never reached
	// is missing evidence, not evidence.
	Terminal bool            `json:"terminal"`
	Outcome  string          `json:"outcome,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Note     string          `json:"note,omitempty"`
	Probes   []ebProbeRecord `json:"probes"`
}

// ebTerminalSlot is one requested position that carries no measurement, listed
// on its own so that the holes in the sample can be counted without scanning
// every run.
type ebTerminalSlot struct {
	Size           int    `json:"size"`
	Seed           int    `json:"seed"`
	RequestedPlies int    `json:"requested_midgame_plies"`
	TerminalPlies  int    `json:"terminal_plies"`
	Outcome        string `json:"outcome"`
	Reason         string `json:"reason"`
	Digest         string `json:"digest"`
}

// ebPositionSummary is how much of the request the positions mode actually
// measured. Requested is every slot that was asked for, Measured is the slots
// a candidate was run on, and Terminal is the slots that were already over.
type ebPositionSummary struct {
	Requested int `json:"requested_slots"`
	Measured  int `json:"measured_slots"`
	Terminal  int `json:"terminal_slots"`
}

// ebPositionComparison is the semantics-preserving evidence for one position:
// two candidates, the same position, and whether they agreed on the answer and
// on the value at the depth they reached.
type ebPositionComparison struct {
	Size         int     `json:"size"`
	MidgamePlies int     `json:"midgame_plies"`
	Seed         int     `json:"seed"`
	Digest       string  `json:"digest"`
	A            string  `json:"a"`
	B            string  `json:"b"`
	SameMove     bool    `json:"same_move"`
	SameDepth    bool    `json:"same_depth"`
	SameScore    *bool   `json:"same_score,omitempty"`
	DepthA       int     `json:"depth_a"`
	DepthB       int     `json:"depth_b"`
	NodesA       int64   `json:"nodes_a"`
	NodesB       int64   `json:"nodes_b"`
	NodeRatio    float64 `json:"node_ratio_b_over_a,omitempty"`
	EvalsA       int64   `json:"evaluations_a"`
	EvalsB       int64   `json:"evaluations_b"`
	EvalRatio    float64 `json:"evaluation_ratio_b_over_a,omitempty"`
	WallNSA      int64   `json:"wall_ns_a"`
	WallNSB      int64   `json:"wall_ns_b"`
	Comparable   bool    `json:"comparable"`
	Note         string  `json:"note,omitempty"`
}

type ebMoveRecord struct {
	Ply       int     `json:"ply"`
	Side      string  `json:"side"`
	Candidate string  `json:"candidate"`
	Move      string  `json:"move"`
	WallNS    int64   `json:"wall_ns"`
	Stats     ebStats `json:"stats,omitzero"`
}

type ebGameRecord struct {
	Index     int    `json:"index"`
	Size      int    `json:"size"`
	Seed      int    `json:"seed"`
	Opening   string `json:"opening"`
	A         string `json:"a"`
	B         string `json:"b"`
	ASide     string `json:"a_side"`
	Winner    string `json:"winner"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	Plies     int    `json:"plies"`
	PlyCap    int    `json:"ply_cap"`
	Truncated bool   `json:"truncated"`
	// Aborted marks a game the harness could not play out: a bot error or an
	// illegal move. It is the field that classifies the failure, together
	// with Error; Outcome, Reason and Winner keep reporting what the game
	// reported, which for a game that never finished is no result and no
	// winner. Such a game is never scored and never counted as a draw, and
	// the moves it did make are kept because they happened.
	Aborted    bool           `json:"aborted"`
	Error      string         `json:"error,omitempty"`
	Digest     string         `json:"digest"`
	Transcript string         `json:"transcript,omitempty"`
	ElapsedNS  int64          `json:"elapsed_ns"`
	Moves      []ebMoveRecord `json:"moves"`

	// winner is the result as a player, kept beside the printable name so the
	// tally never has to parse a string back into a side.
	winner game.Player
}

// ebPair is one opening played from both sides: the statistical unit.
type ebPair struct {
	Seed      int      `json:"seed"`
	Opening   string   `json:"opening"`
	Games     []int    `json:"games"`
	ScoreA    *float64 `json:"score_a,omitempty"`
	Complete  bool     `json:"complete"`
	Truncated int      `json:"truncated_games"`
}

type ebTally struct {
	Games     int `json:"games"`
	Scored    int `json:"scored_games"`
	AWins     int `json:"a_wins"`
	BWins     int `json:"b_wins"`
	Draws     int `json:"draws"`
	Truncated int `json:"truncated_games"`
}

// ebPaired is the paired-uncertainty report.
//
// The unit is the opening pair, so there is no binomial interval here: the two
// games of one opening share an opening and are correlated by construction, and
// a Wilson interval over games would understate the spread.
//
// The bound is the conservative bounded-outcome (Hoeffding) one: a pair score
// lies in [0,1], so for n independent pairs the two-sided 95% half width is
// sqrt(ln(2/0.05) / 2n), whatever the shape of the distribution. That is
// deliberately wider than a t or bootstrap interval, and it is chosen because
// the samples this harness produces are small and often unanimous: eight pairs
// all won give a t interval and a percentile bootstrap of zero width, which
// reads as certainty and is not. The price is that a real gap needs more pairs
// before it can be claimed.
//
// The bound is conditional on the opening sample. It says what these openings
// support, not that one candidate is stronger on this board size in general.
//
// A directional verdict needs every requested pair, not merely a bound clear
// of 0.5 over the pairs that finished. Eight swept pairs alone clear the
// bound; if sixteen more were requested and have no score, the sample that
// would answer the question is three quarters missing, and the missing pairs
// could have gone the other way. A pair without a score is therefore not
// neutral evidence, it is absent evidence, and the verdict says so instead of
// reading a direction off the pairs that happened to finish.
type ebPaired struct {
	Pairs           int      `json:"pairs"`
	ScoredPairs     int      `json:"scored_pairs"`
	IncompletePairs int      `json:"incomplete_pairs"`
	Mean            *float64 `json:"mean_score_a,omitempty"`
	SD              *float64 `json:"sd,omitempty"`
	HalfWidth       *float64 `json:"half_width,omitempty"`
	Low             *float64 `json:"low,omitempty"`
	High            *float64 `json:"high,omitempty"`
	Method          string   `json:"method"`
	// Verdict is directional only when every requested pair has a score and
	// the bound over them clears 0.5. Otherwise it names why it is not:
	// inconclusive for a bound that spans 0.5, inconclusive-incomplete for a
	// sample with pairs missing, no-scored-pairs for nothing to summarise,
	// and aborted for a match that did not finish.
	Verdict string   `json:"verdict"`
	Notes   []string `json:"notes,omitempty"`
}

type ebMatch struct {
	Size   int    `json:"size"`
	A      string `json:"a"`
	B      string `json:"b"`
	PlyCap int    `json:"ply_cap"`
	// Aborted marks a match that stopped part way through. The games recorded
	// above it are real receipts of play; the tally and the bound are absent
	// because a match that did not finish has no score.
	Aborted bool           `json:"aborted"`
	Error   string         `json:"error,omitempty"`
	Tally   ebTally        `json:"tally"`
	Paired  ebPaired       `json:"paired"`
	Pairs   []ebPair       `json:"pairs"`
	Games   []ebGameRecord `json:"games"`
	Elapsed int64          `json:"elapsed_ns"`
}

func ebOutcomeName(o game.Outcome) string {
	switch o {
	case game.Ongoing:
		return "ongoing"
	case game.VerticalWins:
		return "vertical-wins"
	case game.HorizontalWins:
		return "horizontal-wins"
	case game.Draw:
		return "draw"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

func ebReasonName(r game.Reason) string {
	switch r {
	case game.NotOver:
		return "not-over"
	case game.Connection:
		return "connection"
	case game.NoMovesLeft:
		return "no-moves-left"
	case game.Resignation:
		return "resignation"
	case game.Agreement:
		return "agreement"
	}
	return fmt.Sprintf("reason(%d)", int(r))
}

// TestBotEffortExperiment is the experiment. It is gated because it measures
// rather than asserts: an ordinary test run must not pay for it, and its output
// is the artifact, not a pass.
func TestBotEffortExperiment(t *testing.T) {
	if os.Getenv(ebGate) != "1" {
		t.Skipf("set %s=1 to run the effort experiment; knobs: %s", ebGate, strings.Join(ebEnvNames[1:], " "))
	}
	cfg, err := ebParseConfig(os.Getenv)
	if err != nil {
		t.Fatalf("effort experiment configuration: %v", err)
	}

	art, err := ebNewArtifact(cfg)
	if err != nil {
		t.Fatalf("effort experiment setup: %v", err)
	}
	out := cfg.Out
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("twixt-bot-effort-%s-%d.json", cfg.Mode, time.Now().UTC().Unix()))
	}
	start := time.Now()
	defer func() {
		art.ElapsedNS = time.Since(start).Nanoseconds()
		if err := ebWriteArtifact(out, art); err != nil {
			t.Errorf("writing artifact to %s: %v", out, err)
			return
		}
		t.Logf("effort artifact: %s", out)
	}()

	switch cfg.Mode {
	case ebModePositions:
		err = ebRunPositions(t, cfg, art)
	case ebModeMatch:
		err = ebRunMatches(t, cfg, art)
	default:
		err = fmt.Errorf("unknown mode %q", cfg.Mode)
	}
	if err != nil {
		// An error is an abort, recorded as one. It is never folded into a
		// score: a game the bot could not finish tells us nothing about which
		// side is stronger.
		art.Aborted, art.Error = true, err.Error()
		t.Fatalf("effort experiment aborted: %v", err)
	}
}

func ebNewArtifact(cfg ebConfig) (*ebArtifact, error) {
	art := &ebArtifact{
		Schema: ebSchema,
		Mode:   string(cfg.Mode),
		Protocol: ebProtocol{
			Generated:  time.Now().UTC().Format(time.RFC3339),
			GoVersion:  runtime.Version(),
			GOOS:       runtime.GOOS,
			GOARCH:     runtime.GOARCH,
			GOMAXPROCS: runtime.GOMAXPROCS(0),
			Workers:    1,
			Sequential: true,
			Mode:       string(cfg.Mode),
			Env:        cfg.raw,
			Sizes:      cfg.Sizes,
			SeedStart:  cfg.SeedStart,
			SeedCount:  cfg.SeedCount,
			Interval:   ebIntervalMethod,
		},
	}
	if cfg.Mode == ebModePositions {
		art.Protocol.MidgamePlies = cfg.MidgamePlies
	}
	for _, size := range cfg.Sizes {
		rs := ebRules(size)
		art.Protocol.Rules = append(art.Protocol.Rules, ebRulesReport{
			Size:        size,
			Canonical:   rs.Canonical(),
			Fingerprint: rs.Fingerprint(),
			Swap:        rs.Swap,
			PlyCap:      cfg.plyCap(size),
		})
	}

	// Every candidate is built once here, before any measurement: a candidate
	// that cannot be built at all should stop the run immediately rather than
	// after an hour of games.
	for _, name := range cfg.Candidates {
		spec, err := ebLookupCandidate(name)
		if err != nil {
			return nil, err
		}
		pl, err := ebNewPlayer(spec, 0, cfg)
		if err != nil {
			return nil, err
		}
		rep := ebCandidateReport{
			Name:         name,
			Tier:         spec.tier.String(),
			Construction: string(spec.kind),
			Simulations:  cfg.simsFor(name),
			StatsSource:  pl.source,
		}
		switch {
		case pl.mcts != nil:
			// The alpha-beta levers are never applied to this contender, so
			// they are not reported as though they had been. What bounds it
			// is its simulation count, its evaluation cap and the context.
			p := ebReportMCTSParams(pl.mcts.p)
			rep.MCTSParams = &p
			rep.LimitsSource = fmt.Sprintf("%s: this contender is bounded by its simulation count; %s, %s and %s are the alpha-beta search's levers and are not applied to it",
				ebEnvIterations, ebEnvNodes, ebEnvDepth, ebEnvTime)
			rep.Guards = ebGuardReport{
				Simulations: pl.mcts.simulations,
				EvalLimit:   pl.mcts.p.evalLimit,
				Context:     ebContextGuard,
				Source:      "monte-carlo levers as run",
			}
		default:
			l := ebReportLimits(cfg.limitsFor(name))
			rep.Limits = &l
			rep.LimitsSource = "requested"
			if why, ok := cfg.defaulted[name]; ok {
				rep.LimitsSource = why
			}
			rep.Guards = ebGuardReport{
				Nodes:   l.Nodes,
				Depth:   l.Depth,
				Clock:   l.Time,
				ClockNS: l.TimeN,
				Context: ebContextGuard,
				Source:  "requested limits",
			}
			if pl.eff != nil {
				// The effective params are the guards the search will
				// actually consult: where a limit was left at zero, the
				// tier's own ceiling is what binds, and that is what the
				// artifact has to say.
				p := ebReportParams(*pl.eff)
				rep.Params = &p
				rep.Guards = ebGuardReport{
					Nodes:   p.NodeLimit,
					Depth:   p.MaxDepth,
					Clock:   p.Budget,
					ClockNS: p.BudgetNS,
					Context: ebContextGuard,
					Source:  "effective search params",
				}
			}
		}
		art.Protocol.Candidates = append(art.Protocol.Candidates, rep)
	}
	art.Caveats = ebCaveats(cfg, art.Protocol.Candidates)
	return art, nil
}

// ebCaveats states what the numbers in this artifact cannot support. They are
// written into the artifact rather than left to the reader, because every one of
// them is a way a run of this harness could be misread.
func ebCaveats(cfg ebConfig, reports []ebCandidateReport) []string {
	out := []string{
		"the statistical unit of a match is the opening pair, not the game: the two games of one opening share an opening and are correlated, so no binomial or Wilson interval over games appears here",
		"the paired bound is conservative and conditional on this opening sample: it says what these openings support, not that a candidate is stronger on this board size in general",
		"a game that hit the ply cap is recorded as truncated and left out of the score; its winner is unknown, and an unknown result is not a draw",
		"a pair without a score is absent evidence rather than neutral evidence: no directional verdict is reported while any requested pair is missing, however the pairs that finished came out",
		"a bot error or an illegal move aborts the run and is recorded as an abort together with the games already played; it is never scored",
		"node and evaluation counts compare only at an equal work horizon; check same_depth on each position comparison and the per-candidate guards in the protocol before quoting a ratio",
		"the alpha-beta and monte-carlo candidates take different bounds and fill different counters: a simulation cap is not a node or depth cap and neither architecture reports the other's depth, but the evaluation counts are the same unit in both and are the axis their cost can be compared on",
		"every search runs under several guards at once — a work ceiling where one was set, a wall clock, and the caller's context — and it ends at whichever trips first; the guards listed per candidate below are what each contender was given, not a claim that any one of them bound its searches, which only the stop reason recorded with an individual search can establish",
	}
	if n := runtime.GOMAXPROCS(0); n != 2 {
		out = append(out, fmt.Sprintf("GOMAXPROCS was %d, not the 2 the timing protocol specifies; wall times in this artifact are not comparable with a run made under that protocol", n))
	}
	for _, rep := range reports {
		out = append(out, fmt.Sprintf("candidate %s ran under %s, taken from its %s; which of them ended any particular search is only in that search's recorded stop reason",
			rep.Name, ebGuardSentence(rep.Guards), rep.Guards.Source))
	}
	if cfg.Mode == ebModePositions {
		out = append(out, "position probes measure one search on a fixed position; they say nothing about game strength, which needs the paired match")
	}
	return out
}

// ebGuardSentence lists the guards one contender carried, in neutral terms and
// without ranking them: the sentence describes the configuration, and nothing
// in it may read as a claim about which guard did the work.
func ebGuardSentence(g ebGuardReport) string {
	var parts []string
	if g.Nodes > 0 {
		parts = append(parts, fmt.Sprintf("a ceiling of %d nodes", g.Nodes))
	}
	if g.Depth > 0 {
		parts = append(parts, fmt.Sprintf("a depth ceiling of %d", g.Depth))
	}
	if g.Simulations > 0 {
		parts = append(parts, fmt.Sprintf("a ceiling of %d simulations", g.Simulations))
	}
	if g.EvalLimit > 0 {
		parts = append(parts, fmt.Sprintf("a ceiling of %d evaluations", g.EvalLimit))
	}
	if g.ClockNS > 0 {
		parts = append(parts, fmt.Sprintf("a %s clock", g.Clock))
	}
	parts = append(parts, ebContextGuard)
	if len(parts) == 1 {
		return "only " + parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func ebWriteArtifact(path string, art *ebArtifact) error {
	blob, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, blob, 0o644)
}

// ebSetup is how a probe position was reached.
type ebSetup struct {
	opening string
	moves   []string
}

// ebMidgamePos is one requested position: the ply count that was asked for,
// the ply the position is actually at, and whether it is already over.
type ebMidgamePos struct {
	// requested is the ply count the run asked for; actual is the ply this
	// position sits at. They differ only when the setup game ended first,
	// and the difference is recorded rather than resampled away.
	requested int
	actual    int
	g         *game.Game
	setup     ebSetup
	// terminal marks a position that is already over. Such a slot carries no
	// measurement: there is no move to make in a finished game.
	terminal bool
	outcome  string
	reason   string
}

// ebMidgames builds the probe positions for one board and one opening seed.
//
// The setup game is played by the intermediate tier with its clock lifted to
// the guard: that tier stops at a depth ceiling rather than on the clock, so
// with no budget to run out of, the same seed and opening reach the same
// position on any machine. Every requested ply count is cut from the same
// game, so the positions of one seed share a prefix and differ only in how far
// the game has developed.
//
// A requested ply count the setup game never reaches is neither dropped nor
// replaced by an earlier position: it comes back as the terminal position the
// game ended in, marked as such, carrying both the ply that was requested and
// the ply the game actually stopped at. Substituting an earlier position would
// answer a different question under the requested slot's name, and refusing
// the whole seed would throw away the slots that are measurable.
func ebMidgames(size, seed int, targets []int) ([]ebMidgamePos, error) {
	want := slices.Clone(targets)
	slices.Sort(want)
	if len(want) == 0 {
		return nil, errors.New("no midgame ply counts requested")
	}
	if last := want[len(want)-1]; last >= size*size {
		return nil, fmt.Errorf("%dx%d: %d plies does not fit on the board", size, size, last)
	}

	g, err := game.New(ebRules(size))
	if err != nil {
		return nil, err
	}
	op, err := ebOpening(size, seed)
	if err != nil {
		return nil, err
	}
	if _, err := g.PlayPeg(op); err != nil {
		return nil, fmt.Errorf("%dx%d opening %v: %w", size, size, op, err)
	}
	setup := ebSetup{opening: op.String(), moves: []string{op.String()}}
	lift := func(p *params) { p.budget = ebNoClock; p.temperature = 0 }
	vert := tunedEngine(Intermediate, int64(seed), lift)
	horz := tunedEngine(Intermediate, int64(seed)+ebSeedOffset, lift)

	ctx := context.Background()
	out := make([]ebMidgamePos, 0, len(want))
	next := 0
	for {
		res := g.Result()
		over := res.Over()
		// Every requested slot is kept, measurable or not. Once the game is
		// over, every slot still outstanding is that terminal position: the
		// requested ply is preserved beside the ply the game actually reached,
		// and a slot that is over is marked so that nothing is measured on it.
		for next < len(want) && (over || want[next] <= g.Ply()) {
			pos := ebMidgamePos{
				requested: want[next],
				actual:    g.Ply(),
				g:         g.Clone(),
				setup:     ebSetup{opening: setup.opening, moves: slices.Clone(setup.moves)},
			}
			if over {
				pos.terminal = true
				pos.outcome = ebOutcomeName(res.Outcome)
				pos.reason = ebReasonName(res.Reason)
			}
			out = append(out, pos)
			next++
		}
		if next == len(want) {
			return out, nil
		}
		e := vert
		if g.Turn() == game.Horizontal {
			e = horz
		}
		mv, err := e.Move(ctx, g)
		if err != nil {
			return nil, fmt.Errorf("%dx%d seed %d setup at ply %d: %w", size, size, seed, g.Ply(), err)
		}
		if _, err := g.PlayPeg(mv); err != nil {
			return nil, fmt.Errorf("%dx%d seed %d setup played illegal %v at ply %d: %w", size, size, seed, mv, g.Ply(), err)
		}
		setup.moves = append(setup.moves, mv.String())
	}
}

// ebProbe runs one candidate on one position and leaves the position untouched.
func ebProbe(pl *ebPlayer, pos *game.Game) (ebProbeRecord, error) {
	rec := ebProbeRecord{Candidate: pl.spec.name}
	before := game.PositionDigest(pos)
	g := pos.Clone()
	ctx := context.Background()
	switch {
	case pl.direct:
		// The tuned candidates exist to be compared as searches, so the search
		// root is called directly: that yields the value as well as the move,
		// and skips the root sampling that only exists to make a tier weak.
		rec.Path = "searcher.root"
		start := time.Now()
		res, err := pl.own.root(ctx, g)
		rec.WallNS = time.Since(start).Nanoseconds()
		if err != nil {
			return rec, err
		}
		score := res.score
		rec.Move, rec.Score, rec.RootDepth = res.best.String(), &score, res.depth
	default:
		rec.Path = "Bot.Move"
		start := time.Now()
		mv, err := pl.bot.Move(ctx, g)
		rec.WallNS = time.Since(start).Nanoseconds()
		if err != nil {
			return rec, err
		}
		rec.Move = mv.String()
	}
	rec.Stats = pl.stats()
	if rec.RootDepth == 0 {
		rec.RootDepth = rec.Stats.Depth
	}
	if after := game.PositionDigest(g); after != before {
		return rec, fmt.Errorf("candidate %s left the position changed (%s -> %s); a search that does not restore the board invalidates every later probe",
			pl.spec.name, before, after)
	}
	return rec, nil
}

func ebRunPositions(t *testing.T, cfg ebConfig, art *ebArtifact) error {
	t.Helper()
	specs := make([]ebCandidateSpec, 0, len(cfg.Candidates))
	for _, name := range cfg.Candidates {
		spec, err := ebLookupCandidate(name)
		if err != nil {
			return err
		}
		specs = append(specs, spec)
	}
	summary := ebPositionSummary{}
	for _, size := range cfg.Sizes {
		for seed := cfg.SeedStart; seed < cfg.SeedStart+cfg.SeedCount; seed++ {
			positions, err := ebMidgames(size, seed, cfg.MidgamePlies)
			if err != nil {
				return err
			}
			for _, pos := range positions {
				summary.Requested++
				run := ebPositionRun{
					Size:         size,
					MidgamePlies: pos.requested,
					Seed:         seed,
					Opening:      pos.setup.opening,
					Setup:        pos.setup.moves,
					Digest:       game.PositionDigest(pos.g),
					Turn:         pos.g.Turn().String(),
					Plies:        pos.actual,
					Terminal:     pos.terminal,
					Outcome:      pos.outcome,
					Reason:       pos.reason,
				}
				if pos.terminal {
					// The horizon this slot asked for does not exist in this
					// game. The slot is recorded with the position the game
					// really ended in and nothing is measured on it: no
					// candidate is asked to move in a finished position, and
					// no comparison is made from a slot that has no probes.
					summary.Terminal++
					run.Note = fmt.Sprintf("the setup game was already over at ply %d, before the requested %d: no candidate was asked to move here, so this slot carries no measurement",
						pos.actual, pos.requested)
					art.Positions = append(art.Positions, run)
					art.Terminals = append(art.Terminals, ebTerminalSlot{
						Size:           size,
						Seed:           seed,
						RequestedPlies: pos.requested,
						TerminalPlies:  pos.actual,
						Outcome:        pos.outcome,
						Reason:         pos.reason,
						Digest:         run.Digest,
					})
					t.Logf("positions %dx%d seed %d ply %d SKIPPED: the setup game ended at ply %d (%s, %s); this slot is unmeasured",
						size, size, seed, pos.requested, pos.actual, pos.outcome, pos.reason)
					continue
				}
				byName := make(map[string]ebProbeRecord, len(specs))
				for _, spec := range specs {
					// A fresh player per probe: a warm transposition table
					// would make the second candidate cheaper than the first
					// for reasons that have nothing to do with its search.
					pl, err := ebNewPlayer(spec, int64(seed), cfg)
					if err != nil {
						return err
					}
					rec, err := ebProbe(pl, pos.g)
					if err != nil {
						return fmt.Errorf("%s on %dx%d seed %d at %d plies: %w", spec.name, size, size, seed, pos.requested, err)
					}
					run.Probes = append(run.Probes, rec)
					byName[spec.name] = rec
					t.Logf("positions %dx%d seed %d ply %d %-12s move %-4s depth %2d nodes %9d evals %9d search %8.1fms wall %8.1fms %s",
						size, size, seed, pos.requested, spec.name, rec.Move, rec.RootDepth,
						rec.Stats.Nodes, rec.Stats.Evaluations,
						float64(rec.Stats.SearchNS)/1e6, float64(rec.WallNS)/1e6, rec.Stats.StopReason)
				}
				summary.Measured++
				art.Positions = append(art.Positions, run)
				for i := 0; i+1 < len(specs); i++ {
					for j := i + 1; j < len(specs); j++ {
						art.Comparisons = append(art.Comparisons,
							ebCompare(run, byName[specs[i].name], byName[specs[j].name]))
					}
				}
			}
		}
	}
	art.PositionStats = &summary
	if summary.Terminal > 0 {
		art.Caveats = append(art.Caveats, fmt.Sprintf(
			"%d of the %d requested positions were already over before their requested ply and carry no measurement; they are listed under terminal_position_slots, and the measured sample is that much smaller than the request",
			summary.Terminal, summary.Requested))
	}
	if summary.Measured == 0 {
		// Nothing was measured, so there is nothing to report. Writing this
		// out as a successful run would present a file full of terminal
		// positions as evidence about searches that were never made.
		return fmt.Errorf("no position was measurable: every one of the %d requested slots was already over before its requested ply, so this run establishes nothing; ask for fewer plies (%s) or other opening seeds",
			summary.Requested, ebEnvMidgame)
	}
	t.Logf("positions: %d of %d requested slots measured, %d already over before their requested ply",
		summary.Measured, summary.Requested, summary.Terminal)
	return nil
}

// ebCompare states what two probes of one position agreed on. A node ratio is
// only meaningful when both sides reached the same depth and both counted
// nodes at all, and the record says which of those held.
func ebCompare(run ebPositionRun, a, b ebProbeRecord) ebPositionComparison {
	out := ebPositionComparison{
		Size: run.Size, MidgamePlies: run.MidgamePlies, Seed: run.Seed, Digest: run.Digest,
		A: a.Candidate, B: b.Candidate,
		SameMove:  a.Move == b.Move,
		SameDepth: a.RootDepth == b.RootDepth,
		DepthA:    a.RootDepth, DepthB: b.RootDepth,
		NodesA: a.Stats.Nodes, NodesB: b.Stats.Nodes,
		EvalsA: a.Stats.Evaluations, EvalsB: b.Stats.Evaluations,
		WallNSA: a.WallNS, WallNSB: b.WallNS,
	}
	if a.Score != nil && b.Score != nil {
		same := *a.Score == *b.Score
		out.SameScore = &same
	}
	if a.Stats.Nodes > 0 && b.Stats.Nodes > 0 {
		out.NodeRatio = float64(b.Stats.Nodes) / float64(a.Stats.Nodes)
	}
	if a.Stats.Evaluations > 0 && b.Stats.Evaluations > 0 {
		out.EvalRatio = float64(b.Stats.Evaluations) / float64(a.Stats.Evaluations)
	}
	switch {
	case a.Stats.Source != b.Stats.Source:
		out.Note = fmt.Sprintf("different architectures (%s vs %s): only the evaluation count and the wall time are like for like", a.Stats.Source, b.Stats.Source)
	case !out.SameDepth:
		out.Note = "different depths reached: the cost figures are not a like-for-like comparison"
	case a.Stats.Nodes == 0 || b.Stats.Nodes == 0:
		out.Note = "at least one candidate reports no node count: compare on wall time and evaluations only"
	case out.SameScore == nil:
		out.Comparable = true
		out.Note = "only one candidate reports a search value: agreement on the move is the strongest claim available here"
	default:
		out.Comparable = true
	}
	return out
}

// ebOutcome is one finished game reduced to what the score depends on. Only a
// game that produced a result becomes one: a game the harness could not play
// out is recorded on the match as an abort and never reaches the tally, because
// a result nobody has cannot be scored and must not be invented as a draw.
type ebOutcome struct {
	Seed      int
	ASide     game.Player
	Winner    game.Player
	Truncated bool
}

// ebTallyMatch turns finished games into the game tally and the paired units.
//
// The colour balance is checked rather than assumed: two games of one opening
// that gave A the same colour are not a pair, and a pair whose games do not
// both have a result is left out of the paired sample.
func ebTallyMatch(outcomes []ebOutcome) (ebTally, []ebPair, error) {
	var order []int
	byOpening := map[int][]ebOutcome{}
	for _, o := range outcomes {
		if _, ok := byOpening[o.Seed]; !ok {
			order = append(order, o.Seed)
		}
		byOpening[o.Seed] = append(byOpening[o.Seed], o)
	}

	var tally ebTally
	pairs := make([]ebPair, 0, len(order))
	for _, seed := range order {
		games := byOpening[seed]
		if len(games) != 2 {
			return ebTally{}, nil, fmt.Errorf("opening seed %d has %d games: the paired unit is one opening played from both sides",
				seed, len(games))
		}
		if games[0].ASide == games[1].ASide {
			return ebTally{}, nil, fmt.Errorf("opening seed %d gave A %v in both games: the pair is not colour balanced",
				seed, games[0].ASide)
		}
		pair := ebPair{Seed: seed}
		sum, scored := 0.0, 0
		for _, gm := range games {
			tally.Games++
			switch {
			case gm.Truncated:
				// An unfinished game has an unknown result. It is counted as
				// truncated and nowhere else.
				tally.Truncated++
				pair.Truncated++
			case gm.Winner == game.NoPlayer:
				tally.Draws++
				sum += 0.5
				scored++
			case gm.Winner == gm.ASide:
				tally.AWins++
				sum++
				scored++
			default:
				tally.BWins++
				scored++
			}
		}
		tally.Scored += scored
		if scored == len(games) {
			score := sum / float64(len(games))
			pair.Complete, pair.ScoreA = true, &score
		}
		pairs = append(pairs, pair)
	}
	return tally, pairs, nil
}

func ebPairScores(pairs []ebPair) []float64 {
	out := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		if p.Complete && p.ScoreA != nil {
			out = append(out, *p.ScoreA)
		}
	}
	return out
}

// ebIntervalMethod names the bound in the artifact so a reader does not have to
// guess which one was used.
const ebIntervalMethod = "conservative bounded-outcome (Hoeffding) two-sided 95% bound over independent opening-pair scores, clipped to [0,1]; conditional on the opening sample"

// ebHalfWidth is the Hoeffding half width for n bounded pair scores. It does
// not depend on the observed spread, which is the point: a unanimous small
// sample cannot shrink it to nothing.
func ebHalfWidth(n int) float64 {
	if n < 1 {
		return math.Inf(1)
	}
	return math.Sqrt(math.Log(ebConfidence) / (2 * float64(n)))
}

func ebClamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}

// ebPairedStats summarises the pair scores under the conservative bound.
func ebPairedStats(scores []float64, pairs int) ebPaired {
	out := ebPaired{
		Pairs:           pairs,
		ScoredPairs:     len(scores),
		IncompletePairs: pairs - len(scores),
		Method:          ebIntervalMethod,
	}
	n := len(scores)
	if n == 0 {
		out.Verdict = "no-scored-pairs"
		out.Notes = append(out.Notes, "no opening pair had both games finish, so there is no score at all")
		return out
	}
	mean := 0.0
	for _, s := range scores {
		mean += s
	}
	mean /= float64(n)
	out.Mean = &mean

	if n > 1 {
		varSum := 0.0
		for _, s := range scores {
			d := s - mean
			varSum += d * d
		}
		sd := math.Sqrt(varSum / float64(n-1))
		out.SD = &sd
	}

	half := ebHalfWidth(n)
	lo, hi := ebClamp01(mean-half), ebClamp01(mean+half)
	out.HalfWidth, out.Low, out.High = &half, &lo, &hi

	switch {
	case out.IncompletePairs > 0:
		// The pairs that have no score are missing evidence, not evidence of
		// a tie: they could have gone either way, and the worst case for the
		// leader is that every one of them was lost. The observed mean and
		// the bound over the pairs that did finish stay in the report, and
		// the verdict declines to read a direction off them.
		out.Verdict = "inconclusive-incomplete"
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%d of the %d requested pairs have no score, so no direction is claimed: the mean %.3f and the bound %.3f..%.3f describe only the %d pairs that finished, and the missing pairs could have gone either way",
			out.IncompletePairs, out.Pairs, mean, lo, hi, n))
	case lo > 0.5:
		out.Verdict = "a-stronger"
	case hi < 0.5:
		out.Verdict = "b-stronger"
	default:
		out.Verdict = "inconclusive"
		out.Notes = append(out.Notes, fmt.Sprintf(
			"the 95%% paired bound over %d pairs spans %.3f..%.3f and includes 0.5, so this run does not establish a gap in either direction", n, lo, hi))
	}
	if n < 8 {
		// Below eight pairs the half width exceeds 0.5, so even a clean sweep
		// cannot clear 0.5. Saying so is more useful than leaving the reader to
		// work out why a 12-0 result was called inconclusive.
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%d scored pairs cannot support a directional claim under this bound whatever the result: the half width alone is %.3f", n, half))
	}
	return out
}

func ebRunMatches(t *testing.T, cfg ebConfig, art *ebArtifact) error {
	t.Helper()
	specs := make([]ebCandidateSpec, 0, len(cfg.Candidates))
	for _, name := range cfg.Candidates {
		spec, err := ebLookupCandidate(name)
		if err != nil {
			return err
		}
		specs = append(specs, spec)
	}
	build := func(spec ebCandidateSpec, seed int64) (*ebPlayer, error) {
		return ebNewPlayer(spec, seed, cfg)
	}
	for _, size := range cfg.Sizes {
		openings, err := ebOpenings(size, cfg.SeedStart, cfg.SeedCount)
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(specs); i++ {
			for j := i + 1; j < len(specs); j++ {
				m, err := ebPlayMatch(t, cfg, size, specs[i], specs[j], openings, build)
				// The match is recorded either way. The games that finished
				// before an abort are receipts of real play, and dropping
				// them would destroy the only record that they happened.
				art.Matches = append(art.Matches, m)
				if err != nil {
					return err
				}
				t.Logf("match %dx%d %s vs %s: %d-%d-%d over %d games (%d truncated, %d pairs scored), paired mean %s in %s..%s, verdict %s",
					size, size, m.A, m.B, m.Tally.AWins, m.Tally.BWins, m.Tally.Draws,
					m.Tally.Games, m.Tally.Truncated, m.Paired.ScoredPairs,
					ebFormatFloat(m.Paired.Mean), ebFormatFloat(m.Paired.Low), ebFormatFloat(m.Paired.High),
					m.Paired.Verdict)
			}
		}
	}
	return nil
}

func ebFormatFloat(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.3f", *v)
}

// ebPlayerFor builds one candidate's player for one game. The match takes it
// as an argument rather than calling ebNewPlayer itself so that the abort path
// can be driven with a bot that fails: no roster candidate errors or returns an
// illegal move on purpose, and an abort path nothing ever exercises is an abort
// path nobody knows works.
type ebPlayerFor func(spec ebCandidateSpec, seed int64) (*ebPlayer, error)

// abort finishes the record of a match that could not be played out. The games
// that did finish stay on it, the game that failed is kept with the moves it
// managed and its error, and nothing is scored: the tally never sees a result
// the harness does not have.
func (m ebMatch) abort(start time.Time, failed *ebGameRecord, err error) ebMatch {
	if failed != nil {
		failed.Index = len(m.Games)
		m.Games = append(m.Games, *failed)
	}
	m.Aborted, m.Error = true, err.Error()
	m.Elapsed = time.Since(start).Nanoseconds()
	m.Paired = ebPaired{
		Method:  ebIntervalMethod,
		Verdict: "aborted",
		Notes: []string{
			"the match aborted before it could be scored: the games recorded here are what was actually played, and no tally or bound is computed from a match that did not finish",
		},
	}
	return m
}

// ebPlayMatch plays one colour-balanced match, one game at a time.
//
// Games are played sequentially and never in parallel: every game reports
// per-move wall and search times, and two searches sharing a machine would make
// both of those a measurement of the scheduler.
func ebPlayMatch(t *testing.T, cfg ebConfig, size int, specA, specB ebCandidateSpec, openings []ebOpeningRef, build ebPlayerFor) (ebMatch, error) {
	t.Helper()
	aName, bName := specA.name, specB.name
	out := ebMatch{Size: size, A: aName, B: bName, PlyCap: cfg.plyCap(size)}
	outcomes := make([]ebOutcome, 0, 2*len(openings))
	start := time.Now()
	for _, op := range openings {
		for assign := range 2 {
			aSide := game.Vertical
			if assign == 1 {
				aSide = game.Horizontal
			}
			// Fresh players per colour assignment, reused for every move of
			// their own game: that is what a real game gives a bot, and it
			// keeps one game's history and table out of the next.
			seed := int64(op.Seed)*2 + int64(assign)
			pa, err := build(specA, seed)
			if err != nil {
				return out.abort(start, nil, err), err
			}
			pb, err := build(specB, seed+ebSeedOffset)
			if err != nil {
				return out.abort(start, nil, err), err
			}
			vert, horz := pa, pb
			if aSide == game.Horizontal {
				vert, horz = pb, pa
			}
			rec, err := ebPlayGame(cfg, size, op, vert, horz, aSide, aName, bName)
			if err != nil {
				err = fmt.Errorf("%dx%d %s vs %s, opening seed %d, A as %v: %w",
					size, size, aName, bName, op.Seed, aSide, err)
				return out.abort(start, &rec, err), err
			}
			rec.Index = len(out.Games)
			out.Games = append(out.Games, rec)
			outcomes = append(outcomes, ebOutcome{
				Seed: op.Seed, ASide: aSide, Winner: rec.winner, Truncated: rec.Truncated,
			})
			note := ""
			if rec.Truncated {
				note = " (TRUNCATED at the ply cap, unscored)"
			}
			t.Logf("match %dx%d %s vs %s seed %d A=%v: %s by %s in %d plies%s",
				size, size, aName, bName, op.Seed, aSide, rec.Winner, rec.Reason, rec.Plies, note)
		}
	}
	out.Elapsed = time.Since(start).Nanoseconds()

	tally, pairs, err := ebTallyMatch(outcomes)
	if err != nil {
		return out.abort(start, nil, err), err
	}
	for i := range pairs {
		for gi, rec := range out.Games {
			if rec.Seed == pairs[i].Seed {
				pairs[i].Games = append(pairs[i].Games, gi)
				pairs[i].Opening = rec.Opening
			}
		}
	}
	out.Tally, out.Pairs = tally, pairs
	out.Paired = ebPairedStats(ebPairScores(pairs), len(pairs))
	return out, nil
}

// ebPlayGame plays one game and records every move's cost. An unfinished game
// is returned as truncated, not as a draw; anything that went wrong is returned
// as an error, which aborts the run, and the record comes back with the moves
// that were played, the failure written on it and no invented result.
func ebPlayGame(cfg ebConfig, size int, op ebOpeningRef, vert, horz *ebPlayer, aSide game.Player, aName, bName string) (ebGameRecord, error) {
	rec := ebGameRecord{
		Size: size, Seed: op.Seed, Opening: op.Point.String(),
		A: aName, B: bName, ASide: aSide.String(),
		PlyCap: cfg.plyCap(size),
	}
	g, err := game.New(ebRules(size))
	if err != nil {
		return rec, err
	}
	start := time.Now()
	// abort keeps the receipt of a game that could not be played out: the
	// moves that were made, the position they reached and what went wrong.
	//
	// The failure is recorded in Aborted and Error, which is the field pair
	// that classifies it. Outcome, Reason and Winner keep reporting what the
	// game itself reports — an unfinished game with no winner — because those
	// fields are the game's result vocabulary, and writing a token into them
	// that no game result can produce would be a classification the harness
	// invented. An unfinished game is not a draw, and it does not become one
	// here: the draw outcome is a distinct value it is never given, and the
	// tally only ever sees games that finished.
	abort := func(err error) (ebGameRecord, error) {
		rec.Aborted, rec.Error = true, err.Error()
		res := g.Result()
		rec.Outcome, rec.Reason = ebOutcomeName(res.Outcome), ebReasonName(res.Reason)
		rec.winner = res.Winner()
		rec.Winner = rec.winner.String()
		rec.Plies = g.Ply()
		rec.Digest = game.PositionDigest(g)
		rec.ElapsedNS = time.Since(start).Nanoseconds()
		if tr, terr := g.Transcript(); terr == nil {
			rec.Transcript = tr
		}
		return rec, err
	}
	if _, err := g.PlayPeg(op.Point); err != nil {
		return abort(fmt.Errorf("opening %v: %w", op.Point, err))
	}
	rec.Moves = append(rec.Moves, ebMoveRecord{
		Ply: g.Ply(), Side: game.Vertical.String(), Candidate: "opening", Move: op.Point.String(),
	})

	ctx := context.Background()
	for !g.Result().Over() {
		if g.Ply() >= rec.PlyCap {
			rec.Truncated = true
			break
		}
		side := g.Turn()
		pl := vert
		if side == game.Horizontal {
			pl = horz
		}
		t0 := time.Now()
		mv, err := pl.bot.Move(ctx, g)
		wall := time.Since(t0)
		if err != nil {
			return abort(fmt.Errorf("%s (%v) at ply %d: %w", pl.spec.name, side, g.Ply(), err))
		}
		stats := pl.stats()
		if _, err := g.PlayPeg(mv); err != nil {
			return abort(fmt.Errorf("%s (%v) played illegal %v at ply %d: %w", pl.spec.name, side, mv, g.Ply(), err))
		}
		rec.Moves = append(rec.Moves, ebMoveRecord{
			Ply: g.Ply(), Side: side.String(), Candidate: pl.spec.name, Move: mv.String(),
			WallNS: wall.Nanoseconds(), Stats: stats,
		})
	}
	rec.ElapsedNS = time.Since(start).Nanoseconds()
	res := g.Result()
	rec.Plies = g.Ply()
	rec.Outcome, rec.Reason = ebOutcomeName(res.Outcome), ebReasonName(res.Reason)
	rec.winner = res.Winner()
	rec.Winner = rec.winner.String()
	rec.Digest = game.PositionDigest(g)
	if tr, err := g.Transcript(); err == nil {
		rec.Transcript = tr
	}
	if rec.Truncated && rec.winner != game.NoPlayer {
		// The harness's own accounting is broken if this ever trips. The
		// record is kept exactly as it was built and marked aborted, so the
		// contradiction is visible in the artifact instead of smoothed over.
		err := fmt.Errorf("a truncated game reported %v as the winner", rec.winner)
		rec.Aborted, rec.Error = true, err.Error()
		return rec, err
	}
	return rec, nil
}

// BenchmarkEffortSearch is the warm hot-loop cost of one search on fixed
// positions. It is the microbenchmark half of the study: the position is built
// and the searcher warmed outside the timed loop, so what is measured is the
// search itself and not the first allocation of its buffers.
//
// The same environment knobs configure it. Candidates that expose no searcher
// are skipped: their cost is a game-level measurement, not a hot loop.
func BenchmarkEffortSearch(b *testing.B) {
	cfg, err := ebParseConfig(os.Getenv)
	if err != nil {
		b.Fatalf("effort benchmark configuration: %v", err)
	}
	measurable := 0
	for _, size := range cfg.Sizes {
		positions, err := ebMidgames(size, cfg.SeedStart, cfg.MidgamePlies)
		if err != nil {
			b.Fatalf("building positions: %v", err)
		}
		for _, pos := range positions {
			if pos.terminal {
				// A finished position has no search to time: the setup game
				// ended before this slot's requested ply.
				b.Logf("skipping %dx%d ply %d: the setup game was already over at ply %d (%s)",
					size, size, pos.requested, pos.actual, pos.outcome)
				continue
			}
			measurable++
			for _, name := range cfg.Candidates {
				spec, err := ebLookupCandidate(name)
				if err != nil {
					b.Fatalf("%v", err)
				}
				b.Run(fmt.Sprintf("%s/%dx%d/ply%d", name, size, size, pos.requested), func(b *testing.B) {
					pl, err := ebNewPlayer(spec, int64(cfg.SeedStart), cfg)
					if err != nil {
						b.Fatalf("%v", err)
					}
					if pl.own == nil {
						b.Skipf("candidate %s exposes no searcher to time", name)
					}
					if l := cfg.limitsFor(name); l.Nodes == 0 && l.Depth == 0 {
						// A clock-bounded search would report the clock, not
						// the cost of the work, so the loop needs a fixed
						// horizon to repeat.
						pl.own.p.maxDepth = ebProbeDepth
						pl.own.p.budget = ebNoClock
						b.Logf("no node or depth bound requested: timing at depth %d with a %v guard", ebProbeDepth, ebNoClock)
					}
					probe := pos.g.Clone()
					ctx := context.Background()
					// Warm-up outside the timer: the first search allocates the
					// per-ply buffers, the history tables and the transposition
					// table, and that is a startup cost, not a per-search one.
					if _, err := pl.own.root(ctx, probe); err != nil {
						b.Fatalf("warm-up search: %v", err)
					}
					var nodes, evals int64
					iters := 0
					b.ReportAllocs()
					for b.Loop() {
						if _, err := pl.own.root(ctx, probe); err != nil {
							b.Fatalf("search: %v", err)
						}
						nodes += pl.own.nodes
						evals += pl.own.evaluations
						iters++
					}
					if iters > 0 {
						b.ReportMetric(float64(nodes)/float64(iters), "nodes/op")
						b.ReportMetric(float64(evals)/float64(iters), "evals/op")
						b.ReportMetric(float64(pl.own.lastDepth), "depth")
					}
				})
			}
		}
	}
	if measurable == 0 {
		b.Fatalf("no requested position was measurable: every one was already over before its requested ply, so this benchmark timed nothing")
	}
}

// The tests below are not the experiment: they are the accounting rules the
// experiment's numbers depend on. They run in an ordinary test run because a
// scoring bug is silent, and a silent scoring bug turns a measurement into a
// fabrication.

func TestEffortTallyKeepsUnfinishedGamesOutOfTheScore(t *testing.T) {
	// Three openings. The first is a clean split, the second has one game that
	// hit the ply cap, the third is two draws.
	outcomes := []ebOutcome{
		{Seed: 1, ASide: game.Vertical, Winner: game.Vertical},
		{Seed: 1, ASide: game.Horizontal, Winner: game.Vertical},
		{Seed: 2, ASide: game.Vertical, Winner: game.Vertical},
		{Seed: 2, ASide: game.Horizontal, Truncated: true},
		{Seed: 3, ASide: game.Vertical, Winner: game.NoPlayer},
		{Seed: 3, ASide: game.Horizontal, Winner: game.NoPlayer},
	}
	tally, pairs, err := ebTallyMatch(outcomes)
	if err != nil {
		t.Fatalf("tally: %v", err)
	}
	if tally.Games != 6 || tally.Scored != 5 {
		t.Errorf("games %d scored %d, want 6 and 5", tally.Games, tally.Scored)
	}
	if tally.AWins != 2 || tally.BWins != 1 || tally.Draws != 2 {
		t.Errorf("W-L-D %d-%d-%d, want 2-1-2", tally.AWins, tally.BWins, tally.Draws)
	}
	if tally.Truncated != 1 {
		t.Errorf("truncated %d, want 1", tally.Truncated)
	}
	if got := tally.AWins + tally.BWins + tally.Draws; got != tally.Scored {
		t.Errorf("scored games %d do not add up to %d results: a truncated game was scored", tally.Scored, got)
	}
	if len(pairs) != 3 {
		t.Fatalf("pairs %d, want 3", len(pairs))
	}
	if !pairs[0].Complete || pairs[0].ScoreA == nil || *pairs[0].ScoreA != 0.5 {
		t.Errorf("pair 1 score %v, want a complete 0.5: a win as each colour is an even pair", pairs[0].ScoreA)
	}
	if pairs[1].Complete || pairs[1].ScoreA != nil {
		t.Errorf("pair 2 scored %v: a pair with an unfinished game cannot be scored", pairs[1].ScoreA)
	}
	if pairs[1].Truncated != 1 {
		t.Errorf("pair 2 truncated %d, want 1", pairs[1].Truncated)
	}
	if !pairs[2].Complete || pairs[2].ScoreA == nil || *pairs[2].ScoreA != 0.5 {
		t.Errorf("pair 3 score %v, want a complete 0.5", pairs[2].ScoreA)
	}
	if scores := ebPairScores(pairs); len(scores) != 2 {
		t.Errorf("scored pairs %v, want the two complete ones", scores)
	}
	// The paired report has to say that a pair was dropped rather than quietly
	// summarising two pairs as if three had been played, and it must not turn
	// the pairs that did finish into a direction while one is missing.
	rep := ebPairedStats(ebPairScores(pairs), len(pairs))
	if rep.Pairs != 3 || rep.ScoredPairs != 2 || rep.IncompletePairs != 1 {
		t.Errorf("paired report %d pairs, %d scored, %d incomplete; want 3, 2 and 1",
			rep.Pairs, rep.ScoredPairs, rep.IncompletePairs)
	}
	if rep.Verdict != "inconclusive-incomplete" {
		t.Errorf("verdict %q with one pair unscored, want inconclusive-incomplete", rep.Verdict)
	}
}

func TestEffortTallyRejectsUnscorableMatches(t *testing.T) {
	cases := map[string][]ebOutcome{
		"an unpaired opening": {
			{Seed: 1, ASide: game.Vertical, Winner: game.Vertical},
		},
		"three games from one opening": {
			{Seed: 1, ASide: game.Vertical, Winner: game.Vertical},
			{Seed: 1, ASide: game.Horizontal, Winner: game.Vertical},
			{Seed: 1, ASide: game.Vertical, Winner: game.Horizontal},
		},
		"the same colour twice": {
			{Seed: 1, ASide: game.Vertical, Winner: game.Vertical},
			{Seed: 1, ASide: game.Vertical, Winner: game.Horizontal},
		},
	}
	for name, outcomes := range cases {
		tally, pairs, err := ebTallyMatch(outcomes)
		if err == nil {
			t.Errorf("%s: tallied %+v with pairs %v, want an error rather than a score", name, tally, pairs)
		}
		if tally.Games != 0 || len(pairs) != 0 {
			t.Errorf("%s: partial tally %+v returned alongside the error", name, tally)
		}
	}
}

func TestEffortPairedBoundRefusesToOverclaim(t *testing.T) {
	sweep := func(n int, v float64) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = v
		}
		return out
	}
	rep := func(scores []float64) ebPaired {
		return ebPairedStats(scores, len(scores))
	}

	if got := rep(nil); got.Verdict != "no-scored-pairs" || got.Mean != nil || got.Low != nil {
		t.Errorf("no pairs: verdict %q mean %v bound %v, want no-scored-pairs with nothing reported",
			got.Verdict, got.Mean, got.Low)
	}
	// A single won pair is the certainty trap: the bound has to stay wide
	// enough to be useless rather than reporting a proven gap.
	one := rep([]float64{1})
	if one.Verdict != "inconclusive" {
		t.Errorf("one won pair: verdict %q, want inconclusive", one.Verdict)
	}
	if one.Low == nil || *one.Low != 0 || one.High == nil || *one.High != 1 {
		t.Errorf("one won pair: bound %s..%s, want the whole [0,1] range",
			ebFormatFloat(one.Low), ebFormatFloat(one.High))
	}
	// Four clean-sweep pairs still cannot clear 0.5 under this bound.
	if got := rep(sweep(4, 1)); got.Verdict != "inconclusive" {
		t.Errorf("four won pairs: verdict %q, want inconclusive", got.Verdict)
	}
	// Twelve clean-sweep pairs can: the recorded floor is about 0.608.
	twelve := rep(sweep(12, 1))
	if twelve.Verdict != "a-stronger" {
		t.Errorf("twelve won pairs: verdict %q, want a-stronger", twelve.Verdict)
	}
	if twelve.Low == nil || math.Abs(*twelve.Low-0.6078) > 0.001 {
		t.Errorf("twelve won pairs: lower bound %s, want about 0.608", ebFormatFloat(twelve.Low))
	}
	if twelve.High == nil || *twelve.High != 1 {
		t.Errorf("twelve won pairs: upper bound %s, want it clipped to 1", ebFormatFloat(twelve.High))
	}
	// A unanimous sample must not report zero width: that is exactly the
	// degeneracy a t interval or a percentile bootstrap would produce here.
	if twelve.HalfWidth == nil || *twelve.HalfWidth <= 0 {
		t.Errorf("twelve won pairs: half width %s, want a positive width even with no observed spread",
			ebFormatFloat(twelve.HalfWidth))
	}
	if sd := twelve.SD; sd == nil || *sd != 0 {
		t.Errorf("twelve won pairs: sd %v, want the observed zero spread reported alongside the bound", sd)
	}
	mirror := rep(sweep(12, 0))
	if mirror.Verdict != "b-stronger" {
		t.Errorf("twelve lost pairs: verdict %q, want b-stronger", mirror.Verdict)
	}
	if mirror.Low == nil || *mirror.Low != 0 {
		t.Errorf("twelve lost pairs: lower bound %s, want it clipped to 0", ebFormatFloat(mirror.Low))
	}
	// A genuinely close match stays inconclusive however many pairs it has.
	even := make([]float64, 0, 40)
	for range 20 {
		even = append(even, 0.75, 0.25)
	}
	if got := rep(even); got.Verdict != "inconclusive" {
		t.Errorf("even 40-pair match: verdict %q, want inconclusive", got.Verdict)
	}
	// The bound must narrow with more pairs, or it is not a function of the
	// sample size at all.
	if a, b := ebHalfWidth(12), ebHalfWidth(48); !(a > b && b > 0) {
		t.Errorf("half widths %v at 12 pairs and %v at 48: want a positive, shrinking width", a, b)
	}
}

func TestEffortOpeningsAreSeedStableAndUnique(t *testing.T) {
	for _, size := range []int{10, 16, 24} {
		dev, err := ebOpenings(size, 1, 12)
		if err != nil {
			t.Fatalf("%dx%d development openings: %v", size, size, err)
		}
		holdout, err := ebOpenings(size, 101, 12)
		if err != nil {
			t.Fatalf("%dx%d holdout openings: %v", size, size, err)
		}
		seen := map[game.Point]int{}
		for _, refs := range [][]ebOpeningRef{dev, holdout} {
			for _, ref := range refs {
				if prev, dup := seen[ref.Point]; dup {
					t.Errorf("%dx%d: seeds %d and %d share opening %v; the two ranges would replay games",
						size, size, prev, ref.Seed, ref.Point)
				}
				seen[ref.Point] = ref.Seed
			}
		}
		// The same seed has to resolve to the same hole whatever range it was
		// asked for in, or a holdout is not comparable with a development run.
		for _, ref := range append(slices.Clone(dev), holdout...) {
			single, err := ebOpenings(size, ref.Seed, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got := single[0]; got.Point != ref.Point {
				t.Errorf("%dx%d seed %d: %v in its range but %v requested alone",
					size, size, ref.Seed, ref.Point, got.Point)
			}
		}
	}
	// A range longer than the board has openings for must be refused rather
	// than silently repeating a pair.
	pool, err := ebOpeningPool(6)
	if err != nil {
		t.Fatalf("6x6 pool: %v", err)
	}
	if _, err := ebOpenings(6, 1, len(pool)+1); err == nil {
		t.Errorf("a range of %d on a board with %d openings was accepted", len(pool)+1, len(pool))
	}
	if _, err := ebOpenings(6, math.MaxInt, 1); err == nil {
		t.Error("a seed range whose end overflows was accepted; it would have played no games and reported no failure")
	}
	if _, err := ebOpenings(6, 1, 0); err == nil {
		t.Error("a seed range of zero openings was accepted")
	}
}

func TestEffortConfigRejectsUnrunnableRequests(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(name string) string { return kv[name] }
	}
	bad := map[string]map[string]string{
		"unknown mode":             {ebEnvMode: "tournament"},
		"unknown candidate":        {ebEnvCandidates: "plain,quantum"},
		"repeated candidate":       {ebEnvCandidates: "plain,plain"},
		"one candidate in a match": {ebEnvMode: "match", ebEnvCandidates: "pvs"},
		"unusable board size":      {ebEnvSizes: "2"},
		"repeated board size":      {ebEnvSizes: "10,10"},
		"zero midgame plies":       {ebEnvMidgame: "0"},
		"seed below one":           {ebEnvSeedStart: "0"},
		"no openings":              {ebEnvSeedCount: "0"},
		"negative nodes":           {ebEnvNodes: "-1"},
		"negative depth":           {ebEnvDepth: "-3"},
		"depth past the ceiling":   {ebEnvDepth: strconv.Itoa(MaxDepth + 1)},
		"negative clock":           {ebEnvTime: "-1s"},
		"unparsable clock":         {ebEnvTime: "3 seconds"},
		"negative simulations":     {ebEnvIterations: "-10"},
		"negative ply cap":         {ebEnvPlyCap: "-4"},
		"unknown override target":  {ebEnvDepth: "plain=4,quantum=6"},
		"two global overrides":     {ebEnvDepth: "4,6"},
		"repeated override":        {ebEnvDepth: "plain=4,plain=6"},
	}
	for name, kv := range bad {
		if cfg, err := ebParseConfig(env(kv)); err == nil {
			t.Errorf("%s: accepted %v as %+v", name, kv, cfg.limits)
		}
	}

	cfg, err := ebParseConfig(env(map[string]string{
		ebEnvMode:       "match",
		ebEnvCandidates: "pro,max",
		ebEnvSizes:      "10,16",
		ebEnvSeedStart:  "101",
		ebEnvSeedCount:  "12",
		ebEnvTime:       "*=1h,max=10s",
		ebEnvDepth:      "pro=6,max=8",
		ebEnvNodes:      "250000",
		ebEnvPlyCap:     "150",
	}))
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if cfg.Mode != ebModeMatch || cfg.SeedStart != 101 || cfg.SeedCount != 12 {
		t.Errorf("parsed %v seeds %d..%d, want a match over 101..112", cfg.Mode, cfg.SeedStart, cfg.SeedStart+cfg.SeedCount-1)
	}
	// Per-candidate limits are the point of the syntax: the two tiers must be
	// allowed different horizons, with the global value filling the rest.
	if got := cfg.limitsFor("pro"); got.Depth != 6 || got.Time != time.Hour || got.Nodes != 250_000 {
		t.Errorf("pro limits %+v, want depth 6, a 1h guard and 250000 nodes", got)
	}
	if got := cfg.limitsFor("max"); got.Depth != 8 || got.Time != 10*time.Second || got.Nodes != 250_000 {
		t.Errorf("max limits %+v, want depth 8, a 10s clock and 250000 nodes", got)
	}
	if cfg.plyCap(16) != 150 {
		t.Errorf("ply cap %d, want the requested 150", cfg.plyCap(16))
	}
	if len(cfg.defaulted) != 0 {
		t.Errorf("limits defaulted for %v in a fully specified match request", cfg.defaulted)
	}

	// A position run with no work bound gets a deterministic one, because the
	// alternative is a probe that reports the machine's speed.
	probe, err := ebParseConfig(env(map[string]string{ebEnvCandidates: "plain,pvs"}))
	if err != nil {
		t.Fatalf("default position request rejected: %v", err)
	}
	for _, name := range probe.Candidates {
		if got := probe.limitsFor(name); got.Depth != ebProbeDepth || got.Time != ebNoClock {
			t.Errorf("%s probe limits %+v, want depth %d with a %v guard", name, got, ebProbeDepth, ebNoClock)
		}
		if why := probe.defaulted[name]; !strings.Contains(why, ebNoClock.String()) {
			t.Errorf("%s limits provenance %q does not name the %v guard the harness supplied", name, why, ebNoClock)
		}
	}
	if len(probe.defaulted) != len(probe.Candidates) {
		t.Errorf("defaulted %v, want every candidate recorded as defaulted", probe.defaulted)
	}
	// The provenance sentence is what the artifact records, so it has to name
	// the clock that was actually applied. A candidate given an explicit
	// clock and no work bound keeps that clock, and claiming the harness
	// guard it never had would be a fabricated protocol.
	explicit, err := ebParseConfig(env(map[string]string{ebEnvCandidates: "plain", ebEnvTime: "1ms"}))
	if err != nil {
		t.Fatalf("explicit-clock request rejected: %v", err)
	}
	if got := explicit.limitsFor("plain"); got.Time != time.Millisecond || got.Depth != ebProbeDepth {
		t.Errorf("explicit-clock limits %+v, want the requested 1ms with the default probe depth", got)
	}
	why := explicit.defaulted["plain"]
	switch {
	case why == "":
		t.Errorf("no limits provenance recorded for a candidate whose depth the harness supplied")
	case strings.Contains(why, ebNoClock.String()):
		t.Errorf("limits provenance %q claims the %v harness guard for a candidate given an explicit 1ms clock", why, ebNoClock)
	case !strings.Contains(why, "1ms"):
		t.Errorf("limits provenance %q does not name the clock that was applied", why)
	}
	// An explicit node bound is a work bound, so nothing is defaulted and the
	// preset depth is left alone.
	nodeBound, err := ebParseConfig(env(map[string]string{ebEnvCandidates: "plain", ebEnvNodes: "1000"}))
	if err != nil {
		t.Fatalf("node-bounded request rejected: %v", err)
	}
	if got := nodeBound.limitsFor("plain"); got.Nodes != 1000 || got.Depth != 0 || got.Time != 0 {
		t.Errorf("node-bounded limits %+v, want only the node bound set", got)
	}
	if len(nodeBound.defaulted) != 0 {
		t.Errorf("defaulted %v for an explicitly node-bounded run", nodeBound.defaulted)
	}
	// The monte-carlo candidate is bounded by its simulation count, and that
	// bound has to reach it per candidate without leaking onto the others.
	mcts, err := ebParseConfig(env(map[string]string{
		ebEnvMode:       "match",
		ebEnvCandidates: "pvs,mcts",
		ebEnvIterations: "mcts=2000",
	}))
	if err != nil {
		t.Fatalf("monte-carlo request rejected: %v", err)
	}
	if mcts.simsFor("mcts") != 2000 {
		t.Errorf("mcts simulations %d, want 2000", mcts.simsFor("mcts"))
	}
	if mcts.simsFor("pvs") != 0 {
		t.Errorf("pvs picked up the monte-carlo simulation bound: %d", mcts.simsFor("pvs"))
	}
}

func TestEffortPairedVerdictWaitsForEveryPair(t *testing.T) {
	// Eight openings A sweeps and four it could not finish. Eight sweeps
	// alone clear the bound, which is exactly the trap: the four pairs with
	// no score could all have gone the other way, and a directional verdict
	// off the eight would be reading dropped games as strength.
	var outcomes []ebOutcome
	for seed := 1; seed <= 8; seed++ {
		outcomes = append(outcomes,
			ebOutcome{Seed: seed, ASide: game.Vertical, Winner: game.Vertical},
			ebOutcome{Seed: seed, ASide: game.Horizontal, Winner: game.Horizontal},
		)
	}
	for seed := 9; seed <= 12; seed++ {
		outcomes = append(outcomes,
			ebOutcome{Seed: seed, ASide: game.Vertical, Winner: game.Vertical},
			ebOutcome{Seed: seed, ASide: game.Horizontal, Truncated: true},
		)
	}
	tally, pairs, err := ebTallyMatch(outcomes)
	if err != nil {
		t.Fatalf("tally: %v", err)
	}
	if len(pairs) != 12 || tally.Truncated != 4 {
		t.Fatalf("tallied %d pairs with %d truncated games, want 12 and 4", len(pairs), tally.Truncated)
	}
	rep := ebPairedStats(ebPairScores(pairs), len(pairs))
	if rep.ScoredPairs != 8 || rep.IncompletePairs != 4 {
		t.Fatalf("paired report %d scored and %d incomplete, want 8 and 4", rep.ScoredPairs, rep.IncompletePairs)
	}
	if rep.Verdict != "inconclusive-incomplete" {
		t.Errorf("verdict %q over 8 swept pairs with 4 unscored, want inconclusive-incomplete: the missing pairs are not evidence of strength",
			rep.Verdict)
	}
	// The observed numbers stay: what is withheld is the direction, not the
	// measurement, and the note has to name how much of the sample is gone.
	if rep.Mean == nil || *rep.Mean != 1 {
		t.Errorf("mean %s, want the observed 1.000 over the pairs that finished", ebFormatFloat(rep.Mean))
	}
	if rep.Low == nil || *rep.Low <= 0.5 {
		t.Errorf("conditional bound %s..%s, want the bound over the scored pairs reported unchanged",
			ebFormatFloat(rep.Low), ebFormatFloat(rep.High))
	}
	if notes := strings.Join(rep.Notes, " "); !strings.Contains(notes, "4 of the 12 requested pairs") {
		t.Errorf("notes %q do not say how many pairs have no score", notes)
	}
	// Control: the same eight swept pairs with nothing missing do support the
	// direction. Without this, the test above would also pass if the bound
	// had simply been widened until nothing could ever be claimed.
	complete := ebPairedStats(ebPairScores(pairs[:8]), 8)
	if complete.Verdict != "a-stronger" {
		t.Errorf("eight complete swept pairs: verdict %q, want a-stronger; the incomplete case must differ because pairs are missing, not because the bound moved",
			complete.Verdict)
	}
}

// ebFirstLegal is a deterministic stand-in player: it takes the first hole the
// rules allow. It searches nothing, so a test can drive whole games with it in
// microseconds, which is what makes the runner's own paths testable at all.
type ebFirstLegal struct{}

func (ebFirstLegal) Tier() Tier { return Intermediate }

func (ebFirstLegal) Hint(context.Context, *game.Game) (Hint, error) {
	return Hint{}, errors.New("bot: the first-legal test bot gives no hints")
}

func (ebFirstLegal) Move(_ context.Context, g *game.Game) (game.Point, error) {
	legal := g.LegalPlacements(g.Turn())
	if len(legal) == 0 {
		return game.Point{}, fmt.Errorf("no legal placement for %v", g.Turn())
	}
	return legal[0], nil
}

// ebFailBot fails the way a test asked: an error out of Move, or a move the
// rules refuse. No roster candidate does either on purpose, so a bot that
// does is the only way to drive the harness's abort path and see what it
// records.
type ebFailBot struct {
	illegal *game.Point
	err     error
}

func (*ebFailBot) Tier() Tier { return Intermediate }

func (*ebFailBot) Hint(context.Context, *game.Game) (Hint, error) {
	return Hint{}, errors.New("bot: the failing test bot gives no hints")
}

func (b *ebFailBot) Move(context.Context, *game.Game) (game.Point, error) {
	if b.illegal != nil {
		return *b.illegal, nil
	}
	return game.Point{}, b.err
}

func TestEffortPlayGameAbortsRatherThanInventingAResult(t *testing.T) {
	hole, err := ebOpening(6, 1)
	if err != nil {
		t.Fatalf("6x6 opening: %v", err)
	}
	op := ebOpeningRef{Seed: 1, Point: hole}
	offBoard := game.Point{Col: -1, Row: -1}
	cfg := ebConfig{Mode: ebModeMatch, PlyCap: 12}
	cases := []struct {
		name string
		bot  Bot
		want string
	}{
		{"a bot that errors", &ebFailBot{err: errors.New("the search exploded")}, "the search exploded"},
		{"a bot that returns an illegal move", &ebFailBot{illegal: &offBoard}, "played illegal"},
	}
	for _, tc := range cases {
		broken := &ebPlayer{spec: ebCandidateSpec{name: "broken"}, bot: tc.bot}
		sound := &ebPlayer{spec: ebCandidateSpec{name: "sound"}, bot: ebFirstLegal{}}
		rec, err := ebPlayGame(cfg, 6, op, broken, sound, game.Vertical, "broken", "sound")
		if err == nil {
			t.Fatalf("%s: the game returned %s by %s instead of an error", tc.name, rec.Winner, rec.Reason)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not report %q", tc.name, err, tc.want)
		}
		if !strings.Contains(err.Error(), "broken") {
			t.Errorf("%s: error %q does not name the candidate that failed", tc.name, err)
		}
		// The failure is classified by Aborted and Error. The result fields
		// keep reporting the game's own state — unfinished, nobody won — and
		// must not carry a token the game's own vocabulary cannot produce.
		if !rec.Aborted || rec.Error == "" {
			t.Errorf("%s: record aborted=%v error=%q, want the abort written on the receipt", tc.name, rec.Aborted, rec.Error)
		}
		if rec.Outcome != ebOutcomeName(game.Ongoing) || rec.Reason != ebReasonName(game.NotOver) {
			t.Errorf("%s: recorded outcome %q reason %q, want the unfinished position the game actually reports",
				tc.name, rec.Outcome, rec.Reason)
		}
		if rec.Outcome == ebOutcomeName(game.Draw) {
			t.Errorf("%s: an aborted game was recorded as a draw", tc.name)
		}
		if rec.winner != game.NoPlayer {
			t.Errorf("%s: aborted game reported %v as the winner", tc.name, rec.winner)
		}
		if rec.Truncated {
			t.Errorf("%s: an aborted game was also called truncated, which is a scoring category", tc.name)
		}
		// The moves that were played are receipts and are kept: the opening,
		// then the sound player's reply, then the failure.
		if len(rec.Moves) != 2 {
			t.Fatalf("%s: kept %d move records, want the opening and the one move that was played", tc.name, len(rec.Moves))
		}
		if rec.Moves[0].Candidate != "opening" || rec.Moves[1].Candidate != "sound" {
			t.Errorf("%s: move receipts %+v, want the opening then the sound player's move", tc.name, rec.Moves)
		}
		if rec.Plies != 2 || rec.Digest == "" {
			t.Errorf("%s: recorded %d plies and digest %q, want the position the game reached", tc.name, rec.Plies, rec.Digest)
		}
	}
}

func TestEffortMatchKeepsWhatWasPlayedWhenItAborts(t *testing.T) {
	openings, err := ebOpenings(6, 1, 2)
	if err != nil {
		t.Fatalf("6x6 openings: %v", err)
	}
	cfg := ebConfig{Mode: ebModeMatch, PlyCap: 36}
	specA := ebCandidateSpec{name: "sound"}
	specB := ebCandidateSpec{name: "breaks-on-the-third-game"}
	built := 0
	build := func(spec ebCandidateSpec, seed int64) (*ebPlayer, error) {
		if spec.name == specA.name {
			return &ebPlayer{spec: spec, bot: ebFirstLegal{}}, nil
		}
		built++
		if built < 3 {
			return &ebPlayer{spec: spec, bot: ebFirstLegal{}}, nil
		}
		return &ebPlayer{spec: spec, bot: &ebFailBot{err: errors.New("the third game explodes")}}, nil
	}
	m, err := ebPlayMatch(t, cfg, 6, specA, specB, openings, build)
	if err == nil {
		t.Fatalf("the match completed %d games with a bot that fails", len(m.Games))
	}
	if !strings.Contains(err.Error(), "the third game explodes") {
		t.Errorf("error %q does not carry the bot's failure", err)
	}
	if !m.Aborted || m.Error == "" {
		t.Errorf("match aborted=%v error=%q, want the abort recorded on the match", m.Aborted, m.Error)
	}
	// Two games were really played before the third failed. Losing them would
	// destroy the only record that they happened.
	if len(m.Games) != 3 {
		t.Fatalf("kept %d game records, want the two that finished plus the one that failed", len(m.Games))
	}
	for i, rec := range m.Games[:2] {
		if rec.Aborted || rec.Error != "" {
			t.Errorf("game %d was recorded as aborted: %q", i, rec.Error)
		}
		if rec.Digest == "" || len(rec.Moves) == 0 {
			t.Errorf("game %d kept no receipt: digest %q, %d moves", i, rec.Digest, len(rec.Moves))
		}
	}
	if last := m.Games[2]; !last.Aborted || last.Error == "" {
		t.Errorf("the failed game was recorded as aborted=%v error=%q", last.Aborted, last.Error)
	}
	for i, rec := range m.Games {
		if rec.Index != i {
			t.Errorf("game %d carries index %d: the pair references in the artifact would not resolve", i, rec.Index)
		}
	}
	// Nothing is scored: a match that did not finish has no tally, and the
	// two games that did finish are not a match result.
	if m.Tally.Games != 0 || len(m.Pairs) != 0 {
		t.Errorf("an aborted match was scored: tally %+v over %d pairs", m.Tally, len(m.Pairs))
	}
	if m.Paired.Verdict != "aborted" || m.Paired.Mean != nil {
		t.Errorf("paired report %q with mean %s, want an aborted verdict and no score",
			m.Paired.Verdict, ebFormatFloat(m.Paired.Mean))
	}
}

func TestEffortMidgamesKeepEveryRequestedSlot(t *testing.T) {
	// Six plies is reachable on a 10x10 board. Ninety-nine is not: the two
	// sides share 96 usable holes, and the game ends when the side to move
	// has none left. The unreachable slot is the case that used to abort
	// every position of the run, including the ones that were measurable.
	got, err := ebMidgames(10, 1, []int{6, 99})
	if err != nil {
		t.Fatalf("midgames: %v; a horizon the setup game cannot reach must be recorded, not refused", err)
	}
	if len(got) != 2 {
		t.Fatalf("built %d positions for 2 requested plies: every requested slot has to come back", len(got))
	}
	early, late := got[0], got[1]
	if early.requested != 6 || early.actual != 6 {
		t.Errorf("early slot requested %d and sits at %d, want both at 6", early.requested, early.actual)
	}
	if early.terminal || early.g == nil || early.g.Result().Over() {
		t.Errorf("the 6-ply slot came back terminal (%v); a measurable position must survive an unreachable sibling", early.terminal)
	}
	if early.g != nil && early.g.Ply() != 6 {
		t.Errorf("the 6-ply slot holds a position at ply %d", early.g.Ply())
	}
	if late.requested != 99 {
		t.Errorf("late slot requested %d, want the 99 that was asked for", late.requested)
	}
	if !late.terminal {
		t.Fatalf("the 99-ply slot is not marked terminal, though the setup game stopped at ply %d", late.actual)
	}
	if late.actual >= late.requested {
		t.Errorf("terminal slot reports ply %d for a requested %d", late.actual, late.requested)
	}
	if late.g == nil || !late.g.Result().Over() {
		t.Errorf("the terminal slot does not hold a finished position")
	}
	if late.outcome == "" || late.reason == "" {
		t.Errorf("terminal slot outcome %q reason %q, want the result the setup game ended in", late.outcome, late.reason)
	}
	if late.setup.opening != early.setup.opening {
		t.Errorf("terminal slot opening %q, want the same opening as its siblings (%q)", late.setup.opening, early.setup.opening)
	}
	if len(late.setup.moves) != late.actual {
		t.Errorf("terminal slot kept %d setup moves for a position at ply %d", len(late.setup.moves), late.actual)
	}
	// A slot whose requested ply is exactly the terminal ply is terminal too:
	// there is no move to make in that position either, and probing it would
	// report a search on a finished game. The terminal ply is read off the
	// run above rather than pinned here, so this holds whatever the setup
	// tier plays.
	atEnd, err := ebMidgames(10, 1, []int{late.actual})
	if err != nil {
		t.Fatalf("midgames at the terminal ply %d: %v", late.actual, err)
	}
	if len(atEnd) != 1 {
		t.Fatalf("built %d positions for one requested ply", len(atEnd))
	}
	if slot := atEnd[0]; !slot.terminal || slot.requested != late.actual || slot.actual != late.actual {
		t.Errorf("the slot requested at the terminal ply %d came back terminal=%v at ply %d, requested %d: a finished position is terminal whether or not it was the horizon asked for",
			late.actual, slot.terminal, slot.actual, slot.requested)
	}
	if slot := atEnd[0]; slot.terminal && (slot.outcome == "" || slot.reason == "") {
		t.Errorf("the terminal slot at ply %d records outcome %q reason %q", slot.actual, slot.outcome, slot.reason)
	}
}

func TestEffortPositionRunReportsUnmeasurableSlots(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(name string) string { return kv[name] }
	}
	base := map[string]string{
		ebEnvCandidates: "plain,pvs",
		ebEnvSizes:      "10",
		ebEnvSeedStart:  "1",
		ebEnvSeedCount:  "1",
		ebEnvDepth:      "2",
	}
	mixed := map[string]string{ebEnvMidgame: "6,99"}
	for k, v := range base {
		mixed[k] = v
	}
	cfg, err := ebParseConfig(env(mixed))
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	art, err := ebNewArtifact(cfg)
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if err := ebRunPositions(t, cfg, art); err != nil {
		t.Fatalf("positions run: %v; one unreachable horizon must not lose the measurable one", err)
	}
	if len(art.Positions) != 2 {
		t.Fatalf("recorded %d position slots, want both requested plies", len(art.Positions))
	}
	measured, terminal := art.Positions[0], art.Positions[1]
	if measured.Terminal || len(measured.Probes) != 2 {
		t.Errorf("the 6-ply slot recorded terminal=%v with %d probes, want both candidates measured on it",
			measured.Terminal, len(measured.Probes))
	}
	if !terminal.Terminal {
		t.Fatalf("the 99-ply slot is not marked terminal: %+v", terminal)
	}
	if len(terminal.Probes) != 0 {
		t.Errorf("%d candidates were probed on a position that is already over", len(terminal.Probes))
	}
	if terminal.Outcome == "" || terminal.Note == "" {
		t.Errorf("terminal slot outcome %q note %q, want the result and why nothing was measured", terminal.Outcome, terminal.Note)
	}
	if terminal.MidgamePlies != 99 || terminal.Plies >= 99 {
		t.Errorf("terminal slot requested %d and reports ply %d, want the requested horizon beside the actual one",
			terminal.MidgamePlies, terminal.Plies)
	}
	// A slot with no probes cannot enter a comparison: a comparison from it
	// would be a claim about two searches that were never run.
	if len(art.Comparisons) != 1 {
		t.Fatalf("recorded %d comparisons, want the one from the measured slot", len(art.Comparisons))
	}
	if art.Comparisons[0].MidgamePlies != 6 {
		t.Errorf("comparison taken from the %d-ply slot, want the measured 6-ply one", art.Comparisons[0].MidgamePlies)
	}
	if art.PositionStats == nil {
		t.Fatalf("no position summary: the skipped slots have to be counted")
	}
	if got := *art.PositionStats; got.Requested != 2 || got.Measured != 1 || got.Terminal != 1 {
		t.Errorf("summary %+v, want 2 requested, 1 measured and 1 terminal", got)
	}
	if len(art.Terminals) != 1 {
		t.Fatalf("listed %d terminal slots, want the one that was skipped", len(art.Terminals))
	}
	if slot := art.Terminals[0]; slot.RequestedPlies != 99 || slot.Seed != 1 || slot.Outcome == "" {
		t.Errorf("terminal slot record %+v, want the requested ply, its seed and the outcome", slot)
	}
	if notes := strings.Join(art.Caveats, " "); !strings.Contains(notes, "requested positions were already over") {
		t.Errorf("caveats do not mention the skipped slots: %q", notes)
	}

	// A run in which nothing was measurable establishes nothing, and must not
	// be reported as a run that succeeded.
	empty := map[string]string{ebEnvMidgame: "98,99"}
	for k, v := range base {
		empty[k] = v
	}
	blank, err := ebParseConfig(env(empty))
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	art2, err := ebNewArtifact(blank)
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	err = ebRunPositions(t, blank, art2)
	if err == nil {
		t.Fatalf("a run with no measurable position returned success over %d slots", len(art2.Positions))
	}
	if !strings.Contains(err.Error(), "measurable") {
		t.Errorf("error %q does not say the run measured nothing", err)
	}
	if art2.PositionStats == nil || art2.PositionStats.Measured != 0 || art2.PositionStats.Terminal != 2 {
		t.Errorf("summary %+v, want both slots counted as terminal and none measured", art2.PositionStats)
	}
	if len(art2.Comparisons) != 0 {
		t.Errorf("%d comparisons from a run with no measured position", len(art2.Comparisons))
	}
}

func TestEffortProtocolReportsWhatEachContenderRanUnder(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(name string) string { return kv[name] }
	}
	cfg, err := ebParseConfig(env(map[string]string{
		ebEnvMode:       "match",
		ebEnvCandidates: "pvs,mcts",
		ebEnvSizes:      "10",
		ebEnvIterations: "mcts=64",
		ebEnvNodes:      "pvs=1000",
		ebEnvDepth:      "pvs=5",
		ebEnvTime:       "pvs=2s",
	}))
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	art, err := ebNewArtifact(cfg)
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	reports := map[string]ebCandidateReport{}
	for _, rep := range art.Protocol.Candidates {
		reports[rep.Name] = rep
	}
	pvs, ok := reports["pvs"]
	if !ok {
		t.Fatalf("no report for pvs in %+v", art.Protocol.Candidates)
	}
	if pvs.Limits == nil || pvs.Limits.Nodes != 1000 || pvs.Limits.Depth != 5 {
		t.Errorf("pvs limits %+v, want the requested node and depth bounds", pvs.Limits)
	}
	// The guards are the ones the search will actually consult, so they come
	// from the effective params rather than from the request.
	if pvs.Guards.Source != "effective search params" {
		t.Errorf("pvs guards taken from %q, want the effective params", pvs.Guards.Source)
	}
	if pvs.Guards.Nodes != 1000 || pvs.Guards.Depth != 5 || pvs.Guards.ClockNS != int64(2*time.Second) {
		t.Errorf("pvs guards %+v, want 1000 nodes, depth 5 and a 2s clock", pvs.Guards)
	}
	if pvs.Guards.Simulations != 0 || pvs.MCTSParams != nil {
		t.Errorf("pvs picked up monte-carlo reporting: %+v / %+v", pvs.Guards, pvs.MCTSParams)
	}
	mcts, ok := reports["mcts"]
	if !ok {
		t.Fatalf("no report for mcts in %+v", art.Protocol.Candidates)
	}
	// The alpha-beta levers are not applied to this contender, so reporting
	// them as its limits would be a claim about a search that never ran.
	if mcts.Limits != nil {
		t.Errorf("mcts reports alpha-beta limits %+v that were never applied to it", *mcts.Limits)
	}
	if mcts.Guards.Simulations != 64 || mcts.Guards.Nodes != 0 || mcts.Guards.Depth != 0 || mcts.Guards.ClockNS != 0 {
		t.Errorf("mcts guards %+v, want its simulation ceiling and nothing else", mcts.Guards)
	}
	if mcts.MCTSParams == nil {
		t.Fatalf("mcts reports no parameters, so the artifact cannot say what it ran")
	}
	if got, want := *mcts.MCTSParams, ebReportMCTSParams(defaultMCTSParams()); got != want {
		t.Errorf("mcts parameters %+v, want the ones the bot was built with %+v", got, want)
	}
	// A lever that moves has to move the report with it, or the protocol
	// describes a run that did not happen.
	moved := defaultMCTSParams()
	moved.rolloutPlies = 0
	if got := ebReportMCTSParams(moved); got.RolloutPlies != 0 || got == ebReportMCTSParams(defaultMCTSParams()) {
		t.Errorf("the parameter report does not follow its parameters: %+v", got)
	}
	// The report has to cover every lever, not only the ones that existed
	// when it was written: a parameter that never reaches the artifact makes
	// the protocol an incomplete description of the run. A renamed field is
	// caught by the compiler in ebReportMCTSParams; a new one is caught here.
	if got, want := reflect.TypeOf(ebMCTSParamsReport{}).NumField(), reflect.TypeOf(mctsParams{}).NumField(); got != want {
		t.Errorf("the monte-carlo report carries %d fields for %d parameters: every lever the contender runs under has to reach the artifact", got, want)
	}
	// The caveats say what each contender was given and refuse to name the
	// guard that bound it: only a recorded stop reason can do that.
	joined := strings.Join(art.Caveats, "\n")
	for _, name := range []string{"pvs", "mcts"} {
		if !strings.Contains(joined, "candidate "+name+" ran under ") {
			t.Errorf("no guard caveat for %s in:\n%s", name, joined)
		}
	}
	if !strings.Contains(joined, "64 simulations") {
		t.Errorf("the monte-carlo simulation ceiling is not stated in the caveats:\n%s", joined)
	}
	for _, claim := range []string{"the clock is only a guard", "deterministic comparison", "work-bounded run"} {
		if strings.Contains(joined, claim) {
			t.Errorf("caveats claim %q from the configuration alone:\n%s", claim, joined)
		}
	}
}
