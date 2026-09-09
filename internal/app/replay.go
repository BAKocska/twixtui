package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/ui"
)

// ReplayScreen walks through a finished game one move at a time.
//
// The record is replayed in full when the screen opens, because replaying
// forwards is the same path a saved game takes when it is loaded: a record that
// cannot be replayed is refused here instead of appearing to work until the
// player steps into the part of it that does not. What that replay leaves
// behind is one position and the entries that lead to it, not a position per
// entry — see recordCursor.
type ReplayScreen struct {
	deps  Deps
	saved gamestore.Saved

	cursor recordCursor
	// stuck is what the panel says about a seek that could not be made. The next
	// seek that can be clears it.
	stuck string
	// jump is the entry-number input, open only while a number is being typed.
	jump entryJump
	// winning is the run of pegs that won the game, worked out once when the
	// screen opens: it cannot change, and it belongs to one position — the last
	// one, where the chain the record ends in is actually on the board.
	winning []game.Point

	// shown are the entry labels as they are drawn, and who and id are the
	// store's own notes about the game as they are drawn. All three are made
	// safe here, once, rather than in the frame: a frame is built per keypress,
	// and none of this text changes while the screen is up.
	shown   []string
	who, id string

	board ui.BoardView
	// hints are the footer's key parts, built once from the keymap because the
	// keys cannot change while the screen is up.
	hints []string
	// confirm are the keys that commit the number typed into the input, taken
	// from the keymap so that a rebinding moves them.
	confirm       []string
	width, height int
}

// recordCursor is the one position a replay shows, together with the record it
// moves through.
//
// A position per entry is what this replaces. Each one was a whole Game — the
// board, the connectivity structure and a copy of the record so far — so the
// cost grew with the square of the record: opening a 400-entry 48x48 game
// allocated tens of megabytes, and every position but the one on screen was
// waiting to be looked at rather than being looked at. Only the position on
// screen is kept now. Moving back is the engine's own undo and moving forward
// is the engine's own replay of entries the record was already validated by, so
// the position shown is reached by the paths a game itself takes.
//
// Each seek undoes or replays the entries it crosses. There are no checkpoints;
// benchmarks measure this traversal cost separately from construction.
type recordCursor struct {
	// labels[i] is the entry that turns position i into position i+1, in the
	// notation the record itself is written in.
	labels []string
	// game is the position after at entries. The cursor owns it and moves it in
	// place, so nothing else may hold it across a seek.
	game *game.Game
	at   int
	// result is the record's own result, taken before the cursor moved. Stepping
	// back unwinds the game's result along with the rest of the position, so the
	// position on screen cannot be asked how the game ended.
	result game.Result
}

// newRecordCursor takes ownership of final — the game the record replays to —
// and opens at that final position, which is where a review starts.
//
// moves is the record's own transcript. Splitting it is how the labels are had:
// rendering them from the history instead means Game.MoveNotation per entry,
// each of which replays the game from the beginning to work out which offered
// links the player declined, so a screen that has just replayed the record once
// would replay it once more per entry.
func newRecordCursor(final *game.Game, moves string) (recordCursor, error) {
	labels := recordLabels(moves, final.Entries())
	if len(labels) != final.Entries() {
		return recordCursor{}, fmt.Errorf("the transcript has %d entries but the record makes %d", len(labels), final.Entries())
	}
	return recordCursor{labels: labels, game: final, at: len(labels), result: final.Result()}, nil
}

// recordLabels splits a transcript into its entries, the way ReplayTranscript
// reads it. The two have to agree: a label is replayed as an entry, so it must
// be the same text that was validated as one. A record this program stored is
// canonical, but a record handed in from somewhere else is spaced however its
// writer liked, and blank stretches between semicolons are entries in neither.
func recordLabels(moves string, entries int) []string {
	labels := make([]string, 0, entries)
	for part := range strings.SplitSeq(moves, ";") {
		if part = strings.TrimSpace(part); part != "" {
			labels = append(labels, part)
		}
	}
	return labels
}

