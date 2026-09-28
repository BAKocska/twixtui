package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/ui"
)

// rpaHarness drives a replay the way Bubble Tea does: keys go in through
// Update, every command that comes back runs on a goroutine of its own, and
// what it produces is fed back in. A batch is taken apart as the runtime takes
// it apart, so the search and its indicator run side by side.
type rpaHarness struct {
	t    *testing.T
	s    *ReplayScreen
	msgs chan tea.Msg
	// answered counts the analysis outcomes delivered to the screen, whatever
	// the screen then made of them, so a test can wait for a late one to have
	// arrived before asserting that it was ignored.
	answered int
	// left counts the times the screen asked to be left.
	left int
}

func rpaOpen(t *testing.T, d Deps, sv gamestore.Saved, run replayAnalyser) *rpaHarness {
	t.Helper()
	s := rpOpen(t, d, sv)
	s.analyser = run
	h := &rpaHarness{t: t, s: s, msgs: make(chan tea.Msg, 64)}
	h.feed(tea.WindowSizeMsg{Width: 120, Height: 40})
	return h
}

func (h *rpaHarness) dispatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.dispatch(c)
			}
			return
		}
		if msg == nil {
			return
		}
		select {
		case h.msgs <- msg:
		case <-time.After(5 * time.Second):
		}
	}()
}

func (h *rpaHarness) feed(msg tea.Msg) {
	h.t.Helper()
	switch msg.(type) {
	case DoneMsg:
		h.left++
		return
	case replayAnalysisDoneMsg:
		h.answered++
	}
	_, cmd := h.s.Update(msg)
	h.dispatch(cmd)
}

func (h *rpaHarness) press(key string) {
	h.t.Helper()
	msg := tutorialKeyMsg(key)
	if got := msg.String(); got != key {
		h.t.Fatalf("the test cannot press %q: it builds as %q", key, got)
	}
	h.feed(msg)
}

// waitFor delivers what the commands produce until cond holds.
func (h *rpaHarness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case msg := <-h.msgs:
			h.feed(msg)
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s at step %d\n%s", what, h.s.step(), h.block())
		}
	}
}

// outcome waits for the analysis outcome the screen's commands produce and
// hands it back undelivered, dropping everything else they produce on the way.
// The program delivers a message to whichever screen is up when it arrives, so
// a test can give a closed screen's answer to the screen that replaced it.
func (h *rpaHarness) outcome() replayAnalysisDoneMsg {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if m, ok := msg.(replayAnalysisDoneMsg); ok {
				return m
			}
		case <-deadline:
			h.t.Fatal("the search never answered")
		}
	}
}

// block is the analysis block as a wide panel draws it, so that nothing in it
// is wrapped and a line can be read as a whole.
func (h *rpaHarness) block() string {
	return strings.Join(h.s.analysisLines(200, 200), "\n")
}

// frame draws the screen, which is also what puts the marks on the board.
func (h *rpaHarness) frame() string { return h.s.View().Content }

// rpaCall is one request the stand-in engine received.
type rpaCall struct {
	ctx    context.Context
	pos    *game.Game
	digest string
	// progress is handed to the analysis's observer from the search's own
	// goroutine, which is where the engine calls it from.
	progress chan bot.AnalysisProgress
	answer   chan rpaAnswer
}

type rpaAnswer struct {
	result bot.AnalysisResult
	err    error
}

// rpaEngine stands in for bot.Analyze. Each request is announced and then held
// until the test answers it, so whether the screen has moved on by the time a
// search finishes is the test's decision rather than the scheduler's.
type rpaEngine struct{ calls chan *rpaCall }

func newRPAEngine() *rpaEngine { return &rpaEngine{calls: make(chan *rpaCall, 8)} }

func (e *rpaEngine) analyse(ctx context.Context, g *game.Game, opts bot.AnalysisOptions) (bot.AnalysisResult, error) {
	call := &rpaCall{
		ctx: ctx, pos: g, digest: game.PositionDigest(g),
		progress: make(chan bot.AnalysisProgress, 1),
		answer:   make(chan rpaAnswer, 1),
	}
	e.calls <- call
	for {
		select {
		case p := <-call.progress:
			if opts.OnComplete != nil {
				opts.OnComplete(p)
			}
		case a := <-call.answer:
			return a.result, a.err
		case <-time.After(10 * time.Second):
			return bot.AnalysisResult{}, errors.New("the test never answered")
		}
	}
}

func (e *rpaEngine) next(t *testing.T) *rpaCall {
	t.Helper()
	select {
	case c := <-e.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the engine was never asked")
	}
	return nil
}

// The holes the fixture answer names. They are interior holes of the six-hole
// board rpConnectionRecord is played on, and empty at every entry the tests
// analyse.
var (
	rpaMove    = game.Point{Col: 2, Row: 3}
	rpaUpper   = game.Point{Col: 3, Row: 1}
	rpaNone    = game.Point{Col: 1, Row: 4}
	rpaLower   = game.Point{Col: 4, Row: 2}
	rpaFifth   = game.Point{Col: 4, Row: 3}
	rpaAlsoRef = game.Point{Col: 3, Row: 3}
)

