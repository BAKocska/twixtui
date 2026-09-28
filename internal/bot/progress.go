package bot

import (
	"sync/atomic"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// SearchProgress is an immutable snapshot of an engine's current or last Move
// search.
//
// It exists for a screen that wants to show what the engine is doing while it
// thinks. Stats cannot answer that: it describes a search that has already
// happened, and a caller that serialises its bot behind the Move lock would
// wait out the whole search to read it. A snapshot is published whole at the
// start of a search, at the end of every deepening iteration that finished,
// and when Move returns, and it is never edited after that, so reading one
// costs a load and a copy and never waits on the search.
//
// What it reports is completed work only. An iteration still running has
// scored some root moves at the new depth and the rest at the old one, so its
// best move and its counters describe nothing in particular; a snapshot keeps
// showing the last iteration that finished until the next one does.
type SearchProgress struct {
	// Generation counts the Move searches this engine has started, so a
	// reader can tell a new search from the one it looked at last. Zero means
	// none has started yet.
	Generation uint64
	// Started is when that search began.
	Started time.Time
	// Running is true from the moment the search starts until Move returns.
	Running bool
	// Completed says at least one deepening iteration finished, or a winning
	// hole was taken without searching. Depth, Recommended and Stats mean
	// something only when it is true: before then the only answer the search
	// has is the ordering heuristic's guess, and that is not a search.
	Completed bool
	// Depth is the deepest completed iteration, and zero for a win taken
	// without searching.
	Depth int
	// Recommended is the best move of the last completed iteration. It is the
	// search's own choice, before the beginner tier samples among the
	// alternatives, so it need not be the move Move returns on that tier.
	Recommended game.Point
	// Stats is the work as of the last completed iteration while Running is
	// true, with an empty StopReason because the search has not stopped. Once
	// Running is false it is what StatsOf reports for the same search.
	Stats SearchStats
}

// ProgressOf reports b's latest search progress without blocking and without
// starting any work, or the zero SearchProgress for a bot that does not report
// it.
//
// Like StatsOf it is optional: an implementation that reports progress offers
// Progress() SearchProgress, which is what this asks for. Unlike StatsOf it is
// meant to be read from another goroutine while Move is running, so an
// implementation has to answer without waiting for the search.
func ProgressOf(b Bot) SearchProgress {
	if p, ok := b.(interface{ Progress() SearchProgress }); ok {
		return p.Progress()
	}
	return SearchProgress{}
}

// Progress reports the engine's latest Move search as it stood when it was
// last published, and the zero value before the first one. It takes no lock:
// the search stores a fresh snapshot at every publication and never touches
// one again, so the copy returned here is the caller's own. The hint search is
// left out of it for the same reason Stats leaves it out.
func (e *engine) Progress() SearchProgress {
	if p := e.progress.Load(); p != nil {
		return *p
	}
	return SearchProgress{}
}

// progressRun is the writing side of one Move search's progress. It lives on
// the goroutine running the search, which is the only one that edits cur; a
// reader only ever sees a copy of cur, stored whole.
type progressRun struct {
	dst *atomic.Pointer[SearchProgress]
	s   *searcher
	cur SearchProgress
}

// startProgress publishes the start of a Move search and hooks the play
// searcher so that every iteration it finishes is published as well. The hook
// is the one Analyze reports its iterations through, and it belongs to this
// search alone: finish takes it off again, so a search the engine did not
// start through Move reports nothing here.
func (e *engine) startProgress() *progressRun {
	r := &progressRun{
		dst: &e.progress,
		s:   e.play,
		cur: SearchProgress{
			Generation: e.Progress().Generation + 1,
			Started:    time.Now(),
			Running:    true,
		},
	}
	r.publish()
	r.s.onDepth = r.iteration
	return r
}

// iteration publishes a finished iteration. The searcher only records its
// elapsed time when it stops, so the boundary's own figure is the one that
// means anything here, and the stop reason is left empty because the search
// has not stopped: a reader sees the work that iteration finished with and
// nothing the next one has spent since.
func (r *progressRun) iteration(res *rootResult, elapsed time.Duration) {
	st := r.s.stats()
	st.Elapsed, st.StopReason = elapsed, ""
	r.cur.Completed = true
	r.cur.Depth = res.depth
	r.cur.Recommended = res.best
	r.cur.Stats = st
	r.publish()
}

// immediate records a winning hole taken without searching. That is not an
// iteration, but it is a finished answer rather than a guess, so it counts as
// completed at depth zero. It is published with the rest of the ending.
func (r *progressRun) immediate(hole game.Point) {
	r.cur.Completed = true
	r.cur.Depth = 0
	r.cur.Recommended = hole
}

// finish takes the hook off the play searcher and publishes the search as
// ended, with the same figures StatsOf now reports for it. Whether it counts
// as completed is whatever the iterations, or an immediate win, already said.
func (r *progressRun) finish() {
	r.s.onDepth = nil
	r.cur.Running = false
	r.cur.Stats = r.s.stats()
	r.publish()
}

// publish stores a copy of the snapshot as it now stands. The copy is what
// makes the published value immutable: cur goes on changing, and a reader
// holding an earlier publication must not see it do so.
func (r *progressRun) publish() {
	snap := r.cur
	r.dst.Store(&snap)
}
