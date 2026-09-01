package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// Correspondence play needs no connection: each side keeps the game on its own
// machine and they exchange short codes. The code carries the position it was
// made in, so one pasted into the wrong game, pasted twice, or mangled on the
// way is refused instead of corrupting the game. twixtui keeps the game; the
// code carries only the move and the checks.

// The game identifier has two forms, and this file is the boundary between
// them.
//
// netplay mints the canonical one, in the alphabet its codes are written in, so
// it is upper case: A88SRF81. That is the form the invite carries, untouched,
// because the invite is netplay's own payload.
//
// A saved game is a file named after its identifier, so the store insists on
// lower case: on a case-insensitive filesystem two identifiers differing only in
// case would be one file and two unrelated games would overwrite each other.
// Everything on this side of the boundary therefore uses the lower-cased form —
// the store key, the identifier the move codes are bound to, and what the player
// is shown — and both ends derive it from the same invite by the same rule, so
// the two ends agree about what their codes are for.
//
// Only the invite carries an identifier as text. A move code carries
// netplay.GameDigest of one, four bytes, and never the characters themselves,
// so what the two ends must share is not a spelling but a digest. Taking it over
// the stored form is what keeps the identifier a player is shown, the one they
// type at --game, and the one a pasted code is checked against a single thing.
// A second form kept alongside for the digest would be one more thing to hold in
// step, and the first mistake in holding it would show up as every code being
// refused as belonging to another game.
//
// Lower-casing is safe to do twice and cannot merge two identifiers: netplay's
// alphabet is Crockford base32, which has no two characters differing only in
// case.

// correspondenceID turns the identifier an invite carries into the one this
// side stores the game under and binds its codes to.
func correspondenceID(inviteID string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(inviteID))
	if err := gamestore.ValidateID(id); err != nil {
		return "", fmt.Errorf("that invitation names a game this build cannot store: %w", err)
	}
	return id, nil
}

// startCorrespondence creates a game and prints an invitation to send.
func startCorrespondence(cmd *cobra.Command, deps app.Deps, player string, f *gameFlags) error {
	rs, err := f.rules()
	if err != nil {
		return err
	}
	side, chosen, err := f.resolveSide(0)
	if err != nil {
		return err
	}
	if !chosen {
		side = game.Vertical
	}

	invite, err := netplay.NewInvite(rs, side, player)
	if err != nil {
		return err
	}
	code, err := netplay.EncodeInvite(invite)
	if err != nil {
		return err
	}
	id, err := correspondenceID(invite.ID)
	if err != nil {
		return err
	}

	g, err := game.New(rs)
	if err != nil {
		return err
	}
	rec, err := g.Record()
	if err != nil {
		return err
	}
	saved := gamestore.Saved{
		ID:     id,
		Kind:   gamestore.Correspondence,
		Player: player,
		Side:   side.String(),
		// The invitation is open: whoever accepts it is not known here, and no
		// code carries a name, so the host never learns it.
		Opponent: unknownOpponent,
		Record:   rec.Encode(),
	}
	if err := deps.Games.Put(saved); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Started correspondence game %s. You play %s.\n", id, side)
	fmt.Fprintf(out, "Rules: %s\n\n", rs.Describe())
	fmt.Fprintln(out, "Send your opponent this invitation:")
	fmt.Fprintf(out, "\n  %s\n\n", code)
	fmt.Fprintln(out, "They accept it with: twixtui play correspondence --join <code>")
	if side == game.Vertical {
		fmt.Fprintln(out, "You move first: open the board with twixtui play correspondence")
		return nil
	}
	fmt.Fprintln(out, "They move first; apply their code with: twixtui play correspondence")
	return nil
}

// unknownOpponent stands in for a player whose name this end has no way of
// learning. It is worded to read in the sentences it appears in, such as
// "waiting for your opponent".
const unknownOpponent = "your opponent"

