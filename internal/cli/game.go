package cli

import (
	"fmt"
	"os"
	"strings"

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
finished one can be replayed. The game itself is stored as a record — the rules,
the moves, the result and the final position, with digests over them — so a
record that has been edited or truncated is refused rather than loaded as a
different game.

What each saved game is called is a separate matter: which profile played, which
side, what the opponent was named and when it happened are notes this machine
wrote about its own games, they are yours to edit, and no digest covers them.`,
	}

	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List saved games, most recent first",
		Args:  noArgs,
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
			rows := make([][]string, 0, len(saved)+1)
			rows = append(rows, []string{"ID", "KIND", "PLAYERS", "STATE", "UPDATED"})
			for _, sv := range saved {
				state := "in progress"
				if sv.Finished {
					state = "finished"
				}
				rows = append(rows, []string{
					sv.ID,
					string(sv.Kind),
					fmt.Sprintf("%s vs %s", sv.Player, leaderboard.DisplayName(sv.Opponent)),
					state,
					humantime.Since(sv.Updated),
				})
			}
			return ui.WriteTable(cmd.OutOrStdout(), rows, 2)
		},
	}
	list.Flags().IntVar(&limit, "limit", 0, "show at most this many games (0 means all)")

	show := &cobra.Command{
		Use:               "show <id>",
		Short:             "Show a saved game's board and move list",
		Args:              exactArgs(1),
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
		Args:              exactArgs(1),
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
			return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
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
receives it can check it arrived intact and replays to the game it claims to be.

The saved game is loaded and replayed before anything is written, and the record
it hands out is checked against the size a record may have, so a record this
build would refuse to read is refused here too. Both checks happen before the
file named by --out is opened, so a refusal leaves it as it was; a failure of
the write itself is a different matter, and may leave the file part-written.`,
		Args:              exactArgs(1),
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
			// Sending on a record nobody checked is how a corrupted game
			// leaves this machine looking like a game: the receiver refuses it,
			// and the refusal arrives with them rather than here, where the
			// damage is. So it is loaded first, and what goes out is the
			// checked record's own encoding.
			_, rec, err := sv.Load()
			if err != nil {
				return err
			}
			record, err := rec.EncodeCanonical()
			if err != nil {
				return err
			}
			if outPath == "" || outPath == "-" {
				_, err := fmt.Fprint(cmd.OutOrStdout(), record)
				return err
			}
			if err := os.WriteFile(outPath, []byte(record), 0o644); err != nil {
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
rather than being loaded as a different game. So is a file holding more than one
record, or a field written twice: a record is one game, and reading such a file
as whichever record came last would keep bytes nothing checked. Use - to read
standard input.

A record names no players and says nothing about how it was played, so an
imported game is listed by its two sides rather than under your profile. A
record already held here is recognised and named rather than saved twice.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The input is bounded as it is read rather than after: this is a
			// file or a pipe somebody else wrote, and a record has a size a
			// record can be. game.ReadRecord stops past that size, so naming a
			// device or a multi-gigabyte file costs a megabyte and a refusal.
			g, rec, err := readRecordFrom(cmd, args[0])
			if err != nil {
				return err
			}
			store, err := opts.openGames()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// One record read in twice is one game, not two. Recognising the
			// repeat rather than refusing it leaves the command idempotent,
			// which is what a script that re-runs an import wants, and naming
			// the game that already holds it is more use than a refusal.
			if had, ok := savedWithDigest(store, rec.Digest); ok {
				_, err = fmt.Fprintf(out, "already saved as %s: %d moves, %s\n",
					had.ID, g.Ply(), describeResult(g.Result()))
				return err
			}
			// A record holds a ruleset, the moves and the result, and no names
			// at all. Filing it under the importing profile as a hotseat game
			// asserts two things the file does not say: that this machine's
			// player was in it, and that it was played here. The two sides are
			// the only participants the record does name, so they are who the
			// listing shows, and the kind says where the game came from, which
			// is what lets a list of games to carry on with tell an imported
			// record from one of this machine's own. The importing profile is
			// not consulted at all: it has nothing to do with this game.
			//
			// What is stored is the record as this build encodes it, not the
			// bytes that arrived: those may carry anything the digests do not
			// cover, and once stored they would be handed back out by export.
			// That encoding is checked before the game is stored, because the
			// reader takes spellings the canonical encoding does not: a record
			// accepted here can canonicalise past the size a record may have,
			// and what could not be read again is not imported.
			record, err := rec.EncodeCanonical()
			if err != nil {
				return err
			}
			sv := gamestore.Saved{
				ID:       gamestore.NewID(),
				Kind:     gamestore.Imported,
				Player:   game.Vertical.String(),
				Opponent: game.Horizontal.String(),
				Record:   record,
				Finished: g.Result().Over(),
			}
			if err := store.Put(sv); err != nil {
				return err
			}
			_, err = fmt.Fprintf(out,
				"imported as %s: %d moves, %s\n", sv.ID, g.Ply(), describeResult(g.Result()))
			return err
		},
	}

	del := &cobra.Command{
		Use:               "delete <id>",
		Short:             "Delete a saved game",
		Args:              exactArgs(1),
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

// readRecordFrom reads one record from a named file, or from standard input
// when the name is -.
//
// The file is handed to game.ReadRecord rather than read whole. How large the
// thing on the other end is, is not this program's choice: a path can name a
// device that never ends, and a pipe can carry as much as the writer likes. A
// bounded read refuses those for what they are, having held a record's worth of
// them, instead of growing until something else on the machine gives way.
func readRecordFrom(cmd *cobra.Command, name string) (*game.Game, game.Record, error) {
	if name == "-" {
		return game.ReadRecord(cmd.InOrStdin())
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, game.Record{}, err
	}
	defer f.Close()
	return game.ReadRecord(f)
}

// savedWithDigest finds the stored game holding a given record, if there is one.
//
// Records are compared by the digest they carry rather than by their bytes: the
// digest covers the ruleset, the moves, the result and the final position, so
// two records with the same one are the same game however the file was
// line-wrapped or re-encoded on the way here. A stored record that no longer
// loads is skipped, as the listing skips it, rather than failing the import of
// an unrelated game.
func savedWithDigest(store *gamestore.Store, digest string) (gamestore.Saved, bool) {
	if digest == "" {
		return gamestore.Saved{}, false
	}
	for _, sv := range store.List() {
		rec, err := game.DecodeRecord(sv.Record)
		if err != nil {
			continue
		}
		if rec.Digest == digest {
			return sv, true
		}
	}
	return gamestore.Saved{}, false
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
code. Both players authenticate every frame with a key derived from the rest of
the code, which the relay never sees, so it cannot alter, inject or replay a
move without being caught. What it can still do is fail to deliver one.

It does read what it carries, in plain text — both names, the ruleset and every
move. Run one for people who are content for you to see their games.

  twixtui serve --addr :4271
  twixtui play host --relay relay.example:4271
  twixtui play join --relay relay.example:4271 <pairing code>`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Announce the relay once it is listening, not before. This line is
			// what an operator, or a readiness probe watching the log for it,
			// reads to know the relay is up; printed ahead of the bind it
			// asserts a relay that a taken port or an unresolvable host is
			// about to prevent from existing. The address printed is the one
			// actually bound, so a bare port and a port of 0 both name what a
			// client should connect to.
			l, err := netplay.BindRelay(addr)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "relay listening on %s; press ctrl+c to stop\n", l.Addr())
			return netplay.ServeOn(cmd.Context(), l)
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
		Args:              maxArgs(1),
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
			return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
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
