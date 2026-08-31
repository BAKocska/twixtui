package leaderboard

import (
	"math"
	"strings"
)

// Rating parameters.
//
// StartRating is where every human profile enters the scale. 1200 is the
// conventional unrated entry point in club rating systems, and starting well
// above zero keeps a beginner's first few losses from producing a negative
// number, which reads as a bug rather than as a rating.
//
// The K factor is how far one game can move a rating. It is deliberately two
// values: a new profile's 1200 is a guess, so its first games should move it
// quickly, while an established profile's rating is an estimate worth
// defending. With K=16 a player who is genuinely 700 points stronger than the
// seed needs roughly forty wins to get there; K=32 for the provisional period
// halves the climb without letting a single upset later swing an established
// rating by more than sixteen points.
const (
	StartRating      = 1200
	ProvisionalK     = 32.0
	EstablishedK     = 16.0
	ProvisionalGames = 10
)

// Name prefixes that mark a non-profile participant. A recorded opponent is
// either a local profile name, or one of these.
const (
	// BotPrefix marks a bot opponent, followed by its tier name.
	BotPrefix = "bot:"
	// RemotePrefix marks a networked opponent, followed by the name they gave.
	RemotePrefix = "remote:"
)

// botRatings are the fixed ratings of the bot tiers.
//
// Bots are anchors, not participants: a bot's strength is a program constant,
// so letting its rating drift with play volume would move the whole scale
// underneath every human on the board. The keys are the tier names from
// internal/bot.
//
// The 400-point spacing is a scoring decision, not a measurement. 400 points is
// the width at which the stronger side is expected to score about 91%, so the
// steps encode "each tier is meant to be a clearly different opponent, not a
// slightly different one". What the spacing buys is the reward gradient the
// board needs: from the 1200 seed a win over the pro bot is worth 0.97*K points
// while a win over the beginner bot is worth 0.24*K, so beating the pro is worth
// four times as much. Beginner sits below the seed because a new player is
// expected to beat it; pro sits 600 above because a new player is not.
//
// These numbers are nominal anchors. The measured tier gap comes from the
// self-play tournament that establishes the tiers really do differ, and the
// anchors should be re-centred on those win rates once they exist rather than
// left as a guess that looks like a measurement.
var botRatings = map[string]int{
	"beginner":     1000,
	"intermediate": 1400,
	"pro":          1800,
}

// BotName returns the opponent name to record for a bot of the given tier.
func BotName(tier string) string { return BotPrefix + tier }

// RemoteName returns the opponent name to record for a networked opponent.
func RemoteName(name string) string { return RemotePrefix + name }

// IsBot reports whether a participant name denotes a bot, whose rating is fixed.
func IsBot(name string) bool { return strings.HasPrefix(name, BotPrefix) }

// DisplayName strips the participant-kind prefix from a name, for a leaderboard
// column that has its own way of showing what kind of opponent it was.
func DisplayName(name string) string {
	if rest, ok := strings.CutPrefix(name, BotPrefix); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(name, RemotePrefix); ok {
		return rest
	}
	return name
}

// anchorRating returns the fixed rating of a non-participating opponent. An
// unrecognised bot tier is anchored at the seed rather than refused, so an
// older binary reading results written by a newer one still produces a board.
func anchorRating(name string) (int, bool) {
	tier, ok := strings.CutPrefix(name, BotPrefix)
	if !ok {
		return 0, false
	}
	if rating, ok := botRatings[tier]; ok {
		return rating, true
	}
	return StartRating, true
}

// expectedScore is the standard Elo expectation: the share of a game a player
// rated a is expected to take against a player rated b, where a 400-point lead
// means ten-to-one odds.
func expectedScore(a, b float64) float64 {
	return 1 / (1 + math.Pow(10, (b-a)/400))
}

// kFactor returns how far a result may move a rating, given how many rated
// games the participant has already played.
func kFactor(games int) float64 {
	if games < ProvisionalGames {
		return ProvisionalK
	}
	return EstablishedK
}
