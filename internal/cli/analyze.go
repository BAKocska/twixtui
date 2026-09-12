package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/ui"
)

// analysisSchema names the shape of the machine-readable envelope. A reader
// checks it before reading anything else, so it changes whenever a field of
// that envelope changes meaning.
const analysisSchema = "twixtui-analysis/1"

const (
	formatText = "text"
	formatJSON = "json"
)

// The two states an analysis can be reported in. A finished game is answered
// with its result rather than with a move, because there is no turn to advise.
const (
	statusAnalyzed = "analyzed"
	statusTerminal = "terminal"
)

// nodeSafetyGuard remains active even when recursive node work is bounded.
const nodeSafetyGuard = time.Hour

// analyzeFlags are the settings every analyze subcommand shares: how the answer
// is printed, and what the search is allowed to spend on it.
type analyzeFlags struct {
	format string
	nodes  int64
	budget time.Duration
	at     int
}

func (f *analyzeFlags) addSearchFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.format, "format", formatText,
		"how to print the analysis: text or json")
	cmd.Flags().Int64Var(&f.nodes, "nodes", 0,
		"maximum recursive search nodes (a safety clock or interruption can stop earlier)")
	cmd.Flags().DurationVar(&f.budget, "budget", 2*time.Second,
		"how long the search may spend")
	registerFlagCompletion(cmd, "format", analysisFormatCompletions)
}

// addEntryFlag offers --at where there is a record to count entries in. The
// default is the final position, which is why the flag is read through
// Changed rather than compared against a value: entry 0 is the position before
// the first entry and is a perfectly ordinary request.
func (f *analyzeFlags) addEntryFlag(cmd *cobra.Command) {
	cmd.Flags().IntVar(&f.at, "at", 0,
		"analyse the position after this many record entries, draw offers included (default: the final position)")
}

func analysisFormatCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{
		cobra.CompletionWithDesc(formatText, "prose for a reader"),
		cobra.CompletionWithDesc(formatJSON, "the "+analysisSchema+" envelope, for a script"),
	}, cobra.ShellCompDirectiveNoFileComp
}

// plan settles what the flags ask for before anything is loaded or searched. A
// value that cannot mean what it was given for is refused here, so a mistyped
// bound costs a refusal rather than a saved game being read and a search being
// run under a guard nobody asked for.
func (f *analyzeFlags) plan(cmd *cobra.Command) (bot.Limits, error) {
	if f.format != formatText && f.format != formatJSON {
		return bot.Limits{}, fmt.Errorf("--format %q is not a format: choose %s or %s", f.format, formatText, formatJSON)
	}
	if !cmd.Flags().Changed("nodes") {
		if f.budget <= 0 {
			return bot.Limits{}, fmt.Errorf("--budget cannot be %s; give the time the search may spend", f.budget)
		}
		return bot.Limits{Time: f.budget}, nil
	}
	if f.nodes <= 0 {
		return bot.Limits{}, fmt.Errorf("--nodes cannot be %d; give the number of positions the search may visit", f.nodes)
	}
	// Keep the two user-facing budget modes distinct.
	if cmd.Flags().Changed("budget") {
		return bot.Limits{}, errors.New("--nodes and --budget cannot both be given")
	}
	return bot.Limits{Nodes: f.nodes, Time: nodeSafetyGuard}, nil
}

// entryAsked reports the entry the command line asked for and whether it asked
// at all. What the number says on its own is checked here; whether the game has
// that many entries needs the record and is checked once it is loaded.
func (f *analyzeFlags) entryAsked(cmd *cobra.Command) (int, bool, error) {
	if !cmd.Flags().Changed("at") {
		return 0, false, nil
	}
	if f.at < 0 {
		return 0, false, fmt.Errorf("--at cannot be %d; entries are counted from 0, which is the position before the first entry", f.at)
	}
	return f.at, true, nil
}

func newAnalyzeCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Analyse a position with the search engine",
		Long: fmt.Sprintf(`Analyse a position with the search engine.

The engine reads the saved game, the record or the position you name and reports
what it measured: the placement it recommends, the alternatives it scored, the
evaluation terms before and after that move, and what the search spent. Nothing
is written: an analysis reads a saved game and leaves it exactly as it was.

Every claim comes from one restricted reading — %s — so
it is a steer under those restrictions rather than a statement about legal play
or a proof about the game. Scores are integer search units from the point of
view of the root side, not win chances. Scored alternatives are exact or upper
bounds within the selective search; unscored candidates have no measured value.

A search is bounded by the clock by default and by work with --nodes; a search
the clock or an interruption ended reports itself as not reproducible, because
what it found is the work that machine had finished.`, bot.PlacementOnlyPolicy()),
	}
	cmd.AddCommand(
		newAnalyzeGameCommand(opts),
		newAnalyzeRecordCommand(),
		newAnalyzePositionCommand(),
	)
	return cmd
}

func newAnalyzeGameCommand(opts *options) *cobra.Command {
	var f analyzeFlags
	cmd := &cobra.Command{
		Use:   "game <id>",
		Short: "Analyse a position from a saved game",
		Long: `Analyse a position from a saved game.

The stored record is replayed and checked as "game show" checks it, and the
saved game is never written back. --at chooses which entry to stop at: entries
include the ones that are not turns, such as a draw offer, so entry 17 is the
seventeenth thing in the record rather than the seventeenth peg. Entry 0 is the
position before the first entry, and the default is the final position.`,
		Args:              exactArgs(1),
		ValidArgsFunction: opts.gameIDCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			limits, err := f.plan(cmd)
			if err != nil {
				return err
			}
			at, given, err := f.entryAsked(cmd)
			if err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			sv, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			final, rec, err := sv.Load()
			if err != nil {
				return err
			}
			g, entry, err := entryPosition(final, rec.Moves, at, given)
			if err != nil {
				return err
			}
			return f.report(cmd, g, entry, limits)
		},
	}
	f.addSearchFlags(cmd)
	f.addEntryFlag(cmd)
	return cmd
}

func newAnalyzeRecordCommand() *cobra.Command {
	var f analyzeFlags
	cmd := &cobra.Command{
		Use:   "record <file|->",
		Short: "Analyse a position from a game record",
		Long: `Analyse a position from a game record. Use - to read standard input.

The record is read under the size a record may have and refused if it has been
altered or truncated, as "game import" refuses one, and nothing is stored: this
reads a file and prints an analysis. --at counts record entries, draw offers
included, and defaults to the final position.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limits, err := f.plan(cmd)
			if err != nil {
				return err
			}
			at, given, err := f.entryAsked(cmd)
			if err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			// The input is bounded as it is read: a file or a pipe somebody
			// else wrote can carry as much as its writer liked, and a record
			// has a size a record can be.
			final, rec, err := readRecordFrom(cmd, args[0])
			if err != nil {
				return err
			}
			g, entry, err := entryPosition(final, rec.Moves, at, given)
			if err != nil {
				return err
			}
			return f.report(cmd, g, entry, limits)
		},
	}
	f.addSearchFlags(cmd)
	f.addEntryFlag(cmd)
	return cmd
}

func newAnalyzePositionCommand() *cobra.Command {
	var f analyzeFlags
	var rules gameFlags
	var moves string
	cmd := &cobra.Command{
		Use:   "position",
		Short: "Analyse a position given as a move list",
		Long: `Analyse a position given as a move list.

The moves are read in twixtui's own notation, semicolon separated, the way a
record's transcript is read — "twixtui rules show notation" explains it. Leaving
--moves out analyses the opening position. There is no --at here: the position
is whatever the moves given reach.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limits, err := f.plan(cmd)
			if err != nil {
				return err
			}
			rs, err := rules.rules()
			if err != nil {
				return err
			}
			// A transcript is bounded by the same rule a record is, because it
			// is a record's moves field arriving by another route.
			if len(moves) > game.MaxRecordBytes {
				return fmt.Errorf("--moves holds %d bytes: %w", len(moves), game.ErrRecordTooLarge)
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			g, err := game.ReplayTranscript(rs, moves)
			if err != nil {
				return err
			}
			return f.report(cmd, g, g.Entries(), limits)
		},
	}
	f.addSearchFlags(cmd)
	rules.addRuleFlags(cmd)
	cmd.Flags().StringVar(&moves, "moves", "",
		"the moves leading to the position, in twixtui's notation (default: the opening position)")
	return cmd
}

