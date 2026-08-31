package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/leaderboard"
)

// openBoard opens the result log for the resolved configuration directory.
func (o *options) openBoard() (*leaderboard.Board, error) {
	dir, err := o.configPath()
	if err != nil {
		return nil, err
	}
	return leaderboard.Open(dir)
}

func newLeaderboardCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "leaderboard",
		Aliases: []string{"board"},
		Short:   "See who is winning",
		Long: `See who is winning.

Every finished game is recorded once and ratings are replayed from that log, so
the log is the only stored fact and changing how ratings are worked out cannot
leave stale numbers behind. Bots appear alongside people, each tier with its own
fixed rating, so beating the strongest bot counts for more than beating the
weakest.`,
	}

	var limit int
	var player string
	show := &cobra.Command{
		Use:   "show",
		Short: "Show the standings, or one player's recent games",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			board, err := opts.openBoard()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if player != "" {
				store, err := opts.openProfiles()
				if err != nil {
					return err
				}
				name, err := resolveProfileName(store, player)
				if err != nil {
					return err
				}
				history := board.History(name, limit)
				if len(history) == 0 {
					_, err := fmt.Fprintf(out, "%s has no recorded games yet\n", name)
					return err
				}
				w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "WHEN\tOPPONENT\tSIDE\tRESULT\tMOVES")
				for _, r := range history {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
						r.Played.Format("2006-01-02 15:04"),
						leaderboard.DisplayName(r.Opponent),
						r.Side, r.Outcome, r.Moves)
				}
				return w.Flush()
			}

			standings := board.Standings()
			if len(standings) == 0 {
				_, err := fmt.Fprintln(out, "no games recorded yet; play one with: twixtui play bot")
				return err
			}
			if limit > 0 && len(standings) > limit {
				standings = standings[:limit]
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "#\tPLAYER\tRATING\tPLAYED\tWON\tLOST\tDREW\tSCORE")
			for i, s := range standings {
				fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%d\t%d\t%d\t%.0f%%\n",
					i+1, leaderboard.DisplayName(s.Name), s.Rating,
					s.Played, s.Won, s.Lost, s.Drawn, s.WinRate*100)
			}
			return w.Flush()
		},
	}
	show.Flags().IntVar(&limit, "limit", 0, "show at most this many rows (0 means all)")
	show.Flags().StringVar(&player, "player", "", "show this player's recent games instead of the standings")
	registerFlagCompletion(show, "player", opts.profileCompletions)

	var confirm bool
	reset := &cobra.Command{
		Use:   "reset",
		Short: "Delete every recorded game",
		Long: `Delete every recorded game.

This throws away the whole result log, which is where ratings come from, so it
cannot be undone. It needs --yes so that a mistyped command cannot do it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			board, err := opts.openBoard()
			if err != nil {
				return err
			}
			if !confirm {
				return fmt.Errorf("this deletes every recorded game and cannot be undone; pass --yes to do it")
			}
			if err := board.Reset(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "the leaderboard is empty")
			return err
		},
	}
	reset.Flags().BoolVar(&confirm, "yes", false, "confirm that the whole result log should be deleted")

	cmd.AddCommand(show, reset)
	return cmd
}
