package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// analysisNodes is the work bound every search below runs under. A bound on
// work rather than on the clock is what makes these tests mean the same thing
// on a loaded machine as on an idle one, and it keeps each search short.
const analysisNodes = "3000"

// analysisFields is the whole of the published envelope, in the order the
// format documents it.
var analysisFields = []string{
	"schema", "engine", "rules", "entry", "position_digest", "root_side",
	"status", "result", "policy", "limitations", "recommended", "candidates",
	"terms_before", "terms_after", "reason", "stats", "reproducible",
}

// analysisFixtureRules is a small board, so the searches here cost
// milliseconds; the rules are the default preset, which is what a record
// carries.
func analysisFixtureRules() game.Ruleset {
	rs := game.Std
	rs.Size = 12
	return rs
}

// offerEntries is a record whose entries are not its plies: the swap counts as
// a turn and the draw offer does not, so counting one gets a different position
// from counting the other.
var offerEntries = []string{"E7", "swap", "v:draw?", "D5", "H8"}

// analysisFixtureRecord is that game encoded as a record, which is what both
// the file and the standard-input routes are given.
func analysisFixtureRecord(t *testing.T) string {
	t.Helper()
	g, err := game.ReplayTranscript(analysisFixtureRules(), strings.Join(offerEntries, "; "))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	body, err := rec.EncodeCanonical()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// analysisRun is one invocation of the command line: the configuration
// directory it may read, what is waiting on standard input, and the context it
// runs under.
type analysisRun struct {
	dir   string
	stdin string
	ctx   context.Context
}

// do runs the command line with the two output streams kept apart. They have to
// be: standard output carries the envelope a script reads, and a diagnostic
// mixed into it would be read as part of the analysis.
func (r analysisRun) do(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(r.stdin))
	root.SetArgs(append([]string{"--config", r.dir}, args...))
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	err = root.ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

// envelopeOf reads the analysis out of what a command printed, and fails if
// standard output held anything besides it.
func envelopeOf(t *testing.T, stdout string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("standard output is not one JSON document: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("standard output holds more than the envelope:\n%s", stdout)
	}
	return doc
}

// dirSnapshot is the contents of every file under root, which is what a claim
// that a command wrote nothing has to be checked against.
func dirSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// reportUnchanged names what a run changed under root, for a test whose claim is
// that it changed nothing.
func reportUnchanged(t *testing.T, what, root string, before map[string]string) {
	t.Helper()
	after := dirSnapshot(t, root)
	if maps.Equal(before, after) {
		return
	}
	for path, was := range before {
		if now, held := after[path]; !held || now != was {
			t.Errorf("%s changed or removed %s", what, path)
		}
	}
	for path := range after {
		if _, held := before[path]; !held {
			t.Errorf("%s created %s", what, path)
		}
	}
}

// savedFixture imports the fixture record and returns the identifier it was
// stored under, so the saved-game route has a game to read.
func savedFixture(t *testing.T, dir, record string) string {
	t.Helper()
	out, err := run(t, dir, "game", "import", recordFile(t, "fixture.rec", record))
	if err != nil {
		t.Fatalf("the fixture record was refused: %v\n%s", err, out)
	}
	return importedID(t, out)
}

// TestEveryAnalysisInputReachesTheSamePosition covers the ways in. A saved
// game, a record in a file, a record on standard input and a move list are four
// spellings of one position, so at the same entry they have to be analysed as
// the same position; a second parser, or an --at that means something else on
// one route, shows up here as a different digest.
func TestEveryAnalysisInputReachesTheSamePosition(t *testing.T) {
	record := analysisFixtureRecord(t)
	path := recordFile(t, "game.rec", record)
	dir := t.TempDir()
	id := savedFixture(t, dir, record)
	const at = "4"

	routes := map[string][]string{
		"a saved game":    {"analyze", "game", id, "--at", at},
		"a record file":   {"analyze", "record", path, "--at", at},
		"standard input":  {"analyze", "record", "-", "--at", at},
		"a list of moves": {"analyze", "position", "--ruleset", "std", "--size", "12", "--moves", strings.Join(offerEntries[:4], "; ")},
	}
	var first map[string]any
	var firstRoute string
	for route, args := range routes {
		r := analysisRun{dir: dir}
		if route == "standard input" {
			r.stdin = record
		}
		stdout, stderr, err := r.do(t, append(args, "--format", "json", "--nodes", analysisNodes)...)
		if err != nil {
			t.Fatalf("%s was refused: %v\n%s", route, err, stderr)
		}
		doc := envelopeOf(t, stdout)
		if doc["entry"] != float64(4) {
			t.Errorf("%s reports entry %v, want the fourth", route, doc["entry"])
		}
		if first == nil {
			first, firstRoute = doc, route
			continue
		}
		// The recommendation is compared along with the position itself: two
		// routes could agree on the digest and still have handed the engine
		// different games.
		for _, field := range []string{"rules", "entry", "position_digest", "root_side", "status", "recommended"} {
			if doc[field] != first[field] {
				t.Errorf("%s reports %s as %v where %s reports %v",
					route, field, doc[field], firstRoute, first[field])
			}
		}
	}
}

// TestAtCountsRecordEntriesRatherThanPlies covers the counting. A record holds
// entries that are not turns — a draw offer here, alongside a swap that is one —
// so an --at read as a move number lands on a different position, which is the
// mistake this fixture is built to catch.
func TestAtCountsRecordEntriesRatherThanPlies(t *testing.T) {
	rs := analysisFixtureRules()
	record := analysisFixtureRecord(t)
	path := recordFile(t, "offers.rec", record)
	dir := t.TempDir()

	byEntry, err := game.ReplayTranscript(rs, strings.Join(offerEntries[:3], "; "))
	if err != nil {
		t.Fatal(err)
	}
	byPly, err := game.New(rs)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range offerEntries {
		if byPly.Ply() >= 3 {
			break
		}
		if err := byPly.PlayNotation(entry); err != nil {
			t.Fatal(err)
		}
	}
	if byPly.Ply() != 3 {
		t.Fatalf("the fixture reaches %d plies, so it cannot tell entries from plies", byPly.Ply())
	}
	entryDigest, plyDigest := game.PositionDigest(byEntry), game.PositionDigest(byPly)
	if entryDigest == plyDigest {
		t.Fatal("three entries and three plies are the same position here, so this fixture proves nothing")
	}
	opening, err := game.New(rs)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := game.ReplayTranscript(rs, strings.Join(offerEntries, "; "))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		at   string
		want *game.Game
	}{
		{"0", opening},
		{"3", byEntry},
		{"5", whole},
	} {
		stdout, stderr, err := analysisRun{dir: dir}.do(t,
			"analyze", "record", path, "--at", tc.at, "--format", "json", "--nodes", analysisNodes)
		if err != nil {
			t.Fatalf("--at %s was refused: %v\n%s", tc.at, err, stderr)
		}
		doc := envelopeOf(t, stdout)
		if want := game.PositionDigest(tc.want); doc["position_digest"] != want {
			t.Errorf("--at %s analysed the position with digest %v, want %s", tc.at, doc["position_digest"], want)
		}
		if want := tc.want.Turn().String(); doc["root_side"] != want {
			t.Errorf("--at %s has %v to move, want %s", tc.at, doc["root_side"], want)
		}
		if want := float64(tc.want.Entries()); doc["entry"] != want {
			t.Errorf("--at %s reports entry %v, want %v", tc.at, doc["entry"], want)
		}
	}
}

