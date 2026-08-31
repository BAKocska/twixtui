package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// run executes the command line with an isolated configuration directory and
// returns what it printed.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--config", dir}, args...))
	err := root.Execute()
	return out.String(), err
}

// TestHelpNamesEveryCommandWithItsPurpose covers the requirement that help
// explains what each command is for, not merely that it exists.
func TestHelpNamesEveryCommandWithItsPurpose(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, name := range []string{
		"play", "learn", "profile", "leaderboard", "game", "rules", "serve", "theme",
		"completion", "version",
	} {
		if !strings.Contains(text, name) {
			t.Errorf("help does not mention the %q command", name)
		}
	}

	// Every command must carry a short description, since an undescribed command
	// in a list of a dozen tells a newcomer nothing.
	var missing []string
	walk(root, func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			return
		}
		if strings.TrimSpace(c.Short) == "" {
			missing = append(missing, c.CommandPath())
		}
	})
	if len(missing) > 0 {
		t.Errorf("commands with no description: %v", missing)
	}
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, child := range c.Commands() {
		walk(child, fn)
	}
}

// TestEveryFlagIsDescribed checks the flag help, which is what completion shows.
func TestEveryFlagIsDescribed(t *testing.T) {
	root := NewRootCommand()
	var missing []string
	walk(root, func(c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			if strings.TrimSpace(f.Usage) == "" {
				missing = append(missing, c.CommandPath()+" --"+f.Name)
			}
		})
	})
	if len(missing) > 0 {
		t.Errorf("flags with no description: %v", missing)
	}
}

// TestCompletionScriptsAreProduced checks each shell gets a script, and that the
// bash one is the generation that carries descriptions rather than the older one
// that has none.
func TestCompletionScriptsAreProduced(t *testing.T) {
	dir := t.TempDir()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, err := run(t, dir, "completion", shell)
		if err != nil {
			t.Errorf("completion %s: %v", shell, err)
			continue
		}
		if len(out) < 200 {
			t.Errorf("completion %s produced only %d bytes", shell, len(out))
		}
		if !strings.Contains(out, "twixtui") {
			t.Errorf("completion %s does not mention the program", shell)
		}
	}
	bash, err := run(t, dir, "completion", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash, "completion V2") {
		t.Error("the bash script is not the generation that carries descriptions")
	}
	if _, err := run(t, dir, "completion", "tcsh"); err == nil {
		t.Error("an unsupported shell should be refused")
	}
}

// TestValueCompletionsCarryDescriptions is the substance of the completion
// requirement: pressing TAB must explain the options, not only list them.
func TestValueCompletionsCarryDescriptions(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"bot tiers", []string{"__complete", "play", "bot", "--tier", ""}},
		{"rulesets", []string{"__complete", "play", "bot", "--ruleset", ""}},
		{"sides", []string{"__complete", "play", "bot", "--side", ""}},
		{"themes", []string{"__complete", "theme", "set", ""}},
		{"lessons", []string{"__complete", "learn", ""}},
		{"profiles", []string{"__complete", "profile", "use", ""}},
	}
	for _, c := range cases {
		out, err := run(t, dir, c.args...)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		lines := completionLines(out)
		if len(lines) == 0 {
			t.Errorf("%s: no completions offered", c.name)
			continue
		}
		for _, line := range lines {
			if !strings.Contains(line, "\t") {
				t.Errorf("%s: completion %q has no description", c.name, line)
			}
		}
	}
}

