package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

type analysisEnvelope struct {
	Schema string `json:"schema"`
	Engine struct {
		Version  string `json:"version"`
		Commit   string `json:"commit"`
		Modified bool   `json:"modified"`
	} `json:"engine"`
	Rules          string `json:"rules"`
	Entry          int    `json:"entry"`
	PositionDigest string `json:"position_digest"`
	RootSide       string `json:"root_side"`
	Status         string `json:"status"`
	Result         struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"result"`
	Policy      string  `json:"policy"`
	Limitations string  `json:"limitations"`
	Recommended *string `json:"recommended"`
	Candidates  []struct {
		Move  string `json:"move"`
		Score *int   `json:"score"`
		Bound string `json:"bound"`
	} `json:"candidates"`
	Before *analysisTerms `json:"terms_before"`
	After  *analysisTerms `json:"terms_after"`
	Reason *struct {
		Code     string   `json:"code"`
		Headline string   `json:"headline"`
		Detail   string   `json:"detail"`
		Holes    []string `json:"holes"`
	} `json:"reason"`
	Stats *struct {
		Nodes      int64  `json:"nodes"`
		Analyses   int64  `json:"analyses"`
		Depth      int    `json:"completed_depth"`
		Elapsed    int64  `json:"elapsed_ns"`
		StopReason string `json:"stop_reason"`
	} `json:"stats"`
	Reproducible bool `json:"reproducible"`
}

type analysisTerms struct {
	Dist           int `json:"dist"`
	OppDist        int `json:"opp_dist"`
	Bottlenecks    int `json:"bottlenecks"`
	OppBottlenecks int `json:"opp_bottlenecks"`
	Ground         int `json:"ground"`
	Score          int `json:"score"`
}

func decodeAnalysis(t *testing.T, result cliResult) analysisEnvelope {
	t.Helper()
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("analysis exited %d: stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	var got analysisEnvelope
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("analysis JSON: %v\n%s", err, result.stdout)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing analysis output: %v, %v", extra, err)
	}
	if got.Schema != "twixtui-analysis/1" || got.Engine.Version == "" || got.Before == nil || got.Limitations == "" || !strings.Contains(got.Policy, "placement-only") {
		t.Fatalf("analysis lacks its identity, terms or policy: %+v", got)
	}
	if strings.ContainsAny(result.stdout, "\x1b\x00") {
		t.Fatal("machine output contains terminal controls")
	}
	return got
}

func analysisRecord(t *testing.T) (game.Record, *game.Game) {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	if err := g.OfferDraw(game.Vertical); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("B1"); err != nil {
		t.Fatal(err)
	}
	if err := g.OfferDraw(game.Horizontal); err != nil {
		t.Fatal(err)
	}
	prefix := g.Clone()
	if err := g.PlayNotation("swap"); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("C3"); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("F2"); err != nil {
		t.Fatal(err)
	}
	if prefix.Entries() != 3 || prefix.Ply() != 1 || prefix.Turn() != game.Horizontal || g.Entries() <= prefix.Entries() {
		t.Fatal("fixture no longer separates record entries from plies and final position")
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return record, prefix
}

func TestAnalyzeInputsDescribeTheSameRecordEntry(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	record, prefix := analysisRecord(t)
	encoded, err := record.EncodeCanonical()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input := filepath.Join(root, "record Árvíz with spaces.twixt")
	if err := os.WriteFile(input, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	storeCfg := filepath.Join(root, "saved games")
	store, err := gamestore.Open(storeCfg)
	if err != nil {
		t.Fatal(err)
	}
	saved := gamestore.Saved{ID: "analysis-record", Kind: gamestore.Imported, Player: "Original", Opponent: "Other", Record: encoded}
	if err := store.Put(saved); err != nil {
		t.Fatal(err)
	}
	storedPath := filepath.Join(store.Dir(), saved.ID+".json")
	storedBefore, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatal(err)
	}
	freshCfg := filepath.Join(root, "must not be created")
	moves, err := prefix.Transcript()
	if err != nil {
		t.Fatal(err)
	}
	calls := []struct {
		cfg, stdin string
		args       []string
	}{
		{storeCfg, "", []string{"analyze", "game", saved.ID, "--at", "3"}},
		{freshCfg, "", []string{"analyze", "record", input, "--at", "3"}},
		{freshCfg, encoded, []string{"analyze", "record", "-", "--at", "3"}},
		{freshCfg, "", []string{"analyze", "position", "--size", "6", "--moves", moves}},
	}
	var first analysisEnvelope
	for i, call := range calls {
		args := append(call.args, "--nodes", "64", "--format", "json")
		got := decodeAnalysis(t, cliRun(t, bin, call.cfg, call.stdin, args...))
		if got.Entry != 3 || got.PositionDigest != game.PositionDigest(prefix) || got.RootSide != prefix.Turn().String() || got.Rules != prefix.Rules().Canonical() {
			t.Fatalf("input route %d analyzed the wrong entry: %+v", i, got)
		}
		if got.Status != "analyzed" || got.Recommended == nil || got.After == nil || got.Stats == nil || !got.Reproducible || got.Stats.Nodes > 64 {
			t.Fatalf("input route %d lacks bounded analysis: %+v", i, got)
		}
		move, err := game.ParsePoint(*got.Recommended)
		if err != nil || prefix.CanPlace(prefix.Turn(), move) != nil {
			t.Fatalf("illegal recommendation %v: %v", got.Recommended, err)
		}
		for _, candidate := range got.Candidates {
			switch candidate.Bound {
			case "unscored":
				if candidate.Score != nil {
					t.Fatal("unscored candidate fabricated a numeric score")
				}
			case "exact", "upper", "lower":
				if candidate.Score == nil {
					t.Fatal("scored candidate omitted its score")
				}
			default:
				t.Fatalf("unknown score bound %q", candidate.Bound)
			}
		}
		got.Stats.Elapsed = 0
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(got, first) {
			t.Fatalf("input route %d changed deterministic analysis\nfirst=%+v\ngot=%+v", i, first, got)
		}
	}
	storedAfter, err := os.ReadFile(storedPath)
	if err != nil || !bytes.Equal(storedBefore, storedAfter) {
		t.Fatalf("analysis changed its saved game: %v", err)
	}
	if _, err := os.Stat(freshCfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("analysis created config state: %v", err)
	}
	for _, name := range []string{"profiles.json", "leaderboard.json"} {
		if _, err := os.Stat(filepath.Join(storeCfg, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("analysis touched %s: %v", name, err)
		}
	}
	text := cliRun(t, bin, freshCfg, "", "analyze", "position", "--size", "6", "--nodes", "64")
	if text.code != 0 || text.stderr != "" || !strings.Contains(text.stdout, "placement-only") || !strings.Contains(strings.ToLower(text.stdout), "nodes") {
		t.Fatalf("human analysis lacks its policy or work accounting: %+v", text)
	}
}

func TestAnalyzeTerminalAndUnsearchedResultsAreExplicit(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	cfg := filepath.Join(t.TempDir(), "no profile needed")
	unsearched := decodeAnalysis(t, cliRun(t, bin, cfg, "", "analyze", "position", "--size", "6", "--nodes", "1", "--format", "json"))
	if unsearched.Stats == nil || unsearched.Stats.StopReason != "nodes" || unsearched.Stats.Depth != 0 || unsearched.Stats.Nodes > 1 || unsearched.Recommended == nil || len(unsearched.Candidates) == 0 {
		t.Fatalf("tiny node budget did not report its legal unsearched fallback: %+v", unsearched)
	}
	for _, candidate := range unsearched.Candidates {
		if candidate.Bound != "unscored" || candidate.Score != nil {
			t.Fatalf("unsearched candidate was scored: %+v", candidate)
		}
	}
	encoded, _ := cliPathRecord(t)
	terminal := decodeAnalysis(t, cliRun(t, bin, cfg, encoded, "analyze", "record", "-", "--nodes", "1", "--format", "json"))
	if terminal.Status != "terminal" || terminal.Result.Outcome != "vertical-wins" || terminal.Result.Reason != "connection" || terminal.Recommended != nil || terminal.After != nil || terminal.Stats != nil || terminal.Reason != nil || terminal.Candidates == nil || len(terminal.Candidates) != 0 || !terminal.Reproducible {
		t.Fatalf("terminal analysis invented search or omitted its result: %+v", terminal)
	}
	if _, err := os.Stat(cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("analysis created config state: %v", err)
	}
}

func TestAnalyzeRefusalsDoNotWriteState(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	encoded, _ := cliPathRecord(t)
	root := t.TempDir()
	cfg := filepath.Join(root, "untouched config")
	cases := []struct {
		name, input string
		args        []string
	}{
		{"conflicting guards", encoded, []string{"analyze", "record", "-", "--nodes", "1", "--budget", "2s", "--format", "json"}},
		{"outside entries", encoded, []string{"analyze", "record", "-", "--at", "8", "--nodes", "1", "--format", "json"}},
		{"oversized record", encoded + strings.Repeat(" ", (1<<20)+1), []string{"analyze", "record", "-", "--nodes", "1", "--format", "json"}},
	}
	for _, tc := range cases {
		result := cliRun(t, bin, cfg, tc.input, tc.args...)
		if result.code == 0 || result.stdout != "" || result.stderr == "" {
			t.Fatalf("%s did not fail cleanly: %+v", tc.name, result)
		}
	}
	if _, err := os.Stat(cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused analysis changed state: %v", err)
	}
}

func TestAnalyzeDoesNotRedirectAnUnreadableExactGame(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	cfg := t.TempDir()
	encoded, _ := cliPathRecord(t)
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(gamestore.Saved{ID: "target-long", Kind: gamestore.Imported, Record: encoded}); err != nil {
		t.Fatal(err)
	}
	longPath := filepath.Join(store.Dir(), "target-long.json")
	before, err := os.ReadFile(longPath)
	if err != nil {
		t.Fatal(err)
	}
	exactPath := filepath.Join(store.Dir(), "target.json")
	if err := os.WriteFile(exactPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := cliRun(t, bin, cfg, "", "analyze", "game", "TARGET", "--nodes", "1", "--format", "json")
	if result.code == 0 || result.stdout != "" || result.stderr == "" {
		t.Fatalf("unreadable exact game redirected to its sibling: %+v", result)
	}
	after, err := os.ReadFile(longPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refusal changed the sibling game: %v", err)
	}
	broken, err := os.ReadFile(exactPath)
	if err != nil || string(broken) != "{" {
		t.Fatalf("refusal changed the unreadable exact game: %q, %v", broken, err)
	}
}