// seek moves to the position after to entries, clamped to the record.
//
// It reports the first entry it could not make. The cursor is left at the last
// position it did reach, which is a position the record passes through rather
// than a half-applied one, so a caller that reports the failure is still
// showing a real board.
func (c *recordCursor) seek(to int) error {
	to = min(max(to, 0), len(c.labels))
	// Undo reconstructs draw-offer history. Limit it to one navigation jump;
	// larger backward seeks replay a prefix so offer-dense records cannot turn
	// a single request into quadratically many history visits.
	if c.at-to > replayJump {
		fresh, err := game.New(c.game.Rules())
		if err != nil {
			return err
		}
		c.game, c.at = fresh, 0
	}
	for c.at > to {
		if err := c.game.UndoLastMove(); err != nil {
			return fmt.Errorf("taking back entry %d: %w", c.at, err)
		}
		c.at--
	}
	for c.at < to {
		label := c.labels[c.at]
		if err := c.game.PlayNotation(label); err != nil {
			// A refused entry can leave part of a turn staged. Discarding it is
			// what keeps at meaning the position the game is actually in.
			c.game.AbortTurn()
			return fmt.Errorf("replaying entry %d (%q): %w", c.at+1, label, err)
		}
		c.at++
	}
	return nil
}

// current returns the position the cursor is on. It is the cursor's own game,
// and the next seek moves it.
func (c *recordCursor) current() *game.Game { return c.game }

// entries returns how many entries the record has, which is also the index of
// the final position.
func (c *recordCursor) entries() int { return len(c.labels) }

// replayJump is how many moves the up and down keys travel. It is deliberately
// not the keymap's JumpStep, which is a distance across a board rather than a
// distance through a record.
const replayJump = 5

// replayKey is one thing this screen does together with every key that does
// it: a board action from the shared keymap, whose label the footer shows, and
// any key of replay's own for the same thing. Dispatch and the footer are both
// built from this, so a key answered in silence, or a footer naming a key the
// screen ignores, cannot happen. Neighbouring rows with the same help share
// one footer part.
type replayKey struct {
	action ui.Action
	own    []string
	help   string
	// do is what the key does to the screen, and what the screen answers with:
	// move through the record, open the entry input, or leave.
	do func(s *ReplayScreen) tea.Cmd
}

// replayKeys is the whole of what the replay screen answers, in footer order.
// Stepping through a record is not moving a cursor over holes, so the wording is
// replay's own, but the keys are the keymap's: the product teaches h, j, k and l
// on every other board, and a replay is no place to teach something else.
//
// The entry input comes first because the footer is dropped from the end: it is
// the one key that reaches a long record in one go, so it is the one that has to
// survive a narrow terminal, where the footer is the whole of the interface.
var replayKeys = []replayKey{
	{ui.ActNone, []string{":"}, "jump", func(s *ReplayScreen) tea.Cmd { s.jump.start(s.entries()); return nil }},
	{ui.ActMoveRight, []string{"n"}, "next", func(s *ReplayScreen) tea.Cmd { s.seek(s.step() + 1); return nil }},
	{ui.ActMoveLeft, []string{"p"}, "back", func(s *ReplayScreen) tea.Cmd { s.seek(s.step() - 1); return nil }},
	{ui.ActMoveDown, nil, "five", func(s *ReplayScreen) tea.Cmd { s.seek(s.step() + replayJump); return nil }},
	{ui.ActMoveUp, nil, "five", func(s *ReplayScreen) tea.Cmd { s.seek(s.step() - replayJump); return nil }},
	{ui.ActEdgeTop, nil, "ends", func(s *ReplayScreen) tea.Cmd { s.seek(0); return nil }},
	{ui.ActEdgeBottom, nil, "ends", func(s *ReplayScreen) tea.Cmd { s.seek(s.entries()); return nil }},
	{ui.ActQuit, []string{"esc"}, "leaves", func(*ReplayScreen) tea.Cmd { return Back() }},
}

// replayHints renders the footer's key parts, taking the labels from the keymap
// so that a rebinding moves the hint along with the key.
func replayHints(km ui.Keymap) []string {
	parts := make([]string, 0, len(replayKeys))
	labels := make([]string, 0, 4)
	help := ""
	flush := func() {
		if len(labels) > 0 {
			parts = append(parts, strings.Join(labels, " ")+" "+help)
			labels = labels[:0]
		}
	}
	for _, r := range replayKeys {
		if r.help != help {
			flush()
			help = r.help
		}
		// A row with no action of its own takes its labels from its own keys.
		// Asking the keymap for ActNone would name whatever a rebinding had
		// left unmapped.
		if r.action != ui.ActNone {
			if b, ok := km.ByAction(ui.CtxBoard, r.action); ok && b.Label != "" {
				labels = append(labels, b.Label)
			}
		}
		labels = append(labels, r.own...)
	}
	flush()
	return parts
}

