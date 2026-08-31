package game

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Ruleset holds the rule choices that historical editions and online venues of
// TwixT genuinely disagree about. Every divergence found while surveying the
// sources is an explicit option here rather than being silently baked into the
// engine; docs/rules.md records which source supports which setting.
type Ruleset struct {
	// Size is the side length of the square grid of holes. The standard
	// commercial board is 24.
	Size int

	// DeliberateLinking gives the player control over which links exist. The
	// printed box rules describe linking as a choice and state that a link a
	// player could have made but did not is no barrier, so omitting a link is a
	// legal and sometimes useful decision. Online venues instead link
	// automatically and offer no choice at all; set this false to reproduce
	// that. When true the engine still proposes every legal link on placement,
	// but the player may withdraw any of them before committing the turn.
	DeliberateLinking bool

	// LinkRemoval allows a player to take their own links, placed on earlier
	// turns, off the board as part of their turn. The box rules permit this;
	// the paper-and-pencil ruleset does not. Withdrawing a link proposed during
	// the current, uncommitted turn is governed by DeliberateLinking, not by
	// this option.
	LinkRemoval bool

	// PegRemoval additionally allows a player to lift their own previously
	// placed pegs, together with the links attached to them. Only one
	// transcription of the printed rules describes this, and no other
	// implementation or venue offers it, so it is off in every preset and must
	// be opted into.
	PegRemoval bool

	// OwnLinksMayCross relaxes the crossing rule so that only an opponent's
	// links block. Crossed links of the same colour are still not connected to
	// one another. The box rules forbid this; the paper-and-pencil ruleset
	// allows it.
	OwnLinksMayCross bool

	// Swap offers the second player a one-time option, immediately after the
	// first peg is placed, to take over that peg and side. Absent from the
	// original 1962 edition, present in every later edition and online venue.
	Swap bool
}

// Named rulesets.
var (
	// Std is the default: the printed box rules, with deliberate linking, own
	// links blocking, removable links and the swap option.
	Std = Ruleset{
		Size:              24,
		DeliberateLinking: true,
		LinkRemoval:       true,
		PegRemoval:        false,
		OwnLinksMayCross:  false,
		Swap:              true,
	}

	// PP is the paper-and-pencil ruleset used by online venues: links are
	// created automatically and are permanent, and a player's own links may
	// cross each other.
	PP = Ruleset{
		Size:              24,
		DeliberateLinking: false,
		LinkRemoval:       false,
		PegRemoval:        false,
		OwnLinksMayCross:  true,
		Swap:              true,
	}

	// Classic3M is the original 1962 3M edition: box rules without the swap
	// option, which Randolph added for a later edition.
	Classic3M = Ruleset{
		Size:              24,
		DeliberateLinking: true,
		LinkRemoval:       true,
		PegRemoval:        false,
		OwnLinksMayCross:  false,
		Swap:              false,
	}
)

// presets maps ruleset names to their definitions.
var presets = map[string]Ruleset{
	"std":     Std,
	"pp":      PP,
	"classic": Classic3M,
}

// presetSummaries explains each preset in one line, for help text and shell
// completion descriptions.
var presetSummaries = map[string]string{
	"std":     "printed box rules: choose your links, remove your own links, swap offered",
	"pp":      "paper and pencil, as played online: automatic permanent links, own links may cross",
	"classic": "original 1962 3M edition: box rules without the swap option",
}

// PresetNames returns the available ruleset names in a stable order.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Preset returns the named ruleset.
func Preset(name string) (Ruleset, error) {
	rs, ok := presets[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Ruleset{}, fmt.Errorf("unknown ruleset %q (known: %s)", name, strings.Join(PresetNames(), ", "))
	}
	return rs, nil
}

// PresetSummary returns the one-line description of a named preset.
func PresetSummary(name string) string {
	return presetSummaries[strings.ToLower(strings.TrimSpace(name))]
}

// PresetName returns the name of the preset matching rs ignoring board size, or
// the empty string if rs is not a preset.
func (rs Ruleset) PresetName() string {
	for _, name := range PresetNames() {
		p := presets[name]
		p.Size = rs.Size
		if p == rs {
			return name
		}
	}
	return ""
}

// MinSize and MaxSize bound the playable board. The lower bound keeps the board
// wide enough for a knight's move between opposite border rows; the upper bound
// matches the largest size offered by any known venue.
const (
	MinSize = 6
	MaxSize = 48
)

// Validate reports whether the ruleset is usable.
func (rs Ruleset) Validate() error {
	if rs.Size < MinSize || rs.Size > MaxSize {
		return fmt.Errorf("board size %d out of range %d..%d", rs.Size, MinSize, MaxSize)
	}
	if !rs.DeliberateLinking && rs.LinkRemoval {
		return fmt.Errorf("link removal requires deliberate linking: a ruleset that links automatically has no mechanism for taking links off")
	}
	if !rs.DeliberateLinking && rs.PegRemoval {
		return fmt.Errorf("peg removal requires deliberate linking")
	}
	return nil
}

// Describe renders the ruleset as a short human-readable summary.
func (rs Ruleset) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d", rs.Size, rs.Size)
	if name := rs.PresetName(); name != "" {
		fmt.Fprintf(&b, ", %s rules", name)
	}
	if rs.DeliberateLinking {
		b.WriteString(", you choose your links")
	} else {
		b.WriteString(", links are automatic")
	}
	if rs.LinkRemoval {
		b.WriteString(", own links removable")
	} else {
		b.WriteString(", links permanent")
	}
	if rs.PegRemoval {
		b.WriteString(", own pegs removable")
	}
	if rs.OwnLinksMayCross {
		b.WriteString(", own links may cross")
	} else {
		b.WriteString(", no link may cross another")
	}
	if rs.Swap {
		b.WriteString(", swap offered")
	} else {
		b.WriteString(", no swap")
	}
	return b.String()
}

// Canonical returns a stable, parseable encoding of the ruleset. Two engines
// that agree on this string agree on every rule, which is what makes it usable
// as a compatibility check between networked opponents.
func (rs Ruleset) Canonical() string {
	return fmt.Sprintf("size=%d;deliberate=%t;removal=%t;pegremoval=%t;owncross=%t;swap=%t",
		rs.Size, rs.DeliberateLinking, rs.LinkRemoval, rs.PegRemoval, rs.OwnLinksMayCross, rs.Swap)
}

// Fingerprint returns a short hash of the canonical encoding. The network
// handshake compares fingerprints so a mismatched opponent is rejected before
// the first move rather than desyncing mid-game.
func (rs Ruleset) Fingerprint() string {
	sum := sha256.Sum256([]byte(rs.Canonical()))
	return hex.EncodeToString(sum[:4])
}
