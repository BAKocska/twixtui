package netplay

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/BAKocska/twixtui/internal/game"
)

// Correspondence codes are the fallback whose failure modes share nothing with
// the live transports: no socket, no relay, no reachability, no clock. A player
// pastes a short string into whatever channel the two of them already use.
//
// A code carries the move, the game it belongs to, and the position hash both
// before and after the move. The hash before the move is what proves the code
// was made for the position it is being applied to: the move count alone is not
// enough, because two different positions can be reached at the same move
// count. The hash after the move is the same divergence check the live protocol
// makes. A CRC-32 over the whole payload means a paste that lost or gained
// characters is rejected with an explanation instead of corrupting the game.
//
// Move code payload, before base32:
//
//	0        format version
//	1        side that made the move
//	2..3     moves played before this one, uint16
//	4..7     digest of the game identifier
//	8..11    position hash before the move
//	12..15   position hash after the move
//	16       length of the move in notation
//	17..     the move in notation
//	last 4   CRC-32 (IEEE) of everything above
//
// Invite payload, before base32:
//
//	0        format version
//	1        board size
//	2        rule flags
//	3        side the host took
//	4..7     ruleset fingerprint
//	8        length of the game identifier
//	9..      the game identifier
//	         length of the host's name, then the name
//	last 4   CRC-32 (IEEE) of everything above

// codeVersion is the format version of both kinds of code.
const codeVersion = 1

// Prefixes. They are checked in full, including the separator, so that an
// invite pasted where a move was expected is named rather than misread.
const (
	movePrefix   = "TWX"
	invitePrefix = "TWXI"
	codeGroup    = 5 // characters between dashes, for readability
)

// codeHashBytes is how much of a position hash a code carries. Four bytes is
// short enough to keep a code readable and long enough that a code made for a
// different position is refused rather than accidentally accepted.
const codeHashBytes = 4

const (
	moveCodeHeaderLen = 17
	inviteHeaderLen   = 9
	codeChecksumLen   = 4
	maxNotationLen    = 255
	maxGameIDLen      = 32
	// The largest valid move code is about 540 characters. Rejecting the raw
	// paste before normalising it prevents a hostile megabyte string from
	// causing a second megabyte allocation in strings.Map and decode32.
	maxCodeTextLen = 768
)

const (
	maxTranscriptBytes   = MaxFrameSize
	maxTranscriptEntries = 4096
)

// MoveCode is what a correspondence code carries. Inspect returns one without
// needing a game, so a pasted code can be routed to the game it belongs to
// before anything is applied.
type MoveCode struct {
	// Game is the digest of the game identifier. Compare it with GameDigest of
	// a saved game's identifier to find the game the code belongs to.
	Game string
	// Entries is how many entries the record held before this one, which is
	// what the code follows on from.
	Entries int
	// Side is the player who made the move.
	Side game.Player
	// Move is the move in the engine's notation.
	Move string
	// Before is the position hash the move must be applied to, shortened.
	Before string
	// After is the position hash the move produces, shortened.
	After string
}

// gameTag keeps the identifier digest distinct from every other digest here.
const gameTag = "twixt-game/1"

// GameDigest returns the digest a code carries for a game identifier. The
// identifier itself never travels: the digest is enough to tell games apart and
// costs four bytes.
func GameDigest(id string) string {
	sum := sha256.Sum256([]byte(gameTag + strings.TrimSpace(id)))
	return hex.EncodeToString(sum[:codeHashBytes])
}

// NewGameID returns a fresh identifier for a correspondence game.
func NewGameID() string { return randomCode(8) }

// EncodeMove returns the code for playing notation in the position g, which is
// not modified. The move is attributed to the side to move; after a
// resignation or draw message that came from the other side, use EncodeLastMove
// instead, which reads the side from the game's own record.
func EncodeMove(g *game.Game, id, notation string) (string, error) {
	if g == nil {
		return "", errors.New("there is no game to make a move code for")
	}
	if g.Result().Over() {
		return "", game.ErrGameOver
	}
	return encodeMoveFor(g, id, g.Turn(), notation)
}

