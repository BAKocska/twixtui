package netplay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BAKocska/twixtui/internal/game"
)

// The tests here are the attacks from the security review, turned round: each one
// reproduces what used to work and asserts the refusal that stops it now.

// pumped joins two connections through a byte pump that may rewrite what passes
// from host to guest. That is a relay's position exactly: it carries bytes and is
// free to carry different ones.
func pumped(t *testing.T, rewrite func([]byte) []byte) (net.Conn, net.Conn) {
	t.Helper()
	hostSide, hostPump := net.Pipe()
	guestPump, guestSide := net.Pipe()
	go pumpBytes(hostPump, guestPump, rewrite)
	go pumpBytes(guestPump, hostPump, nil)
	t.Cleanup(func() { _ = hostSide.Close(); _ = guestSide.Close() })
	return hostSide, guestSide
}

func pumpBytes(from, to net.Conn, rewrite func([]byte) []byte) {
	buf := make([]byte, 64<<10)
	for {
		n, err := from.Read(buf)
		if n > 0 {
			out := buf[:n]
			if rewrite != nil {
				out = rewrite(out)
			}
			if _, werr := to.Write(out); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// relayed runs a whole relayed game through such a pump. Both ends hold the key
// from one pairing code; the pump holds nothing, which is one thing less than a
// real relay, and a real relay's room part buys it no ability the pump lacks.
func relayed(t *testing.T, rewrite func([]byte) []byte) (host, guest Session) {
	t.Helper()
	_, key, err := splitPairingCode(PairingCode())
	if err != nil {
		t.Fatalf("splitting a fresh pairing code: %v", err)
	}
	hostSide, guestSide := pumped(t, rewrite)
	type result struct {
		s   Session
		err error
	}
	hostCh := make(chan result, 1)
	go func() {
		s, err := hostOverKeyed(t.Context(), hostSide, hostOpts(), key)
		hostCh <- result{s, err}
	}()
	guest, guestErr := joinOverKeyed(t.Context(), guestSide, guestOpts(), key)
	got := <-hostCh
	if guestErr != nil || got.err != nil {
		t.Fatalf("the handshake through the pump failed: host %v, guest %v", got.err, guestErr)
	}
	t.Cleanup(func() { _ = got.s.Close(); _ = guest.Close() })
	wantEvent(t, got.s, EventConnected)
	wantEvent(t, guest, EventConnected)
	return got.s, guest
}

// TestARewritingRelayCannotForgeAnEntry is the review's own attack. A byte pump
// in the relay's position replaces the host's move with something it built
// itself, complete with the entry count and position hash the receiver checks.
// The victim's engine used to accept it: a relay operator could end the game as
// either player, because nothing in the protocol was signed.
func TestARewritingRelayCannotForgeAnEntry(t *testing.T) {
	resigned := func(t *testing.T) *game.Game {
		t.Helper()
		g := game.MustNew(testRules())
		if err := g.Resign(game.Vertical); err != nil {
			t.Fatalf("resigning: %v", err)
		}
		return g
	}
	cases := []struct {
		name  string
		forge func(*testing.T) []byte
	}{
		{
			name: "a resignation the host never made",
			forge: func(t *testing.T) []byte {
				g := resigned(t)
				return framedMessage(t, message{Type: msgResign, Entries: g.Entries(), PosHash: PositionHash(g)})
			},
		},
		{
			name: "a different move from the one the host played",
			forge: func(t *testing.T) []byte {
				g := build(t, v("C3"))
				return framedMessage(t, message{Type: msgMove, Move: "C3", Entries: g.Entries(), PosHash: PositionHash(g)})
			},
		},
		{
			name: "a resignation authenticated with a key the relay made up",
			forge: func(t *testing.T) []byte {
				g := resigned(t)
				m := message{Type: msgResign, Entries: g.Entries(), PosHash: PositionHash(g)}
				covered, err := authBytes(m)
				if err != nil {
					t.Fatalf("encoding the forgery: %v", err)
				}
				// A relay is told the room and never the key part, so the best
				// key it can derive is one from something else entirely.
				m.MAC = frameMAC(deriveFrameKey("ZZZZZZ", "GUESSGUESSGUESS0"), dirHost, 1, covered)
				return framedMessage(t, m)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			forged := c.forge(t)
			swapped := false
			host, guest := relayed(t, func(b []byte) []byte {
				if !swapped && bytes.Contains(b, []byte(`"t":"move"`)) {
					swapped = true
					return forged
				}
				return b
			})
			if err := host.SendMove("B1"); err != nil {
				t.Fatalf("the host could not open: %v", err)
			}
			ev := wantEvent(t, guest, EventError)
			if !errors.Is(ev.Err, ErrUnauthenticated) {
				t.Fatalf("the guest reported %v, want an authentication failure", ev.Err)
			}
			if !swapped {
				t.Fatal("the pump never saw the move frame, so nothing was forged")
			}
			if got := positionOf(t, guest); got.Result().Over() {
				t.Fatalf("the forgery ended the guest's game anyway: %v", got.Result())
			}
			assertSafeForATerminal(t, "the refusal", ev.Text, 512)
		})
	}
}

// TestARelayStillReadsEverything pins the other half of what relay.go now claims.
// The key authenticates a relayed game, it does not hide it. If this ever stops
// being true the documentation has to change with it, and if it silently became
// false the documentation would be overstating the exposure instead of
// understating it, which is the better direction but still wrong.
func TestARelayStillReadsEverything(t *testing.T) {
	var mu sync.Mutex
	var carried []byte
	host, guest := relayed(t, func(b []byte) []byte {
		mu.Lock()
		carried = append(carried, b...)
		mu.Unlock()
		return b
	})
	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("the host could not open: %v", err)
	}
	wantEvent(t, guest, EventMove)
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"ada", "B1", testRules().Canonical()} {
		if !strings.Contains(string(carried), want) {
			t.Fatalf("the pump did not see %q in what it carried, so relay.go overstates what an operator reads", want)
		}
	}
}

// TestFrameTagsCoverPositionInTheConversation is the difference between intact
// bytes and an intact conversation. Every frame these two framers exchange is
// genuine, so nothing here is forged; what is refused is a genuine frame
// delivered twice, out of order, or back at the end that sent it.
func TestFrameTagsCoverPositionInTheConversation(t *testing.T) {
	key := deriveFrameKey("ROOM01", "SECRETSECRETSECR")
	var buf sink
	out := newFramer(&buf, 0)
	out.authenticate(key, dirHost, dirGuest)
	var frames [][]byte
	for _, m := range []message{
		{Type: msgPing},
		{Type: msgMove, Move: "B1", Entries: 1, PosHash: PositionHash(build(t, v("B1")))},
		{Type: msgMove, Move: "C3", Entries: 3, PosHash: PositionHash(build(t, v("B1"), h("A2"), v("C3")))},
	} {
		before := len(buf.b)
		if err := out.write(m); err != nil {
			t.Fatalf("writing a %s: %v", m.Type, err)
		}
		frames = append(frames, append([]byte(nil), buf.b[before:]...))
	}

	// replaying is defined below: a framer wants an io.ReadWriter and there is
	// nothing here for it to answer.
	readAs := func(sendDir, recvDir byte, order ...int) error {
		var stream []byte
		for _, i := range order {
			stream = append(stream, frames[i]...)
		}
		in := newFramer(replaying(stream), 0)
		in.authenticate(key, sendDir, recvDir)
		for range order {
			if _, err := in.read(); err != nil {
				return err
			}
		}
		return nil
	}
	if err := readAs(dirGuest, dirHost, 0, 1, 2); err != nil {
		t.Fatalf("the frames as sent were refused: %v", err)
	}
	for _, c := range []struct {
		name  string
		order []int
	}{
		{"a frame delivered twice", []int{0, 0}},
		{"two frames swapped", []int{0, 2, 1}},
		{"a frame dropped", []int{0, 2}},
	} {
		if err := readAs(dirGuest, dirHost, c.order...); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s was refused with %v, want an authentication failure", c.name, err)
		}
	}
	// The same frames reflected back at the end that sent them.
	if err := readAs(dirHost, dirGuest, 0); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a reflected frame was refused with %v, want an authentication failure", err)
	}
}

