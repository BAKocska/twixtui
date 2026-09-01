package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/humantime"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Picker asks who is playing, which is the first thing the program does
// (R13). It satisfies R14 two ways at once: the list is browsable with an
// empty query, most recently played first, for someone who cannot recall how
// they spelled their name, and typing filters it fuzzily for someone who can
// nearly recall it. A name that is not in the list is offered for creation,
// which is also the whole of the first-run path (R12).
type Picker struct {
	deps   Deps
	prompt string

	edit lineEdit
	nav  navKeys
	// quitHint names the key the shell answers before this screen sees it.
	quitHint string
	// hints are the status-line parts. They are rebuilt only when Cancelled
	// changes what escape does, because the keys they name cannot change while
	// the screen is up.
	hints []string

	rows []pickerRow
	sel  int

	// problem is a rejected name explained in terms of the rule it broke, and
	// notice is standing guidance such as the empty-store case.
	problem string
	notice  string

	width, height int

	chosen func(name string) tea.Cmd
	// cancelled is what escape does, and nil at the root: the picker is the
	// first screen there, with nothing behind it to go back to.
	cancelled func() tea.Cmd
}

// pickerRow is one line of the list: an existing profile, or the offer to
// create the name that has been typed.
type pickerRow struct {
	name string
	// positions are the rune indexes of the characters the query matched, for
	// highlighting. profile.Search reports runes, not bytes, which is what a
	// terminal counts.
	positions []int
	lastUsed  time.Time
	create    bool
}

// NewPicker returns the profile chooser. The prompt is the question at the top
// of the screen; an empty one falls back to the launch wording.
//
// Once a name is chosen the picker replaces itself with the main menu, which is
// what launching the program does. Chosen overrides that for a caller that
// wants the name for something else.
func NewPicker(d Deps, prompt string) *Picker {
	if prompt == "" {
		prompt = "Who is playing?"
	}
	km := shellKeymap(d)
	p := &Picker{
		deps:     d,
		prompt:   prompt,
		nav:      newNavKeys(km),
		quitHint: keyLabel(globalQuitKeys(km)...) + " quit",
	}
	p.chosen = func(name string) tea.Cmd { return Replace(NewMenu(p.deps, name)) }
	p.buildHints()
	p.refresh()
	return p
}

// buildHints assembles the status line. The arrows and the emacs pair move the
// same cursor, so they are one hint rather than two that both say "move", and
// escape is named only where it has a screen to go back to.
func (p *Picker) buildHints() {
	moves := keyLabel(p.nav.up...) + "/" + keyLabel(p.nav.down...) +
		" or " + keyPrev + "/" + keyNext + " move"
	p.hints = []string{moves, keyLabel(p.nav.confirm...) + " choose"}
	if p.cancelled != nil {
		p.hints = append(p.hints, "esc back")
	}
	p.hints = append(p.hints, p.quitHint, "tab fill in")
}

// Chosen replaces what happens once a name has been picked. It is how the
// picker is embedded in another screen: the command can emit a message that
// screen handles instead of leaving the picker.
func (p *Picker) Chosen(f func(name string) tea.Cmd) *Picker {
	if f != nil {
		p.chosen = f
	}
	return p
}

// Cancelled sets what escape does. The picker has no answer of its own: at
// launch it is the first screen, and a program that ends on one stray escape
// is worse than one that ignores the key. Setting this is therefore what puts
// "esc back" on the status line as well, so the offer and the key arrive
// together.
func (p *Picker) Cancelled(f func() tea.Cmd) *Picker {
	if f != nil {
		p.cancelled = f
		p.buildHints()
	}
	return p
}

// Init implements tea.Model.
func (p *Picker) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
	case ThemeChangedMsg:
		p.deps.Theme = m.Theme
		if m.Styles != nil {
			p.deps.Styles = m.Styles
		}
	case tea.KeyPressMsg:
		return p, p.key(m)
	}
	return p, nil
}

