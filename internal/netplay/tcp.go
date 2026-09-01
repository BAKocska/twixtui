package netplay

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// DefaultPort is the port a direct game uses when an address gives none.
const DefaultPort = "4270"

// Listener accepts exactly one opponent for a hosted game.
//
// Binding is a separate step from waiting so that a host who asked for port 0,
// or who wants to show the player the address to pass on, can read the address
// it actually got before it blocks for the opponent.
type Listener struct {
	l net.Listener
}

// Bind opens a listening socket for one game. The address may be a bare port
// (":4270"), a host and port, or empty for the default port on every interface.
func Bind(addr string) (*Listener, error) {
	target := NormalizeAddr(addr)
	l, err := net.Listen("tcp", target)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", target, err)
	}
	return &Listener{l: l}, nil
}

// Addr is the address actually bound, which is what to show the opponent.
func (h *Listener) Addr() string { return h.l.Addr().String() }

// maxPendingJoins bounds how many unproven connections a hosting listener works
// on at once. Since the handshakes run concurrently, the bound is not what keeps
// a silent socket from monopolising the host; it is what keeps a flood of them
// from costing the host an unbounded number of goroutines and sockets. A
// connection is not accepted until a slot is free, so the surplus waits in the
// kernel's backlog and holds nothing of the host's.
const maxPendingJoins = 8

// DefaultJoinTimeout bounds one unproven connection's handshake on a hosting
// listener.
//
// It is far shorter than DefaultHandshakeTimeout because there is no human in
// this exchange: the host writes its invitation the moment the socket opens, and
// a real opponent's client answers within a round trip. Anything that has not
// answered by now is a scanner, a mistyped address or a stalled socket.
const DefaultJoinTimeout = 5 * time.Second

// Wait accepts connections until one completes the game handshake, then stops
// listening. A scanner, a silent socket or a client speaking another protocol
// does not consume the address the host already gave its opponent.
//
// The handshakes run concurrently and each is bounded by DefaultJoinTimeout,
// which is what stops a connection that says nothing from keeping the invited
// opponent out. One at a time, a silent socket owned the listener for a whole
// handshake timeout, and twenty of them owned it for twenty of those.
func (h *Listener) Wait(ctx context.Context, opts HostOptions) (Session, error) {
	cfg := opts.config()
	if err := cfg.check(); err != nil {
		return nil, err
	}
	opts.HandshakeTimeout = joinBound(cfg.HandshakeTimeout)

	// Cancelling on the way out is the cleanup: it closes the listener and
	// abandons every handshake still in progress. It cannot harm the session
	// being returned, because a finished handshake no longer watches the
	// context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	won := make(chan Session, 1)
	failed := make(chan error, 1)
	go h.attempts(ctx, opts, won, failed)

	select {
	case s := <-won:
		_ = h.l.Close()
		return s, nil
	case err := <-failed:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// joinBound is how long one unproven connection may take. A caller asking for
// less gets what it asked for; a caller asking for more, or for the default,
// gets DefaultJoinTimeout, because a listener is the one place where the
// connection has not been shown to be the opponent yet.
func joinBound(want time.Duration) time.Duration {
	if want > 0 && want < DefaultJoinTimeout {
		return want
	}
	return DefaultJoinTimeout
}

// attempts accepts connections and hands each its own handshake, reporting the
// first that succeeds. Exactly one game is on offer here, so a handshake that
// completes second is told so and closed.
func (h *Listener) attempts(ctx context.Context, opts HostOptions, won chan<- Session, failed chan<- error) {
	slots := make(chan struct{}, maxPendingJoins)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		conn, err := h.accept(ctx)
		if err != nil {
			select {
			case failed <- err:
			default:
			}
			return
		}
		go func() {
			defer func() { <-slots }()
			s, err := HostOver(ctx, conn, opts)
			if err != nil {
				// The joining end gets the handshake's specific refusal. The
				// host keeps the same invitation open for the real opponent.
				_ = conn.Close()
				return
			}
			select {
			case won <- s:
			default:
				_ = s.Close()
			}
		}()
	}
}

// Close gives up the listening socket. It is safe to call after Wait.
func (h *Listener) Close() error { return h.l.Close() }

// accept is Accept with a context: cancelling closes the listener, which is the
// only way to interrupt a blocked Accept.
func (h *Listener) accept(ctx context.Context) (net.Conn, error) {
	type accepted struct {
		conn net.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := h.l.Accept()
		ch <- accepted{c, err}
	}()
	select {
	case a := <-ch:
		if a.err != nil {
			return nil, fmt.Errorf("waiting for an opponent: %w", a.err)
		}
		return a.conn, nil
	case <-ctx.Done():
		_ = h.l.Close()
		if a := <-ch; a.conn != nil {
			_ = a.conn.Close()
		}
		return nil, ctx.Err()
	}
}

// Listen binds addr and blocks until the opponent arrives. Use Bind when the
// bound address has to be shown first, as with port 0.
func Listen(ctx context.Context, addr string, opts HostOptions) (Session, error) {
	h, err := Bind(addr)
	if err != nil {
		return nil, err
	}
	s, err := h.Wait(ctx, opts)
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	return s, nil
}

// Dial joins a game hosted at addr. The address is whatever reaches the host: a
// machine on the same network, a tailnet or WireGuard address, or a forwarded
// port. Nothing here is specific to any of those.
func Dial(ctx context.Context, addr string, opts GuestOptions) (Session, error) {
	var d net.Dialer
	target := NormalizeAddr(addr)
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", target, err)
	}
	s, err := JoinOver(ctx, conn, opts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// NormalizeAddr fills in DefaultPort when an address does not name a port, so
// that a player can type a bare host name or a bare port.
func NormalizeAddr(addr string) string { return addPort(addr, DefaultPort) }

func addPort(addr, port string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ":" + port
	}
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if p != "" {
			return addr
		}
		return addr + port // "host:" or ":"
	}
	// SplitHostPort failed, so there is no port at all. A bare IPv6 literal
	// has to be bracketed before one can be appended.
	if strings.HasPrefix(addr, "[") {
		return addr + ":" + port
	}
	if strings.Contains(addr, ":") {
		return "[" + addr + "]:" + port
	}
	return addr + ":" + port
}
