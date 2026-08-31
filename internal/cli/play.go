package cli

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/ui"
)

// gameFlags are the settings common to every way of starting a game.
type gameFlags struct {
	ruleset string
	size    int
	side    string
	tier    string
	seed    int64
	hints   bool
	port    int
	relay   string
	addr    string
	newGame bool
	join    string
}

func (f *gameFlags) addRuleFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.ruleset, "ruleset", "std",
		"which historical ruleset to play: "+strings.Join(game.PresetNames(), ", "))
	cmd.Flags().IntVar(&f.size, "size", 0,
		fmt.Sprintf("board side length in holes (%d to %d, default the ruleset's own)", game.MinSize, game.MaxSize))
	registerFlagCompletion(cmd, "ruleset", rulesetCompletions)
}

func (f *gameFlags) addSideFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.side, "side", "",
		"which side to play: vertical joins top and bottom, horizontal joins left and right, random picks for you")
	registerFlagCompletion(cmd, "side", sideCompletions)
}

// rules builds the ruleset from the flags.
func (f *gameFlags) rules() (game.Ruleset, error) {
	rs, err := game.Preset(f.ruleset)
	if err != nil {
		return game.Ruleset{}, err
	}
	if f.size != 0 {
		rs.Size = f.size
	}
	if err := rs.Validate(); err != nil {
		return game.Ruleset{}, err
	}
	return rs, nil
}

// resolveSide turns the flag into a concrete side, asking nobody: an empty value
// means the interface will ask, which the caller handles.
func (f *gameFlags) resolveSide(seed int64) (game.Player, bool, error) {
	switch strings.ToLower(strings.TrimSpace(f.side)) {
	case "":
		return game.NoPlayer, false, nil
	case "random", "r":
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(time.Now().UnixNano())))
		if rng.IntN(2) == 0 {
			return game.Vertical, true, nil
		}
		return game.Horizontal, true, nil
	}
	pl, err := game.ParsePlayer(strings.ToLower(strings.TrimSpace(f.side)))
	if err != nil {
		return game.NoPlayer, false, err
	}
	return pl, true, nil
}

func rulesetCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	names := game.PresetNames()
	out := make([]cobra.Completion, 0, len(names))
	for _, n := range names {
		out = append(out, cobra.CompletionWithDesc(n, game.PresetSummary(n)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func sideCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{
		cobra.CompletionWithDesc("vertical", "join the top and bottom border rows; moves first"),
		cobra.CompletionWithDesc("horizontal", "join the left and right border columns"),
		cobra.CompletionWithDesc("random", "let twixtui choose"),
	}, cobra.ShellCompDirectiveNoFileComp
}

func tierCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	names := bot.TierNames()
	out := make([]cobra.Completion, 0, len(names))
	for _, n := range names {
		out = append(out, cobra.CompletionWithDesc(n, bot.TierSummary(n)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func newPlayCommand(opts *options) *cobra.Command {
	play := &cobra.Command{
		Use:   "play",
		Short: "Start a game",
		Long: `Start a game.

Against the built-in opponent, against someone at this keyboard, or against
someone on another machine.`,
	}
	play.AddCommand(
		newPlayBotCommand(opts),
		newPlayLocalCommand(opts),
		newPlayHostCommand(opts),
		newPlayJoinCommand(opts),
		newPlayCorrespondenceCommand(opts),
	)
	return play
}

func newPlayBotCommand(opts *options) *cobra.Command {
	var f gameFlags
	cmd := &cobra.Command{
		Use:   "bot",
		Short: "Play against the built-in opponent",
		Long: `Play against the built-in opponent.

The three tiers are genuinely different opponents, not the same one slowed down.
Ask for advice at any time on your turn with ? and the reason will be explained
in the terms the search actually measured.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs, err := f.rules()
			if err != nil {
				return err
			}
			tier, err := bot.ParseTier(f.tier)
			if err != nil {
				return err
			}
			seed := f.seed
			if seed == 0 {
				seed = time.Now().UnixNano()
			}
			side, chosen, err := f.resolveSide(seed)
			if err != nil {
				return err
			}

			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			if !chosen {
				// R7 says the player picks; ask rather than assume.
				return fmt.Errorf("choose a side with --side vertical, --side horizontal or --side random")
			}

			opponent := bot.New(tier, seed)
			cfg := app.GameConfig{
				Kind:  gamestore.VersusBot,
				Rules: rs,
				Seats: map[game.Player]app.Seat{
					side:            {Profile: player, Label: player},
					side.Opponent(): {Bot: opponent, Label: "bot: " + tier.String()},
				},
				Hints:   f.hints,
				HintFor: opponent,
			}
			return runScreens(cmd, deps, func() (app.Screen, error) {
				return app.NewGameScreen(deps, cfg)
			})
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().StringVar(&f.tier, "tier", "intermediate",
		"how hard the opponent plays: "+strings.Join(bot.TierNames(), ", "))
	cmd.Flags().Int64Var(&f.seed, "seed", 0,
		"fix the opponent's randomness so a game can be replayed exactly")
	cmd.Flags().BoolVar(&f.hints, "hints", true,
		"allow ? to ask for advice on your turn")
	registerFlagCompletion(cmd, "tier", tierCompletions)
	return cmd
}

func newPlayLocalCommand(opts *options) *cobra.Command {
	var f gameFlags
	var second string
	cmd := &cobra.Command{
		Use:     "local",
		Aliases: []string{"hotseat"},
		Short:   "Play against someone at this keyboard",
		Long: `Play against someone at this keyboard.

Both players use the same terminal and take turns. The interface always says
whose turn it is, and each player's own border rows are marked.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs, err := f.rules()
			if err != nil {
				return err
			}
			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			side, chosen, err := f.resolveSide(time.Now().UnixNano())
			if err != nil {
				return err
			}
			if !chosen {
				side = game.Vertical
			}
			other := second
			if other == "" {
				other = "Guest"
			} else {
				name, err := resolveProfileName(deps.Profiles, other)
				if err != nil {
					return err
				}
				other = name
			}
			cfg := app.GameConfig{
				Kind:  gamestore.Hotseat,
				Rules: rs,
				Seats: map[game.Player]app.Seat{
					side:            {Profile: player, Label: player},
					side.Opponent(): {Profile: other, Label: other},
				},
			}
			return runScreens(cmd, deps, func() (app.Screen, error) {
				return app.NewGameScreen(deps, cfg)
			})
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().StringVar(&second, "opponent", "",
		"the other player's profile, so the result counts for both of you")
	registerFlagCompletion(cmd, "opponent", opts.profileCompletions)
	return cmd
}

func newPlayHostCommand(opts *options) *cobra.Command {
	var f gameFlags
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Wait for an opponent to connect",
		Long: `Wait for an opponent to connect.

By default twixtui listens for a direct connection, which works on a local
network, over a VPN or tailnet, or with a forwarded port. If neither of you can
accept an incoming connection, both use --relay with the address of a relay one
of you runs with "twixtui serve"; the relay only passes bytes along and never
sees the game.

The address to share is printed before the wait begins.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs, err := f.rules()
			if err != nil {
				return err
			}
			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			side, chosen, err := f.resolveSide(time.Now().UnixNano())
			if err != nil {
				return err
			}
			if !chosen {
				side = game.Vertical
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			out := cmd.OutOrStdout()
			hostOpts := netplay.HostOptions{Name: player, Rules: rs, Side: side}

			var session netplay.Session
			if f.relay != "" {
				code := netplay.PairingCode()
				fmt.Fprintf(out, "Pairing code: %s\n", code)
				fmt.Fprintf(out, "Your opponent runs: twixtui play join --relay %s %s\n\n", f.relay, code)
				fmt.Fprintln(out, "Waiting for them to join. Press ctrl+c to give up.")
				session, err = netplay.HostViaRelay(ctx, f.relay, code, hostOpts)
			} else {
				listener, bindErr := netplay.Bind(fmt.Sprintf(":%d", f.port))
				if bindErr != nil {
					return bindErr
				}
				fmt.Fprintf(out, "Listening on %s\n", listener.Addr())
				fmt.Fprintf(out, "Your opponent runs: twixtui play join <your address>:%s\n\n", portOf(listener.Addr()))
				fmt.Fprintln(out, "Waiting for them to connect. Press ctrl+c to give up.")
				session, err = listener.Wait(ctx, hostOpts)
			}
			if err != nil {
				return err
			}
			defer session.Close()

			return runRemoteGame(cmd, deps, player, session)
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().IntVar(&f.port, "port", 4270, "port to listen on; 0 picks a free one and prints it")
	cmd.Flags().StringVar(&f.relay, "relay", "",
		"pair through a relay at this address instead of listening directly")
	return cmd
}

func newPlayJoinCommand(opts *options) *cobra.Command {
	var f gameFlags
	cmd := &cobra.Command{
		Use:   "join <address or pairing code>",
		Short: "Connect to an opponent who is waiting",
		Long: `Connect to an opponent who is waiting.

Give the address they printed, or, with --relay, the pairing code they printed.
The side you play is whichever one they did not take, and twixtui tells you which
it is before the first move. A ruleset mismatch is refused before the game
starts rather than going wrong later.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			guestOpts := netplay.GuestOptions{Name: player}
			var session netplay.Session
			if f.relay != "" {
				session, err = netplay.JoinViaRelay(ctx, f.relay, args[0], guestOpts)
			} else {
				session, err = netplay.Dial(ctx, netplay.NormalizeAddr(args[0]), guestOpts)
			}
			if err != nil {
				return err
			}
			defer session.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s. You play %s.\n",
				session.OpponentName(), session.Side())
			return runRemoteGame(cmd, deps, player, session)
		},
	}
	cmd.Flags().StringVar(&f.relay, "relay", "",
		"join through a relay at this address, using a pairing code instead of an address")
	return cmd
}

// runRemoteGame builds the game screen for an established session.
func runRemoteGame(cmd *cobra.Command, deps app.Deps, player string, session netplay.Session) error {
	side := session.Side()
	cfg := app.GameConfig{
		Kind:  gamestore.Remote,
		Rules: session.Rules(),
		Seats: map[game.Player]app.Seat{
			side:            {Profile: player, Label: player},
			side.Opponent(): {Remote: true, Label: session.OpponentName()},
		},
		Session: session,
	}
	return runScreens(cmd, deps, func() (app.Screen, error) {
		return app.NewGameScreen(deps, cfg)
	})
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

func newPlayCorrespondenceCommand(opts *options) *cobra.Command {
	var f gameFlags
	cmd := &cobra.Command{
		Use:     "correspondence",
		Aliases: []string{"mail"},
		Short:   "Play by exchanging codes, with no connection at all",
		Long: `Play by exchanging codes, with no connection at all.

Neither player needs a reachable address, a relay, or to be online at the same
time. Each move produces a short code; send it however you like, and your
opponent pastes it in. A code carries the position it was made in, so a code
pasted into the wrong game, or pasted twice, or mangled on the way, is refused
rather than corrupting the game.

  twixtui play correspondence --new            start a game and print an invitation
  twixtui play correspondence --join CODE      accept an invitation
  twixtui play correspondence                  open a game that is waiting for you`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			switch {
			case f.newGame && f.join != "":
				return errors.New("choose either --new or --join, not both")
			case f.newGame:
				return startCorrespondence(cmd, deps, player, &f)
			case f.join != "":
				return joinCorrespondence(cmd, deps, player, f.join)
			}
			return openCorrespondence(cmd, deps)
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().BoolVar(&f.newGame, "new", false, "start a game and print an invitation to send")
	cmd.Flags().StringVar(&f.join, "join", "", "accept an invitation code")
	return cmd
}

// errFirstRunNeedsProfile is reported when a game is asked for before anyone has
// said who is playing.
var errFirstRunNeedsProfile = errors.New(
	"nobody is playing yet: run twixtui to choose a profile, or pass --profile <name>")

// deps assembles the shared collaborators the screens need.
func (o *options) deps() (app.Deps, string, error) {
	dir, err := o.configPath()
	if err != nil {
		return app.Deps{}, "", err
	}
	store, player, err := o.requireProfile()
	if err != nil {
		return app.Deps{}, "", err
	}
	board, err := o.openBoard()
	if err != nil {
		return app.Deps{}, "", err
	}
	games, err := gamestore.Open(dir)
	if err != nil {
		return app.Deps{}, "", err
	}
	th, err := o.theme()
	if err != nil {
		return app.Deps{}, "", err
	}
	styles := ui.StylesFor(th)
	return app.Deps{
		ConfigDir: dir,
		Profiles:  store,
		Board:     board,
		Games:     games,
		Theme:     th,
		Styles:    &styles,
		Keymap:    ui.DefaultKeymap(),
	}, player, nil
}

// runScreens runs the interactive interface with the given first screen.
func runScreens(cmd *cobra.Command, deps app.Deps, first func() (app.Screen, error)) error {
	screen, err := first()
	if err != nil {
		return err
	}
	shell := app.NewShell(deps, screen)
	program := tea.NewProgram(shell, tea.WithContext(cmd.Context()))
	_, err = program.Run()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