// rpaFixture is an answer shaped like one the engine gives for a position with
// room to play: one candidate of every kind of bound, and one more than the
// panel lists.
func rpaFixture() bot.AnalysisResult {
	move := rpaMove
	return bot.AnalysisResult{
		Policy:      bot.PlacementOnlyPolicy(),
		Recommended: &move,
		Candidates: []bot.AnalysisCandidate{
			{Move: rpaMove, Score: 120, Bound: bot.BoundExact},
			{Move: rpaUpper, Score: 80, Bound: bot.BoundUpper},
			{Move: rpaNone, Bound: bot.BoundUnscored},
			{Move: rpaLower, Score: -15, Bound: bot.BoundLower},
			{Move: rpaFifth, Score: 40, Bound: bot.BoundExact},
		},
		Reason:    "advance",
		Headline:  "the headline the engine wrote",
		Detail:    "the detail the engine wrote",
		Highlight: []game.Point{rpaMove, rpaAlsoRef},
		Stats:     &bot.SearchStats{Nodes: 4321, Evaluations: 9000, Depth: 3, Elapsed: 1500 * time.Millisecond, StopReason: "time"},
	}
}

// rpaAt opens rpConnectionRecord on the stand-in engine and goes to entry by
// its number, the way a player does.
func rpaAt(t *testing.T, entry int) (*rpaHarness, *rpaEngine) {
	t.Helper()
	eng := newRPAEngine()
	h := rpaOpen(t, shellTestDeps(t), rpConnectionRecord(t), eng.analyse)
	rpJump(t, h.s, strconv.Itoa(entry))
	if h.s.step() != entry {
		t.Fatalf("the jump reached step %d, want %d", h.s.step(), entry)
	}
	return h, eng
}

// TestReplayAnalysisReadsTheEntryNotThePly is the analysis's half of numbering
// the record by entry: rpConnectionRecord has draw offers between its moves, so
// the position after n entries is not the position after n moves, and two
// neighbouring entries can differ only in the offer standing. The position the
// engine is handed has to be the one on screen, down to the offer.
func TestReplayAnalysisReadsTheEntryNotThePly(t *testing.T) {
	record, err := game.DecodeRecord(rpConnectionRecord(t).Record)
	if err != nil {
		t.Fatal(err)
	}
	labels := recordLabels(record.Moves, 17)
	for _, entry := range []int{4, 5, 9, 13} {
		t.Run(strconv.Itoa(entry), func(t *testing.T) {
			want, err := game.ReplayTranscript(record.Ruleset, strings.Join(labels[:entry], "; "))
			if err != nil {
				t.Fatal(err)
			}
			if want.Ply() == entry {
				t.Fatalf("entry %d is also ply %d, so it cannot tell the two apart", entry, want.Ply())
			}
			h, eng := rpaAt(t, entry)
			h.press("?")
			call := eng.next(t)
			if call.digest != game.PositionDigest(want) {
				t.Fatalf("the engine was asked about position %s, the position after %d entries is %s",
					call.digest, entry, game.PositionDigest(want))
			}
			call.answer <- rpaAnswer{result: rpaFixture()}
			h.waitFor("the analysis", func() bool { return h.answered == 1 })
			if frame := h.frame(); !strings.Contains(frame, "engine's choice") {
				t.Fatalf("the analysis of entry %d is not on screen:\n%s", entry, frame)
			}
		})
	}
}

// TestReplayAnalysisIsShownOnlyOnTheEntryItRead holds a result to the position
// it was computed for: on the board and in the panel at that entry, and nowhere
// once the player has moved — not even on coming back, since what was put away
// is gone rather than hidden.
func TestReplayAnalysisIsShownOnlyOnTheEntryItRead(t *testing.T) {
	h, eng := rpaAt(t, 9)
	h.press("?")
	eng.next(t).answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.answered == 1 })

	frame := h.frame()
	if !strings.Contains(frame, "engine's choice") {
		t.Fatalf("the analysis is not in the panel:\n%s", frame)
	}
	// The recommended hole first, then what the explanation refers to, with the
	// hole the engine named twice marked once.
	if want := []game.Point{rpaMove, rpaAlsoRef}; !slices.Equal(h.s.board.Highlights, want) {
		t.Fatalf("the board marks %v, want %v", h.s.board.Highlights, want)
	}

	h.press("l")
	frame = h.frame()
	if strings.Contains(frame, "engine's choice") || h.block() != "" {
		t.Fatalf("step %d shows the analysis of step 9:\n%s", h.s.step(), frame)
	}
	if marks := h.s.board.Highlights; len(marks) != 0 {
		t.Fatalf("step %d marks %v, the holes of an analysis of step 9", h.s.step(), marks)
	}

	h.press("h")
	if h.s.step() != 9 {
		t.Fatalf("stepping back reached step %d", h.s.step())
	}
	frame = h.frame()
	if strings.Contains(frame, "engine's choice") || len(h.s.board.Highlights) != 0 {
		t.Fatalf("an analysis put away by moving came back with the position:\n%s", frame)
	}
}

