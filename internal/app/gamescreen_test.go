package app

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/ui"
)

// --- test rig ---------------------------------------------------------------

// gsRules is the ruleset the tests play by: the standard rules, on the smallest
// legal board when a whole game has to be played out.
func gsRules(size int) game.Ruleset {
	rs := game.Std
	rs.Size = size
	return rs
}

// gsTestDeps builds the collaborators against temporary directories, with a
// clock that advances a second per reading so a recorded duration is a real
// length rather than zero.
func gsTestDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	board, err := leaderboard.Open(filepath.Join(dir, "board"))
	if err != nil {
		t.Fatalf("opening the leaderboard: %v", err)
	}
	games, err := gamestore.Open(filepath.Join(dir, "games"))
	if err != nil {
		t.Fatalf("opening the game store: %v", err)
	}
	styles := ui.PlainStyles()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return Deps{
		ConfigDir: dir,
		Board:     board,
		Games:     games,
		Styles:    &styles,
		Keymap:    ui.DefaultKeymap(),
		Now: func() time.Time {
			clock = clock.Add(time.Second)
			return clock
		},
	}
}

func gsHotseat(size int) GameConfig {
	return GameConfig{
		Kind:  gamestore.Hotseat,
		Rules: gsRules(size),
		Seats: map[game.Player]Seat{
			game.Vertical:   {Profile: "ada"},
			game.Horizontal: {Profile: "linus"},
		},
	}
}

func gsVersusBot(size int, engine bot.Bot) GameConfig {
	return GameConfig{
		Kind:  gamestore.VersusBot,
		Rules: gsRules(size),
		Seats: map[game.Player]Seat{
			game.Vertical:   {Profile: "ada"},
			game.Horizontal: {Bot: engine},
		},
	}
}

// gsHarness drives a game screen the way Bubble Tea does: keys go in through
// Update and every command that comes back runs on its own goroutine with the
// message fed in again. Nothing here reaches in to change the model, so a test
// travels the same path a player does.
type gsHarness struct {
	t *testing.T
	s *gameScreen

	msgs chan tea.Msg
	done []DoneMsg
	idle time.Duration
	// inflight counts commands still running, so a keypress that started
	// nothing costs no waiting at all.
	inflight atomic.Int32

	width, height int
}

func newGSHarness(t *testing.T, d Deps, cfg GameConfig, w, h int) *gsHarness {
	t.Helper()
	screen, err := NewGameScreen(d, cfg)
	if err != nil {
		t.Fatalf("NewGameScreen: %v", err)
	}
	gs, ok := screen.(*gameScreen)
	if !ok {
		t.Fatalf("NewGameScreen returned %T, which the tests cannot drive", screen)
	}
	hr := &gsHarness{
		t:      t,
		s:      gs,
		msgs:   make(chan tea.Msg, 256),
		idle:   40 * time.Millisecond,
		width:  w,
		height: h,
	}
	hr.dispatch(gs.Init())
	hr.resize(w, h)
	return hr
}

func (h *gsHarness) dispatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	h.inflight.Add(1)
	go func() {
		defer h.inflight.Add(-1)
		msg := cmd()
		if msg == nil {
			return
		}
		select {
		case h.msgs <- msg:
		case <-time.After(5 * time.Second):
		}
	}()
}

// pump feeds queued messages back into the model until nothing new turns up. A
// DoneMsg is the screen handing control back, so it is collected rather than
// delivered.
func (h *gsHarness) pump() {
	for {
		// Wait briefly while nothing is running, and up to idle while something
		// is, so a command that has yet to produce its message is not missed.
		wait := time.Millisecond
		if h.inflight.Load() > 0 {
			wait = h.idle
		}
		select {
		case msg := <-h.msgs:
			switch m := msg.(type) {
			case tea.BatchMsg:
				for _, c := range m {
					h.dispatch(c)
				}
			case DoneMsg:
				h.done = append(h.done, m)
			default:
				_, cmd := h.s.Update(msg)
				h.dispatch(cmd)
			}
		case <-time.After(wait):
			if len(h.msgs) > 0 {
				continue
			}
			if wait < h.idle && h.inflight.Load() > 0 {
				continue
			}
			return
		}
	}
}

func (h *gsHarness) feed(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.s.Update(msg)
	h.dispatch(cmd)
	h.pump()
}

func (h *gsHarness) resize(w, height int) {
	h.width, h.height = w, height
	h.feed(tea.WindowSizeMsg{Width: w, Height: height})
}

// press sends one key, checking on the way that the constructed message really
// encodes as the string the keymaps bind: a binding proven against a key string
// the test invented would prove nothing.
func (h *gsHarness) press(key string) {
	h.t.Helper()
	h.feed(gsKeyMsg(h.t, key))
}

func gsKeyMsg(t *testing.T, key string) tea.KeyPressMsg {
	t.Helper()
	var k tea.Key
	switch key {
	case "space":
		k = tea.Key{Code: tea.KeySpace, Text: " "}
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	case "ctrl+c":
		k = tea.Key{Code: 'c', Mod: tea.ModCtrl}
	default:
		r := []rune(key)
		if len(r) != 1 {
			t.Fatalf("the test rig cannot encode the key %q", key)
		}
		k = tea.Key{Code: r[0], Text: key}
	}
	msg := tea.KeyPressMsg(k)
	if got := msg.String(); got != key {
		t.Fatalf("the constructed key encodes as %q, want %q", got, key)
	}
	return msg
}

func (h *gsHarness) frame() string { return h.s.View().Content }

// ready clears a hotseat handover, which is the next player saying they have
// the keyboard.
func (h *gsHarness) ready() {
	h.t.Helper()
	if h.s.handover {
		h.press("enter")
	}
}

// goTo walks the cursor to a hole with the real movement keys. The column moves
// first when the target sits on a border row, so the walk never has to pass
// through a corner hole, which does not exist.
func (h *gsHarness) goTo(p game.Point) {
	h.t.Helper()
	n := h.s.g.Size()
	if p.Row == 0 || p.Row == n-1 {
		h.walkCol(p.Col)
		h.walkRow(p.Row)
	} else {
		h.walkRow(p.Row)
		h.walkCol(p.Col)
	}
	if h.s.board.Cursor != p {
		h.t.Fatalf("the cursor walked to %v, want %v", h.s.board.Cursor, p)
	}
}

func (h *gsHarness) walkCol(col int) {
	h.t.Helper()
	for i := 0; i < 4*h.s.g.Size() && h.s.board.Cursor.Col != col; i++ {
		if h.s.board.Cursor.Col < col {
			h.press("l")
		} else {
			h.press("h")
		}
	}
}

func (h *gsHarness) walkRow(row int) {
	h.t.Helper()
	for i := 0; i < 4*h.s.g.Size() && h.s.board.Cursor.Row != row; i++ {
		if h.s.board.Cursor.Row < row {
			h.press("j")
		} else {
			h.press("k")
		}
	}
}

