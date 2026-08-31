package ui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
)

// Demo is a Bubble Tea model exercising the whole view layer: a hotseat
// sandbox where both sides are played from the keyboard. Game screens are
// built elsewhere; this model exists to prove the renderer, layout, viewport
// and keymap against a live engine, and to serve as the wiring example.
type Demo struct {
	g      *game.Game
	styles Styles
	keymap Keymap
	board  BoardView

	width, height int
	linkMode      bool
	message       string
}

// NewDemo returns a demo model for the given ruleset. Colour is dropped when
// NO_COLOR is set, per the convention.
func NewDemo(rs game.Ruleset) (*Demo, error) {
	g, err := game.New(rs)
	if err != nil {
		return nil, err
	}
	styles := DefaultStyles()
	if os.Getenv("NO_COLOR") != "" {
		styles = PlainStyles()
	}
	n := rs.Size
	return &Demo{
		g:      g,
		styles: styles,
		keymap: DefaultKeymap(),
		board: BoardView{
			Scale:      Compact,
			Cursor:     game.Point{Col: n / 2, Row: n / 2},
			ShowCursor: true,
		},
	}, nil
}

// Init implements tea.Model. The initial window size arrives as a message.
func (d *Demo) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (d *Demo) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return d.handleKey(msg.String())
	}
	return d, nil
}

func (d *Demo) handleKey(key string) (tea.Model, tea.Cmd) {
	ctx := CtxBoard
	if d.linkMode {
		ctx = CtxLink
	}
	b, ok := d.keymap.Lookup(ctx, key)
	if !ok {
		return d, nil
	}
	d.message = ""
	switch b.Action {
	case ActMoveLeft:
		d.moveCursor(-1, 0)
	case ActMoveRight:
		d.moveCursor(1, 0)
	case ActMoveUp:
		d.moveCursor(0, -1)
	case ActMoveDown:
		d.moveCursor(0, 1)
	case ActJumpLeft:
		d.moveCursor(-JumpStep, 0)
	case ActJumpRight:
		d.moveCursor(JumpStep, 0)
	case ActJumpUp:
		d.moveCursor(0, -JumpStep)
	case ActJumpDown:
		d.moveCursor(0, JumpStep)
	case ActEdgeTop:
		d.moveCursorTo(d.board.Cursor.Col, 0)
	case ActEdgeBottom:
		d.moveCursorTo(d.board.Cursor.Col, d.g.Size()-1)
	case ActEdgeLeft:
		d.moveCursorTo(0, d.board.Cursor.Row)
	case ActEdgeRight:
		d.moveCursorTo(d.g.Size()-1, d.board.Cursor.Row)
	case ActPlacePeg:
		d.placePeg()
	case ActConfirm:
		if d.g.Staged().PegPlaced {
			d.commitTurn()
		} else {
			d.placePeg()
		}
	case ActLinkMode:
		d.linkMode = !d.linkMode
		if d.linkMode && d.g.At(d.board.Cursor) != d.g.Turn() {
			d.message = "link mode: move onto one of your pegs"
		}
	case ActToggleLink:
		d.toggleLink(key)
	case ActAbortTurn:
		d.g.AbortTurn()
		d.message = "turn aborted"
	case ActExitMode:
		d.linkMode = false
	case ActQuit:
		return d, tea.Quit
	}
	return d, nil
}

// moveCursor shifts the cursor by a delta, clamped to the board; a corner
// target backs off along the axis of motion so the cursor always rests on a
// real hole.
func (d *Demo) moveCursor(dCol, dRow int) {
	c := d.board.Cursor
	d.moveCursorTo(c.Col+dCol, c.Row+dRow)
}

func (d *Demo) moveCursorTo(col, row int) {
	n := d.g.Size()
	from := d.board.Cursor
	col = clamp(col, 0, n-1)
	row = clamp(row, 0, n-1)
	if (col == 0 || col == n-1) && (row == 0 || row == n-1) {
		// Corner hole does not exist: give way along the axis of motion.
		if col != from.Col {
			col += sign(from.Col - col)
		} else if row != from.Row {
			row += sign(from.Row - row)
		} else {
			return
		}
	}
	d.board.Cursor = game.Point{Col: col, Row: row}
}

func (d *Demo) placePeg() {
	if err := d.g.PlacePeg(d.board.Cursor); err != nil {
		d.message = friendly(err)
		return
	}
	d.message = fmt.Sprintf("peg at %s — enter commits", d.board.Cursor)
}

func (d *Demo) commitTurn() {
	res, err := d.g.CommitTurn()
	if err != nil {
		d.message = friendly(err)
		return
	}
	d.linkMode = false
	if res.Over() {
		d.message = "game over"
		return
	}
	d.message = fmt.Sprintf("%s to move", d.g.Turn())
}