// NewReplayScreen prepares a stored game for review. The record is decoded,
// replayed and checked here, so a record that has been altered is refused
// before the screen exists rather than at the step that reaches the damage.
func NewReplayScreen(d Deps, saved gamestore.Saved) (Screen, error) {
	final, record, err := saved.Load()
	if err != nil {
		return nil, err
	}
	cursor, err := newRecordCursor(final, record.Moves)
	if err != nil {
		return nil, fmt.Errorf("saved game %s: %w", saved.ID, err)
	}
	km := shellKeymap(d)
	// The input needs a key that commits it, and the keymap is where the
	// product's is written down.
	confirm := []string{"enter"}
	if b, ok := km.ByAction(ui.CtxBoard, ui.ActConfirm); ok && len(b.Keys) > 0 {
		confirm = b.Keys
	}
	return &ReplayScreen{
		deps:    d,
		saved:   saved,
		cursor:  cursor,
		board:   ui.BoardView{Scale: ui.Compact},
		hints:   replayHints(km),
		confirm: confirm,
		shown:   displayLabels(cursor.labels),
		who:     replayLabel(saved.Player) + " vs " + replayLabel(leaderboard.DisplayName(saved.Opponent)),
		id:      replayLabel(saved.ID),
		// The chain is searched for once, here, on the position the record
		// reaches. A View that looked for it per frame would walk the link
		// graph of a 48-hole board on every keypress to draw the same holes.
		winning: winningChain(cursor.current()),
	}, nil
}

// Init satisfies tea.Model.
func (s *ReplayScreen) Init() tea.Cmd { return nil }

// Update satisfies tea.Model.
func (s *ReplayScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = m.Width, m.Height
	case tea.KeyPressMsg:
		return s, s.press(m)
	case tea.PasteMsg:
		// The entry input is the only field on this screen, so a paste is a
		// number for it or nothing at all. The spaces are what a paste out of a
		// chat window brings with it; the digits are bounded and checked before
		// any of them are kept.
		if s.jump.open {
			if len(m.Content) > entryPasteMax {
				s.jump.problem = fmt.Sprintf("max %d digits", s.jump.digits)
			} else {
				s.jump.insert(strings.TrimSpace(m.Content))
			}
		}
	}
	return s, nil
}

// press interprets one key through the same table the footer is built from, so
// that the keys the screen answers and the keys it names are one list.
func (s *ReplayScreen) press(m tea.KeyPressMsg) tea.Cmd {
	key := m.String()
	// The entry input owns the keyboard while it is up. A screen that went on
	// answering its own keys would move the record out from under a player who
	// is typing where they want to go, and would leave the review altogether on
	// the q of a mistyped number. The shell answers the control forms of the
	// quit key before this, so there is always a way out of the program.
	if s.jump.open {
		s.editEntry(m)
		return nil
	}
	action := ui.ActNone
	// The control forms of a binding are the shell's: it answers them before
	// this screen is asked, so the footer does not name them and dispatch does
	// not claim them.
	if !strings.HasPrefix(key, "ctrl+") {
		if b, ok := shellKeymap(s.deps).Lookup(ui.CtxBoard, key); ok {
			action = b.Action
		}
	}
	for _, r := range replayKeys {
		// A row with no action of its own is reached by its own keys only:
		// matching ActNone against a key the keymap does not bind would answer
		// every such key with the same thing.
		if r.action == ui.ActNone || r.action != action {
			if !slices.Contains(r.own, key) {
				continue
			}
		}
		return r.do(s)
	}
	return nil
}

// editEntry hands one key to the entry input: the number is taken, the input is
// abandoned, or the key is text for the field to keep or refuse.
func (s *ReplayScreen) editEntry(m tea.KeyPressMsg) {
	switch key := m.String(); {
	case key == "esc":
		// Escape leaves the input, not the screen. A player who opened it by
		// accident is put back where they were reviewing, which is also why
		// nothing about the position changes here.
		s.jump.cancel()
	case matchesKey(key, s.confirm):
		s.jumpToEntry()
	default:
		s.jump.key(m)
	}
}

