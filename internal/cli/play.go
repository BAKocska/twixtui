package cli

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
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
	// bind is which of this machine's addresses a direct host accepts the
	// connection on, separate from the port it accepts it at.
	bind    string
	relay   string
	addr    string
	newGame bool
	join    string
	// resume names the saved network game to continue instead of starting a
	// new one.
	resume string
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
	want := strings.ToLower(strings.TrimSpace(f.side))
	switch want {
	case "":
		return game.NoPlayer, false, nil
	case "random", "r":
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(time.Now().UnixNano())))
		if rng.IntN(2) == 0 {
			return game.Vertical, true, nil
		}
		return game.Horizontal, true, nil
	}
	pl, err := game.ParsePlayer(want)
	if err != nil {
		// The engine's own refusal names the two sides it parses, which is one
		// short of what this flag takes: random is resolved above and never
		// reaches it, so a player told the accepted values by the parser would
		// be told the flag refuses a value it documents and accepts.
		return game.NoPlayer, false, fmt.Errorf("%q is not a side: choose vertical, horizontal or random", want)
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
		Long: fmt.Sprintf(`Play against the built-in opponent.

The four tiers are genuinely different opponents, not the same one slowed down:
each is allowed a different depth, a different budget and a different amount of
the evaluation. A tier names the effort it may spend, not the depth it reaches
or the move it finds; max is the largest effort on offer rather than a promise
of the best play. Before staging a move, ask for advice on your turn with ?;
the reason is explained in the terms the search actually measured.

Both the opponent and that advice search under one restriction, and it is the
restriction the advice names on screen: %s. The
search places one peg a turn and keeps the links that placement offers; it
never joins two pegs already down, takes one of its own links back or takes the
swap, and it looks at a shortlist of holes rather than at every continuation.
So "no route left" or "the only answer" is what that reading found, not an
exhaustive result. Where the rules allow link edits (std and classic), an
unsearched turn may change those routes.`, bot.PlacementOnlyPolicy()),
		Args: noArgs,
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
			if !chosen {
				// R7 says the player picks; ask rather than assume.
				return fmt.Errorf("choose a side with --side vertical, --side horizontal or --side random")
			}

			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}

			opponent := bot.New(tier, seed)
			cfg := app.GameConfig{
				Kind:  gamestore.VersusBot,
				Rules: rs,
				Seats: map[game.Player]app.Seat{
					side: {Profile: player, Label: player},
					// The label goes through the same pair of functions the
					// recorded name does, so what a player reads here cannot
					// drift from what appears on the leaderboard.
					side.Opponent(): {Bot: opponent, Label: leaderboard.DisplayName(leaderboard.BotName(tier.String()))},
				},
				Hints:   f.hints,
				HintFor: opponent,
			}
			return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
				return app.NewGameScreen(deps, cfg)
			})
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().StringVar(&f.tier, "tier", "intermediate",
		"how hard the opponent plays: "+strings.Join(bot.TierNames(), ", "))
	cmd.Flags().Int64Var(&f.seed, "seed", 0,
		"fix random choices; clock-limited searches can still stop at different depths")
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
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs, err := f.rules()
			if err != nil {
				return err
			}
			side, chosen, err := f.resolveSide(time.Now().UnixNano())
			if err != nil {
				return err
			}
			if !chosen {
				side = game.Vertical
			}
			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
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
			return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
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

By default twixtui listens for a direct connection on every interface, which
works on a local network, over a VPN or tailnet, or with a forwarded port. Use
--bind to accept the connection on one interface only — --bind 127.0.0.1
accepts it from this machine alone — and --port to choose where on it.

If neither of you can accept an incoming connection, both use --relay with the
address of a relay one of you runs with "twixtui serve".

Neither route hides the game. A relay's operator reads what it carries in
plain text: both names, the ruleset and every move. What a relayed game's
pairing code buys is integrity and not secrecy — its second part is a key the
relay is never told, and it stops the relay altering the game rather than
seeing it. A direct connection has no relay in the middle and no secret
either: the invitation, carrying this player's name and the ruleset, goes out
as soon as something connects, before either end has proved anything about the
other.

--resume continues a saved network game whose connection was lost, on the
terms it was played on, instead of starting a new one. Your opponent resumes
the same game from their end, either way round: whoever waits is your choice
each time, not something the saved game decides.