// EncodeLastMove returns the code for the move already played on g. This is the
// call a UI wants: the player makes their move on their own board, and this
// turns it into the string to paste to the opponent.
func EncodeLastMove(g *game.Game, id string) (string, error) {
	if g == nil {
		return "", errors.New("there is no game to make a move code for")
	}
	i := g.Entries() - 1
	if i < 0 {
		return "", errors.New("the game has no moves yet, so there is nothing to send")
	}
	notation, err := g.MoveNotation(i)
	if err != nil {
		return "", err
	}
	side := g.History()[i].Player
	before, err := positionBefore(g, i)
	if err != nil {
		return "", fmt.Errorf("working out the position before %s: %w", notation, err)
	}
	return buildMoveCode(id, side, i, notation, positionSum(before), positionSum(g))
}

// positionBefore rebuilds the position as it was before move i by replaying the
// moves up to it. The engine's undo cannot be used here: it hands the turn back
// to the player named in the move it reverses, which is right for a placement
// but wrong for a resignation or a draw message from the side that was not to
// move, and this package supports those.
func positionBefore(g *game.Game, i int) (*game.Game, error) {
	before, err := game.New(g.Rules())
	if err != nil {
		return nil, err
	}
	history := g.History()
	for j := range i {
		notation, err := g.MoveNotation(j)
		if err != nil {
			return nil, err
		}
		if err := applyEntry(before, history[j].Player, notation); err != nil {
			return nil, fmt.Errorf("move %d (%q): %w", j+1, notation, err)
		}
	}
	return before, nil
}

// encodeMoveFor plays notation for side on a copy of g to find the resulting
// position, and encodes both hashes.
func encodeMoveFor(g *game.Game, id string, side game.Player, notation string) (string, error) {
	before := positionSum(g)
	trial := g.Clone()
	if err := applyEntry(trial, side, notation); err != nil {
		return "", err
	}
	return buildMoveCode(id, side, g.Entries(), notation, before, positionSum(trial))
}

func buildMoveCode(id string, side game.Player, entries int, notation string, before, after [sha256.Size]byte) (string, error) {
	if err := validateGameID(id); err != nil {
		return "", err
	}
	notation = strings.TrimSpace(notation)
	if notation == "" {
		return "", errors.New("there is no move to encode")
	}
	if len(notation) > maxNotationLen {
		return "", fmt.Errorf("move %q is too long to encode", notation)
	}
	if entries < 0 || entries > 0xffff {
		return "", fmt.Errorf("a record of %d entries is out of range for a move code", entries)
	}
	digest, err := hex.DecodeString(GameDigest(id))
	if err != nil {
		return "", err
	}

	payload := make([]byte, 0, moveCodeHeaderLen+len(notation)+codeChecksumLen)
	payload = append(payload, codeVersion, byte(side))
	payload = binary.BigEndian.AppendUint16(payload, uint16(entries))
	payload = append(payload, digest...)
	payload = append(payload, before[:codeHashBytes]...)
	payload = append(payload, after[:codeHashBytes]...)
	payload = append(payload, byte(len(notation)))
	payload = append(payload, notation...)
	payload = binary.BigEndian.AppendUint32(payload, crc32.ChecksumIEEE(payload))
	return formatCode(movePrefix, payload), nil
}

// Inspect reads a code without needing a game: it checks the checksum and the
// format and returns what the code says. Use it to find which saved game a
// pasted code belongs to.
func Inspect(code string) (MoveCode, error) {
	var mc MoveCode
	payload, err := parseCode(code, movePrefix)
	if err != nil {
		return mc, err
	}
	if len(payload) < moveCodeHeaderLen+1+codeChecksumLen {
		return mc, fmt.Errorf("%w: the code is too short to be a move; part of it is probably missing", ErrBadCode)
	}
	if payload[0] != codeVersion {
		return mc, fmt.Errorf("%w: the code uses format version %d and this build understands version %d", ErrBadCode, payload[0], codeVersion)
	}
	side := game.Player(payload[1])
	if side != game.Vertical && side != game.Horizontal {
		return mc, fmt.Errorf("%w: the code does not name a side", ErrBadCode)
	}
	body := payload[:len(payload)-codeChecksumLen]
	n := int(body[16])
	if len(body) != moveCodeHeaderLen+n {
		return mc, fmt.Errorf("%w: the code says its move is %d characters but carries %d", ErrBadCode, n, len(body)-moveCodeHeaderLen)
	}
	mc = MoveCode{
		Game:    hex.EncodeToString(body[4:8]),
		Entries: int(binary.BigEndian.Uint16(body[2:4])),
		Side:    side,
		// The length byte allows 255 bytes of anything. This field arrives from
		// a pasted string and is printed by callers that have no way of knowing
		// it came from outside, so it is filtered here, where it enters.
		Move:   safeText(string(body[moveCodeHeaderLen:]), maxNotationLen),
		Before: hex.EncodeToString(body[8:12]),
		After:  hex.EncodeToString(body[12:16]),
	}
	return mc, nil
}

