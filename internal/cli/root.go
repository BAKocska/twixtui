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

	"github.com/BAKocska/twixtui/internal/theme"
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

// configPath returns the directory holding profiles, the leaderboard, saved
// games and the theme choice. The environment variable exists so that a test or
// a second player on the same machine can run against an isolated directory.
func (o *options) configPath() (string, error) {
	if o.configDir != "" {
		return o.configDir, nil
	}
	if env := os.Getenv("TWIXTUI_CONFIG_DIR"); env != "" {
		return env, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the user configuration directory: %w", err)
	}
	return filepath.Join(base, "twixtui"), nil
}

// theme resolves the colour scheme for this run: an explicit flag wins, then the
// saved choice, and a request for no colour overrides both.
func (o *options) theme() (theme.Theme, error) {
	if o.noColor || os.Getenv("NO_COLOR") != "" {
		return theme.Get("mono")
	}
	if o.themeName != "" {
		return theme.Get(o.themeName)
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

// themeReason explains why the theme in force is not the saved one.
func (o *options) themeReason() string {
	switch {
	case o.noColor:
		return "--no-color was given"
	case os.Getenv("NO_COLOR") != "":
		return "NO_COLOR is set in the environment"
	case o.themeName != "":
		return "--theme " + o.themeName + " was given"
	}
	return "of an override"
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
	// printing is the subcommand form: running the group by itself only prints
	// its help.
	if !strings.Contains(cmd.Use, " ") {
		cmd.Use += " <command>"
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

func themeCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out := make([]cobra.Completion, 0, len(theme.All()))
	for _, t := range theme.All() {
		out = append(out, cobra.CompletionWithDesc(t.Name, t.Summary))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// helpTemplate groups the commands by what they are for, because an alphabetical
// list of a dozen commands tells a newcomer nothing about where to start.
const helpTemplate = `{{with .Long}}{{. | trimTrailingWhitespaces}}

{{end}}{{if .Runnable}}Usage:
  {{.UseLine}}
{{end}}{{if .HasAvailableSubCommands}}
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
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <command>{{end}}{{if gt (len .Aliases) 0}}

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
		Args:  cobra.NoArgs,
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
		Args: cobra.ExactArgs(1),
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
			return fmt.Errorf("unknown shell %q", args[0])
		},
	}
	return cmd
}

func newRulesCommand(opts *options) *cobra.Command {
	rules := &cobra.Command{
		Use:   "rules",
		Short: "Read the rules and where they come from",
	}

	var provenance bool
	show := &cobra.Command{
		Use:   "show [topic]",
		Short: "Print the rules of the game",
		Long: `Print the rules of the game.

Given a topic, print only the sections whose headings mention it, for example
"twixtui rules show links" or "twixtui rules show swap".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := docsRules
			if provenance {
				text = docsProvenance
			}
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}
			text, err := sections(text, topic)
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
	show.Flags().BoolVar(&provenance, "provenance", false,
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
		return "", fmt.Errorf("no section of the rules mentions %q; try one of: board, links, crossing, winning, draw, swap, notation", topic)
	}
	return out.String(), nil
}

func markdownHeading(line string) (level int, heading string, ok bool) {
	trimmed := strings.TrimLeft(line, "#")
	level = len(line) - len(trimmed)
	if level == 0 || !strings.HasPrefix(trimmed, " ") {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed), true
}

func rulesTopicCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var out []cobra.Completion
	for _, line := range strings.Split(docsRules, "\n") {
		if _, heading, ok := markdownHeading(line); ok {
			out = append(out, cobra.Completion(topicWord(heading)))
		}
	}
	return dedupe(out), cobra.ShellCompDirectiveNoFileComp
}

// topicWord reduces a heading to the word a player would actually type. Leading
// articles are skipped, so "The board" completes as "board" rather than "the".
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

func dedupe(in []cobra.Completion) []cobra.Completion {
	seen := make(map[cobra.Completion]bool, len(in))
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func newThemeCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Choose the colour scheme",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the available colour schemes",
		Args:  cobra.NoArgs,
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := opts.theme()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s: %s\n", t.Name, t.Summary)

			// The theme in force is not always the one that was chosen: a
			// --theme flag, --no-color, or NO_COLOR in the environment override
			// it. Saying so is the difference between an explanation and an
			// apparent bug.
			if dir, dirErr := opts.configPath(); dirErr == nil {
				if saved, savedErr := theme.Selected(dir); savedErr == nil && saved.Name != t.Name {
					fmt.Fprintf(w, "overriding the saved choice, %s, because %s\n", saved.Name, opts.themeReason())
				}
			}
			if t.Monochrome() {
				fmt.Fprintln(w, "no colour; the two players are told apart by shape")
				return nil
			}
			for _, row := range [][2]string{
				{"vertical peg", t.VerticalPeg},
				{"vertical link", t.VerticalLink},
				{"horizontal peg", t.HorizontalPeg},
				{"horizontal link", t.HorizontalLink},
				{"grid", t.Grid},
				{"border rows", t.BorderRow},
				{"cursor", t.Cursor},
				{"highlight", t.Highlight},
				{"last move", t.LastMove},
			} {
				fmt.Fprintf(w, "  %-16s %s\n", row[0], row[1])
			}
			return nil
		},
	}

	cmd.AddCommand(list, set, show)
	return cmd
}