// TestHandshakeRefusesWhenOnlyOneEndHasTheKey covers the incompatibility being
// by refusal rather than by quietly falling back. A player who pasted only the
// beginning of a code, or a build from before the key existed, must be told so
// rather than given a game a relay can rewrite.
func TestHandshakeRefusesWhenOnlyOneEndHasTheKey(t *testing.T) {
	_, key, err := splitPairingCode(PairingCode())
	if err != nil {
		t.Fatalf("splitting a fresh pairing code: %v", err)
	}
	for _, c := range []struct {
		name  string
		keyed Role
	}{
		{"the host has the key and the guest does not", Host},
		{"the guest has the key and the host does not", Guest},
	} {
		t.Run(c.name, func(t *testing.T) {
			hc, gc := net.Pipe()
			t.Cleanup(func() { _ = hc.Close(); _ = gc.Close() })
			hostKey, guestKey := key, key
			if c.keyed == Host {
				guestKey = nil
			} else {
				hostKey = nil
			}
			hostErr := make(chan error, 1)
			go func() {
				_, err := hostOverKeyed(t.Context(), hc, hostOpts(), hostKey)
				hostErr <- err
			}()
			_, guestErr := joinOverKeyed(t.Context(), gc, guestOpts(), guestKey)
			if !errors.Is(guestErr, ErrUnauthenticated) {
				t.Fatalf("the guest reported %v, want an authentication failure", guestErr)
			}
			select {
			case err := <-hostErr:
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("the host reported %v, want an authentication failure", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the host was never told why the game did not start")
			}
		})
	}
}

// TestAPairingCodeWithoutItsKeyPartIsRefused stops the weaker game from being
// reachable by accident. Pasting the first line of a code, or the first group of
// it, has to fail loudly rather than pair two ends with nothing to authenticate
// with.
func TestAPairingCodeWithoutItsKeyPartIsRefused(t *testing.T) {
	full := PairingCode()
	room, _, err := splitPairingCode(full)
	if err != nil {
		t.Fatalf("splitting %q: %v", full, err)
	}
	for _, code := range []string{room, strings.ToLower(room), room + "-123", "A"} {
		if _, _, err := splitPairingCode(code); err == nil {
			t.Fatalf("splitPairingCode accepted %q, which carries no key", code)
		}
	}
	// The same refusal has to happen before anything is dialled, so a player
	// with half a code is told what is wrong rather than what it broke.
	_, err = JoinViaRelay(t.Context(), "127.0.0.1:1", room, guestOpts())
	if err == nil || !strings.Contains(err.Error(), "paste the whole code") {
		t.Fatalf("JoinViaRelay with only the room part returned %v", err)
	}
}

// TestAHostReclaimsItsRoomFromSomebodyWithoutTheKey covers pairing-code
// squatting. Whoever presented a code first used to become the opponent; now an
// arrival that cannot authenticate is turned away and the host claims its room
// again, so the invited player still gets the game.
func TestAHostReclaimsItsRoomFromSomebodyWithoutTheKey(t *testing.T) {
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.Wait = 10 * time.Second
	addr := startRelay(t, relay)

	code := PairingCode()
	room, _, err := splitPairingCode(code)
	if err != nil {
		t.Fatalf("splitting %q: %v", code, err)
	}

	// The squatter has the room, which is the beginning of the code, and nothing
	// else. It is an ordinary client of this build joining with what it has.
	squatter := make(chan error, 1)
	go func() {
		conn, err := dialRelay(t.Context(), addr, room)
		if err != nil {
			squatter <- err
			return
		}
		defer conn.Close()
		_, err = JoinOver(t.Context(), conn, guestOpts())
		squatter <- err
	}()

	type result struct {
		s   Session
		err error
	}
	hostCh := make(chan result, 1)
	go func() {
		s, err := HostViaRelay(t.Context(), addr, code, hostOpts())
		hostCh <- result{s, err}
	}()

	select {
	case err := <-squatter:
		if !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("the squatter was refused with %v, want an authentication failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the squatter was never refused")
	}

	// Wait for the host to be waiting in its room again before the invited
	// player arrives, so this tests the reclaim rather than the timing of it.
	waitUntil(t, func() bool {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		rm := relay.rooms[room]
		return rm != nil && rm.waiting != nil
	}, "the host never claimed its room again after turning the squatter away")

	guest, err := JoinViaRelay(t.Context(), addr, code, guestOpts())
	if err != nil {
		t.Fatalf("the invited player could not join after the squatter: %v", err)
	}
	defer guest.Close()
	got := <-hostCh
	if got.err != nil {
		t.Fatalf("the host never got its opponent: %v", got.err)
	}
	defer got.s.Close()
	wantEvent(t, got.s, EventConnected)
	wantEvent(t, guest, EventConnected)
}

func startRelay(t *testing.T, r *Relay) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = r.Serve(ctx, l) }()
	return l.Addr().String()
}

