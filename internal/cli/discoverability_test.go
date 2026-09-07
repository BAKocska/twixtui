package cli

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/leaderboard"
)

// completionPairs reads a __complete response as the values it offers and the
// description each carries.
func completionPairs(t *testing.T, out string) map[string]string {
	t.Helper()
	pairs := make(map[string]string, 8)
	for _, line := range completionLines(out) {
		value, desc, _ := strings.Cut(line, "\t")
		pairs[value] = desc
	}
	return pairs
}

// usageBlock returns the lines of a help page's usage section, which is where a
// reader learns whether an argument is required.
func usageBlock(help string) []string {
	var out []string
	inside := false
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.HasPrefix(line, "Usage:"):
			inside = true
		case inside && strings.TrimSpace(line) == "":
			return out
		case inside:
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// TestEveryCommandsHelpSaysWhatItIsFor is F18. The help page rendered Long and
// nothing else, so every command that needs no paragraph — the groups, and the
// leaves whose one line says everything — opened on its usage and never said
// what it was for. Asking each command in the tree is what keeps the next one
// from arriving the same way.
func TestEveryCommandsHelpSaysWhatItIsFor(t *testing.T) {
	dir := t.TempDir()
	var commands []*cobra.Command
	walk(NewRootCommand(), func(c *cobra.Command) { commands = append(commands, c) })

	for _, c := range commands {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		want := strings.TrimSpace(c.Short)
		if long := strings.TrimSpace(c.Long); long != "" {
			want = strings.SplitN(long, "\n", 2)[0]
		}
		if want == "" {
			t.Errorf("%s carries no description at all", c.CommandPath())
			continue
		}
		args := append(strings.Fields(c.CommandPath())[1:], "--help")
		out, err := run(t, dir, args...)
		if err != nil {
			t.Errorf("%s --help: %v", c.CommandPath(), err)
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("the help of %s never says what it is for; wanted %q in:\n%s",
				c.CommandPath(), want, out)
		}
	}
}

// TestGroupCommandTakesItsSubcommandOptionally is the other half of F18. A
// group with nothing after it prints its help and succeeds, which is what
// somebody who typed the group alone was asking for; the usage line said
// "<command>" and so described a command that was required and had not been
// refused.
func TestGroupCommandTakesItsSubcommandOptionally(t *testing.T) {
	dir := t.TempDir()

	help, err := run(t, dir, "profile", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if got := usageBlock(help); len(got) != 1 || got[0] != "twixtui profile [command]" {
		t.Errorf("the usage of a group reads %q, want one line marking the command optional", got)
	}

	out, err := run(t, dir, "profile")
	if err != nil {
		t.Fatalf("a group with no subcommand was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Commands:") {
		t.Errorf("a group with no subcommand printed no help:\n%s", out)
	}
	if _, err := run(t, dir, "profile", "nonsense"); err == nil {
		t.Error("a group still has to refuse a subcommand it does not have")
	}

	// The root command is the one place where leaving the command out does
	// something else, so it is the one place that says so on a line of its own.
	rootHelp, err := run(t, dir, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if got := usageBlock(rootHelp); len(got) != 2 || got[1] != "twixtui [command]" {
		t.Errorf("the root usage reads %q, want the interactive form and the subcommand form", got)
	}
}

// TestMissingArgumentsAreNamedWithTheUsage is F18's third half. Cobra answers a
// missing argument with "accepts 1 arg(s), received 0", which names neither
// what is missing nor what to type, and usage is silenced for every other
// refusal so nothing else on screen said it either.
func TestMissingArgumentsAreNamedWithTheUsage(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		args  []string
		wants []string
	}{
		{[]string{"profile", "create"}, []string{"<name>", "twixtui profile create <name>"}},
		{[]string{"profile", "rename", "Alice"}, []string{"<new>", "twixtui profile rename <old> <new>"}},
		{[]string{"theme", "set"}, []string{"<name>", "twixtui theme set <name>"}},
		{[]string{"completion"}, []string{"<shell>", "twixtui completion <shell>"}},
		// play join takes the address or pairing code to connect to. It was
		// left on cobra's own validator when the others moved across, so a
		// player who typed the command without its argument was told
		// "accepts 1 arg(s), received 0" and not what to type.
		{[]string{"play", "join"}, []string{"<address or pairing code>", "twixtui play join <address or pairing code>"}},
	} {
		out, err := run(t, dir, tc.args...)
		if err == nil {
			t.Errorf("%v was accepted: %q", tc.args, out)
			continue
		}
		for _, want := range tc.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the refusal does not mention %q: %v", tc.args, want, err)
			}
		}
		if strings.Contains(err.Error(), "arg(s)") {
			t.Errorf("%v: the refusal still counts arguments at the reader: %v", tc.args, err)
		}
	}

	// An argument to a command that takes none is a value in the wrong place,
	// not a mistyped subcommand of a command that has none. play's own leaves
	// are here because they were the last on cobra's validators.
	for _, args := range [][]string{
		{"leaderboard", "show", "Alice"},
		{"play", "bot", "pro"},
		{"play", "local", "24"},
		{"play", "host", "4270"},
		{"play", "correspondence", "abc"},
	} {
		out, err := run(t, dir, args...)
		if err == nil {
			t.Errorf("%v: an argument to a command that takes none was accepted: %q", args, out)
			continue
		}
		for _, want := range []string{args[len(args)-1], "twixtui " + strings.Join(args[:len(args)-1], " ")} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the refusal does not mention %q: %v", args, want, err)
			}
		}
		if strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%v: a value was refused as though it were a subcommand: %v", args, err)
		}
	}
}