// TestReplayAnalysisOvertakenByASeekIsDropped is the stale result: the search
// is still running when the player moves, and finishes anyway. Every way of
// moving has to cancel it at once, and what it then answers has to be dropped
// — here, and on the entry it was about, which the player can go straight back
// to before the answer arrives.
func TestReplayAnalysisOvertakenByASeekIsDropped(t *testing.T) {
	for _, c := range []struct {
		name string
		move func(h *rpaHarness)
	}{
		{"l", func(h *rpaHarness) { h.press("l") }},
		{"h", func(h *rpaHarness) { h.press("h") }},
		{"j", func(h *rpaHarness) { h.press("j") }},
		{"k", func(h *rpaHarness) { h.press("k") }},
		{"g", func(h *rpaHarness) { h.press("g") }},
		{"G", func(h *rpaHarness) { h.press("G") }},
		{"entry number", func(h *rpaHarness) { rpJump(h.t, h.s, "3") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, eng := rpaAt(t, 9)
			h.press("?")
			call := eng.next(t)
			c.move(h)
			if h.s.step() == 9 {
				t.Fatal("the key did not move the replay")
			}
			if call.ctx.Err() == nil {
				t.Fatal("moving through the record left the search running")
			}
			if h.s.analysis.running {
				t.Fatal("the screen is still waiting on a search for a position it has left")
			}
			rpJump(t, h.s, "9")

			call.answer <- rpaAnswer{result: rpaFixture()}
			h.waitFor("the overtaken answer", func() bool { return h.answered == 1 })
			frame := h.frame()
			if strings.Contains(frame, "engine's choice") || h.block() != "" || len(h.s.board.Highlights) != 0 {
				t.Fatalf("an answer the player had moved past was shown:\n%s", frame)
			}
		})
	}
}

// TestLeavingTheReplayCancelsTheAnalysis is the promise every search on a
// screen gets: nothing keeps working for a review nobody is looking at, however
// the player goes.
func TestLeavingTheReplayCancelsTheAnalysis(t *testing.T) {
	for _, key := range []string{"q", "esc"} {
		t.Run(key, func(t *testing.T) {
			h, eng := rpaAt(t, 9)
			h.press("?")
			call := eng.next(t)
			h.press(key)
			if call.ctx.Err() == nil {
				t.Fatalf("leaving with %q left the search running", key)
			}
			h.waitFor("the screen to be left", func() bool { return h.left == 1 })

			call.answer <- rpaAnswer{result: rpaFixture()}
			h.waitFor("the late answer", func() bool { return h.answered == 1 })
			if h.s.analysis.done || h.block() != "" {
				t.Fatalf("a screen that has been left took in its search's answer:\n%s", h.block())
			}
		})
	}
	// The control form of the quit key never reaches the screen: the shell
	// answers it and lets every screen finish on the way out.
	t.Run("ctrl+c", func(t *testing.T) {
		h, eng := rpaAt(t, 9)
		h.press("?")
		call := eng.next(t)
		sh := NewShell(h.s.deps, h.s)
		if _, cmd := sh.Update(tutorialKeyMsg("ctrl+c")); cmd == nil {
			t.Fatal("ctrl+c did not end the program")
		}
		if call.ctx.Err() == nil {
			t.Fatal("ending the program left the search running")
		}
		call.answer <- rpaAnswer{result: rpaFixture()}
	})
}

// TestAskingAgainCancelsTheRunningAnalysis covers the key's other meaning while
// a search runs: it stops the search, says so, and ignores what the search
// answers. Before that, the work the search has finished is on screen as
// finished work, taken from the search's own goroutine without touching the
// screen from there.
func TestAskingAgainCancelsTheRunningAnalysis(t *testing.T) {
	h, eng := rpaAt(t, 9)
	h.press("?")
	call := eng.next(t)
	if !strings.Contains(h.block(), "? cancels") {
		t.Fatalf("a running analysis does not say how to stop it:\n%s", h.block())
	}

	call.progress <- bot.AnalysisProgress{
		Recommended: rpaMove,
		Stats:       bot.SearchStats{Nodes: 1234, Depth: 3, Elapsed: 800 * time.Millisecond},
	}
	h.waitFor("the finished iteration", func() bool { return strings.Contains(h.block(), "depth 3") })
	if block := h.block(); !strings.Contains(block, "completed so far") || !strings.Contains(block, "1234 nodes") {
		t.Fatalf("the finished iteration is not reported as completed work:\n%s", block)
	}
	h.frame()
	if len(h.s.board.Highlights) != 0 {
		t.Fatalf("a search still running marks %v on the board", h.s.board.Highlights)
	}

	h.press("?")
	if call.ctx.Err() == nil {
		t.Fatal("the second ? left the search running")
	}
	if block := h.block(); !strings.Contains(block, "canceled") || strings.Contains(block, "depth 3") {
		t.Fatalf("the panel does not say the analysis was canceled:\n%s", block)
	}

	call.answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the canceled search's answer", func() bool { return h.answered == 1 })
	if frame := h.frame(); strings.Contains(frame, "engine's choice") || len(h.s.board.Highlights) != 0 {
		t.Fatalf("a canceled search's answer was shown:\n%s", frame)
	}

	// Asking once more is a new search, not the old one resumed.
	h.press("?")
	again := eng.next(t)
	if again.ctx.Err() != nil {
		t.Fatal("the new search started out canceled")
	}
	again.answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the new answer", func() bool { return h.answered == 2 })
	if !strings.Contains(h.block(), "engine's choice") {
		t.Fatalf("the search asked for after a cancel was not shown:\n%s", h.block())
	}
}

