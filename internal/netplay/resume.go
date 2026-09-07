package netplay

import (
	"errors"
	"fmt"

	"github.com/BAKocska/twixtui/internal/game"
)

// SnapshotFor builds the state a stored game is picked up from, so that a game
// whose connection was lost can be continued on a new one.
//
// The transcript is read out of the game rather than out of the record text:
// every entry is rendered in the notation the two ends exchange, and tagged
// with the side that made it. The side has to come from the record and cannot
// be counted off the move order, because a resignation and the two draw
// messages may be made by the side that is not to move, and the far end
// enforces the actor of every entry it is asked to replay.
//
// The role is the end this machine takes on the new connection. Nothing in a
// stored game says which end it was, and nothing should: a role belongs to a
// connection, not to a game, and the same game may be picked up from either
// side of the next one. It is therefore the player's answer, passed in here.
func SnapshotFor(g *game.Game, role Role, side game.Player, name, opponent string) (Snapshot, error) {
	if g == nil {
		return Snapshot{}, errors.New("there is no game to continue")
	}
	if side != game.Vertical && side != game.Horizontal {
		return Snapshot{}, fmt.Errorf("%q is not a side to continue on", side)
	}
	moves, err := transcriptOf(g)
	if err != nil {
		return Snapshot{}, err
	}
	// The transcript is the whole of what the two ends compare, so it has to
	// rebuild the position it was read from. One that does not would be a
	// divergence this end discovered only after the opponent had connected,
	// with a saved game already committed to it.
	rebuilt, err := replay(g.Rules(), moves)
	if err != nil {
		return Snapshot{}, fmt.Errorf("the record cannot be replayed as a transcript: %w", err)
	}
	if PositionHash(rebuilt) != PositionHash(g) {
		return Snapshot{}, errors.New("the record and the transcript read out of it describe different positions")
	}
	return Snapshot{
		Role:     role,
		Rules:    g.Rules(),
		Side:     side,
		Name:     name,
		Opponent: opponent,
		Moves:    moves,
	}, nil
}

// transcriptOf reads the shared transcript out of a game, one entry per record
// line.
func transcriptOf(g *game.Game) ([]Entry, error) {
	history := g.History()
	out := make([]Entry, 0, len(history))
	for i, m := range history {
		notation, err := g.MoveNotation(i)
		if err != nil {
			return nil, fmt.Errorf("reading entry %d of the record: %w", i+1, err)
		}
		out = append(out, Entry{Side: m.Player, Move: notation})
	}
	return out, nil
}
