package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/profile"
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

// TestUnknownThemeIsRefusedWhereverTheOutputGoes is F-10. The monochrome
// short-circuit used to run before the named theme was resolved, so a
// misspelled --theme was refused at a terminal and silently ignored the moment
// the output was redirected — which is precisely where a script would have to
// notice it. A test's own stdout is a pipe, so the plain case below is the one
// that used to pass with the typo intact; --no-color forces the short-circuit
// whatever the machine running the suite does with its output.
func TestUnknownThemeIsRefusedWhereverTheOutputGoes(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--theme", "zzz", "theme", "show"},
		{"--no-color", "--theme", "zzz", "theme", "show"},
		// The value is wrong before anything asks what colour to draw in, so a
		// command that never consults a theme has to refuse it too.
		{"--theme", "zzz", "profile", "list"},
	} {
		out, err := run(t, dir, args...)
		if err == nil {
			t.Errorf("%v accepted an unknown theme: %q", args, out)
			continue
		}
		if !strings.Contains(err.Error(), `"zzz"`) || !strings.Contains(err.Error(), "classic") {
			t.Errorf("%v: the refusal should name the value and the known themes: %v", args, err)
		}
	}

	// The command line refuses the value up front, but the resolver has to hold
	// the same rule on its own: a caller that asks for a theme while colour is
	// suppressed must still be told the name is wrong, or the ordering defect
	// returns the moment something asks without going through the flag check.
	if got, err := (&options{configDir: dir, themeName: "zzz", noColor: true}).theme(); err == nil {
		t.Errorf("theme() answered %q for an unknown name because the run was monochrome", got.Name)
	}

	// Refusing the typo must not have become refusing the flag: a known theme
	// still goes through, and still yields to the redirected output.
	out, err := run(t, dir, "--theme", "paper", "theme", "show")
	if err != nil {
		t.Fatalf("a known --theme was refused: %v", err)
	}
	if !strings.Contains(out, "mono") {
		t.Errorf("colour survived a pipe: %q", out)
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

// TestProfileFlagDoesNotChangeTheStoredChoice is F-2. A --profile flag is an
// override for one run, and it used to call UseCurrent on the way past, so a
// single scripted game permanently retargeted the next interactive one — the
// outcome the contract at the top of current.go says the design rules out.
func TestProfileFlagDoesNotChangeTheStoredChoice(t *testing.T) {
	dir := t.TempDir()
	// Bob is created last, so Bob is the stored choice.
	for _, name := range []string{"Alice", "Bob"} {
		if _, err := run(t, dir, "profile", "create", name); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	o := &options{configDir: dir, profile: "Alice"}
	_, playing, err := o.requireProfile()
	if err != nil {
		t.Fatal(err)
	}
	if playing != "Alice" {
		t.Fatalf("--profile did not take effect for this run: playing as %q", playing)
	}

	out, err := run(t, dir, "profile", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "Bob" {
		t.Errorf("the stored choice after --profile Alice = %q, want Bob", got)
	}

	// Having played is a fact about the profile, so it is still recorded as
	// used; only the machine's choice of who is playing is left alone. Dropping
	// the one with the other would reorder the list a player picks from.
	store, err := profile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	alice, ok := store.Get("Alice")
	if !ok {
		t.Fatal("Alice went missing")
	}
	bob, ok := store.Get("Bob")
	if !ok {
		t.Fatal("Bob went missing")
	}
	if !alice.LastUsed.After(bob.LastUsed) {
		t.Errorf("the profile that played was not marked as used: Alice %v, Bob %v",
			alice.LastUsed, bob.LastUsed)
	}
}

// TestProfileFlagRefusesWhatProfileUseRefuses is F-3. The same string cannot
// name a profile for one command and not for another: --profile used to absorb
// every miss the loose search could not rescue by creating a new identity,
// silently, which is how a typo splits a player's history in two.
func TestProfileFlagRefusesWhatProfileUseRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		query    string
	}{
		{"no profile matches", []string{"Alice"}, "Alicia"},
		{"several profiles match", []string{"Alice", "Alicia"}, "Ali"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.existing {
				if _, err := run(t, dir, "profile", "create", name); err != nil {
					t.Fatalf("creating %s: %v", name, err)
				}
			}

			useOut, useErr := run(t, dir, "profile", "use", tc.query)
			if useErr == nil {
				t.Fatalf("profile use %s was accepted, so there is nothing to compare: %q",
					tc.query, useOut)
			}

			o := &options{configDir: dir, profile: tc.query}
			_, _, flagErr := o.requireProfile()
			if flagErr == nil {
				t.Fatalf("--profile %s was accepted where profile use refused it", tc.query)
			}
			if flagErr.Error() != useErr.Error() {
				t.Errorf("the two surfaces disagree about %s:\n  profile use: %v\n  --profile:   %v",
					tc.query, useErr, flagErr)
			}

			store, err := profile.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := store.Get(tc.query); ok {
				t.Errorf("--profile %s created the profile %q", tc.query, p.Name)
			}
			if got := len(store.List()); got != len(tc.existing) {
				t.Errorf("%d profiles exist, want %d", got, len(tc.existing))
			}
		})
	}
}

