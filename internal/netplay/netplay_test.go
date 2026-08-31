package netplay

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// testRules is the smallest board the engine allows, which keeps a scripted
// game short while still exercising every rule the protocol carries.
func testRules() game.Ruleset {
	rs := game.Std
	rs.Size = 6
	return rs
}

// testTuning keeps the timers short enough not to slow the tests down and long
// enough that a loaded machine does not fail them.
func testTuning() Tuning {
	return Tuning{
		Keepalive:        200 * time.Millisecond,
		DeadAfter:        5 * time.Second,
		HandshakeTimeout: 5 * time.Second,
	}
}

func hostOpts() HostOptions {
	return HostOptions{Name: "ada", Rules: testRules(), Side: game.Vertical, Tuning: testTuning()}
}

func guestOpts() GuestOptions {
	return GuestOptions{Name: "grace", Tuning: testTuning()}
}

// scriptedGame is a complete game on a 6x6 board. Vertical connects the top row
// to the bottom one through B1, C3, D5 and B6, each a knight's move from the
// last. Horizontal plays in its own border columns, where it neither blocks that
// chain nor forms any link of its own, so the script is a legal game whose
// outcome depends on nothing but the moves.
var scriptedGame = []Entry{
	{Side: game.Vertical, Move: "B1"},
	{Side: game.Horizontal, Move: "A2"},
	{Side: game.Vertical, Move: "C3"},
	{Side: game.Horizontal, Move: "F2"},
	{Side: game.Vertical, Move: "D5"},
	{Side: game.Horizontal, Move: "A4"},
	{Side: game.Vertical, Move: "B6"},
}

// TestScriptedGameIsAVerticalWin pins the script itself. Every transport test
// plays it, so an engine change that made the script illegal or drawn fails
// here, where the cause is obvious, rather than inside a network test.
func TestScriptedGameIsAVerticalWin(t *testing.T) {
	g, err := replay(testRules(), scriptedGame)
	if err != nil {
		t.Fatalf("replaying the script: %v", err)
	}
	if got, want := g.Result().Outcome, game.VerticalWins; got != want {
		t.Fatalf("outcome %v, want %v", got, want)
	}
	if got, want := g.Result().Reason, game.Connection; got != want {
		t.Fatalf("reason %v, want %v", got, want)
	}
	if got, want := g.Ply(), len(scriptedGame); got != want {
		t.Fatalf("ply %d, want %d", got, want)
	}
}

// connectOver runs both ends of a handshake at once. They cannot be run one
// after the other: net.Pipe is unbuffered, so the first write of a handshake
// driven from a single goroutine would never return.
func connectOver(t *testing.T, hc, gc net.Conn, hopts HostOptions, gopts GuestOptions) (host, guest Session, hostErr, guestErr error) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		host, hostErr = HostOver(t.Context(), hc, hopts)
	}()
	go func() {
		defer wg.Done()
		guest, guestErr = JoinOver(t.Context(), gc, gopts)
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the handshake never finished")
	}
	if host != nil {
		t.Cleanup(func() { _ = host.Close() })
	}
	if guest != nil {
		t.Cleanup(func() { _ = guest.Close() })
	}
	return host, guest, hostErr, guestErr
}

func connectPipe(t *testing.T, hopts HostOptions, gopts GuestOptions) (host, guest Session, hostErr, guestErr error) {
	t.Helper()
	hc, gc := net.Pipe()
	return connectOver(t, hc, gc, hopts, gopts)
}

// mustConnectPipe is connectPipe for the tests that expect the handshake to
// work, with the opening event of each end already consumed.
func mustConnectPipe(t *testing.T) (host, guest Session) {
	t.Helper()
	host, guest, hostErr, guestErr := connectPipe(t, hostOpts(), guestOpts())
	if hostErr != nil || guestErr != nil {
		t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	return host, guest
}

func waitEvent(t *testing.T, s Session) Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatal("the event stream closed while waiting for an event")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived")
	}
	return Event{}
}

func wantEvent(t *testing.T, s Session, kind EventKind) Event {
	t.Helper()
	e := waitEvent(t, s)
	if e.Kind != kind {
		t.Fatalf("got a %s event (%q, err %v), want %s", e.Kind, e.Text, e.Err, kind)
	}
	return e
}

// wantClosed asserts the event stream ends, which is how a session reports that
// it has finished for good.
func wantClosed(t *testing.T, s Session) {
	t.Helper()
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				return
			}
			t.Logf("also saw a %s event (%q)", e.Kind, e.Text)
		case <-time.After(5 * time.Second):
			t.Fatal("the session never finished")
		}
	}
}

func positionOf(t *testing.T, s Session) *game.Game {
	t.Helper()
	r, ok := s.(Resumable)
	if !ok {
		t.Fatalf("a %T does not expose its position", s)
	}
	return r.Position()
}

// assertAgree checks that the two ends hold the same position, which is the
// whole point of the protocol.
func assertAgree(t *testing.T, a, b Session, entries int) {
	t.Helper()
	pa, pb := positionOf(t, a), positionOf(t, b)
	if ha, hb := PositionHash(pa), PositionHash(pb); ha != hb {
		t.Fatalf("after entry %d the two ends disagree:\n%s %v\n%s %v", entries, ha, pa, hb, pb)
	}
	if pa.Entries() != entries {
		t.Fatalf("after entry %d the record holds %d entries", entries, pa.Entries())
	}
}

