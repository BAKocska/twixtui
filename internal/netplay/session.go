package netplay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// Role says which end of a connection this is. The host sets the terms of the
// game: the ruleset, and which side it takes. The guest adopts the ruleset and
// is told which side it was given.
type Role int

// The two ends of a game.
const (
	Host Role = iota
	Guest
)

// String names the role.
func (r Role) String() string {
	if r == Guest {
		return "guest"
	}
	return "host"
}

// EventKind is what happened.
type EventKind int

// The events a session reports.
const (
	// EventConnected arrives once, first, when the handshake succeeded.
	EventConnected EventKind = iota
	// EventMove is a move the opponent made, in the engine's notation.
	EventMove
	// EventResign means the opponent resigned.
	EventResign
	// EventDrawOffer means the opponent offered a draw.
	EventDrawOffer
	// EventDrawAccept means the opponent accepted a standing draw offer.
	EventDrawAccept
	// EventDisconnected means the game is no longer connected. It is not by
	// itself a rules event: the game may be resumed, see Save.
	EventDisconnected
	// EventError is a protocol failure, including a detected divergence. The
	// session is finished when this arrives.
	EventError
)

// String names the event kind.
func (k EventKind) String() string {
	switch k {
	case EventConnected:
		return "connected"
	case EventMove:
		return "move"
	case EventResign:
		return "resign"
	case EventDrawOffer:
		return "draw offer"
	case EventDrawAccept:
		return "draw accepted"
	case EventDisconnected:
		return "disconnected"
	case EventError:
		return "error"
	}
	return "unknown"
}

// Event is one thing the opponent, or the connection, did.
type Event struct {
	Kind EventKind
	// Move is the move in the engine's notation, for EventMove.
	Move string
	// Err is the failure, for EventError.
	Err error
	// Text is a line fit to show the player.
	Text string
}

// Session is one end of a remote game. The caller owns its own game state; the
// session keeps a second copy in step with the opponent's and refuses anything
// that would let the two drift apart.
type Session interface {
	// Side reports which side this end plays.
	Side() game.Player
	// Rules reports the ruleset both ends agreed on.
	Rules() game.Ruleset
	// OpponentName reports the name the other end gave.
	OpponentName() string
	// SendMove plays a move in the engine's notation and sends it.
	SendMove(notation string) error
	SendResign() error
	SendDrawOffer() error
	SendDrawAccept() error
	// Events returns the stream of things the opponent did. It is closed when
	// the session finishes.
	Events() <-chan Event
	// Close ends the session and the underlying connection.
	Close() error
}

// Resumable is a session that can be carried over to a new connection after
// the old one dropped. Every session this package returns implements it; Save
// is the convenient way to reach it.
type Resumable interface {
	Session
	// Snapshot returns everything needed to resume the game.
	Snapshot() Snapshot
	// Position returns a copy of the session's own view of the game, which the
	// protocol keeps in step with the opponent's.
	Position() *game.Game
}

// Entry is one line of the shared transcript: a move in the engine's notation
// together with the side that made it. The side is recorded because resign and
// the two draw messages do not name a player in notation, and any of the three
// may come from the side that is not to move.
type Entry struct {
	Side game.Player
	Move string
}

// Snapshot is what a dropped game needs to be picked up again on a new
// connection. Pass it back through HostOptions.Resume or GuestOptions.Resume.
type Snapshot struct {
	Role     Role
	Rules    game.Ruleset
	Side     game.Player
	Name     string
	Opponent string
	Moves    []Entry
}

// Tuning holds the timing knobs. Zero values mean the defaults, which suit
// human-paced play; the tests use short ones.
type Tuning struct {
	// Keepalive is how often a ping goes out on an otherwise idle connection.
	Keepalive time.Duration
	// DeadAfter is how long a connection may carry no traffic at all before
	// the opponent is declared gone. It also bounds a single write, so a peer
	// that has stopped reading cannot block a move for ever.
	DeadAfter time.Duration
	// HandshakeTimeout bounds the handshake.
	HandshakeTimeout time.Duration
}

// Defaults for Tuning.
const (
	DefaultKeepalive        = 15 * time.Second
	DefaultHandshakeTimeout = 30 * time.Second
)

// eventBuffer is the slack in the event channel. A UI reads events promptly;
// the buffer only exists so that a session never blocks on its own read loop.
const eventBuffer = 32

// HostOptions configures the end that sets the terms of the game.
type HostOptions struct {
	// Name is the local player's name, shown to the guest.
	Name string
	// Rules is the ruleset both ends will play by. Required.
	Rules game.Ruleset
	// Side is the side the host takes; the guest is given the other one.
	// Required, because the choice of side is the player's to make.
	Side game.Player
	// Resume continues the game in the snapshot instead of starting a new one.
	// The snapshot's ruleset and side win over the fields above.
	Resume *Snapshot
	Tuning
}

