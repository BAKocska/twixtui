package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/ui"
)

// --- a search the test runs by hand -----------------------------------------

// gpBot is an engine whose move search the test drives. It reports progress
// the way the real engine does, as whole snapshots behind a lock of its own
// that Move never holds while it waits, so a screen reading Progress can only
// be kept waiting by something between it and the engine. A cancelled context
// is answered with a legal move, the best of the last completed iteration or
// the fallback before one, which is what bot.Bot promises.
type gpBot struct {
	// hold, when set, keeps Move from starting its search until letStart,
	// which is a search holding the engine before it has begun.
	hold     chan struct{}
	holdOnce sync.Once
	// release hands Move the move of a search that ran its course.
	release chan game.Point
	// stubborn makes Move wait for release whatever its context says: a
	// search that outlives the screen which asked for it.
	stubborn bool
	// fallback is what a search cancelled before any completed iteration
	// plays, started the start its snapshots report, and final the work it
	// reports once Move has returned.
	fallback game.Point
	started  time.Time
	final    bot.SearchStats
	// entered is signalled each time a search begins.
	entered chan struct{}

	mu       sync.Mutex
	progress bot.SearchProgress
	ctxs     []context.Context
}

func newGPBot(t *testing.T) *gpBot {
	t.Helper()
	b := &gpBot{
		release:  make(chan game.Point, 4),
		entered:  make(chan struct{}, 4),
		fallback: game.Point{Col: 2, Row: 2},
		started:  time.Now(),
	}
	// A failed test must not leave a search parked for ever.
	t.Cleanup(func() {
		if b.hold != nil {
			b.letStart()
		}
		close(b.release)
	})
	return b
}

func (b *gpBot) Tier() bot.Tier { return bot.Max }

func (b *gpBot) Hint(context.Context, *game.Game) (bot.Hint, error) { return bot.Hint{}, nil }

func (b *gpBot) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	if b.hold != nil {
		<-b.hold
	}
	b.mu.Lock()
	b.ctxs = append(b.ctxs, ctx)
	b.progress = bot.SearchProgress{Generation: b.progress.Generation + 1, Started: b.started, Running: true}
	b.mu.Unlock()
	b.entered <- struct{}{}

	var move game.Point
	reason := "time"
	if b.stubborn {
		move = <-b.release
	} else {
		select {
		case move = <-b.release:
		case <-ctx.Done():
			reason = "canceled"
			move = b.fallback
			b.mu.Lock()
			if b.progress.Completed {
				move = b.progress.Recommended
			}
			b.mu.Unlock()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	final := b.final
	final.StopReason = reason
	b.progress.Running = false
	b.progress.Stats = final
	return move, nil
}

func (b *gpBot) Progress() bot.SearchProgress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.progress
}

// complete publishes a finished iteration, as the engine does at the end of
// each one.
func (b *gpBot) complete(depth int, nodes int64, best game.Point) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.progress.Completed = true
	b.progress.Depth = depth
	b.progress.Recommended = best
	b.progress.Stats = bot.SearchStats{Nodes: nodes, Evaluations: 2 * nodes, Depth: depth}
}

func (b *gpBot) letStart() { b.holdOnce.Do(func() { close(b.hold) }) }

func (b *gpBot) contexts() []context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]context.Context(nil), b.ctxs...)
}

// gpEntered waits for the next search to begin.
func gpEntered(t *testing.T, b *gpBot) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine was never asked for a move")
	}
}

// gpWithin runs f and fails if it has not returned in time. It is how a test
// requires that something does not wait for the search, without hanging when
// it does.
func gpWithin(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s waited for the engine's search", what)
	}
}

// gpTick delivers one tick fired at the given time. The tick it schedules in
// turn is dropped: the harness already has a tick running, and a second one
// would double the ticks without changing what they read.
func gpTick(t *testing.T, h *gsHarness, at time.Time) {
	t.Helper()
	gpWithin(t, "a spinner tick", func() { h.s.Update(botTickMsg{at: at}) })
}

// gpOffers reports whether the help panel offers a game action, as the
// action's own row in gameBindings describes it.
func gpOffers(h *gsHarness, a gameAction) bool {
	for _, b := range gameBindings {
		if b.action != a {
			continue
		}
		for _, e := range h.s.helpEntries() {
			if e.Label == b.label && e.Help == b.help {
				return true
			}
		}
	}
	return false
}

// gpAnswerFrom takes the next engine answer a screen's commands produced,
// without delivering it to that screen. The shell hands every message to the
// screen on top, so after a rematch the answer of a search the finished game
// left running goes to the rematch; routing it there is how the test
// reproduces that.
func gpAnswerFrom(t *testing.T, h *gsHarness) botMoveMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if m, ok := msg.(botMoveMsg); ok {
				return m
			}
		case <-deadline:
			t.Fatal("the finished game's search never answered")
			return botMoveMsg{}
		}
	}
}

