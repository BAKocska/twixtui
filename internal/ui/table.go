package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Column layout for text listings, shared by the command line and by any
// screen that lines cells up.
//
// A terminal aligns columns by the cells a string occupies, and a name is not
// its byte count: "Zsófia" written with a combining accent is nine bytes, six
// runes and six cells; a CJK name is one rune and two cells per character; an
// emoji is four bytes and two cells. Profile names are display identities and
// any script is allowed in them, so a listing measured in bytes comes out
// ragged for most of the world's names. Everything here measures with
// ansi.StringWidth, which counts cells over grapheme clusters and skips escape
// sequences, so styled cells line up with plain ones.

// WriteTable writes rows as columns separated by padding spaces, each column
// as wide as its widest cell in display cells.
//
// Cells are written exactly as they are given: nothing is truncated, re-cased
// or styled, and no escape sequence is added, so a caller that styles a cell
// keeps its styling and a caller that does not gets plain text. A caller
// needing a hard limit truncates its own cells first.
//
// Rows may be ragged: a short row simply stops, and a column no row reaches
// takes no width. No line carries trailing padding, so the output has no
// invisible whitespace at the ends of lines. Every row is one line ending in
// "\n", rows are emitted in the order given, and a row with no cells is a
// blank line.
//
// padding must be at least one column: cells with nothing between them read as
// one cell. A smaller value is the caller's bug, and is reported without
// writing anything. The table is laid out whole and handed to w in one write,
// which is as close to all-or-nothing as a writer allows.
func WriteTable(w io.Writer, rows [][]string, padding int) error {
	if padding < 1 {
		return fmt.Errorf("table padding is %d: columns need at least one space between them", padding)
	}
	if len(rows) == 0 {
		return nil
	}

	// Each cell is measured once. The per-cell widths are kept alongside the
	// column widths so that emitting a row does not measure it a second time.
	total, columns := 0, 0
	for _, r := range rows {
		total += len(r)
		columns = max(columns, len(r))
	}
	cellW := make([]int, 0, total)
	colW := make([]int, columns)
	for _, r := range rows {
		for j, c := range r {
			n := ansi.StringWidth(c)
			cellW = append(cellW, n)
			colW[j] = max(colW[j], n)
		}
	}

	var b strings.Builder
	lineW := 1 + padding*max(0, columns-1)
	for _, n := range colW {
		lineW += n
	}
	b.Grow(lineW * len(rows))

	at := 0
	for _, r := range rows {
		// Trailing empty cells say nothing, and padding out to them would
		// leave a line of invisible spaces behind its last word.
		last := len(r)
		for last > 0 && r[last-1] == "" {
			last--
		}
		for j := range last {
			if j > 0 {
				writeSpaces(&b, padding)
			}
			b.WriteString(r[j])
			if j < last-1 {
				writeSpaces(&b, colW[j]-cellW[at+j])
			}
		}
		at += len(r)
		b.WriteByte('\n')
	}
	out := b.String()
	n, err := io.WriteString(w, out)
	if err == nil && n < len(out) {
		// An io.Writer that writes less than it was given owes an error; this
		// one did not, so the table is short and the caller has to hear it.
		return io.ErrShortWrite
	}
	return err
}

// PadRight pads s with spaces to width display cells, and returns it unchanged
// when it already fills them or more.
//
// It never truncates. Cutting a line to fit is one decision made once, at the
// edge of the frame, and a padder that also cut would make it twice: use
// ansi.Truncate, which cuts on grapheme clusters and keeps escape sequences
// whole, and pad the result.
func PadRight(s string, width int) string {
	n := ansi.StringWidth(s)
	if n >= width {
		return s
	}
	if pad := width - n; pad <= len(spaces) {
		return s + spaces[:pad]
	}
	return s + strings.Repeat(" ", width-n)
}

// spaces is a run of blanks to copy padding out of, so that filling a column
// costs a copy rather than an allocation. A column wider than this is rare
// enough to pay for its own.
const spaces = "                                                                "

func writeSpaces(b *strings.Builder, n int) {
	for n > len(spaces) {
		b.WriteString(spaces)
		n -= len(spaces)
	}
	if n > 0 {
		b.WriteString(spaces[:n])
	}
}
