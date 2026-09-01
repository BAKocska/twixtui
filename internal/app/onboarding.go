package app

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/learn"
	"github.com/BAKocska/twixtui/internal/ui"
)

// The introduction shown on a first run: five steps that say what TwixT is,
// whose edges are whose, and what a peg and a link do, on the real board with
// the real engine underneath.
//
// It is not a short tutorial, and the difference is the whole design. The
// tutorial in internal/learn teaches: it sets tasks, refuses to advance until
// they are answered, and is worth an evening. The introduction only orients,
// and a player who wants to get on with a game must be able to leave it at any
// point, from any step, having read none of it. So nothing here gates: every
// step advances on the pager key whether or not the player did what it invited
// them to do, no step refuses a legal move, and the key that leaves is named on
// the status line of every frame.
//
// What it borrows from internal/learn is everything that would otherwise be a
// second copy: the ruleset its positions assume, the replay that turns a move
// list into a position, and the layout a lesson uses. What it does not borrow
// is learn.Task, because a task is precisely the thing the introduction may not
// have.

// onboardingSteps is how many screens the introduction has, named here only so
// that the tests and the prose below cannot disagree about it.
const onboardingSteps = 5

// onboardingTutorialEntry is the menu entry the introduction points at when it
// is finished with the player. It repeats menu.go's label rather than reading
// it, because the menu's entry table is not reachable from here; renaming the
// entry there has to be done here too, which is why it is a constant carrying
// this note and not a phrase inside a sentence.
const onboardingTutorialEntry = "Learn to play"

// onboardingNote is the line the introduction leaves behind on its way out. It
// is left for the shell to place rather than shown as a step, because the step
// that says where the tutorial is is exactly the step a player who skips never
// reaches — and a player who skips is the one most likely to want it later. The
// shell puts the line on the screen underneath, which is the menu the entry is
// on.
const onboardingNote = "There is a seven-lesson tutorial on the menu, under " + onboardingTutorialEntry + "."

// onboardingStep is one screen of the introduction.
type onboardingStep struct {
	// id names the step, as learn.Lesson's does. Nothing the player sees
	// carries it; it exists so that a test can say which step it means without
	// matching prose, which a reworded sentence would silently break.
	id string
	// text is the prose. It carries no line breaks, so it wraps at whatever
	// width the panel turns out to have.
	text string
	// setup is played onto a fresh board before the step, in game notation.
	setup []string
	// highlight marks holes the prose is talking about.
	highlight []game.Point
	// invite is what the player is offered on this step. It is an offer and
	// never a condition: the pager moves on regardless, and a step with an
	// invitation is finished the moment the player wants it to be.
	invite string
	// told is the line the player is given once their peg is down. It sees the
	// position the move produced rather than the one before it, so a step about
	// links reads off the board whether a link appeared instead of asserting
	// that one did — which is the only honest way to run the step where none
	// does.
	told func(g *game.Game, played game.Point) string
}

// position replays the step's setup.
//
// learn.Step.Position is borrowed for the replay because the ruleset it
// replays against, learn.Rules, is the ruleset these positions are written
// for: twelve holes a side so a whole board fits beside its prose, links
// deliberate, and a player's own links blocking. A step is borrowed for that
// and nothing else — an introduction step cannot be a learn.Step outright,
// since a learn.Step carrying a task refuses to advance until the task is
// answered.
func (s onboardingStep) position() (*game.Game, error) {
	return learn.Step{Setup: s.setup}.Position()
}

// onboardingModel is the introduction screen.
type onboardingModel struct {
	deps   Deps
	keymap ui.Keymap
	styles ui.Styles
	// player is the profile this run is playing as, which is not necessarily the
	// stored current one: --profile overrides the stored choice for a single run
	// without writing it back. Reading the flag from the current profile would
	// have asked one player whether another had seen the introduction, and
	// writing it there would have answered for them.
	player string

	steps []onboardingStep
	step  int
	// placed records that this step's invitation has been answered. The engine
	// alternates the mover, so a second peg would be the opponent's while the
	// prose still says "you".
	placed bool
	// done marks the last step as behind us: the panel says so and the pager
	// key leaves.
	done bool

	g     *game.Game
	board ui.BoardView
	// told is what the player's peg on this step earned them, cleared when the
	// step changes.
	told string

	scroll int
	// marked stops the seen flag being written more than once. Every way out of
	// the screen goes through markSeen, and one of them — the shell's global
	// quit key — arrives after another already has.
	marked bool
	// note is taken by the shell on the way out, once.
	note string

	width, height int
}