// entryPosition is the position the command line asked for. final is the game
// the record replays to, which is the position at its last entry; an earlier
// entry is replayed from the transcript, so what is analysed is a position the
// record itself passes through.
func entryPosition(final *game.Game, moves string, at int, given bool) (*game.Game, int, error) {
	entries := final.Entries()
	if !given || at == entries {
		return final, entries, nil
	}
	if at > entries {
		return nil, 0, fmt.Errorf("--at %d is past the end of this game: it has %d entries, so %d is its final position", at, entries, entries)
	}
	labels := transcriptEntries(moves)
	if len(labels) != entries {
		return nil, 0, fmt.Errorf("the transcript has %d entries but the record makes %d", len(labels), entries)
	}
	g, err := game.ReplayTranscript(final.Rules(), strings.Join(labels[:at], "; "))
	if err != nil {
		return nil, 0, err
	}
	return g, at, nil
}

// transcriptEntries splits a transcript into entries the way
// game.ReplayTranscript reads one, so a prefix of it replays as the same
// entries the record was validated by.
func transcriptEntries(moves string) []string {
	var out []string
	for part := range strings.SplitSeq(moves, ";") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// report searches one position and prints it in the format the flags chose.
//
// Trap interrupts around search, not around an arbitrary blocked input reader.
func (f *analyzeFlags) report(cmd *cobra.Command, g *game.Game, entry int, limits bot.Limits) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := bot.Analyze(ctx, g, bot.AnalysisOptions{Limits: limits})
	if err != nil {
		return err
	}
	if err := f.write(cmd.OutOrStdout(), analysisDocument(g, entry, res)); err != nil {
		return err
	}
	// An interrupted search has just printed the work it had finished. The
	// command still ends as the cancellation it was, so a script cannot read an
	// interrupted answer as a bounded one; a clock or a node ceiling is an
	// ordinary result and ends successfully.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("the analysis was interrupted: %w", err)
	}
	return nil
}

func (f *analyzeFlags) write(w io.Writer, doc analysisDoc) error {
	if f.format == formatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	return writeAnalysisText(w, doc)
}

// analysisDoc is the published envelope. Both formats are rendered from it, so
// what a reader is told and what a script is told cannot drift apart.
type analysisDoc struct {
	Schema         string         `json:"schema"`
	Engine         engineDoc      `json:"engine"`
	Rules          string         `json:"rules"`
	Entry          int            `json:"entry"`
	PositionDigest string         `json:"position_digest"`
	RootSide       string         `json:"root_side"`
	Status         string         `json:"status"`
	Result         resultDoc      `json:"result"`
	Policy         string         `json:"policy"`
	Limitations    string         `json:"limitations"`
	Recommended    *string        `json:"recommended"`
	Candidates     []candidateDoc `json:"candidates"`
	TermsBefore    termsDoc       `json:"terms_before"`
	TermsAfter     *termsDoc      `json:"terms_after"`
	Reason         *reasonDoc     `json:"reason"`
	Stats          *statsDoc      `json:"stats"`
	Reproducible   bool           `json:"reproducible"`
}

// engineDoc says which build produced the analysis, which is what lets a stored
// envelope be compared with another one honestly.
type engineDoc struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Modified bool   `json:"modified"`
}

