package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/netplay"
	"testing"
	"time"
)

type reviewAfterSend struct {
	netplay.Resumable
	after <-chan error
}

func (r reviewAfterSend) SendMove(n string) error {
	if err := r.Resumable.SendMove(n); err != nil {
		return err
	}
	select {
	case err := <-r.after:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("peer did not advance while SendMove returned")
	}
}
func reviewReadEvent(s netplay.Session, kind netplay.EventKind) (netplay.Event, error) {
	select {
	case e, ok := <-s.Events():
		if !ok || e.Kind != kind {
			return e, fmt.Errorf("got event %+v open=%t, want %s", e, ok, kind)
		}
		return e, nil
	case <-time.After(5 * time.Second):
		return netplay.Event{}, fmt.Errorf("missing %s", kind)
	}
}
func TestLocalSendWithTwoQueuedPeerEntries(t *testing.T) {
	host, guest := gsPipePair(t, gsRules(8), game.Vertical)
	for _, s := range []netplay.Session{host, guest} {
		if _, err := reviewReadEvent(s, netplay.EventConnected); err != nil {
			t.Fatal(err)
		}
	}
	after := make(chan error, 1)
	events := make(chan netplay.Event, 2)
	go func() {
		if _, err := reviewReadEvent(guest, netplay.EventMove); err != nil {
			after <- err
			return
		}
		if err := guest.SendMove("A2"); err != nil {
			after <- err
			return
		}
		ev, err := reviewReadEvent(host, netplay.EventMove)
		if err != nil {
			after <- err
			return
		}
		events <- ev
		if err := guest.SendDrawOffer(); err != nil {
			after <- err
			return
		}
		ev, err = reviewReadEvent(host, netplay.EventDrawOffer)
		if err != nil {
			after <- err
			return
		}
		events <- ev
		after <- nil
	}()
	r := reviewAfterSend{Resumable: host.(netplay.Resumable), after: after}
	s, err := newGameScreen(shellTestDeps(t), RemoteConfig("ada", r))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.g.PlacePeg(game.Point{Col: 1, Row: 0}); err != nil {
		t.Fatal(err)
	}
	s.commitTurn()
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if s.stopped {
		t.Fatalf("local send stopped after peer advanced: %s", s.notice)
	}
	if s.g.Entries() != 1 || r.Position().Entries() != 3 {
		t.Fatalf("UI/session entries %d/%d, want 1/3", s.g.Entries(), r.Position().Entries())
	}
	for i := range 2 {
		ev := <-events
		if ev.PositionHash == "" || ev.Entries != i+2 {
			t.Fatalf("live checkpoint missing or wrong: %+v", ev)
		}
		s.Update(netEventMsg{ev: ev})
		if s.stopped {
			t.Fatalf("queued %s stopped: %s", ev.Kind, s.notice)
		}
		saved, err := s.deps.Games.Get(s.storeID)
		if err != nil {
			t.Fatal(err)
		}
		g, err := saved.Game()
		if err != nil {
			t.Fatal(err)
		}
		if g.Entries() != ev.Entries || netplay.PositionHash(g) != ev.PositionHash {
			t.Fatalf("save after %s is not its verified checkpoint", ev.Kind)
		}
	}
	if netplay.PositionHash(s.g) != netplay.PositionHash(r.Position()) {
		t.Fatal("final live position differs")
	}
	s.g = game.MustNew(gsRules(8))
	if err := s.g.PlayNotation("C3"); err != nil {
		t.Fatal(err)
	}
	if s.checkSync() || !s.stopped {
		t.Fatal("trimming later entries suppressed a real prefix mismatch")
	}
	if r.Position().Entries() != 3 {
		t.Fatal("checking a prefix rewound the real session")
	}
}

func TestStaleResultLeavesThePendingAttemptCancellable(t *testing.T) {
	m := mnMenu(t, shellTestDeps(t), 100, 30)
	old := newGSFakeSession(game.Vertical, gsRules(8))
	cmdA := m.connectAs("A", nil, func(context.Context) (netplay.Session, error) { return old, nil })
	msgA := cmdA().(menuSessionMsg)
	shellSend(t, m, "esc")
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	cmdB := m.connectAs("B", nil, func(ctx context.Context) (netplay.Session, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	doneB := make(chan menuSessionMsg, 1)
	go func() { doneB <- cmdB().(menuSessionMsg) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("B did not start")
	}
	m.Update(msgA)
	if !old.closed {
		t.Fatal("stale A session was not closed")
	}
	if m.cancelWait == nil {
		t.Fatal("stale A cleared B cancellation")
	}
	shellSend(t, m, "esc")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("escape failed to cancel still-pending B")
	}
	select {
	case msgB := <-doneB:
		if !errors.Is(msgB.err, context.Canceled) {
			t.Fatalf("B result %v", msgB.err)
		}
		_, cmd := m.Update(msgB)
		if cmd != nil {
			t.Fatal("cancelled B opened a game")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B never returned")
	}
}
