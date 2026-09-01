package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The reference documents printed by "rules show" are markdown files that also
// ship in the repository, so the files stay markdown and the terminal gets a
// laid-out version of them instead: a reader should not have to read past `##`
// and `**` to reach the prose.
//
// Structure is carried by case, indentation and rules rather than by colour,
// because the whole product is expected to stay readable with colour off.

const (
	// proseMeasure caps the line length however wide the terminal is. The eye
	// loses its way back to the start of a 200-column line.
	proseMeasure = 90
	// narrowestMeasure keeps a very narrow terminal from folding every other
	// word onto a line of its own.
	narrowestMeasure = 30
)

// renderMarkdown lays a markdown document out as plain text for a terminal
// width columns wide. It handles what the reference documents are written with:
// headings, paragraphs, bullet and numbered lists, fenced blocks, pipe tables,
// and inline bold, italic and code spans.
func renderMarkdown(doc string, width int) string {
	measure := min(max(width, narrowestMeasure), proseMeasure)
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	var out blocks
	for i := 0; i < len(lines); {
		switch line := lines[i]; {
		case strings.TrimSpace(line) == "":
			out.gap()
			i++
		case isFence(line):
			i = writeFenced(&out, lines, i)
		case isHeading(line):
			level, heading, _ := markdownHeading(line)
			writeHeading(&out, level, heading, measure)
			i++
		case isTableRow(line):
			i = writeTable(&out, lines, i, measure)
		case isListItem(line):
			i = writeList(&out, lines, i, measure)
		default:
			i = writeParagraph(&out, lines, i, measure)
		}
	}
	return out.String()
}

// blocks assembles the rendered document and keeps exactly one blank line
// between blocks: once a paragraph has been reflowed, the blank lines of the
// source no longer correspond to anything on screen.
type blocks struct {
	b       strings.Builder
	owedGap bool
	written bool
}

// gap asks for a blank line before whatever is written next. Nothing is emitted
// until then, so a run of gaps and a gap at the end of the document collapse.
func (bl *blocks) gap() {
	if bl.written {
		bl.owedGap = true
	}
}

func (bl *blocks) line(s string) {
	if bl.owedGap {
		bl.b.WriteByte('\n')
		bl.owedGap = false
	}
	bl.b.WriteString(strings.TrimRight(s, " "))
	bl.b.WriteByte('\n')
	bl.written = true
}

func (bl *blocks) lines(ss []string) {
	for _, s := range ss {
		bl.line(s)
	}
}

func (bl *blocks) String() string { return bl.b.String() }

// writeHeading renders a heading in upper case, ruled off at the top two
// levels. Case and a rule of dashes survive a monochrome terminal, which a bold
// attribute may not.
func writeHeading(out *blocks, level int, heading string, measure int) {
	title := wrapTo(strings.ToUpper(inlineText(heading)), measure)
	if len(title) == 0 {
		return
	}
	out.gap()
	out.lines(title)
	if rule := headingRule(level); rule != "" {
		width := 0
		for _, l := range title {
			width = max(width, ansi.StringWidth(l))
		}
		out.line(strings.Repeat(rule, width))
	}
	out.gap()
}

func headingRule(level int) string {
	switch level {
	case 1:
		return "="
	case 2:
		return "-"
	}
	return ""
}

// writeParagraph joins the source's hard-wrapped lines back into one paragraph
// and folds it to the measure, which is the whole point: the file is wrapped for
// a diff, the terminal wants it wrapped for the terminal.
func writeParagraph(out *blocks, lines []string, i, measure int) int {
	var text strings.Builder
	text.WriteString(strings.TrimSpace(lines[i]))
	for i++; i < len(lines) && !startsBlock(lines[i]); i++ {
		text.WriteByte(' ')
		text.WriteString(strings.TrimSpace(lines[i]))
	}
	out.gap()
	out.lines(wrapTo(inlineText(text.String()), measure))
	out.gap()
	return i
}