type resultDoc struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// candidateDoc is one root move the search kept. Score is null where the bound
// says nothing measured it, because a zero there is not a measurement.
type candidateDoc struct {
	Move  string `json:"move"`
	Score *int   `json:"score"`
	Bound string `json:"bound"`
}

// termsDoc is the evaluation decomposition. dist and opp_dist carry bot.NoChain
// (-1) where the placement-only evaluation found no route left, which is a
// reading under that policy and not a claim that no legal win exists.
type termsDoc struct {
	Dist           int `json:"dist"`
	OppDist        int `json:"opp_dist"`
	Bottlenecks    int `json:"bottlenecks"`
	OppBottlenecks int `json:"opp_bottlenecks"`
	Ground         int `json:"ground"`
	Score          int `json:"score"`
}

type reasonDoc struct {
	Code     string   `json:"code"`
	Headline string   `json:"headline"`
	Detail   string   `json:"detail"`
	Holes    []string `json:"holes"`
}

// statsDoc is what the search spent. nodes counts the positions visited below
// the root over every iteration rather than total work, analyses counts the
// positions loaded and scored including the root and the tactical probes, and
// completed_depth is the last iteration that finished.
type statsDoc struct {
	Nodes          int64  `json:"nodes"`
	Analyses       int64  `json:"analyses"`
	CompletedDepth int    `json:"completed_depth"`
	ElapsedNS      int64  `json:"elapsed_ns"`
	StopReason     string `json:"stop_reason"`
}

// analysisDocument projects a result onto the envelope. Nothing here reaches
// back into the engine: every value is already owned by the result.
func analysisDocument(g *game.Game, entry int, res bot.AnalysisResult) analysisDoc {
	doc := analysisDoc{
		Schema:         analysisSchema,
		Engine:         engineStamp(),
		Rules:          g.Rules().Canonical(),
		Entry:          entry,
		PositionDigest: game.PositionDigest(g),
		RootSide:       g.Turn().String(),
		Status:         statusAnalyzed,
		Result: resultDoc{
			Outcome: outcomeName(res.Result.Outcome),
			Reason:  endReasonName(res.Result.Reason),
		},
		Policy:       res.Policy.String(),
		Limitations:  res.Policy.Summary(),
		Candidates:   make([]candidateDoc, 0, len(res.Candidates)),
		TermsBefore:  terms(res.Before),
		Reproducible: reproducible(res.Stats),
	}
	if res.Result.Over() {
		doc.Status = statusTerminal
	}
	if res.Recommended != nil {
		move := res.Recommended.String()
		doc.Recommended = &move
	}
	for i, c := range res.Candidates {
		row := candidateDoc{Move: c.Move.String(), Bound: string(c.Bound)}
		if c.Bound != bot.BoundUnscored {
			row.Score = &res.Candidates[i].Score
		}
		doc.Candidates = append(doc.Candidates, row)
	}
	if res.After != nil {
		after := terms(*res.After)
		doc.TermsAfter = &after
	}
	if res.Reason != "" {
		holes := make([]string, 0, len(res.Highlight))
		for _, p := range res.Highlight {
			holes = append(holes, p.String())
		}
		doc.Reason = &reasonDoc{
			Code:     res.Reason,
			Headline: res.Headline,
			Detail:   res.Detail,
			Holes:    holes,
		}
	}
	if res.Stats != nil {
		doc.Stats = &statsDoc{
			Nodes:          res.Stats.Nodes,
			Analyses:       res.Stats.Evaluations,
			CompletedDepth: res.Stats.Depth,
			ElapsedNS:      res.Stats.Elapsed.Nanoseconds(),
			StopReason:     res.Stats.StopReason,
		}
	}
	return doc
}