// TestRelayFreesARoomWhenTheSocketHoldingItDies is the cheapest way there was to
// take a relay offline: send JOIN, hang up, repeat. Nothing read a waiting
// client's socket, so its death went unnoticed for the whole pairing wait, and
// the relay said nothing about it either.
func TestRelayFreesARoomWhenTheSocketHoldingItDies(t *testing.T) {
	var mu sync.Mutex
	var logged []string
	relay := NewRelay()
	relay.Logf = func(format string, args ...any) {
		mu.Lock()
		logged = append(logged, fmt.Sprintf(format, args...))
		mu.Unlock()
		t.Logf(format, args...)
	}
	relay.MaxConnections = 1
	relay.Wait = 30 * time.Second
	addr := startRelay(t, relay)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	if err := writeRelayLine(c, relayJoin+" ABCDEF"); err != nil {
		t.Fatalf("joining: %v", err)
	}
	waitUntil(t, func() bool { return relay.Rooms() == 1 }, "the relay never took the room")
	_ = c.Close()

	waitUntil(t, func() bool {
		s := relay.Stats()
		return s.Rooms == 0 && s.Connections == 0 && s.Abandoned == 1
	}, "the relay kept the room and the connection slot of a socket that had gone")

	// The single connection slot really is free again: a real player gets in
	// where before it was told the relay was busy for ten minutes.
	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialling as the real player: %v", err)
	}
	defer second.Close()
	if err := writeRelayLine(second, relayJoin+" ZYXWVT"); err != nil {
		t.Fatalf("joining as the real player: %v", err)
	}
	waitUntil(t, func() bool { return relay.Rooms() == 1 }, "the real player could not claim a room")

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(logged, "\n"), "abandoned=1") {
		t.Fatalf("the operator was told nothing about it: %q", logged)
	}
}

