package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
)

// replayAnalyser reads one position. The replay screen calls bot.Analyze
// through it; a test puts something in its place that it can hold, release and
// inspect, which is how a cancelled or overtaken search is made to happen on
// cue rather than by timing.
type replayAnalyser func(context.Context, *game.Game, bot.AnalysisOptions) (bot.AnalysisResult, error)

// replayCandidateRows is how many of the engine's candidates the panel lists.
// The first few are the ones the search spent its effort on; the rest of the
// shortlist is mostly bounded from above and says little a reviewer can use.
const replayCandidateRows = 4

// replayAnalysisJob is one analysis the player asked for: the entry it reads,
// when it started, how to stop it, and the last iteration the search finished.
//
// The job is also the analysis's generation. A message names the job it
// answers, and the screen takes it only while that job is the one it is
// waiting on, so a result the player has moved past — or one addressed to a
// replay that has since been closed and opened again — is dropped instead of
// being shown against a position it was not computed for. A counter would do
// the first and not the second: two screens count from the same place.
type replayAnalysisJob struct {
	entry   int
	started time.Time
	cancel  context.CancelFunc
	// latest is written by the search each time an iteration finishes and read
	// by Update on a tick. The atomic pointer is the whole of the hand-off: the
	// search never touches the screen, and the screen never waits on the
	// search.
	latest atomic.Pointer[bot.AnalysisProgress]
}

// tick schedules the next look at the job's progress. The interval is the
// game screen's thinking indicator's, so the two move at the same pace.
func (j *replayAnalysisJob) tick() tea.Cmd {
	return tea.Tick(gsSpinnerInterval, func(at time.Time) tea.Msg {
		return replayAnalysisTickMsg{job: j, at: at}
	})
}

// replayAnalysisDoneMsg is the outcome of one analysis.
type replayAnalysisDoneMsg struct {
	job    *replayAnalysisJob
	result bot.AnalysisResult
	err    error
}

// replayAnalysisTickMsg advances the running indicator and picks up the last
// iteration the search finished.
type replayAnalysisTickMsg struct {
	job *replayAnalysisJob
	at  time.Time
}

// replayAnalysis is the engine's reading of one entry of the record, from the
// moment it is asked for until the player moves on.
//
// It is a review action and nothing more. It is asked for with a key, it reads
// a copy of the position on screen, it keeps nothing once the player moves, and
// it writes nothing anywhere: whether the game it reviews was played with
// hints is a fact about that game, not about what its reviewer may look at.
type replayAnalysis struct {
	job     *replayAnalysisJob
	running bool
	// canceled records that the player stopped the search with the key that
	// started it, so the panel can say so rather than going quiet.
	canceled bool
	// done means result and err hold what the search answered.
	done   bool
	result bot.AnalysisResult
	err    error
	// marks are the holes the result refers to, the recommended one first. They
	// are worked out once, when the result arrives, rather than per frame.
	marks []game.Point

	// progress, spinner and elapsed are what the panel shows while the search
	// runs. progress is the last finished iteration, nil until one finishes.
	progress *bot.AnalysisProgress
	spinner  int
	elapsed  time.Duration
}