// gpHeldSearch commits the staged turn by hand and returns the command for the
// search the commit asks for, without running it: a request the screen has
// made that has not yet reached the engine, which the test runs when it
// chooses. maybeBotMove batches the search ahead of the first spinner tick.
func gpHeldSearch(t *testing.T, h *gsHarness) tea.Cmd {
	t.Helper()
	_, cmd := h.s.Update(gsKeyMsg(t, "enter"))
	if !h.s.botThinking || cmd == nil {
		t.Fatalf("committing the turn did not ask the engine for a move: message %q", h.s.message)
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("committing the turn asked for %T, want the search and its first tick", msg)
	}
	return batch[0]
}

// gpDepthEitherForm matches a completed depth in either of the thinking line's
// forms: the panel's "depth 2 done" or the panel-less status line's "d2".
var gpDepthEitherForm = regexp.MustCompile(`\bd(?:epth )?(\d+)\b`)

// gpClaimsDepth reports whether a frame claims depth as completed, in either
// form, anywhere on it.
func gpClaimsDepth(frame string, depth int) bool {
	for _, m := range gpDepthEitherForm.FindAllStringSubmatch(frame, -1) {
		if m[1] == fmt.Sprint(depth) {
			return true
		}
	}
	return false
}

// gpDepthClaim matches any claim of a completed depth, a depth of nought
// included.
var gpDepthClaim = regexp.MustCompile(`\bdepth \d`)

// gpCompactDepth2 is the completed depth 2 in the short form a status line
// with no panel gives it.
var gpCompactDepth2 = regexp.MustCompile(`\bd2\b`)

// gpBottom is the bottom row of a composed frame, where ui.Compose pins the
// status line, as the player reads it.
func gpBottom(frame string) string {
	lines := strings.Split(frame, "\n")
	return ansi.Strip(lines[len(lines)-1])
}

// --- the thinking line --------------------------------------------------------

// TestTheThinkingLineShowsOnlyThisSearchsFinishedWork reads the engine while
// its search holds the serialiser's lock. Any read that took that lock would
// hang the tick, which gpTick turns into a failure. It then requires the line
// to ignore the engine's account of an earlier search, to claim no depth
// before an iteration has finished, and to show a finished iteration's depth
// and nodes with the time measured from the engine's own start.
func TestTheThinkingLineShowsOnlyThisSearchsFinishedWork(t *testing.T) {
	engine := newGPBot(t)
	// The search starts a minute after the screen asks for it, as one queued
	// behind another search would, so a time measured from the screen's own
	// request is a minute out.
	engine.started = time.Now().Add(time.Minute)
	// The engine still carries the final account of an earlier search, as the
	// engine a rematch inherits does.
	engine.progress = bot.SearchProgress{
		Generation: 7, Started: engine.started.Add(-time.Hour), Completed: true, Depth: 9,
		Stats: bot.SearchStats{Nodes: 987654, Depth: 9, StopReason: "time"},
	}
	engine.hold = make(chan struct{})
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if !h.s.botThinking {
		t.Fatal("the engine is not thinking, so there is no line to read")
	}
	gpTick(t, h, time.Now())
	if frame := h.frame(); strings.Contains(frame, "987654") || gpDepthClaim.MatchString(frame) {
		t.Fatalf("the thinking line shows an earlier search's work as this one's:\n%s", frame)
	}
	engine.letStart()
	gpEntered(t, engine)
	gpTick(t, h, time.Now())
	if frame := h.frame(); gpDepthClaim.MatchString(frame) || strings.Contains(frame, "987654") {
		t.Fatalf("the thinking line claims a depth before any iteration finished:\n%s", frame)
	}

	engine.complete(2, 4321, game.Point{Col: 4, Row: 2})
	gpTick(t, h, engine.started.Add(2500*time.Millisecond))
	frame := h.frame()
	// Whole numbers, so that a time measured from the screen's request, 62.5s,
	// cannot pass for the 2.5s since the engine's start.
	for _, want := range []string{`\bdepth 2\b`, `\b4321 nodes\b`, `\b2\.5s\b`} {
		if !regexp.MustCompile(want).MatchString(frame) {
			t.Errorf("the thinking line does not match %s once an iteration has finished:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "987654") {
		t.Errorf("the earlier search's nodes came back:\n%s", frame)
	}
	gsCheckFrame(t, "thinking with progress", frame, h.width, h.height)

	engine.release <- game.Point{Col: 5, Row: 1}
	h.waitFor("the engine's move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})
	if h.s.searchProgressText() != "" || strings.Contains(h.frame(), "4321") {
		t.Errorf("the finished search is still shown as running:\n%s", h.frame())
	}
}

// TestTheThinkingLineKeepsToCompletedIterations lets the search end, and the
// engine publish its final account, before the screen has handled the move,
// with a tick reading the engine in between. The final account's nodes are the
// whole search's, the iteration it was part way through included, so shown
// beside the completed depth they would pass that unfinished work off as the
// completed iteration's. They belong to the finished figures alone.
func TestTheThinkingLineKeepsToCompletedIterations(t *testing.T) {
	engine := newGPBot(t)
	engine.final = bot.SearchStats{Nodes: 250, Evaluations: 400, Depth: 2, Elapsed: 3 * time.Second}
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)
	h.playTurn(game.Point{Col: 1, Row: 0})
	gpEntered(t, engine)

	best := game.Point{Col: 4, Row: 2}
	engine.complete(2, 100, best)
	gpTick(t, h, engine.started.Add(time.Second))
	h.mustContain("the completed iteration's work", "100 nodes")

	engine.release <- best
	answer := gpAnswerFrom(t, h)
	gpTick(t, h, engine.started.Add(3*time.Second))
	frame := h.frame()
	if regexp.MustCompile(`\b250\b`).MatchString(frame) {
		t.Fatalf("the thinking line shows the whole search's nodes beside the completed depth:\n%s", frame)
	}
	for _, want := range []string{`\bdepth 2 done\b`, `\b100 nodes\b`} {
		if !regexp.MustCompile(want).MatchString(frame) {
			t.Errorf("the thinking line lost the completed iteration's %s once the search had ended:\n%s", want, frame)
		}
	}

	h.feed(answer)
	if h.s.g.At(best) != game.Horizontal {
		t.Fatalf("the search's move %v was not played:\n%s", best, h.s.g)
	}
	h.press("i")
	if msg := h.s.message; !regexp.MustCompile(`\b250 nodes\b`).MatchString(msg) || !regexp.MustCompile(`\bdepth 2\b`).MatchString(msg) {
		t.Errorf("the finished figures %q do not give the whole search's nodes with its completed depth", msg)
	}
}

// TestAWinTakenWithoutSearchingClaimsNoDepth hands the thinking line a running
// snapshot that has completed at depth nought, which is how the engine counts
// a winning hole it took without searching. The engine only publishes that as
// its search ends, and the line leaves final snapshots out, so the snapshot is
// delivered running here to reach the line's own rule: there is no iteration
// behind it, and no layout may present it as a completed depth.
func TestAWinTakenWithoutSearchingClaimsNoDepth(t *testing.T) {
	for _, size := range shellSizes {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			engine := newGPBot(t)
			h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), size[0], size[1])
			h.playTurn(game.Point{Col: 1, Row: 0})
			gpEntered(t, engine)
			// The commit's own line answers the key that made it, and holds
			// the status line until the next key.
			h.press("j")

			engine.complete(0, 0, game.Point{Col: 4, Row: 2})
			gpTick(t, h, engine.started.Add(time.Second))
			frame := h.frame()
			if gpClaimsDepth(frame, 0) {
				t.Errorf("the thinking line presents a win taken without searching as depth nought:\n%s", frame)
			}
			if !strings.Contains(frame, "1.0s") {
				t.Errorf("the thinking line lost the search's time:\n%s", frame)
			}
		})
	}
}