// fits reports why the code cannot be applied to g, or nil if it can.
func (c MoveCode) fits(g *game.Game, id string) error {
	if err := validateGameID(id); err != nil {
		return err
	}
	if want := GameDigest(id); c.Game != want {
		return fmt.Errorf("%w: this code belongs to a different game (%s, not %s)", ErrBadCode, c.Game, want)
	}
	if c.Entries != g.Entries() {
		if c.Entries < g.Entries() {
			return fmt.Errorf("%w: this code follows entry %d of the record and this game already has %d, so it has been applied already", ErrBadCode, c.Entries, g.Entries())
		}
		return fmt.Errorf("%w: this code follows entry %d of the record and this game only has %d, so an earlier code is missing", ErrBadCode, c.Entries, g.Entries())
	}
	if got := shortSum(positionSum(g)); got != c.Before {
		return fmt.Errorf("%w: this code was made for a different position (it expects %s, this board is %s)", ErrBadCode, c.Before, got)
	}
	return nil
}

// DecodeMove returns the move a code carries, having checked that the code was
// made for the position g is in and that the move produces the position the
// opponent got. It does not modify g.
func DecodeMove(g *game.Game, id, code string) (string, error) {
	mc, err := checkMoveCode(g, id, code)
	if err != nil {
		return "", err
	}
	return mc.Move, nil
}

// ApplyMove plays the move a code carries on g. The move is tried on a copy
// first, so a code whose result does not match the opponent's leaves the game
// untouched rather than half advanced.
func ApplyMove(g *game.Game, id, code string) (string, error) {
	mc, err := checkMoveCode(g, id, code)
	if err != nil {
		return "", err
	}
	if err := applyEntry(g, mc.Side, mc.Move); err != nil {
		return "", err
	}
	return mc.Move, nil
}

// checkMoveCode validates a code against g in full, without touching g.
func checkMoveCode(g *game.Game, id, code string) (MoveCode, error) {
	var mc MoveCode
	if g == nil {
		return mc, errors.New("there is no game to apply a move code to")
	}
	mc, err := Inspect(code)
	if err != nil {
		return mc, err
	}
	if err := mc.fits(g, id); err != nil {
		return mc, err
	}
	trial := g.Clone()
	if err := applyEntry(trial, mc.Side, mc.Move); err != nil {
		return mc, fmt.Errorf("%w: %s cannot be played here: %w", ErrBadCode, safeText(mc.Move, maxMoveLen), err)
	}
	if got := shortSum(positionSum(trial)); got != mc.After {
		return mc, fmt.Errorf("%w: playing %s here gives %s and your opponent got %s", ErrDiverged, safeText(mc.Move, maxMoveLen), got, mc.After)
	}
	return mc, nil
}

// maxMoveLen bounds a move as written in a code. Real notation is a handful of
// characters; the bound is here because the field arrives from a pasted string
// and ends up in a message on the player's screen.
const maxMoveLen = 64

