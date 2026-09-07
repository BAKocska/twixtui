package game

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A transcript on its own is a list of moves and nothing more, so it cannot tell
// a genuine game from a truncated or edited one: drop an entry, change a hole or
// strip a declined-link annotation and the result is still a legal record, just
// of a different game. That matters wherever a record arrives from somewhere
// else, which is to say a saved game, a correspondence code or a networked
// opponent.
//
// A Record wraps the transcript with the ruleset it was played under, the result
// it reached, and two digests. Both digests are recomputable by anyone, so this
// detects corruption, truncation and accidental divergence; it is not a
// signature and does not claim to stop a determined forger. What it does
// guarantee is that a record which replays to a different game than it says it
// does is rejected instead of silently accepted.

// RecordVersion is the format version written by Encode.
const RecordVersion = 1

const recordHeader = "twixtui-record"

// MaxRecordBytes is the largest encoded record this build accepts. A record
// arrives from somewhere else — a file, standard input, a correspondence code —
// so its size is a property of the input rather than of this program, and the
// bound is applied before the input is held rather than after.
//
// It is a practical safety cap, not a statement about every record that could
// ever be written: a history may hold entries that place no peg, and link
// edits can revisit holes already played, so there is no small bound on a
// transcript in general. The cap leaves room for ordinary games on the widest
// supported board, 48x48. Longer custom histories can exceed it and are refused
// explicitly rather than read into memory without a bound.
//
// The bound holds in both directions, and it has to: a record this build would
// refuse to read is not one to store or send on. EncodeCanonical is where the
// outgoing side of it is checked.
const MaxRecordBytes = 1 << 20

// ErrRecordTooLarge is what every refusal on size wraps, and the one place the
// bound is named to a reader. A caller can then tell a record refused for its
// size from one refused for what it says, which are different problems: the
// first was never read, the second was read and found wanting.
var ErrRecordTooLarge = fmt.Errorf("a game record is at most %d bytes", MaxRecordBytes)

// excerptBytes bounds how much of the input a diagnostic repeats.
const excerptBytes = 64

// excerpt is what a diagnostic quotes. Every quoted fragment of a record goes
// through it: the record was written by whoever sent it, so an error that
// echoes the whole of it back is not a diagnostic but a copy of the input, and
// escaping expands unprintable bytes several times over on the way out.
func excerpt(s string) string {
	if len(s) <= excerptBytes {
		return s
	}
	cut := excerptBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Record is a game together with everything needed to check it replays to the
// game it claims to be.
type Record struct {
	Version int
	Ruleset Ruleset
	// Moves is the transcript: record entries separated by semicolons.
	Moves string
	// Outcome and Reason are the result the record claims to reach.
	Outcome Outcome
	Reason  Reason
	// Position is a digest of the final position, independent of the move text.
	Position string
	// Entries is the number of record entries, which pins padding that changes
	// nothing on the board, such as a repeated draw offer.
	Entries int
	// Digest covers the whole record and catches an edit to any other field.
	Digest string
}

// Record returns a verifiable record of the game as it stands.
func (g *Game) Record() (Record, error) {
	moves, err := g.Transcript()
	if err != nil {
		return Record{}, err
	}
	r := Record{
		Version:  RecordVersion,
		Ruleset:  g.rs,
		Moves:    moves,
		Outcome:  g.result.Outcome,
		Reason:   g.result.Reason,
		Position: PositionDigest(g),
		Entries:  len(g.history),
	}
	r.Digest = r.digest()
	return r, nil
}

// digest hashes every field except itself, so an edit anywhere else shows up.
func (r Record) digest() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%s\x00%d",
		recordHeader, r.Version, r.Ruleset.Canonical(), r.Moves,
		r.Outcome, r.Reason, r.Position, r.Entries)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PositionDigest hashes the position itself: the pegs, the links, the side to
// move and the result. It is derived from the board rather than from the move
// text, so it catches a record whose moves do not lead where it says they do.
// Iteration is in a fixed order, so the digest depends only on the position.
func PositionDigest(g *Game) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00", g.rs.Canonical(), g.n)
	for i := range g.pegs {
		if g.pegs[i] == NoPlayer && g.links[i] == 0 {
			continue
		}
		fmt.Fprintf(h, "%d:%d:%d;", i, g.pegs[i], g.links[i])
	}
	// The pending draw offer is part of the state the two sides have to agree
	// about: without it, two clients can differ over whether a draw is on offer
	// while their digests match, and one will accept a draw the other believes
	// was never made.
	fmt.Fprintf(h, "\x00%d\x00%d\x00%d\x00%t\x00%d",
		g.turn, g.result.Outcome, g.result.Reason, g.swapped, g.drawOfferedBy)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