// TestPlayNowAndTheSearchShowAtEverySize keeps the key that cuts a search short,
// and how far the search has got, on screen at every supported size. Without a
// panel the status line is the only place for either, and the headline used to
// lead it and fill the minimum width by itself.
func TestPlayNowAndTheSearchShowAtEverySize(t *testing.T) {
	for _, size := range shellSizes {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			engine := newGPBot(t)
			h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), size[0], size[1])
			h.playTurn(game.Point{Col: 1, Row: 0})
			gpEntered(t, engine)
			// The commit's own line answers the key that made it, and where
			// there is a panel it holds the status line until the next key.
			// TestPlayNowLeadsTheMovesAnnouncementWithoutAPanel looks before
			// any such key.
			h.press("j")
			key := h.s.gameKeyLabel(gaPlayNow) + " play now"

			gpTick(t, h, engine.started.Add(1500*time.Millisecond))
			frame := h.frame()
			if !strings.Contains(frame, key) || !strings.Contains(frame, "1.5s") {
				t.Errorf("before any iteration, the frame does not show %q and the search's time:\n%s", key, frame)
			}

			engine.complete(2, 4321, game.Point{Col: 4, Row: 2})
			gpTick(t, h, engine.started.Add(2500*time.Millisecond))
			frame = h.frame()
			if !strings.Contains(frame, key) {
				t.Errorf("the frame does not offer %q while the engine thinks:\n%s", key, frame)
			}
			if !gpClaimsDepth(frame, 2) {
				t.Errorf("the frame does not show the completed depth 2:\n%s", frame)
			}
			gsCheckFrame(t, "thinking", frame, h.width, h.height)
		})
	}
}