// GuestOptions configures the end that joins.
type GuestOptions struct {
	// Name is the local player's name, shown to the host.
	Name string
	// Rules, when set, is the ruleset this end insists on: if the host offers
	// anything else the game is refused, naming the difference. Left zero, the
	// host's ruleset is adopted.
	Rules game.Ruleset
	// Side, when set, is the side this end expects to be given. Left zero,
	// whichever side the host did not take is accepted.
	Side game.Player
	// Resume continues the game in the snapshot instead of starting a new one.
	Resume *Snapshot
	Tuning
}

// config is the two option types reduced to what the handshake needs.
type config struct {
	role   Role
	name   string
	rules  game.Ruleset
	side   game.Player
	resume *Snapshot
	// key authenticates every frame. It is set only for a relayed game, from
	// the part of the pairing code the relay is never told, and only through
	// hostOverKeyed and joinOverKeyed: there is no option a caller could set it
	// with, because there is nowhere else a shared secret comes from.
	key []byte
	Tuning
}

func (o HostOptions) config() config {
	c := config{role: Host, name: o.Name, rules: o.Rules, side: o.Side, resume: o.Resume, Tuning: o.Tuning}
	return c.normalise()
}

func (o GuestOptions) config() config {
	c := config{role: Guest, name: o.Name, rules: o.Rules, side: o.Side, resume: o.Resume, Tuning: o.Tuning}
	return c.normalise()
}

func (c config) normalise() config {
	if c.resume != nil {
		c.rules = c.resume.Rules
		c.side = c.resume.Side
		if c.name == "" {
			c.name = c.resume.Name
		}
	}
	c.name = cleanName(c.name)
	if c.name == "" {
		c.name = c.role.String()
	}
	if c.Keepalive <= 0 {
		c.Keepalive = DefaultKeepalive
	}
	if c.DeadAfter <= 0 {
		c.DeadAfter = 4 * c.Keepalive
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = DefaultHandshakeTimeout
	}
	return c
}

// maxNameLen bounds a player name on the wire. Names come from profiles, which
// are already short; the bound is here so a peer cannot send a megabyte of one.
const maxNameLen = 64

// cleanName bounds and sanitises a name from the other end. A name is drawn on
// the player's terminal, so control bytes in it are removed rather than passed
// through: an opponent could otherwise put an escape sequence in their own name.
func cleanName(s string) string {
	return safeText(s, maxNameLen)
}