func (p *Picker) key(m tea.KeyPressMsg) tea.Cmd {
	key := m.String()
	if sel, moved := p.nav.move(key, p.sel, len(p.rows)); moved {
		p.sel = sel
		return nil
	}
	switch {
	case p.nav.isCancel(key):
		if p.cancelled == nil {
			return nil
		}
		return p.cancelled()
	case p.nav.isConfirm(key):
		return p.commit()
	case key == "tab":
		// Fill the query in from the highlighted name, so a player who found
		// their profile by browsing can see what they are about to choose.
		if r := p.row(); r != nil && !r.create {
			p.edit.setValue(r.name)
			p.retype()
		}
		return nil
	}
	if p.edit.key(m) {
		p.retype()
	}
	return nil
}

// retype rebuilds the list after the query changed. The selection goes back to
// the top, which is the best match, and a complaint about the previous name is
// dropped because it no longer describes what is typed.
func (p *Picker) retype() {
	p.sel = 0
	p.problem = ""
	p.refresh()
}

// row returns the highlighted row, or nil when the list is empty.
func (p *Picker) row() *pickerRow {
	if p.sel < 0 || p.sel >= len(p.rows) {
		return nil
	}
	return &p.rows[p.sel]
}

// commit acts on enter: choose the highlighted profile, or create the typed
// name.
func (p *Picker) commit() tea.Cmd {
	r := p.row()
	if r == nil {
		// Nothing to choose. On a fresh install that is the normal state, and
		// the name rule is the right thing to explain.
		return p.create(p.edit.value())
	}
	if r.create {
		return p.create(r.name)
	}
	return p.choose(r.name)
}

// choose records that the profile is the one playing and hands the name on.
//
// Recording it is not decoration: the command line reads the same stored choice,
// so a profile picked here has to be persisted or a later subcommand reports
// that nobody is playing. UseCurrent both marks the profile as used, which is
// what makes the most-recently-played order real, and records the choice.
func (p *Picker) choose(name string) tea.Cmd {
	if _, err := p.deps.Profiles.UseCurrent(name); err != nil {
		if !errors.Is(err, profile.ErrNotFound) {
			// A write failure is worth showing, but not worth throwing the
			// player out of the only screen that can get them into a game.
			p.problem = err.Error()
			p.refresh()
			return nil
		}
		return p.create(name)
	}
	p.refresh()
	return p.chosen(name)
}

// create validates and adds a name, saying which rule was broken when it will
// not do.
func (p *Picker) create(name string) tea.Cmd {
	if err := profile.ValidateName(name); err != nil {
		p.problem = nameProblem(err)
		return nil
	}
	if _, err := p.deps.Profiles.Create(name); err != nil {
		if errors.Is(err, profile.ErrExists) {
			// It exists after all: either another process added it, or it
			// differs from an existing name only in case or spacing, which the
			// store treats as the same profile. Use it.
			return p.choose(name)
		}
		p.problem = err.Error()
		p.refresh()
		return nil
	}
	return p.choose(name)
}

// refresh rebuilds the list from the store for the current query.
func (p *Picker) refresh() {
	query := p.edit.value()
	matches := p.deps.Profiles.Search(query)

	p.rows = p.rows[:0]
	for _, m := range matches {
		p.rows = append(p.rows, pickerRow{
			name:      m.Profile.Name,
			positions: m.Positions,
			lastUsed:  m.Profile.LastUsed,
		})
	}
	// The offer to create is a row of its own, below the matches and marked
	// differently, so that choosing an existing profile and inventing a new one
	// can never be confused with each other.
	if query != "" {
		if _, exists := p.deps.Profiles.Get(query); !exists {
			p.rows = append(p.rows, pickerRow{name: query, create: true})
		}
	}
	if p.sel >= len(p.rows) {
		p.sel = max(0, len(p.rows)-1)
	}

	p.notice = ""
	if len(p.deps.Profiles.List()) == 0 {
		// Fresh install: there is nothing to browse, so the screen asks for a
		// name instead of showing an empty list.
		p.notice = "No profiles on this machine yet. Type a name and press enter to start one."
	} else if query != "" && len(matches) == 0 {
		p.notice = "Nothing matches. Press enter to use it as a new name."
	}
}