// jumpToEntry acts on the number that has been typed.
//
// A number the record has no entry for is refused rather than clamped: someone
// who asks for entry 40 of 17 has mistyped or misremembered, and the end of the
// record answers neither question while looking as though it had.
func (s *ReplayScreen) jumpToEntry() {
	value := s.jump.edit.value()
	if value == "" {
		s.jump.problem = "enter a number"
		return
	}
	// The field holds digits only, and no more of them than the last entry's
	// number has, so this parses unless the number is past the end.
	n, err := strconv.Atoi(value)
	if err != nil || n > s.entries() {
		s.jump.problem = fmt.Sprintf("range 0..%d", s.entries())
		return
	}
	s.jump.cancel()
	s.seek(n)
}

// seek shows the position after to entries, or the nearest one the record has.
// A seek that cannot be made leaves the last position it reached on screen and
// says so in the panel, rather than leaving a board that is neither where the
// player was nor where they asked to be.
func (s *ReplayScreen) seek(to int) {
	if err := s.cursor.seek(to); err != nil {
		s.stuck = err.Error()
		return
	}
	s.stuck = ""
}

// step is where in the record the player is: how many entries have been applied
// to the position on screen.
func (s *ReplayScreen) step() int { return s.cursor.at }

// entries is how many entries the record has, and so the last step.
func (s *ReplayScreen) entries() int { return s.cursor.entries() }

// current returns the position being shown.
func (s *ReplayScreen) current() *game.Game { return s.cursor.current() }

// View satisfies tea.Model.
func (s *ReplayScreen) View() tea.View {
	st := shellStyles(s.deps)
	arr := ui.Arrange(s.width, s.height, s.current().Size())
	if arr.TooSmall {
		return tea.NewView(ui.Compose(arr, nil, nil, "", st))
	}
	focus, showCursor := s.focus()
	if !showCursor {
		s.board = ui.BoardView{} // no last move: reset the viewport to the opening
	}
	s.board.Scale = arr.Scale
	// The viewport follows the record instead of a cursor of the player's: the
	// entry on screen is the one they asked for, and on a 48-hole board the hole
	// it played is off the viewport as often as not. The cursor is what the
	// viewport is built to keep in sight, so it is put on that hole, and no key
	// moves it: there is nothing here to navigate to, and a review whose h and l
	// meant two things at once would teach neither.
	s.board.Cursor, s.board.ShowCursor = focus, showCursor
	// The chain belongs to the position that won. Marked anywhere else it calls
	// out holes that are still empty, which is worse than not marking it: the
	// board would be showing a connection the game had not made yet.
	s.board.Highlights = nil
	if s.step() == s.entries() {
		s.board.Highlights = s.winning
	}
	board := s.board.Render(s.current(), st, arr.BoardAvailW, arr.BoardAvailH)
	panel := s.panel(arr.PanelW, arr.PanelH)
	status := s.status(arr.Width)
	return tea.NewView(ui.Compose(arr, board, panel, status, st))
}

// focus is the hole the board is kept in sight of: the peg the entry on screen
// put down, or the last one placed before it when that entry moved no peg,
// since a draw offer or a resignation happens to a position rather than to a
// place. A record that has not placed a peg yet has nothing to look at, and the
// board is shown from its own origin.
func (s *ReplayScreen) focus() (game.Point, bool) {
	history := s.current().History()
	for i := len(history) - 1; i >= 0; i-- {
		if m := history[i]; m.Kind.ConsumesTurn() {
			return m.Peg, true
		}
	}
	return game.Point{}, false
}

// status is the bottom row: where in the record the player is, then what the
// keys do, dropped from the end when the terminal is too narrow for all of it.
//
// At the widths that have no room for a panel it is the whole of the interface,
// so the entry input is drawn here as well as there, and what is wrong with a
// number comes before the keys that would fix it.
func (s *ReplayScreen) status(width int) string {
	// These are record entries rather than moves: a draw offer is an entry that
	// changes nothing on the board, so counting them as moves disagreed with
	// every other surface's move count.
	counter := fmt.Sprintf("step %d of %d", s.step(), s.entries())
	if s.jump.open {
		note := s.jump.note()
		fieldWidth := max(3, width-ansi.StringWidth(note)-1)
		input := note + " " + s.jump.edit.render(shellStyles(s.deps), fieldWidth)
		return hintLine(width, input, counter, keyLabel(s.confirm...)+" jump", "esc cancel")
	}
	parts := make([]string, 0, len(s.hints)+1)
	parts = append(parts, counter)
	return hintLine(width, append(parts, s.hints...)...)
}