// TestRelayRefusesAClientThatSpeaksBeforeItIsPaired closes the way round the
// watch. A client that sends one byte and hangs up would otherwise leave the
// socket looking alive and the room held.
func TestRelayRefusesAClientThatSpeaksBeforeItIsPaired(t *testing.T) {
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.Wait = 30 * time.Second
	addr := startRelay(t, relay)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer c.Close()
	if err := writeRelayLine(c, relayJoin+" ABCDEF"); err != nil {
		t.Fatalf("joining: %v", err)
	}
	if err := writeAll(c, []byte("TX")); err != nil {
		t.Fatalf("speaking early: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	if !strings.Contains(line, relayErr) || !strings.Contains(line, "wait to be paired") {
		t.Fatalf("the relay answered %q", line)
	}
	waitUntil(t, func() bool { return relay.Rooms() == 0 }, "the room was held by a client that had been refused")
}

// TestTheRelayGivesUpOnAPeerThatStopsReading covers the pump's writes having the
// same bound its reads have. The peer here keeps its own reader alive with one
// byte inside the idle window and reads nothing at all, which used to park a
// relay goroutine and its buffers in a write with no deadline on it.
func TestTheRelayGivesUpOnAPeerThatStopsReading(t *testing.T) {
	relay := NewRelay()
	relay.Logf = t.Logf
	relay.IdleTimeout = 150 * time.Millisecond
	addr := startRelay(t, relay)

	// Both ends are paired before either sends anything, and dialRelay only
	// returns once the relay has answered OK, so by the time the traffic starts
	// the pump is running. Sending before that would be refused for speaking
	// too early, which would end the test for a reason that has nothing to do
	// with the deadline being measured.
	victim, attacker := pairThroughRelay(t, addr, "WEDGE1")

	// One byte per interval keeps the attacker's own reader from timing out, so
	// the only thing left that can end this is a deadline on the relay's write.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				if _, err := attacker.Write([]byte{'.'}); err != nil {
					return
				}
			}
		}
	}()

	blob := make([]byte, 64<<10)
	done := make(chan error, 1)
	go func() {
		for range 512 { // 32 MiB, far past any socket buffer
			if err := writeAll(victim, blob); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the relay swallowed 32 MiB for a peer that was not reading any of it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay is still parked in a write to a peer that stopped reading")
	}
}