// EncodeTranscript renders a whole game as a block of move codes, one per line.
// It is how a correspondence game is handed over in full: to start one from a
// position, or to rescue a live game whose connection cannot be re-established.
func EncodeTranscript(id string, rs game.Ruleset, moves []Entry) (string, error) {
	g, err := game.New(rs)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, e := range moves {
		code, err := encodeMoveFor(g, id, e.Side, e.Move)
		if err != nil {
			return "", fmt.Errorf("move %d (%s %q): %w", i+1, e.Side, e.Move, err)
		}
		if err := applyEntry(g, e.Side, e.Move); err != nil {
			return "", fmt.Errorf("move %d (%s %q): %w", i+1, e.Side, e.Move, err)
		}
		b.WriteString(code)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// ApplyTranscript plays a block of move codes onto g and returns the transcript
// it added.
//
// The block is applied to a copy first and adopted only once every line of it
// has been accepted, so a block that goes wrong at its third line leaves g
// exactly as it was rather than two moves further on. That matters because the
// block came out of a paste: ApplyMove and checkMoveCode take the same care with
// a single code, and session.applyPeer with a single frame, so that a hostile or
// merely mistaken entry cannot advance the player's live game before it is known
// to be good.
func ApplyTranscript(g *game.Game, id, block string) ([]Entry, error) {
	if g == nil {
		return nil, errors.New("there is no game to apply move codes to")
	}
	if len(block) > maxTranscriptBytes {
		return nil, fmt.Errorf("%w: the pasted transcript is %d bytes, over the %d byte limit", ErrBadCode, len(block), maxTranscriptBytes)
	}
	trial := g.Clone()
	var added []Entry
	scanner := bufio.NewScanner(strings.NewReader(block))
	scanner.Buffer(make([]byte, 256), maxCodeTextLen)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(added) >= maxTranscriptEntries {
			return nil, fmt.Errorf("%w: the transcript has more than %d entries", ErrBadCode, maxTranscriptEntries)
		}
		mc, err := Inspect(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		notation, err := ApplyMove(trial, id, line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		added = append(added, Entry{Side: mc.Side, Move: notation})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: malformed transcript: %v", ErrBadCode, err)
	}
	// The copy is discarded here, so taking its fields wholesale is safe: no
	// slice ends up shared with anything that outlives this call.
	*g = *trial
	return added, nil
}

// Invite is what a host must tell a guest before a correspondence game can
// start: which game, by which rules, with the host on which side.
type Invite struct {
	// ID identifies the game. Every move code carries its digest.
	ID string
	// Rules is the ruleset both players will use.
	Rules game.Ruleset
	// HostSide is the side the host took; the guest plays the other one.
	HostSide game.Player
	// HostName is the host's name, for the guest to see who invited them.
	HostName string
}

// GuestSide is the side the invited player takes.
func (inv Invite) GuestSide() game.Player { return inv.HostSide.Opponent() }

// NewInvite returns an invite for a fresh game.
func NewInvite(rs game.Ruleset, hostSide game.Player, hostName string) (Invite, error) {
	if err := rs.Validate(); err != nil {
		return Invite{}, err
	}
	if hostSide != game.Vertical && hostSide != game.Horizontal {
		return Invite{}, errors.New("the host must choose a side before inviting an opponent")
	}
	return Invite{ID: NewGameID(), Rules: rs, HostSide: hostSide, HostName: cleanName(hostName)}, nil
}

// Rule flags in an invite. The ruleset travels as three bytes rather than as
// its canonical text because an invite is meant to be pasted into a chat; the
// fingerprint that travels with it is what proves the two builds reconstruct
// the same rules from them.
const (
	flagDeliberate = 1 << iota
	flagLinkRemoval
	flagPegRemoval
	flagOwnCross
	flagSwap
)

// validateGameID keeps every move tied to the invite and saved game that owns
// it. An empty identifier would make unrelated games share the same digest.
//
// The character set is checked and not only the length, because an identifier
// does not stay inside this package: the caller turns it into a file name, and
// it arrives from a code somebody pasted. Bounding the length alone admitted
// "/etc/twixt" and an identifier with an escape sequence in it. Everything this
// program generates is letters and digits -- upper case from NewGameID, lower
// case from the game store -- so letters, digits and the hyphen the store
// accepts is the whole of what a real identifier can hold.
func validateGameID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("a correspondence game needs a game identifier")
	}
	if len(id) > maxGameIDLen {
		return fmt.Errorf("game identifier is %d characters, the limit is %d", len(id), maxGameIDLen)
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
		default:
			return fmt.Errorf("game identifier %q may only hold letters, digits and hyphens", safeText(id, maxGameIDLen))
		}
	}
	return nil
}

