package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// fixtureRecordLimit is the record size the oversized fixture here is built
// around. It is a fixed number rather than an expression in game.MaxRecordBytes
// because a fixture derived from the bound it is checking grows with that
// bound: raising the bound to try something out would have this test write a
// terabyte to a temporary directory before asserting anything. What it asserts
// is behaviour — a record padded past a megabyte is refused — so a build that
// stops refusing one fails here rather than passing on a constant.
const fixtureRecordLimit = 1 << 20

// canonicalRecord is the record of a short finished game as this build encodes
// it, which is what a stored game and an export must both hold.
func canonicalRecord(t *testing.T) string {
	t.Helper()
	rs := game.Std
	rs.Size = 6
	g, err := game.ReplayTranscript(rs, "B1; resign")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Encode()
}

// recordFile puts body in a file of its own and returns the path.
func recordFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestImportRefusesAFileHoldingMoreThanOneRecord covers the file made by
// concatenating two exports. It used to import as whichever record came last,
// and the whole file was kept as the stored game, so bytes nothing had checked
// came back out of an export afterwards.
func TestImportRefusesAFileHoldingMoreThanOneRecord(t *testing.T) {
	record := canonicalRecord(t)
	dir := t.TempDir()

	// The positive control goes first: this record on its own imports, so a
	// command that refused every file could not pass this test.
	if _, err := run(t, dir, "game", "import", recordFile(t, "one.rec", record)); err != nil {
		t.Fatalf("a sound record was refused: %v", err)
	}

	for name, body := range map[string]string{
		"two.rec":       record + record,
		"different.rec": record + strings.Replace(record, "size=6", "size=7", 1),
		"extra.rec":     record + "moves B1\n",
	} {
		store := t.TempDir()
		if _, err := run(t, store, "game", "import", recordFile(t, name, body)); err == nil {
			t.Errorf("%s was imported as one game", name)
		}
		games, err := gamestore.Open(store)
		if err != nil {
			t.Fatal(err)
		}
		if saved := games.List(); len(saved) != 0 {
			t.Errorf("%s left %d games in the store", name, len(saved))
		}
	}
}

// TestImportStoresTheCheckedRecord covers the round trip. The reader tolerates
// carriage returns, comments and fields in any order, so a file may hold bytes
// the digests do not cover; what is stored, and what a later export hands out,
// is the record as this build encodes it.
func TestImportStoresTheCheckedRecord(t *testing.T) {
	record := canonicalRecord(t)
	lines := strings.Split(strings.TrimRight(record, "\n"), "\n")
	lenient := "# sent by a friend\r\n"
	for i := len(lines) - 1; i >= 0; i-- {
		lenient += lines[i] + "\r\n"
	}
	if lenient == record {
		t.Fatal("the lenient fixture is the canonical encoding, so it proves nothing")
	}

	dir := t.TempDir()
	out, err := run(t, dir, "game", "import", recordFile(t, "lenient.rec", lenient))
	if err != nil {
		t.Fatalf("a record written the long way round was refused: %v\n%s", err, out)
	}
	id := importedID(t, out)

	exported, err := run(t, dir, "game", "export", id)
	if err != nil {
		t.Fatal(err)
	}
	if exported != record {
		t.Errorf("export handed back\n%q\nwant the canonical record\n%q", exported, record)
	}
}

// TestImportRefusesAnOversizedFile covers the size of the input rather than its
// contents. A record arrives from a file or a pipe, so how large it is, is not
// this program's choice; the loader stops at the limit instead of materialising
// whatever is on the other end.
func TestImportRefusesAnOversizedFile(t *testing.T) {
	// The padding is comment lines on an otherwise sound record rather than a
	// run of junk. A parser refuses junk whatever the limit is, so only a
	// record it would otherwise accept can tell the size check apart from the
	// syntax check — which is the check that would go missing.
	record := canonicalRecord(t)
	padding := strings.Repeat("# padding\n", 1+(fixtureRecordLimit-len(record))/len("# padding\n"))
	oversized := record + padding
	if len(oversized) <= fixtureRecordLimit {
		t.Fatalf("the padded record is %d bytes, which does not exceed the %d-byte fixture limit", len(oversized), fixtureRecordLimit)
	}

	dir := t.TempDir()
	out, err := run(t, dir, "game", "import", recordFile(t, "huge.rec", oversized))
	if err == nil {
		t.Fatalf("a file past the record size limit was imported:\n%s", out)
	}
	if !errors.Is(err, game.ErrRecordTooLarge) {
		t.Errorf("the refusal reads %q, which is not a refusal on size", err)
	}
	games, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved := games.List(); len(saved) != 0 {
		t.Errorf("the refused import left %d games in the store", len(saved))
	}

	// The same record with one comment line on it imports, so the refusal
	// above is the size of the file and not the padding.
	if _, err := run(t, dir, "game", "import", recordFile(t, "sound.rec", record+"# padding\n")); err != nil {
		t.Fatalf("a commented record inside the limit was refused: %v", err)
	}
}

// TestExportChecksTheRecordBeforeTouchingTheOutput covers the asymmetry that
// show and replay refused a damaged record while export wrote it out without a
// word. A record this build will not read is not a record to send on, and the
// file named by --out must be left as it was rather than truncated first.
func TestExportChecksTheRecordBeforeTouchingTheOutput(t *testing.T) {
	record := canonicalRecord(t)
	dir := t.TempDir()
	imported, err := run(t, dir, "game", "import", recordFile(t, "sound.rec", record))
	if err != nil {
		t.Fatal(err)
	}
	id := importedID(t, imported)

	target := recordFile(t, "out.rec", "KEEP")
	if _, err := run(t, dir, "game", "export", id, "--out", target); err != nil {
		t.Fatalf("exporting a sound game failed: %v", err)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != record {
		t.Fatalf("export wrote %q, want the record", written)
	}

	// Damage the stored record the way an edit to the file would.
	path := filepath.Join(dir, "games", id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	damaged := strings.Replace(string(raw), "B1", "C1", 1)
	if damaged == string(raw) {
		t.Fatal("the stored game does not contain the move being altered")
	}
	if err := os.WriteFile(path, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(target, []byte("KEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, dir, "game", "export", id, "--out", target); err == nil {
		t.Error("a record that no longer matches its digest was exported")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "KEEP" {
		t.Errorf("the output file was written before the record was checked: %q", after)
	}

	// Standard output is the same gate, and show already refused this record.
	if _, err := run(t, dir, "game", "export", id); err == nil {
		t.Error("a damaged record was written to standard output")
	}
	if _, err := run(t, dir, "game", "show", id); err == nil {
		t.Error("show accepted a damaged record")
	}
}

// TestGameListRefusesANegativeLimit covers the listing limit. Zero means all,
// which is documented; a negative number is a mistake in whatever produced it,
// and quietly meaning "all" hides that.
func TestGameListRefusesANegativeLimit(t *testing.T) {
	record := canonicalRecord(t)
	dir := t.TempDir()
	if _, err := run(t, dir, "game", "import", recordFile(t, "one.rec", record)); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, dir, "game", "list", "--limit", "-1"); err == nil {
		t.Error("game list accepted a negative limit")
	}
	out, err := run(t, dir, "game", "list", "--limit", "0")
	if err != nil {
		t.Fatalf("a limit of zero was refused: %v", err)
	}
	if !strings.Contains(out, string(gamestore.Imported)) {
		t.Errorf("a limit of zero did not list the stored game:\n%s", out)
	}
}