// TestProfileFlagCreatesTheFirstProfile keeps the one write the flag may still
// make. On a machine with no profiles there is no stored choice to retarget and
// no other name the player could have meant, which is what lets a new player go
// from install to a game in a single command.
func TestProfileFlagCreatesTheFirstProfile(t *testing.T) {
	dir := t.TempDir()
	o := &options{configDir: dir, profile: "Alice"}
	_, playing, err := o.requireProfile()
	if err != nil {
		t.Fatal(err)
	}
	if playing != "Alice" {
		t.Fatalf("playing as %q, want Alice", playing)
	}
	out, err := run(t, dir, "profile", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "Alice" {
		t.Errorf("the profile the flag created was not adopted: whoami = %q", got)
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

// lbSeed records finished games the named player lost, so the standings have
// something to show.
func lbSeed(t *testing.T, dir string, player string, opponents ...string) {
	t.Helper()
	board, err := leaderboard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, opponent := range opponents {
		if err := board.Record(leaderboard.Result{
			Played:   time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour),
			Player:   player,
			Opponent: opponent, Outcome: leaderboard.Loss, Side: "vertical", Moves: 4,
			Ruleset: game.Std.Canonical(),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// lbLine returns the printed line naming want.
func lbLine(t *testing.T, out, want string) (int, string) {
	t.Helper()
	for i, l := range strings.Split(out, "\n") {
		if strings.Contains(l, want) {
			return i, l
		}
	}
	t.Fatalf("no line for %q in:\n%s", want, out)
	return 0, ""
}

// TestLeaderboardShowDoesNotRankBotsWithPeople is F4 on the command line. A
// tier's rating is a constant, so ranking it beside earned ratings printed a
// player who had lost every game above the bots that beat them, and made a tier
// look as though it had gained four hundred points in one game.
func TestLeaderboardShowDoesNotRankBotsWithPeople(t *testing.T) {
	dir := t.TempDir()
	beginner := leaderboard.BotName("beginner")
	intermediate := leaderboard.BotName("intermediate")
	lbSeed(t, dir, "Balint", beginner, intermediate)

	out, err := run(t, dir, "leaderboard", "show")
	if err != nil {
		t.Fatal(err)
	}
	playerAt, playerLine := lbLine(t, out, "Balint")
	for _, stored := range []string{beginner, intermediate} {
		botAt, _ := lbLine(t, out, leaderboard.DisplayName(stored))
		if botAt < playerAt {
			t.Errorf("%s is ranked above the player it beat:\n%s", stored, out)
		}
		if strings.Contains(out, stored) {
			t.Errorf("the stored spelling %q is printed:\n%s", stored, out)
		}
	}
	if fields := strings.Fields(playerLine); fields[0] != "Balint" {
		t.Errorf("the only player is given the position %q:\n%s", fields[0], out)
	}
	if !strings.Contains(out, "not ranked") {
		t.Errorf("nothing says the bots are outside the ranking:\n%s", out)
	}
	if got := strings.Fields(strings.Split(out, "\n")[0])[0]; got != "PLAYER" {
		t.Errorf("the heading of a one-player board starts with %q, want no position column:\n%s", got, out)
	}
}

// TestLeaderboardShowRanksPeopleAgainstEachOther: the position column is
// withheld only while there is nobody to hold a position against.
func TestLeaderboardShowRanksPeopleAgainstEachOther(t *testing.T) {
	dir := t.TempDir()
	lbSeed(t, dir, "Balint", "Reka")

	out, err := run(t, dir, "leaderboard", "show")
	if err != nil {
		t.Fatal(err)
	}
	_, winner := lbLine(t, out, "Reka")
	_, loser := lbLine(t, out, "Balint")
	if got := strings.Fields(winner)[0]; got != "1" {
		t.Errorf("the winner is at position %q, want 1:\n%s", got, out)
	}
	if got := strings.Fields(loser)[0]; got != "2" {
		t.Errorf("the loser is at position %q, want 2:\n%s", got, out)
	}
}

// TestLeaderboardHistoryIsPrintedFromTheAskedPlayersSide: every game is recorded
// once, by one of its two players, so a listing that prints the stored row as it
// stands showed the other player as their own opponent and gave them the
// recorder's result. Balint, who lost, was told he had won against himself.
func TestLeaderboardHistoryIsPrintedFromTheAskedPlayersSide(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	lbSeed(t, dir, "Balint", leaderboard.BotName("beginner"))
	board, err := leaderboard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Recorded by Reka, from her side: her win, from the vertical seat.
	if err := board.Record(leaderboard.Result{
		Played: time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
		Player: "Reka", Opponent: "Balint", Outcome: leaderboard.Win,
		Side: "vertical", Moves: 40, Ruleset: game.Std.Canonical(),
	}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, dir, "leaderboard", "show", "--player", "Balint")
	if err != nil {
		t.Fatal(err)
	}
	// A row reads: date time opponent... side result moves. The opponent is the
	// only field that can be more than one word, so the rest are counted from
	// the end.
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "WHEN" {
			continue
		}
		rows++
		opponent := strings.Join(f[2:len(f)-3], " ")
		side, outcome := f[len(f)-3], f[len(f)-2]
		if opponent == "Balint" {
			t.Errorf("Balint's history lists him as his own opponent:\n%s", out)
		}
		if opponent == "Reka" {
			if outcome != string(leaderboard.Loss) {
				t.Errorf("the game Reka recorded as her win reads as a %q for Balint:\n%s", outcome, out)
			}
			if side != "horizontal" {
				t.Errorf("Balint played %q in a game Reka recorded from the vertical seat:\n%s", side, out)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("%d games listed, want the two Balint played:\n%s", rows, out)
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

// TestUnknownSubcommandIsRefused covers the requirement that a group of
// subcommands refuses one it does not have. Printing the group's help and
// reporting success makes a typo indistinguishable from a command that worked,
// which no script can recover from.
func TestUnknownSubcommandIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		near string
	}{
		{[]string{"play", "boot"}, "bot"},
		{[]string{"game", "shwo"}, "show"},
		{[]string{"profile", "lst"}, "list"},
		{[]string{"paly"}, "play"},
	} {
		out, err := run(t, dir, tc.args...)
		if err == nil {
			t.Errorf("%v was accepted; it printed %q", tc.args, out)
			continue
		}
		if !strings.Contains(err.Error(), tc.near) {
			t.Errorf("%v was refused without offering %q: %v", tc.args, tc.near, err)
		}
	}

	// A group asked for nothing in particular still explains itself, which is
	// what someone exploring the command line is doing.
	for _, group := range []string{"play", "game", "profile", "rules", "theme"} {
		out, err := run(t, dir, group)
		if err != nil {
			t.Errorf("%q on its own failed: %v", group, err)
		}
		if !strings.Contains(out, "Commands:") {
			t.Errorf("%q on its own did not list its subcommands: %q", group, out)
		}
	}
}

// TestGameShowPrintsTheWholeBoard covers what a listing is for: the game is
// being read, not played, so every row is printed rather than a viewport's worth
// with a scroll marker standing in for the rest.
func TestGameShowPrintsTheWholeBoard(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	id := importedGame(t, dir, game.Std, "M13; K14; N15; P14")

	out, err := run(t, dir, "game", "show", id)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		labels[strings.TrimSpace(line[:3])] = true
	}
	for row := 1; row <= game.Std.Size; row++ {
		if !labels[strconv.Itoa(row)] {
			t.Errorf("row %d of the %d-row board is missing:\n%s", row, game.Std.Size, out)
		}
	}
	for _, marker := range []string{"↑", "↓", "←", "→"} {
		if strings.Contains(out, marker) {
			t.Errorf("the listing carries the scroll marker %q, so part of the board is not printed:\n%s", marker, out)
		}
	}
}

// importedGame stores a game played through the given transcript and returns the
// identifier it was saved under.
func importedGame(t *testing.T, dir string, rs game.Ruleset, transcript string) string {
	t.Helper()
	g, err := game.ReplayTranscript(rs, transcript)
	if err != nil {
		t.Fatal(err)
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "game.record")
	if err := os.WriteFile(path, []byte(record.Encode()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dir, "game", "import", path)
	if err != nil {
		t.Fatal(err)
	}
	// "imported as <id>: 4 moves, still being played"
	fields := strings.Fields(out)
	if len(fields) < 3 {
		t.Fatalf("the import said %q, which does not name the game", out)
	}
	return strings.TrimSuffix(fields[2], ":")
}

// TestRulesShowKeepsMarkdownForAPipe covers the half of the rules output that is
// not being read by a person: whatever is on the other end of the pipe asked for
// the document, so it arrives as it stands.
func TestRulesShowKeepsMarkdownForAPipe(t *testing.T) {
	out, err := run(t, t.TempDir(), "rules", "show")
	if err != nil {
		t.Fatal(err)
	}
	if out != docsRules {
		t.Error("the document was altered on its way into a pipe")
	}
}

// TestRenderMarkdownLaysDocumentsOutForATerminal covers the other half: a reader
// at a terminal is given prose, with the structure carried by case, indentation
// and rules so that it survives a terminal with no colour.
func TestRenderMarkdownLaysDocumentsOutForATerminal(t *testing.T) {
	const doc = `# The board

The board is a square grid of holes, 24×24 by default, and this paragraph is
deliberately long enough that it has to be folded onto several lines at every
measure worth testing, including a wide one.

## Links

Under the default ` + "`std`" + ` ruleset this includes **your own** links, which is the
*whole* point of the section.

- first item, long enough to need a second line of its own at a narrow measure
  and carrying on across a line break in the source
- second item

| Prefix | Meaning |
|---|---|
| ` + "`~`" + ` | decline an offered link |

` + "```" + `
D4
` + "```" + `
`
	for _, width := range []int{40, 80, 200} {
		out := renderMarkdown(doc, width)
		for _, markup := range []string{"#", "*", "`", "|"} {
			if strings.Contains(out, markup) {
				t.Errorf("at %d columns the markup %q reached the terminal:\n%s", width, markup, out)
			}
		}
		for _, words := range []string{"THE BOARD", "LINKS", "your own", "whole", "std", "decline an offered link", "D4"} {
			if !strings.Contains(out, words) {
				t.Errorf("at %d columns %q was lost in the rendering:\n%s", width, words, out)
			}
		}
		measure := min(width, proseMeasure)
		for _, line := range strings.Split(out, "\n") {
			if got := ansi.StringWidth(line); got > measure {
				t.Errorf("at %d columns a line is %d cells wide, past the %d-column measure: %q",
					width, got, measure, line)
			}
		}
		if !strings.Contains(out, "\n---") && !strings.Contains(out, "\n===") {
			t.Errorf("at %d columns no heading is ruled off, so the structure is invisible:\n%s", width, out)
		}
	}

	// The measure is capped rather than following the terminal, because a
	// 200-column line is hard to read back to the start of.
	widest := 0
	for _, line := range strings.Split(renderMarkdown(doc, 200), "\n") {
		widest = max(widest, ansi.StringWidth(line))
	}
	if widest > proseMeasure {
		t.Errorf("a 200-column terminal was given lines %d cells wide", widest)
	}
	if widest < 60 {
		t.Errorf("a 200-column terminal was given lines only %d cells wide", widest)
	}
}

// TestRenderedDocumentsKeepTheMeasure holds the layout invariant on the real
// documents, at the widths a terminal is actually likely to be: a line wider
// than the measure is folded a second time by the terminal, which undoes the
// wrapping.
func TestRenderedDocumentsKeepTheMeasure(t *testing.T) {
	for _, doc := range []struct {
		name string
		text string
	}{
		{"rules", docsRules},
		{"provenance", docsProvenance},
	} {
		for _, width := range []int{40, 60, 80, 100, 200} {
			measure := min(width, proseMeasure)
			for i, line := range strings.Split(renderMarkdown(doc.text, width), "\n") {
				if got := ansi.StringWidth(line); got > measure {
					t.Errorf("%s at %d columns: line %d is %d cells wide, past the %d-column measure: %q",
						doc.name, width, i+1, got, measure, line)
				}
			}
		}
	}
}

// TestVersionOnAnUnstampedBuild covers the build nobody released: the three
// placeholder fields are not facts and printing them as though they were reads
// as a bug.
func TestVersionOnAnUnstampedBuild(t *testing.T) {
	SetBuildInfo(unstampedVersion, "none", "unknown")
	out, err := run(t, t.TempDir(), "version")
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"dev", "(none)", "unknown"} {
		if strings.Contains(out, placeholder) {
			t.Errorf("the version reports the placeholder %q: %q", placeholder, out)
		}
	}
	if !strings.Contains(out, "built from source") {
		t.Errorf("the version does not say where the build came from: %q", out)
	}
}

// TestLastPlayedIsSingularForOneMinute keeps the command line on the shared
// relative-time formatter: the same fact was rendered "1 minutes ago" here and
// "1 minute ago" in the interface.
func TestLastPlayedIsSingularForOneMinute(t *testing.T) {
	p := profile.Profile{Name: "Balint", LastUsed: time.Now().Add(-95 * time.Second)}
	if got, want := lastPlayedColumn(p), "1 minute ago"; got != want {
		t.Errorf("the last-played column says %q, want %q", got, want)
	}
	if got, want := lastPlayed(p), "last played 1 minute ago"; got != want {
		t.Errorf("the completion description says %q, want %q", got, want)
	}
}

// TestOutputToAPipeCarriesNoEscapeSequences pins the rule that colour is for a
// terminal. Redirected into a file or piped into another command, escape
// sequences are noise at best and corrupt the data at worst — and a listing
// nobody can assert on is a listing nobody can script against.
//
// This is also the difference that made a test pass on one machine and fail on
// another: colour was decided by the environment alone, so a shell with NO_COLOR
// set produced clean output and one without it did not.
func TestOutputToAPipeCarriesNoEscapeSequences(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	rs := game.Std
	rs.Size = 12
	id := importedGame(t, dir, rs, "F7; G8; H9; J10")

	for _, args := range [][]string{
		{"game", "show", id},
		{"game", "list"},
		{"leaderboard", "show"},
		{"profile", "list"},
		{"theme", "list"},
		{"rules", "show", "board"},
	} {
		out, err := run(t, dir, args...)
		if err != nil {
			t.Errorf("%v: %v", args, err)
			continue
		}
		if strings.ContainsRune(out, 0x1b) {
			t.Errorf("%v wrote an escape sequence to a pipe:\n%q", args, out)
		}
	}
}
