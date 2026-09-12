// Package cli builds the command line. Every feature is reachable as a
// subcommand so that it can be scripted and completed by the shell, and the
// bare command opens the interactive interface.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/BAKocska/twixtui/internal/cover"
	"github.com/BAKocska/twixtui/internal/theme"
	"github.com/BAKocska/twixtui/internal/ui"
)

// unstampedVersion is the version a build carries when nothing stamped one into
// it, which is every build that is not a release.
const unstampedVersion = "dev"

// Build information, set through -ldflags at release time.
var (
	version = unstampedVersion
	commit  = "none"
	date    = "unknown"
)

// SetBuildInfo records the values stamped into the binary at build time.
func SetBuildInfo(v, c, d string) {
	if v != "" {
		version = v
	}
	if c != "" {
		commit = c
	}
	if d != "" {
		date = d
	}
}

// options holds the settings every command shares.
type options struct {
	configDir string
	profile   string
	themeName string
	noColor   bool
}

// configEnv names an alternative configuration directory, so that a test or a
// second player on the same machine can run against an isolated one.
const configEnv = "TWIXTUI_CONFIG_DIR"

// configPath returns the directory holding profiles, the leaderboard, saved
// games and the theme choice.
func (o *options) configPath() (string, error) {
	if o.configDir != "" {
		return o.configDir, nil
	}
	env, set, err := envConfigDir()
	if err != nil {
		return "", err
	}
	if set {
		return env, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the user configuration directory: %w", err)
	}
	return filepath.Join(base, "twixtui"), nil
}

// envConfigDir reports the configuration directory the environment names, and
// whether it names one at all.
//
// A variable that is present but blank is refused rather than read as an
// absent one. The difference matters most exactly where nobody is watching: a
// script that exports the variable from an unset shell variable would
// otherwise write its games, profiles and results into the player's real
// configuration directory while looking as though it had isolated itself.
// Falling back there is the one outcome the variable exists to prevent, so the
// blank value is reported instead, and leaving the variable unset stays the way
// to ask for the default.
func envConfigDir() (string, bool, error) {
	value, set := os.LookupEnv(configEnv)
	if !set {
		return "", false, nil
	}
	if strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("%s is set to an empty value; unset it to use the default configuration directory, or give it a path", configEnv)
	}
	return value, true, nil
}

// namedTheme resolves the --theme flag, if one was given. It exists so the flag
// is checked in one place: a value is wrong or right on its own, and resolving
// it only where a theme was actually needed made a misspelling an error at a
// terminal and nothing at all once the output was redirected, which is the one
// place a script could have caught it.
func (o *options) namedTheme() (theme.Theme, bool, error) {
	if o.themeName == "" {
		return theme.Theme{}, false, nil
	}
	t, err := theme.Get(o.themeName)
	if err != nil {
		return theme.Theme{}, false, err
	}
	return t, true, nil
}

// theme resolves the colour scheme for this run: an explicit flag wins, then the
// saved choice, and a request for no colour overrides both.
func (o *options) theme() (theme.Theme, error) {
	named, ok, err := o.namedTheme()
	if err != nil {
		return theme.Theme{}, err
	}
	// Colour is for a terminal. When output is redirected to a file or piped
	// into something else, escape sequences are noise at best and corrupt the
	// data at worst, so they are suppressed the way any well-behaved command
	// suppresses them. This is also what makes a listing's output stable enough
	// to assert on. The named theme is resolved before this all the same: where
	// the output goes decides whether colour is worth emitting, and nothing
	// else.
	if o.noColor || os.Getenv("NO_COLOR") != "" || !stdoutIsTerminal() {
		return theme.Get("mono")
	}
	if ok {
		return named, nil
	}
	dir, err := o.configPath()
	if err != nil {
		return theme.Get(theme.Default)
	}
	t, err := theme.Selected(dir)
	if err != nil {
		// A broken theme setting is not worth refusing to start over; fall back
		// and tell the player once.
		fmt.Fprintf(os.Stderr, "twixtui: %v; using the %s theme\n", err, t.Name)
	}
	return t, nil
}

