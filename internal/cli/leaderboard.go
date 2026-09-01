package cli

import (
	"fmt"
	"strconv"
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
leave stale numbers behind. Only people are ranked. The bots are listed under
them because each tier plays at a rating fixed by the program, which it can
neither win nor lose: those fixed ratings are what make beating the pro count
for more than beating the beginner.`,
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
					// Stored in UTC, which is what keeps two machines' logs
					// comparable, but read here by one person on one machine:
					// shown in the same local time as the saved-game picker
					// and "game list". Rendering the stored value as it stands
					// dates every game an offset away from when the player
					// remembers playing it, with nothing on the row to say so.
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
						r.Played.Local().Format("2006-01-02 15:04"),
						leaderboard.DisplayName(r.Opponent),
						r.Side, r.Outcome, r.Moves)
				}
				return w.Flush()
			}
			standings := board.Standings()
			players, bots := standings.Players, standings.Bots
			if len(players) == 0 && len(bots) == 0 {
				_, err := fmt.Fprintln(out, "no games recorded yet; play one with: twixtui play bot")
				return err
			}
			// A position needs somebody to hold it against, so a board with one
			// player does not claim they are first. It is the whole board that
			// decides that, not the part --limit prints: the top of a longer
			// ranking is still a ranking.
			ranked := len(players) > 1
			// The limit is the top of the ranking. The bots below it are one
			// row per tier played, a fixed reference rather than a list that
			// grows, so there is nothing there worth cutting off.
			if limit > 0 && len(players) > limit {
				players = players[:limit]
			}

			const columns = "RATING\tPLAYED\tWON\tLOST\tDREW\tSCORE"
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			head := "PLAYER\t" + columns
			if ranked {
				head = "#\t" + head
			}
			fmt.Fprintln(w, head)
			for i, s := range players {
				pos := ""
				if ranked {
					pos = strconv.Itoa(i+1) + "\t"
				}
				fmt.Fprintf(w, "%s%s\t%d\t%d\t%d\t%d\t%d\t%.0f%%\n",
					pos, leaderboard.DisplayName(s.Name), s.Rating,
					s.Played, s.Won, s.Lost, s.Drawn, s.WinRate*100)
			}
			if len(bots) > 0 {
				// A line with no tab ends the run of columns, so the two tables
				// are measured apart by the one writer.
				fmt.Fprint(w, "\nBots are not ranked: a tier's rating is fixed, not earned.\n\n")
				fmt.Fprintln(w, "BOT\t"+columns)
				for _, s := range bots {
					fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%.0f%%\n",
						leaderboard.DisplayName(s.Name), s.Rating,
						s.Played, s.Won, s.Lost, s.Drawn, s.WinRate*100)
				}
			}
			return w.Flush()
		},
	}
	show.Flags().IntVar(&limit, "limit", 0, "show at most this many players (0 means all)")
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
