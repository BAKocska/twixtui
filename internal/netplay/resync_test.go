package netplay

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dropConn is a connection that can acknowledge writes without putting their
// bytes on the wire. That is the hard disconnect case for resync: one player
// committed a move locally, the last frame disappeared with the connection,
// and the other player's record is one entry behind.
type dropConn struct {
	net.Conn
	drop atomic.Bool
}

func (c *dropConn) Write(p []byte) (int, error) {
	if c.drop.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

// TestDisconnectThenResync loses the fifth move, tears the connection down,
// reconnects, and proves the protocol replays just that missing move before
// ordinary play resumes. The two saved records are compared by digest first;
// this is not a blind state overwrite.
func TestDisconnectThenResync(t *testing.T) {
	hc, gc := net.Pipe()
	drop := &dropConn{Conn: hc}
	host, guest, hostErr, guestErr := connectOver(t, drop, gc, hostOpts(), guestOpts())
	if hostErr != nil || guestErr != nil {
		t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)

	// Four entries arrive normally.
	playScript(t, host, guest, scriptedGame[:4])

	// The fifth is committed by the host and accepted by the socket wrapper,
	// but its frame never reaches the guest.
	drop.drop.Store(true)
	if err := host.SendMove(scriptedGame[4].Move); err != nil {
		t.Fatalf("committing the move lost at disconnect: %v", err)
	}
	hostSaved, err := Save(host)
	if err != nil {
		t.Fatalf("saving the host: %v", err)
	}
	guestSaved, err := Save(guest)
	if err != nil {
		t.Fatalf("saving the guest: %v", err)
	}
	if len(hostSaved.Moves) != len(guestSaved.Moves)+1 {
		t.Fatalf("host saved %d entries, guest %d; the frame was not actually lost", len(hostSaved.Moves), len(guestSaved.Moves))
	}
	if got := hostSaved.Moves[len(hostSaved.Moves)-1].Move; got != "D5" {
		t.Fatalf("the lost entry was %q", got)
	}
	_ = hc.Close()
	_ = gc.Close()

	// Reconnect over a fresh transport with each end's own saved record.
	rh, rg := net.Pipe()
	hopts := hostOpts()
	hopts.Resume = &hostSaved
	gopts := guestOpts()
	gopts.Resume = &guestSaved
	resumedHost, resumedGuest, hostErr, guestErr := connectOver(t, rh, rg, hopts, gopts)
	if hostErr != nil || guestErr != nil {
		t.Fatalf("resume failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, resumedHost, EventConnected)
	wantEvent(t, resumedGuest, EventConnected)

	// The behind end gets the missing entry as an ordinary UI event immediately
	// after the connection event.
	replayed := wantEvent(t, resumedGuest, EventMove)
	if replayed.Move != "D5" || !strings.Contains(replayed.Text, "replayed") {
		t.Fatalf("the guest got %+v for the missing entry", replayed)
	}
	assertAgree(t, resumedHost, resumedGuest, 5)

	// Nothing special remains after resync: play the rest of the game.
	playScriptFrom(t, resumedHost, resumedGuest, scriptedGame[5:], 5)
}

// playScriptFrom is playScript for a resumed game whose record already holds
// offset entries.
func playScriptFrom(t *testing.T, a, b Session, moves []Entry, offset int) {
	t.Helper()
	for i, m := range moves {
		sender, receiver := a, b
		if sender.Side() != m.Side {
			sender, receiver = b, a
		}
		if err := sender.SendMove(m.Move); err != nil {
			t.Fatalf("entry %d (%s %s): %v", offset+i+1, m.Side, m.Move, err)
		}
		ev := wantEvent(t, receiver, EventMove)
		if ev.Move != m.Move {
			t.Fatalf("entry %d: receiver saw %q, want %q", offset+i+1, ev.Move, m.Move)
		}
		assertAgree(t, a, b, offset+i+1)
	}
}

// TestResyncRefusesDivergentTranscripts gives the two ends records of the same
// length but with different second moves. The reconnect must not choose one as
// authoritative: it refuses both, naming the divergent transcript.
func TestResyncRefusesDivergentTranscripts(t *testing.T) {
	hostSaved := Snapshot{
		Role:  Host,
		Rules: testRules(),
		Side:  1,
		Name:  "ada",
		Moves: []Entry{v("B1"), h("A2")},
	}
	guestSaved := Snapshot{
		Role:  Guest,
		Rules: testRules(),
		Side:  2,
		Name:  "grace",
		Moves: []Entry{v("B1"), h("F2")},
	}
	hopts := hostOpts()
	hopts.Resume = &hostSaved
	gopts := guestOpts()
	gopts.Resume = &guestSaved
	_, _, hostErr, guestErr := connectPipe(t, hopts, gopts)
	for end, err := range map[string]error{"host": hostErr, "guest": guestErr} {
		if !errors.Is(err, ErrDiverged) {
			t.Errorf("%s returned %v, want a divergence", end, err)
			continue
		}
		if !strings.Contains(err.Error(), "moves played were not the same") {
			t.Errorf("%s returned an unhelpful error: %v", end, err)
		}
	}
}

// TestResyncRejectsEntriesAsTheLocalSide prevents an opponent from using an
// unkeyed replay to resign or move on this player's behalf.
func TestResyncRejectsEntriesAsTheLocalSide(t *testing.T) {
	prefix := []Entry{v("B1"), h("A2")}
	s := &session{
		side:  2,
		game:  build(t, prefix...),
		moves: append([]Entry(nil), prefix...),
	}
	forged := append(append([]Entry(nil), prefix...), h("h:resign"))
	final := build(t, forged...)
	_, err := s.absorb(message{
		Type:    msgResync,
		Entries: len(forged),
		PosHash: PositionHash(final),
		Digest:  transcriptDigest(forged),
		Replay:  []wireEntry{{Side: "horizontal", Move: "h:resign"}},
	})
	if !errors.Is(err, ErrProtocol) || !strings.Contains(err.Error(), "local player's behalf") && !strings.Contains(err.Error(), "this end") {
		t.Fatalf("absorb returned %v", err)
	}
	if s.game.Entries() != len(prefix) || len(s.moves) != len(prefix) {
		t.Fatalf("forged replay mutated the game or transcript")
	}
}

// TestResyncChecksFinalTranscriptDigest ensures a replay that happens to reach
// the advertised position still fails if it is not the transcript announced
// in the handshake. Position equality is not transcript equality.
func TestResyncChecksFinalTranscriptDigest(t *testing.T) {
	prefix := []Entry{v("B1"), h("A2")}
	s := &session{
		side:  2,
		game:  build(t, prefix...),
		moves: append([]Entry(nil), prefix...),
	}
	final := build(t, v("B1"), h("A2"), v("C3"))
	_, err := s.absorb(message{
		Type:    msgResync,
		Entries: 3,
		PosHash: PositionHash(final),
		Digest:  strings.Repeat("0", 64),
		Replay:  []wireEntry{{Side: "vertical", Move: "C3"}},
	})
	if !errors.Is(err, ErrDiverged) || !strings.Contains(err.Error(), "different transcript") {
		t.Fatalf("absorb returned %v", err)
	}
}

// TestResyncRefusesDifferentPrefixes gives one end a longer record whose shared
// prefix already disagrees with the shorter one. Missing moves are sent only
// after the prefix digest has proved they belong to the same game.
func TestResyncRefusesDifferentPrefixes(t *testing.T) {
	hostSaved := Snapshot{
		Role:  Host,
		Rules: testRules(),
		Side:  1,
		Name:  "ada",
		Moves: []Entry{v("B1"), h("A2"), v("C3")},
	}
	guestSaved := Snapshot{
		Role:  Guest,
		Rules: testRules(),
		Side:  2,
		Name:  "grace",
		Moves: []Entry{v("C3"), h("A2")},
	}
	hopts := hostOpts()
	hopts.Resume = &hostSaved
	gopts := guestOpts()
	gopts.Resume = &guestSaved

	// The short end waits for a resync frame while the long end refuses before
	// sending one. Closing the transport lets the short end return too.
	hc, gc := net.Pipe()
	errs := make(chan error, 2)
	go func() {
		_, err := HostOver(t.Context(), hc, hopts)
		errs <- err
		_ = hc.Close()
	}()
	go func() {
		_, err := JoinOver(t.Context(), gc, gopts)
		errs <- err
		_ = gc.Close()
	}()
	found := false
	for range 2 {
		select {
		case err := <-errs:
			if errors.Is(err, ErrDiverged) && strings.Contains(err.Error(), "moves up to there were not the same") {
				found = true
			}
		case <-time.After(5 * time.Second):
			t.Fatal("resync did not finish")
		}
	}
	if !found {
		t.Fatal("neither end detected the divergent prefix")
	}
}
