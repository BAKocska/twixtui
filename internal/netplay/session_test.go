package netplay

import (
	"errors"
	"hash/crc32"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// TestFullGameOverPipe plays the whole scripted game between two real sessions
// over an in-memory connection, checking both ends after every move.
func TestFullGameOverPipe(t *testing.T) {
	host, guest := mustConnectPipe(t)
	playScript(t, host, guest, scriptedGame)
}

// TestHostChoosesSideAndGuestIsTold covers the colour negotiation: the host's
// choice is explicit and the guest is given the other side and told so.
func TestHostChoosesSideAndGuestIsTold(t *testing.T) {
	for _, hostSide := range []game.Player{game.Vertical, game.Horizontal} {
		t.Run(hostSide.String(), func(t *testing.T) {
			hopts := hostOpts()
			hopts.Side = hostSide
			host, guest, hostErr, guestErr := connectPipe(t, hopts, guestOpts())
			if hostErr != nil || guestErr != nil {
				t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
			}
			if got := host.Side(); got != hostSide {
				t.Fatalf("the host plays %s, it chose %s", got, hostSide)
			}
			if got := guest.Side(); got != hostSide.Opponent() {
				t.Fatalf("the guest plays %s, it should have been given %s", got, hostSide.Opponent())
			}
			if got := host.OpponentName(); got != "grace" {
				t.Fatalf("the host calls its opponent %q", got)
			}
			if got := guest.OpponentName(); got != "ada" {
				t.Fatalf("the guest calls its opponent %q", got)
			}
			// The guest has to be told which side it got, not left to work it
			// out, so the opening event says so in words.
			ev := wantEvent(t, guest, EventConnected)
			if !strings.Contains(ev.Text, hostSide.Opponent().String()) {
				t.Fatalf("the guest was greeted with %q, which does not say it plays %s", ev.Text, hostSide.Opponent())
			}
			wantEvent(t, host, EventConnected)
		})
	}
}

// TestHostMustChooseASide keeps the choice from defaulting silently.
func TestHostMustChooseASide(t *testing.T) {
	hc, _ := net.Pipe()
	opts := hostOpts()
	opts.Side = game.NoPlayer
	if _, err := HostOver(t.Context(), hc, opts); err == nil {
		t.Fatal("a host with no side was accepted")
	}
}

// TestGuestCanInsistOnASide covers a guest that asked for a side the host took.
func TestGuestCanInsistOnASide(t *testing.T) {
	gopts := guestOpts()
	gopts.Side = game.Vertical // the same side hostOpts takes
	_, _, hostErr, guestErr := connectPipe(t, hostOpts(), gopts)
	if guestErr == nil {
		t.Fatal("the guest accepted a game on the wrong side")
	}
	if !errors.Is(guestErr, ErrProtocol) {
		t.Fatalf("the guest reported %v", guestErr)
	}
	if !strings.Contains(guestErr.Error(), "horizontal") {
		t.Fatalf("the guest's complaint does not say which side it would get: %v", guestErr)
	}
	if !errors.Is(hostErr, ErrRefused) {
		t.Fatalf("the host reported %v, want a refusal", hostErr)
	}
}

// TestRulesetMismatchIsRefused checks a mismatch is refused before the first
// move and that the refusal names the difference, on both ends.
func TestRulesetMismatchIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		guest game.Ruleset
		says  string
	}{
		{
			name: "different rules",
			guest: func() game.Ruleset {
				rs := game.PP
				rs.Size = 6
				return rs
			}(),
			says: "choosing your own links",
		},
		{
			name: "different board size",
			guest: func() game.Ruleset {
				rs := game.Std
				rs.Size = 8
				return rs
			}(),
			says: "board is 8x8 here and 6x6 there",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gopts := guestOpts()
			gopts.Rules = c.guest
			_, _, hostErr, guestErr := connectPipe(t, hostOpts(), gopts)
			if guestErr == nil {
				t.Fatal("the guest accepted a game under different rules")
			}
			if !errors.Is(guestErr, ErrRuleset) {
				t.Fatalf("the guest reported %v, want a ruleset mismatch", guestErr)
			}
			if !strings.Contains(guestErr.Error(), c.says) {
				t.Fatalf("the guest's complaint does not name the difference: %v", guestErr)
			}
			if !errors.Is(hostErr, ErrRefused) {
				t.Fatalf("the host reported %v, want a refusal", hostErr)
			}
			if !strings.Contains(hostErr.Error(), c.says) {
				t.Fatalf("the host was not told the difference: %v", hostErr)
			}
		})
	}
}

