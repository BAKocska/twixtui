package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
)

// gsHintFixture is the advice the stub engine gives: short enough to fit a panel
// unwrapped, so a test can compare the rendered block for equality and catch any
// sentence the interface added of its own.
var gsHintFixture = bot.Hint{
	Move:      game.Point{Col: 3, Row: 3},
	Headline:  "play D4",
	Detail:    "it shortens your route.",
	Highlight: []game.Point{{Col: 4, Row: 1}, {Col: 0, Row: 4}},
}

func gsHintConfig(engine bot.Bot) GameConfig {
	cfg := gsVersusBot(6, engine)
	cfg.Hints, cfg.HintFor = true, engine
	return cfg
}

// TestHintIsShownOnTheBoardAndExplainedVerbatim covers all three halves of the
// requirement: the move is marked on the board, the explanation is there, and it
// is the engine's own words with nothing added.
func TestHintIsShownOnTheBoardAndExplainedVerbatim(t *testing.T) {
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture}
	h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

	// Park the cursor away from every highlighted hole: the cursor's own
	// brackets are drawn over a highlight's, so overlapping them would make the
	// count below say nothing.
	h.goTo(game.Point{Col: 2, Row: 2})
	plain := h.frame()
	if strings.Count(plain, "(·)") != 0 {
		t.Fatalf("holes are already marked before any hint was asked for\n%s", plain)
	}

	h.press("?")
	h.waitFor("the hint", func() bool { return h.s.hint.shown })

	// On the board: the move and the holes the explanation names, marked.
	want := []game.Point{gsHintFixture.Move}
	want = append(want, gsHintFixture.Highlight...)
	got := h.s.hint.highlights()
	if len(got) != len(want) {
		t.Fatalf("the hint marks %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the hint marks %v, want %v", got, want)
		}
	}
	frame := h.frame()
	if n := strings.Count(frame, "(·)"); n != len(want) {
		t.Errorf("%d holes are marked on the board, want %d\n%s", n, len(want), frame)
	}

	// In words: the engine's own, and only the engine's own, plus a legend that
	// says what the marks are. The engine's sentences are compared for equality
	// where they sit, and every remaining line must be the legend, so a sentence
	// of the interface's own invention about the position would fail here rather
	// than hide between the engine's lines.
	lines := h.s.hint.lines(40)
	expect := []string{hintLabel, gsHintFixture.Headline, gsHintFixture.Detail}
	if len(lines) < len(expect) {
		t.Fatalf("the advice block is %q, want at least %q", lines, expect)
	}
	for i := range expect {
		if lines[i] != expect[i] {
			t.Fatalf("the advice block is %q, want it to begin %q", lines, expect)
		}
	}
	// Whatever follows the engine's own text is the legend and nothing else. It
	// is checked against the panel's own legend string rather than a copy, so
	// rewording the legend does not need this test edited, but adding a second
	// line of the interface's own does.
	tail := strings.Join(lines[len(expect):], " ")
	if tail != strings.Join(gsWrap(h.s.hint.legend(), 40), " ") {
		t.Errorf("the advice block says %q after the engine's own text, which the engine did not write", tail)
	}
	if !strings.Contains(tail, gsHintFixture.Move.String()) {
		t.Errorf("the legend does not name the recommended move: %q", tail)
	}
	h.mustContain("headline", gsHintFixture.Headline)
	h.mustContain("detail", gsHintFixture.Detail)

	// Asking again puts the advice away rather than starting another search.
	h.press("?")
	if h.s.hint.shown {
		t.Error("the advice is still shown after being dismissed")
	}
	if _, hints := engine.counts(); hints != 1 {
		t.Errorf("the engine was asked %d times, want 1", hints)
	}
}

