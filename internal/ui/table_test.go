package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// writeTable renders a table into a string, failing the test if it will not
// write at all.
func writeTable(t *testing.T, rows [][]string, padding int) string {
	t.Helper()
	var b bytes.Buffer
	if err := WriteTable(&b, rows, padding); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	return b.String()
}

// columnAt returns the display column cell starts at in line, which is what a
// reader sees. Measuring the text in front of the cell is the only honest way
// to ask whether two rows line up: counting bytes or runes is the mistake the
// tables had.
func columnAt(t *testing.T, line, cell string) int {
	t.Helper()
	i := strings.Index(line, cell)
	if i < 0 {
		t.Fatalf("the cell %q is not in the line %q", cell, line)
	}
	return ansi.StringWidth(line[:i])
}

// TestWriteTableAlignsColumnsInDisplayCells is the whole point of the helper.
// The first column here holds names that agree on nothing: a combining accent
// is more runes than cells, a CJK name is fewer, an emoji is fewer still, and
// a styled name carries bytes that occupy no cells at all. Whatever the mix,
// the next column has to begin in the same screen column on every row, which
// is the property a byte-counting tabwriter loses.
func TestWriteTableAlignsColumnsInDisplayCells(t *testing.T) {
	rows := [][]string{
		{"NAME", "GAMES", "SCORE"},
		{"Zso\u0301fia", "one", "x"},
		{"日本語", "two", "x"},
		{"\x1b[31mBalint\x1b[0m", "three", "x"},
		{"🙂🙂", "four", "x"},
	}
	const padding = 2
	out := writeTable(t, rows, padding)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(rows) {
		t.Fatalf("%d lines for %d rows:\n%s", len(lines), len(rows), out)
	}

	// The widest first cell is six cells wide ("Zsófia", "日本語", "Balint"),
	// and the widest second five ("GAMES", "three").
	const wantSecond = 6 + padding
	const wantThird = wantSecond + 5 + padding
	for i, row := range rows {
		if got := columnAt(t, lines[i], row[1]); got != wantSecond {
			t.Errorf("row %d (%q): second column starts at cell %d, want %d: %q",
				i, row[0], got, wantSecond, lines[i])
		}
		if got := columnAt(t, lines[i], row[2]); got != wantThird {
			t.Errorf("row %d (%q): third column starts at cell %d, want %d: %q",
				i, row[0], got, wantThird, lines[i])
		}
		if strings.HasSuffix(lines[i], " ") {
			t.Errorf("row %d ends in padding: %q", i, lines[i])
		}
	}
}

// TestWriteTableKeepsCellsVerbatim holds the helper to being a layout engine
// and nothing else. A caller that upper-cases a heading, styles a cell or puts
// two spaces inside one gets exactly that back, and a caller that styles
// nothing gets a plain string a test can compare: a helper that added an
// escape sequence of its own would make every redirected listing unreadable.
func TestWriteTableKeepsCellsVerbatim(t *testing.T) {
	styled := "\x1b[32mgreen\x1b[0m"
	rows := [][]string{
		{"", "NAME", "note"},
		{"*", "Anna  Mária", styled},
		{"", "ödön", "  leading and trailing  "},
	}
	out := writeTable(t, rows, 2)

	for _, cell := range []string{"NAME", "note", "Anna  Mária", "ödön", "  leading and trailing  ", styled} {
		if !strings.Contains(out, cell) {
			t.Errorf("the cell %q did not survive the table:\n%q", cell, out)
		}
	}
	if got, want := strings.Count(out, "\x1b"), strings.Count(styled, "\x1b"); got != want {
		t.Errorf("the table carries %d escape sequences, want the %d its cells brought:\n%q", got, want, out)
	}
	// The marker column is empty on most rows, and its width still has to come
	// from the row that fills it, or the names below it do not line up.
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	first := columnAt(t, lines[0], "NAME")
	for i, want := range []string{"NAME", "Anna  Mária", "ödön"} {
		if got := columnAt(t, lines[i], want); got != first {
			t.Errorf("row %d puts the name column at cell %d, want %d: %q", i, got, first, lines[i])
		}
	}
}