// NewOnboarding builds the introduction. It is a Screen like any other: the
// caller decides whether to show it, and the shell decides where leaving it
// goes.
// player is the profile the run is playing as. It is passed in rather than read
// from the store because the two can differ for the length of one command.
func NewOnboarding(d Deps, player string) (Screen, error) {
	m := &onboardingModel{
		deps:   d,
		player: player,
		keymap: shellKeymap(d),
		styles: *shellStyles(d),
		steps:  onboardingContent(),
		note:   onboardingNote,
	}
	if len(m.steps) != onboardingSteps {
		return nil, fmt.Errorf("the introduction has %d steps, not %d", len(m.steps), onboardingSteps)
	}
	if err := m.loadStep(0); err != nil {
		return nil, err
	}
	return m, nil
}

// OnboardingSeen reports whether the player at this keyboard has been through
// the introduction.
//
// It answers for the profile that is playing, because being introduced to the
// game is something that happened to a person and not to a machine: two people
// sharing a machine with a profile each are two players, and the second of them
// is exactly the newcomer the introduction was written for. internal/profile
// holds the flag and the argument for it.
//
// Where there is no profile to ask about — no store, or nobody chosen yet — it
// answers false, so the introduction is offered rather than silently skipped.
// That is the safer of the two wrong answers: offering it again costs one
// keypress, and never offering it loses the feature to whoever needed it most.
// In practice the question does not arise, because the launch path asks who is
// playing before it shows anything a player can reach this from.
func OnboardingSeen(d Deps, player string) bool {
	if d.Profiles == nil || player == "" {
		return false
	}
	p, ok := d.Profiles.Get(player)
	return ok && p.Introduced
}

// markSeen records that this player has been through the introduction, which
// leaving early counts as: somebody who skipped it does not want it again next
// launch, and a program that keeps offering something the player has refused is
// the wall this screen is written not to be.
//
// A failure to write is dropped on purpose. The introduction has already been
// shown by the time this runs, there is nothing useful to say about a read-only
// configuration directory at that moment, and the worst it costs is being
// offered again on the next launch.
func (m *onboardingModel) markSeen() {
	if m.marked {
		return
	}
	m.marked = true
	if m.deps.Profiles == nil {
		return
	}
	if m.player == "" {
		return
	}
	_ = m.deps.Profiles.MarkIntroduced(m.player)
}

// Depart implements Departing: the shell answers the control form of the quit
// key itself, so without this the introduction would be seen and not recorded
// by exactly the player who wanted out of it most.
// Depart is the program-wide quit path, which the shell calls without collecting
// a note. So the note is delivered here, the way the game screen delivers its
// own: the line naming the tutorial exists for the player who skipped, and
// ctrl+c is a way of skipping, so it was the one route out that never got it.
func (m *onboardingModel) Depart() {
	m.markSeen()
	if note := m.DepartNote(); note != "" {
		m.deps.note("%s", note)
	}
}

// DepartNote implements Noting.
func (m *onboardingModel) DepartNote() string {
	n := m.note
	m.note = ""
	return n
}

