// Package netplay carries a TwixT game between two machines.
//
// The protocol is written once, over an io.ReadWriter, and each transport is a
// thin adapter that produces one:
//
//   - direct TCP (tcp.go): one player listens, the other dials. Zero
//     infrastructure, and it is also how a game runs over a Tailscale or
//     WireGuard address, or through a forwarded port.
//   - relay (relay.go): both players dial out to a third machine running the
//     same binary, which pairs them by a short code. This is for the common
//     case where neither end can accept an inbound connection.
//   - correspondence codes (code.go): no live connection at all. Each move
//     becomes a short string the players paste to each other over any chat.
//     Its failure modes are disjoint from the other two: it needs no socket,
//     no relay and no reachability.
//
// Every transport carries the same moves and the same position hashes, so a
// divergence between the two ends is caught the moment it happens rather than
// discovered several moves later.
package netplay

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"sync"
	"time"
)

// Version is the wire protocol version this build speaks. It appears in the
// header of every frame and again inside the handshake. The header copy lets a
// peer speaking a different version be refused before its payload is
// interpreted at all; the handshake copy is what produces the message the
// player sees.
const Version = 1

// Frame layout, integers big-endian:
//
//	0..1   magic 'T' 'X'
//	2      protocol version
//	3..6   payload length in bytes, 1..MaxFrameSize
//	7..10  CRC-32 (IEEE) of the payload
//	11..   payload: one JSON object with a "t" discriminator
//
// The magic stops a stream of noise from being read as a plausible frame, the
// length bound stops a hostile length prefix from asking for an unbounded
// allocation, and the checksum catches a payload that was corrupted rather
// than merely truncated.
const frameHeaderLen = 11

// MaxFrameSize bounds one payload. The largest message the protocol sends is a
// resync transcript, a few tens of bytes per move, so a megabyte is generous
// for any real game and still small enough that a hostile peer cannot make the
// receiver allocate freely.
const MaxFrameSize = 1 << 20

var frameMagic = [2]byte{'T', 'X'}

// Errors the protocol reports. They are sentinels so that a UI, and the tests,
// can react to the kind of failure rather than matching message text.
var (
	// ErrProtocol marks a peer that is not following the protocol.
	ErrProtocol = errors.New("protocol error")
	// ErrVersion marks a peer speaking a different protocol version.
	ErrVersion = errors.New("protocol version mismatch")
	// ErrRuleset marks a peer playing by different rules.
	ErrRuleset = errors.New("ruleset mismatch")
	// ErrDiverged marks two ends whose games are no longer the same game.
	ErrDiverged = errors.New("the two games have diverged")
	// ErrRefused marks a game the opponent declined.
	ErrRefused = errors.New("the opponent refused the game")
	// ErrClosed is returned by a session that has already finished.
	ErrClosed = errors.New("the session is closed")
	// ErrBadCode marks a correspondence code that cannot be trusted.
	ErrBadCode = errors.New("bad move code")
)

// msgType is the discriminator of a protocol message.
type msgType string

const (
	msgHello      msgType = "hello"       // host opens: version, rules, name, side
	msgAccept     msgType = "accept"      // guest agrees: version, name, its side
	msgReject     msgType = "reject"      // either end refuses, with a reason
	msgResync     msgType = "resync"      // moves the peer is missing after a drop
	msgMove       msgType = "move"        // one committed move plus the resulting hash
	msgResign     msgType = "resign"      //
	msgDrawOffer  msgType = "draw-offer"  //
	msgDrawAccept msgType = "draw-accept" //
	msgPing       msgType = "ping"        // keepalive, one way: no reply is sent
	msgBye        msgType = "bye"         // clean shutdown
)

// wireEntry is one transcript line as it travels: a move in the engine's
// notation together with the side that made it. The side is explicit because
// the notation for resign and for the two draw messages does not name a
// player, and any of the three may legitimately come from the side that is not
// to move.
type wireEntry struct {
	Side string `json:"side"`
	Move string `json:"mv"`
}

// message is every protocol message in one struct. A single shape keeps the
// encoder and the decoder honest about which fields exist, at the cost of a
// few unused fields per message.
type message struct {
	Type msgType `json:"t"`

	Version     int    `json:"v,omitempty"`
	Name        string `json:"name,omitempty"`
	Rules       string `json:"rules,omitempty"` // game.Ruleset.Canonical()
	Fingerprint string `json:"fp,omitempty"`    // game.Ruleset.Fingerprint()
	Side        string `json:"side,omitempty"`  // "vertical" or "horizontal"
	Resume      bool   `json:"resume,omitempty"`

	Entries int    `json:"n,omitempty"`  // record entries after this message
	Move    string `json:"mv,omitempty"` // move in the engine's notation
	PosHash string `json:"ph,omitempty"` // PositionHash after this message
	Digest  string `json:"td,omitempty"` // digest of the whole transcript so far

	Replay []wireEntry `json:"replay,omitempty"`
	Text   string      `json:"text,omitempty"`
}