// View implements tea.Model. The shell owns the alternate screen, so only the
// content is set here.
func (p *Picker) View() tea.View {
	st := shellStyles(p.deps)
	w, h := p.width, p.height
	status := paint(st, &st.Status, hintLine(w, p.hints...))

	head := make([]string, 0, 8)
	head = append(head, paint(st, &st.PanelTitle, p.prompt))
	head = append(head, p.edit.render(st, w))
	switch {
	case p.problem != "":
		head = appendWrapped(head, st, &st.Message, p.problem, w)
	case p.notice != "":
		head = appendWrapped(head, st, &st.PanelText, p.notice, w)
	}
	head = append(head, "")

	// The list gets whatever is left once the heading and the status line have
	// had their rows.
	rows := make([]string, 0, len(p.rows))
	for i := range p.rows {
		rows = append(rows, p.renderRow(st, i, w))
	}
	content := append(head, window(rows, h-1-len(head), p.sel)...)

	return tea.NewView(textFrame(st, w, h, content, status))
}

// appendWrapped adds text as however many lines it takes at this width, so a
// sentence of guidance is readable in a narrow pane instead of being cut off.
func appendWrapped(lines []string, st *ui.Styles, style *lipgloss.Style, text string, width int) []string {
	if width < 1 {
		return lines
	}
	for _, l := range wrapText(text, width) {
		lines = append(lines, paint(st, style, l))
	}
	return lines
}

// renderRow draws one line of the list. The selection is marked with a glyph
// as well as a colour, so it survives a terminal with no colour at all.
func (p *Picker) renderRow(st *ui.Styles, i, width int) string {
	r := p.rows[i]
	marker := "  "
	if i == p.sel {
		marker = paint(st, &st.Cursor, "> ")
	}
	if r.create {
		return marker + paint(st, &st.Highlight, "+ new profile ") + fmt.Sprintf("%q", r.name)
	}

	name := highlightRunes(st, r.name, r.positions)
	// The time column is the other half of R14: recognising your own name in a
	// list is easier when you can see which one you played last. It is dropped
	// on a narrow terminal, where the name matters more. Eighteen columns is
	// the widest date humantime falls back to, "27 September 2026".
	const timeColumn = 18
	nameW := width - 2 - timeColumn
	if nameW < profile.MaxNameRunes {
		return marker + name
	}
	when := paint(st, &st.Label, playedAgo(p.deps.Clock(), r.lastUsed))
	return marker + padTo(name, nameW) + when
}

// highlightRunes picks out the characters a query matched. Runs of adjacent
// matches are styled together rather than one at a time, which keeps the
// escape sequences down and the output readable.
func highlightRunes(st *ui.Styles, name string, positions []int) string {
	if len(positions) == 0 {
		return name
	}
	runes := []rune(name)
	hit := make([]bool, len(runes))
	for _, pos := range positions {
		if pos >= 0 && pos < len(hit) {
			hit[pos] = true
		}
	}
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(runes); {
		if !hit[i] {
			b.WriteRune(runes[i])
			i++
			continue
		}
		j := i
		for j < len(runes) && hit[j] {
			j++
		}
		b.WriteString(paint(st, &st.Highlight, string(runes[i:j])))
		i = j
	}
	return b.String()
}

// nameProblem explains a rejected name by the rule it broke. The store has a
// distinct sentinel per rule precisely so that this can say which one.
func nameProblem(err error) string {
	switch {
	case errors.Is(err, profile.ErrNameEmpty):
		return "Type a name first."
	case errors.Is(err, profile.ErrNameTooLong):
		return fmt.Sprintf("Too long: at most %d characters.", profile.MaxNameRunes)
	case errors.Is(err, profile.ErrNamePadded):
		return "No spaces at the start or the end."
	case errors.Is(err, profile.ErrNameControl):
		return "No control characters."
	case errors.Is(err, profile.ErrNameInvisible):
		return "No invisible or direction-changing characters."
	case errors.Is(err, profile.ErrNameNotUTF8):
		return "That is not valid text."
	default:
		return err.Error()
	}
}