// Init implements tea.Model. The first window size arrives as a message.
func (m *onboardingModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *onboardingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reflow()
	case ThemeChangedMsg:
		m.deps.Theme = msg.Theme
		if msg.Styles != nil {
			m.deps.Styles = msg.Styles
			m.styles = *msg.Styles
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

// reflow brings the pager offset back into range after a resize, since the same
// prose wraps into fewer lines in a wider pane and an offset left over from the
// narrow one would leave the panel blank.
func (m *onboardingModel) reflow() {
	arr := m.arrange()
	if arr.TooSmall || arr.PanelH < 1 {
		m.scroll = 0
		return
	}
	m.scroll = onboardingClampScroll(m.scroll, len(m.panel(arr).lines), arr.PanelH)
}

// handleKey dispatches one key: the introduction's own pager keys first, then
// the board keys from the shared keymap, so the movement and the peg key the
// player learns here are the ones a game really uses.
func (m *onboardingModel) handleKey(key string) tea.Cmd {
	switch {
	case matchesKey(key, onboardingKeys(tutActNext)):
		return m.forward()
	case matchesKey(key, onboardingKeys(tutActPrev)):
		m.backward()
		return nil
	case matchesKey(key, onboardingKeys(tutActBack)):
		return m.leave()
	}
	b, ok := m.keymap.Lookup(ui.CtxBoard, key)
	if !ok {
		return nil
	}
	switch b.Action {
	case ui.ActMoveLeft:
		m.moveCursor(-1, 0)
	case ui.ActMoveRight:
		m.moveCursor(1, 0)
	case ui.ActMoveUp:
		m.moveCursor(0, -1)
	case ui.ActMoveDown:
		m.moveCursor(0, 1)
	case ui.ActJumpLeft:
		m.moveCursor(-ui.JumpStep, 0)
	case ui.ActJumpRight:
		m.moveCursor(ui.JumpStep, 0)
	case ui.ActJumpUp:
		m.moveCursor(0, -ui.JumpStep)
	case ui.ActJumpDown:
		m.moveCursor(0, ui.JumpStep)
	case ui.ActPlacePeg, ui.ActConfirm:
		return m.place()
	case ui.ActQuit:
		return m.leave()
	}
	return nil
}

// onboardingKeys are the introduction's own keys, which ui.Keymap carries no
// bindings for because a game has no pager. They are the tutorial's keys, taken
// from its table rather than written out again: a player who goes on to the
// tutorial from here should not have to learn a second set of pager keys, and
// two tables would eventually disagree about which they are.
func onboardingKeys(a tutorialAction) []string {
	for _, b := range tutorialBindings {
		if b.action == a {
			return b.keys
		}
	}
	return nil
}

// leave ends the introduction, having recorded that the player has seen it.
func (m *onboardingModel) leave() tea.Cmd {
	m.markSeen()
	return Back()
}

// forward is the pager's next: another page of this step while there is one,
// then the next step, then the closing panel, then out.
//
// It never asks whether the player took the step's invitation. A step that
// refused to advance would make the introduction a wall, and the four or five
// things it has to say are worth less than a player's patience.
func (m *onboardingModel) forward() tea.Cmd {
	if m.done {
		return m.leave()
	}
	arr := m.arrange()
	if !arr.TooSmall && arr.PanelH > 0 {
		lines := m.panel(arr).lines
		if next := onboardingClampScroll(m.scroll+arr.PanelH, len(lines), arr.PanelH); next > m.scroll {
			m.scroll = next
			return nil
		}
	}
	if m.step+1 < len(m.steps) {
		if err := m.loadStep(m.step + 1); err != nil {
			return Fail(err)
		}
		return nil
	}
	m.done = true
	m.scroll = 0
	return nil
}

// backward is the pager's previous: back a page, then to the previous step.
func (m *onboardingModel) backward() {
	if m.scroll > 0 {
		arr := m.arrange()
		if !arr.TooSmall && arr.PanelH > 0 {
			m.scroll = max(0, m.scroll-arr.PanelH)
			return
		}
	}
	if m.done {
		m.done = false
		m.scroll = 0
		return
	}
	if m.step > 0 {
		// A step that fails to load going backwards is not worth throwing the
		// player out over: they have already read it once, and staying where
		// they are is a smaller wrong than an error banner over an
		// introduction.
		_ = m.loadStep(m.step - 1)
	}
}

// loadStep replays a step's position and clears what the previous one left
// behind. Nothing the player did on a step is remembered: they were invited
// rather than asked, so there is no answer to restore, and a step that comes
// back as it was first written is the one they can read again.
func (m *onboardingModel) loadStep(i int) error {
	s := m.steps[i]
	g, err := s.position()
	if err != nil {
		return fmt.Errorf("introduction step %d: %w", i+1, err)
	}
	m.step = i
	m.g = g
	m.told = ""
	m.placed = false
	m.scroll = 0
	m.board = ui.BoardView{ShowCursor: true, Cursor: onboardingStartCursor(s, g.Size())}
	return nil
}

// onboardingStartCursor puts the cursor where the step is about, so the first
// frame has the part of the board the prose talks about in view. A step that
// invites a move and marks the holes it means opens with the cursor on one of
// them, which is the opposite of the tutorial's rule: a lesson would be giving
// its answer away, and the introduction has no answer to give.
func onboardingStartCursor(s onboardingStep, n int) game.Point {
	if len(s.highlight) > 0 {
		return s.highlight[0]
	}
	// The middle hole, which on the twelve-hole board these positions use is
	// F6 — the hole the step about notation names, so its example is under the
	// cursor rather than three holes away from it.
	return game.Point{Col: (n - 1) / 2, Row: (n - 1) / 2}
}

// moveCursor shifts the board cursor, clamped to the board and nothing more.
// The four absent corners are not stepped around: a player who walks into one
// and presses the peg key is told that corners do not exist, which is worth
// more than a cursor that mysteriously will not go there.
func (m *onboardingModel) moveCursor(dCol, dRow int) {
	n := m.g.Size()
	c := m.board.Cursor
	m.board.Cursor = game.Point{
		Col: min(max(c.Col+dCol, 0), n-1),
		Row: min(max(c.Row+dRow, 0), n-1),
	}
}

// place puts a peg in the hole under the cursor, on the steps that invite one.
// On any other step the peg key pages forward instead, so it is never a key
// that does nothing.
//
// An illegal hole is answered rather than refused silently. The two mistakes
// newcomers actually make — reaching into the opponent's border line and
// reaching for a corner — are illegal moves, and a player stopped without being
// told why has learned nothing from being stopped.
//
// One peg per step. The engine alternates the mover, and every line of prose
// here addresses the player as Vertical: "Vertical moves first", your top and
// bottom rows, a peg of yours a knight's move away. A second peg therefore
// played for Horizontal while the text still said "you", the refusal named
// Vertical's forbidden columns for a hole in Horizontal's forbidden rows, and
// the caption congratulated the player on a hole that had just been taken by
// somebody else. Allowing several pegs was meant to let a newcomer feel the
// links form; what it actually did was hand them the opposite side without
// saying so. So the invitation is answered once, and after that the key pages
// on, which is what it does on every step that invites nothing.
func (m *onboardingModel) place() tea.Cmd {
	s := m.steps[m.step]
	if m.done || s.invite == "" || m.placed {
		return m.forward()
	}
	picked := m.board.Cursor
	if err := m.g.CanPlace(m.g.Turn(), picked); err != nil {
		m.told = onboardingRefusal(m.g.Turn(), picked, err)
		m.scrollToTold()
		return nil
	}
	if _, err := m.g.PlayPeg(picked); err != nil {
		// CanPlace has just vouched for the move, so this does not happen; say
		// so rather than leaving a board that disagrees with the text.
		m.told = fmt.Sprintf("%s could not be played: %v.", picked, err)
		m.scrollToTold()
		return nil
	}
	m.placed = true
	if s.told != nil {
		m.told = s.told(m.g, picked)
	}
	m.scrollToTold()
	return nil
}

// onboardingRefusal names the rule that stopped a peg, in the terms of the
// three or four rules the introduction has room to teach.
func onboardingRefusal(mover game.Player, p game.Point, err error) string {
	switch {
	case errors.Is(err, game.ErrCornerHole):
		return fmt.Sprintf("%s is one of the four missing corners: a corner would sit in a border line of each player at once, so the board leaves it out and no peg can ever stand there. Try another hole.", p)
	case errors.Is(err, game.ErrOpponentBorder):
		// Named from the mover's own side rather than assumed. The introduction
		// only ever hands the player Vertical, but a refusal that names the wrong
		// pair of edges is worse than one that names none, and this sentence had
		// already been caught telling a player that a hole in the top row was in
		// the left or right column.
		mine, theirs := "top and bottom rows", "left and right columns"
		if mover == game.Horizontal {
			mine, theirs = "left and right columns", "top and bottom rows"
		}
		return fmt.Sprintf("%s is in one of the %s, which are your opponent's border lines. You may use your own %s as much as you like, and never theirs. Try another hole.", p, theirs, mine)
	case errors.Is(err, game.ErrOccupied):
		return fmt.Sprintf("%s already holds a peg, and a hole only ever holds one. Try an empty one.", p)
	default:
		return fmt.Sprintf("%s cannot be used: %v. Try another hole.", p, err)
	}
}

// arrange is the layout for the current frame.
func (m *onboardingModel) arrange() ui.Arrangement {
	return onboardingArrange(m.width, m.height, m.boardSize())
}

// boardSize is the side of the board the introduction draws, which is the
// tutorial ruleset's, not that of any game the player may have been in.
func (m *onboardingModel) boardSize() int {
	if m.g != nil {
		return m.g.Size()
	}
	return learn.Rules().Size
}

// onboardingArrange lays a step out. It is a lesson's layout — board above,
// measured prose below — until the board will not fit whole, and then it is
// prose alone.
//
// That last rule is the difference from the tutorial, and it is about what a
// part of a board is worth. A lesson's learner has chosen to be there and can
// scroll a clipped board to find the holes the prose points at. The
// introduction's reader has not chosen anything yet: a step that says "the
// highlighted top and bottom rows are yours" over a board showing four of its
// twelve rows has told them something they cannot check, and four rows of grid
// is worth less to them than the four lines of prose those rows would have
// held. So the board is drawn when the whole of it fits in the rows it is given
// and dropped when it does not, which at a twelve-hole board means a terminal
// of roughly 28 by 22 and up. Below that the introduction still reads, which is
// the requirement it exists under.
func onboardingArrange(width, height, n int) ui.Arrangement {
	arr := tutorialArrange(width, height, n)
	if arr.TooSmall {
		return arr
	}
	if blockW, blockH := arr.Scale.BlockSize(n); blockW > arr.BoardW || blockH > arr.BoardH {
		return tutorialTextArrange(width, height, n)
	}
	return arr
}

// View implements tea.Model.
func (m *onboardingModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

// frame renders the whole terminal. Every path ends in ui.Compose, which is
// what guarantees no line is wider than the terminal and no more lines than it
// has rows — including the terminal too small for either, which Compose answers
// with its own notice.
func (m *onboardingModel) frame() string {
	arr := m.arrange()
	if arr.TooSmall {
		return ui.Compose(arr, nil, nil, "", &m.styles)
	}
	lines := m.panel(arr).lines
	m.scroll = onboardingClampScroll(m.scroll, len(lines), arr.PanelH)
	more := m.scroll+arr.PanelH < len(lines)
	panel := tutorialWindow(lines, m.scroll, arr.PanelH)

	var board []string
	if arr.BoardH > 0 {
		m.board.Scale = arr.Scale
		m.board.Highlights = m.steps[m.step].highlight
		board = m.board.Render(m.g, &m.styles, arr.BoardAvailW, arr.BoardAvailH)
	}
	return ui.Compose(arr, board, panel, m.statusLine(more), &m.styles)
}

// onboardingPanel is the panel's text together with the offset a caller needs
// to bring the line about the player's move into view.
type onboardingPanel struct {
	lines []string
	// toldAt is the first line of the block about the player's peg, or -1 when
	// there is none.
	toldAt int
}

// panel builds the whole of the panel as one block of wrapped text, paged
// rather than divided into regions: at twenty columns there is room for three
// lines in total, and paging one block is what makes every word reachable at
// every size the program supports.
func (m *onboardingModel) panel(arr ui.Arrangement) onboardingPanel {
	p := onboardingPanel{toldAt: -1}
	w := arr.PanelW
	if w < 1 {
		return p
	}
	add := func(style lipgloss.Style, text string) {
		for _, l := range tutorialWrap(text, w) {
			p.lines = append(p.lines, paint(&m.styles, &style, l))
		}
	}
	blank := func() {
		if len(p.lines) > 0 {
			p.lines = append(p.lines, "")
		}
	}

	if m.done {
		add(m.styles.PanelTitle, "That is the whole of it")
		blank()
		add(m.styles.PanelText, "You know enough to start. Everything else — blocking, building two ways at once, the endgame, the swap — is easier to learn from a game you are losing than from a screen.")
		blank()
		add(m.styles.Highlight, onboardingNote)
		return p
	}

	s := m.steps[m.step]
	add(m.styles.PanelTitle, fmt.Sprintf("A quick introduction — step %d of %d", m.step+1, len(m.steps)))
	blank()
	add(m.styles.PanelText, s.text)
	if s.invite != "" {
		blank()
		add(m.styles.PanelTitle, s.invite)
	}
	if m.told != "" {
		blank()
		p.toldAt = len(p.lines)
		add(m.styles.Highlight, m.told)
	}
	if arr.BoardH == 0 {
		// Every step names the holes it is talking about, so a frame with no
		// board in it still reads; this only accounts for the absence, so that
		// a reader who expected a picture knows the program is behaving rather
		// than failing. It goes last, and not under the heading where a notice
		// would ordinarily go, because in the pane small enough to need it
		// three rows are a third of everything there is and the prose has the
		// better claim on them.
		blank()
		add(m.styles.Message, "The board is not drawn: this terminal has no room for it.")
	}
	return p
}

// scrollToTold pages the panel to what the player's peg earned them, which they
// have just acted to see. Without it a step whose prose already fills the pane
// answers a keypress by changing nothing the player can see, and the peg going
// down on the board looks like the whole of the answer.
func (m *onboardingModel) scrollToTold() {
	arr := m.arrange()
	if arr.TooSmall || arr.PanelH < 1 {
		return
	}
	p := m.panel(arr)
	if p.toldAt < 0 {
		return
	}
	// Where the line runs to the end of the text the panel is pulled back to
	// show the whole tail, so the reader gets the invitation above it rather
	// than a page that starts blank.
	m.scroll = min(p.toldAt, max(0, len(p.lines)-arr.PanelH))
}

// onboardingClampScroll holds a pager offset inside the text so that the panel
// is always full: the furthest down it may go is the offset whose page ends on
// the last line.
//
// The tutorial's own clamp pages in whole panels instead, which means its last
// page can be one line of text above nine blank rows. That is defensible in a
// lesson somebody has chosen to work through and reads, here, as the program
// having stopped: a newcomer three keypresses into an introduction has no
// reason to read a mostly blank frame as a pager reaching the end. Overlapping
// costs them a few lines they read a moment ago, which is much the cheaper of
// the two, and it is why this screen does not share that helper.
func onboardingClampScroll(scroll, total, h int) int {
	if h < 1 || total <= h {
		return 0
	}
	return min(max(scroll, 0), total-h)
}

// statusLine is the always-present bottom row.
//
// The key that leaves is the only hint the line is required to carry, and it
// therefore comes first: tutorialFit guarantees the hints it is given as
// mandatory and drops the rest from the end, but nothing rescues a mandatory
// list that is itself too long for the pane. At twenty columns "space place a
// peg · q skip" does not fit, and what a clipped line loses is its tail — which
// is how the way out came to be the hint that vanished in the narrowest pane
// that has one. So the line leads with it everywhere. Leading with the way out
// rather than with the action reads a shade defensively, and that is the right
// register for a screen whose whole promise is that it can be left.
func (m *onboardingModel) statusLine(more bool) string {
	skip := m.keyLabel(ui.ActQuit) + " skip"
	next := tutorialKeyLabel(tutActNext)
	back := tutorialKeyLabel(tutActPrev)
	// The keymap's own terse verb, so the hint cannot describe the key
	// differently from the way the rest of the program does.
	place := m.keyLabel(ui.ActPlacePeg) + " " + m.keyShort(ui.ActPlacePeg)

	if m.done {
		// The closing panel offers one key and what it does is leave, so a
		// second name for the same act would be noise.
		return m.status(tutorialFit(m.width, []string{next + " done"}, nil))
	}
	must := []string{skip}
	var extra []string
	switch {
	case more && m.steps[m.step].invite != "":
		// The invitation may itself be on a later page in a small pane, and a
		// player who cannot see it has no reason to guess the peg key is live.
		extra = []string{next + " read on", place, back + " back a page"}
	case more:
		extra = []string{next + " read on", back + " back a page"}
	case m.steps[m.step].invite != "":
		extra = []string{place, m.keyLabel(ui.ActMoveLeft) + " move", next + " next", back + " back"}
	default:
		extra = []string{next + " next", back + " back"}
	}
	return m.status(tutorialFit(m.width, must, extra))
}

// status applies the status style, or leaves the text alone when colour is off.
func (m *onboardingModel) status(line string) string {
	style := m.styles.Status
	return paint(&m.styles, &style, line)
}

// keyLabel is the label the shared keymap gives an action, so a hint can never
// name a key the screen does not answer.
func (m *onboardingModel) keyLabel(a ui.Action) string {
	if b, ok := m.keymap.ByAction(ui.CtxBoard, a); ok {
		return b.Label
	}
	return ""
}

// keyShort is the terse verb the shared keymap gives an action, for the status
// line. It falls back to the fuller help text, since a hint with a key and no
// meaning beside it names nothing.
func (m *onboardingModel) keyShort(a ui.Action) string {
	b, ok := m.keymap.ByAction(ui.CtxBoard, a)
	if !ok {
		return ""
	}
	if b.Short != "" {
		return b.Short
	}
	return b.Help
}

// onboardingContent is the introduction itself: what the game is, whose edges
// are whose, a peg, a link, and a link that is refused.
//
// Five steps is a decision and not a starting point. The tutorial has seven
// lessons and forty-odd steps and is the right length for somebody who has
// decided to learn the game; this is read by somebody who has decided to try
// the program, and everything past the fifth screen is read by fewer of them
// than the fourth. What survived the cut: the object of the game, because
// without it the board is a grid of dots; whose borders are whose, because it
// is the one placement rule; the knight's move, because it is what newcomers
// get wrong first; and blocking, because it is why the knight's move was chosen
// and what makes the game a contest. Everything else — setups, the swap, the
// endgame — is in the tutorial, and the closing panel says so.
//
// Every position here is replayed by the package's tests, so a move list that
// does not load is a test failure rather than something a first-run player
// discovers.
func onboardingContent() []onboardingStep {
	return []onboardingStep{
		{
			// A finished game says the object in a picture, which is worth more
			// on the first screen than any sentence about it. Vertical's chain
			// runs the length of the board and Horizontal's, down the left,
			// plainly did not get there.
			id: "goal",
			setup: []string{
				"F1", "A2", "G3", "B4", "F5", "A6", "G7",
				"B8", "F9", "A10", "G11", "C9", "E12",
			},
			highlight: pointsNamed("F1", "E12"),
			text: "TwixT is about joining your own two sides of the board. Each turn you put one peg into a hole; " +
				"two of your own pegs a knight's move apart link up as the peg goes down, and the first unbroken " +
				"chain of linked pegs from one of your own border lines to the other wins the game. " +
				"Vertical has just finished one here: it runs from F1 in the top row down to E12 in the bottom.",
		},
		{
			id:        "board",
			highlight: onboardingBorders(game.Vertical, learn.Rules().Size),
			text: "That is the board: a square grid with its four corners missing, because a corner hole would sit in a border line " +
				"of each player at once. The top row and the bottom row are Vertical's, and Vertical moves first. " +
				"The left and right columns are Horizontal's. There is one rule about where a peg may go and this is it: " +
				"your own border rows are always open to you, and your opponent's never are.",
		},
		{
			id: "peg",
			text: "A turn is one peg into an empty hole, and that is all of it. There is no passing, and a peg never moves again once it is down. " +
				"The letters along the top and the numbers down the side name the holes, so F6 is the sixth column and the sixth row.",
			invite: "Try it: move the cursor and put a peg down.",
			told: func(g *game.Game, played game.Point) string {
				if g.IsBorderRow(game.Vertical, played) {
					return fmt.Sprintf("Down it goes, and worth noticing: %s is in one of Vertical's own border rows, which is where a chain has to begin and end.", played)
				}
				return fmt.Sprintf("Down it goes. %s was as good a hole as any other: on an empty board every hole is open to you except the corners and Horizontal's two columns.", played)
			},
		},
		{
			id:        "links",
			setup:     []string{"F6", "C9"},
			highlight: pointsNamed("D5", "D7", "E4", "E8", "G4", "G8", "H5", "H7"),
			text: "Links are made for you rather than by you. Two of your own pegs are linked when they stand a knight's move apart — " +
				"two holes one way and one the other, as a knight jumps in chess — and the link appears as the second peg lands. " +
				"Exactly eight holes could ever link to Vertical's peg on F6, and they are the ones marked: " +
				"D5, D7, E4, E8, G4, G8, H5 and H7. Holes side by side never link, nor do diagonal neighbours, which is what newcomers get wrong first.",
			invite: "Put a peg on one of those eight and watch the link appear.",
			told:   onboardingLinkReport,
		},
		{
			id:        "blocking",
			setup:     []string{"F6", "F7", "F2", "H8"},
			highlight: pointsNamed("G8"),
			text: "And links block links, which is what makes this a contest rather than a race. Horizontal has linked F7 to H8, straight across " +
				"Vertical's way down the board, and a link that would cross a link already on the board is simply never made. " +
				"Nothing stops the peg going in: G8 is an empty legal hole and a knight's move from F6. It just achieves nothing while that link stands.",
			invite: "Put a peg on G8 and watch no link appear.",
			told:   onboardingLinkReport,
		},
	}
}

// onboardingLinkReport is the line the two link steps give the player, read off
// the board instead of written out beside each of them. They are the same act
// with opposite answers — a peg a knight's move from your own, with and without
// an enemy link across the line — so the honest report is the one the position
// gives, and neither step can end up claiming a link that is not there.
func onboardingLinkReport(g *game.Game, played game.Point) string {
	mask := g.LinkMask(played)
	switch {
	case mask > 0:
		return fmt.Sprintf("There it is: the peg on %s and the link, made for you as it landed.", played)
	case linkableNeighbour(g, played):
		return fmt.Sprintf("The peg is on %s and no link was made. It is a knight's move from one of your own pegs, so the link was there to be had — and the line it would have run along crosses a link already on the board, which is the whole of the rule. The peg has landed and achieved nothing.", played)
	default:
		return fmt.Sprintf("The peg is on %s and no link was made: there is no peg of yours a knight's move from it.", played)
	}
}

// linkableNeighbour reports whether a peg has one of its own a knight's move
// away, which is what tells a link that was blocked from a peg that had nothing
// to link to in the first place.
func linkableNeighbour(g *game.Game, p game.Point) bool {
	owner := g.At(p)
	if owner == game.NoPlayer {
		return false
	}
	for d := game.Dir(0); d < game.NumDirs; d++ {
		if q := p.Add(d); g.Exists(q) && g.At(q) == owner {
			return true
		}
	}
	return false
}

// onboardingBorders returns every hole in a side's two border lines, corners
// aside.
func onboardingBorders(pl game.Player, n int) []game.Point {
	out := make([]game.Point, 0, 2*(n-2))
	for i := 1; i < n-1; i++ {
		switch pl {
		case game.Vertical:
			out = append(out, game.Point{Col: i, Row: 0}, game.Point{Col: i, Row: n - 1})
		case game.Horizontal:
			out = append(out, game.Point{Col: 0, Row: i}, game.Point{Col: n - 1, Row: i})
		}
	}
	return out
}

// pointsNamed parses hole names the way the content writes them. A bad name is
// a programming error in the content above, and it panics rather than being
// returned, so that a step which cannot be built fails at the first call rather
// than as an introduction that half loads.
func pointsNamed(names ...string) []game.Point {
	out := make([]game.Point, 0, len(names))
	for _, n := range names {
		p, err := game.ParsePoint(n)
		if err != nil {
			panic("app: bad hole name " + n + " in the introduction: " + err.Error())
		}
		out = append(out, p)
	}
	return out
}