// pairThroughRelay puts two connections in one room and returns them only once
// both have been told they are paired. The two dials have to overlap: the first
// blocks until the second arrives.
func pairThroughRelay(t *testing.T, addr, room string) (net.Conn, net.Conn) {
	t.Helper()
	type dialled struct {
		c   net.Conn
		err error
	}
	firstCh := make(chan dialled, 1)
	go func() {
		c, err := dialRelay(t.Context(), addr, room)
		firstCh <- dialled{c, err}
	}()
	second, err := dialRelay(t.Context(), addr, room)
	if err != nil {
		t.Fatalf("dialling the second end of %s: %v", room, err)
	}
	t.Cleanup(func() { _ = second.Close() })
	first := <-firstCh
	if first.err != nil {
		t.Fatalf("dialling the first end of %s: %v", room, first.err)
	}
	t.Cleanup(func() { _ = first.c.Close() })
	return first.c, second
}

// TestASilentJoinerDoesNotHoldTheHost covers the direct host running its
// handshakes one at a time. Sockets that connected and said nothing used to own
// the listener for a whole handshake timeout each, and they queued.
func TestASilentJoinerDoesNotHoldTheHost(t *testing.T) {
	h, err := Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	defer h.Close()

	opts := hostOpts()
	opts.HandshakeTimeout = 2 * time.Second
	type result struct {
		s   Session
		err error
	}
	hostCh := make(chan result, 1)
	go func() {
		s, err := h.Wait(t.Context(), opts)
		hostCh <- result{s, err}
	}()

	// The host writes its invitation the moment it takes a socket up, so a byte
	// on all four inside one window is proof that all four hold handshake slots
	// at the same time. The window is shorter than a single handshake bound on
	// purpose: reading them one after another would let a host that runs its
	// handshakes in turn satisfy each read as the one before it expired, which
	// is the very arrangement being ruled out. A plain sleep would be worse
	// still, passing the whole test on any run where nothing had been accepted
	// yet, with nothing in the invited guest's way to measure.
	const silent = 4
	greeted := make(chan error, silent)
	for i := range silent {
		c, err := net.Dial("tcp", h.Addr())
		if err != nil {
			t.Fatalf("dialling silent socket %d: %v", i+1, err)
		}
		defer c.Close()
		go func() {
			_ = c.SetReadDeadline(time.Now().Add(opts.HandshakeTimeout / 2))
			_, err := c.Read(make([]byte, 1))
			_ = c.SetReadDeadline(time.Time{})
			greeted <- err
		}()
	}
	for range silent {
		if err := <-greeted; err != nil {
			t.Fatalf("the host did not have all %d silent sockets in a handshake at once, so nothing was in the guest's way: %v", silent, err)
		}
	}

	start := time.Now()
	guest, err := Dial(t.Context(), h.Addr(), guestOpts())
	if err != nil {
		t.Fatalf("the invited guest could not join past %d silent sockets: %v", silent, err)
	}
	defer guest.Close()
	if waited := time.Since(start); waited > opts.HandshakeTimeout {
		t.Fatalf("the invited guest waited %v behind %d silent sockets, more than one handshake bound", waited, silent)
	}
	got := <-hostCh
	if got.err != nil {
		t.Fatalf("the host never accepted the invited guest: %v", got.err)
	}
	defer got.s.Close()
	wantEvent(t, got.s, EventConnected)
	wantEvent(t, guest, EventConnected)
}