var outcomeNames = map[Outcome]string{
	Ongoing:        "ongoing",
	VerticalWins:   "vertical-wins",
	HorizontalWins: "horizontal-wins",
	Draw:           "draw",
}

var reasonNames = map[Reason]string{
	NotOver:     "not-over",
	Connection:  "connection",
	NoMovesLeft: "no-moves-left",
	Resignation: "resignation",
	Agreement:   "agreement",
}

func lookupName[T comparable](names map[T]string, want string) (T, bool) {
	var keys []T
	for k := range names {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return names[keys[i]] < names[keys[j]] })
	for _, k := range keys {
		if names[k] == want {
			return k, true
		}
	}
	var zero T
	return zero, false
}

// Encode writes the record as text: one field per line, the moves last but for
// the digest, so a record stays readable and diffable. It says nothing about
// how large the result is; a record on its way out of this program goes through
// EncodeCanonical instead.
func (r Record) Encode() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d\n", recordHeader, r.Version)
	fmt.Fprintf(&b, "ruleset %s\n", r.Ruleset.Canonical())
	fmt.Fprintf(&b, "result %s %s\n", outcomeNames[r.Outcome], reasonNames[r.Reason])
	fmt.Fprintf(&b, "position %s\n", r.Position)
	fmt.Fprintf(&b, "entries %d\n", r.Entries)
	fmt.Fprintf(&b, "moves %s\n", r.Moves)
	fmt.Fprintf(&b, "digest %s\n", r.Digest)
	return b.String()
}

// EncodeCanonical is Encode for a record leaving this program — into the store,
// into a file, onto standard output — and it refuses a record whose encoding
// this build would not read back.
//
// Reading and writing are not symmetric, which is why this exists. A reader is
// lenient about spelling: the ruleset's flags go through strconv.ParseBool, so
// the value in "swap=1" expands from "1" to "true" when it is written back out.
// An input inside the limit can therefore canonicalise past it, and a record
// accepted as a game would be stored or handed to somebody else in a form
// nothing — this build included — could read again. Whatever is accepted as a
// game has to survive being written down, so the size of the encoding is
// checked here, before its caller has anything to write. The encoding itself is
// unchanged: canonical means canonical, flags spelled out in full.
func (r Record) EncodeCanonical() (string, error) {
	s := r.Encode()
	if len(s) > MaxRecordBytes {
		return "", fmt.Errorf("%w; this one encodes to %d", ErrRecordTooLarge, len(s))
	}
	return s, nil
}

// DecodeRecord parses a record and checks its digest. It does not replay the
// game; call Replay for that.
//
// A record is one record: every field appears exactly once. Two records in one
// file repeat the header, which is refused here, because reading such a file as
// whichever record happened to come last keeps bytes nothing ever checked
// beside a game that was checked.
func DecodeRecord(s string) (Record, error) {
	if len(s) > MaxRecordBytes {
		return Record{}, fmt.Errorf("%w; this one is %d", ErrRecordTooLarge, len(s))
	}
	var r Record
	seen := map[string]bool{}
	for i, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		if seen[key] {
			return Record{}, fmt.Errorf("line %d: %q appears twice; a record holds one of each field, so this is either an edited record or several records in one file",
				i+1, excerpt(key))
		}
		seen[key] = true
		switch key {
		case recordHeader:
			v, err := strconv.Atoi(rest)
			if err != nil {
				return Record{}, fmt.Errorf("line %d: unreadable format version %q", i+1, excerpt(rest))
			}
			if v != RecordVersion {
				return Record{}, fmt.Errorf("this record is version %d, this build reads version %d", v, RecordVersion)
			}
			r.Version = v
		case "ruleset":
			rs, err := ParseCanonicalRuleset(rest)
			if err != nil {
				return Record{}, fmt.Errorf("line %d: %w", i+1, err)
			}
			r.Ruleset = rs
		case "result":
			outName, reasonName, ok := strings.Cut(rest, " ")
			if !ok {
				return Record{}, fmt.Errorf("line %d: result needs an outcome and a reason", i+1)
			}
			out, ok := lookupName(outcomeNames, outName)
			if !ok {
				return Record{}, fmt.Errorf("line %d: unknown outcome %q", i+1, excerpt(outName))
			}
			reason, ok := lookupName(reasonNames, strings.TrimSpace(reasonName))
			if !ok {
				return Record{}, fmt.Errorf("line %d: unknown end reason %q", i+1, excerpt(reasonName))
			}
			r.Outcome, r.Reason = out, reason
		case "position":
			r.Position = rest
		case "entries":
			n, err := strconv.Atoi(rest)
			if err != nil {
				return Record{}, fmt.Errorf("line %d: unreadable entry count %q", i+1, excerpt(rest))
			}
			r.Entries = n
		case "moves":
			r.Moves = rest
		case "digest":
			r.Digest = rest
		default:
			return Record{}, fmt.Errorf("line %d: unknown field %q", i+1, excerpt(key))
		}
	}
	// The moves field is required even though an empty transcript is a valid
	// game: a record that never says what was played is not one, and without
	// this the omission is reported as a digest mismatch, which reads as
	// tampering rather than as the missing field it is.
	for _, need := range []string{recordHeader, "ruleset", "result", "position", "entries", "moves", "digest"} {
		if !seen[need] {
			return Record{}, fmt.Errorf("record is missing its %s", need)
		}
	}
	if want := r.digest(); want != r.Digest {
		return Record{}, fmt.Errorf("this record has been altered or truncated: digest is %s but its contents hash to %s", excerpt(r.Digest), want)
	}
	return r, nil
}