// EncodeInvite renders an invite as a code to paste to the opponent.
func EncodeInvite(inv Invite) (string, error) {
	if err := inv.Rules.Validate(); err != nil {
		return "", err
	}
	if inv.HostSide != game.Vertical && inv.HostSide != game.Horizontal {
		return "", errors.New("the invite does not say which side the host took")
	}
	id := strings.TrimSpace(inv.ID)
	if id == "" {
		return "", errors.New("the invite has no game identifier")
	}
	if len(id) > maxGameIDLen {
		return "", fmt.Errorf("game identifier is %d characters, the limit is %d", len(id), maxGameIDLen)
	}
	name := cleanName(inv.HostName)
	fingerprint, err := hex.DecodeString(inv.Rules.Fingerprint())
	if err != nil {
		return "", fmt.Errorf("the engine's ruleset fingerprint is not hexadecimal: %w", err)
	}
	if len(fingerprint) != codeHashBytes {
		return "", fmt.Errorf("the engine's ruleset fingerprint is %d bytes, this format carries %d", len(fingerprint), codeHashBytes)
	}

	var flags byte
	for _, f := range []struct {
		on   bool
		mask byte
	}{
		{inv.Rules.DeliberateLinking, flagDeliberate},
		{inv.Rules.LinkRemoval, flagLinkRemoval},
		{inv.Rules.PegRemoval, flagPegRemoval},
		{inv.Rules.OwnLinksMayCross, flagOwnCross},
		{inv.Rules.Swap, flagSwap},
	} {
		if f.on {
			flags |= f.mask
		}
	}

	payload := make([]byte, 0, inviteHeaderLen+len(id)+1+len(name)+codeChecksumLen)
	payload = append(payload, codeVersion, byte(inv.Rules.Size), flags, byte(inv.HostSide))
	payload = append(payload, fingerprint...)
	payload = append(payload, byte(len(id)))
	payload = append(payload, id...)
	payload = append(payload, byte(len(name)))
	payload = append(payload, name...)
	payload = binary.BigEndian.AppendUint32(payload, crc32.ChecksumIEEE(payload))
	return formatCode(invitePrefix, payload), nil
}