// TestHintSaysWhenThereIsNoAdvice is the truthfulness case that matters most: an
// engine that fails must not be paraphrased into advice.
func TestHintSaysWhenThereIsNoAdvice(t *testing.T) {
	t.Run("the engine reports an error", func(t *testing.T) {
		engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hintErr: errors.New("search failed")}
		h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

		h.press("?")
		h.waitFor("the failed hint", func() bool { return h.s.hint.unavailable != "" })
		if h.s.hint.shown {
			t.Fatal("a hint is shown for a search that failed")
		}
		if !strings.HasPrefix(h.s.hint.unavailable, hintNoAdvice) {
			t.Errorf("the panel reads %q, want it to start with %q", h.s.hint.unavailable, hintNoAdvice)
		}
		if len(h.s.hint.highlights()) != 0 {
			t.Error("a failed search still marked holes on the board")
		}
		h.mustContain("no advice", hintNoAdvice)
	})

	t.Run("the engine gives no explanation", func(t *testing.T) {
		engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}},
			hint: bot.Hint{Move: game.Point{Col: 3, Row: 3}}}
		h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

		h.press("?")
		h.waitFor("the empty hint", func() bool { return h.s.hint.unavailable != "" })
		if h.s.hint.unavailable != hintNoAdvice {
			t.Errorf("the panel reads %q, want exactly %q", h.s.hint.unavailable, hintNoAdvice)
		}
		if h.s.hint.shown {
			t.Error("an explanation with no text in it was shown as advice")
		}
	})
}

// TestAskingTwiceStartsOneSearch holds the engine inside Hint and presses the
// key again: a bot.Bot is not safe for concurrent use, so a second search would
// be a bug and not merely wasteful.
func TestAskingTwiceStartsOneSearch(t *testing.T) {
	gate := make(chan struct{})
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture, gate: gate}
	h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

	h.press("?")
	if !h.s.hint.running {
		t.Fatal("the first ? did not start a search")
	}
	h.mustContain("searching", hintSearching)

	h.press("?")
	h.press("?")
	if _, hints := engine.counts(); hints != 1 {
		t.Fatalf("the engine was asked %d times, want 1", hints)
	}
	if !strings.Contains(h.s.message, "already") {
		t.Errorf("the second ? says %q, which does not explain the wait", h.s.message)
	}

	// The interface is still live while the search runs.
	before := h.s.board.Cursor
	h.press("j")
	if h.s.board.Cursor == before {
		t.Error("the cursor did not move while the engine was searching")
	}

	close(gate)
	h.waitFor("the hint", func() bool { return h.s.hint.shown })
}

// TestHintIsDroppedWhenThePositionChanges keeps advice from outliving the
// position it describes.
func TestHintIsDroppedWhenThePositionChanges(t *testing.T) {
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture}
	h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

	h.press("?")
	h.waitFor("the hint", func() bool { return h.s.hint.shown })

	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("space")
	if h.s.hint.active() {
		t.Fatal("the advice survived the move it was advice about")
	}
	if strings.Contains(h.frame(), gsHintFixture.Detail) {
		t.Errorf("the explanation is still on screen after the position changed\n%s", h.frame())
	}
}

// TestHintIsDroppedWhenALinkIsEdited is the same rule for the other half of a
// turn: editing links changes the position the advice was about.
func TestHintIsDroppedWhenALinkIsEdited(t *testing.T) {
	engine := &stubBot{tier: bot.Pro, hint: gsHintFixture}
	d := gsTestDeps(t)
	cfg := gsHotseat(6)
	cfg.Hints, cfg.HintFor = true, engine
	h := newGSHarness(t, d, cfg, 80, 24)
	for _, p := range gsLinkOpening {
		h.playTurn(p)
	}
	h.ready()

	h.press("?")
	h.waitFor("the hint", func() bool { return h.s.hint.shown })

	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("x")
	h.press("4")
	if h.s.g.HasLink(mustLink(t, game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})) {
		t.Fatalf("the link was not removed: %q", h.s.message)
	}
	if h.s.hint.active() {
		t.Error("the advice survived a link edit")
	}
}

func mustLink(t *testing.T, a, b game.Point) game.Link {
	t.Helper()
	l, ok := game.NewLink(a, b)
	if !ok {
		t.Fatalf("%v and %v are not a knight's move apart", a, b)
	}
	return l
}