// writeList renders a run of list items, each wrapped with its text hanging
// under the marker so that a long item still reads as one item.
func writeList(out *blocks, lines []string, i, measure int) int {
	out.gap()
	for i < len(lines) {
		indent, marker, text, ok := listItem(lines[i])
		if !ok {
			break
		}
		var body strings.Builder
		body.WriteString(text)
		for i++; i < len(lines) && !startsBlock(lines[i]); i++ {
			body.WriteByte(' ')
			body.WriteString(strings.TrimSpace(lines[i]))
		}
		lead := strings.Repeat(" ", indent) + marker + " "
		hang := strings.Repeat(" ", ansi.StringWidth(lead))
		for n, l := range wrapTo(inlineText(body.String()), max(measure-ansi.StringWidth(lead), narrowestMeasure)) {
			if n == 0 {
				out.line(lead + l)
				continue
			}
			out.line(hang + l)
		}
	}
	out.gap()
	return i
}

// writeFenced copies a fenced block out indented, dropping the fences. What is
// inside is a literal — a transcript, a command — and reflowing it would change
// what it says.
func writeFenced(out *blocks, lines []string, i int) int {
	fence := strings.TrimSpace(lines[i])[:3]
	out.gap()
	for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
		out.line("    " + strings.TrimRight(lines[i], " "))
	}
	if i < len(lines) {
		i++
	}
	out.gap()
	return i
}

// writeTable lays a pipe table out as aligned columns when the cells are short
// enough to line up, and one labelled record per row when they are not: a grid
// of sentences is unreadable at any width.
func writeTable(out *blocks, lines []string, i, measure int) int {
	var rows [][]string
	for i < len(lines) {
		cells, ok := tableRow(lines[i])
		if !ok {
			break
		}
		i++
		if isTableDivider(cells) {
			continue
		}
		for j := range cells {
			cells[j] = inlineText(cells[j])
		}
		rows = append(rows, cells)
	}
	out.gap()
	if grid := gridTable(rows, measure); grid != nil {
		out.lines(grid)
	} else {
		out.lines(recordTable(rows, measure))
	}
	out.gap()
	return i
}

// gridTable returns the table as aligned columns under an upper-case heading
// row, matching how the command line's own listings are laid out, or nil when
// the widest cells will not fit the measure.
func gridTable(rows [][]string, measure int) []string {
	if len(rows) == 0 {
		return nil
	}
	columns := 0
	for _, r := range rows {
		columns = max(columns, len(r))
	}
	widths := make([]int, columns)
	for _, r := range rows {
		for j, c := range r {
			widths[j] = max(widths[j], ansi.StringWidth(c))
		}
	}
	total := 2 * (columns - 1)
	for _, w := range widths {
		total += w
	}
	if total > measure {
		return nil
	}
	out := make([]string, 0, len(rows))
	for n, r := range rows {
		var line strings.Builder
		for j := range columns {
			cell := ""
			if j < len(r) {
				cell = r[j]
			}
			if n == 0 {
				cell = strings.ToUpper(cell)
			}
			if j > 0 {
				line.WriteString("  ")
			}
			line.WriteString(cell)
			if j < columns-1 {
				line.WriteString(strings.Repeat(" ", widths[j]-ansi.StringWidth(cell)))
			}
		}
		out = append(out, line.String())
	}
	return out
}