// framer reads and writes frames on one connection. Writes are serialised
// because the keepalive ticker and the player's own moves share the
// connection, and both buffers are reused because a session writes one frame
// per human move and there is no reason to allocate for each.
type framer struct {
	r       *bufio.Reader
	w       io.Writer
	conn    net.Conn // non-nil when the transport supports deadlines
	timeout time.Duration

	mu  sync.Mutex
	out []byte

	in []byte // read side, only touched by the read loop
}

func newFramer(rw io.ReadWriter, timeout time.Duration) *framer {
	f := &framer{
		r:       bufio.NewReader(rw),
		w:       rw,
		timeout: timeout,
	}
	if c, ok := rw.(net.Conn); ok {
		f.conn = c
	}
	return f
}

// write sends one message as a single frame.
func (f *framer) write(m message) error {
	return f.writeTimeout(m, f.timeout)
}

// writeTimeout sends one message with an explicit write deadline.
func (f *framer) writeTimeout(m message, timeout time.Duration) error {
	payload, err := marshalMessage(m)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writePayload(payload, timeout)
}

// tryWriteTimeout sends a courtesy message only when no ordinary write owns the
// connection. Shutdown uses it before closing rw: waiting for the mutex would
// put Close behind the very stuck write that closing rw has to unblock.
func (f *framer) tryWriteTimeout(m message, timeout time.Duration) bool {
	payload, err := marshalMessage(m)
	if err != nil || !f.mu.TryLock() {
		return false
	}
	defer f.mu.Unlock()
	return f.writePayload(payload, timeout) == nil
}

func marshalMessage(m message) ([]byte, error) {
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encoding a %s message: %w", m.Type, err)
	}
	if len(payload) > MaxFrameSize {
		return nil, fmt.Errorf("a %s message is %d bytes, over the %d byte frame limit", m.Type, len(payload), MaxFrameSize)
	}
	return payload, nil
}

// writePayload writes payload while the caller holds f.mu.
func (f *framer) writePayload(payload []byte, timeout time.Duration) error {
	f.out = f.out[:0]
	f.out = append(f.out, frameMagic[0], frameMagic[1], byte(Version))
	f.out = binary.BigEndian.AppendUint32(f.out, uint32(len(payload)))
	f.out = binary.BigEndian.AppendUint32(f.out, crc32.ChecksumIEEE(payload))
	f.out = append(f.out, payload...)
	if f.conn != nil && timeout > 0 {
		_ = f.conn.SetWriteDeadline(time.Now().Add(timeout))
		defer f.conn.SetWriteDeadline(time.Time{})
	}
	return writeAll(f.w, f.out)
}

// writeAll writes the whole buffer. An io.Writer is required to report a short
// write as an error, but a transport wrapper that splits writes is easy to get
// wrong and a truncated frame would desync the peer permanently, so the loop
// is here rather than trusted to be unnecessary.
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// read returns the next message. It reassembles frames split across any number
// of reads, which is the normal case on a real network.
func (f *framer) read() (message, error) {
	var hdr [frameHeaderLen]byte
	if _, err := io.ReadFull(f.r, hdr[:]); err != nil {
		return message{}, err
	}
	if hdr[0] != frameMagic[0] || hdr[1] != frameMagic[1] {
		return message{}, fmt.Errorf("%w: this is not a twixt connection (frame starts %#x %#x)", ErrProtocol, hdr[0], hdr[1])
	}
	if int(hdr[2]) != Version {
		return message{}, fmt.Errorf("%w: the opponent speaks protocol version %d and this build speaks version %d; both ends need the same twixtui release", ErrVersion, hdr[2], Version)
	}
	n := binary.BigEndian.Uint32(hdr[3:7])
	sum := binary.BigEndian.Uint32(hdr[7:11])
	if n == 0 {
		return message{}, fmt.Errorf("%w: empty frame", ErrProtocol)
	}
	if n > MaxFrameSize {
		return message{}, fmt.Errorf("%w: the opponent announced a %d byte frame, over the %d byte limit", ErrProtocol, n, MaxFrameSize)
	}
	if cap(f.in) < int(n) {
		f.in = make([]byte, n)
	}
	f.in = f.in[:n]
	if _, err := io.ReadFull(f.r, f.in); err != nil {
		return message{}, err
	}
	if got := crc32.ChecksumIEEE(f.in); got != sum {
		return message{}, fmt.Errorf("%w: frame checksum is %#08x, expected %#08x", ErrProtocol, got, sum)
	}
	var m message
	if err := json.Unmarshal(f.in, &m); err != nil {
		return message{}, fmt.Errorf("%w: malformed message: %w", ErrProtocol, err)
	}
	if m.Type == "" {
		return message{}, fmt.Errorf("%w: message without a type", ErrProtocol)
	}
	return m, nil
}