// TestHintsAreOnlyOfferedWhenThereIsAnEngineToAsk covers the configuration side:
// a game set up without hints must not pretend to have them.
func TestHintsAreOnlyOfferedWhenThereIsAnEngineToAsk(t *testing.T) {
	cases := []struct {
		name string
		set  func(*GameConfig, bot.Bot)
	}{
		{"hints switched off", func(c *GameConfig, e bot.Bot) { c.Hints, c.HintFor = false, e }},
		{"no engine to ask", func(c *GameConfig, e bot.Bot) { c.Hints, c.HintFor = true, nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture}
			cfg := gsVersusBot(6, engine)
			c.set(&cfg, engine)
			h := newGSHarness(t, gsTestDeps(t), cfg, 80, 24)

			h.press("?")
			if h.s.message != hintOff {
				t.Errorf("? says %q, want %q", h.s.message, hintOff)
			}
			if h.s.hint.active() {
				t.Error("a hint appeared in a game that has none")
			}
			if _, hints := engine.counts(); hints != 0 {
				t.Errorf("the engine was asked %d times, want 0", hints)
			}
			for _, e := range h.s.helpEntries() {
				if e.Label == "?" {
					t.Error("the help offers a hint key in a game without hints")
				}
			}
		})
	}
}

// TestHintIsRefusedWhenItIsNotThePlayersMove keeps advice tied to a decision the
// player is actually about to make.
func TestHintIsRefusedWhenItIsNotThePlayersMove(t *testing.T) {
	gate := make(chan struct{})
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture, gate: gate}
	h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if !h.s.botThinking {
		t.Fatal("the engine is not thinking, so it is not the wrong moment to ask")
	}
	h.press("?")
	if h.s.hint.active() {
		t.Error("advice was given about the opponent's move")
	}
	if _, hints := engine.counts(); hints != 0 {
		t.Errorf("the engine was asked for a hint %d times while it was moving, want 0", hints)
	}
	close(gate)
	h.waitFor("the engine's move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})
}

// TestLeavingCancelsTheHintSearch is the same promise the opponent engine gets:
// nothing keeps searching for a screen nobody is looking at.
func TestLeavingCancelsTheHintSearch(t *testing.T) {
	gate := make(chan struct{})
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, hint: gsHintFixture, gate: gate}
	h := newGSHarness(t, gsTestDeps(t), gsHintConfig(engine), 80, 24)

	h.press("?")
	if !h.s.hint.running {
		t.Fatal("no search to cancel")
	}
	h.press("q")

	ctxs := engine.contexts()
	if len(ctxs) == 0 {
		t.Fatal("the engine was never called")
	}
	select {
	case <-ctxs[0].Done():
	case <-timeoutAfterASecond():
		t.Fatal("leaving the screen did not cancel the hint search")
	}
	close(gate)
}

// TestSerialisedEngineDoesNotOverlapSearches covers the hazard behind
// GameConfig.HintFor being the opponent engine: bot.Bot carries the state of its
// search and is not safe for concurrent use, so the screen has to make a hint
// and a move take turns on the same engine.
func TestSerialisedEngineDoesNotOverlapSearches(t *testing.T) {
	engine := &overlapBot{}
	cfg := gsVersusBot(6, engine)
	cfg.Hints, cfg.HintFor = true, engine
	h := newGSHarness(t, gsTestDeps(t), cfg, 80, 24)

	// Ask for advice and then move at once: the hint search is cancelled but may
	// still be inside Hint when the move search is dispatched.
	h.press("?")
	h.playTurn(game.Point{Col: 1, Row: 0})
	h.waitFor("the engine's move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})
	if n := engine.overlaps(); n != 0 {
		t.Errorf("two searches ran on one engine at once %d times", n)
	}
}

// overlapBot reports any call that starts while another is still running.
type overlapBot struct {
	mu       sync.Mutex
	inside   int
	overlapC int
}

func (b *overlapBot) Tier() bot.Tier { return bot.Pro }

func (b *overlapBot) enter() {
	b.mu.Lock()
	b.inside++
	if b.inside > 1 {
		b.overlapC++
	}
	b.mu.Unlock()
}

func (b *overlapBot) exit() {
	b.mu.Lock()
	b.inside--
	b.mu.Unlock()
}

func (b *overlapBot) overlaps() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overlapC
}

func (b *overlapBot) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	b.enter()
	defer b.exit()
	time.Sleep(5 * time.Millisecond)
	return game.Point{Col: 5, Row: 1}, nil
}

func (b *overlapBot) Hint(ctx context.Context, g *game.Game) (bot.Hint, error) {
	b.enter()
	defer b.exit()
	select {
	case <-ctx.Done():
		return bot.Hint{}, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	return gsHintFixture, nil
}

func timeoutAfterASecond() <-chan time.Time { return time.After(2 * time.Second) }
