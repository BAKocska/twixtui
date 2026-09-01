package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/learn"
	"github.com/BAKocska/twixtui/internal/ui"
)

// The interactive tutorial (R21). The lessons themselves live in
// internal/learn; this file is only their presentation: a chooser, a pager over
// each step's prose, and the board with the step's holes marked.
//
// Two things about the content shape the code. The tutorial's positions come
// from learn.Rules() and learn.Step.Position(), not from whatever ruleset the
// player was last playing under, because the lessons assume that ruleset's
// settings. And the hole the learner picks goes to learn.Task.Accept exactly as
// picked, illegal holes included: the two mistakes beginners actually make,
// reaching into the opponent's border line and reaching for a corner, are
// illegal moves, and Accept is the thing that names the misconception behind
// them. Filtering the pick first would leave the learner stopped without being
// told why, which is the whole point of a lesson.

// tutorialProgressFile is the completion record's name under Deps.ConfigDir. It
// is the tutorial's own small file rather than a corner of anyone else's,
// because it is written on its own and read on its own.
const tutorialProgressFile = "tutorial.json"

// tutorialProgressVersion is stamped into the file so a later format change can
// recognise what it is reading.
const tutorialProgressVersion = 1

// tutorialProgressRecord is the on-disk shape of the completion record.
type tutorialProgressRecord struct {
	Version   int      `json:"version"`
	Completed []string `json:"completed"`
}

// tutorialProgress remembers which lessons the player has finished. A tutorial
// that forgets where you got to is worth little on the second evening.
type tutorialProgress struct {
	dir  string
	done map[string]bool
}

// loadTutorialProgress reads the completion record. A missing file is a first
// run; an unreadable or damaged one is treated the same way, because refusing
// to open the tutorial over a stray file would be a worse failure than asking
// the player to tick a lesson off again. An empty dir disables persistence,
// which is what a caller without a configuration directory wants.
func loadTutorialProgress(dir string) *tutorialProgress {
	p := &tutorialProgress{dir: dir, done: make(map[string]bool)}
	if dir == "" {
		return p
	}
	data, err := os.ReadFile(filepath.Join(dir, tutorialProgressFile))
	if err != nil {
		return p
	}
	var rec tutorialProgressRecord
	if json.Unmarshal(data, &rec) != nil || rec.Version != tutorialProgressVersion {
		return p
	}
	for _, id := range rec.Completed {
		p.done[id] = true
	}
	return p
}

// completed reports whether the player has finished a lesson before.
func (p *tutorialProgress) completed(id string) bool { return p.done[id] }