// TestPlayNowLeadsTheMovesAnnouncementWithoutAPanel commits a move from the
// keyboard and then only lets the search run. Nothing else is pressed, so the
// commit's announcement stays the status line's message for the whole search,
// as it does for a player who sits back and waits for the engine. Without a
// panel that line is the only place for the play-now key and the search, and
// the announcement used to take all of it: neither was on screen until some
// other key cleared the message. TestPlayNowAndTheSearchShowAtEverySize
// presses such a key before it looks, which is why it never saw this. With a
// panel the announcement keeps the line to itself, as it always has, and once
// the move arrives its own announcement takes the line at every size.
//
// Both boards are played because the layouts differ: at 40x12 the six-hole
// board leaves room for a panel below it and the full-size board does not.
// 70x12 is added to the supported sizes as a line with no panel beside the
// full-size board that is wide enough for the announcement to follow the key
// and the search whole, which is where it can be seen not to have been lost.
func TestPlayNowLeadsTheMovesAnnouncementWithoutAPanel(t *testing.T) {
	sizes := append(slices.Clone(shellSizes), [2]int{70, 12})
	var panelless, paneled, followed int
	for _, n := range []int{6, 24} {
		for _, size := range sizes {
			w, ht := size[0], size[1]
			panel := ui.Arrange(w, ht, n).Panel
			if panel == ui.PanelNone {
				panelless++
			} else {
				paneled++
			}
			t.Run(fmt.Sprintf("board %d at %dx%d", n, w, ht), func(t *testing.T) {
				engine := newGPBot(t)
				h := newGSHarness(t, gsTestDeps(t), gsVersusBot(n, engine), w, ht)
				h.playTurn(game.Point{Col: 1, Row: 0})
				gpEntered(t, engine)
				key := h.s.gameKeyLabel(gaPlayNow) + " play now"
				announced := h.s.toMoveText()

				gpTick(t, h, engine.started.Add(1500*time.Millisecond))
				bottom := gpBottom(h.frame())
				if panel == ui.PanelNone {
					if !strings.HasPrefix(bottom, key+" · 1.5s") {
						t.Errorf("with the move's announcement standing, the bottom row %q does not lead with %q and the search's time", bottom, key)
					}
				} else if bottom != announced {
					t.Errorf("with a panel, the bottom row %q is not the move's announcement %q alone", bottom, announced)
				}

				engine.complete(2, 4321, game.Point{Col: 4, Row: 2})
				gpTick(t, h, engine.started.Add(2500*time.Millisecond))
				frame := h.frame()
				bottom = gpBottom(frame)
				if panel == ui.PanelNone {
					if !strings.HasPrefix(bottom, key) || !gpCompactDepth2.MatchString(bottom) {
						t.Errorf("with the move's announcement standing, the bottom row %q does not lead with %q and the completed depth d2", bottom, key)
					}
					if whole := key + " · 2.5s d2 · " + announced; ansi.StringWidth(whole) <= w {
						if bottom != whole {
							t.Errorf("the bottom row %q has room for the announcement after the key and the search, want %q", bottom, whole)
						}
						followed++
					}
				} else if bottom != announced {
					t.Errorf("with a panel, the bottom row %q is not the move's announcement %q alone", bottom, announced)
				}
				gsCheckFrame(t, "thinking with the announcement standing", frame, w, ht)

				move := game.Point{Col: 4, Row: 2}
				engine.release <- move
				h.waitFor("the engine's move", func() bool { return h.s.g.At(move) == game.Horizontal })
				bottom = gpBottom(h.frame())
				if strings.Contains(bottom, key) || !strings.Contains(bottom, "played "+move.String()) {
					t.Errorf("once the move has arrived, the bottom row %q is not its announcement", bottom)
				}
			})
		}
	}
	if panelless == 0 || paneled == 0 || followed == 0 {
		t.Fatalf("the sizes covered %d layouts with no panel, %d with one, and %d with room for the announcement after the search; each needs one",
			panelless, paneled, followed)
	}
}

// --- play now -----------------------------------------------------------------