// TestProtocolVersionMismatchIsRefused covers both halves of the version check:
// the byte in every frame header, and the version inside the handshake.
func TestProtocolVersionMismatchIsRefused(t *testing.T) {
	t.Run("frame header", func(t *testing.T) {
		gc, pc := net.Pipe()
		go func() {
			// A frame from a build two versions on. The payload is deliberately
			// well formed: the header alone has to stop it.
			payload := []byte(`{"t":"hello"}`)
			_ = writeAll(pc, rawFrame(Version+1, uint32(len(payload)), crc32.ChecksumIEEE(payload), payload))
		}()
		_, err := JoinOver(t.Context(), gc, guestOpts())
		if !errors.Is(err, ErrVersion) {
			t.Fatalf("got %v, want a version mismatch", err)
		}
		if !strings.Contains(err.Error(), "version 2") {
			t.Fatalf("the complaint does not name the version: %v", err)
		}
	})

	t.Run("handshake", func(t *testing.T) {
		gc, pc := net.Pipe()
		peer := newRawPeer(pc)
		rs := testRules()
		done := make(chan error, 1)
		go func() {
			_, err := JoinOver(t.Context(), gc, guestOpts())
			done <- err
		}()
		peer.send(t, message{
			Type:        msgHello,
			Version:     Version + 7,
			Name:        "future",
			Rules:       rs.Canonical(),
			Fingerprint: rs.Fingerprint(),
			Side:        game.Vertical.String(),
			Digest:      transcriptDigest(nil),
		})
		// The guest also tells the host why, rather than only failing locally.
		refusal := peer.recv(t)
		if refusal.Type != msgReject {
			t.Fatalf("the guest answered with a %q", refusal.Type)
		}
		if !strings.Contains(refusal.Text, "version") {
			t.Fatalf("the refusal sent to the host was %q", refusal.Text)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrVersion) {
				t.Fatalf("got %v, want a version mismatch", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the guest never gave up")
		}
	})
}

// TestDivergenceIsCaught is the check that a wrong position hash stops the game
// at once. The first subtest is the control: the same move with the right hash
// has to be accepted, or the second subtest would prove nothing.
func TestDivergenceIsCaught(t *testing.T) {
	t.Run("matching hash is accepted", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, hostOpts())
		if err := host.SendMove("B1"); err != nil {
			t.Fatalf("the host could not open: %v", err)
		}
		peer.recv(t) // the host's move

		mirror := build(t, v("B1"), h("A2"))
		peer.send(t, message{Type: msgMove, Move: "A2", Entries: 2, PosHash: PositionHash(mirror)})
		ev := wantEvent(t, host, EventMove)
		if ev.Move != "A2" {
			t.Fatalf("the host saw %q", ev.Move)
		}
	})

	t.Run("wrong hash ends the game", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, hostOpts())
		if err := host.SendMove("B1"); err != nil {
			t.Fatalf("the host could not open: %v", err)
		}
		peer.recv(t)

		// A legal move, in turn, at the right move number, but the sender
		// claims a position this end cannot reproduce.
		wrong := PositionHash(build(t, v("B1"), h("C2")))
		peer.send(t, message{Type: msgMove, Move: "A2", Entries: 2, PosHash: wrong})
		ev := wantEvent(t, host, EventError)
		if !errors.Is(ev.Err, ErrDiverged) {
			t.Fatalf("the host reported %v, want a divergence", ev.Err)
		}
		if !strings.Contains(ev.Text, "hashes to") {
			t.Fatalf("the report does not show the two hashes: %q", ev.Text)
		}
		if got := positionOf(t, host).Entries(); got != 1 {
			t.Fatalf("the rejected peer move was kept in the position: %d entries", got)
		}
		saved, err := Save(host)
		if err != nil {
			t.Fatalf("saving after divergence: %v", err)
		}
		if len(saved.Moves) != 1 || saved.Moves[0].Move != "B1" {
			t.Fatalf("the rejected peer move was kept in the transcript: %+v", saved.Moves)
		}
		wantClosed(t, host)
	})

	t.Run("wrong move number ends the game", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, hostOpts())
		if err := host.SendMove("B1"); err != nil {
			t.Fatalf("the host could not open: %v", err)
		}
		peer.recv(t)

		mirror := build(t, v("B1"), h("A2"))
		peer.send(t, message{Type: msgMove, Move: "A2", Entries: 9, PosHash: PositionHash(mirror)})
		ev := wantEvent(t, host, EventError)
		if !errors.Is(ev.Err, ErrDiverged) {
			t.Fatalf("the host reported %v, want a divergence", ev.Err)
		}
	})
}