// TestARefusalFromThePeerIsBoundedAndFiltered covers the two message fields whose
// content is a sentence rather than a token. Both reach the player's screen
// verbatim, and neither was bounded by anything but the frame limit, so a peer
// that had not even completed a handshake could put 200 KB of escape sequences
// into the player's scrollback.
func TestARefusalFromThePeerIsBoundedAndFiltered(t *testing.T) {
	hostile := "no thanks\x1b]0;TITLE-HIJACKED\x07\x1b[41;97m" + strings.Repeat("A", 200<<10)

	t.Run("a rejected invitation", func(t *testing.T) {
		hc, pc := net.Pipe()
		t.Cleanup(func() { _ = hc.Close(); _ = pc.Close() })
		peer := newRawPeer(pc)
		done := make(chan error, 1)
		go func() {
			_, err := HostOver(t.Context(), hc, hostOpts())
			done <- err
		}()
		if hello := peer.recv(t); hello.Type != msgHello {
			t.Fatalf("the host opened with %q", hello.Type)
		}
		peer.send(t, message{Type: msgReject, Text: hostile})
		select {
		case err := <-done:
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("the host reported %v, want a refusal", err)
			}
			assertSafeForATerminal(t, "the refusal", err.Error(), maxWireTextLen+128)
		case <-time.After(5 * time.Second):
			t.Fatal("the host never reported the refusal")
		}
	})

	t.Run("a goodbye", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, hostOpts())
		peer.send(t, message{Type: msgBye, Text: hostile})
		ev := wantEvent(t, host, EventDisconnected)
		assertSafeForATerminal(t, "the goodbye", ev.Text, maxWireTextLen)
	})

	t.Run("a message type this build does not expect", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, hostOpts())
		peer.send(t, message{Type: msgType(hostile)})
		ev := wantEvent(t, host, EventError)
		assertSafeForATerminal(t, "the complaint", ev.Text, maxWireTextLen)
	})

	t.Run("an unauthenticated frame on a relayed game", func(t *testing.T) {
		// This refusal happens before anything has been filtered, because the
		// tag has to cover the bytes the opponent actually sent. So it must not
		// quote any of them.
		_, key, err := splitPairingCode(PairingCode())
		if err != nil {
			t.Fatalf("splitting a fresh pairing code: %v", err)
		}
		hc, pc := net.Pipe()
		t.Cleanup(func() { _ = hc.Close(); _ = pc.Close() })
		peer := newRawPeer(pc)
		// The peer is given the key only so that it can read the invitation,
		// which a relay can do without any key at all. What it sends is a frame
		// with no tag on it, which is the most a forger can manage.
		peer.f.authenticate(key, dirGuest, dirHost)
		done := make(chan error, 1)
		go func() {
			_, err := hostOverKeyed(t.Context(), hc, hostOpts(), key)
			done <- err
		}()
		if hello := peer.recv(t); hello.Type != msgHello {
			t.Fatalf("the host opened with %q", hello.Type)
		}
		peer.sendBytes(t, framedMessage(t, message{Type: msgType(hostile), Text: hostile}))
		select {
		case err := <-done:
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("the host reported %v, want an authentication failure", err)
			}
			assertSafeForATerminal(t, "the refusal", err.Error(), maxWireTextLen+128)
		case <-time.After(5 * time.Second):
			t.Fatal("the host never refused the frame")
		}
	})
}