// TestAnalysedRecordInputIsBoundedBySize covers the size of the input rather
// than what it says. A record arrives from a file or a pipe somebody else wrote,
// so how large it is, is not this program's choice.
func TestAnalysedRecordInputIsBoundedBySize(t *testing.T) {
	sound := analysisFixtureRecord(t)
	// The padding is comment lines on an otherwise sound record: a parser
	// refuses junk whatever the limit is, so only a record that would
	// otherwise be accepted can tell the size check apart from the syntax one.
	padding := strings.Repeat("# padding\n", 1+(fixtureRecordLimit-len(sound))/len("# padding\n"))
	oversized := sound + padding
	if len(oversized) <= fixtureRecordLimit {
		t.Fatalf("the padded record is %d bytes, which does not exceed the %d-byte fixture limit", len(oversized), fixtureRecordLimit)
	}
	dir := t.TempDir()

	// The control comes first: the same record inside the limit is analysed, so
	// a command that refused every input could not pass this test.
	stdout, stderr, err := analysisRun{dir: dir}.do(t,
		"analyze", "record", recordFile(t, "sound.rec", sound+"# padding\n"), "--format", "json", "--nodes", analysisNodes)
	if err != nil {
		t.Fatalf("a commented record inside the limit was refused: %v\n%s", err, stderr)
	}
	envelopeOf(t, stdout)

	for _, route := range []string{"a file", "standard input"} {
		r := analysisRun{dir: dir}
		arg := recordFile(t, "huge.rec", oversized)
		if route == "standard input" {
			r.stdin, arg = oversized, "-"
		}
		stdout, _, err := r.do(t, "analyze", "record", arg, "--format", "json", "--nodes", analysisNodes)
		if err == nil {
			t.Errorf("an oversized record from %s was analysed:\n%s", route, stdout)
			continue
		}
		if !errors.Is(err, game.ErrRecordTooLarge) {
			t.Errorf("the refusal of %s reads %q, which is not a refusal on size", route, err)
		}
		if stdout != "" {
			t.Errorf("the refused input from %s still printed to standard output:\n%s", route, stdout)
		}
	}
}

