package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// Correspondence play needs no connection: each side keeps the game on its own
// machine and they exchange short codes. The code carries the position it was
// made in, so one pasted into the wrong game, pasted twice, or mangled on the
// way is refused instead of corrupting the game. twixtui keeps the game; the
// code carries only the move and the checks.

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

	g, err := game.New(rs)
	if err != nil {
		return err
	}
	rec, err := g.Record()
	if err != nil {
		return err
	}
	saved := gamestore.Saved{
		ID:       invite.ID,
		Kind:     gamestore.Correspondence,
		Player:   player,
		Side:     side.String(),
		Opponent: "invited",
		Record:   rec.Encode(),
	}
	if err := deps.Games.Put(saved); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Started correspondence game %s. You play %s.\n\n", invite.ID, side)
	fmt.Fprintln(out, "Send your opponent this invitation:")
	fmt.Fprintf(out, "\n  %s\n\n", code)
	fmt.Fprintln(out, "They accept it with: twixtui play correspondence --join <code>")
	fmt.Fprintf(out, "When they reply, apply their code with: twixtui play correspondence\n")
	return nil
}

// joinCorrespondence accepts an invitation.
func joinCorrespondence(cmd *cobra.Command, deps app.Deps, player, code string) error {
	invite, err := netplay.DecodeInvite(code)
	if err != nil {
		return err
	}
	if _, err := deps.Games.Get(invite.ID); err == nil {
		return fmt.Errorf("you already have a game %s; open it with: twixtui play correspondence", invite.ID)
	}

	g, err := game.New(invite.Rules)
	if err != nil {
		return err
	}
	rec, err := g.Record()
	if err != nil {
		return err
	}
	mySide := invite.HostSide.Opponent()
	saved := gamestore.Saved{
		ID:       invite.ID,
		Kind:     gamestore.Correspondence,
		Player:   player,
		Side:     mySide.String(),
		Opponent: invite.HostName,
		Record:   rec.Encode(),
	}
	if err := deps.Games.Put(saved); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Joined %s against %s. You play %s.\n", invite.ID, invite.HostName, mySide)
	fmt.Fprintf(out, "Rules: %s\n\n", invite.Rules.Describe())
	if mySide == game.Vertical {
		fmt.Fprintln(out, "You move first. Opening the board so you can play.")
		return openCorrespondenceGame(cmd, deps, saved)
	}
	fmt.Fprintln(out, "Your opponent moves first; apply their code when it arrives with:")
	fmt.Fprintln(out, "  twixtui play correspondence")
	return nil
}

// openCorrespondence opens a correspondence game, choosing it when there is only
// one and listing them when there are several.
func openCorrespondence(cmd *cobra.Command, deps app.Deps) error {
	games := deps.Games.OfKind(gamestore.Correspondence)
	var open []gamestore.Saved
	for _, sv := range games {
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
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Several correspondence games are open:")
	for _, sv := range open {
		fmt.Fprintf(out, "  %s  %s\n", sv.ID, sv.Describe())
	}
	fmt.Fprintln(out, "\nOpen one with: twixtui game replay <id>, or apply a code from the interface.")
	return nil
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
			side:            {Profile: saved.Player, Label: saved.Player},
			side.Opponent(): {Remote: true, Label: saved.Opponent},
		},
		Resume:  &saved,
		StoreID: saved.ID,
	}
	return runScreens(cmd, deps, func() (app.Screen, error) {
		return app.NewGameScreen(deps, cfg)
	})
}