// DecodeInvite reads an invite code. It rebuilds the ruleset from the flags and
// then checks that this build fingerprints it the way the host did, so two
// releases that do not agree about the rules are caught here rather than
// several moves later.
func DecodeInvite(code string) (Invite, error) {
	var inv Invite
	payload, err := parseCode(code, invitePrefix)
	if err != nil {
		return inv, err
	}
	if len(payload) < inviteHeaderLen+1+codeChecksumLen {
		return inv, fmt.Errorf("%w: the invite is too short; part of it is probably missing", ErrBadCode)
	}
	if payload[0] != codeVersion {
		return inv, fmt.Errorf("%w: the invite uses format version %d and this build understands version %d", ErrBadCode, payload[0], codeVersion)
	}
	body := payload[:len(payload)-codeChecksumLen]
	side := game.Player(body[3])
	if side != game.Vertical && side != game.Horizontal {
		return inv, fmt.Errorf("%w: the invite does not name a side", ErrBadCode)
	}
	flags := body[2]
	const knownFlags = flagDeliberate | flagLinkRemoval | flagPegRemoval | flagOwnCross | flagSwap
	if flags & ^byte(knownFlags) != 0 {
		return inv, fmt.Errorf("%w: the invite has rule flags this build does not understand (%#02x)", ErrRuleset, flags & ^byte(knownFlags))
	}
	rs := game.Ruleset{
		Size:              int(body[1]),
		DeliberateLinking: flags&flagDeliberate != 0,
		LinkRemoval:       flags&flagLinkRemoval != 0,
		PegRemoval:        flags&flagPegRemoval != 0,
		OwnLinksMayCross:  flags&flagOwnCross != 0,
		Swap:              flags&flagSwap != 0,
	}
	if err := rs.Validate(); err != nil {
		return inv, fmt.Errorf("%w: the invite describes an unplayable game: %w", ErrBadCode, err)
	}
	if got := rs.Fingerprint(); got != hex.EncodeToString(body[4:8]) {
		return inv, fmt.Errorf("%w: the invite's rules fingerprint to %s here and to %s where it was made; both ends need the same twixtui release", ErrRuleset, got, hex.EncodeToString(body[4:8]))
	}

	rest := body[inviteHeaderLen-1:]
	id, rest, err := takeString(rest)
	if err != nil {
		return inv, fmt.Errorf("%w: the invite's game identifier is cut short", ErrBadCode)
	}
	// EncodeInvite checks the identifier and DecodeInvite used to check nothing:
	// the length prefix allows 255 bytes of anything, and the caller turns what
	// comes back into a file name. The same rule is applied here, so a hostile
	// paste is refused by the format that read it rather than by whatever the
	// caller happens to check afterwards.
	id = strings.TrimSpace(id)
	if err := validateGameID(id); err != nil {
		return inv, fmt.Errorf("%w: the invite's game identifier is not one this program could have produced: %w", ErrBadCode, err)
	}
	name, rest, err := takeString(rest)
	if err != nil {
		return inv, fmt.Errorf("%w: the invite's host name is cut short", ErrBadCode)
	}
	// The name is chosen by whoever made the invite and is drawn on this
	// player's terminal, so it is sanitised on the way in as well as on the way
	// out. Filtering only when encoding would leave every caller of this
	// function responsible for remembering, and there is more than one.
	name = safeText(name, maxNameLen)
	if len(rest) != 0 {
		return inv, fmt.Errorf("%w: the invite carries %d bytes more than it should", ErrBadCode, len(rest))
	}
	return Invite{ID: id, Rules: rs, HostSide: side, HostName: name}, nil
}

// takeString reads a length-prefixed string.
func takeString(b []byte) (string, []byte, error) {
	if len(b) == 0 {
		return "", nil, errors.New("no length")
	}
	n := int(b[0])
	if len(b) < 1+n {
		return "", nil, errors.New("truncated")
	}
	return string(b[1 : 1+n]), b[1+n:], nil
}

// parseCode strips the prefix and the grouping, decodes the base32 and verifies
// the checksum. The checksum is checked before anything else is believed, so a
// mangled paste is reported as mangled rather than as a strange move.
func parseCode(code, prefix string) ([]byte, error) {
	if len(code) > maxCodeTextLen {
		return nil, fmt.Errorf("%w: the pasted code is %d characters, over the %d character limit", ErrBadCode, len(code), maxCodeTextLen)
	}
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '-', '_', '.', ',':
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))

	// The invite prefix begins with the move prefix, so the longer one has to
	// be ruled out explicitly to give the right message.
	other := movePrefix
	if prefix == movePrefix {
		other = invitePrefix
	}
	body, ok := strings.CutPrefix(cleaned, prefix)
	if !ok || (prefix == movePrefix && strings.HasPrefix(cleaned, invitePrefix)) {
		if strings.HasPrefix(cleaned, other) {
			return nil, fmt.Errorf("%w: that is a %s code, not a %s code", ErrBadCode, kindOf(other), kindOf(prefix))
		}
		return nil, fmt.Errorf("%w: a %s code starts with %s-", ErrBadCode, kindOf(prefix), prefix)
	}
	payload, padded, err := decode32(body)
	if err != nil {
		return nil, err
	}
	if len(payload) < codeChecksumLen+1 {
		return nil, fmt.Errorf("%w: the code is too short; part of it is probably missing", ErrBadCode)
	}
	split := len(payload) - codeChecksumLen
	want := binary.BigEndian.Uint32(payload[split:])
	if got := crc32.ChecksumIEEE(payload[:split]); got != want {
		return nil, fmt.Errorf("%w: the code did not survive being copied (checksum %08x, expected %08x); ask your opponent to send it again", ErrBadCode, got, want)
	}
	if padded {
		// The payload is whole and it checksums, so the damage is confined to
		// the bits of the last character that carry nothing: only the last
		// character can have been altered, since anything lost or changed
		// earlier moves a payload byte and the checksum above would have
		// spoken. This is the one message that can name where the fault is,
		// and the one place that must not repeat the truncation story —
		// telling a player to look for a lost tail that is not lost sends them
		// hunting for text that was never sent.
		return nil, fmt.Errorf("%w: the last character of the code was altered on the way; ask your opponent to send it again", ErrBadCode)
	}
	return payload, nil
}