// playTurn plays one whole turn from the keyboard: take the keyboard if a
// hotseat handover is waiting, walk to the hole, place the peg, commit.
func (h *gsHarness) playTurn(p game.Point) {
	h.t.Helper()
	h.ready()
	h.goTo(p)
	h.press("space")
	h.press("enter")
}

func (h *gsHarness) mustContain(what, text string) {
	h.t.Helper()
	if !strings.Contains(h.frame(), text) {
		h.t.Fatalf("%s: the frame does not contain %q\n--- frame ---\n%s", what, text, h.frame())
	}
}

// waitFor pumps until the condition holds, so no test depends on how long a
// goroutine took.
func (h *gsHarness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s\nmessage %q notice %q\n%s",
				what, h.s.message, h.s.notice, h.s.g)
		}
		h.pump()
	}
}

// gsCheckFrame is the fitting invariant: no line wider than the terminal, and no
// more lines than it has rows.
func gsCheckFrame(t *testing.T, where, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) > h {
		t.Errorf("%s: %d lines rendered into %d rows", where, len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("%s: line %d is %d cells wide in a %d-cell terminal: %q", where, i, got, w, l)
		}
	}
}

// gsWinScript is a whole game: vertical builds a chain from the top border row
// to the bottom one while horizontal fills its own border column, which links
// nothing and blocks nothing.
var gsWinScript = []game.Point{
	{Col: 1, Row: 0}, {Col: 5, Row: 1},
	{Col: 2, Row: 2}, {Col: 5, Row: 2},
	{Col: 3, Row: 4}, {Col: 5, Row: 3},
	{Col: 1, Row: 5},
}

// --- stub engine ------------------------------------------------------------

// stubBot is an engine with a fixed reply. gate, when set, holds Move and Hint
// until the test releases them, which is how "the interface stays live while the
// engine thinks" is exercised without depending on a real search's timing.
type stubBot struct {
	tier    bot.Tier
	moves   []game.Point
	hint    bot.Hint
	hintErr error
	gate    chan struct{}

	mu        sync.Mutex
	moveCalls int
	hintCalls int
	ctxs      []context.Context
}

func (b *stubBot) Tier() bot.Tier { return b.tier }

func (b *stubBot) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	b.mu.Lock()
	b.ctxs = append(b.ctxs, ctx)
	n := b.moveCalls
	b.moveCalls++
	gate := b.gate
	b.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return game.Point{}, ctx.Err()
		}
	}
	if len(b.moves) == 0 {
		return game.Point{}, bot.ErrNoMove
	}
	if n >= len(b.moves) {
		n = len(b.moves) - 1
	}
	return b.moves[n], nil
}

func (b *stubBot) Hint(ctx context.Context, g *game.Game) (bot.Hint, error) {
	b.mu.Lock()
	b.hintCalls++
	b.ctxs = append(b.ctxs, ctx)
	gate := b.gate
	b.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return bot.Hint{}, ctx.Err()
		}
	}
	return b.hint, b.hintErr
}

func (b *stubBot) counts() (moves, hints int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.moveCalls, b.hintCalls
}

func (b *stubBot) contexts() []context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]context.Context(nil), b.ctxs...)
}

// --- hotseat ----------------------------------------------------------------

// TestHotseatGamePlayedToAWin plays a whole game through the model and requires
// the win, the stored game and exactly one leaderboard row. One row is the
// point: the board credits both players by reading that row backwards, so a
// hotseat game recorded once per player would be counted twice.
func TestHotseatGamePlayedToAWin(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)

	for _, p := range gsWinScript {
		h.playTurn(p)
	}

	res := h.s.g.Result()
	if res.Outcome != game.VerticalWins || res.Reason != game.Connection {
		t.Fatalf("result is %v/%v, want VerticalWins by Connection\n%s", res.Outcome, res.Reason, h.s.g)
	}
	if got := h.s.g.Ply(); got != len(gsWinScript) {
		t.Errorf("%d turns played, want %d", got, len(gsWinScript))
	}
	h.mustContain("game over panel", "wins")
	h.mustContain("game over panel", "border to border")

	rows := d.Board.History("ada", 0)
	if len(rows) != 1 {
		t.Fatalf("%d leaderboard rows for ada, want exactly 1: %+v", len(rows), rows)
	}
	if other := d.Board.History("linus", 0); len(other) != 1 {
		t.Fatalf("%d leaderboard rows for linus, want exactly 1: %+v", len(other), other)
	}
	row := rows[0]
	if row.Player != "ada" || row.Opponent != "linus" {
		t.Errorf("row is %q against %q, want ada against linus", row.Player, row.Opponent)
	}
	if row.Outcome != leaderboard.Win {
		t.Errorf("row outcome is %q, want win", row.Outcome)
	}
	if row.Side != game.Vertical.String() {
		t.Errorf("row side is %q, want vertical", row.Side)
	}
	if row.Moves != len(gsWinScript) {
		t.Errorf("row records %d moves, want %d", row.Moves, len(gsWinScript))
	}
	if row.Ruleset != gsRules(6).Canonical() {
		t.Errorf("row ruleset is %q, want %q", row.Ruleset, gsRules(6).Canonical())
	}
	if row.Duration <= 0 {
		t.Errorf("row duration is %v, want a positive length", row.Duration)
	}

	saved := d.Games.List()
	if len(saved) != 1 {
		t.Fatalf("%d stored games, want 1", len(saved))
	}
	if !saved[0].Finished {
		t.Error("the stored game is not marked finished")
	}
	replayed, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the stored game does not replay: %v", err)
	}
	if replayed.Result() != res {
		t.Errorf("the stored game replays to %v, want %v", replayed.Result(), res)
	}

	// Leaving a finished game must not add a second row.
	h.press("q")
	if rows := d.Board.History("ada", 0); len(rows) != 1 {
		t.Fatalf("%d leaderboard rows after leaving, want 1", len(rows))
	}
	if len(h.done) != 1 || h.done[0].Next != nil || h.done[0].Err != nil {
		t.Fatalf("leaving produced %+v, want one plain DoneMsg", h.done)
	}
}