// TestUnknownCompletionShellNamesTheOnesItKnows is F22: being told a value is
// unknown without being told which ones are known leaves the reader to go and
// find a list they were nearly given.
func TestUnknownCompletionShellNamesTheOnesItKnows(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "completion", "tcsh")
	if err == nil {
		t.Fatalf("an unsupported shell was accepted: %q", out)
	}
	for _, want := range []string{"tcsh", "bash", "zsh", "fish", "powershell"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestRulesTopicsComeFromTheDocumentBeingPrinted is F10. The topics were
// extracted from the rules, completed from the rules and suggested from a list
// written out by hand, whichever document the command was about to print: under
// --provenance every topic offered was one that could not narrow it, and the
// hand-written list could not go stale visibly.
func TestRulesTopicsComeFromTheDocumentBeingPrinted(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		flag   []string
		want   string
		absent string
	}{
		{"rules", nil, "crossing", "disagreements"},
		{"provenance", []string{"--provenance"}, "disagreements", "crossing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ask := func(extra ...string) []string {
				args := []string{"rules", "show"}
				args = append(args, tc.flag...)
				return append(args, extra...)
			}

			out, err := run(t, dir, append([]string{"__complete"}, ask("")...)...)
			if err != nil {
				t.Fatal(err)
			}
			offered := completionPairs(t, out)
			if len(offered) == 0 {
				t.Fatalf("no topics offered:\n%s", out)
			}
			if _, ok := offered[tc.want]; !ok {
				t.Errorf("%q is not offered as a topic of this document: %v", tc.want, offered)
			}
			if _, ok := offered[tc.absent]; ok {
				t.Errorf("%q belongs to the other document and is offered anyway: %v", tc.absent, offered)
			}

			// A topic that is offered has to work, or the completion is a list
			// of guesses.
			for topic, desc := range offered {
				if strings.TrimSpace(desc) == "" {
					t.Errorf("the topic %q is offered with no description", topic)
				}
				section, err := run(t, dir, ask(topic)...)
				if err != nil {
					t.Errorf("the offered topic %q was refused: %v", topic, err)
					continue
				}
				if strings.TrimSpace(section) == "" {
					t.Errorf("the offered topic %q printed nothing", topic)
				}
			}

			// So does a topic the refusal suggests.
			_, err = run(t, dir, ask("quidditch")...)
			if err == nil {
				t.Fatal("an unknown topic should be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal suggests nothing from this document: %v", err)
			}
			if strings.Contains(err.Error(), tc.absent) {
				t.Errorf("the refusal suggests %q, which is in the other document: %v", tc.absent, err)
			}
		})
	}
}