// TestPlayNowPlaysTheSearchsOwnMoveOnce presses escape during a search that has
// finished an iteration. The keyboard has to answer while the search holds the
// engine, the search's own context has to be cancelled, and the move it then
// returns has to be played exactly once, under the generation it was asked
// under. The figures shown afterwards have to be the engine's final account,
// not the last snapshot the thinking line showed.
func TestPlayNowPlaysTheSearchsOwnMoveOnce(t *testing.T) {
	engine := newGPBot(t)
	engine.final = bot.SearchStats{Nodes: 5000, Evaluations: 9000, Depth: 3, Elapsed: 1500 * time.Millisecond}
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)

	if gpOffers(h, gaPlayNow) {
		t.Error("play now is offered before the engine has anything to play")
	}
	h.playTurn(game.Point{Col: 1, Row: 0})
	gpEntered(t, engine)

	key := gsKeyMsg(t, "j")
	before := h.s.board.Cursor
	gpWithin(t, "a key", func() { h.s.Update(key) })
	if h.s.board.Cursor == before {
		t.Error("the cursor did not move while the search held the engine")
	}

	best := game.Point{Col: 4, Row: 2}
	engine.complete(3, 4321, best)
	gpTick(t, h, time.Now())
	h.mustContain("the completed depth", "depth 3")
	if !gpOffers(h, gaPlayNow) {
		t.Fatal("the help does not offer play now while the engine thinks")
	}
	if footer := gsStatusLine(h); !strings.Contains(footer, h.s.gameKeyLabel(gaPlayNow)) {
		t.Errorf("the footer does not name the play-now key while the engine thinks: %q", footer)
	}

	gen := h.s.botGen
	h.press("esc")
	ctxs := engine.contexts()
	if len(ctxs) != 1 {
		t.Fatalf("the engine was asked for %d moves, want 1", len(ctxs))
	}
	select {
	case <-ctxs[0].Done():
	case <-time.After(2 * time.Second):
		t.Fatal("play now did not cancel the search")
	}
	h.waitFor("the search's move", func() bool { return h.s.g.At(best) == game.Horizontal })
	h.pump()

	if h.s.botGen != gen {
		t.Errorf("play now moved the search generation from %d to %d", gen, h.s.botGen)
	}
	if got := h.s.g.Entries(); got != 2 {
		t.Fatalf("the game holds %d entries after one move each, want 2:\n%s", got, h.s.g)
	}
	if h.s.g.Turn() != game.Vertical || h.s.botThinking {
		t.Errorf("after play now it is %v to move, thinking %v; want the player to move", h.s.g.Turn(), h.s.botThinking)
	}
	if n := len(engine.contexts()); n != 1 {
		t.Errorf("the engine was asked for %d moves, want 1", n)
	}
	if msg := h.s.message; !strings.Contains(msg, best.String()) || !regexp.MustCompile(`\bdepth 3\b`).MatchString(msg) {
		t.Errorf("the move's line %q does not name the move and the depth it was completed at", msg)
	}
	// Any key clears the move's line from the footer, which then shows the
	// keys that work now.
	h.press("j")
	if gpOffers(h, gaPlayNow) || strings.Contains(gsStatusLine(h), h.s.gameKeyLabel(gaPlayNow)) {
		t.Errorf("play now is still offered with no search to cut short: footer %q", gsStatusLine(h))
	}

	if !gpOffers(h, gaSearchInfo) {
		t.Fatal("the finished search's figures are not offered")
	}
	h.press("i")
	msg := h.s.message
	for _, want := range []string{best.String(), "5000", "1.5s", searchStopText("canceled")} {
		if !strings.Contains(msg, want) {
			t.Errorf("the search's figures %q do not include %q", msg, want)
		}
	}
	if !regexp.MustCompile(`\bdepth 3\b`).MatchString(msg) {
		t.Errorf("the search's figures %q do not give the depth it completed", msg)
	}
	if strings.Contains(msg, "4321") {
		t.Errorf("the search's figures %q are the last live snapshot rather than its final account", msg)
	}
}

// TestPlayNowBeforeAnIterationClaimsNoDepth cuts a search short before its
// first iteration completes: the engine's fallback is played, and nothing on
// screen presents a depth of nought as a completed one.
func TestPlayNowBeforeAnIterationClaimsNoDepth(t *testing.T) {
	engine := newGPBot(t)
	engine.final = bot.SearchStats{Nodes: 40, Evaluations: 60, Elapsed: 100 * time.Millisecond}
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)

	h.playTurn(game.Point{Col: 1, Row: 0})
	gpEntered(t, engine)
	gpTick(t, h, time.Now())
	if frame := h.frame(); gpDepthClaim.MatchString(frame) {
		t.Fatalf("the thinking line claims a depth before any iteration finished:\n%s", frame)
	}

	h.press("esc")
	h.waitFor("the fallback move", func() bool { return h.s.g.At(engine.fallback) == game.Horizontal })
	h.pump()
	if got := h.s.g.Entries(); got != 2 {
		t.Fatalf("the game holds %d entries after one move each, want 2", got)
	}
	if msg := h.s.message; gpDepthClaim.MatchString(msg) || !strings.Contains(msg, engine.fallback.String()) {
		t.Errorf("the move's line %q claims a depth, or does not name the move", msg)
	}
	h.press("i")
	if msg := h.s.message; gpDepthClaim.MatchString(msg) || !strings.Contains(msg, "40") {
		t.Errorf("the search's figures %q claim a depth, or lose its nodes", msg)
	}
}