The address to share is printed before the wait begins.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Whatever the flags settle between themselves is settled before
			// the first thing that writes. Resolving the profile creates it on
			// a machine that has none and dates it on one that has it, so a
			// command line refused for a board size it cannot build or an
			// interface it cannot bind is refused before that: otherwise the
			// run leaves a profile behind and plays no game, which is the one
			// outcome a refusal is supposed to rule out.
			resumeID, err := f.resumeOverrides(cmd)
			if err != nil {
				return err
			}
			var rs game.Ruleset
			var side game.Player
			if resumeID == "" {
				if rs, err = f.rules(); err != nil {
					return err
				}
				chosen := false
				if side, chosen, err = f.resolveSide(time.Now().UnixNano()); err != nil {
					return err
				}
				if !chosen {
					side = game.Vertical
				}
			}
			var target string
			if f.relay == "" {
				if target, err = f.listenAddr(); err != nil {
					return err
				}
			}

			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}

			out := cmd.OutOrStdout()
			var resume *app.RemoteResume
			var hostOpts netplay.HostOptions
			if resumeID != "" {
				if resume, err = resumeSaved(deps, resumeID, player); err != nil {
					return err
				}
				hostOpts = resume.Host()
				fmt.Fprintln(out, resume.Describe())
			} else {
				hostOpts = netplay.HostOptions{Name: player, Rules: rs, Side: side}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			var session netplay.Session
			if f.relay != "" {
				code := netplay.PairingCode()
				fmt.Fprintf(out, "Pairing code: %s\n", code)
				fmt.Fprintf(out, "Your opponent runs: twixtui play join --relay %s %s\n\n", f.relay, code)
				fmt.Fprintln(out, "Waiting for them to join. Press ctrl+c to give up.")
				session, err = netplay.HostViaRelay(ctx, f.relay, code, hostOpts)
			} else {
				listener, bindErr := netplay.Bind(target)
				if bindErr != nil {
					return bindErr
				}
				// The bound address is what the opponent is told, rather than
				// the port alone pasted after a placeholder: a host that bound
				// one interface knows the whole address to give out, and one
				// that bound every interface knows only the port and says so.
				fmt.Fprintf(out, "Listening on %s\n", listener.Addr())
				fmt.Fprintf(out, "Your opponent runs: twixtui play join %s\n\n", netplay.JoinTarget(listener.Addr()))
				fmt.Fprintln(out, "Waiting for them to connect. Press ctrl+c to give up.")
				session, err = listener.Wait(ctx, hostOpts)
			}
			if err != nil {
				return gaveUp(ctx, "an opponent", err)
			}
			defer session.Close()

			if resume != nil {
				return runRemoteGame(cmd, deps, resume.Continue(session))
			}
			return runRemoteGame(cmd, deps, app.RemoteConfig(player, session))
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().StringVar(&f.bind, "bind", "",
		"interface address to accept the connection on, such as 127.0.0.1 for this machine alone; left out, every interface accepts it")
	cmd.Flags().IntVar(&f.port, "port", 4270, "port to listen on; 0 picks a free one and prints it")
	cmd.Flags().StringVar(&f.relay, "relay", "",
		"pair through a relay at this address instead of listening directly")
	f.addResumeFlag(cmd, opts)
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
starts rather than going wrong later.

Neither a direct connection nor a relay hides the game; "twixtui play host
--help" says what each of them exposes.

--resume continues a saved network game whose connection was lost instead of
starting a new one. The saved ruleset and side are what this end insists on, so
a host offering anything else is refused rather than played on other terms.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			if target == "" {
				return errors.New("give the address they printed, or their pairing code together with --relay")
			}
			// What the flags say on their own is settled before the profile is
			// resolved, which writes; the host command says what a refusal
			// after that leaves behind. A pairing code is checked here for a
			// second reason as well: the banner below echoes the code back,
			// which is the one thing a player can check without the host on
			// the phone — and echoing a code that was never going to pair,
			// over a promise to wait for an opponent who cannot arrive, is
			// exactly the reading that hides a typo.
			resumeID, err := f.resumeOverrides(cmd)
			if err != nil {
				return err
			}
			if f.relay != "" {
				if err := netplay.CheckPairingCode(target); err != nil {
					return err
				}
			}

			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			guestOpts := netplay.GuestOptions{Name: player}
			var resume *app.RemoteResume
			if resumeID != "" {
				if resume, err = resumeSaved(deps, resumeID, player); err != nil {
					return err
				}
				guestOpts = resume.Guest()
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Say what is being waited for before waiting for it. A relay that
			// is reachable but has nobody in the room accepts the connection
			// and then says nothing at all, so a joiner that prints nothing is
			// indistinguishable from a hung one. The banner goes out before
			// the call rather than after it because the wait happens inside
			// the call; a relay that cannot be reached prints its own error a
			// moment later and supersedes it.
			out := cmd.OutOrStdout()
			if resume != nil {
				fmt.Fprintln(out, resume.Describe())
			}
			var session netplay.Session
			if f.relay != "" {
				fmt.Fprintf(out, "Pairing code: %s\n", target)
				fmt.Fprintf(out, "Joining through the relay at %s.\n\n", f.relay)
				fmt.Fprintln(out, "Waiting for the host. Press ctrl+c to give up.")
				session, err = netplay.JoinViaRelay(ctx, f.relay, target, guestOpts)
			} else {
				// A direct join usually connects or fails within a round trip,
				// so it gets one line rather than the relay's three; a filtered
				// port is the case where it too sits there, and then this is
				// what says so.
				addr := netplay.NormalizeAddr(target)
				fmt.Fprintf(out, "Connecting to %s. Press ctrl+c to give up.\n", addr)
				session, err = netplay.Dial(ctx, addr, guestOpts)
			}
			if err != nil {
				return gaveUp(ctx, "the host", err)
			}
			defer session.Close()

			fmt.Fprintf(out, "Connected to %s. You play %s.\n",
				session.OpponentName(), session.Side())
			if resume != nil {
				return runRemoteGame(cmd, deps, resume.Continue(session))
			}
			return runRemoteGame(cmd, deps, app.RemoteConfig(player, session))
		},
	}
	cmd.Flags().StringVar(&f.relay, "relay", "",
		"join through a relay at this address, using a pairing code instead of an address")
	f.addResumeFlag(cmd, opts)
	return cmd
}