// TestOutOfTurnMoveIsRefused stops a peer playing twice in a row.
func TestOutOfTurnMoveIsRefused(t *testing.T) {
	host, peer := hostAgainstRaw(t, hostOpts())
	// It is the host's turn, so a move from the guest is out of turn.
	peer.send(t, message{Type: msgMove, Move: "A2", Entries: 1, PosHash: strings.Repeat("0", 64)})
	ev := wantEvent(t, host, EventError)
	if !errors.Is(ev.Err, ErrProtocol) {
		t.Fatalf("the host reported %v", ev.Err)
	}
	if !strings.Contains(ev.Text, "out of turn") {
		t.Fatalf("the report was %q", ev.Text)
	}
}

// TestIllegalMoveFromThePeerIsRefused covers a peer whose engine disagrees
// about the rules badly enough to send a move that cannot be played.
func TestIllegalMoveFromThePeerIsRefused(t *testing.T) {
	host, peer := hostAgainstRaw(t, hostOpts())
	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("the host could not open: %v", err)
	}
	peer.recv(t)
	// A2 is horizontal's own border column, but B1 is vertical's; horizontal
	// may not play there.
	peer.send(t, message{Type: msgMove, Move: "B1", Entries: 2, PosHash: strings.Repeat("0", 64)})
	ev := wantEvent(t, host, EventError)
	if !errors.Is(ev.Err, ErrProtocol) {
		t.Fatalf("the host reported %v", ev.Err)
	}
	if !strings.Contains(ev.Text, "illegal move") {
		t.Fatalf("the report was %q", ev.Text)
	}
}

// TestLocalMoveIsCheckedBeforeItIsSent keeps a player's own mistake local.
func TestLocalMoveIsCheckedBeforeItIsSent(t *testing.T) {
	host, guest := mustConnectPipe(t)

	if err := host.SendMove("A2"); err == nil {
		t.Fatal("the host was allowed to play in its opponent's border column")
	}
	if err := guest.SendMove("A2"); !errors.Is(err, game.ErrNotYourTurn) {
		t.Fatalf("the guest playing out of turn gave %v", err)
	}
	// The rejected attempts must not have moved either game on.
	if got := positionOf(t, host).Entries(); got != 0 {
		t.Fatalf("the host's record has %d entries after two refused moves", got)
	}
	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("a legal move was refused: %v", err)
	}
	wantEvent(t, guest, EventMove)
}

// TestResignTravels covers a resignation from the side to move and from the
// side that is not, which is the case the notation has to name a player for.
func TestResignTravels(t *testing.T) {
	t.Run("in turn", func(t *testing.T) {
		host, guest := mustConnectPipe(t)
		if err := host.SendResign(); err != nil {
			t.Fatalf("resigning: %v", err)
		}
		ev := wantEvent(t, guest, EventResign)
		if !strings.Contains(ev.Text, "ada") {
			t.Fatalf("the guest was told %q", ev.Text)
		}
		assertAgree(t, host, guest, 1)
		if got := positionOf(t, guest).Result(); got.Outcome != game.HorizontalWins || got.Reason != game.Resignation {
			t.Fatalf("the result is %+v", got)
		}
	})

	t.Run("while the opponent is thinking", func(t *testing.T) {
		host, guest := mustConnectPipe(t)
		// It is the host's turn, and the guest resigns anyway.
		if err := guest.SendResign(); err != nil {
			t.Fatalf("resigning: %v", err)
		}
		wantEvent(t, host, EventResign)
		assertAgree(t, host, guest, 1)
		if got := positionOf(t, host).Result(); got.Outcome != game.VerticalWins {
			t.Fatalf("the result is %+v", got)
		}
	})
}