// requestedTheme is the theme this run asked for, and the phrase naming where
// the request came from: an explicit --theme wins over the saved choice, which
// is the same order theme resolves them in.
//
// It is separate from the theme in force because those two differ whenever
// colour is suppressed, and the explanation a player needs then is about what
// they asked for. Comparing the theme in force against the saved choice alone
// told somebody who had just typed "--theme paper" that the saved classic was
// being overridden — naming a theme they had not asked for as the one they had.
func (o *options) requestedTheme() (theme.Theme, string, error) {
	named, ok, err := o.namedTheme()
	if err != nil {
		return theme.Theme{}, "", err
	}
	if ok {
		return named, "--theme " + named.Name, nil
	}
	dir, err := o.configPath()
	if err != nil {
		return theme.Theme{}, "", err
	}
	saved, err := theme.Selected(dir)
	if err != nil {
		return theme.Theme{}, "", err
	}
	return saved, "the saved choice, " + saved.Name, nil
}

// monochromeReason says why colour was suppressed. The three causes are the
// only ways the theme in force can differ from the one requested, so a caller
// asks only once it has seen them differ.
func (o *options) monochromeReason() string {
	switch {
	case o.noColor:
		return "--no-color was given"
	case os.Getenv("NO_COLOR") != "":
		return "NO_COLOR is set in the environment"
	default:
		return "the output is not going to a terminal"
	}
}