// TestEscapeKeepsItsOtherMeanings holds the two things escape already did:
// answer no to a question, including one asked while the engine thinks, and
// leave link mode. Where neither applies and there is no search, it still does
// nothing at all.
func TestEscapeKeepsItsOtherMeanings(t *testing.T) {
	t.Run("a question asked while the engine thinks", func(t *testing.T) {
		engine := newGPBot(t)
		h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)
		h.playTurn(game.Point{Col: 1, Row: 0})
		gpEntered(t, engine)

		h.press("r")
		if h.s.confirm != gaResign {
			t.Fatalf("resign did not ask first: message %q", h.s.message)
		}
		h.press("esc")
		if h.s.confirm != gaNone || h.s.g.Result().Over() {
			t.Fatalf("escape did not answer no: confirm %v, result %v", h.s.confirm, h.s.g.Result())
		}
		select {
		case <-engine.contexts()[0].Done():
			t.Fatal("escape at a question also cut the search short")
		default:
		}
		if !h.s.botThinking || !gpOffers(h, gaPlayNow) {
			t.Error("the search, or the offer to cut it short, did not survive the question")
		}

		engine.release <- game.Point{Col: 5, Row: 1}
		h.waitFor("the engine's move", func() bool {
			return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
		})
	})

	t.Run("link mode", func(t *testing.T) {
		h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, newGPBot(t)), 120, 40)
		h.press("x")
		if !h.s.linkMode {
			t.Fatalf("link mode did not open: message %q", h.s.message)
		}
		h.press("esc")
		if h.s.linkMode {
			t.Error("escape no longer leaves link mode")
		}
	})

	t.Run("nothing to answer and nothing to hurry", func(t *testing.T) {
		engine := newGPBot(t)
		h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)
		before := h.frame()
		h.press("esc")
		if len(h.done) != 0 || h.s.g.Entries() != 0 || h.s.message != "" || len(engine.contexts()) != 0 {
			t.Errorf("escape on an idle board did something: departures %d, entries %d, message %q, searches %d",
				len(h.done), h.s.g.Entries(), h.s.message, len(engine.contexts()))
		}
		if after := h.frame(); after != before {
			t.Errorf("escape on an idle board changed the screen\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}

// --- leaving and rematches ----------------------------------------------------

// TestALateMoveAfterLeavingIsNotPlayed leaves while a search is running and has
// it answer afterwards, as a search slow to notice its cancellation does.
func TestALateMoveAfterLeavingIsNotPlayed(t *testing.T) {
	engine := newGPBot(t)
	engine.stubborn = true
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsVersusBot(6, engine), 120, 40)
	h.playTurn(game.Point{Col: 1, Row: 0})
	gpEntered(t, engine)

	h.press("q")
	if len(h.done) != 1 {
		t.Fatalf("leaving produced %d departures, want 1", len(h.done))
	}
	late := game.Point{Col: 5, Row: 1}
	engine.release <- late
	h.waitFor("the late answer to be delivered", func() bool {
		return h.inflight.Load() == 0 && len(h.msgs) == 0
	})
	if h.s.g.At(late) != game.NoPlayer || h.s.g.Entries() != 1 {
		t.Fatalf("the move of a search the player left was played:\n%s", h.s.g)
	}
	sv, err := d.Games.Get(h.s.storeID)
	if err != nil {
		t.Fatalf("the left game was not kept: %v", err)
	}
	saved, err := sv.Game()
	if err != nil {
		t.Fatalf("the left game does not replay: %v", err)
	}
	if saved.Entries() != 1 {
		t.Errorf("the stored game holds %d entries, want the player's one", saved.Entries())
	}
}

// TestARematchDoesNotPlayTheLastGamesMove resigns while the engine is still
// searching, starts a rematch at once, and then has the finished game's search
// answer. The shell delivers that answer to the rematch, which must not play
// it, whether or not the rematch is itself searching by then; the rematch's
// own search is then played once.
func TestARematchDoesNotPlayTheLastGamesMove(t *testing.T) {
	t.Run("the engine opens the rematch", func(t *testing.T) {
		engine := newGPBot(t)
		engine.stubborn = true
		d := gsTestDeps(t)
		h := newGSHarness(t, d, gsVersusBot(6, engine), 120, 40)
		h.playTurn(game.Point{Col: 1, Row: 0})
		gpEntered(t, engine)
		h.press("r")
		h.press("y")
		h.waitFor("the resignation", func() bool { return h.s.g.Result().Over() })

		rh := gsAdopt(t, gsRematchScreen(h), 120, 40)
		if !rh.s.botThinking {
			t.Fatal("the engine did not open the rematch")
		}
		stale := game.Point{Col: 2, Row: 2}
		engine.release <- stale
		rh.feed(gpAnswerFrom(t, h))
		if got := rh.s.g.Entries(); got != 0 {
			t.Fatalf("the rematch played the finished game's move %v:\n%s", stale, rh.s.g)
		}

		gpEntered(t, engine)
		fresh := game.Point{Col: 3, Row: 3}
		engine.release <- fresh
		rh.waitFor("the rematch's own move", func() bool { return rh.s.g.At(fresh) == game.Vertical })
		rh.pump()
		if got := rh.s.g.Entries(); got != 1 || rh.s.g.At(stale) != game.NoPlayer {
			t.Fatalf("the rematch holds %d entries after the engine's one move:\n%s", got, rh.s.g)
		}
		if rh.s.g.Turn() != game.Horizontal {
			t.Errorf("after the engine's opening it is %v to move, want the player", rh.s.g.Turn())
		}
	})

	t.Run("the player opens the rematch", func(t *testing.T) {
		engine := newGPBot(t)
		engine.stubborn = true
		d := gsTestDeps(t)
		cfg := GameConfig{
			Kind:  gamestore.VersusBot,
			Rules: gsRules(6),
			Seats: map[game.Player]Seat{
				game.Vertical:   {Bot: engine},
				game.Horizontal: {Profile: "ada"},
			},
		}
		h := newGSHarness(t, d, cfg, 120, 40)
		gpEntered(t, engine)
		opening := game.Point{Col: 2, Row: 2}
		engine.release <- opening
		h.waitFor("the engine's opening", func() bool { return h.s.g.At(opening) == game.Vertical })
		h.playTurn(game.Point{Col: 5, Row: 1})
		gpEntered(t, engine)
		h.press("r")
		h.press("y")
		h.waitFor("the resignation", func() bool { return h.s.g.Result().Over() })

		rh := gsAdopt(t, gsRematchScreen(h), 120, 40)
		if rh.s.botThinking || rh.s.g.Turn() != game.Vertical || !rh.s.mover().Human() {
			t.Fatal("the rematch does not open with the player to move")
		}
		stale := game.Point{Col: 3, Row: 3}
		engine.release <- stale
		rh.feed(gpAnswerFrom(t, h))
		if got := rh.s.g.Entries(); got != 0 || rh.s.g.At(stale) != game.NoPlayer {
			t.Fatalf("the rematch played the finished game's move %v for the player:\n%s", stale, rh.s.g)
		}

		rh.playTurn(game.Point{Col: 1, Row: 0})
		gpEntered(t, engine)
		fresh := game.Point{Col: 5, Row: 1}
		engine.release <- fresh
		rh.waitFor("the engine's reply", func() bool { return rh.s.g.At(fresh) == game.Horizontal })
		rh.pump()
		if got := rh.s.g.Entries(); got != 2 {
			t.Fatalf("the rematch holds %d entries after one move each, want 2:\n%s", got, rh.s.g)
		}
	})
}

// TestARematchShowsOnlyItsOwnSearch has two games' requests reach the one
// engine out of order. The finished game asked for a search that had not yet
// reached the engine when the player resigned; the rematch's search takes the
// engine first, and the finished game's takes it next, before the rematch has
// handled its own move. While that other search runs, and once it has ended,
// its snapshots are the engine's latest. The rematch has to go on showing its
// own search's completed work, and give its own search's final figures for
// the move it plays.
func TestARematchShowsOnlyItsOwnSearch(t *testing.T) {
	engine := newGPBot(t)
	engine.stubborn = true
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)
	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("space")
	queued := gpHeldSearch(t, h)
	h.press("r")
	h.press("y")
	h.waitFor("the resignation", func() bool { return h.s.g.Result().Over() })

	rh := gsAdopt(t, gsRematchScreen(h), 120, 40)
	if !rh.s.botThinking {
		t.Fatal("the engine did not open the rematch")
	}
	gpEntered(t, engine)
	own := game.Point{Col: 3, Row: 3}
	engine.complete(3, 3000, own)
	gpTick(t, rh, engine.started.Add(2*time.Second))
	rh.mustContain("the rematch's completed iteration", "3000 nodes")
	engine.final = bot.SearchStats{Nodes: 3500, Evaluations: 7000, Depth: 3, Elapsed: 2 * time.Second}
	engine.release <- own
	answer := gpAnswerFrom(t, rh)

	late := make(chan tea.Msg, 1)
	go func() { late <- queued() }()
	gpEntered(t, engine)
	other := game.Point{Col: 4, Row: 4}
	engine.complete(5, 99999, other)
	gpTick(t, rh, engine.started.Add(3*time.Second))
	frame := rh.frame()
	if strings.Contains(frame, "99999") || regexp.MustCompile(`\bdepth 5\b`).MatchString(frame) {
		t.Fatalf("the rematch shows the finished game's search as its own:\n%s", frame)
	}
	if !strings.Contains(frame, "3000 nodes") {
		t.Errorf("the rematch lost its own search's completed work:\n%s", frame)
	}
	engine.final = bot.SearchStats{Nodes: 88888, Evaluations: 90000, Depth: 5, Elapsed: 4 * time.Second}
	engine.release <- other
	var stale tea.Msg
	select {
	case stale = <-late:
	case <-time.After(5 * time.Second):
		t.Fatal("the finished game's search never answered")
	}

	rh.feed(answer)
	if rh.s.g.At(own) != game.Vertical {
		t.Fatalf("the rematch did not play its own search's move %v:\n%s", own, rh.s.g)
	}
	rh.feed(stale)
	if got := rh.s.g.Entries(); got != 1 || rh.s.g.At(other) != game.NoPlayer {
		t.Fatalf("the rematch holds %d entries after the engine's one move:\n%s", got, rh.s.g)
	}
	rh.press("i")
	msg := rh.s.message
	if strings.Contains(msg, "88888") || !strings.Contains(msg, "3500") || !strings.Contains(msg, own.String()) {
		t.Errorf("the rematch's search figures %q are not its own search's final account", msg)
	}
}

// --- the finished search's figures -------------------------------------------

// TestSearchFiguresAreOfferedOnlyOnceASearchHasFinished keeps the key off the
// help until the engine has played a move of this game, and off it again while
// the next search runs, when the last one's figures would read as this one's.
func TestSearchFiguresAreOfferedOnlyOnceASearchHasFinished(t *testing.T) {
	engine := newGPBot(t)
	engine.final = bot.SearchStats{Nodes: 777, Evaluations: 900, Depth: 2, Elapsed: 1500 * time.Millisecond}
	h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), 120, 40)

	if gpOffers(h, gaSearchInfo) {
		t.Error("search figures are offered before the engine has searched")
	}
	h.press("i")
	if h.s.g.Entries() != 0 || strings.Contains(h.s.message, "777") {
		t.Errorf("the search key did something before any search: message %q", h.s.message)
	}

	h.playTurn(game.Point{Col: 1, Row: 0})
	gpEntered(t, engine)
	engine.complete(2, 500, game.Point{Col: 5, Row: 1})
	gpTick(t, h, time.Now())
	if gpOffers(h, gaSearchInfo) {
		t.Error("search figures are offered while a search is running")
	}
	h.press("i")
	if msg := h.s.message; strings.Contains(msg, "777") || strings.Contains(msg, "500") {
		t.Errorf("the search key showed figures while the search was running: %q", msg)
	}

	move := game.Point{Col: 5, Row: 1}
	engine.release <- move
	h.waitFor("the engine's move", func() bool { return h.s.g.At(move) == game.Horizontal })
	if !gpOffers(h, gaSearchInfo) {
		t.Fatal("the finished search's figures are not offered")
	}
	h.press("i")
	msg := h.s.message
	for _, want := range []string{move.String(), "777", "1.5s", searchStopText("time")} {
		if !strings.Contains(msg, want) {
			t.Errorf("the search's figures %q do not include %q", msg, want)
		}
	}
	if !regexp.MustCompile(`\bdepth 2\b`).MatchString(msg) {
		t.Errorf("the search's figures %q do not give the depth it completed", msg)
	}
}