// rpaRow finds the candidate row for p in a block: among the lines under the
// candidates heading, the one whose first word is the hole's name. The line
// naming the engine's choice starts with a hole too, and is not a row.
func rpaRow(t *testing.T, block string, p game.Point) (string, bool) {
	t.Helper()
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "candidates") {
			continue
		}
		for _, row := range lines[i+1:] {
			if fields := strings.Fields(row); len(fields) > 0 && fields[0] == p.String() {
				return strings.Join(fields[1:], " "), true
			}
		}
	}
	return "", false
}

// TestReplayAnalysisSaysWhatEachScoreIs is the honesty of the numbers. A score
// the search measured is shown as one; a ceiling is shown as a ceiling, not as a
// value to compare with the measured one; and a candidate nothing scored shows
// no number at all, because its zero is the absence of a measurement.
func TestReplayAnalysisSaysWhatEachScoreIs(t *testing.T) {
	h, eng := rpaAt(t, 9)
	h.press("?")
	eng.next(t).answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.answered == 1 })
	block := h.block()

	for _, c := range []struct {
		p           game.Point
		want, never string
	}{
		{rpaMove, "+120 exact", "at most"},
		{rpaUpper, "at most +80", "exact"},
		{rpaLower, "at least -15", "exact"},
	} {
		row, ok := rpaRow(t, block, c.p)
		if !ok {
			t.Fatalf("no row for %s:\n%s", c.p, block)
		}
		if !strings.Contains(row, c.want) || strings.Contains(row, c.never) {
			t.Errorf("%s reads %q, want %q and never %q", c.p, row, c.want, c.never)
		}
	}
	row, ok := rpaRow(t, block, rpaNone)
	if !ok {
		t.Fatalf("no row for the unscored candidate %s:\n%s", rpaNone, block)
	}
	if !strings.Contains(row, "unscored") || strings.ContainsAny(row, "0123456789") {
		t.Errorf("the unscored candidate reads %q, which puts a number to nothing measured", row)
	}
	if _, ok := rpaRow(t, block, rpaFifth); ok {
		t.Errorf("the panel lists a fifth candidate:\n%s", block)
	}

	// The move is the engine's choice under a bounded placement-only search,
	// and the work behind it is there to be weighed.
	for _, want := range []string{
		rpaMove.String() + " is the engine's choice", "not a proven best move", "placement-only",
		"the headline the engine wrote", "the detail the engine wrote",
		"depth 3", "4321 nodes", "not reproducible",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the analysis does not say %q:\n%s", want, block)
		}
	}
	if strings.Contains(strings.ReplaceAll(block, "not a proven best move", ""), "best move") {
		t.Errorf("the analysis calls a move the best one:\n%s", block)
	}
}

// TestReplayAnalysisMarksOnlyUnrepeatableWorkAsSuch keeps the reproducibility
// note on the stops that earn it. A search the clock or an interruption ended
// is whatever this machine had finished; one that ended on its own terms is
// the same search on any machine, and calling it unrepeatable would be as
// wrong as the reverse.
func TestReplayAnalysisMarksOnlyUnrepeatableWorkAsSuch(t *testing.T) {
	for _, c := range []struct {
		stop       string
		repeatable bool
	}{
		{"time", false}, {"canceled", false},
		{"depth", true}, {"nodes", true}, {"decided", true}, {"immediate", true},
	} {
		t.Run(c.stop, func(t *testing.T) {
			r := rpaFixture()
			r.Stats.StopReason = c.stop
			a := replayAnalysis{job: &replayAnalysisJob{}, done: true, result: r}
			block := strings.Join(a.lines(200), "\n")
			if marked := strings.Contains(block, "not reproducible"); marked == c.repeatable {
				t.Errorf("a search that stopped on %q is marked not reproducible=%v:\n%s", c.stop, marked, block)
			}
		})
	}
}

// TestReplayAnalysisOfAFinishedPositionAdvisesNothing asks the real engine about
// the last entry of finished games. There is no turn left to advise, so there
// is no move, no candidate and no mark of the engine's; the chain the game was
// won with stays marked where it was.
func TestReplayAnalysisOfAFinishedPositionAdvisesNothing(t *testing.T) {
	resigned, _ := rpSpecialRecord(t, "h:resign")
	for _, c := range []struct {
		name  string
		sv    gamestore.Saved
		ended string
	}{
		{"connection", rpConnectionRecord(t), "vertical won by completing a chain"},
		{"resignation", resigned, "vertical won by resignation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := rpaOpen(t, shellTestDeps(t), c.sv, nil)
			if h.s.step() != h.s.entries() {
				t.Fatal("the review did not open at the final entry")
			}
			h.press("?")
			h.waitFor("the engine's answer", func() bool { return h.answered == 1 })
			if h.s.analysis.err != nil {
				t.Fatalf("the engine refused a finished position: %v", h.s.analysis.err)
			}
			block := h.block()
			if !strings.Contains(block, c.ended) || !strings.Contains(block, "no move to advise") {
				t.Errorf("the analysis does not say the game is over:\n%s", block)
			}
			if strings.Contains(block, "engine's choice") || strings.Contains(block, "candidates") {
				t.Errorf("a finished position was given a move:\n%s", block)
			}
			h.frame()
			if !slices.Equal(h.s.board.Highlights, h.s.winning) {
				t.Errorf("the board marks %v at the final entry, want the winning chain %v", h.s.board.Highlights, h.s.winning)
			}
		})
	}
}