// TestHotseatHandoverStopsTheWrongPlayerMoving covers the one mistake a shared
// keyboard makes easy: carrying on typing after your own turn and placing your
// opponent's peg.
func TestHotseatHandoverStopsTheWrongPlayerMoving(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if !h.s.handover {
		t.Fatal("no handover after a hotseat turn: the next player is never asked for")
	}
	if h.s.g.Turn() != game.Horizontal {
		t.Fatalf("turn is %v, want horizontal", h.s.g.Turn())
	}
	h.mustContain("handover prompt", "horizontal's turn")
	h.mustContain("handover prompt", "linus")

	before := h.s.g.PegCount(game.Horizontal)
	h.goTo(game.Point{Col: 3, Row: 3})
	h.press("space")
	if h.s.g.Staged().PegPlaced {
		t.Error("space placed a peg while the keyboard was still being handed over")
	}
	if got := h.s.g.PegCount(game.Horizontal); got != before {
		t.Errorf("horizontal has %d pegs, want %d", got, before)
	}
	if !strings.Contains(h.s.message, "press") {
		t.Errorf("the refusal reads %q, which does not say what to do", h.s.message)
	}

	h.press("enter")
	if h.s.handover {
		t.Fatal("the handover survived enter")
	}
	h.press("space")
	if !h.s.g.Staged().PegPlaced {
		t.Fatal("space did not place a peg once the keyboard had been handed over")
	}
}

// --- the engine seat --------------------------------------------------------

func TestBotMovesAfterTheHuman(t *testing.T) {
	engine := &stubBot{tier: bot.Beginner, moves: []game.Point{{Col: 5, Row: 1}}}
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsVersusBot(6, engine), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	h.waitFor("the engine's move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})

	if h.s.g.Turn() != game.Vertical {
		t.Errorf("turn is %v after the engine moved, want vertical", h.s.g.Turn())
	}
	if h.s.botThinking {
		t.Error("the screen still says the engine is thinking after its move arrived")
	}
	if moves, _ := engine.counts(); moves != 1 {
		t.Errorf("the engine was asked for %d moves, want 1", moves)
	}
	if h.s.handover {
		t.Error("a game against an engine asked for a hotseat handover")
	}
	h.mustContain("engine move", "F2")
}

// TestInterfaceStaysLiveWhileTheEngineThinks holds the engine inside Move and
// then works the interface: if the search ran inside Update, none of this would
// answer at all.
func TestInterfaceStaysLiveWhileTheEngineThinks(t *testing.T) {
	gate := make(chan struct{})
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, gate: gate}
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsVersusBot(6, engine), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if !h.s.botThinking {
		t.Fatal("the screen does not know the engine is thinking")
	}
	h.mustContain("thinking indicator", "thinking")

	before := h.s.board.Cursor
	h.press("j")
	if h.s.board.Cursor == before {
		t.Error("the cursor did not move while the engine was thinking")
	}
	gsCheckFrame(t, "while thinking", h.frame(), h.width, h.height)
	if h.s.g.At(game.Point{Col: 5, Row: 1}) != game.NoPlayer {
		t.Fatal("the engine's move landed before it was released")
	}

	spun := h.s.spinner
	h.feed(botTickMsg{})
	if h.s.spinner == spun {
		t.Error("the thinking indicator did not advance on a tick")
	}

	close(gate)
	h.waitFor("the released move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})
	if h.s.botThinking {
		t.Error("the thinking indicator is still on after the move arrived")
	}
}

// TestLeavingCancelsTheEngine proves the search is told to stop rather than left
// running behind a screen nobody is looking at.
func TestLeavingCancelsTheEngine(t *testing.T) {
	gate := make(chan struct{})
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}, gate: gate}
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsVersusBot(6, engine), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if !h.s.botThinking {
		t.Fatal("the engine is not thinking, so there is nothing to cancel")
	}

	h.press("q")
	ctxs := engine.contexts()
	if len(ctxs) == 0 {
		t.Fatal("the engine was never called")
	}
	select {
	case <-ctxs[0].Done():
	case <-time.After(2 * time.Second):
		t.Fatal("leaving the screen did not cancel the engine's context")
	}
	if len(h.done) != 1 {
		t.Fatalf("leaving produced %d DoneMsg, want 1", len(h.done))
	}
	close(gate)

	saved := d.Games.Unfinished()
	if len(saved) != 1 {
		t.Fatalf("%d unfinished games stored, want 1", len(saved))
	}
	resumed, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the saved game does not replay: %v", err)
	}
	if resumed.At(game.Point{Col: 1, Row: 0}) != game.Vertical {
		t.Error("the saved game has lost the move that was played")
	}
}

// TestResumeDoesNotRecordAFinishedGameTwice guards the other half of the
// one-row-per-game rule: opening a game that is already over must not add a row
// for a game that was recorded when it ended.
func TestResumeDoesNotRecordAFinishedGameTwice(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)
	for _, p := range gsWinScript {
		h.playTurn(p)
	}
	h.press("q")

	saved := d.Games.List()
	if len(saved) != 1 {
		t.Fatalf("%d stored games, want 1", len(saved))
	}
	cfg := gsHotseat(6)
	cfg.Resume = &saved[0]
	again := newGSHarness(t, d, cfg, 80, 24)
	if !again.s.stopped {
		t.Error("a finished game reopened as if it could be played on")
	}
	again.mustContain("reopened result", "game over")
	if rows := d.Board.History("ada", 0); len(rows) != 1 {
		t.Fatalf("%d leaderboard rows after reopening, want 1", len(rows))
	}
}

// TestResumeContinuesAnUnfinishedGame covers picking a game back up: the stored
// position comes back and play carries on under the same stored identifier.
func TestResumeContinuesAnUnfinishedGame(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)
	h.playTurn(game.Point{Col: 1, Row: 0})
	h.press("q")

	saved := d.Games.Unfinished()
	if len(saved) != 1 {
		t.Fatalf("%d unfinished games, want 1", len(saved))
	}
	cfg := gsHotseat(6)
	cfg.Resume = &saved[0]
	again := newGSHarness(t, d, cfg, 80, 24)
	if again.s.g.At(game.Point{Col: 1, Row: 0}) != game.Vertical {
		t.Fatal("the resumed game does not hold the move that was played")
	}
	if again.s.g.Turn() != game.Horizontal {
		t.Errorf("the resumed game is on %v's move, want horizontal", again.s.g.Turn())
	}
	again.playTurn(game.Point{Col: 5, Row: 1})
	again.press("q")
	if got := d.Games.List(); len(got) != 1 {
		t.Fatalf("%d stored games after resuming, want the same 1", len(got))
	}
}

// --- link editing -----------------------------------------------------------

// gsLinkOpening leaves vertical with pegs B1 and C3 linked, plus C1 and B3
// unlinked because B3:C1 would cross B1:C3. Horizontal fills its own border
// column, which links nothing.
var gsLinkOpening = []game.Point{
	{Col: 1, Row: 0}, {Col: 5, Row: 1},
	{Col: 2, Row: 2}, {Col: 5, Row: 2},
	{Col: 2, Row: 0}, {Col: 5, Row: 3},
	{Col: 1, Row: 2}, {Col: 5, Row: 4},
}

func gsLinkPosition(t *testing.T) *gsHarness {
	t.Helper()
	h := newGSHarness(t, gsTestDeps(t), gsHotseat(6), 80, 24)
	for _, p := range gsLinkOpening {
		h.playTurn(p)
	}
	h.ready()
	return h
}

