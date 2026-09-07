package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/profile"
)

// runBare runs the command line exactly as it is given, without the isolated
// configuration directory run adds. A test of the global flags themselves has
// to be able to leave --config out, or pass it a value of its own choosing.
//
// Every case below runs a command that reads no configuration, so nothing here
// can reach the real configuration directory even when it resolves one.
func runBare(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// TestExplicitlyEmptyFlagValuesAreRefused covers the difference between leaving
// a flag out and giving it nothing. Every value below arrives at the code that
// reads it as the same empty string a default does, so an empty one used to be
// obeyed as "whatever you would have done anyway": --config wrote to the real
// configuration directory, --profile played as whoever the machine last chose,
// --out wrote to standard output. The refusal has to come before any of that
// happens, and it must not cost the omitted flag its default.
func TestExplicitlyEmptyFlagValuesAreRefused(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"config", []string{"--config", "", "version"}},
		{"config whitespace", []string{"--config", "   ", "version"}},
		{"theme", []string{"--theme", "", "version"}},
		{"profile", []string{"--profile", "", "version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runBare(t, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted: %q", tc.args, out)
			}
			flag := tc.args[0]
			if !strings.Contains(err.Error(), flag) {
				t.Errorf("the refusal does not name %s: %v", flag, err)
			}
		})
	}

	// The same values, on the commands that would act on them. Each refusal
	// has to be of the value that was given: "game export --out ''" reaching
	// the command and failing on the game identifier instead would leave the
	// empty output path unproven.
	for _, tc := range []struct {
		name string
		flag string
		args []string
	}{
		{"player", "--player", []string{"leaderboard", "show", "--player", ""}},
		{"out", "--out", []string{"game", "export", "--out", "", "whatever"}},
		{"profile on a command that plays", "--profile", []string{"--profile", "", "profile", "whoami"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, dir, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted: %q", tc.args, out)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("%v was refused for something other than %s: %v", tc.args, tc.flag, err)
			}
		})
	}

	// The refusal must be of the empty value, not of the flag: omitting these
	// still means what it documents.
	for _, args := range [][]string{
		{"profile", "list"},
		{"leaderboard", "show"},
		{"leaderboard", "show", "--limit", "0"},
	} {
		if out, err := run(t, dir, args...); err != nil {
			t.Errorf("%v was refused: %v\n%s", args, err, out)
		}
	}
}

// TestCompletionStillBrowsesWithAnEmptyValue is the control on the refusal
// above. A shell asking what could follow "--profile" sends the empty string it
// has so far, and so does a request for the topics of "rules show
// --provenance": a value check that could not tell those apart from a command
// line would turn every one of these listings into an error and leave TAB dead.
func TestCompletionStillBrowsesWithAnEmptyValue(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Balint"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"__complete", "--profile", ""},
		{"__complete", "profile", "use", ""},
		{"__complete", "leaderboard", "show", "--player", ""},
		{"__complete", "rules", "show", "--provenance", ""},
		{"__complete", "--theme", ""},
	} {
		out, err := run(t, dir, args...)
		if err != nil {
			t.Errorf("%v: %v", args, err)
			continue
		}
		if len(completionLines(out)) == 0 {
			t.Errorf("%v offered nothing:\n%s", args, out)
		}
	}
}

// TestBlankConfigDirectoryInTheEnvironmentIsRefused covers the variable an
// isolated run depends on. A script that exports it from a shell variable
// nobody set exports an empty one, and reading that as "no directory named"
// puts the script's profiles, games and results into the player's own
// configuration directory — the single outcome the variable exists to prevent.
// It is refused by every command rather than only by the ones that open a file,
// because a script is the place where nobody is watching the output.
func TestBlankConfigDirectoryInTheEnvironmentIsRefused(t *testing.T) {
	t.Setenv(configEnv, "")
	out, err := runBare(t, "version")
	if err == nil {
		t.Fatalf("a blank %s was accepted: %q", configEnv, out)
	}
	if !strings.Contains(err.Error(), configEnv) {
		t.Errorf("the refusal does not name the variable: %v", err)
	}

	t.Setenv(configEnv, t.TempDir())
	if out, err := runBare(t, "profile", "list"); err != nil {
		t.Errorf("a directory named in the environment was refused: %v\n%s", err, out)
	}
}