// mark records a lesson as finished and writes the record out. The in-memory
// flag is set whether or not the write succeeds, so a read-only disk costs the
// player nothing within the session.
func (p *tutorialProgress) mark(id string) error {
	if p.done[id] {
		return nil
	}
	p.done[id] = true
	if p.dir == "" {
		return nil
	}
	ids := make([]string, 0, len(p.done))
	for id := range p.done {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	data, err := json.MarshalIndent(tutorialProgressRecord{
		Version:   tutorialProgressVersion,
		Completed: ids,
	}, "", "  ")
	if err != nil {
		return err
	}
	return tutorialWriteFile(filepath.Join(p.dir, tutorialProgressFile), append(data, '\n'))
}

// tutorialWriteFile replaces path with data or leaves the previous contents
// alone. The bytes go to a temporary file in the same directory, are flushed to
// the device and are then renamed over the target: a rename within a directory
// is atomic, so an interrupted write cannot leave half a record where the next
// run expects a whole one.
func tutorialWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	// Once the rename below has moved the file this removes nothing, which is
	// exactly what is wanted; until then it clears up after a failed write.
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flushing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// tutorialAction is something the tutorial does that a game does not, so
// ui.Keymap carries no binding for it. Everything to do with the board — moving
// over the holes and putting a peg down — comes from the shared keymap instead,
// so the tutorial teaches the keys the game really uses.
type tutorialAction uint8

// The tutorial's own actions: a pager, the two teaching affordances, and a way
// back out.
const (
	tutActNext tutorialAction = iota
	tutActPrev
	tutActRestart
	tutActShow
	tutActHelp
	tutActBack
)

// tutorialBinding is one of the tutorial's own key rows, in the same shape as
// ui.Binding so dispatch, the status hints and the key page can all read it.
type tutorialBinding struct {
	action tutorialAction
	keys   []string
	label  string
	help   string
}

// tutorialBindings is the single source of truth for the tutorial's own keys.
// Every key here is an unmodified printable or a plain special key, so it
// survives a terminal multiplexer, and none of them appears in
// ui.DefaultKeymap: TestTutorialKeysDoNotShadowTheKeymap holds that line.
var tutorialBindings = []tutorialBinding{
	{tutActNext, []string{"n"}, "n", "read on, then the next step"},
	{tutActPrev, []string{"p"}, "p", "back a page, then the previous step"},
	{tutActRestart, []string{"r"}, "r", "put this step back as you found it"},
	{tutActShow, []string{"s"}, "s", "show me the answer"},
	{tutActHelp, []string{"?"}, "?", "this key page"},
	{tutActBack, []string{"esc"}, "esc", "back one level"},
}

// tutorialLookup resolves a key against the tutorial's own bindings.
func tutorialLookup(key string) (tutorialBinding, bool) {
	for _, b := range tutorialBindings {
		if slices.Contains(b.keys, key) {
			return b, true
		}
	}
	return tutorialBinding{}, false
}

// tutorialKeyLabel is the label the tutorial's binding table gives an action.
func tutorialKeyLabel(a tutorialAction) string {
	for _, b := range tutorialBindings {
		if b.action == a {
			return b.label
		}
	}
	return ""
}

// tutorialBoardActions are the shared keymap's actions the tutorial performs.
// Link editing and abandoning a turn are left out because a lesson is answered
// by choosing one hole: listing a key that does nothing would be worse than
// listing none, so the key page is built from this same list.
var tutorialBoardActions = []ui.Action{
	ui.ActMoveLeft, ui.ActMoveRight, ui.ActMoveUp, ui.ActMoveDown,
	ui.ActJumpLeft, ui.ActJumpRight, ui.ActJumpUp, ui.ActJumpDown,
	ui.ActEdgeTop, ui.ActEdgeBottom, ui.ActEdgeLeft, ui.ActEdgeRight,
	ui.ActPlacePeg, ui.ActConfirm, ui.ActQuit,
}

// tutorialActionHelp is what the key page says about the actions whose meaning
// is narrower here than in a game. The keys are still the keymap's; only the
// wording is the tutorial's, because there is no turn to commit in a lesson and
// quitting leaves the tutorial rather than the program.
var tutorialActionHelp = map[ui.Action]string{
	ui.ActPlacePeg: "answer with the hole under the cursor",
	ui.ActConfirm:  "the same as space",
	ui.ActQuit:     "leave the tutorial",
}

// Layout share. The prose is the lesson, so the panel is given a target depth
// before the board is allowed to grow into it: a header, a blank, four lines of
// text, a blank and a prompt. The board keeps a few rows regardless, since a
// step that talks about holes needs some of them on screen.
const (
	tutorialPanelTarget = 8
	tutorialMinBoardH   = 4

	// tutorialMeasure caps how wide prose is set, whatever the terminal. The eye
	// finds the start of the next line from where the last one ended, and past
	// roughly ninety columns that distance is long enough to lose it, so a very
	// wide terminal gets a measured column of text rather than one line for a
	// whole paragraph. The board is a grid rather than prose and keeps the full
	// width.
	tutorialMeasure = 90
)

// tutorialSep separates the hints on the status line, as it does in ui.
const tutorialSep = " · "

// tutorialMode is which of the tutorial's two places the player is in.
type tutorialMode uint8

// The two modes: choosing a lesson, and working through one.
const (
	tutorialChoosing tutorialMode = iota
	tutorialInLesson
)

// tutorialModel is the tutorial screen.
type tutorialModel struct {
	keymap ui.Keymap
	styles ui.Styles

	progress *tutorialProgress
	lessons  []learn.Lesson

	// standalone marks a screen opened on one named lesson rather than through
	// the chooser, which decides where leaving that lesson goes.
	standalone bool

	mode     tutorialMode
	selected int // the chooser's cursor
	listTop  int // the first lesson row on screen

	lesson learn.Lesson
	step   int
	g      *game.Game
	board  ui.BoardView
	// answered remembers the hole played on each step of the current lesson, so
	// paging back to a step restores it instead of asking for the same move
	// twice.
	answered map[int]game.Point

	scroll   int // the first panel line on screen
	feedback string
	solved   bool
	revealed bool
	// done marks the lesson's last step as behind us: the panel congratulates
	// and offers what comes next.
	done bool

	helping    bool
	helpScroll int
	message    string

	width, height int
}

// NewTutorialScreen opens the interactive tutorial. An empty lessonID starts at
// the lesson chooser; a lesson id starts that lesson, and leaving it then
// leaves the screen rather than dropping into a chooser the player never saw.
func NewTutorialScreen(d Deps, lessonID string) (Screen, error) {
	m := &tutorialModel{
		keymap:   d.Keymap,
		lessons:  learn.Lessons(),
		progress: loadTutorialProgress(d.ConfigDir),
	}
	if len(m.keymap) == 0 {
		m.keymap = ui.DefaultKeymap()
	}
	if d.Styles != nil {
		m.styles = *d.Styles
	} else {
		m.styles = ui.PlainStyles()
	}
	if lessonID == "" {
		return m, nil
	}
	l, ok := learn.Find(lessonID)
	if !ok {
		return nil, fmt.Errorf("no tutorial lesson named %q", lessonID)
	}
	m.standalone = true
	if err := m.openLesson(l); err != nil {
		return nil, err
	}
	return m, nil
}

// Init implements tea.Model. The first window size arrives as a message.
func (m *tutorialModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *tutorialModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reflow()
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

// reflow brings the pager offsets back into range after a resize. The same
// prose wraps into fewer lines in a wider pane, so an offset left over from the
// narrow one would otherwise leave the panel blank. The two pagers are clamped
// against their own layouts, since the key page has the terminal to itself and
// a lesson shares it with the board.
func (m *tutorialModel) reflow() {
	if arr := tutorialTextArrange(m.width, m.height, m.boardSize()); arr.TooSmall {
		m.helpScroll = 0
	} else {
		m.helpScroll = tutorialClampScroll(m.helpScroll, len(m.helpLines(arr.PanelW)), arr.PanelH)
	}
	if m.mode != tutorialInLesson {
		return
	}
	if arr := tutorialArrange(m.width, m.height, m.boardSize()); arr.TooSmall {
		m.scroll = 0
	} else {
		m.scroll = tutorialClampScroll(m.scroll, len(m.panel(arr).lines), arr.PanelH)
	}
}

// handleKey dispatches one key. The key page is modal, and the chooser and a
// lesson have little in common, so each has its own dispatch rather than one
// switch that has to ask which mode it is in at every case.
func (m *tutorialModel) handleKey(key string) tea.Cmd {
	m.message = ""
	switch {
	case m.helping:
		return m.helpKey(key)
	case m.mode == tutorialChoosing:
		return m.chooserKey(key)
	default:
		return m.lessonKey(key)
	}
}

// helpKey drives the key page: the pager keys scroll it and anything else
// closes it, which is what a reader expects of a page they opened to look
// something up.
func (m *tutorialModel) helpKey(key string) tea.Cmd {
	arr := m.arrange()
	if b, ok := tutorialLookup(key); ok && arr.PanelH > 0 {
		lines := len(m.helpLines(arr.PanelW))
		switch b.action {
		case tutActNext:
			if m.helpScroll+arr.PanelH < lines {
				m.helpScroll += arr.PanelH
				return nil
			}
		case tutActPrev:
			if m.helpScroll > 0 {
				m.helpScroll = max(0, m.helpScroll-arr.PanelH)
				return nil
			}
		}
	}
	m.helping = false
	m.helpScroll = 0
	return nil
}

// chooserKey drives the lesson list. It moves on the keymap's own movement
// keys, so the list and the board are steered the same way.
func (m *tutorialModel) chooserKey(key string) tea.Cmd {
	if b, ok := tutorialLookup(key); ok {
		switch b.action {
		case tutActHelp:
			m.helping, m.helpScroll = true, 0
		case tutActBack:
			return Back()
		case tutActNext:
			m.moveSelection(1)
		case tutActPrev:
			m.moveSelection(-1)
		}
		return nil
	}
	b, ok := m.keymap.Lookup(ui.CtxBoard, key)
	if !ok {
		return nil
	}
	switch b.Action {
	case ui.ActMoveUp:
		m.moveSelection(-1)
	case ui.ActMoveDown:
		m.moveSelection(1)
	case ui.ActJumpUp:
		m.moveSelection(-ui.JumpStep)
	case ui.ActJumpDown:
		m.moveSelection(ui.JumpStep)
	case ui.ActEdgeTop:
		m.moveSelection(-len(m.lessons))
	case ui.ActEdgeBottom:
		m.moveSelection(len(m.lessons))
	case ui.ActPlacePeg, ui.ActConfirm:
		if err := m.openLesson(m.lessons[m.selected]); err != nil {
			return Fail(err)
		}
	case ui.ActQuit:
		return Back()
	}
	return nil
}

// lessonKey drives a lesson: the tutorial's own pager keys first, then the
// board keys from the shared keymap.
func (m *tutorialModel) lessonKey(key string) tea.Cmd {
	if b, ok := tutorialLookup(key); ok {
		switch b.action {
		case tutActHelp:
			m.helping, m.helpScroll = true, 0
		case tutActBack:
			return m.leaveLesson()
		case tutActNext:
			return m.forward()
		case tutActPrev:
			return m.backward()
		case tutActRestart:
			return m.restart()
		case tutActShow:
			m.showAnswer()
		}
		return nil
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
	case ui.ActEdgeTop:
		m.moveCursorTo(m.board.Cursor.Col, 0)
	case ui.ActEdgeBottom:
		m.moveCursorTo(m.board.Cursor.Col, m.g.Size()-1)
	case ui.ActEdgeLeft:
		m.moveCursorTo(0, m.board.Cursor.Row)
	case ui.ActEdgeRight:
		m.moveCursorTo(m.g.Size()-1, m.board.Cursor.Row)
	case ui.ActPlacePeg, ui.ActConfirm:
		return m.submit()
	case ui.ActQuit:
		return Back()
	}
	return nil
}

// moveSelection shifts the chooser's cursor, clamped to the list.
func (m *tutorialModel) moveSelection(d int) {
	m.selected = min(max(m.selected+d, 0), len(m.lessons)-1)
}

// selectLesson puts the chooser's cursor on a lesson, so leaving one lands on
// where the player was rather than at the top of the list.
func (m *tutorialModel) selectLesson(id string) {
	if i := slices.IndexFunc(m.lessons, func(l learn.Lesson) bool { return l.ID == id }); i >= 0 {
		m.selected = i
	}
}

// moveCursor shifts the board cursor by a delta.
func (m *tutorialModel) moveCursor(dCol, dRow int) {
	c := m.board.Cursor
	m.moveCursorTo(c.Col+dCol, c.Row+dRow)
}

// moveCursorTo puts the cursor on a hole, clamped to the board and nothing
// more.
//
// A game screen makes the cursor step around the four absent corner holes,
// because a peg can never go there and stopping on one is only a nuisance. The
// tutorial deliberately does not: reaching for a corner is one of the two
// mistakes the lessons are written to catch, and learn.Task.Accept answers a
// corner with the reason corners do not exist. A cursor that cannot rest on one
// would make that explanation unreachable and leave the learner to work the
// rule out from an interface that silently refuses to go there.
func (m *tutorialModel) moveCursorTo(col, row int) {
	n := m.g.Size()
	m.board.Cursor = game.Point{
		Col: min(max(col, 0), n-1),
		Row: min(max(row, 0), n-1),
	}
}

// openLesson switches to a lesson at its first step.
func (m *tutorialModel) openLesson(l learn.Lesson) error {
	if len(l.Steps) == 0 {
		return fmt.Errorf("tutorial lesson %q has no steps", l.ID)
	}
	m.lesson = l
	m.mode = tutorialInLesson
	m.done = false
	m.answered = make(map[int]game.Point)
	return m.loadStep(0)
}

// loadStep replays the step's position and clears what the previous step left
// behind. A step the learner has already answered gets their move put back.
func (m *tutorialModel) loadStep(i int) error {
	s := m.lesson.Steps[i]
	g, err := s.Position()
	if err != nil {
		return fmt.Errorf("tutorial lesson %s step %d: %w", m.lesson.ID, i+1, err)
	}
	m.step = i
	m.g = g
	m.scroll = 0
	m.feedback = ""
	m.revealed = false
	m.solved = s.Task == nil
	m.board = ui.BoardView{ShowCursor: true, Cursor: tutorialStartCursor(s, g.Size())}
	if played, seen := m.answered[i]; seen && s.Task != nil {
		// Accept never modifies the position and depends on nothing but it, so
		// replaying the pick recovers the explanation along with the move.
		if accepted, feedback := s.Task.Accept(g, played); accepted {
			if _, err := g.PlayPeg(played); err == nil {
				m.solved = true
				m.feedback = feedback
				m.board.Cursor = played
			}
		}
	}
	return nil
}

// tutorialStartCursor puts the cursor where the step is about, so the first
// frame scrolls the part of the board the prose talks about into view. No task
// step carries highlights, so a task never opens with its answer under the
// cursor.
func tutorialStartCursor(s learn.Step, n int) game.Point {
	if len(s.Highlight) > 0 {
		return s.Highlight[0]
	}
	return game.Point{Col: n / 2, Row: n / 2}
}

// submit hands the hole under the cursor to the step's checker, exactly as the
// learner picked it. An illegal hole goes through unfiltered on purpose: Accept
// diagnoses it and names the misconception, and the move is played onto the
// board only once Accept has accepted it.
func (m *tutorialModel) submit() tea.Cmd {
	s := m.lesson.Steps[m.step]
	if s.Task == nil || m.solved {
		return m.forward()
	}
	picked := m.board.Cursor
	accepted, feedback := s.Task.Accept(m.g, picked)
	m.feedback = feedback
	m.solved = accepted
	if accepted {
		if _, err := m.g.PlayPeg(picked); err != nil {
			// Accept vouched for the move, so this does not happen; say so
			// rather than leaving a board that disagrees with the text.
			m.message = fmt.Sprintf("%s could not be played: %v", picked, err)
			m.solved = false
		} else {
			m.answered[m.step] = picked
		}
	}
	m.scrollToFeedback()
	return nil
}

// scrollToFeedback pages the panel to the explanation, which is the part of it
// the learner has just earned the right to read. When the explanation runs to
// the end of the text the panel is pulled back to show that whole tail, so the
// reader gets the prompt above it rather than a page that starts blank.
func (m *tutorialModel) scrollToFeedback() {
	arr := m.arrange()
	if arr.PanelH < 1 {
		return
	}
	p := m.panel(arr)
	if p.feedbackAt < 0 {
		return
	}
	m.scroll = min(p.feedbackAt, max(0, len(p.lines)-arr.PanelH))
}

// forward is the pager's next: another page of the step while there is one,
// then the next step, then the end of the lesson.
func (m *tutorialModel) forward() tea.Cmd {
	arr := m.arrange()
	if arr.PanelH > 0 && m.scroll+arr.PanelH < len(m.panel(arr).lines) {
		m.scroll += arr.PanelH
		return nil
	}
	if m.done {
		return m.nextLesson()
	}
	if !m.solved {
		m.message = fmt.Sprintf("answer the task to go on, or %s shows the answer", tutorialKeyLabel(tutActShow))
		return nil
	}
	if m.step+1 < len(m.lesson.Steps) {
		if err := m.loadStep(m.step + 1); err != nil {
			return Fail(err)
		}
		return nil
	}
	return m.finish()
}

// backward is the pager's previous: back a page, then to the previous step.
func (m *tutorialModel) backward() tea.Cmd {
	arr := m.arrange()
	if m.scroll > 0 && arr.PanelH > 0 {
		m.scroll = max(0, m.scroll-arr.PanelH)
		return nil
	}
	if m.done {
		m.done = false
		return nil
	}
	if m.step == 0 {
		m.message = "this is the first step of the lesson"
		return nil
	}
	if err := m.loadStep(m.step - 1); err != nil {
		return Fail(err)
	}
	return nil
}

// restart puts the step back as the learner found it, which is the only way out
// of a position their own answer has changed.
func (m *tutorialModel) restart() tea.Cmd {
	delete(m.answered, m.step)
	if err := m.loadStep(m.step); err != nil {
		return Fail(err)
	}
	m.message = "step restarted"
	return nil
}

// showAnswer marks the model answer on the board. It is not played: the learner
// still puts the peg in, because a tutorial that plays the move for them
// teaches neither the move nor the keys.
func (m *tutorialModel) showAnswer() {
	s := m.lesson.Steps[m.step]
	switch {
	case s.Task == nil:
		m.message = "there is nothing to answer on this step"
	case m.solved:
		m.message = "this step is already answered"
	default:
		m.revealed = true
		m.message = fmt.Sprintf("the answer is %s, marked on the board", s.Task.Answer)
	}
}

// finish ends the lesson and records it as done, so a later run knows where the
// player got to.
func (m *tutorialModel) finish() tea.Cmd {
	m.done = true
	m.scroll = 0
	m.feedback = ""
	if err := m.progress.mark(m.lesson.ID); err != nil {
		m.message = fmt.Sprintf("could not record progress: %v", err)
	}
	return nil
}

// nextLesson starts what is taught after this lesson, or leaves when this was
// the last one.
func (m *tutorialModel) nextLesson() tea.Cmd {
	if l, ok := m.following(); ok {
		if err := m.openLesson(l); err != nil {
			return Fail(err)
		}
		return nil
	}
	return m.leaveLesson()
}

// following returns the lesson taught after the current one.
func (m *tutorialModel) following() (learn.Lesson, bool) {
	i := slices.IndexFunc(m.lessons, func(l learn.Lesson) bool { return l.ID == m.lesson.ID })
	if i < 0 || i+1 >= len(m.lessons) {
		return learn.Lesson{}, false
	}
	return m.lessons[i+1], true
}

// leaveLesson goes back one level: to the chooser when the player came through
// it, and out of the screen when the tutorial was opened on this lesson alone.
func (m *tutorialModel) leaveLesson() tea.Cmd {
	if m.standalone {
		return Back()
	}
	m.selectLesson(m.lesson.ID)
	m.mode = tutorialChoosing
	m.done = false
	return nil
}

// boardSize is the side of the board the tutorial draws, which is the tutorial
// ruleset's and not that of any game the player may have been in.
func (m *tutorialModel) boardSize() int {
	if m.g != nil {
		return m.g.Size()
	}
	return learn.Rules().Size
}

// arrange is the layout for the current frame.
func (m *tutorialModel) arrange() ui.Arrangement {
	if m.helping || m.mode == tutorialChoosing {
		return tutorialTextArrange(m.width, m.height, m.boardSize())
	}
	return tutorialArrange(m.width, m.height, m.boardSize())
}

// tutorialArrange lays a lesson out: the board at the top, the prose panel
// below it, and the panel set to a measure rather than to the terminal.
//
// ui.Arrange decides the too-small case. What the tutorial changes is where the
// panel goes, how the remaining rows are split, and — because of that split —
// which scale the board is drawn at. The panel goes below rather than beside,
// because Arrange's preference for a narrow side panel is right for a game
// screen showing a turn line and wrong for a screen whose content is several
// sentences: the same paragraph needs three times as many lines at 36 columns
// as it does across a wide pane, so at every terminal size a panel below the
// board holds more of it. Its width stops at tutorialMeasure, since beyond that
// a wider pane costs the reader more in finding the next line than it saves in
// lines.
//
// The scale is then chosen against the rows the board is actually left with,
// not against the terminal. Taking the panel's rows first and keeping the scale
// Arrange picked for the whole screen is how a lesson ends up pointing at holes
// that are behind the viewport arrow: at 100x30 the detail board is 24 rows and
// the screen has 29, so Arrange says detail, but the prose takes eight of them
// and the last three rows of the board — two missing corners and the whole
// bottom group of highlighted holes — go off the bottom, while the compact
// board is 13 rows and would have fitted whole with room to spare. The trade is
// a coarser drawing, which costs a lesson little, against holes that are not on
// screen at all, which costs it the lesson.
func tutorialArrange(width, height, n int) ui.Arrangement {
	arr := ui.Arrange(width, height, n)
	if arr.TooSmall {
		return arr
	}
	avail := height - 1 // ui.Arrange keeps the bottom row for the status line
	budget := max(tutorialMinBoardH, avail-tutorialPanelTarget)
	arr.Scale = ui.ScaleFor(width, budget, n)

	blockW, blockH := arr.Scale.BlockSize(n)
	boardH := min(blockH, budget)
	arr.Panel = ui.PanelBottom
	arr.BoardW = min(blockW, width)
	arr.BoardH = boardH
	arr.PanelW = min(width, tutorialMeasure)
	arr.PanelH = avail - boardH
	arr.BoardAvailW = width
	arr.BoardAvailH = boardH
	return arr
}

// tutorialTextArrange is the layout for the tutorial's text-only frames, the
// chooser and the key page: there is no board on either, so everything above
// the status line is panel. It still goes through ui.Arrange for the too-small
// decision and through ui.Compose for the clipping, which is where those rules
// belong.
func tutorialTextArrange(width, height, n int) ui.Arrangement {
	arr := ui.Arrange(width, height, n)
	if arr.TooSmall {
		return arr
	}
	arr.Panel = ui.PanelBottom
	arr.BoardW, arr.BoardH = 0, 0
	arr.BoardAvailW, arr.BoardAvailH = 0, 0
	arr.PanelW, arr.PanelH = min(width, tutorialMeasure), height-1
	return arr
}

// View implements tea.Model.
func (m *tutorialModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

// frame renders the whole terminal. Every path ends in ui.Compose, which is
// what guarantees that no line is wider than the terminal and that there are
// never more lines than it has rows.
func (m *tutorialModel) frame() string {
	arr := m.arrange()
	if arr.TooSmall {
		return ui.Compose(arr, nil, nil, "", &m.styles)
	}
	switch {
	case m.helping:
		lines := m.helpLines(arr.PanelW)
		m.helpScroll = tutorialClampScroll(m.helpScroll, len(lines), arr.PanelH)
		more := m.helpScroll+arr.PanelH < len(lines)
		panel := tutorialWindow(lines, m.helpScroll, arr.PanelH)
		return ui.Compose(arr, nil, panel, m.statusLine(more), &m.styles)
	case m.mode == tutorialChoosing:
		return ui.Compose(arr, nil, m.chooserLines(arr), m.statusLine(false), &m.styles)
	default:
		p := m.panel(arr)
		m.scroll = tutorialClampScroll(m.scroll, len(p.lines), arr.PanelH)
		more := m.scroll+arr.PanelH < len(p.lines)
		m.board.Scale = arr.Scale
		m.board.Highlights = m.highlights()
		board := m.board.Render(m.g, &m.styles, arr.BoardAvailW, arr.BoardAvailH)
		panel := tutorialWindow(p.lines, m.scroll, arr.PanelH)
		return ui.Compose(arr, board, panel, m.statusLine(more), &m.styles)
	}
}

// highlights are the holes the board marks: the ones the step's prose calls
// out, plus the model answer once the learner has asked for it.
func (m *tutorialModel) highlights() []game.Point {
	s := m.lesson.Steps[m.step]
	if !m.revealed || s.Task == nil || m.solved {
		return s.Highlight
	}
	// learn's content must not be modified, so the answer goes onto a copy.
	return append(slices.Clone(s.Highlight), s.Task.Answer)
}

// tutorialPanel is the panel's text together with the offset a caller needs to
// bring the feedback into view.
type tutorialPanel struct {
	lines []string
	// feedbackAt is the first line of the feedback block, or -1 when there is
	// no feedback to show.
	feedbackAt int
}

// panel builds the whole of the lesson panel as one block of text.
//
// It is one block, paged, rather than fixed regions with the prose scrolling
// inside one of them: at twenty columns there is room for three lines in total,
// and paging one block guarantees that every word is reachable at every
// supported size instead of being clipped away. The step's text carries no line
// breaks of its own, so it is wrapped here, at the width the panel turns out to
// have.
func (m *tutorialModel) panel(arr ui.Arrangement) tutorialPanel {
	p := tutorialPanel{feedbackAt: -1}
	w := arr.PanelW
	if w < 1 {
		return p
	}
	add := func(style lipgloss.Style, text string) {
		for _, l := range tutorialWrap(text, w) {
			p.lines = append(p.lines, m.render(style, l))
		}
	}
	blank := func() {
		if len(p.lines) > 0 {
			p.lines = append(p.lines, "")
		}
	}

	if m.done {
		add(m.styles.PanelTitle, m.lesson.Title+" — finished")
		blank()
		add(m.styles.Highlight, fmt.Sprintf("That is all %s of this lesson done.",
			tutorialCount(len(m.lesson.Steps), "step")))
		blank()
		if next, ok := m.following(); ok {
			add(m.styles.PanelTitle, "Next: "+next.Title)
			add(m.styles.PanelText, next.Summary)
		} else {
			add(m.styles.PanelText, "That was the last lesson in the tutorial.")
		}
		return p
	}

	s := m.lesson.Steps[m.step]
	add(m.styles.PanelTitle, fmt.Sprintf("%s — step %d of %d", m.lesson.Title, m.step+1, len(m.lesson.Steps)))
	blank()
	add(m.styles.PanelText, s.Text)
	if s.Task != nil {
		blank()
		add(m.styles.PanelTitle, "Task: "+s.Task.Prompt)
		if m.revealed && !m.solved {
			add(m.styles.Highlight, fmt.Sprintf("The answer is %s, marked on the board.", s.Task.Answer))
		}
	}
	if m.feedback != "" {
		blank()
		p.feedbackAt = len(p.lines)
		verdict, style := "Not yet.", m.styles.Message
		if m.solved {
			verdict, style = "Correct.", m.styles.Highlight
		}
		add(style, verdict)
		add(m.styles.PanelText, m.feedback)
	}
	return p
}

// chooserLines builds the lesson list: every lesson with its summary under it
// when they all fit, and titles alone when they do not, with the selected
// lesson's summary below the list if there is room for it. At twenty columns
// there is not, and seven titles at a glance beat two titles with their prose.
func (m *tutorialModel) chooserLines(arr ui.Arrangement) []string {
	w, h := arr.PanelW, arr.PanelH
	if w < 1 || h < 1 {
		return nil
	}
	out := make([]string, 0, h)
	if h >= 4 {
		out = append(out, m.render(m.styles.PanelTitle, "the TwixT tutorial — choose a lesson"))
	}
	avail := max(1, h-len(out))

	rows, at := m.chooserRows(w, true)
	summarised := len(rows) <= avail
	if !summarised {
		rows, at = m.chooserRows(w, false)
	}
	listH := min(len(rows), avail)
	// Keep the whole of the selected lesson's rows in view where they fit, and
	// its first row when they do not.
	m.listTop = tutorialListTop(m.listTop, at[m.selected+1]-1, listH, len(rows))
	m.listTop = tutorialListTop(m.listTop, at[m.selected], listH, len(rows))
	out = append(out, tutorialWindow(rows, m.listTop, listH)...)

	if !summarised && h-len(out) >= 2 {
		l := m.lessons[m.selected]
		out = append(out, "")
		for _, line := range tutorialWrap(l.Title+" — "+l.Summary, w) {
			if len(out) >= h {
				break
			}
			out = append(out, m.render(m.styles.PanelText, line))
		}
	}
	return out
}

// chooserRows renders the lessons as list rows, with each summary under its
// title when withSummary is set. The second result holds the offset each
// lesson's rows begin at, with a final entry for the end of the list, so the
// caller can scroll a whole entry into view.
//
// A title stays on one line, cut rather than reflowed, because a list whose
// rows change height as titles wrap is hard to steer.
func (m *tutorialModel) chooserRows(width int, withSummary bool) (rows []string, at []int) {
	at = make([]int, 0, len(m.lessons)+1)
	for i, l := range m.lessons {
		at = append(at, len(rows))
		marker, style := "  ", m.styles.PanelText
		if i == m.selected {
			marker, style = "> ", m.styles.Highlight
		}
		text := marker + l.Title
		if m.progress.completed(l.ID) {
			text += " (done)"
			if i != m.selected {
				style = m.styles.Label
			}
		}
		rows = append(rows, m.render(style, ansi.Truncate(text, max(1, width), "")))
		if !withSummary {
			continue
		}
		for _, line := range tutorialWrap(l.Summary, max(1, width-4)) {
			rows = append(rows, "    "+m.render(m.styles.Label, line))
		}
	}
	at = append(at, len(rows))
	return rows, at
}

// helpLines is the key page: the shared keymap's rows for the actions the
// tutorial performs, then the tutorial's own keys.
func (m *tutorialModel) helpLines(width int) []string {
	if width < 1 {
		return nil
	}
	out := []string{m.render(m.styles.PanelTitle, "keys")}
	for _, b := range m.keymap {
		if b.Contexts&ui.CtxBoard == 0 || !slices.Contains(tutorialBoardActions, b.Action) {
			continue
		}
		help := b.Help
		if override, ok := tutorialActionHelp[b.Action]; ok {
			help = override
		}
		out = append(out, m.helpRow(b.Label, help, width)...)
	}
	for _, b := range tutorialBindings {
		out = append(out, m.helpRow(b.label, b.help, width)...)
	}
	return out
}

// helpRow renders one key row, wrapping the description under a hanging indent
// so a narrow pane keeps a key and its meaning together.
func (m *tutorialModel) helpRow(label, help string, width int) []string {
	const keyW = 7
	if width < keyW+8 {
		out := []string{m.render(m.styles.Label, label)}
		for _, l := range tutorialWrap(help, max(1, width-2)) {
			out = append(out, "  "+m.render(m.styles.PanelText, l))
		}
		return out
	}
	wrapped := tutorialWrap(help, width-keyW)
	out := make([]string, 0, len(wrapped))
	for i, l := range wrapped {
		head := strings.Repeat(" ", keyW)
		if i == 0 {
			head = tutorialPadKey(label, keyW)
		}
		out = append(out, m.render(m.styles.Label, head)+m.render(m.styles.PanelText, l))
	}
	return out
}

// statusLine is the always-present bottom row: the last thing that happened, or
// what the keys do here. Key labels come from the shared keymap and from the
// tutorial's own binding table, so the line cannot drift from what the keys
// really are.
func (m *tutorialModel) statusLine(more bool) string {
	if m.message != "" {
		return m.render(m.styles.Message, m.message)
	}
	leave := m.keyLabel(ui.ActQuit) + " leave"
	var must, extra []string
	switch {
	case m.helping:
		must = []string{tutorialKeyLabel(tutActNext) + " read on", "any other key closes"}
	case m.mode == tutorialChoosing:
		must = []string{m.keyLabel(ui.ActConfirm) + " open", leave}
		extra = []string{
			m.keyLabel(ui.ActMoveDown) + " move",
			tutorialKeyLabel(tutActHelp) + " keys",
		}
	case m.done:
		verb, out := " next lesson", leave
		if _, ok := m.following(); !ok {
			verb = " back to the list"
			if m.standalone {
				verb, out = " leave", ""
			}
		}
		must = []string{tutorialKeyLabel(tutActNext) + verb}
		if out != "" {
			must = append(must, out)
		}
		extra = []string{tutorialKeyLabel(tutActPrev) + " back a step"}
	case more:
		must = []string{tutorialKeyLabel(tutActNext) + " read on", leave}
		extra = []string{tutorialKeyLabel(tutActPrev) + " back a page"}
	case m.lesson.Steps[m.step].Task != nil && !m.solved:
		must = []string{m.keyLabel(ui.ActPlacePeg) + " answer here", leave}
		extra = []string{
			m.keyLabel(ui.ActMoveLeft) + " move",
			tutorialKeyLabel(tutActShow) + " show me",
			tutorialKeyLabel(tutActHelp) + " keys",
		}
	default:
		must = []string{tutorialKeyLabel(tutActNext) + " next", leave}
		extra = []string{
			tutorialKeyLabel(tutActPrev) + " back",
			tutorialKeyLabel(tutActRestart) + " restart",
			tutorialKeyLabel(tutActHelp) + " keys",
		}
	}
	return m.render(m.styles.Status, tutorialFit(m.width, must, extra))
}

// keyLabel is the label the shared keymap gives an action, so a hint always
// names the key the keymap really binds.
func (m *tutorialModel) keyLabel(a ui.Action) string {
	if b, ok := m.keymap.ByAction(ui.CtxBoard, a); ok {
		return b.Label
	}
	return ""
}

// render applies a style, or leaves the text alone when colour is off.
func (m *tutorialModel) render(style lipgloss.Style, text string) string {
	if m.styles.Plain {
		return text
	}
	return style.Render(text)
}

// tutorialFit assembles a hint line by importance: the hints in must are always
// there, and the rest are added while the width lasts, so a narrow pane loses
// the least useful hint rather than the most useful one.
func tutorialFit(width int, must, extra []string) string {
	line := strings.Join(must, tutorialSep)
	for _, e := range extra {
		next := line + tutorialSep + e
		if ansi.StringWidth(next) > width {
			break
		}
		line = next
	}
	return line
}

// tutorialWrap breaks text into lines no wider than width. Words are kept whole
// unless a single word is wider than the pane, which is the only case where
// breaking one beats overflowing.
func tutorialWrap(text string, width int) []string {
	if width < 1 || strings.TrimSpace(text) == "" {
		return nil
	}
	return wrapText(text, width)
}

// tutorialWindow returns the h lines of text starting at top.
func tutorialWindow(lines []string, top, h int) []string {
	if h < 1 || len(lines) == 0 {
		return nil
	}
	top = min(max(top, 0), len(lines))
	return lines[top:min(top+h, len(lines))]
}

// tutorialClampScroll holds a pager offset inside the text, no further down
// than the last page a whole page of text starts on. Text that fits in one page
// has no offset at all, which is what makes a resize that widens the pane put
// the reader back at the top rather than in front of a blank panel.
func tutorialClampScroll(scroll, total, h int) int {
	if h < 1 || total <= h {
		return 0
	}
	last := ((total - 1) / h) * h
	return min(max(scroll, 0), last)
}

// tutorialListTop scrolls a list window as little as it takes to show sel.
func tutorialListTop(top, sel, h, n int) int {
	if h >= n {
		return 0
	}
	top = min(max(top, 0), n-h)
	switch {
	case sel < top:
		return sel
	case sel >= top+h:
		return sel - h + 1
	}
	return top
}

// tutorialPadKey right-pads a key label to width, measuring the label as it
// displays: the keymap's labels carry arrow glyphs.
func tutorialPadKey(label string, width int) string {
	if pad := width - ansi.StringWidth(label); pad > 0 {
		return label + strings.Repeat(" ", pad)
	}
	return label + " "
}

// tutorialCount renders a count and its noun for the closing line of a lesson.
func tutorialCount(n int, noun string) string {
	if n == 1 {
		return "one " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