func terms(t bot.Terms) termsDoc {
	return termsDoc{
		Dist:           t.Dist,
		OppDist:        t.OppDist,
		Bottlenecks:    t.Bottlenecks,
		OppBottlenecks: t.OppBottlenecks,
		Ground:         t.Ground,
		Score:          t.Score(),
	}
}

// reproducible describes this result's completed work, not a guarantee that the
// same wall-clock budget will finish that work on every machine.
func reproducible(stats *bot.SearchStats) bool {
	if stats == nil {
		return true
	}
	switch stats.StopReason {
	case "nodes", "depth", "immediate", "decided":
		return true
	}
	return false
}

// engineStamp is the build this analysis came from. A release reports the
// version and commit stamped into it; a source build reports the honest "dev"
// version with the commit the toolchain recorded, and an unknown commit is
// empty rather than the placeholder word "none", which is not a commit. A
// checkout with uncommitted changes is marked, because then the commit alone
// does not say what ran.
func engineStamp() engineDoc {
	revision, _, modified := checkoutBuilt()
	stamp := engineDoc{Version: version, Commit: revision, Modified: modified}
	if version != unstampedVersion && commit != "" && commit != "none" {
		stamp.Commit = commit
	}
	return stamp
}

// The two name maps publish the same words the record format does. The engine's
// own maps are unexported, and both surfaces are read by scripts, so the names
// have to agree; an unhandled value is reported as the number it is rather than
// as one of the names.
var analysisOutcomes = map[game.Outcome]string{
	game.Ongoing:        "ongoing",
	game.VerticalWins:   "vertical-wins",
	game.HorizontalWins: "horizontal-wins",
	game.Draw:           "draw",
}

var analysisEndReasons = map[game.Reason]string{
	game.NotOver:     "not-over",
	game.Connection:  "connection",
	game.NoMovesLeft: "no-moves-left",
	game.Resignation: "resignation",
	game.Agreement:   "agreement",
}

func outcomeName(o game.Outcome) string {
	if name, ok := analysisOutcomes[o]; ok {
		return name
	}
	return fmt.Sprintf("outcome-%d", int(o))
}

func endReasonName(r game.Reason) string {
	if name, ok := analysisEndReasons[r]; ok {
		return name
	}
	return fmt.Sprintf("reason-%d", int(r))
}