// addResumeFlag offers --resume on the two ends of a live game. It is one
// helper because the flag means the same thing at both: the game is the saved
// one, and only the way the connection is made again is being chosen here.
func (f *gameFlags) addResumeFlag(cmd *cobra.Command, opts *options) {
	cmd.Flags().StringVar(&f.resume, "resume", "",
		"continue this saved network game instead of starting a new one")
	registerFlagCompletion(cmd, "resume", opts.gameIDCompletions)
}

// resumeOverrides settles what --resume means between the flags alone: the
// identifier of the saved game to continue, empty when the flag was not
// given, and a refusal when the command line gives no identifier, gives one
// that could not name a saved game anywhere, or names both a saved game and
// new terms for it.
//
// The terms of a continued game come from its record, so a flag that would set
// them differently is refused rather than quietly ignored: a player who passed
// both meant one of the two, and playing the saved game on the saved terms
// while appearing to accept the others is the reading that goes wrong later.
//
// It reads nothing but the flags, which is what lets every caller settle it
// before opening a store or resolving a profile: both of those write, and a
// command line this refuses is one that was never going to play a game.
func (f *gameFlags) resumeOverrides(cmd *cobra.Command) (string, error) {
	id := strings.TrimSpace(f.resume)
	if id == "" {
		if cmd.Flags().Changed("resume") {
			return "", errors.New("--resume needs the identifier of the saved game to continue")
		}
		return "", nil
	}
	for _, name := range []string{"ruleset", "size", "side"} {
		if cmd.Flags().Changed(name) {
			return "", fmt.Errorf("--%s cannot be given with --resume: a continued game keeps the terms it was played on", name)
		}
	}
	// Whether an identifier could name a saved game at all is a fact about
	// what was typed, so it is settled here; whether this machine holds that
	// game, and whether it is one this player can continue, is what the store
	// is still asked. The store checks the same thing again on the way in and
	// reports it in the same words, so nothing but the moment of the refusal
	// changes — and that moment is the point: it now comes before a profile
	// is created or dated for a game that was never going to be continued.
	if err := gamestore.ValidateID(id); err != nil {
		return "", err
	}
	return id, nil
}

