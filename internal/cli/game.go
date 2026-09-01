package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/humantime"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/learn"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/ui"
)

// openGames opens the saved-game store for the resolved configuration directory.
func (o *options) openGames() (*gamestore.Store, error) {
	dir, err := o.configPath()
	if err != nil {
		return nil, err
	}
	return gamestore.Open(dir)
}

// gameIDCompletions completes a saved game's identifier, described so the player
// can tell which is which without looking them up.
func (o *options) gameIDCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	store, err := o.openGames()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	saved := store.List()
	out := make([]cobra.Completion, 0, len(saved))
	for _, sv := range saved {
		out = append(out, cobra.CompletionWithDesc(sv.ID, describeSaved(sv)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// describeSaved is the player-facing summary of a stored game.
//
// gamestore holds the recorded opponent name in its encoded form, which is what
// makes a game's identity stable, and it must not learn about scoring in order
// to render it. So the rendering happens here, in the one place that prints these
// for a reader, and every such place goes through it: otherwise "bot:beginner"
// leaks into a listing beside the same bot spelled properly elsewhere.
func describeSaved(sv gamestore.Saved) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s vs %s", sv.Player, leaderboard.DisplayName(sv.Opponent))
	if sv.Side != "" {
		fmt.Fprintf(&b, " (%s)", sv.Side)
	}
	if sv.Finished {
		b.WriteString(", finished")
	} else {
		b.WriteString(", in progress")
	}
	return b.String()
}

func newGameCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "game",
		Short: "Work with saved games",
		Long: `Work with saved games.

Games are saved as they are played, so an interrupted one can be resumed and a
finished one can be replayed. A saved game carries its own integrity check: a
file that has been edited is refused rather than loaded as a different game.`,
	}

	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List saved games, most recent first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			saved := store.List()
			if len(saved) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(),
					"no saved games yet; play one with: twixtui play bot --side random")
				return err
			}
			if limit > 0 && len(saved) > limit {
				saved = saved[:limit]
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tKIND\tPLAYERS\tSTATE\tUPDATED")
			for _, sv := range saved {
				state := "in progress"
				if sv.Finished {
					state = "finished"
				}
				fmt.Fprintf(w, "%s\t%s\t%s vs %s\t%s\t%s\n",
					sv.ID, sv.Kind, sv.Player, leaderboard.DisplayName(sv.Opponent), state, humantime.Since(sv.Updated))
			}
			return w.Flush()
		},
	}
	list.Flags().IntVar(&limit, "limit", 0, "show at most this many games (0 means all)")

	show := &cobra.Command{
		Use:               "show <id>",
		Short:             "Show a saved game's board and move list",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: opts.gameIDCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			sv, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			g, err := sv.Game()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s: %s\n", sv.ID, describeSaved(sv))
			fmt.Fprintf(out, "rules: %s\n", g.Rules().Describe())
			fmt.Fprintf(out, "moves: %d\n", g.Ply())
			fmt.Fprintf(out, "result: %s\n\n", describeResult(g.Result()))

			board, err := opts.renderBoard(g)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, board)

			transcript, err := g.Transcript()
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "\nthe moves in twixtui's notation, explained by: twixtui rules show notation")
			fmt.Fprintln(out, transcript)
			return nil
		},
	}

	replay := &cobra.Command{
		Use:   "replay <id>",
		Short: "Step through a saved game move by move",
		Long: `Step through a saved game move by move.

Opens the board and walks forwards and backwards through the game with the same
keys used to play it.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: opts.gameIDCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			sv, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			deps, _, err := opts.deps()
			if err != nil {
				return err
			}
			return runScreens(cmd, deps, func() (app.Screen, error) {
				return app.NewReplayScreen(deps, sv)
			})
		},
	}

	var outPath string
	export := &cobra.Command{
		Use:   "export <id>",
		Short: "Write a saved game out as a record",
		Long: `Write a saved game out as a record.

The record holds the ruleset, the moves, the result and two digests, so whoever
receives it can check it arrived intact and replays to the game it claims to be.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: opts.gameIDCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			sv, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			if outPath == "" || outPath == "-" {
				_, err := fmt.Fprint(cmd.OutOrStdout(), sv.Record)
				return err
			}
			if err := os.WriteFile(outPath, []byte(sv.Record), 0o644); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s to %s\n", sv.ID, outPath)
			return err
		},
	}
	export.Flags().StringVar(&outPath, "out", "", "write to this file instead of standard output")

	importCmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Read a game record in, checking it as it goes",
		Long: `Read a game record in, checking it as it goes.

A record that has been altered or truncated is refused, naming what went wrong,
rather than being loaded as a different game. Use - to read standard input.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var body []byte
			var err error
			if args[0] == "-" {
				body, err = io.ReadAll(cmd.InOrStdin())
			} else {
				body, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			g, _, err := game.LoadRecord(string(body))
			if err != nil {
				return err
			}
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			_, player, err := opts.requireProfile()
			if err != nil {
				return err
			}
			sv := gamestore.Saved{
				ID:       gamestore.NewID(),
				Kind:     gamestore.Hotseat,
				Player:   player,
				Opponent: "imported",
				Record:   string(body),
				Finished: g.Result().Over(),
			}
			if err := store.Put(sv); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"imported as %s: %d moves, %s\n", sv.ID, g.Ply(), describeResult(g.Result()))
			return err
		},
	}

	del := &cobra.Command{
		Use:               "delete <id>",
		Short:             "Delete a saved game",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: opts.gameIDCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			sv, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			if err := store.Delete(sv.ID); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", sv.ID)
			return err
		},
	}

	cmd.AddCommand(list, show, replay, export, importCmd, del)
	return cmd
}

// describeResult renders a result in words.
func describeResult(r game.Result) string {
	if !r.Over() {
		return "still being played"
	}
	reason := map[game.Reason]string{
		game.Connection:  "by completing a chain",
		game.NoMovesLeft: "with no legal moves left",
		game.Resignation: "by resignation",
		game.Agreement:   "by agreement",
	}[r.Reason]
	switch r.Outcome {
	case game.Draw:
		return "drawn " + reason
	case game.VerticalWins:
		return "vertical won " + reason
	case game.HorizontalWins:
		return "horizontal won " + reason
	}
	return "unknown"
}

func newServeCommand(opts *options) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a relay so two players behind firewalls can pair up",
		Long: `Run a relay so two players behind firewalls can pair up.

