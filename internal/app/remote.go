package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// RemoteConfig is the game a freshly connected session plays: the terms are
// whatever the handshake settled on, and there is no stored game behind it yet.
//
// It is here rather than in each of the two callers because the command line
// and the menu open the same game once a connection exists, and a seat map
// built twice is a seat map that can be built two ways.
func RemoteConfig(player string, session netplay.Session) GameConfig {
	side := session.Side()
	return GameConfig{
		Kind:  gamestore.Remote,
		Rules: session.Rules(),
		Seats: map[game.Player]Seat{
			side:            {Profile: player, Label: player},
			side.Opponent(): {Remote: true, Label: session.OpponentName()},
		},
		Session: session,
	}
}

// RemoteResume is a stored network game prepared to be continued on a new
// connection.
//
// What a lost connection leaves behind is a saved game like any other: the
// record, the seats, the identifier. It does not say which end of the
// connection this machine was, and it should not — the protocol's roles belong
// to a connection, and the players are free to swap who waits when they make
// the next one. So the role is asked for at the point the connection is made,
// through Host or Guest below, and everything else comes from the stored row.
//
// The identity of the game is what this type carries: continuing one goes back
// into the row it came out of, under the same identifier, with the same seats
// and the same transcript. Nothing here starts a game beside the saved one.
type RemoteResume struct {
	// Saved is the stored row, which the continued game is saved back into.
	Saved gamestore.Saved
	// Snapshot is what the two ends reconcile their transcripts against. Its
	// Role is not meaningful here — Host and Guest below each set their own,
	// because the role is the answer to how the connection is being made and
	// not a property of the stored game.
	Snapshot netplay.Snapshot
}

// PrepareRemoteResume checks that a stored game is a network game that can be
// continued and reads the transcript both ends will compare out of it.
//
// It refuses rather than falling back: a row of another kind, a finished game,
// a game belonging to another profile or one whose record will not replay has
// no connection to get back, and quietly starting a fresh game instead would
// lose the one the player asked for.
func PrepareRemoteResume(sv gamestore.Saved, player string) (RemoteResume, error) {
	// The identifier is what the continued game is saved back into. Without
	// one the game screen would allocate a fresh identifier and the game would
	// come out beside the saved one instead of in it, which is the fallback
	// this whole path exists to avoid.
	if sv.ID == "" {
		return RemoteResume{}, errors.New("the stored game has no identifier, so a continued game could not be saved back into it")
	}
	if sv.Kind != gamestore.Remote {
		return RemoteResume{}, fmt.Errorf("saved game %s is a %s game, not one played over a connection", sv.ID, sv.Kind)
	}
	g, err := sv.Game()
	if err != nil {
		return RemoteResume{}, err
	}
	if sv.Finished || g.Result().Over() {
		return RemoteResume{}, fmt.Errorf("saved game %s is over, so there is no connection to get back", sv.ID)
	}
	side, err := game.ParsePlayer(sv.Side)
	if err != nil {
		return RemoteResume{}, fmt.Errorf("saved game %s records an unreadable side %q: %w", sv.ID, sv.Side, err)
	}
	opponent, remote := strings.CutPrefix(sv.Opponent, leaderboard.RemotePrefix)
	if !remote || opponent == "" {
		return RemoteResume{}, fmt.Errorf("saved game %s was not played against an opponent on another machine, so there is no connection to get back", sv.ID)
	}
	// The seats are the stored ones. A game continued under a different
	// profile would be a different game's result at the end of it, so the
	// profile in hand has to be the one the row was saved for; only a row that
	// names nobody takes the current player, which is what an imported or
	// hand-written row would leave.
	local := sv.Player
	switch {
	case local == "":
		local = player
	case player != "" && !strings.EqualFold(local, player):
		return RemoteResume{}, fmt.Errorf("saved game %s is %s's, not %s's: continue it as %s", sv.ID, local, player, local)
	}
	snap, err := netplay.SnapshotFor(g, netplay.Host, side, local, opponent)
	if err != nil {
		return RemoteResume{}, fmt.Errorf("saved game %s: %w", sv.ID, err)
	}
	return RemoteResume{Saved: sv, Snapshot: snap}, nil
}

// Host is how to continue the game by waiting for the opponent to arrive.
func (r RemoteResume) Host() netplay.HostOptions {
	snap := r.Snapshot
	snap.Role = netplay.Host
	return netplay.HostOptions{Name: snap.Name, Rules: snap.Rules, Side: snap.Side, Resume: &snap}
}

// Guest is how to continue it by connecting to an opponent who is waiting.
//
// The ruleset and the side are both stated rather than left for the host to
// decide, which is what makes a host offering something else a refusal instead
// of a game played on terms the saved one was not.
func (r RemoteResume) Guest() netplay.GuestOptions {
	snap := r.Snapshot
	snap.Role = netplay.Guest
	return netplay.GuestOptions{Name: snap.Name, Rules: snap.Rules, Side: snap.Side, Resume: &snap}
}

// Continue is the game the reconnected session plays: the stored row, the
// stored seats, the stored identifier.
func (r RemoteResume) Continue(session netplay.Session) GameConfig {
	saved := r.Saved
	side := r.Snapshot.Side
	return GameConfig{
		Kind:  gamestore.Remote,
		Rules: r.Snapshot.Rules,
		Seats: map[game.Player]Seat{
			side: {Profile: r.Snapshot.Name, Label: r.Snapshot.Name},
			// The stored bare name, not the name the opponent gave this time:
			// the label goes back through leaderboard.RemoteName on the next
			// save, so the row a continued game is written to is the row it
			// came from, and a result recorded later is recorded against the
			// same opponent. What the panel calls them still comes from the
			// connection, which is where a name they have since changed is.
			side.Opponent(): {Remote: true, Label: r.Snapshot.Opponent},
		},
		Session: session,
		Resume:  &saved,
		StoreID: saved.ID,
	}
}

// Describe is the one-line summary of what is being continued, shown before
// the wait for the connection begins so that a reconnection cannot be mistaken
// for a new game.
func (r RemoteResume) Describe() string {
	return fmt.Sprintf("Continuing %s: %s (%s) against %s, %s in the record.",
		r.Saved.ID, r.Snapshot.Name, r.Snapshot.Side, r.Snapshot.Opponent,
		entriesPhrase(len(r.Snapshot.Moves)))
}

// entriesPhrase counts record entries. The listing's plural helper appends an
// "s", which this unit does not take.
func entriesPhrase(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}