// NewRootCommand builds the command tree.
func NewRootCommand() *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "twixtui",
		Short: "Play TwixT in the terminal",
		Long: `Play TwixT in the terminal.

TwixT is Alex Randolph's 1962 connection game. Two players take turns pegging a
24x24 board; each peg you place offers links to your own pegs a knight's move
away, and you choose which of them to make. Links may never cross, and the
winner is the first to join their two border rows with an unbroken chain.

Run twixtui with no arguments for the interactive interface, or use the
subcommands below to go straight to a game.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          rejectUnknownSubcommand,
		// Flag values and --theme are checked here rather than wherever a
		// command happens to read them, so that a command which never draws
		// refuses a misspelled theme too instead of accepting it in silence,
		// and so that a value which cannot mean anything is refused before the
		// command it was given to writes, listens or deletes anything.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFlagValues(cmd); err != nil {
				return err
			}
			// Read for its error alone: a blank TWIXTUI_CONFIG_DIR is a
			// mistake wherever it is seen, so it is reported by every command
			// rather than only by the ones that go on to open a file.
			if _, _, err := envConfigDir(); err != nil {
				return err
			}
			// The cover's environment variables are applied once, here, so that
			// a complaint about one of them is printed before anything switches
			// the terminal to its alternate screen. Nothing on a drawing path
			// reports them: the artwork is chosen for every frame, so a
			// diagnostic there would repeat for as long as a menu is open and
			// would be written over the picture it was about. A bad value is
			// reported and then ignored, because somebody who mistyped the name
			// of a picture wants to play the game rather than be stopped by it.
			for _, problem := range cover.ParseEnvironment() {
				fmt.Fprintf(cmd.ErrOrStderr(), "twixtui: %v\n", problem)
			}
			_, _, err := opts.namedTheme()
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInteractive(cmd, opts)
		},
	}

	flags := root.PersistentFlags()
	flags.StringVar(&opts.configDir, "config", "",
		"directory holding profiles, games and settings (default: your user configuration directory)")
	flags.StringVar(&opts.profile, "profile", "",
		"play as this profile instead of being asked")
	flags.StringVar(&opts.themeName, "theme", "",
		"colour scheme for this run only")
	flags.BoolVar(&opts.noColor, "no-color", false,
		"draw without colour")

	registerFlagCompletion(root, "theme", themeCompletions)
	registerFlagCompletion(root, "profile", opts.profileCompletions)

	root.AddCommand(
		newPlayCommand(opts),
		newLearnCommand(opts),
		newProfileCommand(opts),
		newLeaderboardCommand(opts),
		newGameCommand(opts),
		newAnalyzeCommand(opts),
		newRulesCommand(opts),
		newServeCommand(opts),
		newThemeCommand(opts),
		newCompletionCommand(),
		newVersionCommand(),
	)

	guardSubcommands(root)
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(usageTemplate)
	return root
}

// Execute runs the command line and returns the process exit status.
func Execute() int {
	root := NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "twixtui: %v\n", err)
		return 1
	}
	return 0
}

// registerFlagCompletion attaches a completion function to a flag, failing loudly
// during development if the flag does not exist.
func registerFlagCompletion(cmd *cobra.Command, flag string, fn func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective)) {
	if err := cmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		panic(fmt.Sprintf("registering completion for --%s: %v", flag, err))
	}
}

// checkFlagValues refuses values that cannot mean what they were given for,
// before the command they were given to does any work.
//
// Only the flags the command line actually set are examined, and that is the
// whole point. A flag left out is indistinguishable from one given its own
// default by the time a command reads the variable behind it, so "game export"
// writing to standard output and "game export --out ”" writing to a file with
// no name arrive as the same empty string, and a blank "--profile" read as an
// absent one silently plays as somebody else. Visit walks exactly what was
// given, which keeps every documented default working and turns an explicitly
// empty value into the mistake it is.
//
// The two rules are stated over the whole tree rather than flag by flag,
// because they hold over the whole tree: no flag here gives a blank value a
// meaning, and the only count flag is --limit, which counts rows of a listing
// and already spells "all of them" zero.
func checkFlagValues(cmd *cobra.Command) error {
	var problem error
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if problem != nil {
			return
		}
		switch {
		case f.Value.Type() == "string" && strings.TrimSpace(f.Value.String()) == "":
			problem = blankFlagValue(f)
		case f.Name == "limit":
			if n, err := cmd.Flags().GetInt(f.Name); err == nil && n < 0 {
				problem = fmt.Errorf("--limit cannot be %d; give the number of rows to show, or 0 to show them all", n)
			}
		}
	})
	return problem
}

// blankFlagValue refuses an explicitly empty flag value, saying what leaving
// the flag out would have done instead.
func blankFlagValue(f *pflag.Flag) error {
	if f.DefValue == "" {
		return fmt.Errorf("--%s was given an empty value; leave the flag out rather than giving it nothing", f.Name)
	}
	return fmt.Errorf("--%s was given an empty value; leave the flag out to use %q", f.Name, f.DefValue)
}

// guardSubcommands makes every command that is only a group of subcommands
// refuse one it does not have. Cobra answers an unrecognised subcommand of a
// group by printing the group's help and stopping successfully, so a typo looks
// like a command that worked and a script cannot tell the difference. A group
// needs a run of its own for cobra to check its arguments at all; printing help
// is what it does when asked for nothing in particular.
func guardSubcommands(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		guardSubcommands(sub)
	}
	if !cmd.HasSubCommands() {
		return
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	if cmd.Args == nil {
		cmd.Args = rejectUnknownSubcommand
	}
	if cmd.Runnable() {
		return
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	// Cobra builds the usage line out of Use, and for a group the line worth
	// printing names the subcommand. It is optional in the usage because it is
	// optional in fact: a group with nothing after it prints its own help and
	// succeeds, which is what somebody who typed the group alone was asking
	// for. Spelling it "<command>" said the opposite and made the help look
	// like a refusal that had failed to refuse.
	if !strings.Contains(cmd.Use, " ") {
		cmd.Use += " [command]"
	}
	cmd.DisableFlagsInUseLine = true
}

// rejectUnknownSubcommand refuses an argument that is not one of the command's
// subcommands, naming the near miss cobra already knows how to find.
func rejectUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("unknown command %q for %q%s",
		args[0], cmd.CommandPath(), didYouMean(cmd, args[0]))
}

// didYouMean offers the commands close enough to what was typed to be what was
// meant, as a clause to hang off the end of the refusal.
func didYouMean(cmd *cobra.Command, typed string) string {
	if cmd.DisableSuggestions {
		return ""
	}
	suggestions := cmd.SuggestionsFor(typed)
	if len(suggestions) == 0 {
		return ""
	}
	quoted := make([]string, len(suggestions))
	for i, s := range suggestions {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	last := len(quoted) - 1
	if last == 0 {
		return "; did you mean " + quoted[0] + "?"
	}
	return "; did you mean " + strings.Join(quoted[:last], ", ") + " or " + quoted[last] + "?"
}

// exactArgs is cobra.ExactArgs with a diagnostic somebody can act on. Cobra's
// own wording — "accepts 1 arg(s), received 0" — names neither what is missing
// nor what to type instead, and usage is silenced for every other refusal so
// nothing else on screen says either. These messages carry the usage line
// themselves.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) < n:
			return missingArgs(cmd, len(args), n)
		case len(args) > n:
			return unexpectedArg(cmd, args[n])
		}
		return nil
	}
}

// maxArgs is cobra.MaximumNArgs with the same diagnostic.
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return unexpectedArg(cmd, args[n])
		}
		return nil
	}
}

// noArgs refuses an argument to a command that takes none. Cobra answers this
// with "unknown command", which is a fair guess for a group of subcommands and
// simply wrong for a leaf: "leaderboard show Balint" is not a mistyped
// subcommand of a command that has none, it is a value where a flag was wanted.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return unexpectedArg(cmd, args[0])
}

// missingArgs names the arguments the command line did not fill in, taking
// their names from the command's own usage line so that the refusal and the
// usage cannot drift apart.
func missingArgs(cmd *cobra.Command, given, want int) error {
	if names := usagePlaceholders(cmd.Use); len(names) == want {
		return fmt.Errorf("missing %s; usage: %s", joinWith("and", names[given:]), cmd.UseLine())
	}
	return fmt.Errorf("%s takes %d arguments and was given %d; usage: %s",
		cmd.CommandPath(), want, given, cmd.UseLine())
}

func unexpectedArg(cmd *cobra.Command, extra string) error {
	return fmt.Errorf("%s does not take %q; usage: %s", cmd.CommandPath(), extra, cmd.UseLine())
}

// usagePlaceholders returns the argument names of a usage string, in order, so
// that "rename <old> <new>" can be refused by naming <new>.
func usagePlaceholders(use string) []string {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return nil
	}
	return fields[1:]
}

// joinWith lists words as prose: "a", "a and b", "a, b and c".
func joinWith(conjunction string, words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	last := len(words) - 1
	return strings.Join(words[:last], ", ") + " " + conjunction + " " + words[last]
}

// validArgNames returns the values a command accepts, without the descriptions
// they carry for completion. A refusal has room for the names and needs them:
// being told a value is unknown without being told which ones are not leaves
// the reader to find the help page for a list they were nearly given.
func validArgNames(cmd *cobra.Command) []string {
	out := make([]string, 0, len(cmd.ValidArgs))
	for _, v := range cmd.ValidArgs {
		name, _, _ := strings.Cut(string(v), "\t")
		out = append(out, name)
	}
	return out
}

func themeCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out := make([]cobra.Completion, 0, len(theme.All()))
	for _, t := range theme.All() {
		out = append(out, cobra.CompletionWithDesc(t.Name, t.Summary))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// helpTemplate groups the commands by what they are for, because an alphabetical
// list of a dozen commands tells a newcomer nothing about where to start.
//
// The description falls back to Short where a command has no Long. Rendering
// Long alone left the commands that need no paragraph — the groups, and the
// leaves whose one-line description says everything — with a help page that
// opened on their usage line and never said what they were for.
//
// The subcommand line is added only for the root command. Every group's own
// usage line already carries the optional "[command]", which guardSubcommands
// puts there; the root command is the one place where running it with no
// command does something other than print this page.
const helpTemplate = `{{if .Long}}{{.Long | trimTrailingWhitespaces}}{{else}}{{.Short}}{{end}}

Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if and .HasAvailableSubCommands (not .HasParent)}}
  {{.CommandPath}} [command]{{end}}
{{if .HasAvailableSubCommands}}
Commands:
{{range .Commands}}{{if (and .IsAvailableCommand (ne .Name "help"))}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableInheritedFlags}}
Global flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableSubCommands}}
Use "{{.CommandPath}} <command> --help" for more about a command.
{{end}}`

const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if and .HasAvailableSubCommands (not .HasParent)}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Commands:{{range .Commands}}{{if (and .IsAvailableCommand (ne .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} <command> --help" for more about a command.{{end}}
`

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), describeBuild())
			return err
		},
	}
}