// TestEveryStringOfAnIncomingFrameIsBoundedAndFiltered is the guard against a
// field being added to the wire and forgotten. It walks the decoded message
// rather than a list written out by hand, so a new string field arrives here
// unfiltered and fails.
func TestEveryStringOfAnIncomingFrameIsBoundedAndFiltered(t *testing.T) {
	hostile := "\x1b]0;OWNED\x07\u202e" + strings.Repeat("Z", 8192)
	var buf sink
	out := newFramer(&buf, 0)
	if err := out.write(message{
		Type:        msgType(hostile),
		Name:        hostile,
		Rules:       hostile,
		Fingerprint: hostile,
		Side:        hostile,
		Move:        hostile,
		PosHash:     hostile,
		Digest:      hostile,
		Text:        hostile,
		Replay:      []wireEntry{{Side: hostile, Move: hostile}},
	}); err != nil {
		t.Fatalf("framing the hostile message: %v", err)
	}
	got, err := newFramer(replaying(buf.b), 0).read()
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	seen := 0
	walkStrings(reflect.ValueOf(got), "message", func(path, s string) {
		seen++
		// maxWireTextLen is the loosest of the per-field bounds, so every field
		// has to be at least this short.
		assertSafeForATerminal(t, path, s, maxWireTextLen)
		if s == hostile {
			t.Fatalf("%s came through untouched", path)
		}
	})
	if seen < 10 {
		t.Fatalf("only %d strings were checked, so the walk is not reaching the whole message", seen)
	}
}

// walkStrings calls fn for every string anywhere in v.
func walkStrings(v reflect.Value, path string, fn func(path, s string)) {
	switch v.Kind() {
	case reflect.String:
		fn(path, v.String())
	case reflect.Struct:
		for i := range v.NumField() {
			walkStrings(v.Field(i), path+"."+v.Type().Field(i).Name, fn)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkStrings(v.Index(i), path+"["+strconv.Itoa(i)+"]", fn)
		}
	}
}

// replaying hands a framer bytes to read. A framer wants an io.ReadWriter;
// writing is an error rather than a discard, because nothing that reads a
// canned stream should be answering it.
type replayStream struct{ r *bytes.Reader }

func replaying(b []byte) *replayStream { return &replayStream{r: bytes.NewReader(b)} }

func (s *replayStream) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *replayStream) Write([]byte) (int, error) {
	return 0, errors.New("this framer is only reading")
}

func assertSafeForATerminal(t *testing.T, what, s string, max int) {
	t.Helper()
	if len(s) > max {
		t.Fatalf("%s is %d bytes, over the %d byte bound: %q", what, len(s), max, truncateRunes(s, 120))
	}
	if !utf8.ValidString(s) {
		t.Fatalf("%s is not valid UTF-8: %q", what, truncateRunes(s, 120))
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("%s carries %q, which a terminal would act on: %q", what, r, truncateRunes(s, 120))
		}
	}
}

// TestDecodeInviteRefusesAnIdentifierThisProgramCouldNotHaveMade covers the
// asymmetry the review found: EncodeInvite checked the identifier and
// DecodeInvite checked nothing, so a hand-built invite could hand the caller 255
// arbitrary bytes to use as a file name.
func TestDecodeInviteRefusesAnIdentifierThisProgramCouldNotHaveMade(t *testing.T) {
	for _, id := range []string{
		"../../../../tmp/pwned",
		"/etc/twixt",
		strings.Repeat("A", 200),
		"a\x1b]0;window title hijacked\x07b",
		"a\nb\rc",
		"one two",
		"game.json",
		"",
	} {
		code := handBuiltInvite(t, testRules(), game.Vertical, id, "Mallory")
		inv, err := DecodeInvite(code)
		if !errors.Is(err, ErrBadCode) {
			t.Fatalf("DecodeInvite turned the identifier %q into %q with error %v", id, inv.ID, err)
		}
		assertSafeForATerminal(t, "the refusal", err.Error(), 512)
	}
	// The identifiers this program does produce still decode.
	for _, id := range []string{NewGameID(), "abcd1234", "GAME-ONE"} {
		code := handBuiltInvite(t, testRules(), game.Vertical, id, "Ada")
		inv, err := DecodeInvite(code)
		if err != nil {
			t.Fatalf("DecodeInvite refused the identifier %q: %v", id, err)
		}
		if inv.ID != id {
			t.Fatalf("the identifier came back as %q, want %q", inv.ID, id)
		}
	}
}