func TestAnalysisTranscriptLimitRefusesOtherwiseValidInput(t *testing.T) {
	r := analysisRun{dir: t.TempDir()}
	stdout, _, err := r.do(t, "analyze", "position", "--moves", "; ; B1; ", "--nodes", "1", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	envelopeOf(t, stdout)
	oversized := strings.Repeat("; ", (1<<19)+1)
	stdout, _, err = r.do(t, "analyze", "position", "--moves", oversized, "--nodes", "1", "--format", "json")
	if !errors.Is(err, game.ErrRecordTooLarge) || stdout != "" {
		t.Fatalf("oversized valid transcript: error=%v, stdout=%q", err, stdout)
	}
}

func TestClockBoundAnalysisReportsItsInterruptedWork(t *testing.T) {
	stdout, _, err := (analysisRun{dir: t.TempDir()}).do(t,
		"analyze", "position", "--budget", "1ns", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	doc := envelopeOf(t, stdout)
	stats, ok := doc["stats"].(map[string]any)
	if !ok || stats["stop_reason"] != "time" || doc["reproducible"] != false {
		t.Fatalf("clock-limited result was misreported: %+v", doc)
	}
}

// TestRefusedAnalysisPrintsNothingAndChangesNothing covers what a mistyped
// command line costs. A refusal has to come before the work, print nothing a
// script could parse as an analysis, and leave the configuration directory as
// it was — including on a finished game, where there is nothing to search and a
// bound could quietly go unchecked.
func TestRefusedAnalysisPrintsNothingAndChangesNothing(t *testing.T) {
	dir := t.TempDir()
	id := savedFixture(t, dir, analysisFixtureRecord(t))
	finished := recordFile(t, "finished.rec", canonicalRecord(t))
	before := dirSnapshot(t, dir)

	for _, tc := range []struct {
		what string
		args []string
	}{
		{"a format nothing renders", []string{"analyze", "game", id, "--format", "xml"}},
		{"an empty format", []string{"analyze", "game", id, "--format", ""}},
		{"a node bound of zero", []string{"analyze", "game", id, "--nodes", "0"}},
		{"a negative node bound", []string{"analyze", "game", id, "--nodes", "-1"}},
		{"a node bound too large to be a number", []string{"analyze", "game", id, "--nodes", "99999999999999999999"}},
		{"a node bound and a budget at once", []string{"analyze", "game", id, "--nodes", "1000", "--budget", "5s"}},
		{"a budget of no time at all", []string{"analyze", "game", id, "--budget", "0s"}},
		{"a negative budget", []string{"analyze", "game", id, "--budget", "-1s"}},
		{"a node bound of zero on a finished game", []string{"analyze", "record", finished, "--nodes", "0"}},
		{"a negative entry", []string{"analyze", "game", id, "--at", "-1"}},
		{"an entry past the end", []string{"analyze", "game", id, "--at", "99"}},
		{"a saved game that is not there", []string{"analyze", "game", "nosuchgame"}},
		{"a record file that is not there", []string{"analyze", "record", filepath.Join(dir, "absent.rec")}},
		{"an empty move list", []string{"analyze", "position", "--moves", ""}},
		{"a ruleset nobody plays", []string{"analyze", "position", "--ruleset", "hopscotch"}},
		{"a board too small to play on", []string{"analyze", "position", "--size", "3"}},
		{"an entry on a position given as moves", []string{"analyze", "position", "--at", "1"}},
		{"an argument to a command that takes none", []string{"analyze", "position", "E7"}},
		{"a subcommand that does not exist", []string{"analyze", "sideways"}},
	} {
		stdout, _, err := analysisRun{dir: dir}.do(t, tc.args...)
		if err == nil {
			t.Errorf("%s was accepted:\n%s", tc.what, stdout)
			continue
		}
		if stdout != "" {
			t.Errorf("%s printed to standard output:\n%s", tc.what, stdout)
		}
	}
	reportUnchanged(t, "a refused analysis", dir, before)
}

// TestTerminalPositionIsReportedWithoutAMove covers the finished game. There is
// no turn to advise, so the engine's own result and the terms of the position
// are the answer; inventing a recommendation, a search or a reason for one
// would be reporting a turn that cannot be played.
func TestTerminalPositionIsReportedWithoutAMove(t *testing.T) {
	path := recordFile(t, "finished.rec", canonicalRecord(t))
	dir := t.TempDir()

	stdout, stderr, err := analysisRun{dir: dir}.do(t, "analyze", "record", path, "--format", "json", "--nodes", analysisNodes)
	if err != nil {
		t.Fatalf("a finished game was refused: %v\n%s", err, stderr)
	}
	doc := envelopeOf(t, stdout)

	if doc["status"] != statusTerminal {
		t.Errorf("a finished game is reported as %v, want %q", doc["status"], statusTerminal)
	}
	for _, field := range []string{"recommended", "terms_after", "stats", "reason"} {
		value, held := doc[field]
		if !held {
			t.Errorf("the envelope of a finished game has no %s field at all", field)
			continue
		}
		if value != nil {
			t.Errorf("a finished game reports %s as %v, want null: nothing was searched", field, value)
		}
	}
	if candidates, ok := doc["candidates"].([]any); !ok || len(candidates) != 0 {
		t.Errorf("a finished game reports candidates %v, want an empty list rather than null", doc["candidates"])
	}
	if doc["reproducible"] != true {
		t.Error("a result read off the position reports itself as not reproducible")
	}
	result, ok := doc["result"].(map[string]any)
	if !ok {
		t.Fatalf("the result is %v, want an object", doc["result"])
	}
	if result["outcome"] != "vertical-wins" || result["reason"] != "resignation" {
		t.Errorf("the fixture ends %v by %v, want vertical-wins by resignation", result["outcome"], result["reason"])
	}
	before, ok := doc["terms_before"].(map[string]any)
	if !ok {
		t.Fatalf("the terms are %v, want an object", doc["terms_before"])
	}
	for _, field := range []string{"dist", "opp_dist", "bottlenecks", "opp_bottlenecks", "ground", "score"} {
		if _, ok := before[field].(float64); !ok {
			t.Errorf("terms_before.%s is %v, want a number", field, before[field])
		}
	}

	// The prose form answers the same way: it reports how the game ended and
	// recommends nothing, since there is no turn to play.
	text, _, err := analysisRun{dir: dir}.do(t, "analyze", "record", path)
	if err != nil {
		t.Fatalf("the prose form was refused: %v", err)
	}
	for _, want := range []string{fmt.Sprint(result["outcome"]), fmt.Sprint(result["reason"])} {
		if !strings.Contains(text, want) {
			t.Errorf("the prose never says the game ended %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "recommended:") {
		t.Errorf("the prose recommends a move in a finished game:\n%s", text)
	}
}

// TestPreCancelledAnalysisRefusesBeforeReadingAnything covers the context a
// command inherits. A run that has already been cancelled has nothing to
// report, so it must not read a record or start a search and must not print an
// analysis of one.
func TestPreCancelledAnalysisRefusesBeforeReadingAnything(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := strings.NewReader(analysisFixtureRecord(t))
	unread := input.Len()
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetIn(input)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--config", t.TempDir(), "analyze", "record", "-", "--format", "json", "--nodes", analysisNodes})
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled run ended with %v", err)
	}
	if stdout.Len() != 0 || input.Len() != unread {
		t.Fatalf("canceled run consumed input or printed output: remaining=%d/%d, stdout=%q", input.Len(), unread, stdout.String())
	}
}

type analysisCancelOnEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (r analysisCancelOnEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.cancel()
	}
	return n, err
}

// Cancellation arriving with the completed input must still produce an honest
// partial envelope and a failing exit, without racing command startup.
func TestCancellationAfterInputPrintsItsWorkAndStillFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	record, err := game.MustNew(game.Std).Record()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := record.EncodeCanonical()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(analysisCancelOnEOF{Reader: strings.NewReader(encoded), cancel: cancel})
	root.SetArgs([]string{"--config", t.TempDir(), "analyze", "record", "-", "--nodes", analysisNodes, "--format", "json"})
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted analysis ended with %v, want cancellation", err)
	}
	doc := envelopeOf(t, stdout.String())
	stats, ok := doc["stats"].(map[string]any)
	if !ok || stats["stop_reason"] != "canceled" || doc["reproducible"] != false {
		t.Fatalf("canceled input was reported as completed work: %+v", doc)
	}
}

