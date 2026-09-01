package netplay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The relay exists for the common case where neither player can accept an
// inbound connection: home routers with no port forwarding, carrier-grade NAT
// on mobile networks, hotel and campus networks that block unsolicited inbound
// traffic. Both players only dial out, which defeats all of those without
// having to reason about what kind of NAT is in the way.
//
// It is a byte pump. It pairs two connections that arrived with the same room
// name and copies bytes between them without looking at them, so it never needs
// to understand the game, never holds game state and does not need updating when
// the protocol changes.
//
// What that does not mean is that it is trusted. A relay operator sits in the
// middle of the connection and sees every byte of it, and a byte pump is free to
// send bytes other than the ones it was given. So:
//
//   - What an operator can read. Everything. The frames are plaintext JSON:
//     both players' names, the ruleset, and every move as it is played. The
//     authentication in auth.go protects the game's integrity, not its secrecy,
//     and there is no encryption here. A player who does not want a stranger
//     reading their game should use a direct connection, or a relay one of the
//     two of them runs.
//   - What an operator can no longer do. Change any of it. Both ends
//     authenticate every frame with a key derived from the part of the pairing
//     code the relay is never told, and a tag covers a frame's position in the
//     conversation as well as its contents, so an altered, injected, replayed,
//     reordered or dropped frame is refused by the far end instead of being
//     played. Before that key existed an operator could resign on either
//     player's behalf and the victim's own engine would accept it.
//   - What an operator can still do. Refuse to carry the game, or stop carrying
//     it part way through: delivery is the one thing a relay is trusted with and
//     the one thing nothing can check. It also learns that these two players are
//     playing, and when.
//
// So self-hosting stays cheap to offer, but "anyone can run one" is only true of
// the game's integrity. Whose relay it is decides who reads the game.
//
// The pairing prelude is one line each way, which is all the relay ever parses:
//
//	client -> relay   TWIXT-RELAY/1 JOIN <room>\n
//	relay  -> client  TWIXT-RELAY/1 OK\n
//	relay  -> client  TWIXT-RELAY/1 ERR <reason>\n
//
// The room is the first characters of the pairing code and nothing else; the key
// part never leaves the two players. After OK the connection carries protocol
// frames and the relay is blind to them.

// DefaultRelayPort is the port a relay uses when an address gives none.
const DefaultRelayPort = "4271"

const (
	relayGreeting = "TWIXT-RELAY/1"
	relayJoin     = "JOIN"
	relayOK       = "OK"
	relayErr      = "ERR"

	// relayMaxPrelude bounds the first line, so a peer that never sends a
	// newline cannot make the relay buffer without limit.
	relayMaxPrelude = 128
	// relayPreludeTimeout bounds how long a connection may sit without saying
	// what room it wants.
	relayPreludeTimeout = 20 * time.Second
	// relayWriteTimeout bounds the relay's own short replies, so a client that
	// never reads cannot park the goroutine refusing it.
	relayWriteTimeout = time.Second
	// relayCodeMaxLen bounds a room name. Six characters is what a client of
	// this build sends; the slack is for one that sends more.
	relayCodeMaxLen = 32
	// DefaultPairingWait is how long the first player waits in a room for the
	// second. It is generous because the player has to pass the code on by
	// hand, over whatever chat they use. A waiting client's socket is watched,
	// so this bounds only a client that is still there and still waiting.
	DefaultPairingWait = 10 * time.Minute
	// DefaultMaxRelayConnections bounds the relay's live sockets and handler
	// goroutines. Operators may lower it before Serve starts.
	DefaultMaxRelayConnections = 512
	// DefaultRelayIdleTimeout releases a paired room that carries no bytes.
	// Real sessions send keepalive frames well inside this window.
	DefaultRelayIdleTimeout = 2 * time.Minute
	// relayLogInterval throttles the operator log. A relay being abused would
	// otherwise turn its own log into a second denial of service; one line per
	// interval, carrying the running counts, says everything an operator needs.
	relayLogInterval = 30 * time.Second
)

// normalisePairingCode puts a code, or the room part of one, into the form the
// relay matches on, mapping the characters that are read wrongly by eye.
func normalisePairingCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	code = strings.ReplaceAll(code, "-", "")
	if code == "" {
		return "", errors.New("a relayed game needs a pairing code")
	}
	if len(code) > relayCodeMaxLen {
		return "", fmt.Errorf("pairing code is %d characters, the limit is %d", len(code), relayCodeMaxLen)
	}
	out := make([]byte, 0, len(code))
	for _, c := range code {
		v, ok := codeValue(c)
		if !ok {
			return "", fmt.Errorf("pairing code contains %q, which is not part of a code", string(c))
		}
		out = append(out, codeAlphabet[v])
	}
	return string(out), nil
}