// start begins reading pos, the position after entry entries. pos must be a
// copy nothing else touches: the search makes and unmakes moves on it from
// another goroutine, and the cursor's own game is moved by the next seek.
//
// The options are the analysis defaults — the hint's placement-only policy
// under its two-second guard — with an observer added, so what the replay
// shows is what bot.Analyze gives any other caller.
func (a *replayAnalysis) start(run replayAnalyser, pos *game.Game, entry int) tea.Cmd {
	a.drop()
	if run == nil {
		run = bot.Analyze
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &replayAnalysisJob{entry: entry, started: time.Now(), cancel: cancel}
	*a = replayAnalysis{job: job, running: true}
	opts := bot.AnalysisOptions{
		// This runs on the search's goroutine, inside the search. The snapshot
		// is the observer's own, so storing it is all that happens here.
		OnComplete: func(p bot.AnalysisProgress) { job.latest.Store(&p) },
	}
	return tea.Batch(
		func() tea.Msg {
			res, err := run(ctx, pos, opts)
			return replayAnalysisDoneMsg{job: job, result: res, err: err}
		},
		job.tick(),
	)
}

// stop is the analysis key pressed while the search runs: the search is
// cancelled, and whatever it would have answered is not waited for.
func (a *replayAnalysis) stop() {
	if a.job != nil {
		a.job.cancel()
	}
	a.running = false
	a.canceled = true
	a.progress = nil
}

// drop cancels a search in flight and forgets everything about the analysis,
// which is what moving through the record and leaving the review both do. The
// job goes with it, so a message still on its way finds nothing to answer.
func (a *replayAnalysis) drop() {
	if a.job != nil {
		a.job.cancel()
	}
	*a = replayAnalysis{}
}

// finish takes in a finished search. Anything but the search the screen is
// still waiting on is ignored.
func (a *replayAnalysis) finish(m replayAnalysisDoneMsg) {
	if m.job == nil || m.job != a.job || !a.running {
		return
	}
	// The search has returned; cancelling releases what its context holds.
	a.job.cancel()
	a.running = false
	a.progress = nil
	a.done = true
	a.result, a.err = m.result, m.err
	a.marks = nil
	if m.err == nil && m.result.Recommended != nil {
		move := *m.result.Recommended
		a.marks = make([]game.Point, 0, len(m.result.Highlight)+1)
		a.marks = append(a.marks, move)
		for _, p := range m.result.Highlight {
			if p != move {
				a.marks = append(a.marks, p)
			}
		}
	}
}

// tick advances the indicator and reads the last finished iteration off the
// job. A tick for any other job ends its own chain rather than starting a
// second one beside the current search's.
func (a *replayAnalysis) tick(m replayAnalysisTickMsg) tea.Cmd {
	if m.job == nil || m.job != a.job || !a.running {
		return nil
	}
	a.spinner++
	a.elapsed = max(m.at.Sub(m.job.started), 0)
	if p := m.job.latest.Load(); p != nil {
		a.progress = p
	}
	return m.job.tick()
}

// on reports whether the analysis belongs to entry. Seeking drops it, so this
// is the second of two locks rather than the only one: a result is never drawn
// against an entry it was not computed for.
func (a *replayAnalysis) on(entry int) bool { return a.job != nil && a.job.entry == entry }

// shown reports whether a result for entry is on screen.
func (a *replayAnalysis) shown(entry int) bool { return a.on(entry) && a.done && a.err == nil }

// highlights are the holes to mark on the board at entry: the recommended move
// first, then the holes the engine's explanation names. There are none for any
// other entry, and none for a finished position, which has no move to advise.
func (a *replayAnalysis) highlights(entry int) []game.Point {
	if !a.shown(entry) {
		return nil
	}
	return a.marks
}

// toggleAnalysis is the analysis key. It reads the position on screen, stops a
// reading still running, or puts away one already shown — the last because the
// marks it puts on the board are in the way of looking at the board itself.
// A reading that failed or was stopped is asked for again.
func (s *ReplayScreen) toggleAnalysis() tea.Cmd {
	a := &s.analysis
	switch {
	case a.running:
		a.stop()
		return nil
	case a.shown(s.step()):
		a.drop()
		return nil
	}
	return a.start(s.analyser, s.current().Clone(), s.step())
}

// analysisLines is the analysis block of the panel, at most rows lines of it,
// and nothing at all when no analysis belongs to the entry on screen.
func (s *ReplayScreen) analysisLines(width, rows int) []string {
	if width <= 0 || rows <= 0 || !s.analysis.on(s.step()) {
		return nil
	}
	return clampLines(s.analysis.lines(width), rows)
}

// analysisLead is how many rows the analysis block is promised in the panel —
// its badge, and where the analysis stands — and 0 when no analysis belongs to
// the entry on screen. The panel sets these rows aside before it gives the
// record's own details any: the details read the same at every entry, and the
// analysis is what the player has just asked for.
func (s *ReplayScreen) analysisLead(width int) int {
	if width <= 0 || !s.analysis.on(s.step()) {
		return 0
	}
	return len(s.analysis.lead(width))
}

// analysisStatus is the analysis's part of a status line width columns wide,
// and empty when no analysis belongs to the entry on screen. A terminal with
// no room for a panel has nowhere else to show it.
func (s *ReplayScreen) analysisStatus(width int) string {
	if width <= 0 || !s.analysis.on(s.step()) {
		return ""
	}
	return s.analysis.brief(width)
}

// indicator is a running search in the fewest words: that it runs, and for
// how long.
func (a *replayAnalysis) indicator() string {
	return fmt.Sprintf("analysing %s %s",
		gsSpinnerFrames[a.spinner%len(gsSpinnerFrames)], replaySeconds(a.elapsed))
}

// brief is the analysis in the few words a status line width columns wide has
// room for. It keeps the qualification the panel spells out: a hole comes with
// its policy badge and with "not proven", the caveat as an item of its own
// after the pair — the same pair a game's status line keeps for a hint.
//
// A recommendation is fitted by giving up whole items before anything is cut:
// the caveat goes first, and the badge only when the hole alone is all that
// fits. The frame pulls a cut back to the last whole word before it, and a cut
// that fills the row exactly has no scrap of the separator in front of its
// mark to show where the pair ended: at twenty columns a four-character hole,
// AA10 on a wide board, came out as "AA10 placement-only…" and then as
// "AA10…", without the badge, though the pair fits with a column to spare.
// Every other state is one phrase with nothing in it to give up first, and is
// shortened with a mark.
func (a *replayAnalysis) brief(width int) string {
	r := a.result
	var state string
	switch {
	case a.running:
		state = a.indicator()
	case a.canceled:
		state = "analysis canceled"
	case a.err != nil:
		state = "analysis failed"
	case r.Result.Over():
		state = "game over: no move to advise"
	case r.Recommended == nil:
		state = "engine returned no move"
	default:
		hole := r.Recommended.String()
		pair := hole + " " + r.Policy.Label()
		if ansi.StringWidth(pair) > width {
			return truncateText(hole, width)
		}
		return hintLine(width, pair, "not proven")
	}
	return truncateText(state, width)
}

// lead is the beginning of the block: the badge, and one sentence saying where
// the analysis stands — running, stopped, refused, over, or the move it chose.
// Everything after it qualifies or details that sentence, so a panel with rows
// for nothing else still shows this much.
func (a *replayAnalysis) lead(width int) []string {
	// The badge is the policy the search runs under. Until a result arrives
	// that is the one policy bot.Analyze searches with; after, it is the one
	// the result itself states.
	policy := bot.PlacementOnlyPolicy()
	if a.done && a.err == nil {
		policy = a.result.Policy
	}
	out := []string{replayAnalysisHeader(policy.Label(), width)}

	r := a.result
	var state string
	switch {
	case a.running:
		state = a.indicator() + " · ? cancels"
	case a.canceled:
		state = "canceled; ? starts it again"
	case a.err != nil:
		// The error is the engine's, but it is text all the same, and the
		// panel draws nothing a terminal would act on.
		state = "the engine could not analyse this position: " + replayLabel(a.err.Error())
	case r.Result.Over():
		state = "the game is over here, " + describeOutcome(r.Result) + ", so there is no move to advise"
	case r.Recommended == nil:
		state = "the engine returned no move"
	default:
		state = fmt.Sprintf("%s is the engine's choice under a bounded search, not a proven best move", *r.Recommended)
	}
	return append(out, wrapText(state, width)...)
}

// lines renders the block, most important line first, because a short panel
// keeps the beginning and drops the end: the lead, then what stands behind it.
//
// The engine's own words — its headline and detail, and its account of the
// policy it searched under — are shown as it wrote them. What this adds is what
// the numbers are: which move the search settled on and that it is a choice,
// what each candidate's score is a bound on, and how much work stands behind
// them. A result is never called the best move, because a bounded search under
// a placement-only policy cannot establish one.
func (a *replayAnalysis) lines(width int) []string {
	out := make([]string, 0, 16)
	out = append(out, a.lead(width)...)
	add := func(text string) { out = append(out, truncateText(text, width)) }
	wrap := func(text string) { out = append(out, wrapText(text, width)...) }

	r := a.result
	switch {
	case a.running:
		if p := a.progress; p != nil {
			wrap(fmt.Sprintf("completed so far: depth %d, %d nodes, %s",
				p.Stats.Depth, p.Stats.Nodes, replaySeconds(p.Stats.Elapsed)))
		}
		return out
	case a.canceled, a.err != nil, r.Result.Over(), r.Recommended == nil:
		return out
	}

	if h := strings.TrimSpace(r.Headline); h != "" {
		wrap(h)
	}
	if d := strings.TrimSpace(r.Detail); d != "" {
		wrap(d)
	}
	if len(r.Candidates) > 0 {
		add("candidates, in search units:")
		for _, c := range r.Candidates[:min(len(r.Candidates), replayCandidateRows)] {
			add("  " + padTo(c.Move.String(), 4) + " " + replayScorePhrase(c))
		}
	}
	if st := r.Stats; st != nil {
		wrap(fmt.Sprintf("completed work: depth %d, %d nodes, %s",
			st.Depth, st.Nodes, replaySeconds(st.Elapsed)))
		stop := "stopped: " + replayStopPhrase(st.StopReason)
		if !replayReproducible(st.StopReason) {
			stop += ", so not reproducible"
		}
		wrap(stop)
		if st.Depth == 0 && st.StopReason != "immediate" {
			wrap("no iteration finished, so the candidates are the move ordering's and unscored")
		}
	}
	wrap(r.Policy.Summary())
	return out
}

// replayAnalysisHeader is the block's first line: what it is, and the policy
// badge. On a panel with no room for both the badge is kept, because it is the
// part that qualifies everything under it.
func replayAnalysisHeader(badge string, width int) string {
	if full := "analysis · " + badge; ansi.StringWidth(full) <= width {
		return full
	}
	return truncateText(badge, width)
}

// replayScorePhrase says what a candidate's score is. An unscored candidate
// gets no number: its score is zero because nothing measured it, and a zero on
// screen would read as a measurement.
func replayScorePhrase(c bot.AnalysisCandidate) string {
	switch c.Bound {
	case bot.BoundExact:
		return fmt.Sprintf("%+d exact", c.Score)
	case bot.BoundUpper:
		return fmt.Sprintf("at most %+d", c.Score)
	case bot.BoundLower:
		return fmt.Sprintf("at least %+d", c.Score)
	case bot.BoundUnscored:
		return "unscored"
	}
	// A bound this screen does not know says nothing it can put a number to.
	return replayLabel(string(c.Bound))
}

// replayStopPhrase explains why the search stopped, in the words the analyze
// command uses for the same reasons.
func replayStopPhrase(reason string) string {
	switch reason {
	case "depth":
		return "every allowed iteration finished"
	case "nodes":
		return "the node ceiling was reached"
	case "immediate":
		return "a winning hole was taken without searching"
	case "decided":
		return "a forced line inside the selective search, not a proof"
	case "time":
		return "the search-time guard was reached"
	case "canceled":
		return "the search was interrupted"
	}
	return replayLabel(reason)
}

// replayReproducible reports whether the completed work would be the same on
// another run. A search the clock or an interruption ended is whatever this
// machine had finished by then; one that ended on its own terms is not.
func replayReproducible(reason string) bool {
	switch reason {
	case "nodes", "depth", "immediate", "decided":
		return true
	}
	return false
}

// replaySeconds renders a duration to a tenth of a second, which is as finely
// as a reader can use it.
func replaySeconds(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}