func (c config) check() error {
	if c.role == Host {
		if c.side != game.Vertical && c.side != game.Horizontal {
			return errors.New("the host must choose a side before inviting an opponent")
		}
		if err := c.rules.Validate(); err != nil {
			return err
		}
	} else {
		if c.side != game.NoPlayer && c.side != game.Vertical && c.side != game.Horizontal {
			return fmt.Errorf("%q is not a side", c.side)
		}
		if c.rules.Size != 0 {
			if err := c.rules.Validate(); err != nil {
				return err
			}
		}
	}
	if c.resume != nil {
		if c.resume.Role != c.role {
			return fmt.Errorf("this snapshot was saved as the %s and is being resumed as the %s", c.resume.Role, c.role)
		}
		if c.side != game.Vertical && c.side != game.Horizontal {
			return errors.New("the snapshot does not say which side this end plays")
		}
		if err := c.rules.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// HostOver runs the host end of the protocol over any transport. On failure the
// caller keeps ownership of rw; on success the session owns it and Close closes
// it. The context bounds the handshake only.
func HostOver(ctx context.Context, rw io.ReadWriter, opts HostOptions) (Session, error) {
	return handshake(ctx, rw, opts.config())
}

// JoinOver runs the guest end of the protocol over any transport.
func JoinOver(ctx context.Context, rw io.ReadWriter, opts GuestOptions) (Session, error) {
	return handshake(ctx, rw, opts.config())
}

// hostOverKeyed is HostOver with a key both ends derived from the pairing code
// of a relayed game, so that neither end has to trust the relay carrying it.
func hostOverKeyed(ctx context.Context, rw io.ReadWriter, opts HostOptions, key []byte) (Session, error) {
	cfg := opts.config()
	cfg.key = key
	return handshake(ctx, rw, cfg)
}

// joinOverKeyed is JoinOver with the same key.
func joinOverKeyed(ctx context.Context, rw io.ReadWriter, opts GuestOptions, key []byte) (Session, error) {
	cfg := opts.config()
	cfg.key = key
	return handshake(ctx, rw, cfg)
}

func handshake(ctx context.Context, rw io.ReadWriter, cfg config) (Session, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if c, ok := rw.(io.Closer); ok {
		stop := closeOnCancel(ctx, c)
		defer stop()
	}
	if c, ok := rw.(net.Conn); ok {
		_ = c.SetDeadline(time.Now().Add(cfg.HandshakeTimeout))
		defer c.SetDeadline(time.Time{})
	}

	s := &session{
		rw:        rw,
		f:         newFramer(rw, cfg.DeadAfter),
		role:      cfg.role,
		name:      cfg.name,
		keepalive: cfg.Keepalive,
		deadAfter: cfg.DeadAfter,
		done:      make(chan struct{}),
		exited:    make(chan struct{}),
	}
	if cfg.key != nil {
		if cfg.role == Host {
			s.f.authenticate(cfg.key, dirHost, dirGuest)
		} else {
			s.f.authenticate(cfg.key, dirGuest, dirHost)
		}
	}
	if cfg.role == Host {
		s.rules, s.side = cfg.rules, cfg.side
	}
	switch {
	case cfg.resume != nil:
		s.moves = append([]Entry(nil), cfg.resume.Moves...)
		s.opponent = cfg.resume.Opponent
		g, err := replay(cfg.rules, s.moves)
		if err != nil {
			return nil, fmt.Errorf("the saved game cannot be replayed: %w", err)
		}
		s.game = g
	case cfg.role == Host:
		// The guest cannot do this yet: it does not know the rules until the
		// invitation arrives, so it builds its game in openAsGuest.
		g, err := game.New(cfg.rules)
		if err != nil {
			return nil, err
		}
		s.game = g
	}

	var pending []Event
	var err error
	if cfg.role == Host {
		pending, err = s.openAsHost(cfg)
	} else {
		pending, err = s.openAsGuest(cfg)
	}
	if err != nil {
		return nil, err
	}
	s.start(pending)
	return s, nil
}

// closeOnCancel closes c if ctx is cancelled before stop is called. It is how a
// handshake blocked on a transport with no deadline of its own is abandoned.
func closeOnCancel(ctx context.Context, c io.Closer) (stop func()) {
	if ctx == nil || ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// openAsHost sends the invitation and reads the guest's answer.
func (s *session) openAsHost(cfg config) ([]Event, error) {
	hello := message{
		Type:        msgHello,
		Version:     Version,
		Name:        s.name,
		Rules:       s.rules.Canonical(),
		Fingerprint: s.rules.Fingerprint(),
		Side:        s.side.String(),
		Resume:      cfg.resume != nil,
		Entries:     len(s.moves),
		Digest:      transcriptDigest(s.moves),
	}
	if err := s.f.write(hello); err != nil {
		return nil, fmt.Errorf("sending the invitation: %w", err)
	}
	m, err := s.f.read()
	if err != nil {
		return nil, fmt.Errorf("waiting for the opponent: %w", err)
	}
	switch m.Type {
	case msgAccept:
	case msgReject:
		return nil, fmt.Errorf("%w: %s", ErrRefused, m.Text)
	case msgHello:
		return nil, fmt.Errorf("%w: the other end is hosting too, so nobody is joining; one of you must join instead", ErrProtocol)
	default:
		return nil, fmt.Errorf("%w: expected the opponent to accept, got %q", ErrProtocol, m.Type)
	}
	if m.Version != Version {
		return nil, versionError(m.Version)
	}
	if m.Fingerprint != s.rules.Fingerprint() {
		return nil, fmt.Errorf("%w: the opponent acknowledged a ruleset fingerprint of %s, not %s", ErrRuleset, m.Fingerprint, s.rules.Fingerprint())
	}
	guest, err := parsePlayer(m.Side)
	if err != nil {
		return nil, err
	}
	if guest != s.side.Opponent() {
		return nil, fmt.Errorf("%w: this end took %s, so the opponent must play %s, but it claims %s", ErrProtocol, s.side, s.side.Opponent(), guest)
	}
	s.opponent = cleanName(m.Name)
	return s.reconcile(cfg, m)
}

// openAsGuest reads the invitation, checks it is a game this end can play and
// answers. A refusal is sent to the host as well as returned, so both players
// see the same reason.
func (s *session) openAsGuest(cfg config) ([]Event, error) {
	m, err := s.f.read()
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			// One end has the whole pairing code and the other does not. Say so
			// rather than hanging up in silence: the host is waiting for an
			// opponent and cannot otherwise tell somebody who is not holding the
			// key from a connection that merely dropped.
			s.refuse(err.Error())
		}
		return nil, fmt.Errorf("waiting for the host: %w", err)
	}
	if m.Type != msgHello {
		return nil, fmt.Errorf("%w: expected an invitation, got %q", ErrProtocol, m.Type)
	}
	if m.Version != Version {
		err := versionError(m.Version)
		s.refuse(err.Error())
		return nil, err
	}
	rules, err := parseRuleset(m.Rules)
	if err != nil {
		s.refuse(err.Error())
		return nil, err
	}
	if got := rules.Fingerprint(); got != m.Fingerprint {
		err := fmt.Errorf("%w: the host's ruleset fingerprints to %s here but the host called it %s; the two builds do not agree on what the rules mean", ErrRuleset, got, m.Fingerprint)
		s.refuse(err.Error())
		return nil, err
	}
	if cfg.rules.Size != 0 && cfg.rules != rules {
		err := fmt.Errorf("%w: %s", ErrRuleset, describeRulesetDiff(cfg.rules, rules))
		s.refuse(err.Error())
		return nil, err
	}
	hostSide, err := parsePlayer(m.Side)
	if err != nil {
		s.refuse(err.Error())
		return nil, err
	}
	side := hostSide.Opponent()
	if cfg.side != game.NoPlayer && cfg.side != side {
		err := fmt.Errorf("%w: the host took %s, so this end would play %s, not %s", ErrProtocol, hostSide, side, cfg.side)
		s.refuse(err.Error())
		return nil, err
	}
	s.rules, s.side = rules, side
	s.opponent = cleanName(m.Name)
	if s.game == nil {
		g, err := game.New(rules)
		if err != nil {
			s.refuse(err.Error())
			return nil, err
		}
		s.game = g
	}

	accept := message{
		Type:        msgAccept,
		Version:     Version,
		Name:        s.name,
		Fingerprint: rules.Fingerprint(),
		Side:        side.String(),
		Resume:      cfg.resume != nil,
		Entries:     len(s.moves),
		Digest:      transcriptDigest(s.moves),
	}
	if err := s.f.write(accept); err != nil {
		return nil, fmt.Errorf("accepting the game: %w", err)
	}
	return s.reconcile(cfg, m)
}

func versionError(peer int) error {
	return fmt.Errorf("%w: the opponent speaks protocol version %d and this build speaks version %d; both ends need the same twixtui release", ErrVersion, peer, Version)
}

// refuse tells the peer why the game is off. The write error is dropped: this
// end is giving up anyway, and the peer will see the connection go.
//
// The reason is bounded here, at the point it is produced, and not only where
// one is received. Some of these reasons quote what the peer sent, so an
// unbounded one would reflect a hostile megabyte straight back and would not fit
// in a frame at all.
func (s *session) refuse(text string) {
	_ = s.f.writeTimeout(message{Type: msgReject, Text: safeText(text, maxWireTextLen)}, time.Second)
}

// reconcile settles any difference between the two transcripts. Both ends know
// both move counts once the invitation and the acceptance have been exchanged,
// so each acts without a further round trip: the end with the longer transcript
// pushes the missing moves, the end with the shorter one waits for them.
//
// The prefix check is what makes this safe. Before sending anything, the longer
// end verifies that its own transcript, cut to the peer's length, digests to
// what the peer reported. If it does not, the two ends played different games
// and there is nothing to reconcile.
func (s *session) reconcile(cfg config, peer message) ([]Event, error) {
	resuming := cfg.resume != nil
	if peer.Resume != resuming {
		if resuming {
			return nil, fmt.Errorf("%w: this end is resuming a game with %d entries in its record and the opponent is starting a new one", ErrDiverged, len(s.moves))
		}
		return nil, fmt.Errorf("%w: the opponent is resuming a game with %d entries in its record and this end is starting a new one; load the saved game to continue it", ErrDiverged, peer.Entries)
	}
	mine, theirs := len(s.moves), peer.Entries
	if theirs < 0 {
		return nil, fmt.Errorf("%w: the opponent claims a record of %d entries", ErrProtocol, theirs)
	}
	switch {
	case mine == theirs:
		if peer.Digest != transcriptDigest(s.moves) {
			return nil, fmt.Errorf("%w: both ends are at move %d but the moves played were not the same", ErrDiverged, mine)
		}
		return nil, nil

	case mine > theirs:
		if peer.Digest != transcriptDigest(s.moves[:theirs]) {
			return nil, fmt.Errorf("%w: the opponent is at move %d and the moves up to there were not the same", ErrDiverged, theirs)
		}
		out := message{
			Type:    msgResync,
			Entries: mine,
			PosHash: PositionHash(s.game),
			Digest:  transcriptDigest(s.moves),
			Replay:  wireEntries(s.moves[theirs:]),
		}
		if err := s.f.write(out); err != nil {
			return nil, fmt.Errorf("sending the missing moves: %w", err)
		}
		return nil, nil

	default:
		m, err := s.f.read()
		if err != nil {
			return nil, fmt.Errorf("waiting for the moves this end missed: %w", err)
		}
		if m.Type != msgResync {
			return nil, fmt.Errorf("%w: expected the %d missing moves, got %q", ErrProtocol, theirs-mine, m.Type)
		}
		return s.absorb(m)
	}
}

// absorb applies the record entries the peer says this end missed. The shared
// prefix was already checked before the replay; the resulting position hash and
// final transcript digest are both checked afterwards. Without per-side
// signatures an opponent may replay only its own entries: accepting an entry as
// this end would let it resign on the local player's behalf.
func (s *session) absorb(m message) ([]Event, error) {
	peer := s.side.Opponent()
	events := make([]Event, 0, len(m.Replay))
	for _, e := range m.Replay {
		side, err := parsePlayer(e.Side)
		if err != nil {
			return nil, err
		}
		if side != peer {
			return nil, fmt.Errorf("%w: the opponent tried to replay an entry as this end's %s side", ErrProtocol, side)
		}
		// Legality and the final digest remain the authority for the peer's
		// own replayed entry.
		if err := applyEntry(s.game, side, e.Move); err != nil {
			return nil, fmt.Errorf("%w: replaying missed move %d (%q): %w", ErrDiverged, len(s.moves)+1, e.Move, err)
		}
		s.moves = append(s.moves, Entry{Side: side, Move: e.Move})
		ev := eventFor(e.Move)
		ev.Text = fmt.Sprintf("move %d, replayed after reconnecting", len(s.moves))
		events = append(events, ev)
	}
	if m.Entries != len(s.moves) {
		return nil, fmt.Errorf("%w: the opponent says the record holds %d entries and replaying its moves left this end with %d", ErrDiverged, m.Entries, len(s.moves))
	}
	if want := PositionHash(s.game); m.PosHash != want {
		return nil, fmt.Errorf("%w: replaying the missed entries gave a different position (%s here, %s there)", ErrDiverged, shortHash(want), shortHash(m.PosHash))
	}
	if want := transcriptDigest(s.moves); m.Digest == "" || m.Digest != want {
		return nil, fmt.Errorf("%w: replaying the missed entries produced a different transcript (%s here, %s there)", ErrDiverged, shortHash(want), shortHash(m.Digest))
	}
	return events, nil
}

// eventFor maps a transcript line to the event the UI should see.
func eventFor(notation string) Event {
	if _, token, ok := parseAction(notation); ok {
		switch token {
		case resignToken:
			return Event{Kind: EventResign}
		case drawOfferToken:
			return Event{Kind: EventDrawOffer}
		case drawAcceptToken:
			return Event{Kind: EventDrawAccept}
		}
	}
	return Event{Kind: EventMove, Move: notation}
}

// The engine's words for the record entries that are not turns. They are read
// out of the engine rather than repeated here, so this package cannot drift
// from the notation it has to parse and produce.
var (
	resignToken     = actionToken(game.ResignMove)
	drawOfferToken  = actionToken(game.DrawOfferMove)
	drawAcceptToken = actionToken(game.DrawAcceptMove)
)

// actionToken returns the bare word for a record entry kind, without the side
// tag the engine writes in front of it.
func actionToken(kind game.MoveKind) string {
	notation := strings.ToLower(actionNotation(kind, game.Vertical))
	if _, token, ok := strings.Cut(notation, ":"); ok {
		return token
	}
	return notation
}

// actionNotation is how the engine records a resignation or a draw message for
// a side.
func actionNotation(kind game.MoveKind, side game.Player) string {
	return game.Move{Kind: kind, Player: side}.Notation(nil)
}

// parseAction recognises a record entry that is not a turn, in either the bare
// or the side-tagged form. The side is NoPlayer for the bare form, which the
// engine also accepts because a player may type "resign" at a prompt.
func parseAction(notation string) (game.Player, string, bool) {
	s := strings.ToLower(strings.TrimSpace(notation))
	side := game.NoPlayer
	if tag, rest, tagged := strings.Cut(s, ":"); tagged {
		pl, err := game.ParsePlayer(tag)
		if err != nil {
			return game.NoPlayer, "", false
		}
		side, s = pl, rest
	}
	switch s {
	case resignToken, drawOfferToken, drawAcceptToken:
		return side, s, true
	}
	return game.NoPlayer, "", false
}

// applyEntry plays one transcript line for a named side. A resignation or a
// draw message is dispatched with the side given rather than through the
// engine's bare form, because either player may make one while the opponent is
// thinking and the bare form attributes it to whoever is to move.
func applyEntry(g *game.Game, side game.Player, notation string) error {
	if tagged, token, ok := parseAction(notation); ok {
		if tagged != game.NoPlayer && tagged != side {
			return fmt.Errorf("%q says it is %s's but it was recorded as %s's", notation, tagged, side)
		}
		switch token {
		case resignToken:
			return g.Resign(side)
		case drawOfferToken:
			return g.OfferDraw(side)
		default:
			return g.AcceptDraw(side)
		}
	}
	if g.Turn() != side {
		return fmt.Errorf("%s played %q out of turn", side, notation)
	}
	return g.PlayNotation(notation)
}

// replay rebuilds a game from a transcript.
func replay(rs game.Ruleset, moves []Entry) (*game.Game, error) {
	g, err := game.New(rs)
	if err != nil {
		return nil, err
	}
	for i, e := range moves {
		if err := applyEntry(g, e.Side, e.Move); err != nil {
			return nil, fmt.Errorf("move %d (%s %q): %w", i+1, e.Side, e.Move, err)
		}
	}
	return g, nil
}

func wireEntries(moves []Entry) []wireEntry {
	out := make([]wireEntry, len(moves))
	for i, e := range moves {
		out[i] = wireEntry{Side: e.Side.String(), Move: e.Move}
	}
	return out
}

// transcriptTag prefixes the transcript digest for the same reason positionTag
// prefixes a position.
const transcriptTag = "twixt-transcript/1"

// transcriptDigest hashes a transcript. Sides are included, and every line is
// terminated, so no two different transcripts digest alike.
func transcriptDigest(moves []Entry) string {
	h := sha256.New()
	h.Write([]byte(transcriptTag))
	var line []byte
	for _, e := range moves {
		line = line[:0]
		line = append(line, byte(e.Side), ' ')
		line = append(line, e.Move...)
		line = append(line, '\n')
		h.Write(line)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// shortHash cuts a digest down to the part worth showing the player. The cut is
// rune-safe because the digests it is given may have arrived from the other end,
// where anything at all could have been in the field.
func shortHash(s string) string {
	return truncateRunes(s, 12)
}

// why records what ended a session, so the read loop can tell the player the
// difference between a deliberate close, a silent opponent and a broken peer.
type why int32

const (
	whyPeer why = iota // the connection or the peer ended it
	whyUser            // Close was called on this end
	whyDead            // the keepalive watchdog gave up on the opponent
)

// session is one end of a game. It keeps its own game, applied from the same
// notation both ends exchange, which is what lets it verify every position hash
// the opponent sends and produce the right hash for its own moves.
type session struct {
	rw   io.ReadWriter
	f    *framer
	role Role

	side     game.Player
	rules    game.Ruleset
	name     string
	opponent string

	events chan Event
	done   chan struct{}
	exited chan struct{}

	mu    sync.Mutex
	game  *game.Game
	moves []Entry

	keepalive time.Duration
	deadAfter time.Duration
	seen      atomic.Int64
	reason    atomic.Int32
	closeOnce sync.Once
	closeErr  error
}

func (s *session) start(pending []Event) {
	s.events = make(chan Event, len(pending)+eventBuffer)
	s.events <- Event{Kind: EventConnected, Text: s.greeting()}
	for _, e := range pending {
		s.events <- e
	}
	s.touch()
	go s.readLoop()
	go s.keepaliveLoop()
}

// greeting is how the guest is told which side it was given, and how either end
// gets the agreed rules in one line.
func (s *session) greeting() string {
	return fmt.Sprintf("playing %s: you are %s, %s", s.opponentOr("your opponent"), s.side, s.rules.Describe())
}

func (s *session) touch() { s.seen.Store(time.Now().UnixNano()) }

func (s *session) Side() game.Player    { return s.side }
func (s *session) Rules() game.Ruleset  { return s.rules }
func (s *session) OpponentName() string { return s.opponent }
func (s *session) Events() <-chan Event { return s.events }

// Snapshot returns the state needed to resume this game elsewhere.
func (s *session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Role:     s.role,
		Rules:    s.rules,
		Side:     s.side,
		Name:     s.name,
		Opponent: s.opponent,
		Moves:    append([]Entry(nil), s.moves...),
	}
}

// Position returns a copy of the session's own view of the game.
func (s *session) Position() *game.Game {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.game.Clone()
}

// Save returns the state a dropped session needs to be resumed. Pass it back
// through HostOptions.Resume or GuestOptions.Resume on the new connection.
func Save(s Session) (Snapshot, error) {
	r, ok := s.(Resumable)
	if !ok {
		return Snapshot{}, errors.New("this session does not keep a transcript and cannot be resumed")
	}
	return r.Snapshot(), nil
}

// Close ends the session. It tells the opponent, closes the connection and
// waits for the read loop to finish, so no event arrives after it returns.
func (s *session) Close() error {
	s.stop(whyUser)
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
	}
	return s.closeErr
}

// stop tears the session down exactly once.
func (s *session) stop(r why) {
	s.closeOnce.Do(func() {
		s.reason.Store(int32(r))
		close(s.done)
		if r == whyUser {
			// Best effort only. If another write is stuck, closing rw below
			// must happen first so that it can return.
			s.f.tryWriteTimeout(message{Type: msgBye, Text: "the opponent left the game"}, 250*time.Millisecond)
		}
		if c, ok := s.rw.(io.Closer); ok {
			s.closeErr = c.Close()
		}
	})
}

func (s *session) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// emit hands an event to the caller. The non-blocking attempt comes first
// because a session that is shutting down has already closed done, and a plain
// select between the two would drop the very event that explains why: which
// event that is, is the whole point of the last one.
func (s *session) emit(e Event) {
	select {
	case s.events <- e:
		return
	default:
	}
	select {
	case s.events <- e:
	case <-s.done:
	}
}

// readLoop is the only goroutine that writes to the event channel after start,
// which is what lets it close the channel when the session finishes.
func (s *session) readLoop() {
	defer close(s.exited)
	defer close(s.events)
	for {
		m, err := s.f.read()
		if err != nil {
			s.reportFailure(err)
			return
		}
		s.touch()
		switch m.Type {
		case msgPing:
			// A ping is one way. Replying from here would mean writing while
			// the peer may itself be writing, which on a transport with no
			// buffer of its own is a deadlock; both ends ping on their own
			// timer instead, and any frame at all counts as life.
		case msgBye:
			text := strings.TrimSpace(m.Text)
			if text == "" {
				text = "the opponent left the game"
			}
			s.emit(Event{Kind: EventDisconnected, Text: text})
			s.stop(whyPeer)
			return
		case msgMove, msgResign, msgDrawOffer, msgDrawAccept:
			ev, err := s.applyPeer(m)
			if err != nil {
				s.emit(Event{Kind: EventError, Err: err, Text: err.Error()})
				s.stop(whyPeer)
				return
			}
			s.emit(ev)
		default:
			err := fmt.Errorf("%w: unexpected %q message", ErrProtocol, m.Type)
			s.emit(Event{Kind: EventError, Err: err, Text: err.Error()})
			s.stop(whyPeer)
			return
		}
	}
}

func (s *session) reportFailure(err error) {
	switch why(s.reason.Load()) {
	case whyUser:
		// This end closed the session; the caller does not need telling.
	case whyDead:
		s.emit(Event{Kind: EventDisconnected, Text: fmt.Sprintf("%s stopped responding", s.opponentOr("the opponent"))})
	default:
		if isDisconnect(err) {
			s.emit(Event{Kind: EventDisconnected, Text: "the connection to the opponent dropped"})
		} else {
			s.emit(Event{Kind: EventError, Err: err, Text: err.Error()})
		}
	}
	s.stop(whyPeer)
}

func (s *session) opponentOr(fallback string) string {
	if s.opponent == "" {
		return fallback
	}
	return s.opponent
}

// isDisconnect reports whether an error means the connection ended rather than
// the peer misbehaving.
func isDisconnect(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe)
}