// TestTheSearchFiguresShowAtEverySize asks for a finished search's figures at
// every supported size and reads them where the player does, on the status row
// of the composed frame. They used to be one sentence cut to the width, whose
// opening words alone filled the minimum width, so neither the move nor any
// figure showed there, and at forty columns the nodes and the time were cut off.
// The move and its completed depth have to show at every size, the nodes and
// the time from forty columns up, nothing may be cut part way through, and
// nodes shown at all are still said to be the whole search's.
func TestTheSearchFiguresShowAtEverySize(t *testing.T) {
	for _, size := range shellSizes {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			engine := newGPBot(t)
			engine.final = bot.SearchStats{Nodes: 210304, Evaluations: 400000, Depth: 5, Elapsed: 4600 * time.Millisecond}
			h := newGSHarness(t, gsTestDeps(t), gsVersusBot(6, engine), size[0], size[1])
			h.playTurn(game.Point{Col: 1, Row: 0})
			gpEntered(t, engine)

			// Play now, the way the cut sentence was found: its figures carry
			// a stop reason as well as the rest.
			best := game.Point{Col: 4, Row: 2}
			engine.complete(5, 4321, best)
			h.press("esc")
			h.waitFor("the search's move", func() bool { return h.s.g.At(best) == game.Horizontal })
			h.pump()

			h.press("i")
			frame := h.frame()
			gsCheckFrame(t, "the search's figures", frame, h.width, h.height)
			rows := strings.Split(frame, "\n")
			status := ansi.Strip(rows[len(rows)-1])
			if !strings.Contains(status, best.String()) || !gpClaimsDepth(status, 5) {
				t.Errorf("the status row %q does not give the move %v and its completed depth 5", status, best)
			}
			if h.width >= 40 {
				for _, want := range []string{`\b210304 nodes\b`, `\b4\.6s\b`} {
					if !regexp.MustCompile(want).MatchString(status) {
						t.Errorf("the status row %q does not match %s at %d columns", status, want, h.width)
					}
				}
			}
			if strings.Contains(status, ellipsis) {
				t.Errorf("the status row %q was cut rather than shortened by whole items", status)
			}
			if strings.Contains(status, "nodes") && !strings.Contains(status, "in all") {
				t.Errorf("the status row %q gives the nodes without saying they are the whole search's", status)
			}
		})
	}
}