// TestDrawOfferAndAcceptTravel covers the two draw messages, including that the
// standing offer is part of the position both ends verify.
func TestDrawOfferAndAcceptTravel(t *testing.T) {
	host, guest := mustConnectPipe(t)
	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("opening: %v", err)
	}
	wantEvent(t, guest, EventMove)

	if err := host.SendDrawOffer(); err != nil {
		t.Fatalf("offering a draw: %v", err)
	}
	wantEvent(t, guest, EventDrawOffer)
	assertAgree(t, host, guest, 2)
	if got := positionOf(t, guest).DrawOfferedBy(); got != game.Vertical {
		t.Fatalf("the guest thinks the offer came from %s", got)
	}

	if err := guest.SendDrawAccept(); err != nil {
		t.Fatalf("accepting a draw: %v", err)
	}
	wantEvent(t, host, EventDrawAccept)
	assertAgree(t, host, guest, 3)
	if got := positionOf(t, host).Result(); got.Outcome != game.Draw || got.Reason != game.Agreement {
		t.Fatalf("the result is %+v", got)
	}
}

// TestSendMoveRoutesRecordEntries lets a caller pass a whole transcript line to
// SendMove, including the side-tagged forms the engine writes.
func TestSendMoveRoutesRecordEntries(t *testing.T) {
	host, guest := mustConnectPipe(t)
	if err := host.SendMove("v:draw?"); err != nil {
		t.Fatalf("sending a tagged draw offer: %v", err)
	}
	wantEvent(t, guest, EventDrawOffer)
	if err := guest.SendMove("resign"); err != nil {
		t.Fatalf("sending a bare resignation: %v", err)
	}
	wantEvent(t, host, EventResign)
	assertAgree(t, host, guest, 2)
}

// TestSendMoveRefusesTheOpponentsRecordEntry stops one end resigning on behalf
// of the other. The refusal has to arrive before the entry is played, not after
// it: a caller passing a transcript line straight through sees one error either
// way, and only the record shows whether the game was ended behind it.
func TestSendMoveRefusesTheOpponentsRecordEntry(t *testing.T) {
	host, _ := mustConnectPipe(t)
	err := host.SendMove("h:resign")
	if err == nil {
		t.Fatal("the host was allowed to resign for its opponent")
	}
	if !strings.Contains(err.Error(), "h:resign") {
		t.Fatalf("the refusal does not name the entry it would not send: %v", err)
	}
	g := positionOf(t, host)
	if g.Result().Over() {
		t.Fatalf("the refused entry ended the game anyway: %+v", g.Result())
	}
	if g.Entries() != 0 {
		t.Fatalf("the refused entry left %d in the record", g.Entries())
	}
}

// chopConn splits every write into small pieces and can hold each one up, so a
// frame arrives across several reads with gaps between them. The chunk size is
// deliberately smaller than the frame header, so the splits land inside the
// magic, inside the length, inside the checksum and inside the payload.
type chopConn struct {
	net.Conn
	size  int
	delay time.Duration
}