// resumeSaved reads the saved game an identifier names and prepares it to be
// continued as this player. The identifier is the one resumeOverrides
// returned, so what is left here is the half that needs the store.
func resumeSaved(deps app.Deps, id, player string) (*app.RemoteResume, error) {
	if deps.Games == nil {
		return nil, errors.New("there is nowhere to read saved games from")
	}
	sv, err := deps.Games.Get(id)
	if err != nil {
		return nil, err
	}
	res, err := app.PrepareRemoteResume(sv, player)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// checkGameTerms reports whether the ruleset and side flags can mean anything
// at all, without settling which side a "random" one lands on. It is what a
// command that resolves its terms further in refuses a typo by before it
// resolves the profile, which writes.
func (f *gameFlags) checkGameTerms() error {
	if _, err := f.rules(); err != nil {
		return err
	}
	_, _, err := f.resolveSide(0)
	return err
}

// listenAddr is the address a direct host binds, assembled from the interface
// and the port, which are asked for separately because they are separate
// choices: which of this machine's addresses answers, and where on it.
func (f *gameFlags) listenAddr() (string, error) {
	host := strings.TrimSpace(f.bind)
	if trimmed, ok := strings.CutPrefix(host, "["); ok {
		host = strings.TrimSuffix(trimmed, "]")
	}
	if host == "" {
		return netplay.BindAddr(":" + strconv.Itoa(f.port))
	}
	return netplay.BindAddr(net.JoinHostPort(host, strconv.Itoa(f.port)))
}

// gaveUp turns a cancelled wait into a sentence a player can act on.
//
// Cancelling the context closes the socket out from under the read that is
// blocked on it, so what the network layer reports is a use-of-closed-connection
// error naming an ephemeral port: accurate, and no use to somebody who has just
// pressed ctrl+c on purpose. The original error is dropped rather than wrapped
// because it describes the cancellation this function is already naming, not a
// reason for it. The status stays non-zero: no game was played.
func gaveUp(ctx context.Context, waitingFor string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("gave up waiting for %s", waitingFor)
	}
	return err
}

// runRemoteGame runs the game screen for an established session. The
// configuration is built by the caller because a fresh connection and a
// continued one are two different games: one has no stored row behind it, and
// the other must go back into the row it came out of.
func runRemoteGame(cmd *cobra.Command, deps app.Deps, cfg app.GameConfig) error {
	return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
		return app.NewGameScreen(deps, cfg)
	})
}

func newPlayCorrespondenceCommand(opts *options) *cobra.Command {
	var f gameFlags
	var gameID string
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
  twixtui play correspondence                  open a game that is waiting for you
  twixtui play correspondence --game ID        open that game, when several are open`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Which of the three things this command does is decided by the
			// flags alone, and so is whether the terms of a new game can mean
			// anything: both are settled before the profile is resolved,
			// because resolving it writes.
			switch {
			case f.newGame && f.join != "":
				return errors.New("choose either --new or --join, not both")
			case f.newGame:
				if err := f.checkGameTerms(); err != nil {
					return err
				}
			}

			deps, player, err := opts.deps()
			if err != nil {
				return err
			}
			if player == "" {
				return errFirstRunNeedsProfile
			}
			switch {
			case f.newGame:
				return startCorrespondence(cmd, deps, player, &f)
			case f.join != "":
				return joinCorrespondence(cmd, deps, player, f.join)
			}
			return openCorrespondence(cmd, deps, gameID)
		},
	}
	f.addRuleFlags(cmd)
	f.addSideFlag(cmd)
	cmd.Flags().BoolVar(&f.newGame, "new", false, "start a game and print an invitation to send")
	cmd.Flags().StringVar(&f.join, "join", "", "accept an invitation code")
	cmd.Flags().StringVar(&gameID, "game", "", "open this saved game, when more than one is waiting")
	registerFlagCompletion(cmd, "game", opts.gameIDCompletions)
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
//
// Notes left by the screens are printed after the interface has closed rather
// than while it is open: the alternate screen takes its own output with it when
// it goes, so a line drawn on the way out is never seen. That is where a player
// is told their unfinished game was saved.
func runScreens(cmd *cobra.Command, deps app.Deps, first func(app.Deps) (app.Screen, error)) error {
	var notes []string
	deps.Note = func(line string) { notes = append(notes, line) }

	// The builder is given this function's own copy of the dependencies rather
	// than closing over the caller's: the note channel is installed here, and a
	// screen built from the caller's copy would have nowhere to leave a note.
	screen, err := first(deps)
	if err != nil {
		return err
	}
	shell := app.NewShell(deps, screen)
	program := tea.NewProgram(shell, tea.WithContext(cmd.Context()))
	_, runErr := program.Run()

	for _, line := range notes {
		fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}