// TestLinkModeShowsDigitsOnRealNeighbours proves the affordance: the digits sit
// on the holes they act on, and only on holes that can actually be linked.
func TestLinkModeShowsDigitsOnRealNeighbours(t *testing.T) {
	h := gsLinkPosition(t)

	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("x")
	if !h.s.linkMode {
		t.Fatal("x did not enter link mode")
	}
	// B1 has exactly one own peg a knight's move away: C3, to the
	// south-south-east, which is the fourth of the eight directions.
	want := map[game.Point]rune{{Col: 2, Row: 2}: '4'}
	digits := h.s.linkDigits()
	if len(digits) != len(want) {
		t.Fatalf("link mode offers %v, want %v", digits, want)
	}
	for p, r := range want {
		if digits[p] != r {
			t.Fatalf("link mode offers %v, want %v", digits, want)
		}
	}
	h.mustContain("link mode panel", "editing links at B1")
	h.mustContain("link mode panel", "C3")
	h.mustContain("link mode panel", "remove")
	if !strings.Contains(h.frame(), "4") {
		t.Errorf("the digit is not drawn on the board\n%s", h.frame())
	}

	link, _ := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	if !h.s.g.HasLink(link) {
		t.Fatalf("%v is not on the board before the toggle", link)
	}
	h.press("4")
	if h.s.g.HasLink(link) {
		t.Fatalf("%v survived its digit: %q", link, h.s.message)
	}
	if !strings.Contains(h.s.message, link.String()) {
		t.Errorf("the message %q does not name the link that changed", h.s.message)
	}
	h.press("4")
	if !h.s.g.HasLink(link) {
		t.Fatalf("%v did not come back on a second press: %q", link, h.s.message)
	}
}

// TestRefusedCrossingNamesTheBlockingLink holds the promise that a refusal
// explains itself: the player is told which link is in the way, not merely that
// something is.
func TestRefusedCrossingNamesTheBlockingLink(t *testing.T) {
	h := gsLinkPosition(t)

	// This turn's peg first, since new links come after it. E5 is out of knight
	// reach of every peg already down, so it adds no link of its own and cannot
	// become the blocker the refusal names.
	h.goTo(game.Point{Col: 4, Row: 4})
	h.press("space")
	if !h.s.g.Staged().PegPlaced {
		t.Fatalf("the peg was not staged: %q", h.s.message)
	}

	h.goTo(game.Point{Col: 1, Row: 2})
	h.press("x")
	// B3's own neighbour C1 lies to the north-north-east, direction one.
	h.press("1")

	blocked, _ := game.NewLink(game.Point{Col: 1, Row: 2}, game.Point{Col: 2, Row: 0})
	blocker, _ := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	if h.s.g.HasLink(blocked) {
		t.Fatal("a crossing link was created")
	}
	for _, want := range []string{blocked.String(), blocker.String(), "cross"} {
		if !strings.Contains(h.s.message, want) {
			t.Fatalf("the refusal %q does not mention %q", h.s.message, want)
		}
	}
	h.mustContain("refusal on screen", blocker.String())
}

// TestTurnRunsRemovalsThenPegThenLinks walks the whole transactional turn in the
// order the rules impose, and requires each refusal to give its reason.
func TestTurnRunsRemovalsThenPegThenLinks(t *testing.T) {
	h := gsLinkPosition(t)

	older, _ := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})
	declined, _ := game.NewLink(game.Point{Col: 1, Row: 2}, game.Point{Col: 3, Row: 3})

	// A turn that places D4 is offered a link back to B3 and declines it, so the
	// next turn has two own pegs a knight's move apart and unlinked.
	h.goTo(game.Point{Col: 3, Row: 3})
	h.press("space")
	if !h.s.g.HasLink(declined) {
		t.Fatalf("the placement did not take the link it was offered\n%s", h.s.g)
	}
	h.mustContain("staged panel", "staged peg D4")
	h.press("x")
	h.press("7") // WNW, back towards B3
	if h.s.g.HasLink(declined) {
		t.Fatalf("the offered link could not be withdrawn after the peg: %q", h.s.message)
	}
	h.press("enter")
	if h.s.g.HasLink(declined) {
		t.Fatal("the declined link came back on commit")
	}
	h.playTurn(game.Point{Col: 0, Row: 1}) // horizontal, out of the way
	h.ready()

	// Before this turn's peg a new link waits for it, and says so.
	h.goTo(game.Point{Col: 1, Row: 2})
	h.press("x")
	h.press("3") // ESE, towards D4
	if h.s.g.HasLink(declined) {
		t.Fatal("a new link was made before this turn's peg")
	}
	if !strings.Contains(h.s.message, "after this turn's peg") {
		t.Errorf("the refusal reads %q, which does not give the order", h.s.message)
	}
	if got := h.s.linkVerb(game.Point{Col: 3, Row: 3}); got != "after peg" {
		t.Errorf("the overlay says %q of a link that has to wait", got)
	}

	// Removals do come first, and are undoable inside the turn.
	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("4")
	if h.s.g.HasLink(older) {
		t.Fatalf("an older link could not be removed before the peg: %q", h.s.message)
	}
	if !h.s.stagedRemoval(older) {
		t.Errorf("staged removals are %v, want %v", h.s.g.Staged().Removed, older)
	}
	h.press("4")
	if !h.s.g.HasLink(older) {
		t.Fatalf("a removal made this turn could not be undone: %q", h.s.message)
	}
	if h.s.stagedRemoval(older) {
		t.Error("the undone removal is still staged")
	}

	// Now the peg, and then the link edits. Placing is a board action, so link
	// mode is left first.
	h.press("esc")
	h.goTo(game.Point{Col: 4, Row: 4})
	h.press("space")
	if !h.s.g.Staged().PegPlaced {
		t.Fatalf("the peg was not staged: %q", h.s.message)
	}
	h.goTo(game.Point{Col: 1, Row: 2})
	h.press("x")
	h.press("3")
	if !h.s.g.HasLink(declined) {
		t.Fatalf("two older pegs could not be linked after this turn's peg: %q", h.s.message)
	}
	staged := h.s.g.Staged()
	if len(staged.Added) != 1 || staged.Added[0].Canonical() != declined {
		t.Errorf("staged additions are %v, want just %v", staged.Added, declined)
	}
	h.mustContain("staged additions", "added "+declined.String())

	// An older link may not be taken off now: removals came first.
	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("4")
	if !h.s.g.HasLink(older) {
		t.Fatal("an older link was removed after this turn's peg was placed")
	}
	if !strings.Contains(h.s.message, "removals come before") {
		t.Errorf("the refusal reads %q, which does not give the rule", h.s.message)
	}

	h.press("enter")
	if !h.s.g.HasLink(declined) {
		t.Fatal("the link added by hand did not survive the commit")
	}
}