// playedAgo says how long ago a profile was used, coarsely: the point is
// recognising your own row, not measuring anything. The wording comes from
// humantime, so a profile reads the same here as it does under
// "twixtui profile list", and anything older than a month is given as its date
// rather than as a count of days nobody can place.
func playedAgo(now, then time.Time) string {
	if then.IsZero() {
		return "never played"
	}
	return humantime.Ago(now, then)
}

// plural pairs a count with its unit. It is the saved-game listing's helper for
// "3 moves"; the ages in this file get their wording from humantime instead.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// lineEdit is a one-line text field: the picker's query and the network form's
// address and code.
//
// It is written here rather than taken from bubbles/textinput because the
// screens that use it must own up, down, ctrl+p and ctrl+n for their list,
// which that component binds to its own suggestion feature, and because its
// blinking cursor emits an endless stream of commands, which would make every
// frame assertion in a test a race.
type lineEdit struct {
	runes []rune
	pos   int
}

func (e *lineEdit) value() string { return string(e.runes) }

func (e *lineEdit) setValue(s string) {
	e.runes = append(e.runes[:0], []rune(s)...)
	e.pos = len(e.runes)
}

// key applies a keypress and reports whether it changed the text.
func (e *lineEdit) key(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "left":
		e.pos = max(0, e.pos-1)
		return false
	case "right":
		e.pos = min(len(e.runes), e.pos+1)
		return false
	case "home", "ctrl+a":
		e.pos = 0
		return false
	case "end", "ctrl+e":
		e.pos = len(e.runes)
		return false
	case "backspace":
		if e.pos == 0 {
			return false
		}
		e.runes = append(e.runes[:e.pos-1], e.runes[e.pos:]...)
		e.pos--
		return true
	case "delete", "ctrl+d":
		if e.pos >= len(e.runes) {
			return false
		}
		e.runes = append(e.runes[:e.pos], e.runes[e.pos+1:]...)
		return true
	case "ctrl+u":
		if len(e.runes) == 0 {
			return false
		}
		e.runes, e.pos = e.runes[:0], 0
		return true
	case "ctrl+k":
		if e.pos >= len(e.runes) {
			return false
		}
		e.runes = e.runes[:e.pos]
		return true
	case "ctrl+w":
		return e.deleteWord()
	}
	// Anything with literal text is text: printable characters, and the space
	// key, which is legal inside a name.
	if m.Text == "" {
		return false
	}
	e.insert(m.Text)
	return true
}

func (e *lineEdit) insert(s string) {
	add := []rune(s)
	e.runes = append(e.runes, add...)
	copy(e.runes[e.pos+len(add):], e.runes[e.pos:])
	copy(e.runes[e.pos:], add)
	e.pos += len(add)
}

// deleteWord removes the word before the cursor, which is the one editing
// shortcut worth having when a name has interior spaces.
func (e *lineEdit) deleteWord() bool {
	if e.pos == 0 {
		return false
	}
	i := e.pos
	for i > 0 && e.runes[i-1] == ' ' {
		i--
	}
	for i > 0 && e.runes[i-1] != ' ' {
		i--
	}
	e.runes = append(e.runes[:i], e.runes[e.pos:]...)
	e.pos = i
	return true
}

// caret is drawn as a character rather than as a colour so that the cursor is
// still visible with colour switched off, which is the same reasoning the board
// renderer uses for its bracketed cursor.
const caret = "|"

// render draws the field with its prompt and caret, clipped to width so a long
// name scrolls rather than wrapping the frame.
func (e *lineEdit) render(st *ui.Styles, width int) string {
	const prompt = "> "
	const promptWidth = 2
	line := string(e.runes[:e.pos]) + paint(st, &st.Cursor, caret) + string(e.runes[e.pos:])
	if width <= promptWidth {
		// No room for text. The frame clips anyway, and a negative budget
		// below would ask TruncateLeft for more than there is.
		return prompt
	}
	if w := ansi.StringWidth(line); w > width-promptWidth {
		// Keep the caret in view by dropping characters from the left.
		line = ansi.TruncateLeft(line, w-(width-promptWidth), "")
	}
	return prompt + line
}
