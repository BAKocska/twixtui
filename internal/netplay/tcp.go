package netplay

import (
	"context"
	"fmt"
	"net"
	"strings"
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

// Wait accepts connections until one completes the game handshake, then stops
// listening. A scanner, a silent socket or a client speaking another protocol
// does not consume the address the host already gave its opponent.
func (h *Listener) Wait(ctx context.Context, opts HostOptions) (Session, error) {
	if err := opts.config().check(); err != nil {
		return nil, err
	}
	for {
		conn, err := h.accept(ctx)
		if err != nil {
			return nil, err
		}
		s, err := HostOver(ctx, conn, opts)
		if err == nil {
			_ = h.l.Close()
			return s, nil
		}
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The joining end gets the handshake's specific refusal. The host
		// keeps the same invitation open for the real opponent.
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