// Relay pairs clients by room name and copies bytes between them.
type Relay struct {
	// Wait is how long the first client of a room waits for the second. Zero
	// means DefaultPairingWait.
	Wait time.Duration
	// MaxConnections bounds waiting and paired client sockets together. Set it
	// before Serve starts; zero means DefaultMaxRelayConnections.
	MaxConnections int
	// IdleTimeout closes a paired connection after this long without bytes.
	// Set it before Serve starts; zero means DefaultRelayIdleTimeout.
	IdleTimeout time.Duration
	// Logf, when set, is where the relay reports what its operator needs to
	// see. Left nil, it logs through the standard logger. It is never given a
	// pairing code, a player name or an address: an operator needs enough to
	// tell abuse from popularity, and nothing about the games being carried.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	rooms   map[string]*room
	active  int
	stats   relayStats
	lastLog time.Time
}

// relayStats is the running count behind Stats and the operator log.
type relayStats struct {
	paired         uint64
	abandoned      uint64
	refusedBusy    uint64
	refusedPrelude uint64
}

// RelayStats is a relay's account of the traffic it has carried. It names no
// player, room or address, because a relay that recorded those would be one its
// users had to trust with more than delivery.
type RelayStats struct {
	// Rooms is how many rooms the relay is holding now.
	Rooms int
	// Connections is how many client sockets are live now.
	Connections int
	// Paired is how many pairs the relay has put together since it started.
	Paired uint64
	// Abandoned is how many waiting clients stopped being usable before an
	// opponent arrived. This climbing while Paired does not is what somebody
	// exhausting the relay's rooms looks like.
	Abandoned uint64
	// RefusedBusy is how many connections were turned away because
	// MaxConnections was reached.
	RefusedBusy uint64
	// RefusedPrelude is how many connections never asked for a room in a form
	// the relay understands.
	RefusedPrelude uint64
}

// room is one room name's state on the relay.
type room struct {
	waiting *client // a client waiting for its opponent
	paired  bool    // two clients are connected and being pumped
}

// client is one connection, with the buffered reader that read its prelude:
// bytes that arrived in the same packet as the prelude line must be pumped, not
// dropped.
type client struct {
	conn   net.Conn
	r      *bufio.Reader
	paired chan *client

	// gone is closed when a client stopped being worth waiting for while it
	// held a room: its socket died, or it spoke before it was paired.
	gone chan struct{}
	// why is what the watcher saw, written before gone is closed. A nil error
	// means the socket was healthy and sent something too early.
	why error
	// watched is closed when the watcher has handed the reader back, so that
	// the pump is the only thing reading it from then on.
	watched chan struct{}
	stopped atomic.Bool
}

func newClient(conn net.Conn, r *bufio.Reader) *client {
	return &client{
		conn:    conn,
		r:       r,
		paired:  make(chan *client, 1),
		gone:    make(chan struct{}),
		watched: make(chan struct{}),
	}
}

// watch takes over the reader while its owner waits for an opponent, and reports
// the wait becoming pointless.
//
// Nothing else reads a waiting client's socket, which is why its death would
// otherwise go unnoticed until the pairing wait elapsed: ten minutes in which a
// room and a connection slot are held by nobody. The watch ends on the first
// thing that happens on the socket, and either of them means the same thing
// here. An error means the socket has gone. A byte means a client talking before
// it has been paired, which no client of this protocol does, because both ends
// wait for OK before they send a frame.
func (c *client) watch() {
	go func() {
		defer close(c.watched)
		_, err := c.r.Peek(1)
		if c.stopped.Load() {
			// The wait ended for a reason of its own; whatever the peek saw is
			// the pump's business now, not a verdict on the client.
			return
		}
		c.why = err
		close(c.gone)
	}()
}

// stopWatch ends the watch and waits for the reader to come back, so that from
// here on the pump is the only thing reading it. An expired read deadline is
// what unblocks a peek still waiting for a first byte; the watcher knows to draw
// no conclusion from one.
func (c *client) stopWatch() {
	c.stopped.Store(true)
	_ = c.conn.SetReadDeadline(time.Now())
	<-c.watched
	_ = c.conn.SetReadDeadline(time.Time{})
}

// NewRelay returns a relay with no rooms.
func NewRelay() *Relay {
	return &Relay{rooms: make(map[string]*room)}
}