// TestAbortDropsTheWholeTurn covers the escape hatch a staged turn needs.
func TestAbortDropsTheWholeTurn(t *testing.T) {
	h := gsLinkPosition(t)
	older, _ := game.NewLink(game.Point{Col: 1, Row: 0}, game.Point{Col: 2, Row: 2})

	h.goTo(game.Point{Col: 1, Row: 0})
	h.press("x")
	h.press("4")
	if h.s.g.HasLink(older) {
		t.Fatalf("the removal did not take: %q", h.s.message)
	}
	h.press("esc")
	h.goTo(game.Point{Col: 4, Row: 4})
	h.press("space")
	if !h.s.g.Staged().PegPlaced {
		t.Fatalf("the peg was not staged, so the abort has nothing to drop: %q", h.s.message)
	}
	h.press("a")
	if h.s.g.Staged().PegPlaced {
		t.Error("the staged peg survived the abort")
	}
	if !h.s.g.HasLink(older) {
		t.Error("the removed link did not come back on the abort")
	}
	if h.s.g.At(game.Point{Col: 4, Row: 4}) != game.NoPlayer {
		t.Error("the aborted peg is still on the board")
	}
}

// --- swap -------------------------------------------------------------------

// TestSwapIsOfferedExactlyWhenItIsAvailable covers the pie rule, which lasts one
// turn and is easy to miss.
func TestSwapIsOfferedExactlyWhenItIsAvailable(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)

	if h.s.swapOffered() {
		t.Fatal("the swap is offered before the opening peg exists")
	}
	h.press("s")
	if !strings.Contains(h.s.message, "not on offer") {
		t.Errorf("pressing s early says %q", h.s.message)
	}

	opening := game.Point{Col: 1, Row: 0}
	h.playTurn(opening)
	h.ready()
	if !h.s.g.CanSwap() {
		t.Fatal("the engine does not offer the swap after the opening peg")
	}
	if !h.s.swapOffered() {
		t.Fatal("the screen does not offer the swap when the engine does")
	}
	h.mustContain("swap offer", "swap on offer")
	h.mustContain("swap offer", "B1")
	h.mustContain("swap offer", "This turn only")

	h.press("s")
	mirror := game.Point{Col: opening.Row, Row: opening.Col}
	if !h.s.g.Swapped() {
		t.Fatalf("the swap was not taken: %q", h.s.message)
	}
	if h.s.g.At(opening) != game.NoPlayer {
		t.Errorf("the opening hole still holds %v", h.s.g.At(opening))
	}
	if got := h.s.g.At(mirror); got != game.Horizontal {
		t.Errorf("the mirrored hole %v holds %v, want horizontal\n%s", mirror, got, h.s.g)
	}
	if h.s.g.Turn() != game.Vertical {
		t.Errorf("turn is %v after the swap, want vertical", h.s.g.Turn())
	}
	h.ready()
	if h.s.swapOffered() {
		t.Error("the swap is still offered after it was taken")
	}
	if h.s.g.CanSwap() {
		t.Error("the engine still offers the swap after it was taken")
	}
}

// --- resign and draw --------------------------------------------------------

func TestResignEndsTheGameAndRecordsOneRow(t *testing.T) {
	engine := &stubBot{tier: bot.Beginner, moves: []game.Point{{Col: 5, Row: 1}}}
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsVersusBot(6, engine), 80, 24)

	// A confirmation stands between the player and an irreversible key.
	h.press("r")
	if h.s.confirm != gaResign {
		t.Fatal("r resigned without asking")
	}
	h.press("n")
	if h.s.g.Result().Over() {
		t.Fatal("declining the confirmation resigned anyway")
	}

	h.press("r")
	h.press("y")
	res := h.s.g.Result()
	if res.Reason != game.Resignation || res.Winner() != game.Horizontal {
		t.Fatalf("result is %v/%v, want horizontal winning by resignation", res.Outcome, res.Reason)
	}
	h.mustContain("result", "resignation")

	rows := d.Board.History("ada", 0)
	if len(rows) != 1 {
		t.Fatalf("%d leaderboard rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Outcome != leaderboard.Loss {
		t.Errorf("outcome is %q, want loss", rows[0].Outcome)
	}
	if want := leaderboard.BotName(bot.Beginner.String()); rows[0].Opponent != want {
		t.Errorf("opponent is %q, want %q", rows[0].Opponent, want)
	}
}

// TestDrawOfferSurvivesTheMoveAndIsAccepted covers the engine's deliberate rule:
// an offer is not a turn, and committing a move refuses only an offer the
// opponent made, so offering a draw together with your move works.
func TestDrawOfferSurvivesTheMoveAndIsAccepted(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)

	h.press("d")
	if h.s.g.DrawOfferedBy() != game.Vertical {
		t.Fatalf("no standing offer from vertical: %q", h.s.message)
	}
	if h.s.g.Turn() != game.Vertical {
		t.Fatal("the draw offer consumed the turn")
	}
	if h.s.g.Ply() != 0 {
		t.Fatalf("the draw offer counted as %d turns", h.s.g.Ply())
	}

	h.playTurn(game.Point{Col: 1, Row: 0})
	if h.s.g.DrawOfferedBy() != game.Vertical {
		t.Fatal("committing a move withdrew the mover's own offer")
	}
	h.mustContain("offer on screen", "draw offered by vertical")

	h.ready()
	h.press("d")
	res := h.s.g.Result()
	if res.Outcome != game.Draw || res.Reason != game.Agreement {
		t.Fatalf("result is %v/%v, want a draw by agreement", res.Outcome, res.Reason)
	}
	h.mustContain("result", "drawn")
	rows := d.Board.History("ada", 0)
	if len(rows) != 1 {
		t.Fatalf("%d leaderboard rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Outcome != leaderboard.DrawOutcome {
		t.Errorf("outcome is %q, want draw", rows[0].Outcome)
	}
	if other := d.Board.History("linus", 0); len(other) != 1 {
		t.Fatalf("%d rows for linus, want 1", len(other))
	}
}

// --- resize -----------------------------------------------------------------