// describeBuild says which build this is. A release is stamped through -ldflags
// and reports its version, commit and date. Anything else was built from a
// checkout, where those three fields are placeholders rather than facts, so say
// so — and name the commit the toolchain recorded, which is the one thing about
// a source build worth knowing.
func describeBuild() string {
	if version != unstampedVersion {
		return fmt.Sprintf("twixtui %s (%s) built %s", version, commit, date)
	}
	revision, when, modified := checkoutBuilt()
	if revision == "" {
		return "twixtui, built from source (not a release build)"
	}
	built := "twixtui, built from source at commit " + revision
	if when != "" {
		built += " of " + when
	}
	if modified {
		built += ", with local changes"
	}
	return built
}

// checkoutBuilt reports what the toolchain recorded about the checkout a source
// build came from. Everything is empty for a binary built outside a repository.
func checkoutBuilt() (revision, when string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = shortCommit(setting.Value)
		case "vcs.time":
			when = buildDate(setting.Value)
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, when, modified
}

// shortCommit abbreviates a commit hash to the length git itself prints.
func shortCommit(revision string) string {
	const short = 7
	if len(revision) > short {
		return revision[:short]
	}
	return revision
}

// buildDate renders a recorded timestamp as a date, or leaves it alone if it is
// not the timestamp it is supposed to be.
func buildDate(value string) string {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return t.Format("2 January 2006")
}