// Rooms reports how many rooms the relay is holding, which is the only thing it
// knows about the games it carries.
func (r *Relay) Rooms() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rooms)
}

// Stats returns what the relay knows about its own traffic.
func (r *Relay) Stats() RelayStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RelayStats{
		Rooms:          len(r.rooms),
		Connections:    r.active,
		Paired:         r.stats.paired,
		Abandoned:      r.stats.abandoned,
		RefusedBusy:    r.stats.refusedBusy,
		RefusedPrelude: r.stats.refusedPrelude,
	}
}

func (r *Relay) wait() time.Duration {
	if r.Wait > 0 {
		return r.Wait
	}
	return DefaultPairingWait
}

func (r *Relay) maxConnections() int {
	if r.MaxConnections > 0 {
		return r.MaxConnections
	}
	return DefaultMaxRelayConnections
}

func (r *Relay) idleTimeout() time.Duration {
	if r.IdleTimeout > 0 {
		return r.IdleTimeout
	}
	return DefaultRelayIdleTimeout
}

func (r *Relay) admit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active >= r.maxConnections() {
		return false
	}
	r.active++
	return true
}

func (r *Relay) releaseConnection() {
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
}

// note counts one notable event and tells the operator about it, at most once
// every relayLogInterval. The line carries the running counts rather than
// anything about the connection that prompted it, so an operator watching the
// log sees the shape of the traffic and nothing about the games.
//
// The caller must not hold r.mu.
func (r *Relay) note(counter *uint64, what string) {
	r.mu.Lock()
	*counter++
	stats, rooms, active := r.stats, len(r.rooms), r.active
	now := time.Now()
	speak := r.lastLog.IsZero() || now.Sub(r.lastLog) >= relayLogInterval
	if speak {
		r.lastLog = now
	}
	r.mu.Unlock()
	if !speak {
		return
	}
	r.logf("relay: %s; rooms=%d connections=%d paired=%d abandoned=%d refused-busy=%d refused-prelude=%d",
		what, rooms, active, stats.paired, stats.abandoned, stats.refusedBusy, stats.refusedPrelude)
}

func (r *Relay) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Serve accepts connections until ctx is cancelled or the listener fails. It
// closes l on the way out.
func (r *Relay) Serve(ctx context.Context, l net.Listener) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		_ = l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accepting a connection: %w", err)
		}
		if !r.admit() {
			r.refuse(conn, errors.New("relay is busy; try again later"))
			r.note(&r.stats.refusedBusy, "a connection was turned away because the relay is at its connection limit")
			continue
		}
		go func() {
			defer r.releaseConnection()
			r.handle(ctx, conn)
		}()
	}
}

// Serve runs a relay on addr until ctx is cancelled.
func Serve(ctx context.Context, addr string) error {
	target := addPort(addr, DefaultRelayPort)
	l, err := net.Listen("tcp", target)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", target, err)
	}
	return ServeOn(ctx, l)
}

// ServeOn runs a relay on a listener the caller bound, which is how a relay
// asked for port 0 can report its port before it starts serving.
func ServeOn(ctx context.Context, l net.Listener) error {
	return NewRelay().Serve(ctx, l)
}

// refuse tells a client why it is not being paired, and hangs up. The reason is
// filtered because a client's own input reaches it, and it has to stay on the
// one line the client is reading.
func (r *Relay) refuse(conn net.Conn, reason error) {
	_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
	_ = writeRelayLine(conn, relayErr+" "+safeText(reason.Error(), relayMaxPrelude))
	_ = conn.Close()
}

// handle reads one client's prelude, pairs it, and then pumps its side of the
// conversation.
func (r *Relay) handle(ctx context.Context, conn net.Conn) {
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(relayPreludeTimeout))
	code, err := readJoin(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		r.refuse(conn, err)
		r.note(&r.stats.refusedPrelude, "a connection did not ask for a room in a form this relay understands")
		return
	}

	me := newClient(conn, br)
	partner, rm, err := r.pair(ctx, code, me)
	if err != nil {
		r.refuse(conn, err)
		return
	}
	if partner == nil { // the wait ended without an opponent
		_ = conn.Close()
		return
	}
	defer r.release(code, rm)
	if err := writeRelayLine(conn, relayOK); err != nil {
		_ = conn.Close()
		_ = partner.conn.Close()
		return
	}
	stop := closeOnCancel(ctx, conn)
	defer stop()

	// One direction each. Both sides of the copy carry a rolling idle deadline:
	// the reader renews one whenever bytes arrive, the writer sets one before
	// every write, so neither a peer that says nothing nor a peer that stops
	// reading can park this goroutine. Whichever direction ends first closes
	// both connections, which unblocks the other copy.
	src := &idleReader{conn: conn, r: me.r, timeout: r.idleTimeout()}
	dst := &deadlineWriter{conn: partner.conn, timeout: r.idleTimeout()}
	_, _ = io.Copy(dst, src)
	_ = conn.Close()
	_ = partner.conn.Close()
}

