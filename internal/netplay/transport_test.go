package netplay

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFullGameOverLoopbackTCP uses the exported direct-transport API over a real
// kernel TCP socket. It proves the in-memory pipe result is not an artefact of
// net.Pipe's delivery behaviour, and that binding port 0 exposes the real port
// before Wait blocks for the guest.
func TestFullGameOverLoopbackTCP(t *testing.T) {
	h, err := Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	defer h.Close()
	if strings.HasSuffix(h.Addr(), ":0") {
		t.Fatalf("Addr returned the requested port rather than the bound one: %s", h.Addr())
	}

	type result struct {
		s   Session
		err error
	}
	hostResult := make(chan result, 1)
	go func() {
		s, err := h.Wait(t.Context(), hostOpts())
		hostResult <- result{s, err}
	}()
	guest, err := Dial(t.Context(), h.Addr(), guestOpts())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer guest.Close()
	got := <-hostResult
	if got.err != nil {
		t.Fatalf("accepting: %v", got.err)
	}
	host := got.s
	defer host.Close()

	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	playScript(t, host, guest, scriptedGame)
}

func TestNormalizeAddr(t *testing.T) {
	cases := map[string]string{
		"":                  ":" + DefaultPort,
		"example.test":      "example.test:" + DefaultPort,
		"example.test:7331": "example.test:7331",
		":7331":             ":7331",
		"2001:db8::1":       "[2001:db8::1]:" + DefaultPort,
		"[2001:db8::1]":     "[2001:db8::1]:" + DefaultPort,
	}
	for input, want := range cases {
		if got := NormalizeAddr(input); got != want {
			t.Errorf("NormalizeAddr(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestListenerSurvivesAStrayConnection covers a scanner or mistyped client that
// reaches the port before the invited guest. Only a successful handshake owns
// the one game slot.
func TestListenerSurvivesAStrayConnection(t *testing.T) {
	h, err := Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	defer h.Close()
	type result struct {
		s   Session
		err error
	}
	hostResult := make(chan result, 1)
	go func() {
		s, err := h.Wait(t.Context(), hostOpts())
		hostResult <- result{s: s, err: err}
	}()

	stray, err := net.Dial("tcp", h.Addr())
	if err != nil {
		t.Fatalf("dialling stray client: %v", err)
	}
	_, _ = stray.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	_ = stray.Close()

	guest, err := Dial(t.Context(), h.Addr(), guestOpts())
	if err != nil {
		t.Fatalf("the invited guest could not connect after the stray client: %v", err)
	}
	defer guest.Close()
	select {
	case got := <-hostResult:
		if got.err != nil {
			t.Fatalf("host stopped after the stray client: %v", got.err)
		}
		defer got.s.Close()
		wantEvent(t, got.s, EventConnected)
		wantEvent(t, guest, EventConnected)
	case <-time.After(5 * time.Second):
		t.Fatal("the host never accepted the invited guest")
	}
}

// TestListenCancellation proves a host waiting for an opponent does not leak an
// Accept goroutine when the command is cancelled.
func TestListenCancellation(t *testing.T) {
	h, err := Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	_, err = h.Wait(ctx, hostOpts())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait returned %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelling Wait took %s", elapsed)
	}
}

// TestFullGameThroughRelay starts the relay in-process on loopback and drives
// the same complete game through it. The relay's only contribution is pairing
// by code and copying bytes; if it tried to parse the game this test would not
// be using the same HostOver/JoinOver implementation as the other transports.
func TestFullGameThroughRelay(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.Wait = 5 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- relay.Serve(ctx, l) }()

	code := PairingCode()
	type result struct {
		s   Session
		err error
	}
	hostResult := make(chan result, 1)
	go func() {
		s, err := HostViaRelay(t.Context(), l.Addr().String(), code, hostOpts())
		hostResult <- result{s, err}
	}()
	guest, err := JoinViaRelay(t.Context(), l.Addr().String(), strings.ToLower(code), guestOpts())
	if err != nil {
		t.Fatalf("joining through the relay: %v", err)
	}
	defer guest.Close()
	got := <-hostResult
	if got.err != nil {
		t.Fatalf("hosting through the relay: %v", got.err)
	}
	host := got.s
	defer host.Close()

	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	playScript(t, host, guest, scriptedGame)

	_ = host.Close()
	_ = guest.Close()
	waitUntil(t, func() bool { return relay.Rooms() == 0 }, "the relay room was not released")
	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("relay shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay did not stop")
	}
}

func waitUntil(t *testing.T, fn func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(failure)
}

// TestPairingCodesAreShortAndForgiving covers the shape of a code and the typo
// forgiveness applying to the whole of it. The key part matters as much as the
// room part here: the two ends derive the frame key from it independently, so a
// code typed in lower case with the dashes left out has to produce the same key,
// byte for byte, as the one the host printed.
func TestPairingCodesAreShortAndForgiving(t *testing.T) {
	code := PairingCode()
	room, key, err := splitPairingCode(code)
	if err != nil {
		t.Fatalf("splitting a fresh code %q: %v", code, err)
	}
	if len(room) != pairingRoomLen {
		t.Fatalf("the room part of %q is %q (%d characters)", code, room, len(room))
	}
	if !strings.HasPrefix(code, room) {
		t.Fatalf("the room part %q is not the beginning of %q", room, code)
	}
	if strings.ContainsAny(code, "ILOU") {
		t.Fatalf("PairingCode returned an ambiguous character: %q", code)
	}
	for _, variant := range []string{
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		strings.ReplaceAll(code, "-", " "),
		"  " + strings.ReplaceAll(strings.ToLower(code), "-", ".") + "\n",
		strings.ReplaceAll(strings.ReplaceAll(code, "0", "O"), "1", "I"),
	} {
		gotRoom, gotKey, err := splitPairingCode(variant)
		if err != nil {
			t.Fatalf("splitting %q: %v", variant, err)
		}
		if gotRoom != room {
			t.Fatalf("splitting %q gave room %q, want %q", variant, gotRoom, room)
		}
		if string(gotKey) != string(key) {
			t.Fatalf("splitting %q gave a different frame key", variant)
		}
	}
}

// TestRelayIsADumbBytePump sends bytes that are not protocol frames through a
// paired room. They arrive unchanged, proving the relay does not understand or
// rewrite the game protocol it carries.
func TestRelayIsADumbBytePump(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	relay := NewRelay()
	relay.Logf = t.Logf
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.Serve(ctx, l)

	code := PairingCode()
	type dialled struct {
		c   net.Conn
		err error
	}
	first := make(chan dialled, 1)
	go func() {
		c, err := dialRelay(t.Context(), l.Addr().String(), code)
		first <- dialled{c, err}
	}()
	second, err := dialRelay(t.Context(), l.Addr().String(), code)
	if err != nil {
		t.Fatalf("dialling the second end: %v", err)
	}
	defer second.Close()
	one := <-first
	if one.err != nil {
		t.Fatalf("dialling the first end: %v", one.err)
	}
	defer one.c.Close()

	payload := []byte{0, 1, 2, 3, 0xff, '\n', 'T', 'X'}
	writeErr := make(chan error, 1)
	go func() { writeErr <- writeAll(one.c, payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(second, got); err != nil {
		t.Fatalf("reading relayed bytes: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("writing relayed bytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %v, want %v", got, payload)
	}
}

// TestStaleRelayReleaseCannotDeleteAReplacementRoom reproduces the generation
// race: the second endpoint of an old pair may finish after new clients have
// already reused the same code.
func TestStaleRelayReleaseCannotDeleteAReplacementRoom(t *testing.T) {
	r := NewRelay()
	old := &room{paired: true}
	r.rooms["ROOM"] = old
	r.release("ROOM", old)
	replacement := &room{paired: true}
	r.rooms["ROOM"] = replacement

	// This is the delayed release from the old pair. It must not touch the
	// replacement room that happens to use the same code.
	r.release("ROOM", old)
	if got := r.rooms["ROOM"]; got != replacement || !got.paired {
		t.Fatalf("stale release changed the replacement room: %#v", got)
	}
}

// TestRelayRejectsHostilePrelude bounds the only thing the relay parses. A
// client that sends no valid short JOIN line gets an error instead of holding a
// room or growing a buffer without limit.
func TestRelayRejectsHostilePrelude(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	relay := NewRelay()
	relay.Logf = t.Logf
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.Serve(ctx, l)

	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer c.Close()
	if err := writeAll(c, []byte(strings.Repeat("X", relayMaxPrelude+20)+"\n")); err != nil {
		t.Fatalf("writing hostile prelude: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	if !strings.Contains(line, relayErr) || !strings.Contains(line, "longer than") {
		t.Fatalf("the relay answered %q", line)
	}
	if relay.Rooms() != 0 {
		t.Fatalf("the hostile client created %d rooms", relay.Rooms())
	}
}

// TestRelayBoundsAdmittedConnections keeps one waiting room from allowing an
// unbounded number of sockets, timers and handler goroutines.
func TestRelayBoundsAdmittedConnections(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.MaxConnections = 1
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.Serve(ctx, l)

	first, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dialling first client: %v", err)
	}
	defer first.Close()
	if err := writeRelayLine(first, relayJoin+" "+PairingCode()); err != nil {
		t.Fatalf("joining first room: %v", err)
	}

	second, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dialling second client: %v", err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(second).ReadString('\n')
	if err != nil {
		t.Fatalf("reading capacity refusal: %v", err)
	}
	if !strings.Contains(line, relayErr) || !strings.Contains(line, "busy") {
		t.Fatalf("relay answered %q", line)
	}
}

// TestRelayClosesAnIdlePair ensures two silent endpoints cannot occupy active
// relay slots forever. Real sessions stay alive by carrying protocol pings.
func TestRelayClosesAnIdlePair(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.IdleTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.Serve(ctx, l)

	code := PairingCode()
	type dialled struct {
		c   net.Conn
		err error
	}
	first := make(chan dialled, 1)
	go func() {
		c, err := dialRelay(t.Context(), l.Addr().String(), code)
		first <- dialled{c: c, err: err}
	}()
	second, err := dialRelay(t.Context(), l.Addr().String(), code)
	if err != nil {
		t.Fatalf("dialling second end: %v", err)
	}
	defer second.Close()
	one := <-first
	if one.err != nil {
		t.Fatalf("dialling first end: %v", one.err)
	}
	defer one.c.Close()

	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("the silent pair remained open")
	}
	waitUntil(t, func() bool { return relay.Rooms() == 0 }, "idle room was not released")
}

// TestTwoSimultaneousGuestsLeaveExactlyOneSession covers two invited-looking
// guests completing their handshakes at the same moment. Exactly one game is on
// offer, so exactly one session may survive and the loser's connection must be
// closed.
//
// The handover used to be a buffered channel with a non-blocking send, which
// cannot decide this: once Wait has taken the first session the buffer is empty
// again, so a handshake finishing a moment later sent into it successfully,
// believed it had won, and was never closed by anybody. That left a live socket
// and its reader goroutine behind for as long as the process ran, and left the
// second guest sitting in a game the host was not playing.
func TestTwoSimultaneousGuestsLeaveExactlyOneSession(t *testing.T) {
	// Repeated because the defect is a race: one round may not interleave.
	for round := range 20 {
		h, err := Bind("127.0.0.1:0")
		if err != nil {
			t.Fatalf("round %d: binding: %v", round, err)
		}

		type result struct {
			s   Session
			err error
		}
		hostResult := make(chan result, 1)
		go func() {
			s, err := h.Wait(t.Context(), hostOpts())
			hostResult <- result{s, err}
		}()

		// Both guests dial from a standing start so their handshakes overlap.
		var start sync.WaitGroup
		start.Add(1)
		guests := make(chan result, 2)
		for range 2 {
			go func() {
				start.Wait()
				s, err := Dial(t.Context(), h.Addr(), guestOpts())
				guests <- result{s, err}
			}()
		}
		start.Done()

		host := <-hostResult
		if host.err != nil {
			h.Close()
			t.Fatalf("round %d: the host accepted nobody: %v", round, host.err)
		}

		// Collect both guests, then have the host play one move and see which
		// of them is in the game the host is actually playing.
		var live []Session
		var refused int
		for range 2 {
			g := <-guests
			if g.err != nil {
				// A refusal before the handshake finished is a legitimate way
				// to lose, and is not the leak this test is about.
				refused++
				continue
			}
			live = append(live, g.s)
		}
		if err := host.s.SendMove("B1"); err != nil {
			t.Fatalf("round %d: the host could not play: %v", round, err)
		}
		inGame, ended, silent := classify(live, "B1")
		for _, s := range live {
			s.Close()
		}
		host.s.Close()
		h.Close()

		if inGame != 1 {
			t.Fatalf("round %d: %d guests are in the host's game, want exactly 1 (%d ended, %d silent, %d refused)",
				round, inGame, ended, silent, refused)
		}
		// This is the leak. A guest that finished its handshake and lost must be
		// closed, so its stream ends and it can say so. One that is merely
		// forgotten hears nothing: the host holds a socket and a reader
		// goroutine for a game it is not playing, and the player is left staring
		// at a board waiting for a move that will never come.
		if silent != 0 {
			t.Fatalf("round %d: %d guest(s) finished the handshake and were then neither played nor closed",
				round, silent)
		}
	}
}

// classify reports, for sessions that completed a handshake, how many received
// the host's move, how many had their stream ended, and how many heard nothing
// at all before the deadline.
func classify(sessions []Session, move string) (inGame, ended, silent int) {
	var wg sync.WaitGroup
	const (
		unknown = iota
		got
		over
		quiet
	)
	out := make([]int, len(sessions))
	for i, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.After(2 * time.Second)
			for {
				select {
				case ev, ok := <-s.Events():
					if !ok {
						out[i] = over
						return
					}
					switch ev.Kind {
					case EventMove:
						if ev.Move == move {
							out[i] = got
						} else {
							out[i] = quiet
						}
						return
					case EventDisconnected, EventError:
						out[i] = over
						return
					}
				case <-deadline:
					out[i] = quiet
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, r := range out {
		switch r {
		case got:
			inGame++
		case over:
			ended++
		default:
			silent++
		}
	}
	return inGame, ended, silent
}