// TestAnalysisJSONCarriesOnlyWhatItPromises covers the envelope a script reads:
// the fields it documents and nothing else, coordinates where it says
// coordinates, a bound beside every score, no number where nothing measured
// one, and the same answer twice under the same work bound apart from how long
// it took.
func TestAnalysisJSONCarriesOnlyWhatItPromises(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"analyze", "position", "--ruleset", "std", "--size", "12",
		"--moves", "E7; F9", "--format", "json", "--nodes", analysisNodes,
	}

	stdout, stderr, err := analysisRun{dir: dir}.do(t, args...)
	if err != nil {
		t.Fatalf("the analysis was refused: %v\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("a successful analysis wrote to standard error:\n%s", stderr)
	}
	doc := envelopeOf(t, stdout)

	if len(doc) != len(analysisFields) {
		t.Errorf("the envelope holds %v, want exactly %v", slices.Sorted(maps.Keys(doc)), analysisFields)
	}
	for _, field := range analysisFields {
		if _, held := doc[field]; !held {
			t.Errorf("the envelope has no %s", field)
		}
	}
	if doc["schema"] != analysisSchema {
		t.Errorf("the envelope names the schema %v, want %q", doc["schema"], analysisSchema)
	}
	if doc["status"] != statusAnalyzed {
		t.Errorf("an unfinished game is reported as %v, want %q", doc["status"], statusAnalyzed)
	}
	if policy, _ := doc["policy"].(string); policy == "" {
		t.Error("the envelope states no analysis policy")
	}
	if limits, _ := doc["limitations"].(string); limits == "" {
		t.Error("the envelope states no limitations")
	}
	if engine, ok := doc["engine"].(map[string]any); !ok {
		t.Errorf("the engine stamp is %v, want an object", doc["engine"])
	} else if stamped, _ := engine["version"].(string); stamped == "" {
		t.Errorf("the engine stamp names no version: %v", engine)
	}

	move, _ := doc["recommended"].(string)
	if _, err := game.ParsePoint(move); err != nil {
		t.Fatalf("the recommendation %v is not a hole: %v", doc["recommended"], err)
	}
	candidates, ok := doc["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		t.Fatalf("the analysis kept no candidates: %v", doc["candidates"])
	}
	bounds := map[string]bool{"exact": true, "upper": true, "lower": true, "unscored": true}
	for i, raw := range candidates {
		c, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("candidate %d is %v, want an object", i, raw)
		}
		if _, err := game.ParsePoint(fmt.Sprint(c["move"])); err != nil {
			t.Errorf("candidate %d's move %v is not a hole", i, c["move"])
		}
		bound, _ := c["bound"].(string)
		if !bounds[bound] {
			t.Errorf("candidate %d carries the bound %v, which is not one this format defines", i, c["bound"])
		}
		if bound == "unscored" {
			if c["score"] != nil {
				t.Errorf("candidate %d is unscored and still carries the score %v, which nothing measured", i, c["score"])
			}
			continue
		}
		if _, ok := c["score"].(float64); !ok {
			t.Errorf("candidate %d carries the score %v, want a number", i, c["score"])
		}
	}

	reason, ok := doc["reason"].(map[string]any)
	if !ok {
		t.Fatalf("the reason is %v, want an object", doc["reason"])
	}
	for _, field := range []string{"code", "headline", "detail"} {
		if value, _ := reason[field].(string); value == "" {
			t.Errorf("the reason's %s is empty", field)
		}
	}
	if _, ok := reason["holes"].([]any); !ok {
		t.Errorf("the reason's holes are %v, want a list", reason["holes"])
	}
	stats, ok := doc["stats"].(map[string]any)
	if !ok {
		t.Fatalf("the search stats are %v, want an object", doc["stats"])
	}
	for _, field := range []string{"nodes", "analyses", "completed_depth", "elapsed_ns"} {
		if _, ok := stats[field].(float64); !ok {
			t.Errorf("stats.%s is %v, want a number", field, stats[field])
		}
	}
	if stop, _ := stats["stop_reason"].(string); stop == "" {
		t.Error("the stats do not say what stopped the search")
	}
	if doc["reproducible"] != true {
		t.Errorf("a search bounded by work reports itself as not reproducible, having stopped on %v", stats["stop_reason"])
	}
	if _, ok := doc["terms_after"].(map[string]any); !ok {
		t.Errorf("the terms after the recommended move are %v, want an object", doc["terms_after"])
	}

	// The same bound twice is the same analysis, apart from how long it took:
	// the clock is the one thing another machine cannot reproduce, which is why
	// it is the one field excluded here.
	again, _, err := analysisRun{dir: dir}.do(t, args...)
	if err != nil {
		t.Fatalf("the second analysis was refused: %v", err)
	}
	repeat := envelopeOf(t, again)
	delete(stats, "elapsed_ns")
	if repeated, ok := repeat["stats"].(map[string]any); ok {
		delete(repeated, "elapsed_ns")
	}
	if !reflect.DeepEqual(doc, repeat) {
		t.Errorf("two searches under the same work bound disagree:\n%v\nand\n%v", doc, repeat)
	}
}