Direct play needs one side to accept an incoming connection, which a home router
or a company network often prevents. A relay is somewhere both sides can reach:
each connects out to it and it passes bytes between them. It never parses the
game, keeps nothing on disk, and is told only the first group of the pairing
code, so it cannot alter, inject, replay or drop a move without being caught:
both players authenticate every frame with a key derived from the rest of the
code, which the relay never sees.

It does read what it carries, in plain text — both names, the ruleset and every
move. Run one for people who are content for you to see their games.

  twixtui serve --addr :4271
  twixtui play host --relay relay.example:4271
  twixtui play join --relay relay.example:4271 <pairing code>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "relay listening on %s; press ctrl+c to stop\n", addr)
			return netplay.Serve(cmd.Context(), addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":4271", "address to listen on")
	return cmd
}

func newLearnCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "learn [lesson]",
		Aliases: []string{"tutorial"},
		Short:   "Learn the game interactively",
		Long: `Learn the game interactively.

A guided tour of the board, the knight's-move link, the crossing rule that
catches every newcomer, blocking, the double threat, and how a game is won. Each
lesson sets up real positions and asks you to find the move, using the same keys
you play with.

Given a lesson name, start there; otherwise choose from the list.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: lessonCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, _, err := opts.deps()
			if err != nil {
				return err
			}
			lesson := ""
			if len(args) == 1 {
				lesson = args[0]
			}
			return runScreens(cmd, deps, func() (app.Screen, error) {
				return app.NewTutorialScreen(deps, lesson)
			})
		},
	}
	return cmd
}

// renderBoard draws a board once for a non-interactive listing. It is rendered
// at the size the whole board needs rather than the size of the terminal: a
// listing is read by scrolling back, so a board cut off at the bottom with a
// scroll marker in it — which is what a viewport does — loses rows for nothing.
// The drawing scale still follows the terminal's width, since that is what
// decides whether the wide scale can be read without folding.
func (o *options) renderBoard(g *game.Game) (string, error) {
	th, err := o.theme()
	if err != nil {
		return "", err
	}
	styles := ui.StylesFor(th)
	width, _ := terminalSize()
	scale := ui.Detail
	blockW, blockH := scale.BlockSize(g.Size())
	if blockW > width {
		scale = ui.Compact
		blockW, blockH = scale.BlockSize(g.Size())
	}
	view := &ui.BoardView{Scale: scale}
	return strings.Join(view.Render(g, &styles, blockW, blockH), "\n"), nil
}

// lessonCompletions completes a tutorial lesson name with its summary.
func lessonCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	lessons := learn.Lessons()
	out := make([]cobra.Completion, 0, len(lessons))
	for _, l := range lessons {
		out = append(out, cobra.CompletionWithDesc(l.ID, l.Title))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