// TestHistoryReachesPlayersWithoutAProfile is F11. The log is the history and a
// profile is only a name to play under today: resolving --player against the
// profiles that still exist made a deleted player's games and every networked
// opponent's games unreachable, which are exactly the histories somebody needs
// to look a name up for.
func TestHistoryReachesPlayersWithoutAProfile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Balint", "Katalin"} {
		if _, err := run(t, dir, "profile", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	board, err := leaderboard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	played := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for _, r := range []leaderboard.Result{
		{
			Played:   played,
			Player:   "Katalin",
			Opponent: "Balint",
			Outcome:  leaderboard.Win,
			Side:     "vertical",
			Moves:    30,
			Ruleset:  game.Std.Canonical(),
		},
		{
			Played:   played.Add(time.Hour),
			Player:   "Balint",
			Opponent: leaderboard.RemoteName("Reka"),
			Outcome:  leaderboard.Loss,
			Side:     "horizontal",
			Moves:    20,
			Ruleset:  game.Std.Canonical(),
		},
	} {
		if err := board.Record(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, dir, "profile", "delete", "Katalin"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		asked string
	}{
		{"a deleted profile", "Katalin"},
		{"a networked opponent by name", "Reka"},
		{"a networked opponent as shown", "Reka (remote)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, dir, "leaderboard", "show", "--player", tc.asked)
			if err != nil {
				t.Fatalf("%s has games in the log and could not be asked about: %v", tc.asked, err)
			}
			if !strings.Contains(out, "Balint") {
				t.Errorf("the history of %s does not name the player they beat:\n%s", tc.asked, out)
			}
			if !strings.Contains(out, string(leaderboard.Win)) {
				t.Errorf("the history of %s does not read from their own side:\n%s", tc.asked, out)
			}
		})
	}

	offered := completionPairs(t, mustRun(t, dir, "__complete", "leaderboard", "show", "--player", ""))
	for _, want := range []string{"Balint", "Katalin", "Reka (remote)"} {
		desc, ok := offered[want]
		if !ok {
			t.Errorf("%q is not offered as a player to ask about: %v", want, offered)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("%q is offered with no description", want)
		}
	}
	if desc := offered["Katalin"]; !strings.Contains(desc, "no longer a profile") {
		t.Errorf("a name the log alone holds is offered as %q, which does not say so", desc)
	}

	// A local player and a networked one who go by the same name are two
	// people. Neither may answer for the other.
	if _, err := run(t, dir, "profile", "create", "Reka"); err != nil {
		t.Fatal(err)
	}
	local, err := run(t, dir, "leaderboard", "show", "--player", "Reka")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(local, "no recorded games") {
		t.Errorf("the local Reka was answered with the networked Reka's games:\n%s", local)
	}
	remote, err := run(t, dir, "leaderboard", "show", "--player", "Reka (remote)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote, "Balint") {
		t.Errorf("the networked Reka's game went missing once a local Reka existed:\n%s", remote)
	}
}

// TestProfileCompletionsStayDistinct is F23. The description was when the
// profile last played, and when it last played is not a distinguishing fact:
// profiles created by one script share a timestamp to the second, so a shell
// that groups its candidates by description offered one row for all of them.
func TestProfileCompletionsStayDistinct(t *testing.T) {
	dir := t.TempDir()
	// Carol is created last, so she is the current profile and the other two
	// are alike in every way a description used to mention.
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if _, err := run(t, dir, "profile", "create", name); err != nil {
			t.Fatal(err)
		}
	}

	offered := completionPairs(t, mustRun(t, dir, "__complete", "profile", "use", ""))
	if len(offered) != 3 {
		t.Fatalf("%d profiles offered, want 3: %v", len(offered), offered)
	}
	byDescription := make(map[string]string, len(offered))
	for value, desc := range offered {
		if strings.TrimSpace(desc) == "" {
			t.Errorf("%q is offered with no description", value)
			continue
		}
		if other, clash := byDescription[desc]; clash {
			t.Errorf("%q and %q are offered under the same description %q", other, value, desc)
		}
		byDescription[desc] = value
	}
	for _, want := range []string{"Alice", "Bob", "Carol"} {
		if _, ok := offered[want]; !ok {
			t.Errorf("%q is not offered, or is offered under a decorated value: %v", want, offered)
		}
	}
}

