package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// A record leaves this program as a file or as a stream and comes back the same
// way, and both ends of that are paths: the configuration directory it is
// stored in, the file it is written to, the file it is read from. On Windows
// those paths hold spaces as a matter of course — a temporary directory lives
// under a user's profile, which is under "C:\Users\<name>" and often under
// "Documents and Settings" before that — and a user's own directory is as
// likely as not to hold characters outside ASCII.
//
// Every path in this test therefore has both. Nothing here goes through a
// shell: the executable is started with an argument vector, which is the only
// way a path with a space in it survives being passed on.

// cliResult is what one non-interactive run of twixtui produced.
type cliResult struct {
	stdout string
	stderr string
	code   int
}

// cliRun runs the compiled binary against one configuration directory and
// returns what it printed and the status it exited with. The status is
// returned rather than asserted, because half of what these tests are about is
// a refusal.
func cliRun(t *testing.T, bin, cfg, stdin string, args ...string) cliResult {
	t.Helper()
	full := append([]string{"--config", cfg}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TWIXTUI_CONFIG_DIR="+cfg)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	res := cliResult{stdout: out.String(), stderr: errs.String()}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running twixtui %s: %v", strings.Join(full, " "), err)
		}
		res.code = exit.ExitCode()
	}
	return res
}

// cliPathGames is how many games a configuration directory holds. It is read
// through the store rather than through the program, so "the command stored
// nothing" is measured against the files and not against what the command said
// about itself.
func cliPathGames(t *testing.T, cfg string) int {
	t.Helper()
	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatalf("opening the store in %s: %v", cfg, err)
	}
	return len(store.List())
}

// cliPathRecord builds a game and returns it as its own encoding and as the
// canonical one, which is what the program stores and hands back.
func cliPathRecord(t *testing.T) (encoded, canonical string) {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g := game.MustNew(rs)
	for _, move := range []string{"B1", "F2", "C3", "F3", "D5", "F4", "B6"} {
		if err := g.PlayNotation(move); err != nil {
			t.Fatalf("building the record: playing %s: %v", move, err)
		}
	}
	if g.Ply() != 7 {
		t.Fatalf("the fixture holds %d moves, want the 7 that were played", g.Ply())
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = rec.EncodeCanonical()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode(), canonical
}

// TestRecordsSurvivePathsWithSpacesAndUnicode carries one game from a file into
// a store, out again to another file, through a pipe into a second store, and
// out of that one, and checks the bytes at the far end are the bytes the first
// import produced. Every path involved holds a space and a character outside
// ASCII.
func TestRecordsSurvivePathsWithSpacesAndUnicode(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	encoded, canonical := cliPathRecord(t)

	root := filepath.Join(t.TempDir(), "twixtui e2e ünnepi könyvtár")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// The configuration directories do not exist yet: creating them is part of
	// what a path like this has to survive.
	first := filepath.Join(root, "config egy")
	second := filepath.Join(root, "config kettő")
	incoming := filepath.Join(root, "kapott játszma ő.twixtui")
	if err := os.WriteFile(incoming, []byte(encoded), 0o644); err != nil {
		t.Fatal(err)
	}

	imported := cliRun(t, bin, first, "", "game", "import", incoming)
	if imported.code != 0 {
		t.Fatalf("importing from %s exited %d: %s%s", incoming, imported.code, imported.stdout, imported.stderr)
	}
	id := cliPathImportedID(t, imported.stdout)
	if got := cliPathGames(t, first); got != 1 {
		t.Fatalf("the store in %s holds %d games after one import, want 1", first, got)
	}

	exported := cliRun(t, bin, first, "", "game", "export", id)
	if exported.code != 0 {
		t.Fatalf("exporting %s exited %d: %s%s", id, exported.code, exported.stdout, exported.stderr)
	}
	if exported.stdout != canonical {
		t.Fatalf("the exported record is not the canonical encoding of the game that went in\n--- exported ---\n%s\n--- want ---\n%s",
			exported.stdout, canonical)
	}

	outPath := filepath.Join(root, "kiírt játszma ű.twixtui")
	written := cliRun(t, bin, first, "", "game", "export", id, "--out", outPath)
	if written.code != 0 {
		t.Fatalf("exporting to %s exited %d: %s%s", outPath, written.code, written.stdout, written.stderr)
	}
	if !strings.Contains(written.stdout, outPath) {
		t.Errorf("the export does not say where it wrote the record:\n%s", written.stdout)
	}
	onDisk, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading back the exported record: %v", err)
	}
	if string(onDisk) != exported.stdout {
		t.Fatalf("the file and the stream carry different records\n--- file ---\n%s\n--- stream ---\n%s",
			onDisk, exported.stdout)
	}

	// Into the second machine's store through standard input, which is the
	// route a player who piped one command into the other takes.
	piped := cliRun(t, bin, second, string(onDisk), "game", "import", "-")
	if piped.code != 0 {
		t.Fatalf("importing from standard input exited %d: %s%s", piped.code, piped.stdout, piped.stderr)
	}
	secondID := cliPathImportedID(t, piped.stdout)
	round := cliRun(t, bin, second, "", "game", "export", secondID)
	if round.code != 0 {
		t.Fatalf("exporting %s exited %d: %s%s", secondID, round.code, round.stdout, round.stderr)
	}
	if round.stdout != exported.stdout {
		t.Fatalf("the record changed on the way through a file and a pipe\n--- first ---\n%s\n--- second ---\n%s",
			exported.stdout, round.stdout)
	}
	if !strings.Contains(round.stdout, "B6") {
		t.Errorf("the record that came back does not hold the moves that were played:\n%s", round.stdout)
	}

	// The same record again is the same game, and saying so is what makes an
	// import safe to re-run.
	again := cliRun(t, bin, second, "", "game", "import", outPath)
	if again.code != 0 {
		t.Fatalf("re-importing exited %d: %s%s", again.code, again.stdout, again.stderr)
	}
	if !strings.Contains(again.stdout, "already saved as "+secondID) {
		t.Errorf("re-importing did not recognise the game it already holds:\n%s", again.stdout)
	}
	if got := cliPathGames(t, second); got != 1 {
		t.Errorf("the store in %s holds %d games after importing one record twice, want 1", second, got)
	}
}