// handBuiltInvite assembles an invite without going through EncodeInvite, so the
// encoder's own validation is not in the way. This is what a hostile paste looks
// like.
func handBuiltInvite(t *testing.T, rs game.Ruleset, side game.Player, id, name string) string {
	t.Helper()
	fingerprint, err := hex.DecodeString(rs.Fingerprint())
	if err != nil {
		t.Fatalf("decoding the ruleset fingerprint: %v", err)
	}
	var flags byte
	for _, f := range []struct {
		on   bool
		mask byte
	}{
		{rs.DeliberateLinking, flagDeliberate},
		{rs.LinkRemoval, flagLinkRemoval},
		{rs.PegRemoval, flagPegRemoval},
		{rs.OwnLinksMayCross, flagOwnCross},
		{rs.Swap, flagSwap},
	} {
		if f.on {
			flags |= f.mask
		}
	}
	payload := []byte{codeVersion, byte(rs.Size), flags, byte(side)}
	payload = append(payload, fingerprint...)
	payload = append(payload, byte(len(id)))
	payload = append(payload, id...)
	payload = append(payload, byte(len(name)))
	payload = append(payload, name...)
	payload = binary.BigEndian.AppendUint32(payload, crc32.ChecksumIEEE(payload))
	return formatCode(invitePrefix, payload)
}

// TestApplyTranscriptLeavesTheGameAloneWhenALineIsBad covers the one apply path
// in this package that used to mutate the caller's live game before the whole
// block was known to be good.
func TestApplyTranscriptLeavesTheGameAloneWhenALineIsBad(t *testing.T) {
	id := "FULL-RECORD"
	block, err := EncodeTranscript(id, testRules(), scriptedGame)
	if err != nil {
		t.Fatalf("encoding the transcript: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) < 3 {
		t.Fatalf("the scripted game encoded to %d lines", len(lines))
	}
	for _, c := range []struct {
		name  string
		block string
	}{
		{"a third line that is not a code at all", strings.Join([]string{lines[0], lines[1], "TWX-BOGUS"}, "\n")},
		{"a third line that is a real code for an earlier position", strings.Join([]string{lines[0], lines[1], lines[0]}, "\n")},
		{"a third line from a different game", strings.Join([]string{lines[0], lines[1], mustEncodeMove(t, "OTHER-GAME")}, "\n")},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := game.MustNew(testRules())
			added, err := ApplyTranscript(g, id, c.block)
			if err == nil {
				t.Fatal("the block was accepted")
			}
			if len(added) != 0 {
				t.Fatalf("ApplyTranscript reported %d entries added", len(added))
			}
			if g.Entries() != 0 {
				t.Fatalf("the game was left %d entries in", g.Entries())
			}
		})
	}

	// A block that is good throughout still applies in full, so the refusal is
	// not simply refusing everything.
	g := game.MustNew(testRules())
	added, err := ApplyTranscript(g, id, block)
	if err != nil {
		t.Fatalf("a good block was refused: %v", err)
	}
	if len(added) != len(scriptedGame) || g.Entries() != len(scriptedGame) {
		t.Fatalf("a good block added %d entries and left the game at %d, want %d of each", len(added), g.Entries(), len(scriptedGame))
	}
}

func mustEncodeMove(t *testing.T, id string) string {
	t.Helper()
	code, err := EncodeMove(game.MustNew(testRules()), id, "B1")
	if err != nil {
		t.Fatalf("encoding a move for %q: %v", id, err)
	}
	return code
}