// writeAnalysisText prints the envelope for a reader. It names the policy, what
// each score is a bound on, the work the search did and why it stopped, because
// those are what make the numbers mean anything; it never says a move is best or
// how likely a win is, since the search measured neither.
func writeAnalysisText(w io.Writer, doc analysisDoc) error {
	var head strings.Builder
	fmt.Fprintf(&head, "rules: %s\n", doc.Rules)
	fmt.Fprintf(&head, "position: entry %d, root side %s, digest %s\n", doc.Entry, doc.RootSide, doc.PositionDigest)
	fmt.Fprintf(&head, "policy: %s\n%s\n", doc.Policy, doc.Limitations)
	if doc.Status == statusTerminal {
		fmt.Fprintf(&head, "status: the game is over, %s by %s; there is no turn to advise\n",
			doc.Result.Outcome, doc.Result.Reason)
	} else {
		fmt.Fprintf(&head, "status: %s\n", doc.Status)
	}
	fmt.Fprintf(&head, "terms for %s: %s\n", doc.RootSide, termsPhrase(doc.TermsBefore))
	if doc.Recommended != nil {
		fmt.Fprintf(&head, "\nrecommended: %s\n", *doc.Recommended)
	}
	if doc.Reason != nil {
		fmt.Fprintf(&head, "  %s\n  %s\n", doc.Reason.Headline, doc.Reason.Detail)
		fmt.Fprintf(&head, "  reason %s; holes %s\n", doc.Reason.Code, strings.Join(doc.Reason.Holes, ", "))
	}
	if doc.TermsAfter != nil {
		fmt.Fprintf(&head, "  terms after it: %s\n", termsPhrase(*doc.TermsAfter))
	}
	if len(doc.Candidates) > 0 {
		fmt.Fprintf(&head, "\nalternatives, in the order the search kept them. Scores are integer search units from %s's point of view, not win chances:\n",
			doc.RootSide)
	}
	if _, err := io.WriteString(w, head.String()); err != nil {
		return err
	}
	if len(doc.Candidates) > 0 {
		rows := make([][]string, 0, len(doc.Candidates)+1)
		rows = append(rows, []string{"MOVE", "SCORE", "WHAT THE SCORE IS"})
		for _, c := range doc.Candidates {
			score := ""
			if c.Score != nil {
				score = fmt.Sprintf("%+d", *c.Score)
			}
			rows = append(rows, []string{c.Move, score, boundPhrase(c.Bound)})
		}
		if err := ui.WriteTable(w, rows, 2); err != nil {
			return err
		}
	}
	var tail strings.Builder
	if doc.Stats != nil {
		fmt.Fprintf(&tail, "\nsearch: %d nodes below the root, %d positions scored, depth %d completed, %s\n",
			doc.Stats.Nodes, doc.Stats.Analyses, doc.Stats.CompletedDepth,
			time.Duration(doc.Stats.ElapsedNS))
		fmt.Fprintf(&tail, "stopped on %s: %s\n", doc.Stats.StopReason, stopPhrase(doc.Stats.StopReason))
		if doc.Stats.CompletedDepth == 0 && doc.Stats.StopReason != "immediate" {
			tail.WriteString("No iteration completed; the recommendation is the ordering heuristic's unscored fallback.\n")
		}
	}
	switch {
	case doc.Stats == nil:
		tail.WriteString("reproducible: yes, the result is read off the position and nothing was searched\n")
	case doc.Reproducible:
		tail.WriteString("reproducible: yes for this completed work; another run must also avoid time/cancellation stops\n")
	default:
		tail.WriteString("reproducible: no, this is the work that finished here rather than a fixed amount\n")
	}
	_, err := io.WriteString(w, tail.String())
	return err
}

func termsPhrase(t termsDoc) string {
	return fmt.Sprintf("chain needs %s, the opponent's needs %s; bottlenecks %d against %d; ground %+d in a thousand; score %+d",
		distPhrase(t.Dist), distPhrase(t.OppDist), t.Bottlenecks, t.OppBottlenecks, t.Ground, t.Score)
}

// distPhrase renders a distance term. bot.NoChain is not a distance: it is the
// placement-only evaluation finding no route left, which says nothing about
// whether a legal turn could still connect, so it is never printed as a number.
func distPhrase(d int) string {
	switch d {
	case bot.NoChain:
		return "no route this evaluation can find"
	case 1:
		return "1 peg"
	}
	return fmt.Sprintf("%d pegs", d)
}

// boundPhrase says what a score is a bound on, which is the difference between
// a value the search established and one it stopped short of establishing.
func boundPhrase(b string) string {
	switch bot.ScoreBound(b) {
	case bot.BoundExact:
		return "exact"
	case bot.BoundUpper:
		return "at most this"
	case bot.BoundLower:
		return "at least this"
	case bot.BoundUnscored:
		return "nothing measured it"
	}
	return b
}

// stopPhrase explains a stop reason. The first three ended the search on its
// own terms; the rest interrupt the search.
func stopPhrase(reason string) string {
	switch reason {
	case "depth":
		return "every allowed iteration finished"
	case "nodes":
		return "the node ceiling was reached"
	case "immediate":
		return "a winning placement was taken without searching"
	case "decided":
		return "the selective search found a forced line, which is not a proof over every legal turn"
	case "time":
		return "the search-time guard or caller deadline was reached"
	case "canceled":
		return "the caller interrupted the search"
	case "no-move":
		return "the position had nowhere to play"
	case "error":
		return "the search could not restore the position it was given"
	}
	return "unrecognised stop reason"
}