func TestGameScreenFitsEverySize(t *testing.T) {
	sizes := []struct{ w, h int }{{20, 8}, {40, 12}, {80, 24}, {200, 60}, {12, 4}, {1, 1}}
	for _, size := range sizes {
		d := gsTestDeps(t)
		engine := &stubBot{
			tier:  bot.Pro,
			moves: []game.Point{{Col: 4, Row: 4}},
			hint: bot.Hint{
				Move:      game.Point{Col: 13, Row: 13},
				Headline:  "play N14",
				Detail:    "it shortens your route.",
				Highlight: []game.Point{{Col: 9, Row: 9}},
			},
		}
		cfg := gsVersusBot(24, engine)
		cfg.Hints, cfg.HintFor = true, engine
		h := newGSHarness(t, d, cfg, size.w, size.h)

		h.playTurn(game.Point{Col: 12, Row: 12})
		h.waitFor("the engine's move", func() bool {
			return h.s.g.At(game.Point{Col: 4, Row: 4}) == game.Horizontal
		})
		gsCheckFrame(t, "play", h.frame(), size.w, size.h)

		h.press("?")
		h.waitFor("the hint", func() bool { return h.s.hint.shown })
		gsCheckFrame(t, "hint", h.frame(), size.w, size.h)

		h.goTo(game.Point{Col: 12, Row: 12})
		h.press("x")
		gsCheckFrame(t, "link mode", h.frame(), size.w, size.h)
		h.press("esc")

		h.press("r")
		gsCheckFrame(t, "confirmation", h.frame(), size.w, size.h)
		h.press("y")
		gsCheckFrame(t, "game over", h.frame(), size.w, size.h)
	}
}

// TestShrinkAndRegrowKeepsStateAndCursor shrinks the terminal until the cursor's
// hole has left the view, grows it back and requires the frame to be identical,
// which can only hold if position, cursor and viewport all survived.
func TestShrinkAndRegrowKeepsStateAndCursor(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(24), 200, 60)
	for _, p := range []game.Point{{Col: 5, Row: 5}, {Col: 10, Row: 10}, {Col: 6, Row: 7}, {Col: 12, Row: 11}} {
		h.playTurn(p)
	}
	h.ready()
	h.goTo(game.Point{Col: 21, Row: 22})

	before := h.frame()
	cursor := h.s.board.Cursor
	transcript, err := h.s.g.Transcript()
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	gsCheckFrame(t, "before shrink", before, 200, 60)

	h.resize(30, 10)
	small := h.frame()
	gsCheckFrame(t, "shrunk", small, 30, 10)
	if small == before {
		t.Fatal("shrinking the terminal changed nothing, so the test proves nothing")
	}

	h.resize(200, 60)
	if after := h.frame(); after != before {
		t.Errorf("the frame did not come back:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if h.s.board.Cursor != cursor {
		t.Errorf("the cursor moved from %v to %v", cursor, h.s.board.Cursor)
	}
	got, err := h.s.g.Transcript()
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if got != transcript {
		t.Errorf("the game changed across the resize:\n%s\n%s", transcript, got)
	}
}

// --- remote play ------------------------------------------------------------

// gsPipePair runs both ends of the protocol over an in-memory connection, so
// each side is a real session rather than a stand-in.
func gsPipePair(t *testing.T, rs game.Ruleset, hostSide game.Player) (host, guest netplay.Session) {
	t.Helper()
	hc, gc := net.Pipe()
	type result struct {
		s   netplay.Session
		err error
	}
	hostCh, guestCh := make(chan result, 1), make(chan result, 1)
	tuning := netplay.Tuning{
		HandshakeTimeout: 10 * time.Second,
		Keepalive:        time.Second,
		DeadAfter:        30 * time.Second,
	}
	go func() {
		s, err := netplay.HostOver(context.Background(), hc, netplay.HostOptions{
			Name: "ada", Rules: rs, Side: hostSide, Tuning: tuning,
		})
		hostCh <- result{s, err}
	}()
	go func() {
		s, err := netplay.JoinOver(context.Background(), gc, netplay.GuestOptions{
			Name: "linus", Tuning: tuning,
		})
		guestCh <- result{s, err}
	}()
	hr, gr := <-hostCh, <-guestCh
	if hr.err != nil || gr.err != nil {
		t.Fatalf("the handshake failed: host %v, guest %v", hr.err, gr.err)
	}
	t.Cleanup(func() {
		hr.s.Close()
		gr.s.Close()
	})
	return hr.s, gr.s
}

func gsRemoteConfig(size int, session netplay.Session, local game.Player) GameConfig {
	return GameConfig{
		Kind:  gamestore.Remote,
		Rules: gsRules(size),
		Seats: map[game.Player]Seat{
			local:            {Profile: "ada"},
			local.Opponent(): {Remote: true, Label: "linus"},
		},
		Session: session,
	}
}

// TestRemoteMovesTravelBothWays drives a real session over a pipe: the local
// move reaches the opponent, and the opponent's move is applied here.
func TestRemoteMovesTravelBothWays(t *testing.T) {
	host, guest := gsPipePair(t, gsRules(6), game.Vertical)

	// The opponent's end has to read its own events for the protocol to run.
	guestEvents := make(chan netplay.Event, 16)
	go func() {
		for ev := range guest.Events() {
			guestEvents <- ev
		}
		close(guestEvents)
	}()

	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, host, game.Vertical), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if h.s.stopped {
		t.Fatalf("the game stopped after a local move: %q", h.s.notice)
	}

	deadline := time.After(5 * time.Second)
	for seen := false; !seen; {
		select {
		case ev := <-guestEvents:
			if ev.Kind == netplay.EventMove {
				if ev.Move != "B1" {
					t.Fatalf("the opponent received %q, want B1", ev.Move)
				}
				seen = true
			}
		case <-deadline:
			t.Fatal("the local move never reached the opponent")
		}
	}

	if err := guest.SendMove("F2"); err != nil {
		t.Fatalf("the opponent could not move: %v", err)
	}
	h.waitFor("the opponent's move", func() bool {
		return h.s.g.At(game.Point{Col: 5, Row: 1}) == game.Horizontal
	})
	if h.s.g.Turn() != game.Vertical {
		t.Errorf("turn is %v, want vertical", h.s.g.Turn())
	}
	if h.s.stopped {
		t.Errorf("the game stopped after a legal remote move: %q", h.s.notice)
	}
	h.mustContain("opponent move", "F2")
}

// TestRemoteDisconnectionIsReported requires the player to be told, rather than
// left in front of a board that has quietly stopped answering.
func TestRemoteDisconnectionIsReported(t *testing.T) {
	host, guest := gsPipePair(t, gsRules(6), game.Vertical)
	go func() {
		for range guest.Events() {
		}
	}()

	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, host, game.Vertical), 80, 24)

	if err := guest.Close(); err != nil {
		t.Fatalf("closing the opponent's end: %v", err)
	}
	h.waitFor("the connection to be reported gone", func() bool { return h.s.stopped })
	if !strings.Contains(h.s.notice, "the game is saved and can be resumed") {
		t.Errorf("the notice reads %q, which does not tell the player where they stand", h.s.notice)
	}
	h.mustContain("disconnection notice", "resumed")

	// The screen still answers keys, and refuses to play on.
	h.goTo(game.Point{Col: 3, Row: 3})
	h.press("space")
	if h.s.g.Staged().PegPlaced {
		t.Error("a peg was staged after the connection dropped")
	}
	gsCheckFrame(t, "after disconnection", h.frame(), h.width, h.height)
}

