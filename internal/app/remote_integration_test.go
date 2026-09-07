package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/netplay"
)

func reconnectRow(t *testing.T, m *Menu, contains string) tea.Cmd {
	t.Helper()
	c := mnChooser(t, m)
	for range len(c.opts) {
		if strings.Contains(c.opts[c.sel].label, contains) {
			return shellSend(t, m, "enter")
		}
		shellSend(t, m, "down")
	}
	t.Fatalf("no reconnect row contains %q", contains)
	return nil
}

func beginReconnection(t *testing.T, m *Menu, opponent, addr string) menuSessionMsg {
	t.Helper()
	mnPick(t, m, "Continue a saved game")
	reconnectRow(t, m, opponent)
	mnPick(t, m, "connect to their address")
	mnTypeInto(t, m, addr)
	cmd := shellSend(t, m, "enter")
	if cmd == nil {
		t.Fatal("the address did not start the connection")
	}
	msg, ok := cmd().(menuSessionMsg)
	if !ok || msg.err != nil {
		t.Fatalf("reconnection result: message=%+v ok=%t", msg, ok)
	}
	t.Cleanup(func() { _ = msg.session.Close() })
	return msg
}

func TestLateReconnectCannotReplaceAnotherSavedGame(t *testing.T) {
	d := shellTestDeps(t)
	a := rmSave(t, d, "Balint", "Zsofia", "B1", "A2")
	b := rmSave(t, d, "Balint", "Reka")
	a.Side, b.Side = "horizontal", "horizontal"
	for _, saved := range []gamestore.Saved{a, b} {
		if err := d.Games.Put(saved); err != nil {
			t.Fatal(err)
		}
	}
	longA := a
	g, err := a.Game()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("C3"); err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	longA.Record = rec.Encode()
	addrA := rmListen(t, longA, game.Vertical, "Zsofia", "Balint")
	addrB := rmListen(t, b, game.Vertical, "Reka", "Balint")

	m := mnMenu(t, d, 100, 30)
	resultA := beginReconnection(t, m, "Zsofia", addrA)
	// A completed, but its result is still queued when the user cancels and
	// chooses B. Both successful results must retain their original identity.
	shellSend(t, m, "esc")
	resultB := beginReconnection(t, m, "Reka", addrB)
	beforeB, err := d.Games.Get(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, unexpected := m.Update(resultA)
	if unexpected != nil {
		t.Fatal("a stale connection result tried to open a game")
	}
	if err := resultA.session.SendMove("B3"); !errors.Is(err, netplay.ErrClosed) {
		t.Fatalf("the stale session was not closed: %v", err)
	}
	afterB, err := d.Games.Get(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterB.Record != beforeB.Record {
		t.Fatal("game A's delayed result rewrote game B")
	}
	if _, ok := m.form.(*waitForm); !ok {
		t.Fatal("the stale result dismissed game B's waiting form")
	}
	_, openB := m.Update(resultB)
	cfg := mnStartedConfig(t, openB)
	if cfg.StoreID != b.ID || cfg.Seats[game.Vertical].Label != "Reka" {
		t.Fatalf("the current attempt opened the wrong game: %+v", cfg)
	}
	if stored, err := d.Games.Get(a.ID); err != nil || stored.Record != a.Record {
		t.Fatalf("the cancelled game was modified: %v", err)
	}
}

func replayBacklogScreen(t *testing.T) (*gameScreen, netplay.Session, gamestore.Saved, *game.Game) {
	t.Helper()
	d := shellTestDeps(t)
	short := rmSave(t, d, "Balint", "Zsofia", "B1", "A2")
	short.Side = "horizontal"
	if err := d.Games.Put(short); err != nil {
		t.Fatal(err)
	}
	long := short
	g, err := short.Game()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("C3"); err != nil {
		t.Fatal(err)
	}
	if err := g.OfferDraw(game.Vertical); err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	long.Record = rec.Encode()
	addr := rmListen(t, long, game.Vertical, "Zsofia", "Balint")
	resume, err := PrepareRemoteResume(short, "Balint")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := netplay.Dial(ctx, addr, resume.Guest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	s, err := newGameScreen(d, resume.Continue(session))
	if err != nil {
		t.Fatal(err)
	}
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return s, session, short, g
}

func consumeRemoteEvent(t *testing.T, session netplay.Session, want netplay.EventKind) netplay.Event {
	t.Helper()
	select {
	case ev, ok := <-session.Events():
		if !ok || ev.Kind != want {
			t.Fatalf("event %+v, want %s (open=%t)", ev, want, ok)
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event", want)
		return netplay.Event{}
	}
}

func TestReconnectReplaysMoveAndDrawOfferWithoutFalseDivergence(t *testing.T) {
	s, session, saved, expected := replayBacklogScreen(t)
	for _, kind := range []netplay.EventKind{netplay.EventConnected, netplay.EventMove, netplay.EventDrawOffer} {
		ev := consumeRemoteEvent(t, session, kind)
		s.Update(netEventMsg{ev: ev})
		if s.stopped {
			t.Fatalf("stopped after %s before consuming the valid backlog: %s", kind, s.notice)
		}
	}
	if netplay.PositionHash(s.g) != netplay.PositionHash(expected) || s.g.DrawOfferedBy() != game.Vertical {
		t.Fatal("the complete replay did not reach the offered-draw position")
	}
	stored, err := s.deps.Games.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := stored.Game()
	if err != nil || persisted.Entries() != expected.Entries() || persisted.DrawOfferedBy() != game.Vertical {
		t.Fatalf("the reconciled backlog was not saved: %v", err)
	}
	if len(s.deps.Games.List()) != 1 {
		t.Fatal("replaying the backlog created another saved row")
	}
}

func TestInvalidEventCheckpointDoesNotReachTheSavedGame(t *testing.T) {
	s, session, saved, _ := replayBacklogScreen(t)
	s.Update(netEventMsg{ev: consumeRemoteEvent(t, session, netplay.EventConnected)})
	ev := consumeRemoteEvent(t, session, netplay.EventMove)
	ev.PositionHash = "not-the-checked-position"
	s.Update(netEventMsg{ev: ev})
	if !s.stopped {
		t.Fatal("a mismatched event checkpoint did not stop play")
	}
	stored, err := s.deps.Games.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Record != saved.Record || s.g.Entries() != 2 {
		t.Fatal("an unverified event overwrote the last agreed game")
	}
}

func TestQueuedDrawOfferPreservesTheLocalStagedTurn(t *testing.T) {
	s, session, saved, expected := replayBacklogScreen(t)
	for _, kind := range []netplay.EventKind{netplay.EventConnected, netplay.EventMove} {
		s.Update(netEventMsg{ev: consumeRemoteEvent(t, session, kind)})
	}
	peg := game.Point{Col: 3, Row: 3}
	if err := s.g.PlacePeg(peg); err != nil {
		t.Fatal(err)
	}
	s.Update(netEventMsg{ev: consumeRemoteEvent(t, session, netplay.EventDrawOffer)})
	if s.stopped || !s.g.Staged().PegPlaced || s.g.Staged().Peg != peg {
		t.Fatal("an off-turn draw offer stopped play or discarded the local staged peg")
	}
	stored, err := s.deps.Games.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := stored.Game()
	if err != nil || netplay.PositionHash(persisted) != netplay.PositionHash(expected) {
		t.Fatalf("the saved game includes uncommitted edits or lost the draw offer: %v", err)
	}
}