func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Print a shell completion script",
		Long: `Print a shell completion script.

The scripts include a short description beside each command, flag and value, so
pressing TAB explains the options rather than only listing them.

  bash    source <(twixtui completion bash)
          or write it to /etc/bash_completion.d/twixtui
  zsh     twixtui completion zsh > "${fpath[1]}/_twixtui"
          descriptions need compinit, which most zsh setups already run
  fish    twixtui completion fish > ~/.config/fish/completions/twixtui.fish
  pwsh    twixtui completion powershell | Out-String | Invoke-Expression`,
		ValidArgs: []cobra.Completion{
			cobra.CompletionWithDesc("bash", "Bash 4.4 or newer, which is what carries descriptions"),
			cobra.CompletionWithDesc("zsh", "Z shell"),
			cobra.CompletionWithDesc("fish", "fish shell"),
			cobra.CompletionWithDesc("powershell", "PowerShell"),
		},
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root := cmd.Root()
			switch strings.ToLower(args[0]) {
			case "bash":
				// The second generator is the only bash path that carries
				// descriptions; the original one has none.
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			}
			return fmt.Errorf("unknown shell %q; twixtui prints completions for %s",
				args[0], joinWith("and", validArgNames(cmd)))
		},
	}
	return cmd
}

func newRulesCommand(opts *options) *cobra.Command {
	rules := &cobra.Command{
		Use:   "rules",
		Short: "Read the rules and where they come from",
	}

	show := &cobra.Command{
		Use:   "show [topic]",
		Short: "Print the rules of the game",
		Long: `Print the rules of the game.

Given a topic, print only the sections whose headings mention it, for example
"twixtui rules show links" or "twixtui rules show swap".`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}
			text, err := sections(rulesDocument(cmd), topic)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !isTerminal(out) {
				// Redirected into a file, a pager or another program: the
				// document as it stands is what the reader on the other end
				// asked for.
				_, err := io.WriteString(out, text)
				return err
			}
			width, _ := terminalSize()
			_, err = io.WriteString(out, renderMarkdown(text, width))
			return err
		},
	}
	show.Flags().Bool("provenance", false,
		"print which source supports which rule, and where the sources disagree")
	show.ValidArgsFunction = rulesTopicCompletions

	rules.AddCommand(show)
	return rules
}

// sections returns a markdown document, or only the sections whose heading
// contains topic.
func sections(text, topic string) (string, error) {
	if topic == "" {
		return text, nil
	}
	needle := strings.ToLower(topic)
	var out strings.Builder
	var keeping bool
	var keptLevel int
	for _, line := range strings.Split(text, "\n") {
		if level, heading, ok := markdownHeading(line); ok {
			switch {
			case strings.Contains(strings.ToLower(heading), needle):
				keeping, keptLevel = true, level
			case keeping && level > keptLevel:
				// A subsection of a kept section stays.
			default:
				keeping = false
			}
		}
		if keeping {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("no heading mentions %q; try one of: %s",
			topic, strings.Join(topicWords(text), ", "))
	}
	return out.String(), nil
}

// rulesDocument reports which document "rules show" is about to print. Every
// topic the command offers, suggests or completes has to come from this one,
// or a suggestion narrows a document the reader did not ask for and finds
// nothing in the one they did.
func rulesDocument(cmd *cobra.Command) string {
	if provenance, err := cmd.Flags().GetBool("provenance"); err == nil && provenance {
		return docsProvenance
	}
	return docsRules
}

// topics returns the words a document can be filtered by, paired with the
// heading each came from, in the order the headings appear.
func topics(text string) []docTopic {
	var out []docTopic
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		_, heading, ok := markdownHeading(line)
		if !ok {
			continue
		}
		word := topicWord(heading)
		if word == "" || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, docTopic{word: word, heading: heading})
	}
	return out
}

// docTopic is a filter word and the heading it was taken from, which is what a
// reader needs to see beside it to know what they would be printing.
type docTopic struct {
	word    string
	heading string
}

// topicWords is the filter words alone, for a refusal that has to list them.
func topicWords(text string) []string {
	found := topics(text)
	out := make([]string, 0, len(found))
	for _, t := range found {
		out = append(out, t.word)
	}
	return out
}