// TestBlankProfileSelectorIsRefusedBeforeItPlaysAsAnybody is F08. The loose
// search answers a blank query with every profile, which is what makes it the
// browsable list behind TAB completion — and read as a selection, that answer
// means "the only profile" on a machine with one. So a blank --profile played
// as, and marked as played, whoever happened to be there.
func TestBlankProfileSelectorIsRefusedBeforeItPlaysAsAnybody(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}
	store, err := profile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := store.Get("Alice")
	if !ok {
		t.Fatal("Alice went missing")
	}

	// Inside options an empty string is what an absent flag looks like, so the
	// blank values a resolver can be handed are the whitespace ones. The
	// command line refuses "--profile ''" before it reaches any of this, which
	// is what the next test is about.
	for _, query := range []string{"   ", " \t "} {
		o := &options{configDir: dir, profile: query}
		if _, playing, err := o.requireProfile(); err == nil {
			t.Errorf("--profile %q played as %q", query, playing)
		}
	}
	for _, query := range []string{"", "   ", " \t "} {
		if _, err := resolveProfileName(store, query); err == nil {
			t.Errorf("the selector accepted %q", query)
		}
		if out, err := run(t, dir, "profile", "use", query); err == nil {
			t.Errorf("profile use %q was accepted: %q", query, out)
		}
	}

	after, ok := store.Get("Alice")
	if !ok {
		t.Fatal("Alice went missing")
	}
	if !after.LastUsed.Equal(before.LastUsed) {
		t.Errorf("a refused selector still marked Alice as having played: %v then %v",
			before.LastUsed, after.LastUsed)
	}

	// The refusal is of the blank value alone: a name still selects.
	if out, err := run(t, dir, "profile", "use", "Alice"); err != nil {
		t.Errorf("a named profile was refused: %v\n%s", err, out)
	}
}

// TestBlankProfileFlagDoesNotFallBackToTheCurrentProfile pins the half of F08
// that a refusal alone does not: the flag must not quietly mean "whoever is
// current", which is what it meant when an empty value was read as an absent
// one.
func TestBlankProfileFlagDoesNotFallBackToTheCurrentProfile(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dir, "--profile", "", "profile", "whoami")
	if err == nil {
		t.Fatalf("an empty --profile answered with a profile: %q", out)
	}
	if strings.Contains(out, "Alice") {
		t.Errorf("an empty --profile still played as the current profile: %q", out)
	}
}

// TestNegativeListingLimitIsRefusedAndZeroMeansAll is F24. A limit is a count
// of rows, and a negative one was read as "no limit at all", so a mistyped
// "--limit -1" printed everything and looked like it had worked.
func TestNegativeListingLimitIsRefusedAndZeroMeansAll(t *testing.T) {
	dir := t.TempDir()
	lbSeed(t, dir, "Balint", "Reka")

	for _, args := range [][]string{
		{"leaderboard", "show", "--limit", "-1"},
		{"game", "list", "--limit", "-1"},
	} {
		out, err := run(t, dir, args...)
		if err == nil {
			t.Errorf("%v was accepted: %q", args, out)
			continue
		}
		if !strings.Contains(err.Error(), "--limit") {
			t.Errorf("%v: the refusal does not name the flag: %v", args, err)
		}
	}

	all, err := run(t, dir, "leaderboard", "show", "--limit", "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Balint", "Reka"} {
		if !strings.Contains(all, name) {
			t.Errorf("--limit 0 left %s out of the standings:\n%s", name, all)
		}
	}
	one, err := run(t, dir, "leaderboard", "show", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(one, "Balint") {
		t.Errorf("--limit 1 printed the second player too:\n%s", one)
	}
}

// TestLeaderboardResetKeepsSavedGames is the substance behind the wording in
// F25. Reset deletes the result log, which is where ratings come from; the
// saved games are files of their own and it has never touched them, so the help
// may not say it deletes every recorded game.
func TestLeaderboardResetKeepsSavedGames(t *testing.T) {
	dir := t.TempDir()
	rs := game.Std
	rs.Size = 6
	g, err := game.ReplayTranscript(rs, "B1; C3; resign")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "game.twixt")
	if err := os.WriteFile(path, []byte(rec.Encode()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, dir, "game", "import", path); err != nil {
		t.Fatalf("importing a record: %v\n%s", err, out)
	}
	lbSeed(t, dir, "Balint", "Reka")

	if out, err := run(t, dir, "leaderboard", "reset", "--yes"); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}

	board, err := run(t, dir, "leaderboard", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(board, "no games recorded yet") {
		t.Errorf("the result log survived its own reset:\n%s", board)
	}
	games, err := run(t, dir, "game", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(games, "no saved games") {
		t.Errorf("resetting the leaderboard deleted the saved games it does not own:\n%s", games)
	}
}