// playScript plays a whole scripted game between two live sessions, checking
// after every move that the receiving end was told the move and that both ends
// still agree on the position.
func playScript(t *testing.T, a, b Session, moves []Entry) {
	t.Helper()
	for i, m := range moves {
		sender, receiver := a, b
		if sender.Side() != m.Side {
			sender, receiver = b, a
		}
		if sender.Side() != m.Side {
			t.Fatalf("move %d: neither end plays %s", i+1, m.Side)
		}
		if err := sender.SendMove(m.Move); err != nil {
			t.Fatalf("move %d (%s %s): %v", i+1, m.Side, m.Move, err)
		}
		ev := wantEvent(t, receiver, EventMove)
		if ev.Move != m.Move {
			t.Fatalf("move %d: the other end was told %q, %q was sent", i+1, ev.Move, m.Move)
		}
		assertAgree(t, a, b, i+1)
	}
	if len(moves) == len(scriptedGame) {
		if got := positionOf(t, a).Result().Outcome; got != game.VerticalWins {
			t.Fatalf("after the whole script the outcome is %v", got)
		}
	}
}

// rawPeer is one end of a connection driven by hand, so a test can be a peer
// that a real session never would be: a wrong version, a wrong position hash, a
// hostile length prefix.
type rawRead struct {
	m   message
	err error
}

type rawPeer struct {
	conn  net.Conn
	f     *framer
	inbox chan rawRead
}

func newRawPeer(conn net.Conn) *rawPeer {
	return &rawPeer{conn: conn, f: newFramer(conn, 5*time.Second)}
}

// drain keeps reading after the handshake. net.Pipe has no buffer, so a real
// session cannot finish SendMove unless its test peer is already reading.
func (p *rawPeer) drain() {
	p.inbox = make(chan rawRead, 16)
	go func() {
		for {
			m, err := p.f.read()
			p.inbox <- rawRead{m: m, err: err}
			if err != nil {
				close(p.inbox)
				return
			}
		}
	}()
}

func (p *rawPeer) send(t *testing.T, m message) {
	t.Helper()
	if err := p.f.write(m); err != nil {
		t.Fatalf("sending a %s: %v", m.Type, err)
	}
}

func (p *rawPeer) recv(t *testing.T) message {
	t.Helper()
	if p.inbox == nil {
		m, err := p.f.read()
		if err != nil {
			t.Fatalf("reading a message: %v", err)
		}
		return m
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got, ok := <-p.inbox:
			if !ok {
				t.Fatal("the raw peer's event stream closed")
			}
			if got.err != nil {
				t.Fatalf("reading a message: %v", got.err)
			}
			if got.m.Type == msgPing {
				continue
			}
			return got.m
		case <-deadline:
			t.Fatal("the raw peer received no message")
		}
	}
}

func (p *rawPeer) sendBytes(t *testing.T, b []byte) {
	t.Helper()
	if err := writeAll(p.conn, b); err != nil {
		t.Fatalf("sending %d raw bytes: %v", len(b), err)
	}
}

// acceptAsGuest performs the guest half of the handshake by hand and returns the
// invitation, leaving the peer free to misbehave afterwards.
func (p *rawPeer) acceptAsGuest(t *testing.T) (message, game.Ruleset, game.Player) {
	t.Helper()
	hello := p.recv(t)
	if hello.Type != msgHello {
		t.Fatalf("first message was %q, want %q", hello.Type, msgHello)
	}
	rs, err := parseRuleset(hello.Rules)
	if err != nil {
		t.Fatalf("parsing the host's ruleset: %v", err)
	}
	hostSide, err := parsePlayer(hello.Side)
	if err != nil {
		t.Fatalf("parsing the host's side: %v", err)
	}
	side := hostSide.Opponent()
	p.send(t, message{
		Type:        msgAccept,
		Version:     Version,
		Name:        "raw",
		Fingerprint: rs.Fingerprint(),
		Side:        side.String(),
		Digest:      transcriptDigest(nil),
	})
	return hello, rs, side
}

// hostAgainstRaw starts a host session whose opponent is driven by hand.
func hostAgainstRaw(t *testing.T, opts HostOptions) (Session, *rawPeer) {
	return hostAgainstRawMode(t, opts, true)
}

// hostAgainstSilentRaw is the same handshake, but the peer stops reading once
// it has accepted. That is the dead-peer case the keepalive must notice.
func hostAgainstSilentRaw(t *testing.T, opts HostOptions) (Session, *rawPeer) {
	return hostAgainstRawMode(t, opts, false)
}

func hostAgainstRawMode(t *testing.T, opts HostOptions, drain bool) (Session, *rawPeer) {
	t.Helper()
	hc, pc := net.Pipe()
	peer := newRawPeer(pc)
	type result struct {
		s   Session
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := HostOver(t.Context(), hc, opts)
		ch <- result{s, err}
	}()
	peer.acceptAsGuest(t)
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("the host handshake failed: %v", r.err)
		}
		t.Cleanup(func() { _ = r.s.Close() })
		wantEvent(t, r.s, EventConnected)
		if drain {
			peer.drain()
		}
		return r.s, peer
	case <-time.After(10 * time.Second):
		t.Fatal("the host handshake never finished")
	}
	return nil, nil
}

// rawFrame builds a frame by hand, so a test can choose the version byte, the
// announced length and the checksum independently of the payload.
func rawFrame(version byte, length, checksum uint32, payload []byte) []byte {
	out := make([]byte, 0, frameHeaderLen+len(payload))
	out = append(out, frameMagic[0], frameMagic[1], version)
	out = append(out, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	out = append(out, byte(checksum>>24), byte(checksum>>16), byte(checksum>>8), byte(checksum))
	return append(out, payload...)
}