// recordTable returns the table one row at a time, the first cell as the
// record's name and the rest as its heading and value, so a cell holding a
// sentence is read rather than truncated.
func recordTable(rows [][]string, measure int) []string {
	if len(rows) < 2 {
		var out []string
		for _, r := range rows {
			out = append(out, wrapTo(strings.Join(r, " · "), measure)...)
		}
		return out
	}
	heading := rows[0]
	var out []string
	for _, r := range rows[1:] {
		if len(out) > 0 {
			out = append(out, "")
		}
		if len(r) > 0 {
			out = append(out, wrapTo(r[0], measure)...)
		}
		for j := 1; j < len(r); j++ {
			if r[j] == "" {
				continue
			}
			text := r[j]
			if j < len(heading) && heading[j] != "" {
				text = heading[j] + ": " + text
			}
			for n, l := range wrapTo(text, max(measure-4, narrowestMeasure)) {
				if n == 0 {
					out = append(out, "  "+l)
					continue
				}
				out = append(out, "    "+l)
			}
		}
	}
	return out
}

// inlineText drops the markup that only means something in a source file: code
// spans, bold and italic. The words are what a reader wants, and the markers are
// punctuation the terminal cannot honour. Escapes and links are not handled
// because the reference documents use neither.
func inlineText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '`', '*':
			marker := markerRun(s, i)
			body, next, ok := delimited(s, i, marker)
			if !ok {
				b.WriteByte(c)
				i++
				continue
			}
			if c == '`' {
				// A code span is a literal: whatever is between the backticks
				// stands as it is written.
				b.WriteString(strings.Trim(body, " "))
			} else {
				b.WriteString(inlineText(body))
			}
			i = next
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// markerRun returns the run of the delimiter starting at i, so that ** is told
// apart from *.
func markerRun(s string, i int) string {
	n := i
	for n < len(s) && s[n] == s[i] {
		n++
	}
	return s[i:n]
}

// delimited returns what stands between the marker at i and its next
// occurrence, and where the text carries on.
func delimited(s string, i int, marker string) (body string, next int, ok bool) {
	rest := s[i+len(marker):]
	end := strings.Index(rest, marker)
	if end < 0 {
		return "", 0, false
	}
	return rest[:end], i + 2*len(marker) + end, true
}

// wrapTo folds text onto lines of at most width cells: whole words while they
// fit, and a word longer than the measure cut rather than left to overflow.
//
// ansi.Wrap is not used here because a hyphen is always a break point to it and
// it puts that hyphen one cell past the limit. A line wider than the measure is
// folded again by the terminal, which is exactly what the measure exists to
// prevent.
func wrapTo(text string, width int) []string {
	if width < 1 {
		return nil
	}
	var lines []string
	var line string
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
		for ansi.StringWidth(line) > width {
			cut := ansi.Truncate(line, width, "")
			lines = append(lines, cut)
			line = strings.TrimPrefix(line, cut)
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// startsBlock reports whether a line begins a block of its own, which is where
// the paragraph or list item being gathered has to stop.
func startsBlock(line string) bool {
	switch {
	case strings.TrimSpace(line) == "", isFence(line), isHeading(line), isTableRow(line), isListItem(line):
		return true
	}
	return false
}

func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isHeading(line string) bool {
	_, _, ok := markdownHeading(line)
	return ok
}

func isTableRow(line string) bool {
	_, ok := tableRow(line)
	return ok
}

func isListItem(line string) bool {
	_, _, _, ok := listItem(line)
	return ok
}

// listItem reports a list item's indentation, the marker to print it with, and
// the text on its first line. A numbered item keeps its own number, since the
// document may be counting steps.
func listItem(line string) (indent int, marker, text string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	indent = len(line) - len(trimmed)
	for _, bullet := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, bullet) {
			return indent, "-", strings.TrimSpace(trimmed[len(bullet):]), true
		}
	}
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits > 0 && strings.HasPrefix(trimmed[digits:], ". ") {
		return indent, trimmed[:digits+1], strings.TrimSpace(trimmed[digits+2:]), true
	}
	return 0, "", "", false
}

// tableRow splits a pipe-table row into its cells.
func tableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return nil, false
	}
	cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(trimmed, "|"), "|"), "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells, true
}

// isTableDivider reports the ---|--- row under a table's headings, which carries
// no content of its own.
func isTableDivider(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !strings.Contains(c, "-") || strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return true
}