// TestThemeOverrideNamesTheThemeThatWasAskedFor is F20. The reason compared the
// theme in force against the saved choice, so "--theme paper" with colour
// suppressed reported that the saved classic was being overridden — naming a
// theme the player had not asked for as the one they had.
func TestThemeOverrideNamesTheThemeThatWasAskedFor(t *testing.T) {
	dir := t.TempDir()
	// An empty NO_COLOR is an unset one, which keeps --no-color the only reason
	// colour is off however the machine running the suite is set up.
	t.Setenv("NO_COLOR", "")

	asked, err := run(t, dir, "--no-color", "--theme", "paper", "theme", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "--theme paper") {
		t.Errorf("the override does not name the theme that was asked for:\n%s", asked)
	}
	if strings.Contains(asked, "classic") {
		t.Errorf("the override names the saved theme the request had already replaced:\n%s", asked)
	}
	if !strings.Contains(asked, "--no-color") {
		t.Errorf("the override does not say why it happened:\n%s", asked)
	}

	// With no theme asked for, the saved choice is what is being overridden.
	if _, err := run(t, dir, "theme", "set", "slate"); err != nil {
		t.Fatal(err)
	}
	saved, err := run(t, dir, "--no-color", "theme", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved, "the saved choice, slate") {
		t.Errorf("the override does not name the saved choice:\n%s", saved)
	}

	// Nothing is overridden when what was asked for is what is in force, and no
	// escape sequence reaches output that is not a terminal.
	for _, out := range []string{asked, saved} {
		if strings.Contains(out, "\x1b") {
			t.Errorf("redirected output carries escape sequences:\n%q", out)
		}
	}
	mono, err := run(t, dir, "--no-color", "--theme", "mono", "theme", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mono, "overriding") {
		t.Errorf("asking for the theme that is in force reported an override:\n%s", mono)
	}
}

// TestListingsAlignInDisplayCells is F09 on the command line. A name is not its
// byte count: a name carrying a combining accent has more bytes than cells, one
// written in a fullwidth script has more cells than characters, and the
// listings were measured in bytes, so every column after the name came out
// somewhere else on each row.
func TestListingsAlignInDisplayCells(t *testing.T) {
	dir := t.TempDir()
	// A name with a combining accent (more bytes than cells), one of two-cell
	// characters (more cells than runes), and a plain short one.
	for _, name := range []string{"Zso\u0301fia", "李雷", "Bo"} {
		if _, err := run(t, dir, "profile", "create", name); err != nil {
			t.Fatalf("creating %q: %v", name, err)
		}
	}

	profiles, err := run(t, dir, "profile", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertColumnAligned(t, "profile list", profiles, regexp.MustCompile(`\d{4}-\d{2}-\d{2}|CREATED`))

	lbSeed(t, dir, "李雷", "Zso\u0301fia")
	standings, err := run(t, dir, "leaderboard", "show")
	if err != nil {
		t.Fatal(err)
	}
	assertColumnAligned(t, "leaderboard show", standings, regexp.MustCompile(`RATING|\b1[0-9]{3}\b`))
}

// assertColumnAligned checks that the column found by match starts at the same
// display cell on every line that has one, which is what a reader means by a
// column.
func assertColumnAligned(t *testing.T, what, out string, match *regexp.Regexp) {
	t.Helper()
	starts := make(map[int][]string)
	for _, line := range strings.Split(out, "\n") {
		loc := match.FindStringIndex(line)
		if loc == nil {
			continue
		}
		at := ansi.StringWidth(line[:loc[0]])
		starts[at] = append(starts[at], line)
	}
	if len(starts) == 0 {
		t.Fatalf("%s: no column to measure in:\n%s", what, out)
	}
	if len(starts) > 1 {
		t.Errorf("%s: the column starts at %d different places: %v\n%s", what, len(starts), starts, out)
	}
}

// mustRun runs the command line and fails the test if it is refused.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}