// panel is the record beside the board: where in it the player is, what it came
// to, and the entries themselves as a list to move through.
//
// The list is promised its share of the rows before the prose is given any. The
// panel below a board is four rows at its smallest, and one that spent every row
// on titles and results would leave a review with nothing to review.
func (s *ReplayScreen) panel(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	out := make([]string, 0, height)
	// The input goes first because it is what the player is doing.
	out = append(out, clampLines(s.jump.lines(shellStyles(s.deps), width), height)...)
	list := min(s.entries()+1, max(1, height/2))
	if room := height - len(out) - list; room > 1 {
		out = append(out, clampLines(s.describe(width), room-1)...)
		out = append(out, "")
	}
	return append(out, s.entryList(width, height-len(out))...)
}

// describe is the panel's prose, most useful line first, because a short panel
// keeps the beginning of it and drops the end.
func (s *ReplayScreen) describe(width int) []string {
	g := s.current()
	lines := make([]string, 0, 10)
	add := func(text string) { lines = append(lines, truncateText(text, width)) }
	wrap := func(text string) { lines = append(lines, wrapText(text, width)...) }
	add(fmt.Sprintf("step %d of %d", s.step(), s.entries()))
	if s.stuck != "" {
		wrap("cannot go there: " + s.stuck)
	}
	add(fmt.Sprintf("moves played: %d", g.Ply()))
	// The result is the record's, not the position's: stepping back into the
	// middle of a game unwinds the position's own result, and a review still has
	// to say how the game it is reviewing ended.
	wrap("result: " + describeOutcome(s.cursor.result))
	add(s.who)
	add(g.Rules().Describe())
	add(s.id)
	return lines
}

// entryList renders the rows of the entry list that fit in n rows, centred on
// the position shown. Row 0 is the opening position, so a row's number is both
// what the input takes and how many entries have been applied to reach it —
// which is not its ply: a draw offer is an entry that plays no move.
//
// Only the rows that are drawn are built. Rendering all four hundred of a long
// record to show a dozen of them would rebuild the whole list on every keypress,
// which is the mistake keeping a position per entry was.
func (s *ReplayScreen) entryList(width, n int) []string {
	total := s.entries() + 1
	if width <= 0 || n <= 0 {
		return nil
	}
	n = min(n, total)
	start := min(max(s.step()-n/2, 0), total-n)
	st := shellStyles(s.deps)
	// The numbers share a column so that the labels beside them line up, and
	// they are padded on the right: the number belongs with the marker in front
	// of it, and a marker followed by blanks reads as a gap.
	digits := len(strconv.Itoa(s.entries()))
	rows := make([]string, 0, n)
	for i := start; i < start+n; i++ {
		label := "opening position"
		if i > 0 {
			label = s.shown[i-1]
		}
		// The row being reviewed is marked with a glyph as well as a colour, so
		// that it is still the marked row with colour switched off.
		marker := "  "
		if i == s.step() {
			marker = paint(st, &st.Cursor, "> ")
		}
		rows = append(rows, marker+truncateText(padTo(strconv.Itoa(i), digits)+" "+label, max(0, width-2)))
	}
	return rows
}

// entryJump is the entry-number input, which the ":" key opens.
//
// With it a record of any length is one number away, which is what a list alone
// cannot give: reaching entry 300 of 400 by stepping is sixty keypresses. It is
// modal because the digits are the record's own numbers, and a screen that went
// on answering its navigation keys while they were being typed would move the
// record out from under the player typing them.
type entryJump struct {
	open bool
	edit lineEdit
	// digits is the most that may be typed. A number with more digits than the
	// last entry's number cannot name an entry, so the field refuses it instead
	// of holding a number nothing can be done with — which is also what keeps a
	// pasted megabyte from being scanned, let alone parsed.
	digits int
	// last is the final entry's number, the largest the field takes.
	last int
	// problem is what was wrong with what was typed. It stands until the text
	// changes, because a message cleared by the next keypress is gone before it
	// has been read.
	problem string
}