// joinCorrespondence accepts an invitation.
func joinCorrespondence(cmd *cobra.Command, deps app.Deps, player, code string) error {
	invite, err := netplay.DecodeInvite(code)
	if err != nil {
		return err
	}
	id, err := correspondenceID(invite.ID)
	if err != nil {
		return err
	}
	if _, err := deps.Games.Get(id); err == nil {
		return fmt.Errorf("you already have game %s; open it with: twixtui play correspondence", id)
	}

	g, err := game.New(invite.Rules)
	if err != nil {
		return err
	}
	rec, err := g.Record()
	if err != nil {
		return err
	}
	host := invite.HostName
	if host == "" {
		host = unknownOpponent
	}
	mySide := invite.GuestSide()
	saved := gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Correspondence,
		Player:   player,
		Side:     mySide.String(),
		Opponent: host,
		Record:   rec.Encode(),
	}
	if err := deps.Games.Put(saved); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Joined %s against %s. You play %s.\n", id, host, mySide)
	fmt.Fprintf(out, "Rules: %s\n\n", invite.Rules.Describe())
	if mySide == game.Vertical {
		fmt.Fprintln(out, "You move first. Opening the board so you can play.")
		return openCorrespondenceGame(cmd, deps, saved)
	}
	fmt.Fprintln(out, "Your opponent moves first; apply their code when it arrives with:")
	fmt.Fprintln(out, "  twixtui play correspondence")
	return nil
}

// openCorrespondence opens a correspondence game: the one named, else the one
// waiting for a move from this player, else the only one there is.
func openCorrespondence(cmd *cobra.Command, deps app.Deps, id string) error {
	if id != "" {
		saved, err := deps.Games.Resolve(id)
		if err != nil {
			return err
		}
		if saved.Kind != gamestore.Correspondence {
			return fmt.Errorf("game %s is a %s game, not a correspondence one", saved.ID, saved.Kind)
		}
		return openCorrespondenceGame(cmd, deps, saved)
	}

	var open []gamestore.Saved
	for _, sv := range deps.Games.OfKind(gamestore.Correspondence) {
		if !sv.Finished {
			open = append(open, sv)
		}
	}
	switch len(open) {
	case 0:
		return errors.New("no correspondence game is waiting; start one with --new or accept one with --join <code>")
	case 1:
		return openCorrespondenceGame(cmd, deps, open[0])
	}

	// With more than one game going, the one to open is the one that can be
	// played: in the others there is nothing to do but wait for a code.
	var yours []gamestore.Saved
	for _, sv := range open {
		if toMove(sv) {
			yours = append(yours, sv)
		}
	}
	if len(yours) == 1 {
		return openCorrespondenceGame(cmd, deps, yours[0])
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%d correspondence games are open:\n\n", len(open))
	for _, sv := range open {
		state := "waiting for " + sv.Opponent
		if toMove(sv) {
			state = "your move"
		}
		fmt.Fprintf(out, "  %s  %s  %s\n", sv.ID, state, sv.Describe())
	}
	fmt.Fprintln(out, "\nOpen one with: twixtui play correspondence --game <id>")
	return nil
}

// toMove reports whether a saved game is waiting for this player rather than for
// their opponent. A game whose record will not load is not one to open, so it
// answers no rather than failing the listing it appears in.
func toMove(sv gamestore.Saved) bool {
	g, err := sv.Game()
	if err != nil || g.Result().Over() {
		return false
	}
	side, err := game.ParsePlayer(sv.Side)
	if err != nil {
		return false
	}
	return g.Turn() == side
}

// openCorrespondenceGame hands a stored correspondence game to the interface,
// which is where codes are pasted in and produced.
func openCorrespondenceGame(cmd *cobra.Command, deps app.Deps, saved gamestore.Saved) error {
	g, err := saved.Game()
	if err != nil {
		return err
	}
	side, err := game.ParsePlayer(saved.Side)
	if err != nil {
		return fmt.Errorf("saved game %s records an unreadable side %q: %w", saved.ID, saved.Side, err)
	}
	cfg := app.GameConfig{
		Kind:  gamestore.Correspondence,
		Rules: g.Rules(),
		Seats: map[game.Player]app.Seat{
			side: {Profile: saved.Player, Label: saved.Player},
			// The screen saves the opponent under the name the leaderboard
			// records them by, and this game is reopened from that save every
			// turn, so the prefix has to come off again or it accumulates. It
			// is BareName rather than DisplayName because this label is fed
			// back through RemoteName on the next save: it has to round-trip,
			// not read well.
			side.Opponent(): {Remote: true, Label: leaderboard.BareName(saved.Opponent)},
		},
		Codes:   true,
		Resume:  &saved,
		StoreID: saved.ID,
	}
	return runScreens(cmd, deps, func() (app.Screen, error) {
		return app.NewGameScreen(deps, cfg)
	})
}