// TestAnalysingASavedGameLeavesItAlone covers the promise that analysis reads.
// A saved game is the record of a game that was played, and an analysis of it is
// not an event in that game.
func TestAnalysingASavedGameLeavesItAlone(t *testing.T) {
	dir := t.TempDir()
	id := savedFixture(t, dir, analysisFixtureRecord(t))
	before := dirSnapshot(t, dir)

	for _, args := range [][]string{
		{"analyze", "game", id, "--nodes", analysisNodes},
		{"analyze", "game", id, "--at", "2", "--format", "json", "--nodes", analysisNodes},
	} {
		stdout, stderr, err := analysisRun{dir: dir}.do(t, args...)
		if err != nil {
			t.Fatalf("%v was refused: %v\n%s%s", args, err, stdout, stderr)
		}
		if stdout == "" {
			t.Errorf("%v printed nothing", args)
		}
	}
	reportUnchanged(t, "analysing a saved game", dir, before)
}

// TestAnalyzeCompletionsOfferTheCurrentValues covers what TAB answers with. Each
// list is the one the rest of the command line already uses, so a saved game, a
// ruleset or a format that exists is offered and nothing else is.
func TestAnalyzeCompletionsOfferTheCurrentValues(t *testing.T) {
	dir := t.TempDir()
	id := savedFixture(t, dir, analysisFixtureRecord(t))

	games := completionPairs(t, mustRun(t, dir, "__complete", "analyze", "game", ""))
	desc, offered := games[id]
	if !offered {
		t.Fatalf("the saved game %s is not offered: %v", id, games)
	}
	if !strings.Contains(desc, "vertical") {
		t.Errorf("the saved game is offered as %q, which does not say which game it is", desc)
	}

	formats := completionPairs(t, mustRun(t, dir, "__complete", "analyze", "game", "--format", ""))
	if len(formats) != 2 {
		t.Errorf("--format offers %v, want exactly the two formats", formats)
	}
	for _, want := range []string{formatText, formatJSON} {
		if _, held := formats[want]; !held {
			t.Errorf("--format does not offer %q: %v", want, formats)
		}
	}

	rulesets := completionPairs(t, mustRun(t, dir, "__complete", "analyze", "position", "--ruleset", ""))
	for _, name := range game.PresetNames() {
		if desc := rulesets[name]; desc == "" {
			t.Errorf("--ruleset does not offer the %q preset with its summary: %v", name, rulesets)
		}
	}
}