// completionLines returns the candidate lines of a __complete response, dropping
// the trailing directive line.
func completionLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion ended") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// TestProfileCompletionIsFuzzy checks the completion helps someone who
// misremembers their own name, which is the reason it exists.
func TestProfileCompletionIsFuzzy(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Balint", "Katalin", "Zsofia"} {
		if _, err := run(t, dir, "profile", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	for query, want := range map[string]string{
		"blaint": "Balint",  // transposed
		"katlin": "Katalin", // missing letter
		"zsof":   "Zsofia",  // prefix
		"BALINT": "Balint",  // wrong case
	} {
		out, err := run(t, dir, "__complete", "profile", "use", query)
		if err != nil {
			t.Errorf("%q: %v", query, err)
			continue
		}
		lines := completionLines(out)
		if len(lines) == 0 {
			t.Errorf("%q offered no completion", query)
			continue
		}
		if !strings.HasPrefix(lines[0], want) {
			t.Errorf("%q completed to %q first, want %q", query, lines[0], want)
		}
	}
}

// TestRulesShowFiltersByTopic checks the topic filter returns the right section
// and refuses an unknown one with something useful.
func TestRulesShowFiltersByTopic(t *testing.T) {
	dir := t.TempDir()
	full, err := run(t, dir, "rules", "show")
	if err != nil {
		t.Fatal(err)
	}
	if len(full) < 2000 {
		t.Fatalf("the rules document is only %d bytes; is it embedded?", len(full))
	}

	section, err := run(t, dir, "rules", "show", "board")
	if err != nil {
		t.Fatal(err)
	}
	if len(section) >= len(full) {
		t.Error("a topic did not narrow the output")
	}
	if !strings.Contains(strings.ToLower(section), "board") {
		t.Error("the board section does not mention the board")
	}

	if _, err := run(t, dir, "rules", "show", "quidditch"); err == nil {
		t.Error("an unknown topic should be refused")
	} else if !strings.Contains(err.Error(), "board") {
		t.Errorf("the refusal should suggest real topics, got %v", err)
	}

	prov, err := run(t, dir, "rules", "show", "--provenance")
	if err != nil {
		t.Fatal(err)
	}
	if prov == full {
		t.Error("--provenance printed the same document as the rules")
	}
}

// TestThemeCommands covers listing, choosing and showing, including the case
// where the theme in force is not the chosen one.
func TestThemeCommands(t *testing.T) {
	dir := t.TempDir()
	list, err := run(t, dir, "theme", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"classic", "slate", "paper", "mono"} {
		if !strings.Contains(list, name) {
			t.Errorf("theme list does not mention %q", name)
		}
	}
	if !strings.Contains(list, "*") {
		t.Error("theme list does not mark the current theme")
	}

	if _, err := run(t, dir, "theme", "set", "paper"); err != nil {
		t.Fatal(err)
	}
	show, err := run(t, dir, "theme", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "paper") {
		t.Errorf("theme show does not report the chosen theme: %q", show)
	}

	// An override must be explained rather than silently applied, or it looks
	// like the choice was ignored.
	overridden, err := run(t, dir, "--no-color", "theme", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overridden, "mono") {
		t.Errorf("--no-color did not take effect: %q", overridden)
	}
	if !strings.Contains(overridden, "paper") || !strings.Contains(overridden, "no-color") {
		t.Errorf("the override was not explained: %q", overridden)
	}

	if _, err := run(t, dir, "theme", "set", "chartreuse"); err == nil {
		t.Error("an unknown theme should be refused")
	}
}

// TestProfileCommands covers the lifecycle and that the loose match refuses an
// ambiguous query rather than guessing.
func TestProfileCommands(t *testing.T) {
	dir := t.TempDir()
	if out, err := run(t, dir, "profile", "list"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "no profiles yet") {
		t.Errorf("a fresh install should say so: %q", out)
	}
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, dir, "profile", "whoami"); err != nil || !strings.Contains(out, "Balint") {
		t.Errorf("whoami = %q, %v", out, err)
	}
	if _, err := run(t, dir, "profile", "create", "balint"); err == nil {
		t.Error("a name differing only in case should be refused as a duplicate")
	}
	if _, err := run(t, dir, "profile", "create", "   "); err == nil {
		t.Error("a blank name should be refused")
	}
	if _, err := run(t, dir, "profile", "create", "Balnit"); err != nil {
		t.Fatal(err)
	}
	// "bal" now matches both, so it must be refused rather than guessed at.
	if _, err := run(t, dir, "profile", "use", "bal"); err == nil {
		t.Error("an ambiguous name should be refused")
	}
	if _, err := run(t, dir, "profile", "rename", "Balnit", "Katalin"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(t, dir, "profile", "list"); !strings.Contains(out, "Katalin") {
		t.Errorf("rename did not take: %q", out)
	}
	if _, err := run(t, dir, "profile", "delete", "Katalin"); err != nil {
		t.Fatal(err)
	}
}

// TestLeaderboardResetNeedsConfirmation checks a destructive command cannot be
// triggered by a mistyped one.
func TestLeaderboardResetNeedsConfirmation(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "leaderboard", "reset"); err == nil {
		t.Error("reset should require confirmation")
	}
	if _, err := run(t, dir, "leaderboard", "reset", "--yes"); err != nil {
		t.Errorf("reset with confirmation: %v", err)
	}
}

// TestGameCommandsOnAnEmptyStore checks the listings explain themselves rather
// than printing nothing.
func TestGameCommandsOnAnEmptyStore(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "game", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no saved games") {
		t.Errorf("game list on an empty store = %q", out)
	}
	if _, err := run(t, dir, "game", "show", "nope"); err == nil {
		t.Error("showing a game that does not exist should be refused")
	}
}

// TestImportRefusesATamperedRecord checks the integrity check is wired into the
// command, not merely present in the engine.
func TestImportRefusesATamperedRecord(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("twixtui-record 1\nmoves D4\ndigest 0000\n"))
	root.SetArgs([]string{"--config", dir, "game", "import", "-"})
	if err := root.Execute(); err == nil {
		t.Error("importing a malformed record should be refused")
	}
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	SetBuildInfo("1.2.3", "abcdef", "2026-01-01")
	t.Cleanup(func() { SetBuildInfo("dev", "none", "unknown") })
	out, err := run(t, t.TempDir(), "version")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1.2.3", "abcdef", "2026-01-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output %q does not contain %q", out, want)
		}
	}
}

// TestPlayBotRequiresASideChoice covers the requirement that the player picks
// their colour rather than being given one.
func TestPlayBotRequiresASideChoice(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, dir, "play", "bot")
	if err == nil {
		t.Fatal("starting a bot game without choosing a side should be refused")
	}
	if !strings.Contains(err.Error(), "--side") {
		t.Errorf("the refusal should name the flag, got %v", err)
	}
}

// TestUnknownRulesetAndTierAreRefused checks bad values fail before a game
// starts rather than part way through one.
func TestUnknownRulesetAndTierAreRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, dir, "play", "bot", "--side", "vertical", "--ruleset", "nonsense"); err == nil {
		t.Error("an unknown ruleset should be refused")
	}
	if _, err := run(t, dir, "play", "bot", "--side", "vertical", "--tier", "nonsense"); err == nil {
		t.Error("an unknown tier should be refused")
	}
	if _, err := run(t, dir, "play", "bot", "--side", "vertical", "--size", "3"); err == nil {
		t.Error("an impossible board size should be refused")
	}
}