func markdownHeading(line string) (level int, heading string, ok bool) {
	trimmed := strings.TrimLeft(line, "#")
	level = len(line) - len(trimmed)
	if level == 0 || !strings.HasPrefix(trimmed, " ") {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed), true
}

func rulesTopicCompletions(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	found := topics(rulesDocument(cmd))
	out := make([]cobra.Completion, 0, len(found))
	for _, t := range found {
		out = append(out, cobra.CompletionWithDesc(t.word, t.heading))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// topicWord reduces a heading to the word a player would actually type. Leading
// articles are skipped, so "The board" completes as "board" rather than "the".
// Whatever it returns is a word of the heading it came from, so filtering by it
// always finds at least that section: a topic offered anywhere is a topic that
// works.
func topicWord(heading string) string {
	for _, w := range strings.Fields(strings.ToLower(heading)) {
		w = strings.Trim(w, "`*_,.:;()")
		switch w {
		case "", "the", "a", "an", "and", "of", "in", "on", "to", "as", "twixtui":
			continue
		}
		return w
	}
	return strings.ToLower(strings.TrimSpace(heading))
}

// themeSwatch is the block a colour is shown as, three cells wide: one cell is
// easy to mistake for a bullet, and a long run reads as a bar rather than as a
// sample of a single colour.
const themeSwatch = "███"

func newThemeCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Choose the colour scheme",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the available colour schemes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := opts.configPath()
			if err != nil {
				return err
			}
			current, _ := theme.Selected(dir)
			w := cmd.OutOrStdout()
			for _, t := range theme.All() {
				marker := " "
				if t.Name == current.Name {
					marker = "*"
				}
				if _, err := fmt.Fprintf(w, "%s %-9s %s\n", marker, t.Name, t.Summary); err != nil {
					return err
				}
			}
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <name>",
		Short: "Choose a colour scheme and remember it",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := opts.configPath()
			if err != nil {
				return err
			}
			t, err := theme.Select(dir, args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "theme set to %s: %s\n", t.Name, t.Summary)
			return err
		},
		ValidArgsFunction: themeCompletions,
	}

	show := &cobra.Command{
		Use:   "show",
		Short: "Show the colour scheme in use and the colours it assigns",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := opts.theme()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s: %s\n", t.Name, t.Summary)

			// The theme in force is not always the one that was asked for:
			// --no-color, NO_COLOR in the environment, and output that is not
			// going to a terminal all suppress colour, and those are the only
			// three ways the two can differ. Saying which one happened, and
			// naming what was actually asked for rather than the saved choice
			// the request had already replaced, is the difference between an
			// explanation and an apparent bug.
			if requested, phrase, reqErr := opts.requestedTheme(); reqErr == nil && requested.Name != t.Name {
				fmt.Fprintf(w, "overriding %s, because %s\n", phrase, opts.monochromeReason())
			}
			if t.Monochrome() {
				fmt.Fprintln(w, "no colour; the two players are told apart by shape")
				return nil
			}
			// A colour is easier to recognise than to read: at a terminal each
			// row carries a block painted in the very style the board draws
			// that role with, so the sample cannot claim a colour the game does
			// not use. Redirected output gets the names and the hex values on
			// their own, because an escape sequence in a file is noise to
			// whatever reads it next.
			styles := ui.StylesFor(t)
			atTerminal := isTerminal(w)
			rows := make([][]string, 0, 9)
			for _, role := range []struct {
				label, hex, sample string
			}{
				{"vertical peg", t.VerticalPeg, styles.PegVertical.Render(themeSwatch)},
				{"vertical link", t.VerticalLink, styles.LinkVertical.Render(themeSwatch)},
				{"horizontal peg", t.HorizontalPeg, styles.PegHorizontal.Render(themeSwatch)},
				{"horizontal link", t.HorizontalLink, styles.LinkHorizontal.Render(themeSwatch)},
				{"grid", t.Grid, styles.Hole.Render(themeSwatch)},
				{"border rows", t.BorderRow, styles.Label.Render(themeSwatch)},
				{"cursor", t.Cursor, styles.Cursor.Render(themeSwatch)},
				{"highlight", t.Highlight, styles.Highlight.Render(themeSwatch)},
				{"last move", t.LastMove, styles.LastMove.Render(themeSwatch)},
			} {
				row := []string{"", role.label, role.hex}
				if atTerminal {
					row = append(row, role.sample)
				}
				rows = append(rows, row)
			}
			return ui.WriteTable(w, rows, 2)
		},
	}

	cmd.AddCommand(list, set, show)
	return cmd
}