// toggleLink adds or removes the link from the cursor's peg in the direction
// named by the pressed digit.
func (d *Demo) toggleLink(key string) {
	dir := game.Dir(key[0] - '1')
	a := d.board.Cursor
	b := a.Add(dir)
	l, ok := game.NewLink(a, b)
	if !ok {
		return
	}
	var err error
	if d.g.HasLink(l) {
		err = d.g.RemoveLink(a, b)
	} else {
		err = d.g.AddLink(a, b)
	}
	if err != nil {
		d.message = friendly(err)
		return
	}
	d.message = fmt.Sprintf("link %s", l)
}

// friendly maps engine errors to short human phrasing; unknown errors pass
// through as they are already descriptive sentinels.
func friendly(err error) string {
	switch err {
	case game.ErrOccupied:
		return "that hole is taken"
	case game.ErrOpponentBorder:
		return "that is your opponent's border row"
	case game.ErrPegAlreadySet:
		return "one peg per turn — enter commits"
	case game.ErrNotOwnPeg:
		return "needs two of your pegs"
	case game.ErrLinkCrosses:
		return "a link is in the way"
	case game.ErrNoPegPlaced:
		return "place a peg first"
	}
	return err.Error()
}

// linkDigits builds the link-mode overlay: a digit on every knight neighbour
// of the cursor that holds one of the mover's pegs.
func (d *Demo) linkDigits() map[game.Point]rune {
	if !d.linkMode || d.g.At(d.board.Cursor) != d.g.Turn() {
		return nil
	}
	digits := make(map[game.Point]rune, game.NumDirs)
	for dir := game.Dir(0); dir < game.NumDirs; dir++ {
		target := d.board.Cursor.Add(dir)
		if d.g.Exists(target) && d.g.At(target) == d.g.Turn() {
			digits[target] = rune('1' + dir)
		}
	}
	return digits
}

// View implements tea.Model.
func (d *Demo) View() tea.View {
	arr := Arrange(d.width, d.height, d.g.Size())
	var frame string
	if arr.TooSmall {
		frame = Compose(arr, nil, nil, "", &d.styles)
	} else {
		d.board.Scale = arr.Scale
		d.board.Digits = d.linkDigits()
		d.board.Highlights = d.stagedHighlight()
		board := d.board.Render(d.g, &d.styles, arr.BoardAvailW, arr.BoardAvailH)
		panel := d.panelLines(arr)
		frame = Compose(arr, board, panel, d.statusLine(), &d.styles)
	}
	v := tea.NewView(frame)
	v.AltScreen = true
	return v
}

// stagedHighlight marks the uncommitted peg so the player sees what enter
// will commit.
func (d *Demo) stagedHighlight() []game.Point {
	if st := d.g.Staged(); st.PegPlaced {
		return []game.Point{st.Peg}
	}
	return nil
}

// panelLines builds the information panel: turn state, staged edits, link
// mode legend and key help, sized for the arrangement's panel box.
func (d *Demo) panelLines(arr Arrangement) []string {
	if arr.Panel == PanelNone {
		return nil
	}
	st := &d.styles
	var lines []string
	lines = append(lines, st.apply(styLabel, "twixt sandbox"))
	lines = append(lines, "")

	turn := d.g.Turn()
	glyph, id := string(glyphPegVertical), styPegVertical
	if turn == game.Horizontal {
		glyph, id = string(glyphPegHorizontal), styPegHorizontal
	}
	if res := d.g.Result(); res.Over() {
		lines = append(lines, st.apply(styLabel, fmt.Sprintf("game over: %v", res.Outcome)))
	} else {
		lines = append(lines, st.apply(id, glyph)+" "+turn.String()+" to move")
	}
	lines = append(lines, fmt.Sprintf("move %d", d.g.Ply()+1))

	if staged := d.g.Staged(); staged.PegPlaced {
		lines = append(lines, fmt.Sprintf("staged: peg %s", staged.Peg))
	}
	if d.linkMode {
		lines = append(lines, "")
		lines = append(lines, st.apply(styLinkDigit, "link mode")+" — digits toggle")
		lines = append(lines, "   8 1")
		lines = append(lines, "  7   2")
		lines = append(lines, "   [·]")
		lines = append(lines, "  6   3")
		lines = append(lines, "   5 4")
	}

	lines = append(lines, "")
	ctx := CtxBoard
	if d.linkMode {
		ctx = CtxLink
	}
	for _, e := range d.keymap.HelpEntries(ctx) {
		lines = append(lines, st.apply(styLabel, padRight(e.Label, 6))+e.Help)
	}
	return lines
}

// statusLine is the single always-present line: a message when something
// happened, otherwise the essential keys, labelled straight from the keymap.
func (d *Demo) statusLine() string {
	if d.message != "" {
		return d.styles.apply(styLabel, d.message)
	}
	var hint string
	if d.linkMode {
		hint = d.keymap.HintLine(CtxLink, ActToggleLink, ActExitMode, ActConfirm)
	} else {
		hint = d.keymap.HintLine(CtxBoard, ActPlacePeg, ActConfirm, ActLinkMode, ActQuit)
	}
	return d.styles.apply(styLabel, hint)
}

func padRight(s string, width int) string {
	r := len([]rune(s))
	if r >= width {
		return s
	}
	return s + strings.Repeat(" ", width-r)
}