// TestReplayAnalysisReadsACopy hands the engine's position back to the test and
// changes it there. A screen that gave the engine the position it shows would
// now be showing whatever the search left it in; the one on screen has to be
// untouched, and the record still walks the way a fresh replay does.
func TestReplayAnalysisReadsACopy(t *testing.T) {
	h, eng := rpaAt(t, 9)
	shown := h.s.current()
	before := game.PositionDigest(shown)
	h.press("?")
	call := eng.next(t)
	if call.pos == shown {
		t.Fatal("the engine was handed the position on screen rather than a copy of it")
	}
	if err := call.pos.UndoLastMove(); err != nil {
		t.Fatal(err)
	}
	if got := game.PositionDigest(h.s.current()); got != before {
		t.Fatalf("changing the analysed position changed the one on screen: %s, was %s", got, before)
	}
	call.answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.answered == 1 })

	record, err := game.DecodeRecord(rpConnectionRecord(t).Record)
	if err != nil {
		t.Fatal(err)
	}
	h.press("l")
	want, err := game.ReplayTranscript(record.Ruleset, strings.Join(recordLabels(record.Moves, 17)[:10], "; "))
	if err != nil {
		t.Fatal(err)
	}
	rpSamePosition(t, 10, h.s.current(), want)
}

// rpaFiles lists everything under dir with its size and modification time, so
// two listings differ if anything was created, removed or written.
func rpaFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("dir=%t size=%d mod=%d", e.IsDir(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestReplayAnalysisWithTheRealEngine runs bot.Analyze itself under a work
// bound, with the machine's hint preference turned off. A review is not a game
// played with or without advice: the analysis is asked for here, answers with a
// move the rules allow on the position on screen, leaves that position as it
// was, and writes nothing.
func TestReplayAnalysisWithTheRealEngine(t *testing.T) {
	d := shellTestDeps(t)
	off := false
	if err := (gameDefaults{Hints: &off}).save(d.ConfigDir); err != nil {
		t.Fatal(err)
	}
	h := rpaOpen(t, d, rpConnectionRecord(t), func(ctx context.Context, g *game.Game, o bot.AnalysisOptions) (bot.AnalysisResult, error) {
		// A node bound with the clock lifted out of its way is a search that
		// ends on its own terms, and so takes the same time on any machine.
		o.Limits = bot.Limits{Nodes: 400, Time: time.Minute}
		return bot.Analyze(ctx, g, o)
	})
	rpJump(t, h.s, "9")
	pos := h.s.current()
	before := game.PositionDigest(pos)
	files := rpaFiles(t, d.ConfigDir)

	h.press("?")
	h.waitFor("the engine's answer", func() bool { return h.answered == 1 })
	a := h.s.analysis
	if a.err != nil || !a.shown(9) {
		t.Fatalf("the analysis did not land on step 9: %v\n%s", a.err, h.block())
	}
	if a.result.Recommended == nil {
		t.Fatal("the engine recommended nothing for a position with room to play")
	}
	if err := pos.CanPlace(pos.Turn(), *a.result.Recommended); err != nil {
		t.Errorf("the engine recommended %s, which the position refuses: %v", *a.result.Recommended, err)
	}
	if got := game.PositionDigest(h.s.current()); got != before {
		t.Errorf("the analysis moved the position on screen: %s, was %s", got, before)
	}
	block := h.block()
	for _, want := range []string{"placement-only", "is the engine's choice", "not a proven best move", "completed work"} {
		if !strings.Contains(block, want) {
			t.Errorf("the analysis does not say %q:\n%s", want, block)
		}
	}
	if st := a.result.Stats; st == nil || st.StopReason != "nodes" && st.StopReason != "depth" && st.StopReason != "decided" && st.StopReason != "immediate" {
		t.Errorf("a work-bound search stopped on %+v", st)
	} else if strings.Contains(block, "not reproducible") {
		t.Errorf("a search that stopped on %q is called unrepeatable:\n%s", st.StopReason, block)
	}
	h.frame()
	if len(h.s.board.Highlights) == 0 || h.s.board.Highlights[0] != *a.result.Recommended {
		t.Errorf("the board marks %v, which does not start with the move %s", h.s.board.Highlights, *a.result.Recommended)
	}
	if after := rpaFiles(t, d.ConfigDir); !maps.Equal(files, after) {
		t.Errorf("the analysis wrote to the configuration directory:\nbefore %v\nafter  %v", files, after)
	}
}

// TestReplayFooterOffersTheAnalysisKey holds the footer and the key together:
// the footer names ? and pressing it reads the entry on screen. While the entry
// input is open the keyboard is the input's, so ? is text it refuses rather
// than an analysis started behind the number being typed.
func TestReplayFooterOffersTheAnalysisKey(t *testing.T) {
	h, eng := rpaAt(t, 9)
	if keys := rpFooterKeys(t, h.s.status(200)); !slices.Contains(keys, "?") {
		t.Fatalf("the footer does not name ?: %v", keys)
	}

	h.press(":")
	h.press("?")
	if h.s.analysis.running || h.s.analysis.job != nil {
		t.Fatal("? typed into the entry input started an analysis")
	}
	if h.s.jump.problem == "" {
		t.Error("the entry input took ? without refusing it")
	}
	h.press("esc")

	h.press("?")
	if !h.s.analysis.running || !h.s.analysis.on(9) {
		t.Fatal("? did not start an analysis of the entry on screen")
	}
	eng.next(t).answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.answered == 1 })

	// With the answer on screen the same key puts it away, marks and all.
	h.press("?")
	if frame := h.frame(); strings.Contains(frame, "engine's choice") || len(h.s.board.Highlights) != 0 {
		t.Fatalf("a second ? did not put the analysis away:\n%s", frame)
	}
}