// gsFakeSession is a session under the test's control, for the two cases a real
// pipe cannot produce on demand: an injected divergence, and reading back
// exactly what the screen sent.
type gsFakeSession struct {
	side   game.Player
	rules  game.Ruleset
	events chan netplay.Event

	mu     sync.Mutex
	sent   []string
	closed bool
}

func newGSFakeSession(side game.Player, rs game.Ruleset) *gsFakeSession {
	return &gsFakeSession{side: side, rules: rs, events: make(chan netplay.Event, 8)}
}

func (f *gsFakeSession) Side() game.Player            { return f.side }
func (f *gsFakeSession) Rules() game.Ruleset          { return f.rules }
func (f *gsFakeSession) OpponentName() string         { return "linus" }
func (f *gsFakeSession) Events() <-chan netplay.Event { return f.events }
func (f *gsFakeSession) SendResign() error            { return f.SendMove("resign") }
func (f *gsFakeSession) SendDrawOffer() error         { return f.SendMove("draw?") }
func (f *gsFakeSession) SendDrawAccept() error        { return f.SendMove("draw!") }

func (f *gsFakeSession) SendMove(notation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, notation)
	return nil
}

func (f *gsFakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
	return nil
}

func (f *gsFakeSession) sentMoves() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// TestRemoteMoveIsSentAsNotation checks the wire form of a local move, since a
// notation the opponent cannot replay is a divergence waiting to happen.
func TestRemoteMoveIsSentAsNotation(t *testing.T) {
	session := newGSFakeSession(game.Vertical, gsRules(6))
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, session, game.Vertical), 80, 24)

	h.playTurn(game.Point{Col: 1, Row: 0})
	if got := session.sentMoves(); len(got) != 1 || got[0] != "B1" {
		t.Fatalf("the screen sent %v, want [B1]", got)
	}

	h.press("d")
	if got := session.sentMoves(); len(got) != 2 || got[1] != "draw?" {
		t.Fatalf("the screen sent %v, want a draw offer second", got)
	}
}

// TestDivergenceStopsTheGame covers the one failure it would be dishonest to
// play through: the two ends no longer agree about the position.
func TestDivergenceStopsTheGame(t *testing.T) {
	session := newGSFakeSession(game.Vertical, gsRules(6))
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, session, game.Vertical), 80, 24)

	session.events <- netplay.Event{Kind: netplay.EventError, Err: netplay.ErrDiverged, Text: "diverged"}
	h.waitFor("the divergence to stop the game", func() bool { return h.s.stopped })
	if !strings.Contains(h.s.notice, "no longer hold the same position") {
		t.Errorf("the notice reads %q, which does not say what went wrong", h.s.notice)
	}
	h.mustContain("divergence notice", "same position")

	h.goTo(game.Point{Col: 3, Row: 3})
	h.press("space")
	if h.s.g.Staged().PegPlaced {
		t.Error("play carried on from a position the two ends disagree about")
	}
	if got := session.sentMoves(); len(got) != 0 {
		t.Errorf("moves were still sent after the divergence: %v", got)
	}
}

// TestRemoteResignationEndsTheGame covers the opponent conceding over the wire.
func TestRemoteResignationEndsTheGame(t *testing.T) {
	session := newGSFakeSession(game.Vertical, gsRules(6))
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, session, game.Vertical), 80, 24)

	session.events <- netplay.Event{Kind: netplay.EventResign}
	h.waitFor("the resignation to end the game", func() bool { return h.s.g.Result().Over() })

	res := h.s.g.Result()
	if res.Reason != game.Resignation || res.Winner() != game.Vertical {
		t.Fatalf("result is %v/%v, want vertical winning by resignation", res.Outcome, res.Reason)
	}
	rows := d.Board.History("ada", 0)
	if len(rows) != 1 {
		t.Fatalf("%d leaderboard rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Outcome != leaderboard.Win {
		t.Errorf("outcome is %q, want win", rows[0].Outcome)
	}
	if want := leaderboard.RemoteName("linus"); rows[0].Opponent != want {
		t.Errorf("opponent is %q, want %q", rows[0].Opponent, want)
	}
}

// --- construction and keys --------------------------------------------------

