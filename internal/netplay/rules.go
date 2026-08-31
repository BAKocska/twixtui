package netplay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BAKocska/twixtui/internal/game"
)

// The handshake sends the ruleset as game.Ruleset.Canonical() together with its
// fingerprint. The receiver parses the canonical form back into a Ruleset and
// then checks that its own engine fingerprints it identically, which is what
// makes the exchange self-checking: if the two builds disagree about what the
// canonical encoding means, or one of them has a rule the other has never
// heard of, the fingerprints differ and the game is refused before the first
// move rather than desyncing later.

// rulesetKeys are the keys game.Ruleset.Canonical() emits, in its order.
var rulesetKeys = []string{"size", "deliberate", "removal", "pegremoval", "owncross", "swap"}

// parseRuleset reads the encoding produced by game.Ruleset.Canonical(). It is
// strict: an unknown key, a missing key or a repeated key is an error, because
// each of those means the peer's rules are not fully understood here and
// guessing would be worse than refusing.
func parseRuleset(s string) (game.Ruleset, error) {
	var rs game.Ruleset
	seen := make(map[string]bool, len(rulesetKeys))
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return rs, fmt.Errorf("%w: ruleset setting %q has no value", ErrRuleset, part)
		}
		if seen[key] {
			return rs, fmt.Errorf("%w: ruleset repeats %q", ErrRuleset, key)
		}
		seen[key] = true

		if key == "size" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return rs, fmt.Errorf("%w: board size %q is not a number", ErrRuleset, value)
			}
			rs.Size = n
			continue
		}
		flag, err := strconv.ParseBool(value)
		if err != nil {
			return rs, fmt.Errorf("%w: ruleset setting %s=%q is not a yes or no", ErrRuleset, key, value)
		}
		switch key {
		case "deliberate":
			rs.DeliberateLinking = flag
		case "removal":
			rs.LinkRemoval = flag
		case "pegremoval":
			rs.PegRemoval = flag
		case "owncross":
			rs.OwnLinksMayCross = flag
		case "swap":
			rs.Swap = flag
		default:
			return rs, fmt.Errorf("%w: the opponent's ruleset has a setting this build does not know about (%q); both ends need the same twixtui release", ErrRuleset, key)
		}
	}
	for _, key := range rulesetKeys {
		if !seen[key] {
			return rs, fmt.Errorf("%w: the opponent's ruleset does not say anything about %q", ErrRuleset, key)
		}
	}
	if err := rs.Validate(); err != nil {
		return rs, fmt.Errorf("%w: the opponent offered an unplayable ruleset: %w", ErrRuleset, err)
	}
	return rs, nil
}

// describeRulesetDiff names every rule the two ends disagree about, so a
// refusal tells the player what to change rather than only that something is
// wrong.
func describeRulesetDiff(mine, theirs game.Ruleset) string {
	var parts []string
	if mine.Size != theirs.Size {
		parts = append(parts, fmt.Sprintf("board is %dx%d here and %dx%d there", mine.Size, mine.Size, theirs.Size, theirs.Size))
	}
	flags := []struct {
		name  string
		mine  bool
		other bool
	}{
		{"choosing your own links", mine.DeliberateLinking, theirs.DeliberateLinking},
		{"removing your own links", mine.LinkRemoval, theirs.LinkRemoval},
		{"removing your own pegs", mine.PegRemoval, theirs.PegRemoval},
		{"letting your own links cross", mine.OwnLinksMayCross, theirs.OwnLinksMayCross},
		{"the swap option", mine.Swap, theirs.Swap},
	}
	for _, f := range flags {
		if f.mine != f.other {
			parts = append(parts, fmt.Sprintf("%s is %s here and %s there", f.name, onOff(f.mine), onOff(f.other)))
		}
	}
	if len(parts) == 0 {
		return "no difference this build can name"
	}
	return strings.Join(parts, "; ")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// parsePlayer reads a side name from the wire, reporting one this build does not
// recognise as a protocol error.
func parsePlayer(s string) (game.Player, error) {
	pl, err := game.ParsePlayer(strings.TrimSpace(s))
	if err != nil {
		return game.NoPlayer, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return pl, nil
}