func (c *chopConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(c.size, len(p))
		if c.delay > 0 {
			time.Sleep(c.delay)
		}
		w, err := c.Conn.Write(p[:n])
		written += w
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// TestFragmentedAndSlowConnection plays the whole game with every frame chopped
// into three-byte pieces and each piece delayed. Frame reassembly is the classic
// place for this protocol to go wrong.
func TestFragmentedAndSlowConnection(t *testing.T) {
	hc, gc := net.Pipe()
	host, guest, hostErr, guestErr := connectOver(t,
		&chopConn{Conn: hc, size: 3, delay: time.Millisecond},
		&chopConn{Conn: gc, size: 3, delay: time.Millisecond},
		hostOpts(), guestOpts())
	if hostErr != nil || guestErr != nil {
		t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	playScript(t, host, guest, scriptedGame)
}

// TestFramesArrivingTogetherAreBothRead is the other half of reassembly: two
// frames in a single write must not leave the second one sitting in the buffer.
func TestFramesArrivingTogetherAreBothRead(t *testing.T) {
	host, peer := hostAgainstRaw(t, hostOpts())
	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("opening: %v", err)
	}
	peer.recv(t)

	mirror := build(t, v("B1"), h("A2"))
	ping := framedMessage(t, message{Type: msgPing})
	move := framedMessage(t, message{Type: msgMove, Move: "A2", Entries: 2, PosHash: PositionHash(mirror)})
	peer.sendBytes(t, append(ping, move...))

	ev := wantEvent(t, host, EventMove)
	if ev.Move != "A2" {
		t.Fatalf("the host saw %q", ev.Move)
	}
}

// framedMessage encodes one message the way the framer does, for tests that need
// the bytes rather than the send.
func framedMessage(t *testing.T, m message) []byte {
	t.Helper()
	var buf sink
	f := newFramer(&buf, 0)
	if err := f.write(m); err != nil {
		t.Fatalf("framing a %s: %v", m.Type, err)
	}
	return buf.b
}

// sink collects writes.
type sink struct{ b []byte }

func (s *sink) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *sink) Read([]byte) (int, error) { return 0, nil }

// TestHostilePeer feeds a live session the things a broken or malicious peer
// sends. Every case has to end the session with an error, promptly, without
// hanging, panicking or trying to allocate what it was told to.
func TestHostilePeer(t *testing.T) {
	valid := func(t *testing.T, m message) []byte { return framedMessage(t, m) }

	cases := []struct {
		name    string
		bytes   func(*testing.T) []byte
		sentine error
		says    string
	}{
		{
			name:    "noise",
			bytes:   func(*testing.T) []byte { return []byte("GET / HTTP/1.1\r\nHost: twixt\r\n\r\n") },
			sentine: ErrProtocol,
			says:    "not a twixt connection",
		},
		{
			name:    "absurd length prefix",
			bytes:   func(*testing.T) []byte { return rawFrame(Version, 0xffffffff, 0, nil) },
			sentine: ErrProtocol,
			says:    "over the",
		},
		{
			name:    "payload larger than the limit",
			bytes:   func(*testing.T) []byte { return rawFrame(Version, 8<<20, 0, nil) },
			sentine: ErrProtocol,
			says:    "over the",
		},
		{
			name:    "empty frame",
			bytes:   func(*testing.T) []byte { return rawFrame(Version, 0, 0, nil) },
			sentine: ErrProtocol,
			says:    "empty frame",
		},
		{
			name:    "frame from another version",
			bytes:   func(*testing.T) []byte { return rawFrame(Version+3, 2, crc32.ChecksumIEEE([]byte("{}")), []byte("{}")) },
			sentine: ErrVersion,
			says:    "protocol version",
		},
		{
			name: "corrupted payload",
			bytes: func(t *testing.T) []byte {
				b := valid(t, message{Type: msgPing})
				b[len(b)-1] ^= 0xff
				return b
			},
			sentine: ErrProtocol,
			says:    "checksum",
		},
		{
			name: "payload that is not json",
			bytes: func(*testing.T) []byte {
				p := []byte("{not json at all")
				return rawFrame(Version, uint32(len(p)), crc32.ChecksumIEEE(p), p)
			},
			sentine: ErrProtocol,
			says:    "malformed message",
		},
		{
			name: "message with no type",
			bytes: func(*testing.T) []byte {
				p := []byte(`{"v":1}`)
				return rawFrame(Version, uint32(len(p)), crc32.ChecksumIEEE(p), p)
			},
			sentine: ErrProtocol,
			says:    "without a type",
		},
		{
			name:    "message this build does not know",
			bytes:   func(t *testing.T) []byte { return valid(t, message{Type: "cheat"}) },
			sentine: ErrProtocol,
			says:    "unexpected",
		},
		{
			name:    "handshake message after the handshake",
			bytes:   func(t *testing.T) []byte { return valid(t, message{Type: msgHello, Version: Version}) },
			sentine: ErrProtocol,
			says:    "unexpected",
		},
		{
			name:    "move with no position hash",
			bytes:   func(t *testing.T) []byte { return valid(t, message{Type: msgResign, Entries: 1}) },
			sentine: ErrProtocol,
			says:    "no position hash",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, peer := hostAgainstRaw(t, hostOpts())
			peer.sendBytes(t, c.bytes(t))
			ev := wantEvent(t, host, EventError)
			if !errors.Is(ev.Err, c.sentine) {
				t.Fatalf("reported %v, want %v", ev.Err, c.sentine)
			}
			if !strings.Contains(ev.Text, c.says) {
				t.Fatalf("the report was %q, expected it to mention %q", ev.Text, c.says)
			}
			wantClosed(t, host)
		})
	}
}