func kindOf(prefix string) string {
	if prefix == invitePrefix {
		return "game invite"
	}
	return "move"
}

// formatCode renders a payload as a prefixed, dash-grouped code.
func formatCode(prefix string, payload []byte) string {
	enc := encode32(payload)
	var b strings.Builder
	b.Grow(len(prefix) + len(enc) + len(enc)/codeGroup + 2)
	b.WriteString(prefix)
	for i := 0; i < len(enc); i += codeGroup {
		b.WriteByte('-')
		b.WriteString(enc[i:min(i+codeGroup, len(enc))])
	}
	return b.String()
}

// codeAlphabet is Crockford's base32: it leaves out I, L, O and U, so no two
// characters in it are easily confused with one another when a player reads a
// code out or types it back in.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var codeValues = buildCodeValues()

func buildCodeValues() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for v, c := range []byte(codeAlphabet) {
		t[c] = int8(v)
		if c >= 'A' && c <= 'Z' {
			t[c+('a'-'A')] = int8(v)
		}
	}
	// The characters Crockford leaves out are accepted as the ones they get
	// mistaken for. This is the typo resistance: a player who writes I for 1 or
	// O for 0 still gets their move through.
	for _, p := range []struct {
		c byte
		v int8
	}{{'I', 1}, {'i', 1}, {'L', 1}, {'l', 1}, {'O', 0}, {'o', 0}} {
		t[p.c] = p.v
	}
	return t
}

func codeValue(r rune) (int, bool) {
	if r < 0 || r > 255 {
		return 0, false
	}
	v := codeValues[byte(r)]
	if v < 0 {
		return 0, false
	}
	return int(v), true
}

// randomCode returns n characters of the code alphabet.
func randomCode(n int) string {
	b := make([]byte, n)
	// crypto/rand.Read is documented never to fail.
	_, _ = rand.Read(b)
	for i, v := range b {
		// 256 is a whole multiple of the alphabet size, so this is unbiased.
		b[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(b)
}

// encode32 writes five bits per character, most significant first, padding the
// last character with zero bits.
func encode32(src []byte) string {
	out := make([]byte, 0, (len(src)*8+4)/5)
	var acc uint32
	var bits uint
	for _, b := range src {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, codeAlphabet[(acc>>bits)&31])
		}
	}
	if bits > 0 {
		out = append(out, codeAlphabet[(acc<<(5-bits))&31])
	}
	return string(out)
}

// decode32 reverses encode32. It returns the payload, and separately whether
// the last character's padding bits were not zero, which the caller weighs
// against the checksum rather than reporting on its own: non-zero padding
// means either a lost tail or an altered final character, and only the
// checksum can tell those apart.
//
// A wrong length, on the other hand, decode32 can diagnose alone. Five bits
// per character leaves at most four over, so a code whose bits do not divide
// that way is not the length any whole code has, and characters must have gone
// missing. That is worth saying before the checksum is consulted, because it
// is the only diagnosis that names a cause.
func decode32(s string) ([]byte, bool, error) {
	out := make([]byte, 0, len(s)*5/8)
	var acc uint32
	var bits uint
	for _, r := range s {
		v, ok := codeValue(r)
		if !ok {
			return nil, false, fmt.Errorf("%w: the code contains %q, which is not part of a code", ErrBadCode, string(r))
		}
		acc = acc<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte((acc>>bits)&0xff))
		}
	}
	if bits >= 5 {
		return nil, false, fmt.Errorf("%w: the code ends in the middle of a character; part of it is probably missing", ErrBadCode)
	}
	return out, bits > 0 && acc&((1<<bits)-1) != 0, nil
}

func shortSum(sum [sha256.Size]byte) string {
	return hex.EncodeToString(sum[:codeHashBytes])
}