// keepaliveLoop pings an idle connection and gives up on an opponent that has
// gone quiet for good.
func (s *session) keepaliveLoop() {
	t := time.NewTicker(s.keepalive)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, s.seen.Load())) > s.deadAfter {
				s.stop(whyDead)
				return
			}
			if err := s.f.write(message{Type: msgPing}); err != nil {
				// A ping that cannot even be written means the connection is
				// finished. Say so here rather than leaving the read loop
				// blocked on a socket nothing will ever arrive on.
				s.stop(whyDead)
				return
			}
		}
	}
}

// applyPeer trial-applies the opponent's message and checks that both ends
// reached the same position before it replaces the session's last agreed game.
// A hostile or divergent entry therefore cannot contaminate a resumable
// snapshot.
func (s *session) applyPeer(m message) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer := s.side.Opponent()
	trial := s.game.Clone()
	var ev Event
	var entry Entry
	switch m.Type {
	case msgMove:
		if _, _, ok := parseAction(m.Move); ok {
			return ev, fmt.Errorf("%w: %q must be sent as its own message, not as a move", ErrProtocol, m.Move)
		}
		if trial.Turn() != peer {
			return ev, fmt.Errorf("%w: the opponent played %q out of turn", ErrProtocol, m.Move)
		}
		if err := trial.PlayNotation(m.Move); err != nil {
			return ev, fmt.Errorf("%w: the opponent sent an illegal move %q: %w", ErrProtocol, m.Move, err)
		}
		notation, err := trial.MoveNotation(trial.Entries() - 1)
		if err != nil {
			return ev, fmt.Errorf("%w: the opponent's move %q could not be recorded: %w", ErrProtocol, m.Move, err)
		}
		ev = Event{Kind: EventMove, Move: notation, Text: fmt.Sprintf("%s played %s", s.opponentOr(peer.String()), notation)}
		entry = Entry{Side: peer, Move: notation}
	case msgResign:
		if err := trial.Resign(peer); err != nil {
			return ev, fmt.Errorf("%w: the opponent could not resign: %w", ErrProtocol, err)
		}
		ev = Event{Kind: EventResign, Text: fmt.Sprintf("%s resigned", s.opponentOr(peer.String()))}
		entry = Entry{Side: peer, Move: actionNotation(game.ResignMove, peer)}
	case msgDrawOffer:
		if err := trial.OfferDraw(peer); err != nil {
			return ev, fmt.Errorf("%w: the opponent could not offer a draw: %w", ErrProtocol, err)
		}
		ev = Event{Kind: EventDrawOffer, Text: fmt.Sprintf("%s offered a draw", s.opponentOr(peer.String()))}
		entry = Entry{Side: peer, Move: actionNotation(game.DrawOfferMove, peer)}
	case msgDrawAccept:
		if err := trial.AcceptDraw(peer); err != nil {
			return ev, fmt.Errorf("%w: the opponent could not accept a draw: %w", ErrProtocol, err)
		}
		ev = Event{Kind: EventDrawAccept, Text: fmt.Sprintf("%s accepted the draw", s.opponentOr(peer.String()))}
		entry = Entry{Side: peer, Move: actionNotation(game.DrawAcceptMove, peer)}
	default:
		return ev, fmt.Errorf("%w: %q is not a move message", ErrProtocol, m.Type)
	}
	if err := verifyPosition(m, trial); err != nil {
		return ev, err
	}
	s.game = trial
	s.moves = append(s.moves, entry)
	return ev, nil
}