// TestTruncatedFrameEndsTheSession covers a peer that starts a frame and then
// disappears: the session has to finish rather than wait for ever.
func TestTruncatedFrameEndsTheSession(t *testing.T) {
	host, peer := hostAgainstRaw(t, hostOpts())
	whole := framedMessage(t, message{Type: msgMove, Move: "B1", Entries: 1, PosHash: PositionHash(build(t, v("B1")))})
	peer.sendBytes(t, whole[:len(whole)-4])
	_ = peer.conn.Close()

	ev := waitEvent(t, host)
	if ev.Kind != EventDisconnected && ev.Kind != EventError {
		t.Fatalf("got a %s event (%q)", ev.Kind, ev.Text)
	}
	wantClosed(t, host)
}

// TestKeepaliveNoticesADeadPeer covers an opponent that goes away without the
// connection being closed, which no read error would reveal on its own.
//
// The two cases reach the keepalive loop's two branches. A peer that has
// stopped reading makes the ping itself unwritable. A peer that still reads
// takes every ping, so the only thing left to end the game is the watch on how
// long it has been since anything arrived. With the unreadable case alone that
// watch could be deleted outright and this test would still pass, because the
// failed write reports the same thing a tick earlier.
func TestKeepaliveNoticesADeadPeer(t *testing.T) {
	tuned := func() HostOptions {
		opts := hostOpts()
		opts.Keepalive = 30 * time.Millisecond
		opts.DeadAfter = 150 * time.Millisecond
		return opts
	}
	noticed := func(t *testing.T, host Session) {
		t.Helper()
		started := time.Now()
		ev := wantEvent(t, host, EventDisconnected)
		if !strings.Contains(ev.Text, "stopped responding") {
			t.Fatalf("the report was %q", ev.Text)
		}
		if waited := time.Since(started); waited > 3*time.Second {
			t.Fatalf("it took %s to notice", waited)
		}
		wantClosed(t, host)
	}

	t.Run("a peer that stops answering", func(t *testing.T) {
		host, peer := hostAgainstRaw(t, tuned())
		peer.swallow()
		noticed(t, host)
	})

	t.Run("a peer that stops reading", func(t *testing.T) {
		host, _ := hostAgainstSilentRaw(t, tuned())
		noticed(t, host)
	})
}

// TestKeepaliveKeepsAResponsiveGameAlive is the control for the test above: with
// the same short timers, two real sessions survive an idle spell far longer
// than DeadAfter because each one's one-way pings count as traffic at the other.
func TestKeepaliveKeepsAResponsiveGameAlive(t *testing.T) {
	hopts := hostOpts()
	hopts.Keepalive = 20 * time.Millisecond
	hopts.DeadAfter = 100 * time.Millisecond
	gopts := guestOpts()
	gopts.Keepalive = 20 * time.Millisecond
	gopts.DeadAfter = 100 * time.Millisecond

	host, guest, hostErr, guestErr := connectPipe(t, hopts, gopts)
	if hostErr != nil || guestErr != nil {
		t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)

	time.Sleep(600 * time.Millisecond)

	if err := host.SendMove("B1"); err != nil {
		t.Fatalf("the game did not survive being idle: %v", err)
	}
	wantEvent(t, guest, EventMove)
}