type idleReader struct {
	conn    net.Conn
	r       io.Reader
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	if r.timeout > 0 {
		_ = r.conn.SetReadDeadline(time.Now().Add(r.timeout))
	}
	return r.r.Read(p)
}

// deadlineWriter bounds one write of the pump. It is a plain io.Writer on
// purpose: handing io.Copy a net.Conn would let it use the connection's own
// ReadFrom and never pass through here at all.
type deadlineWriter struct {
	conn    net.Conn
	timeout time.Duration
}

func (w *deadlineWriter) Write(p []byte) (int, error) {
	if w.timeout > 0 {
		_ = w.conn.SetWriteDeadline(time.Now().Add(w.timeout))
	}
	return w.conn.Write(p)
}

// pair registers a client in its room and returns its opponent and the exact
// room generation they paired in. A nil partner with a nil error means the wait
// ended without an opponent, because the context was cancelled or because the
// waiting client itself went away.
func (r *Relay) pair(ctx context.Context, code string, me *client) (*client, *room, error) {
	r.mu.Lock()
	rm := r.rooms[code]
	if rm == nil {
		rm = &room{}
		r.rooms[code] = rm
	}
	switch {
	case rm.paired:
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("two players are already using the code %s", code)
	case rm.waiting != nil:
		partner := rm.waiting
		rm.waiting = nil
		rm.paired = true
		r.stats.paired++
		r.mu.Unlock()
		partner.paired <- me // buffered, so this never blocks
		return partner, rm, nil
	default:
		rm.waiting = me
		r.mu.Unlock()
	}

	// From here a room and this connection's slot are held, so the socket
	// holding them is watched: a room is worth exactly as much as the socket
	// that claimed it.
	me.watch()
	defer me.stopWatch()

	select {
	case partner := <-me.paired:
		return partner, rm, nil
	case <-me.gone:
		if !r.giveUp(code, rm, me) {
			return <-me.paired, rm, nil
		}
		r.note(&r.stats.abandoned, "a client stopped waiting for an opponent before one arrived, so its room and connection are free again")
		if me.why == nil {
			return nil, nil, errors.New("a client must wait to be paired before it sends anything")
		}
		return nil, nil, nil
	case <-time.After(r.wait()):
		if !r.giveUp(code, rm, me) {
			return <-me.paired, rm, nil
		}
		return nil, nil, fmt.Errorf("nobody joined with the code %s", code)
	case <-ctx.Done():
		if !r.giveUp(code, rm, me) {
			return <-me.paired, rm, nil
		}
		return nil, nil, nil
	}
}

// giveUp takes a waiting client out of its room and reports whether it was still
// the one waiting. It may not be: an opponent can arrive in the instant a timer
// fires, in which case the pairing has already been handed over and has to be
// taken.
func (r *Relay) giveUp(code string, rm *room, me *client) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rm.waiting != me {
		return false
	}
	rm.waiting = nil
	r.forget(code, rm)
	return true
}

// release marks one exact paired room generation free again. Reconnecting to
// the same code creates a new generation; a delayed handler from the old pair
// must not delete that replacement.
func (r *Relay) release(code string, rm *room) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rooms[code] != rm {
		return
	}
	rm.paired = false
	r.forget(code, rm)
}

// forget drops an empty room. The caller holds the lock.
func (r *Relay) forget(code string, rm *room) {
	if !rm.paired && rm.waiting == nil {
		delete(r.rooms, code)
	}
}

// The reclaim after turning somebody away has to tolerate a few failed dials.
// The relay frees a room a moment after the connection holding it closes, so an
// immediate re-dial can still be told the room is taken.
const (
	relayReclaimAttempts = 5
	relayReclaimPause    = 50 * time.Millisecond
)