// verifyPosition compares the opponent's view after its entry with the
// trial-applied position here. The entry count catches a lost or duplicated
// message; the hash catches the same notation being applied to different
// boards.
func verifyPosition(m message, g *game.Game) error {
	if m.PosHash == "" {
		return fmt.Errorf("%w: the opponent's %s carried no position hash", ErrProtocol, m.Type)
	}
	if m.Entries != g.Entries() {
		return fmt.Errorf("%w: the opponent's record holds %d entries and this end's holds %d", ErrDiverged, m.Entries, g.Entries())
	}
	if want := PositionHash(g); m.PosHash != want {
		return fmt.Errorf("%w: after entry %d the opponent's board hashes to %s and this one to %s", ErrDiverged, g.Entries(), shortHash(m.PosHash), shortHash(want))
	}
	return nil
}

// SendMove plays a move in the engine's notation on this end and sends it. The
// notation for resign and the two draw messages is routed to the matching
// method, so a caller that passes a transcript line straight through does the
// right thing.
func (s *session) SendMove(notation string) error {
	if tagged, token, ok := parseAction(notation); ok {
		if tagged != game.NoPlayer && tagged != s.side {
			return fmt.Errorf("%q is the opponent's to send, not this end's", notation)
		}
		switch token {
		case resignToken:
			return s.SendResign()
		case drawOfferToken:
			return s.SendDrawOffer()
		default:
			return s.SendDrawAccept()
		}
	}
	if s.closed() {
		return ErrClosed
	}
	s.mu.Lock()
	if s.game.Turn() != s.side {
		s.mu.Unlock()
		return game.ErrNotYourTurn
	}
	if err := s.game.PlayNotation(notation); err != nil {
		s.mu.Unlock()
		return err
	}
	canonical, err := s.game.MoveNotation(s.game.Entries() - 1)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.moves = append(s.moves, Entry{Side: s.side, Move: canonical})
	m := message{Type: msgMove, Move: canonical, Entries: s.game.Entries(), PosHash: PositionHash(s.game)}
	s.mu.Unlock()
	return s.f.write(m)
}