// rpaOnScreen draws the screen and finds the analysis on it: status on the
// bottom row, and panel in the rows above it wherever the arrangement has a
// panel. Each phrase is one the other place never uses, so neither can be read
// off the other's rows.
func rpaOnScreen(t *testing.T, h *rpaHarness, arr ui.Arrangement, status, panel string) {
	t.Helper()
	frame := h.frame()
	shellAssertFits(t, "replay with an analysis", frame, arr.Width, arr.Height)
	lines := strings.Split(frame, "\n")
	if bottom := lines[len(lines)-1]; !strings.Contains(bottom, status) {
		t.Fatalf("at %dx%d the bottom row %q does not say %q:\n%s", arr.Width, arr.Height, bottom, status, frame)
	}
	if arr.Panel == ui.PanelNone {
		return
	}
	if above := strings.Join(lines[:len(lines)-1], "\n"); !strings.Contains(above, panel) {
		t.Fatalf("at %dx%d the panel does not say %q:\n%s", arr.Width, arr.Height, panel, frame)
	}
}

// TestReplayAnalysisIsOnScreenAtEverySize asks for an analysis in every
// terminal a review has to work in, and follows it to each way it can end. The
// bottom row carries it at every size, because at the smallest there is no
// panel and the bottom row is the whole of the interface. Wherever there is a
// panel, the panel carries it too, however few rows it has: the seven-row panel
// under the board at 40x20 once gave every row the entry list left over to the
// record's details, and showed nothing at all after ? — not the running search,
// not how to stop it, not what it found.
func TestReplayAnalysisIsOnScreenAtEverySize(t *testing.T) {
	sizes := append([][2]int{{40, 20}}, shellSizes...)
	// The premise, on the six-hole board rpConnectionRecord is played on: one
	// of the sizes has no panel at all, and 40x20 puts its panel under the
	// board, where rows are scarce, rather than beside it.
	panelless := false
	for _, size := range sizes {
		panelless = panelless || ui.Arrange(size[0], size[1], 6).Panel == ui.PanelNone
	}
	if !panelless || ui.Arrange(40, 20, 6).Panel != ui.PanelBottom {
		t.Fatal("the sizes no longer include a terminal with no panel and one with a short panel under the board")
	}

	for _, c := range []struct {
		name  string
		entry int
		// end is how the search ends: with a move, with the game already over
		// at the entry, refused, or stopped by the player.
		end func(h *rpaHarness, call *rpaCall)
		// status is what the bottom row says once it has ended, and panel what
		// the panel says.
		status, panel string
	}{
		{"choice", 9, func(_ *rpaHarness, call *rpaCall) {
			call.answer <- rpaAnswer{result: rpaFixture()}
		}, rpaMove.String() + " placement-only", "engine's choice"},
		{"game over", 17, func(h *rpaHarness, call *rpaCall) {
			call.answer <- rpaAnswer{result: bot.AnalysisResult{Policy: bot.PlacementOnlyPolicy(), Result: h.s.cursor.result}}
		}, "game over", "the game is over here"},
		{"error", 9, func(_ *rpaHarness, call *rpaCall) {
			call.answer <- rpaAnswer{err: errors.New("the search broke")}
		}, "analysis failed", "could not analyse"},
		{"canceled", 9, func(h *rpaHarness, call *rpaCall) {
			h.press("?")
			call.answer <- rpaAnswer{result: rpaFixture()}
		}, "analysis canceled", "canceled; ? starts it again"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, size := range sizes {
				w, ht := size[0], size[1]
				t.Run(fmt.Sprintf("%dx%d", w, ht), func(t *testing.T) {
					h, eng := rpaAt(t, c.entry)
					h.feed(tea.WindowSizeMsg{Width: w, Height: ht})
					arr := ui.Arrange(w, ht, h.s.current().Size())
					h.press("?")
					call := eng.next(t)
					rpaOnScreen(t, h, arr, "analysing", "? cancels")

					c.end(h, call)
					h.waitFor("the search to end", func() bool { return h.answered == 1 })
					rpaOnScreen(t, h, arr, c.status, c.panel)
				})
			}
		})
	}
}