func TestNewGameScreenRefusesImpossibleConfigurations(t *testing.T) {
	d := gsTestDeps(t)
	cases := []struct {
		name string
		cfg  GameConfig
		want string
	}{
		{"no seats", GameConfig{Rules: gsRules(6)}, "no seat"},
		{"nobody at the keyboard", GameConfig{
			Rules: gsRules(6),
			Seats: map[game.Player]Seat{
				game.Vertical:   {Bot: &stubBot{}},
				game.Horizontal: {Bot: &stubBot{}},
			},
		}, "neither side is played"},
		{"remote seat without a session", GameConfig{
			Rules: gsRules(6),
			Seats: map[game.Player]Seat{
				game.Vertical:   {Profile: "ada"},
				game.Horizontal: {Remote: true},
			},
		}, "needs a session"},
		{"session without a remote seat", GameConfig{
			Rules:   gsRules(6),
			Seats:   map[game.Player]Seat{game.Vertical: {Profile: "ada"}, game.Horizontal: {Profile: "linus"}},
			Session: newGSFakeSession(game.Horizontal, gsRules(6)),
		}, "neither seat is remote"},
		{"session on the wrong side", GameConfig{
			Rules: gsRules(6),
			Seats: map[game.Player]Seat{
				game.Vertical:   {Profile: "ada"},
				game.Horizontal: {Remote: true},
			},
			Session: newGSFakeSession(game.Horizontal, gsRules(6)),
		}, "remote seat cannot be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewGameScreen(d, c.cfg)
			if err == nil {
				t.Fatalf("the configuration was accepted, want an error mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error is %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// TestGameKeysDoNotShadowTheBoardKeymap keeps the two tables disjoint where it
// matters. A confirmation intercepts the keyboard before the board keymap is
// consulted, so its keys may repeat a board key; a play-phase key that did so
// would silently take a board action away.
func TestGameKeysDoNotShadowTheBoardKeymap(t *testing.T) {
	km := ui.DefaultKeymap()
	for _, b := range gameBindings {
		if b.phases&phasePlay == 0 {
			continue
		}
		for _, key := range b.keys {
			for _, ctx := range []ui.Context{ui.CtxBoard, ui.CtxLink} {
				if existing, ok := km.Lookup(ctx, key); ok {
					t.Errorf("game key %q also drives board action %v", key, existing.Action)
				}
			}
		}
	}
}

// TestGameKeysAreMultiplexerSafe holds the line the board keymap holds: an
// unmodified printable, or one of the basic special keys. Anything modified is
// not reliably delivered inside a terminal multiplexer.
func TestGameKeysAreMultiplexerSafe(t *testing.T) {
	safe := map[string]bool{"esc": true, "enter": true, "space": true, "tab": true}
	for _, b := range gameBindings {
		for _, key := range b.keys {
			switch {
			case safe[key]:
			case strings.Contains(key, "+"):
				t.Errorf("game key %q is a modified combination", key)
			case len([]rune(key)) != 1:
				t.Errorf("game key %q is neither a single printable nor a basic special key", key)
			}
		}
	}
}

// TestHelpNamesTheKeysThatApply keeps the help honest: it offers the keys that
// work now and not the ones that do not.
func TestHelpNamesTheKeysThatApply(t *testing.T) {
	d := gsTestDeps(t)
	engine := &stubBot{tier: bot.Pro, moves: []game.Point{{Col: 5, Row: 1}}}
	cfg := gsVersusBot(6, engine)
	cfg.Hints, cfg.HintFor = true, engine
	h := newGSHarness(t, d, cfg, 120, 40)

	labels := map[string]bool{}
	for _, e := range h.s.helpEntries() {
		labels[e.Label] = true
	}
	for _, want := range []string{"space", "enter", "x", "a", "q", "?", "d", "r"} {
		if !labels[want] {
			t.Errorf("the help does not mention %q: %v", want, labels)
		}
	}
	if labels["p"] {
		t.Error("the help offers peg removal under rules that forbid it")
	}
	if labels["s"] {
		t.Error("the help offers the swap when it is not available")
	}
}

// TestPegRemovalIsReachableOnlyWhenTheRulesAllowIt covers the opt-in rule: the
// key does what the engine allows, and says so when it does not.
func TestPegRemovalIsReachableOnlyWhenTheRulesAllowIt(t *testing.T) {
	t.Run("refused by the standard rules", func(t *testing.T) {
		d := gsTestDeps(t)
		h := newGSHarness(t, d, gsHotseat(6), 80, 24)
		h.playTurn(game.Point{Col: 1, Row: 0})
		h.playTurn(game.Point{Col: 5, Row: 1})
		h.ready()
		h.goTo(game.Point{Col: 1, Row: 0})
		h.press("p")
		if h.s.g.At(game.Point{Col: 1, Row: 0}) != game.Vertical {
			t.Fatal("a peg was lifted under rules that forbid it")
		}
		if !strings.Contains(h.s.message, "lifting a peg") {
			t.Errorf("the refusal reads %q", h.s.message)
		}
	})

	t.Run("allowed when the ruleset opts in", func(t *testing.T) {
		d := gsTestDeps(t)
		cfg := gsHotseat(6)
		cfg.Rules.PegRemoval = true
		h := newGSHarness(t, d, cfg, 80, 24)
		h.playTurn(game.Point{Col: 1, Row: 0})
		h.playTurn(game.Point{Col: 5, Row: 1})
		h.ready()
		h.goTo(game.Point{Col: 1, Row: 0})
		h.press("p")
		if got := h.s.g.At(game.Point{Col: 1, Row: 0}); got != game.NoPlayer {
			t.Fatalf("the peg was not lifted: hole holds %v (%q)", got, h.s.message)
		}
		h.mustContain("lifted peg", "lifted B1")

		// A turn is still exactly one peg. With nothing staged, enter places
		// rather than commits, so the lift alone can never end a turn.
		h.goTo(game.Point{Col: 3, Row: 3})
		ply := h.s.g.Ply()
		h.press("enter")
		if h.s.g.Ply() != ply {
			t.Fatalf("the turn was committed with no peg placed: ply is %d", h.s.g.Ply())
		}
		if !h.s.g.Staged().PegPlaced {
			t.Fatalf("enter did not place this turn's peg: %q", h.s.message)
		}
		h.press("enter")
		if h.s.g.Ply() != ply+1 {
			t.Fatalf("the turn did not commit: ply is %d, want %d", h.s.g.Ply(), ply+1)
		}
		if h.s.g.At(game.Point{Col: 1, Row: 0}) != game.NoPlayer {
			t.Error("the lifted peg came back on the commit")
		}
		if h.s.g.At(game.Point{Col: 3, Row: 3}) != game.Vertical {
			t.Error("the turn's peg is not on the board after the commit")
		}
	})
}

// TestGameScreenRunsUnderARealProgram is the smoke test: the screen is driven by
// a real Bubble Tea program over a real terminal writer, so the alt-screen view,
// the renderer and the resize path are exercised rather than simulated. The
// screen hands control back with a DoneMsg instead of quitting, so the program
// is stopped by the test.
func TestGameScreenRunsUnderARealProgram(t *testing.T) {
	d := gsTestDeps(t)
	screen, err := NewGameScreen(d, gsHotseat(6))
	if err != nil {
		t.Fatalf("NewGameScreen: %v", err)
	}
	tm := teatest.NewTestModel(t, screen, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "vertical to move")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(gsKeyMsg(t, "space"))
	tm.Send(gsKeyMsg(t, "enter"))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "horizontal's turn")
	}, teatest.WithDuration(5*time.Second))

	// Only a 200-cell terminal draws the board at the detail pitch.
	tm.Send(tea.WindowSizeMsg{Width: 200, Height: 60})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "A   B   C")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(gsKeyMsg(t, "ctrl+c"))
	tm.Quit()
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	// ctrl+c is still a way out of a game, so the position is on disk.
	saved := d.Games.Unfinished()
	if len(saved) != 1 {
		t.Fatalf("%d unfinished games stored after ctrl+c, want 1", len(saved))
	}
	resumed, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the saved game does not replay: %v", err)
	}
	if resumed.PegCount(game.Vertical) != 1 {
		t.Errorf("the saved game holds %d vertical pegs, want 1", resumed.PegCount(game.Vertical))
	}
}

// TestCtrlCSavesBeforeEndingTheProgram covers the exit that is easy to forget:
// killing the program is not abandoning the game.
func TestCtrlCSavesBeforeEndingTheProgram(t *testing.T) {
	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsHotseat(6), 80, 24)
	h.playTurn(game.Point{Col: 1, Row: 0})

	h.press("ctrl+c")
	if len(h.done) != 1 {
		t.Fatalf("ctrl+c produced %d DoneMsg, want 1", len(h.done))
	}
	if !h.done[0].Quit {
		t.Error("ctrl+c did not ask the shell to end the program")
	}
	if h.done[0].Err != nil {
		t.Errorf("ctrl+c reported %v", h.done[0].Err)
	}
	saved := d.Games.Unfinished()
	if len(saved) != 1 {
		t.Fatalf("%d unfinished games stored, want 1", len(saved))
	}
	resumed, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the saved game does not replay: %v", err)
	}
	if resumed.At(game.Point{Col: 1, Row: 0}) != game.Vertical {
		t.Error("the saved game has lost the move that was played")
	}
}