// TestCloseTellsTheOpponent covers a player leaving on purpose.
func TestCloseTellsTheOpponent(t *testing.T) {
	host, guest := mustConnectPipe(t)
	if err := host.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	ev := wantEvent(t, guest, EventDisconnected)
	if ev.Text == "" {
		t.Fatal("the guest was told nothing")
	}
	wantClosed(t, guest)
	if err := guest.SendMove("B1"); !errors.Is(err, ErrClosed) {
		t.Fatalf("sending after the opponent left gave %v", err)
	}
	// Closing twice is what a UI will do on the way out.
	if err := host.Close(); err != nil {
		t.Fatalf("closing again: %v", err)
	}
}

// blockedWriteConn can stop one write before it reaches the underlying
// connection. Close releases it. It reproduces an in-flight move holding the
// framer mutex while the user tries to leave.
type blockedWriteConn struct {
	net.Conn
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockedWriteConn(c net.Conn) *blockedWriteConn {
	return &blockedWriteConn{Conn: c, entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (c *blockedWriteConn) Write(p []byte) (int, error) {
	if c.block.Load() {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		<-c.release
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *blockedWriteConn) Close() error {
	c.once.Do(func() { close(c.release) })
	return c.Conn.Close()
}

// TestCloseDoesNotWaitBehindAnInFlightWrite guards the shutdown ordering:
// closing the connection must unblock the writer before a courtesy goodbye
// tries to take the same framer mutex.
func TestCloseDoesNotWaitBehindAnInFlightWrite(t *testing.T) {
	hc, gc := net.Pipe()
	blocked := newBlockedWriteConn(hc)
	host, guest, hostErr, guestErr := connectOver(t, blocked, gc, hostOpts(), guestOpts())
	if hostErr != nil || guestErr != nil {
		t.Fatalf("handshake failed: host %v, guest %v", hostErr, guestErr)
	}
	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	blocked.block.Store(true)
	sendDone := make(chan error, 1)
	go func() { sendDone <- host.SendMove("B1") }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("the move never reached the blocked writer")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- host.Close() }()
	select {
	case <-closeDone:
		// This is the contract: Close did not queue behind the stuck write.
	case <-time.After(500 * time.Millisecond):
		blocked.once.Do(func() { close(blocked.release) })
		t.Fatal("Close blocked behind the in-flight write")
	}
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("closing did not release the in-flight write")
	}
}

// TestHandshakeRefusesTwoHosts covers two players who both chose to host, which
// on a relay is what a pairing code collision looks like. Real loopback TCP is
// used because its send buffer lets both hellos cross; net.Pipe deliberately
// has no buffer and two simultaneous first writes would instead time out.
func TestHandshakeRefusesTwoHosts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	b, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	a := <-accepted
	defer a.Close()
	defer b.Close()

	errs := make(chan error, 2)
	for _, c := range []net.Conn{a, b} {
		go func(c net.Conn) {
			_, err := HostOver(t.Context(), c, hostOpts())
			errs <- err
		}(c)
	}
	for range 2 {
		select {
		case err := <-errs:
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("got %v, want a protocol error", err)
			}
			if !strings.Contains(err.Error(), "hosting too") {
				t.Fatalf("the complaint was %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a host never gave up")
		}
	}
}

// TestHandshakeTimesOut covers a peer that connects and then says nothing.
func TestHandshakeTimesOut(t *testing.T) {
	gc, pc := net.Pipe()
	defer pc.Close()
	opts := guestOpts()
	opts.HandshakeTimeout = 100 * time.Millisecond
	started := time.Now()
	if _, err := JoinOver(t.Context(), gc, opts); err == nil {
		t.Fatal("the guest waited for ever and then succeeded anyway")
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Fatalf("the handshake took %s to give up", waited)
	}
}