// TestReplayAnalysisStatusKeepsTheBadgeBesideALongHole puts the longest name a
// hole can have — four characters, a column past Z on a row past 9 — on the
// bottom row at every size a review has to work in. The row gives up "not
// proven" before the badge, and the badge only when the hole alone is all that
// fits, so wherever the hole and its badge fit both are there whole, and
// nothing on the row is a piece of either. At 20x8 the row once read "AA10…":
// the brief was cut to exactly the width, the frame pulled the cut back to the
// last whole word, and the badge went with it, though "AA10 placement-only" is
// nineteen columns.
func TestReplayAnalysisStatusKeepsTheBadgeBesideALongHole(t *testing.T) {
	rs := game.Std
	rs.Size = 30
	hole := game.Point{Col: 26, Row: 9}
	name := hole.String()
	if len(name) != 4 || hole.Col >= rs.Size || hole.Row >= rs.Size {
		t.Fatalf("%s is not a four-character hole of a %d-hole board", name, rs.Size)
	}
	if !slices.Contains(shellSizes, [2]int{20, 8}) {
		t.Fatal("the sizes no longer include 20x8, where a cut brief filled the row exactly")
	}
	g := game.MustNew(rs)
	for _, move := range []string{"D4", "F5"} {
		if err := g.PlayNotation(move); err != nil {
			t.Fatalf("playing %s: %v", move, err)
		}
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	sv := gamestore.Saved{
		ID: "replay-wide", Kind: gamestore.Imported, Player: "Vertical",
		Opponent: "Horizontal", Side: "vertical", Record: record.Encode(),
	}
	pair := name + " placement-only"
	whole := pair + " · not proven"

	for _, size := range shellSizes {
		w, ht := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, ht), func(t *testing.T) {
			eng := newRPAEngine()
			h := rpaOpen(t, shellTestDeps(t), sv, eng.analyse)
			h.feed(tea.WindowSizeMsg{Width: w, Height: ht})
			h.press("?")
			move := hole
			eng.next(t).answer <- rpaAnswer{result: bot.AnalysisResult{
				Policy:      bot.PlacementOnlyPolicy(),
				Recommended: &move,
				Candidates:  []bot.AnalysisCandidate{{Move: move, Score: 120, Bound: bot.BoundExact}},
				Headline:    "the headline the engine wrote",
				Highlight:   []game.Point{move},
				Stats:       &bot.SearchStats{Nodes: 4321, Depth: 3, Elapsed: 1500 * time.Millisecond, StopReason: "time"},
			}}
			h.waitFor("the analysis", func() bool { return h.answered == 1 })

			frame := h.frame()
			shellAssertFits(t, "replay with a four-character hole", frame, w, ht)
			lines := strings.Split(frame, "\n")
			bottom := lines[len(lines)-1]
			if !strings.Contains(bottom, name) {
				t.Fatalf("at %dx%d the bottom row %q lost the hole", w, ht, bottom)
			}
			if ansi.StringWidth(pair) <= w && !strings.Contains(bottom, pair) {
				t.Fatalf("at %dx%d the bottom row %q lost the badge, though %q fits", w, ht, bottom, pair)
			}
			if ansi.StringWidth(whole) <= w && !strings.Contains(bottom, whole) {
				t.Fatalf("at %dx%d the bottom row %q gave up the caveat, though %q fits", w, ht, bottom, whole)
			}
			if strings.Contains(bottom, ellipsis) {
				t.Fatalf("at %dx%d the bottom row %q was cut rather than giving up whole items", w, ht, bottom)
			}
		})
	}
}