// SendResign concedes the game. Like the two draw messages it is recorded
// locally before it goes out: a player who resigned has resigned even if the
// connection dies on the way, and the resync on reconnecting carries it across.
func (s *session) SendResign() error { return s.sendAction(game.ResignMove) }

// SendDrawOffer offers a draw.
func (s *session) SendDrawOffer() error { return s.sendAction(game.DrawOfferMove) }

// SendDrawAccept accepts the opponent's standing draw offer.
func (s *session) SendDrawAccept() error { return s.sendAction(game.DrawAcceptMove) }

// sendAction plays and sends one of the record entries that is not a turn.
func (s *session) sendAction(kind game.MoveKind) error {
	if s.closed() {
		return ErrClosed
	}
	var mt msgType
	s.mu.Lock()
	var err error
	switch kind {
	case game.ResignMove:
		mt, err = msgResign, s.game.Resign(s.side)
	case game.DrawOfferMove:
		mt, err = msgDrawOffer, s.game.OfferDraw(s.side)
	case game.DrawAcceptMove:
		mt, err = msgDrawAccept, s.game.AcceptDraw(s.side)
	default:
		s.mu.Unlock()
		return fmt.Errorf("%v is not something to send on its own", kind)
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.moves = append(s.moves, Entry{Side: s.side, Move: actionNotation(kind, s.side)})
	m := message{Type: mt, Entries: s.game.Entries(), PosHash: PositionHash(s.game)}
	s.mu.Unlock()
	return s.f.write(m)
}
