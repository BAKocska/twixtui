package netplay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// The relay exists for the common case where neither player can accept an
// inbound connection: home routers with no port forwarding, carrier-grade NAT
// on mobile networks, hotel and campus networks that block unsolicited inbound
// traffic. Both players only dial out, which defeats all of those without
// having to reason about what kind of NAT is in the way.
//
// It is deliberately a byte pump. It pairs two connections that arrived with
// the same pairing code and copies bytes between them without looking at them,
// so it never needs to understand the game, never holds game state, cannot
// desync anybody, and does not need updating when the protocol changes. That is
// also why it is safe to self-host: there is nothing in it to trust with the
// game beyond delivery.
//
// The pairing prelude is one line each way, which is all the relay ever parses:
//
//	client -> relay   TWIXT-RELAY/1 JOIN <code>\n
//	relay  -> client  TWIXT-RELAY/1 OK\n
//	relay  -> client  TWIXT-RELAY/1 ERR <reason>\n
//
// After OK the connection carries protocol frames and the relay is blind.

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
	// relayCodeMaxLen bounds a pairing code. Codes are generated at six
	// characters; the slack is for players who type their own.
	relayCodeMaxLen = 32
	// DefaultPairingWait is how long the first player waits in a room for the
	// second. It is generous because the player has to pass the code on by
	// hand, over whatever chat they use.
	DefaultPairingWait = 10 * time.Minute
	// DefaultMaxRelayConnections bounds the relay's live sockets and handler
	// goroutines. Operators may lower it before Serve starts.
	DefaultMaxRelayConnections = 512
	// DefaultRelayIdleTimeout releases a paired room that carries no bytes.
	// Real sessions send keepalive frames well inside this window.
	DefaultRelayIdleTimeout = 2 * time.Minute
)

// pairingCodeLen is the length of a generated code. Six characters of the
// 32-character alphabet is a shade over 30 bits, which is ample for telling
// concurrent games apart without being a chore to read out.
const pairingCodeLen = 6

// PairingCode returns a fresh pairing code for a relayed game. Codes are
// case-insensitive and avoid the letters that are easily confused with digits,
// because a player has to read one out or paste it into a chat.
func PairingCode() string { return randomCode(pairingCodeLen) }

// normalisePairingCode puts a code the player typed into the form the relay
// matches on, mapping the characters that are read wrongly by eye.
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

// Relay pairs clients by pairing code and copies bytes between them.
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

	mu     sync.Mutex
	rooms  map[string]*room
	active int
}

// room is one pairing code's state on the relay.
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
}

// NewRelay returns a relay with no rooms.
func NewRelay() *Relay {
	return &Relay{rooms: make(map[string]*room)}
}

// Rooms reports how many pairing codes the relay is holding, which is the only
// thing it knows about the games it carries.
func (r *Relay) Rooms() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rooms)
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
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = writeRelayLine(conn, relayErr+" relay is busy; try again later")
			_ = conn.Close()
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

// handle reads one client's prelude, pairs it, and then pumps its side of the
// conversation.
func (r *Relay) handle(ctx context.Context, conn net.Conn) {
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(relayPreludeTimeout))
	code, err := readJoin(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = writeRelayLine(conn, relayErr+" "+err.Error())
		_ = conn.Close()
		return
	}

	me := &client{conn: conn, r: br, paired: make(chan *client, 1)}
	partner, rm, err := r.pair(ctx, code, me)
	if err != nil {
		_ = writeRelayLine(conn, relayErr+" "+err.Error())
		_ = conn.Close()
		return
	}
	if partner == nil { // ctx cancelled while waiting
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

	// One direction each. The reader renews a rolling idle deadline whenever
	// bytes arrive. Whichever direction ends first closes both connections,
	// which unblocks the other copy.
	src := &idleReader{conn: conn, r: me.r, timeout: r.idleTimeout()}
	_, _ = io.Copy(partner.conn, src)
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

// pair registers a client in its room and returns its opponent and the exact
// room generation they paired in. A nil partner with a nil error means the
// context was cancelled while waiting.
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
		r.mu.Unlock()
		partner.paired <- me // buffered, so this never blocks
		return partner, rm, nil
	default:
		rm.waiting = me
		r.mu.Unlock()
	}

	select {
	case partner := <-me.paired:
		return partner, rm, nil
	case <-time.After(r.wait()):
		// Check under the lock: an opponent may have arrived in the instant
		// the timer fired, in which case it has already been handed over.
		r.mu.Lock()
		if rm.waiting == me {
			rm.waiting = nil
			r.forget(code, rm)
			r.mu.Unlock()
			return nil, nil, fmt.Errorf("nobody joined with the code %s", code)
		}
		r.mu.Unlock()
		return <-me.paired, rm, nil
	case <-ctx.Done():
		r.mu.Lock()
		if rm.waiting == me {
			rm.waiting = nil
			r.forget(code, rm)
			r.mu.Unlock()
			return nil, nil, nil
		}
		r.mu.Unlock()
		return <-me.paired, rm, nil
	}
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

// HostViaRelay hosts a game through a relay under the given pairing code, which
// the player must pass to the opponent. It blocks until the opponent joins the
// same code.
func HostViaRelay(ctx context.Context, relayAddr, code string, opts HostOptions) (Session, error) {
	conn, err := dialRelay(ctx, relayAddr, code)
	if err != nil {
		return nil, err
	}
	s, err := HostOver(ctx, conn, opts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// JoinViaRelay joins a game through a relay using the pairing code the host
// gave out.
func JoinViaRelay(ctx context.Context, relayAddr, code string, opts GuestOptions) (Session, error) {
	conn, err := dialRelay(ctx, relayAddr, code)
	if err != nil {
		return nil, err
	}
	s, err := JoinOver(ctx, conn, opts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// dialRelay connects to the relay and waits to be paired. The wait is bounded
// by the context rather than by a timeout of its own: the host is waiting for
// another human to paste a code, which takes as long as it takes.
func dialRelay(ctx context.Context, relayAddr, code string) (net.Conn, error) {
	code, err := normalisePairingCode(code)
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

	if err := writeRelayLine(conn, relayJoin+" "+code); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("asking the relay for room %s: %w", code, err)
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
		_ = conn.Close()
		return nil, fmt.Errorf("the relay refused: %s", strings.TrimPrefix(line, relayErr+" "))
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("%w: the relay answered %q", ErrProtocol, line)
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

// readJoin reads a client's prelude and returns the pairing code it asked for.
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