// start opens the input on a record whose last entry is last.
func (j *entryJump) start(last int) {
	last = max(last, 0)
	*j = entryJump{open: true, digits: len(strconv.Itoa(last)), last: last}
}

// cancel closes the input, forgetting what was typed. The position on screen is
// not touched: cancelling asks for nothing.
func (j *entryJump) cancel() { *j = entryJump{} }

// key applies a keypress: text is a digit for the field or a refusal, and
// anything else is the field's own editing and movement.
func (j *entryJump) key(m tea.KeyPressMsg) {
	if m.Text != "" {
		j.insert(m.Text)
		return
	}
	if j.edit.key(m) {
		j.problem = ""
	}
}

// entryPasteMax bounds what is looked at at all. A paste is text from anywhere
// and can be a whole file; nothing longer than this can be an entry number
// under any reading, so it is refused on its length alone, before it is scanned
// or copied.
const entryPasteMax = 64

// insert takes typed or pasted text, which has to be a number and has to fit
// the field's bound before any of it is kept.
func (j *entryJump) insert(text string) {
	if text == "" {
		return
	}
	if len(text) > entryPasteMax {
		j.problem = fmt.Sprintf("max %d digits", j.digits)
		return
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			j.problem = "digits only"
			return
		}
	}
	// Forty keystrokes of nines leave the field holding the digits it may, and
	// say why the rest were refused.
	if len(text) > j.digits-len(j.edit.value()) {
		j.problem = fmt.Sprintf("max %d digits", j.digits)
		return
	}
	j.edit.insert(text)
	j.problem = ""
}

// lines are the input's rows in the panel: the field, and either the range it
// takes or what was wrong with what was typed. There are always two of them, so
// the list below does not shift as the player types.
func (j *entryJump) lines(st *ui.Styles, width int) []string {
	if !j.open || width <= 0 {
		return nil
	}
	return []string{j.edit.render(st, width), truncateText(j.note(), width)}
}

// note is what the input says about itself: the problem while there is one,
// because a message the player has to act on outranks a range they can read off
// the list.
func (j *entryJump) note() string {
	if j.problem != "" {
		return j.problem
	}
	return fmt.Sprintf("entry 0..%d", j.last)
}

// replayLabel makes text from the store safe to draw, and is applied once per
// string when the screen opens rather than per frame.
//
// Two kinds of text need it, for two reasons. The notes around the record — who
// played, what the game was filed as — are this machine's own JSON, which a
// player may edit and another program may have written badly; an escape
// sequence in a name would be obeyed by the terminal rather than shown. The
// record's own entry labels are notation, so nothing in them is unprintable,
// but notation is allowed its own spacing: a record handed in from elsewhere is
// written however its writer liked, and a tab inside an entry is an entry the
// parser accepts and the terminal acts on rather than draws.
//
// Whitespace is therefore collapsed rather than dropped: it separates the parts
// of a label, and removing it would join two of them into a token that was
// never in the record.
func replayLabel(text string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// Format characters include the bidirectional overrides, which can
			// make text display in an order other than the one it is stored in.
			return -1
		}
		return r
	}, text)
	if strings.IndexByte(clean, ' ') < 0 {
		return clean
	}
	return strings.Join(strings.Fields(clean), " ")
}

// displayLabels renders the record's entries for the list. The cursor keeps the
// record's own text, which is what was validated as entries and what is replayed
// to move through them; this is the copy that is drawn.
func displayLabels(labels []string) []string {
	shown := make([]string, len(labels))
	for i, l := range labels {
		shown[i] = replayLabel(l)
	}
	return shown
}

// describeOutcome renders a result in words, matching the wording used elsewhere.
func describeOutcome(r game.Result) string {
	if !r.Over() {
		return "still being played"
	}
	reason := map[game.Reason]string{
		game.Connection:  "by completing a chain",
		game.NoMovesLeft: "with no legal moves left",
		game.Resignation: "by resignation",
		game.Agreement:   "by agreement",
	}[r.Reason]
	switch r.Outcome {
	case game.Draw:
		return "drawn " + reason
	case game.VerticalWins:
		return "vertical won " + reason
	case game.HorizontalWins:
		return "horizontal won " + reason
	}
	return "unknown"
}