// cliPathImportedID reads the identifier an import announced.
func cliPathImportedID(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "imported as ")
	if !ok {
		t.Fatalf("no imported game in:\n%s", out)
	}
	id, _, _ := strings.Cut(rest, ":")
	return strings.TrimSpace(id)
}

// TestRecordRefusalsLeaveNothingBehind covers the other half of moving records
// about: what happens when the path or the record is wrong. A refusal has to
// name what it refused and leave the store and the destination as they were —
// an import that half-stored a damaged game, or an export that truncated the
// file it was about to refuse to write, is worse than one that fails.
func TestRecordRefusalsLeaveNothingBehind(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	encoded, _ := cliPathRecord(t)

	root := filepath.Join(t.TempDir(), "hibás bemenet könyvtár")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "config három")

	missing := filepath.Join(root, "nincs ilyen játszma ő.twixtui")
	absent := cliRun(t, bin, cfg, "", "game", "import", missing)
	if absent.code == 0 {
		t.Fatalf("importing a file that is not there was accepted:\n%s", absent.stdout)
	}
	if !strings.Contains(absent.stderr, filepath.Base(missing)) {
		t.Errorf("the refusal does not name the file it could not read:\n%s", absent.stderr)
	}

	lines := strings.SplitAfter(strings.TrimSuffix(encoded, "\n"), "\n")
	truncated := filepath.Join(root, "csonka játszma ő.twixtui")
	if err := os.WriteFile(truncated, []byte(strings.Join(lines[:len(lines)-1], "")), 0o644); err != nil {
		t.Fatal(err)
	}
	cut := cliRun(t, bin, cfg, "", "game", "import", truncated)
	if cut.code == 0 {
		t.Fatalf("a truncated record was imported:\n%s", cut.stdout)
	}

	altered := filepath.Join(root, "átírt játszma ő.twixtui")
	if err := os.WriteFile(altered, []byte(strings.Replace(encoded, "B1", "B2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := cliRun(t, bin, cfg, "", "game", "import", altered)
	if changed.code == 0 {
		t.Fatalf("a record whose moves were edited under its digest was imported:\n%s", changed.stdout)
	}

	// Standard input takes the same refusal, which is the route with no file
	// name in the message to fall back on.
	streamed := cliRun(t, bin, cfg, "twixtui-record 1\nnonsense\n", "game", "import", "-")
	if streamed.code == 0 {
		t.Fatalf("nonsense on standard input was imported:\n%s", streamed.stdout)
	}
	if got := cliPathGames(t, cfg); got != 0 {
		t.Errorf("%d games were stored by refused imports, want 0", got)
	}

	// A refusal happens before the destination is opened, so the file the
	// export was told to write must not exist afterwards.
	target := filepath.Join(root, "nem születhet meg ű.twixtui")
	refused := cliRun(t, bin, cfg, "", "game", "export", "nosuchgame", "--out", target)
	if refused.code == 0 {
		t.Fatalf("exporting a game that is not there was accepted:\n%s", refused.stdout)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused export left %s behind (stat error %v)", target, err)
	}
}