func TestWriteTableAcceptsRaggedRows(t *testing.T) {
	rows := [][]string{
		{"a", "bb", "ccc"},
		{"a"},
		{"a", "x"},
		{},
		{"a", "", "ccc"},
		{"a", "bb", ""},
	}
	out := writeTable(t, rows, 2)
	want := strings.Join([]string{
		"a  bb  ccc",
		"a",
		"a  x",
		"",
		"a      ccc",
		"a  bb",
		"",
	}, "\n")
	if out != want {
		t.Errorf("ragged rows rendered\n%q\nwant\n%q", out, want)
	}
}

// TestWriteTablePaddingMustSeparateColumns pins the refusal. Columns with
// nothing between them read as one column, so a caller asking for that has a
// bug, and the listing must not be half-written before the caller hears about
// it.
func TestWriteTablePaddingMustSeparateColumns(t *testing.T) {
	rows := [][]string{{"a", "b"}, {"c", "d"}}
	for _, padding := range []int{0, -1} {
		var b bytes.Buffer
		err := WriteTable(&b, rows, padding)
		if err == nil {
			t.Errorf("padding %d accepted, output %q", padding, b.String())
		}
		if b.Len() != 0 {
			t.Errorf("padding %d wrote %q before refusing", padding, b.String())
		}
	}
	// Rows that hold no cells at all have no columns to separate, which the
	// width estimate has to survive rather than compute a negative size from.
	if got := writeTable(t, [][]string{{}, {}}, 3); got != "\n\n" {
		t.Errorf("two empty rows rendered %q, want two blank lines", got)
	}
	if got := writeTable(t, nil, 2); got != "" {
		t.Errorf("no rows rendered %q, want nothing", got)
	}
}

// TestWriteTableReportsAFailedWrite keeps the error path real: a listing that
// could not be written must not look like one that was.
func TestWriteTableReportsAFailedWrite(t *testing.T) {
	sentinel := errors.New("disk full")
	err := WriteTable(failingWriter{sentinel}, [][]string{{"a", "b"}}, 2)
	if !errors.Is(err, sentinel) {
		t.Errorf("WriteTable = %v, want the writer's own error", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// TestPadRightMeasuresDisplayCells is the same measurement question asked of
// the one-cell helper the interactive screens use, including the case that
// makes a padder that truncates dangerous: a string already wider than the
// column comes back whole, because cutting a line to fit is a decision made
// once, at the edge of the frame.
func TestPadRightMeasuresDisplayCells(t *testing.T) {
	for _, c := range []struct {
		what  string
		s     string
		width int
	}{
		{"ascii", "Balint", 12},
		{"combining accents", "Zso\u0301fia", 12},
		{"fullwidth", "日本語", 12},
		{"emoji", "🙂🙂", 12},
		{"styled", "\x1b[31mBalint\x1b[0m", 12},
		{"already exact", "Balint", 6},
		{"wider than a run of blanks", "x", 200},
	} {
		got := PadRight(c.s, c.width)
		if w := ansi.StringWidth(got); w != c.width {
			t.Errorf("%s: PadRight(%q, %d) is %d cells wide", c.what, c.s, c.width, w)
		}
		if !strings.HasPrefix(got, c.s) {
			t.Errorf("%s: PadRight(%q, %d) = %q, which does not start with its input", c.what, c.s, c.width, got)
		}
	}
	// Too wide for its column: returned as it is, never cut.
	for _, s := range []string{"日本語", "Balint", "🙂🙂"} {
		if got := PadRight(s, 2); got != s {
			t.Errorf("PadRight(%q, 2) = %q, want the string untouched", s, got)
		}
	}
}