// Replay rebuilds the game from the record and checks it arrives where the
// record says it does. A record whose moves lead somewhere else is refused.
func (r Record) Replay() (*Game, error) {
	g, err := ReplayTranscript(r.Ruleset, r.Moves)
	if err != nil {
		return nil, err
	}
	if got := g.Result(); got.Outcome != r.Outcome || got.Reason != r.Reason {
		return nil, fmt.Errorf("record claims %s by %s but its moves end %s by %s",
			outcomeNames[r.Outcome], reasonNames[r.Reason],
			outcomeNames[got.Outcome], reasonNames[got.Reason])
	}
	if got := PositionDigest(g); got != r.Position {
		return nil, fmt.Errorf("record claims final position %s but its moves reach %s", excerpt(r.Position), got)
	}
	if got := g.Entries(); got != r.Entries {
		return nil, fmt.Errorf("record claims %d entries but its moves make %d", r.Entries, got)
	}
	return g, nil
}

// LoadRecord decodes and replays in one step, which is what a caller reading a
// saved game wants.
func LoadRecord(s string) (*Game, Record, error) {
	r, err := DecodeRecord(s)
	if err != nil {
		return nil, Record{}, err
	}
	g, err := r.Replay()
	if err != nil {
		return nil, Record{}, err
	}
	return g, r, nil
}

// ReadRecord reads one record from r, then decodes and replays it. This is the
// way in for a record that comes from a file or a pipe: the reader stops after
// MaxRecordBytes, so an input that is not a record — or is far too large to be
// one — is refused without the rest of it ever being held in memory.
func ReadRecord(r io.Reader) (*Game, Record, error) {
	var b strings.Builder
	n, err := io.Copy(&b, io.LimitReader(r, MaxRecordBytes+1))
	if err != nil {
		return nil, Record{}, err
	}
	if n > MaxRecordBytes {
		return nil, Record{}, fmt.Errorf("%w; this input is longer", ErrRecordTooLarge)
	}
	return LoadRecord(b.String())
}

// ParseCanonicalRuleset reads the encoding produced by Ruleset.Canonical.
func ParseCanonicalRuleset(s string) (Ruleset, error) {
	var rs Ruleset
	seen := map[string]bool{}
	for _, field := range strings.Split(strings.TrimSpace(s), ";") {
		if field == "" {
			continue
		}
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return Ruleset{}, fmt.Errorf("malformed ruleset field %q", excerpt(field))
		}
		if seen[key] {
			return Ruleset{}, fmt.Errorf("ruleset field %q appears twice", excerpt(key))
		}
		seen[key] = true
		switch key {
		case "size":
			n, err := strconv.Atoi(value)
			if err != nil {
				return Ruleset{}, fmt.Errorf("unreadable board size %q", excerpt(value))
			}
			rs.Size = n
		case "deliberate", "removal", "pegremoval", "owncross", "swap":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return Ruleset{}, fmt.Errorf("unreadable %s value %q", key, excerpt(value))
			}
			switch key {
			case "deliberate":
				rs.DeliberateLinking = b
			case "removal":
				rs.LinkRemoval = b
			case "pegremoval":
				rs.PegRemoval = b
			case "owncross":
				rs.OwnLinksMayCross = b
			case "swap":
				rs.Swap = b
			}
		default:
			return Ruleset{}, fmt.Errorf("unknown ruleset field %q", excerpt(key))
		}
	}
	for _, need := range []string{"size", "deliberate", "removal", "pegremoval", "owncross", "swap"} {
		if !seen[need] {
			return Ruleset{}, fmt.Errorf("ruleset is missing %s", need)
		}
	}
	if err := rs.Validate(); err != nil {
		return Ruleset{}, err
	}
	return rs, nil
}
