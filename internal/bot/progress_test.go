package bot

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// pacedContext paces a Move search against a progress poller on another
// goroutine.
//
// The search calls Err at its work boundaries, on its own goroutine, and the
// first of those calls after an iteration has been published waits until the
// poller acknowledges having seen that iteration. So the poller observes every
// iteration the search publishes while it runs, however the scheduler treats
// the two goroutines, and what a test asserts about those observations does not
// depend on timing luck. The wait is bounded: a poller that never looks slows
// the search down and marks the context stalled rather than hanging the test.
//
// Its fields belong to the goroutine running Move, which has to be the test's
// own; the poller only ever touches ack.
type pacedContext struct {
	context.Context
	b       Bot
	ack     chan int
	acked   int
	stalled bool
}

func newPacedContext(parent context.Context, b Bot) *pacedContext {
	return &pacedContext{Context: parent, b: b, ack: make(chan int)}
}

func (c *pacedContext) Err() error {
	if p := ProgressOf(c.b); p.Running && p.Completed && p.Depth > c.acked {
		select {
		case c.acked = <-c.ack:
		case <-time.After(10 * time.Second):
			c.acked, c.stalled = p.Depth, true
		}
	}
	return c.Context.Err()
}

// progressPoller reads ProgressOf on a goroutine of its own, the way a screen
// polls a bot that is thinking on another, and keeps every snapshot that
// differs from the one before it. A running snapshot with a completed iteration
// it has not acknowledged yet is handed to onCompleted, if set, and then
// acknowledged to the pacedContext the search runs under.
type progressPoller struct {
	done chan struct{}
	out  chan []SearchProgress
}

func startProgressPoller(b Bot, ack chan<- int, onCompleted func(SearchProgress)) *progressPoller {
	pp := &progressPoller{done: make(chan struct{}), out: make(chan []SearchProgress, 1)}
	go func() {
		var seen []SearchProgress
		keep := func(p SearchProgress) {
			if len(seen) == 0 || p != seen[len(seen)-1] {
				seen = append(seen, p)
			}
		}
		acked := 0
		for {
			select {
			case <-pp.done:
				// One more read after Move has returned, so that the
				// snapshot it left behind is the last one in the list.
				keep(ProgressOf(b))
				pp.out <- seen
				return
			default:
			}
			p := ProgressOf(b)
			keep(p)
			if p.Running && p.Completed && p.Depth > acked {
				if onCompleted != nil {
					onCompleted(p)
				}
				select {
				case ack <- p.Depth:
					acked = p.Depth
				case <-pp.done:
				}
			}
			runtime.Gosched()
		}
	}()
	return pp
}

// stop ends the poll and returns what it saw, in the order it saw it.
func (pp *progressPoller) stop() []SearchProgress {
	close(pp.done)
	return <-pp.out
}

// TestProgressIsZeroUntilASearchStarts holds the zero value to meaning that no
// search has started: a bot with no search has nothing to report, an engine
// that has not searched has nothing yet, and a request the engine refuses
// before searching is not a search.
func TestProgressIsZeroUntilASearchStarts(t *testing.T) {
	if got := ProgressOf(scriptedBot{}); got != (SearchProgress{}) {
		t.Errorf("a bot with no search reports %+v, want the zero value", got)
	}
	b := New(Max, 1)
	if got := ProgressOf(b); got != (SearchProgress{}) {
		t.Errorf("an engine that has not searched reports %+v, want the zero value", got)
	}
	over := game.MustNew(smallRules(8))
	if err := over.Resign(game.Vertical); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if _, err := b.Move(context.Background(), over); err == nil {
		t.Fatal("Move accepted a finished game")
	}
	if got := ProgressOf(b); got != (SearchProgress{}) {
		t.Errorf("a request refused before searching published %+v, want the zero value", got)
	}
}