// TestReplayAnalysisTakesOnlyTheAnswerItIsWaitingFor holds two searches at
// once. The player asks, moves on or stops the search, and asks again; the
// first search answers while the second is still running. That answer is for
// a search the screen is no longer waiting on, and taking it would do three
// wrong things at once: cancel the search that is running, stop waiting for
// it, and show a reading of another position — or an earlier reading of this
// one — as its result. A replay closed and opened again is the same case: the
// old screen's search answers into the new one, which is waiting on a search
// of the very same entry. Only the second search's own answer is shown.
func TestReplayAnalysisTakesOnlyTheAnswerItIsWaitingFor(t *testing.T) {
	// The second search answers with a different hole, so what is on screen at
	// the end says which search it came from.
	second := rpaFixture()
	upper := rpaUpper
	second.Recommended = &upper
	second.Highlight = []game.Point{rpaUpper}

	// waiting is the screen still running the second search, untouched, after
	// the first one's answer has been delivered to it.
	waiting := func(t *testing.T, h *rpaHarness, current *rpaCall) {
		t.Helper()
		if !h.s.analysis.running || h.s.analysis.done {
			t.Fatalf("an answer for another search ended the one running:\n%s", h.block())
		}
		if current.ctx.Err() != nil {
			t.Fatal("an answer for another search canceled the one running")
		}
		if frame := h.frame(); strings.Contains(frame, "engine's choice") || len(h.s.board.Highlights) != 0 {
			t.Fatalf("an answer for another search was shown:\n%s", frame)
		}
	}
	// shows is the second search's answer on screen, at the entry it read.
	shows := func(t *testing.T, h *rpaHarness, entry int) {
		t.Helper()
		if !h.s.analysis.shown(entry) {
			t.Fatalf("the second search's answer is not shown at step %d:\n%s", entry, h.block())
		}
		if !strings.Contains(h.block(), rpaUpper.String()+" is the engine's choice") {
			t.Fatalf("the screen shows another search's answer:\n%s", h.block())
		}
		h.frame()
		if want := []game.Point{rpaUpper}; !slices.Equal(h.s.board.Highlights, want) {
			t.Fatalf("the board marks %v, want the second search's %v", h.s.board.Highlights, want)
		}
	}

	for _, c := range []struct {
		name string
		// again asks for the second search while the first is unanswered, and
		// entry is where the second search reads.
		again func(h *rpaHarness)
		entry int
	}{
		{"after moving on", func(h *rpaHarness) { h.press("l"); h.press("?") }, 10},
		{"after stopping it", func(h *rpaHarness) { h.press("?"); h.press("?") }, 9},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, eng := rpaAt(t, 9)
			h.press("?")
			first := eng.next(t)
			c.again(h)
			current := eng.next(t)
			if h.s.step() != c.entry || !h.s.analysis.on(c.entry) || !h.s.analysis.running {
				t.Fatalf("the second search is not running at step %d", c.entry)
			}

			first.answer <- rpaAnswer{result: rpaFixture()}
			h.waitFor("the first search's answer", func() bool { return h.answered == 1 })
			waiting(t, h, current)

			current.answer <- rpaAnswer{result: second}
			h.waitFor("the second search's answer", func() bool { return h.answered == 2 })
			shows(t, h, c.entry)
		})
	}

	t.Run("in a replay opened again", func(t *testing.T) {
		old, eng := rpaAt(t, 9)
		old.press("?")
		first := eng.next(t)
		old.press("q")
		if first.ctx.Err() == nil {
			t.Fatal("leaving the replay left its search running")
		}

		h := rpaOpen(t, old.s.deps, rpConnectionRecord(t), eng.analyse)
		rpJump(t, h.s, "9")
		h.press("?")
		current := eng.next(t)

		// The old screen's search answers after all, and the program hands
		// what it answered to the screen that is up now.
		first.answer <- rpaAnswer{result: rpaFixture()}
		h.feed(old.outcome())
		if h.answered != 1 {
			t.Fatal("the old screen's answer was not delivered to the new one")
		}
		waiting(t, h, current)

		current.answer <- rpaAnswer{result: second}
		h.waitFor("the second search's answer", func() bool { return h.answered == 2 })
		shows(t, h, 9)
	})
}

// TestReplayAnalysisTicksOnlyWhileItsSearchRuns is the running indicator's
// chain. A tick for the search the screen is running asks for the next one; a
// tick for a search that has finished, been stopped with ?, been moved past or
// left, or been overtaken by a newer one asks for nothing. A chain that went on
// after its search would tick for as long as the program ran, and there would
// be one more such chain for every analysis the player had ever asked for.
func TestReplayAnalysisTicksOnlyWhileItsSearchRuns(t *testing.T) {
	// tick delivers a tick for job and returns what the screen asks for next.
	tick := func(h *rpaHarness, job *replayAnalysisJob) tea.Cmd {
		h.t.Helper()
		_, cmd := h.s.Update(replayAnalysisTickMsg{job: job, at: job.started.Add(time.Second)})
		return cmd
	}
	for _, c := range []struct {
		name string
		// after is what happens once the first search is running.
		after func(h *rpaHarness, eng *rpaEngine, call *rpaCall)
	}{
		{"finished", func(h *rpaHarness, _ *rpaEngine, call *rpaCall) {
			call.answer <- rpaAnswer{result: rpaFixture()}
			h.waitFor("the answer", func() bool { return h.answered == 1 })
		}},
		{"stopped with ?", func(h *rpaHarness, _ *rpaEngine, _ *rpaCall) { h.press("?") }},
		{"moved past", func(h *rpaHarness, _ *rpaEngine, _ *rpaCall) { h.press("l") }},
		{"left", func(h *rpaHarness, _ *rpaEngine, _ *rpaCall) { h.press("q") }},
		{"overtaken", func(h *rpaHarness, eng *rpaEngine, _ *rpaCall) {
			h.press("?")
			h.press("?")
			next := eng.next(h.t)
			if tick(h, h.s.analysis.job) == nil {
				h.t.Fatal("a tick for the newer search did not ask for the next one")
			}
			next.answer <- rpaAnswer{result: rpaFixture()}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, eng := rpaAt(t, 9)
			h.press("?")
			call := eng.next(t)
			job := h.s.analysis.job
			if tick(h, job) == nil {
				t.Fatal("a tick for the search that is running did not ask for the next one")
			}

			c.after(h, eng, call)
			if tick(h, job) != nil {
				t.Fatalf("a tick for a search that has been %s asked for another", c.name)
			}
			// The stand-in holds a search until it is answered. One answered
			// already has taken its answer off the channel, so this never
			// waits.
			call.answer <- rpaAnswer{result: rpaFixture()}
		})
	}
}