// HostViaRelay hosts a game through a relay under the given pairing code, which
// the player must pass to the opponent whole: its first characters name the room
// the relay pairs the two clients in, and the rest is the key they authenticate
// the game with and the relay is never told.
//
// It blocks until somebody joins the same room and proves they hold the same
// key. Somebody who does not hold it is not the opponent -- they learnt or
// guessed the room and nothing more -- so the room is claimed again rather than
// the game being handed to whoever got there first. What that does not undo is
// that they were sent the invitation before they were turned away, so they have
// seen the host's name and the ruleset.
func HostViaRelay(ctx context.Context, relayAddr, code string, opts HostOptions) (Session, error) {
	room, key, err := splitPairingCode(code)
	if err != nil {
		return nil, err
	}
	reclaims := 0
	for {
		conn, err := dialRelay(ctx, relayAddr, room)
		if err != nil {
			if reclaims == 0 || reclaims > relayReclaimAttempts || ctx.Err() != nil {
				return nil, err
			}
			reclaims++
			select {
			case <-time.After(relayReclaimPause):
				continue
			case <-ctx.Done():
				return nil, err
			}
		}
		s, err := hostOverKeyed(ctx, conn, opts, key)
		if err == nil {
			return s, nil
		}
		_ = conn.Close()
		if !errors.Is(err, ErrUnauthenticated) || ctx.Err() != nil {
			return nil, err
		}
		reclaims = 1
	}
}

// JoinViaRelay joins a game through a relay using the pairing code the host gave
// out. The whole code is needed: without its key part this end could not tell
// the host's moves from a relay's.
func JoinViaRelay(ctx context.Context, relayAddr, code string, opts GuestOptions) (Session, error) {
	room, key, err := splitPairingCode(code)
	if err != nil {
		return nil, err
	}
	conn, err := dialRelay(ctx, relayAddr, room)
	if err != nil {
		return nil, err
	}
	s, err := joinOverKeyed(ctx, conn, opts, key)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// dialRelay connects to the relay and waits to be paired in one room. The wait
// is bounded by the context rather than by a timeout of its own: the host is
// waiting for another human to paste a code, which takes as long as it takes.
func dialRelay(ctx context.Context, relayAddr, room string) (net.Conn, error) {
	room, err := normalisePairingCode(room)
	if err != nil {
		return nil, err
	}
	target := addPort(relayAddr, DefaultRelayPort)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("connecting to the relay at %s: %w", target, err)
	}
	stop := closeOnCancel(ctx, conn)
	defer stop()

	if err := writeRelayLine(conn, relayJoin+" "+room); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("asking the relay for room %s: %w", room, err)
	}
	br := bufio.NewReader(conn)
	line, err := readRelayLine(br)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("waiting for the relay to pair us: %w", err)
	}
	switch {
	case line == relayOK:
		// The reply may have arrived in the same packet as the opponent's
		// first frame, so the buffer goes on to the protocol.
		return &bufConn{Conn: conn, r: br}, nil
	case strings.HasPrefix(line, relayErr+" "):
		// Whoever answered on the relay's port wrote this reason and it is
		// about to be printed, so it is filtered like any other outside text.
		_ = conn.Close()
		return nil, fmt.Errorf("the relay refused: %s", safeText(strings.TrimPrefix(line, relayErr+" "), relayMaxPrelude))
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("%w: the relay answered %q", ErrProtocol, safeText(line, relayMaxPrelude))
	}
}

// bufConn is a connection whose first bytes have already been read into a
// buffer. It is still a net.Conn, so deadlines and Close work as usual.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func writeRelayLine(w io.Writer, rest string) error {
	return writeAll(w, []byte(relayGreeting+" "+rest+"\n"))
}

// readRelayLine reads one prelude line and returns what follows the greeting.
// It reads a byte at a time so the length bound is exact; it is one short line
// once per connection.
func readRelayLine(r *bufio.Reader) (string, error) {
	line := make([]byte, 0, 32)
	for len(line) <= relayMaxPrelude {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == '\n' {
			rest, ok := strings.CutPrefix(strings.TrimSpace(string(line)), relayGreeting)
			if !ok {
				return "", fmt.Errorf("%w: expected a %s greeting", ErrProtocol, relayGreeting)
			}
			return strings.TrimSpace(rest), nil
		}
		line = append(line, c)
	}
	return "", fmt.Errorf("%w: the greeting line is longer than %d bytes", ErrProtocol, relayMaxPrelude)
}

// readJoin reads a client's prelude and returns the room it asked for.
func readJoin(r *bufio.Reader) (string, error) {
	line, err := readRelayLine(r)
	if err != nil {
		return "", err
	}
	rest, ok := strings.CutPrefix(line, relayJoin+" ")
	if !ok {
		return "", fmt.Errorf("expected %s followed by a pairing code", relayJoin)
	}
	return normalisePairingCode(rest)
}
