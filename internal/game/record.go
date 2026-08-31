package game

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
// the digest, so a record stays readable and diffable.
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

// DecodeRecord parses a record and checks its digest. It does not replay the
// game; call Replay for that.
func DecodeRecord(s string) (Record, error) {
	var r Record
	seen := map[string]bool{}
	for i, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch key {
		case recordHeader:
			v, err := strconv.Atoi(rest)
			if err != nil {
				return Record{}, fmt.Errorf("line %d: unreadable format version %q", i+1, rest)
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
				return Record{}, fmt.Errorf("line %d: unknown outcome %q", i+1, outName)
			}
			reason, ok := lookupName(reasonNames, strings.TrimSpace(reasonName))
			if !ok {
				return Record{}, fmt.Errorf("line %d: unknown end reason %q", i+1, reasonName)
			}
			r.Outcome, r.Reason = out, reason
		case "position":
			r.Position = rest
		case "entries":
			n, err := strconv.Atoi(rest)
			if err != nil {
				return Record{}, fmt.Errorf("line %d: unreadable entry count %q", i+1, rest)
			}
			r.Entries = n
		case "moves":
			r.Moves = rest
		case "digest":
			r.Digest = rest
		default:
			return Record{}, fmt.Errorf("line %d: unknown field %q", i+1, key)
		}
		seen[key] = true
	}
	for _, need := range []string{recordHeader, "ruleset", "result", "position", "entries", "digest"} {
		if !seen[need] {
			return Record{}, fmt.Errorf("record is missing its %s", need)
		}
	}
	if want := r.digest(); want != r.Digest {
		return Record{}, fmt.Errorf("this record has been altered or truncated: digest is %s but its contents hash to %s", r.Digest, want)
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
		return nil, fmt.Errorf("record claims final position %s but its moves reach %s", r.Position, got)
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
			return Ruleset{}, fmt.Errorf("malformed ruleset field %q", field)
		}
		switch key {
		case "size":
			n, err := strconv.Atoi(value)
			if err != nil {
				return Ruleset{}, fmt.Errorf("unreadable board size %q", value)
			}
			rs.Size = n
		case "deliberate", "removal", "pegremoval", "owncross", "swap":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return Ruleset{}, fmt.Errorf("unreadable %s value %q", key, value)
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
			return Ruleset{}, fmt.Errorf("unknown ruleset field %q", key)
		}
		seen[key] = true
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