// TestProgressAfterACompletedMove checks the snapshot a finished search leaves
// behind: not running, the first search, completed to its depth ceiling, with
// the counters StatsOf reports and the move the search chose.
func TestProgressAfterACompletedMove(t *testing.T) {
	const depth = 2
	b, err := NewWithLimits(Max, 1, Limits{Depth: depth, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	g := quietPosition(t)
	before := time.Now()
	move, err := b.Move(context.Background(), g)
	after := time.Now()
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	p := ProgressOf(b)
	if p.Running {
		t.Errorf("Move has returned and the snapshot still says it is running: %+v", p)
	}
	if p.Generation != 1 {
		t.Errorf("the first search is generation %d, want 1", p.Generation)
	}
	if !p.Completed || p.Depth != depth {
		t.Fatalf("the snapshot reports completed=%v at depth %d, want the %d-ply ceiling reached: %+v", p.Completed, p.Depth, depth, p)
	}
	if st := StatsOf(b); p.Stats != st {
		t.Errorf("the snapshot reports %+v and StatsOf %+v for the same search", p.Stats, st)
	}
	if p.Stats.StopReason != stopDepth || p.Stats.Depth != p.Depth {
		t.Errorf("the snapshot's counters say %+v, want the search ended on its ceiling at depth %d", p.Stats, p.Depth)
	}
	// The max tier plays the move its search found, so the recommendation and
	// the move are the same hole.
	if p.Recommended != move {
		t.Errorf("the snapshot recommends %v and Move played %v", p.Recommended, move)
	}
	if p.Started.Before(before) || p.Started.After(after) {
		t.Errorf("the search is dated %v, outside the call that ran it (%v to %v)", p.Started, before, after)
	}
}

// TestProgressOfAnImmediateWin covers the one answer that is finished without
// an iteration: a winning hole taken at the root counts as completed at depth
// zero and recommends the hole.
func TestProgressOfAnImmediateWin(t *testing.T) {
	b, err := NewWithLimits(Max, 1, Limits{Depth: 2, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	g := winThreat(t)
	move, err := b.Move(context.Background(), g)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	probe := g.Clone()
	res, err := probe.PlayPeg(move)
	if err != nil || res.Winner() != g.Turn() {
		t.Fatalf("control: Move played %v, which does not win at once (%v)\n%s", move, err, g)
	}
	p := ProgressOf(b)
	if p.Running || p.Generation != 1 {
		t.Errorf("the snapshot reports running=%v in generation %d, want a finished first search", p.Running, p.Generation)
	}
	if !p.Completed || p.Depth != 0 || p.Stats.StopReason != stopImmediate {
		t.Errorf("the snapshot reports completed=%v at depth %d stopped for %q, want an immediate win completed at depth 0",
			p.Completed, p.Depth, p.Stats.StopReason)
	}
	if p.Recommended != move {
		t.Errorf("the snapshot recommends %v, want the winning hole %v", p.Recommended, move)
	}
	if st := StatsOf(b); p.Stats != st {
		t.Errorf("the snapshot reports %+v and StatsOf %+v for the same search", p.Stats, st)
	}
}

// TestProgressGenerationCountsMoveSearchesOnly holds the generation to Move
// searches. A hint and an analysis on the engine's own hint searcher are
// searches too, and the hint here is given a deeper ceiling than the move, so
// one that published would show as a depth the move never reached as well as
// in the generation.
func TestProgressGenerationCountsMoveSearchesOnly(t *testing.T) {
	ctx := context.Background()
	b, err := NewWithLimits(Max, 1, Limits{Depth: 1, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	e := b.(*engine)
	hs := e.hintSearcher()
	hs.p.maxDepth, hs.p.budget = 2, time.Hour
	g := quietPosition(t)

	if _, err := b.Move(ctx, g); err != nil {
		t.Fatalf("Move: %v", err)
	}
	first := ProgressOf(b)
	if first.Generation != 1 || first.Running || !first.Completed || first.Depth != 1 {
		t.Fatalf("the first search reports %+v, want generation 1 finished at depth 1", first)
	}

	if _, err := b.Hint(ctx, g); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if got := ProgressOf(b); got != first {
		t.Errorf("a hint changed the move's progress from %+v to %+v", first, got)
	}
	reports := 0
	if _, _, _, err := analyzeOn(ctx, hs, g, func(AnalysisProgress) { reports++ }); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if reports == 0 {
		t.Fatal("control: the analysis reported no finished iteration, so it could not have published one")
	}
	if got := ProgressOf(b); got != first {
		t.Errorf("an analysis on the engine changed the move's progress from %+v to %+v", first, got)
	}

	if _, err := b.Move(ctx, g); err != nil {
		t.Fatalf("second Move: %v", err)
	}
	second := ProgressOf(b)
	if second.Generation != 2 {
		t.Errorf("the second search is generation %d, want 2", second.Generation)
	}
	if second.Running || !second.Completed || second.Depth != 1 {
		t.Errorf("the second search reports %+v, want it finished at depth 1", second)
	}
	if second.Started.Before(first.Started) {
		t.Errorf("the second search is dated %v, before the first at %v", second.Started, first.Started)
	}
	if st := StatsOf(b); second.Stats != st {
		t.Errorf("the snapshot reports %+v and StatsOf %+v for the same search", second.Stats, st)
	}
}

// TestProgressOfASearchCanceledBeforeItsFirstIteration: a search stopped
// before any iteration finished answers from the ordering heuristic, and that
// guess must not be presented as a completed iteration — nor may the previous
// search's completion be carried into this one.
func TestProgressOfASearchCanceledBeforeItsFirstIteration(t *testing.T) {
	b, err := NewWithLimits(Max, 1, Limits{Depth: 1, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	g := quietPosition(t)
	if _, err := b.Move(context.Background(), g); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if p := ProgressOf(b); !p.Completed || p.Depth != 1 {
		t.Fatalf("control: the first search reports %+v, want it finished at depth 1", p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	move, err := b.Move(ctx, g)
	if err != nil {
		t.Fatalf("Move on a cancelled context: %v", err)
	}
	if err := g.CanPlace(g.Turn(), move); err != nil {
		t.Fatalf("Move on a cancelled context returned illegal %v: %v", move, err)
	}
	p := ProgressOf(b)
	if p.Generation != 2 || p.Running {
		t.Errorf("the cancelled search reports generation %d running=%v, want generation 2 finished", p.Generation, p.Running)
	}
	if p.Completed || p.Depth != 0 {
		t.Errorf("a search cancelled before its first iteration reports completed=%v at depth %d: %+v", p.Completed, p.Depth, p)
	}
	if p.Stats.StopReason != stopCanceled || p.Stats.Depth != 0 {
		t.Errorf("the cancelled search's counters say %+v, want it cancelled at depth 0", p.Stats)
	}
	if st := StatsOf(b); p.Stats != st {
		t.Errorf("the snapshot reports %+v and StatsOf %+v for the same search", p.Stats, st)
	}
}

// TestProgressOfASearchCanceledAfterAnIteration cancels from the poller's side
// once it sees a completed iteration, the way a "play now" key would, and holds
// the ending to that work: the search stops on what it had finished, the last
// snapshot published while it ran is what the final one reports, and the move
// is that iteration's.
func TestProgressOfASearchCanceledAfterAnIteration(t *testing.T) {
	// The clock is out of the way and the depth ceiling far off, so only the
	// cancellation ends the search. The guard only bounds the test if the
	// poller never gets to cancel.
	b, err := NewWithLimits(Max, 1, Limits{Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	g := quietPosition(t)
	guard, stopGuard := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopGuard()
	parent, cancel := context.WithCancel(guard)
	defer cancel()
	ctx := newPacedContext(parent, b)

	var canceledAt SearchProgress
	poller := startProgressPoller(b, ctx.ack, func(p SearchProgress) {
		if canceledAt.Generation == 0 {
			canceledAt = p
			cancel()
		}
	})
	move, err := b.Move(ctx, g)
	seen := poller.stop()
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := g.CanPlace(g.Turn(), move); err != nil {
		t.Fatalf("Move returned illegal %v after a cancellation: %v", move, err)
	}
	if ctx.stalled {
		t.Fatal("the poller stopped acknowledging iterations, so it did not see all of them")
	}
	if !canceledAt.Completed || canceledAt.Depth < 1 {
		t.Fatalf("the poller never saw a completed iteration to cancel after: %+v", seen)
	}

	final := ProgressOf(b)
	if final.Stats.StopReason != stopCanceled {
		t.Fatalf("the search stopped for %q, want the cancellation: %+v", final.Stats.StopReason, final)
	}
	if len(seen) == 0 || seen[len(seen)-1] != final {
		t.Fatalf("the poller's last snapshot is not the one Move left behind: %+v", seen)
	}
	var last SearchProgress
	for _, p := range seen {
		if p.Running && p.Completed {
			last = p
		}
	}
	if final.Running || !final.Completed {
		t.Errorf("the ended search reports running=%v completed=%v, want finished with an iteration behind it", final.Running, final.Completed)
	}
	if final.Depth != last.Depth || final.Recommended != last.Recommended {
		t.Errorf("the search ended at depth %d recommending %v, but the last iteration published while it ran was depth %d recommending %v",
			final.Depth, final.Recommended, last.Depth, last.Recommended)
	}
	if final.Depth < canceledAt.Depth {
		t.Errorf("the search ended at depth %d, shallower than the depth %d it was cancelled after", final.Depth, canceledAt.Depth)
	}
	if final.Stats.Depth != final.Depth {
		t.Errorf("the snapshot's depth is %d and its counters' %d", final.Depth, final.Stats.Depth)
	}
	if final.Stats.Nodes < last.Stats.Nodes {
		t.Errorf("the search ended with %d nodes, fewer than the %d its last iteration had", final.Stats.Nodes, last.Stats.Nodes)
	}
	if st := StatsOf(b); final.Stats != st {
		t.Errorf("the snapshot reports %+v and StatsOf %+v for the same search", final.Stats, st)
	}
	if move != final.Recommended {
		t.Errorf("Move played %v, not %v from the last completed iteration", move, final.Recommended)
	}
}

// TestProgressPolledWhileMoveRuns reads the progress from another goroutine for
// the whole of a search and holds what it sees to completed work: nothing goes
// backwards, a snapshot with no finished iteration carries no work, and each
// iteration's snapshot carries exactly that iteration's counters and move.
//
// The last is checked against the same search stopped at each shallower depth.
// Its first iterations do the same work whatever its ceiling, so counters read
// at any other moment than the end of the iteration would differ from theirs.
func TestProgressPolledWhileMoveRuns(t *testing.T) {
	const depth = 3
	ctx0 := context.Background()
	g := quietPosition(t)

	type reference struct {
		move  game.Point
		stats SearchStats
	}
	refs := map[int]reference{}
	for d := 1; d < depth; d++ {
		rb, err := NewWithLimits(Max, 1, Limits{Depth: d, Time: time.Hour})
		if err != nil {
			t.Fatalf("NewWithLimits: %v", err)
		}
		m, err := rb.Move(ctx0, g)
		if err != nil {
			t.Fatalf("reference Move at depth %d: %v", d, err)
		}
		st := StatsOf(rb)
		if st.StopReason != stopDepth || st.Depth != d {
			t.Fatalf("control: the reference at depth %d stopped with %+v", d, st)
		}
		refs[d] = reference{move: m, stats: st}
	}

	b, err := NewWithLimits(Max, 1, Limits{Depth: depth, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	guard, stopGuard := context.WithTimeout(ctx0, time.Minute)
	defer stopGuard()
	ctx := newPacedContext(guard, b)
	poller := startProgressPoller(b, ctx.ack, nil)
	move, err := b.Move(ctx, g)
	seen := poller.stop()
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if ctx.stalled {
		t.Fatal("the poller stopped acknowledging iterations, so it did not see all of them")
	}
	final := ProgressOf(b)
	if final.Stats.StopReason != stopDepth || final.Depth != depth {
		t.Fatalf("the search stopped with %+v, want it to reach its %d-ply ceiling", final.Stats, depth)
	}
	if len(seen) == 0 || seen[len(seen)-1] != final {
		t.Fatalf("the poller's last snapshot is not the one Move left behind: %+v", seen)
	}

	var prev SearchProgress
	var iterations []SearchProgress
	for i, p := range seen {
		if p.Generation < prev.Generation || p.Generation > 1 {
			t.Errorf("snapshot %d is generation %d after %d, for a single search", i, p.Generation, prev.Generation)
		}
		if p.Generation == 0 && p != (SearchProgress{}) {
			t.Errorf("snapshot %d names no search but carries %+v", i, p)
		}
		if p.Generation == 1 && prev.Generation == 1 {
			if p.Depth < prev.Depth {
				t.Errorf("snapshot %d went back from depth %d to %d", i, prev.Depth, p.Depth)
			}
			if prev.Completed && !p.Completed {
				t.Errorf("snapshot %d lost the completed iteration snapshot %d had", i, i-1)
			}
			if !prev.Running && p.Running {
				t.Errorf("snapshot %d is running after snapshot %d had ended", i, i-1)
			}
			if p.Running && p.Stats.Nodes < prev.Stats.Nodes {
				t.Errorf("snapshot %d reports %d nodes after %d", i, p.Stats.Nodes, prev.Stats.Nodes)
			}
		}
		if p.Running {
			if p.Stats.StopReason != "" {
				t.Errorf("running snapshot %d says the search stopped for %q", i, p.Stats.StopReason)
			}
			if !p.Completed && (p.Depth != 0 || p.Stats != (SearchStats{})) {
				t.Errorf("running snapshot %d has no finished iteration but reports depth %d and %+v", i, p.Depth, p.Stats)
			}
			if p.Completed {
				iterations = append(iterations, p)
			}
		}
		prev = p
	}

	// The pacing lets the poller see every iteration the search published.
	if len(iterations) != depth {
		t.Fatalf("the poller saw %d running iterations, want one for each of the %d depths: %+v", len(iterations), depth, iterations)
	}
	for i, p := range iterations {
		d := i + 1
		if p.Depth != d || p.Stats.Depth != d {
			t.Errorf("iteration %d is reported at depth %d with counters at depth %d", d, p.Depth, p.Stats.Depth)
		}
		want, wantMove := final.Stats, move
		if r, ok := refs[d]; ok {
			want, wantMove = r.stats, r.move
		}
		if p.Stats.Nodes != want.Nodes || p.Stats.Evaluations != want.Evaluations {
			t.Errorf("the depth-%d snapshot reports %d nodes and %d analyses, the search to that depth spent %d and %d",
				d, p.Stats.Nodes, p.Stats.Evaluations, want.Nodes, want.Evaluations)
		}
		if p.Recommended != wantMove {
			t.Errorf("the depth-%d snapshot recommends %v, the search to that depth chose %v", d, p.Recommended, wantMove)
		}
		if p.Stats.Elapsed > final.Stats.Elapsed {
			t.Errorf("the depth-%d snapshot is dated %v, past the %v the whole search took", d, p.Stats.Elapsed, final.Stats.Elapsed)
		}
	}
}

// TestProgressSnapshotsBelongToTheCaller: a snapshot is the caller's to edit,
// and editing it cannot reach what the engine reports next.
func TestProgressSnapshotsBelongToTheCaller(t *testing.T) {
	b, err := NewWithLimits(Max, 1, Limits{Depth: 1, Time: time.Hour})
	if err != nil {
		t.Fatalf("NewWithLimits: %v", err)
	}
	if _, err := b.Move(context.Background(), quietPosition(t)); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got := ProgressOf(b)
	want := got
	got.Generation, got.Started, got.Running, got.Completed, got.Depth = 99, time.Time{}, true, false, 99
	got.Recommended = game.Point{Col: -1, Row: -1}
	got.Stats.Nodes, got.Stats.Depth, got.Stats.StopReason = -1, 99, "edited"
	if got == want {
		t.Fatal("control: the edits left the copy as it was")
	}
	if again := ProgressOf(b); again != want {
		t.Errorf("editing a snapshot changed the next one from %+v to %+v", want, again)
	}
}
