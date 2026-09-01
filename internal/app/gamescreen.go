package app

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Peg glyphs for the panel. They repeat what the board renderer draws, so the
// panel names a side the same way the board shows it; the renderer keeps its
// own copy because it paints a cell grid rather than text.
const (
	gsPegVertical   = "●"
	gsPegHorizontal = "○"
)

// gsSpinnerInterval is how often the thinking indicator advances. It is short
// enough to read as motion and long enough that a slow terminal is not redrawn
// for nothing.
const gsSpinnerInterval = 120 * time.Millisecond

var gsSpinnerFrames = [...]string{"|", "/", "-", "\\"}

// gameAction is a key action of the game itself rather than of the board. The
// board actions — movement, placing, committing, link editing, aborting,
// quitting — come from ui.Keymap and are not restated here.
type gameAction uint8

const (
	gaNone gameAction = iota
	gaHint
	gaSwap
	gaDraw
	gaResign
	gaLiftPeg
	gaCode
	gaYes
	gaNo
)

// gamePhase is when a game key applies. A pending confirmation takes the
// keyboard over, so the two phases are disjoint.
type gamePhase uint8

const (
	phasePlay gamePhase = 1 << iota
	phaseConfirm
)

// gameBinding is one game key. Dispatch, the panel's help rows and the status
// hints all read this table, so a key can never be documented as something it
// does not do.
type gameBinding struct {
	action gameAction
	keys   []string
	phases gamePhase
	label  string
	help   string
}

// gameBindings are the keys the game adds to the board keymap. None of them may
// collide with ui.DefaultKeymap; TestGameKeysDoNotShadowTheBoardKeymap holds
// that line.
var gameBindings = []gameBinding{
	{gaHint, []string{"?"}, phasePlay, "?", "hint: what to play and why"},
	{gaSwap, []string{"s"}, phasePlay, "s", "take the swap option"},
	{gaDraw, []string{"d"}, phasePlay, "d", "offer or accept a draw"},
	{gaResign, []string{"r"}, phasePlay, "r", "resign"},
	{gaLiftPeg, []string{"p"}, phasePlay, "p", "lift one of your pegs"},
	{gaCode, []string{"c"}, phasePlay, "c", "the exchange: your code and theirs"},
	{gaYes, []string{"y"}, phaseConfirm, "y", "yes"},
	{gaNo, []string{"n", "esc"}, phaseConfirm, "n", "no, carry on"},
}

func gameKeyLookup(phase gamePhase, key string) (gameBinding, bool) {
	for _, b := range gameBindings {
		if b.phases&phase == 0 {
			continue
		}
		for _, k := range b.keys {
			if k == key {
				return b, true
			}
		}
	}
	return gameBinding{}, false
}

// botMoveMsg is a move the opponent engine chose. gen names the search it
// answers so a move from a search the screen has moved past is dropped.
type botMoveMsg struct {
	gen  int
	move game.Point
	err  error
}

// botTickMsg advances the thinking indicator.
type botTickMsg struct{}

// netEventMsg is one thing the remote opponent or the connection did.
type netEventMsg struct{ ev netplay.Event }

// netClosedMsg means the session's event stream ended.
type netClosedMsg struct{}

// gameScreen is the screen a game is played on: it owns the position, decides
// what the keys mean given who is sitting at this keyboard, and draws through
// internal/ui.
type gameScreen struct {
	deps   Deps
	cfg    GameConfig
	keymap ui.Keymap
	styles *ui.Styles

	g     *game.Game
	board ui.BoardView

	width, height int

	linkMode bool
	// message is transient: it answers the last keypress and is cleared by the
	// next one. notice is sticky: the game's outcome, a dropped connection, a
	// divergence — things that stay true until the player leaves.
	message string
	notice  string
	// stopped means no further play is possible on this screen: the game ended,
	// the connection dropped, or the two ends disagree about the position.
	stopped bool

	// confirm is the action waiting for a yes or no, gaNone when none is.
	confirm gameAction
	// handover gates a hotseat game between turns so that the player who has
	// just moved cannot keep playing for their opponent by carrying on typing.
	handover bool

	started  time.Time
	created  time.Time
	storeID  string
	recorded bool
	leaving  bool
	// departNote is the line depart left for the player: which game was saved
	// and how to pick it up again. The screen does not deliver it itself,
	// because where it belongs depends on what the player is left looking at
	// afterwards, and only the shell knows that.
	departNote string
	// returns records whether leaving this screen goes back to the screen it
	// was opened over. The two quit keys then part company — the plain letter
	// comes back, the control form ends the program — and the help has to say
	// which is which.
	returns bool
	// savedAt is the number of recorded entries the stored copy holds, so an
	// autosave writes only when the position has actually moved on.
	savedAt    int
	saveFailed bool

	botGen      int
	botThinking bool
	botCancel   context.CancelFunc
	spinner     int

	session netplay.Session
	// remoteSide is the side the networked opponent plays, NoPlayer in a local
	// game.
	remoteSide game.Player
	netNote    string
	// corr is the code exchange of a correspondence game, nil in every other
	// kind, and the one thing that drives a remote seat with no session behind
	// it.
	corr *correspondence

	hint hintPanel
}

// NewGameScreen builds the screen for one game. Both sides must have a seat and
// at least one of them must be played at this keyboard. A remote seat is driven
// either by a live session or, in a correspondence game, by the move codes the
// players exchange by hand; it needs exactly one of the two.
func NewGameScreen(d Deps, cfg GameConfig) (Screen, error) {
	humans, remote := 0, game.NoPlayer
	for _, side := range []game.Player{game.Vertical, game.Horizontal} {
		seat, ok := cfg.Seats[side]
		if !ok {
			return nil, fmt.Errorf("game: no seat for %s", side)
		}
		if seat.Human() {
			humans++
		}
		if seat.Remote {
			remote = side
		}
	}
	if humans == 0 {
		return nil, errors.New("game: neither side is played at this keyboard")
	}
	switch {
	case cfg.Codes && cfg.Session != nil:
		return nil, errors.New("game: a game is played either over a session or by move codes, not both")
	case cfg.Codes && remote == game.NoPlayer:
		return nil, errors.New("game: move codes drive a remote seat, and neither seat is remote")
	case remote != game.NoPlayer && cfg.Session == nil && !cfg.Codes:
		return nil, errors.New("game: a remote seat needs a session")
	case remote == game.NoPlayer && cfg.Session != nil:
		return nil, errors.New("game: a session was given but neither seat is remote")
	case cfg.Session != nil && cfg.Session.Side() != remote.Opponent():
		return nil, fmt.Errorf("game: the session plays %s, so the remote seat cannot be %s",
			cfg.Session.Side(), remote)
	case cfg.Codes && cfg.StoreID == "" && cfg.Resume == nil:
		// The identifier is what both ends bind their codes to. Allocating one
		// here would produce a game whose codes the opponent can only refuse.
		return nil, errors.New("game: a correspondence game needs the identifier its codes are bound to")
	}

	now := d.Clock()
	created, storeID := now, cfg.StoreID
	var g *game.Game
	var err error
	if cfg.Resume != nil {
		if g, err = cfg.Resume.Game(); err != nil {
			return nil, fmt.Errorf("game: resuming %s: %w", cfg.Resume.ID, err)
		}
		created = cfg.Resume.Created
		if storeID == "" {
			storeID = cfg.Resume.ID
		}
	} else if g, err = game.New(cfg.Rules); err != nil {
		return nil, err
	}
	if storeID == "" {
		storeID = gamestore.NewID()
	}

	// A bot.Bot carries the working state of its search and is not safe for
	// concurrent use, and a game may legitimately consult the opponent engine
	// for hints as well as for its moves. Every engine in this game therefore
	// goes through the same serialiser, so a hint search and a move search take
	// turns instead of corrupting each other. The wait is short because the
	// hint's context is cancelled as soon as the position changes.
	lock := new(sync.Mutex)
	seats := make(map[game.Player]Seat, len(cfg.Seats))
	for side, seat := range cfg.Seats {
		if seat.Bot != nil {
			seat.Bot = &serialBot{lock: lock, engine: seat.Bot}
		}
		seats[side] = seat
	}
	cfg.Seats = seats
	if cfg.HintFor != nil {
		cfg.HintFor = &serialBot{lock: lock, engine: cfg.HintFor}
	}

	styles := d.Styles
	if styles == nil {
		set := ui.DefaultStyles()
		if os.Getenv("NO_COLOR") != "" {
			set = ui.PlainStyles()
		}
		styles = &set
	}
	keymap := d.Keymap
	if len(keymap) == 0 {
		keymap = ui.DefaultKeymap()
	}

	n := g.Size()
	s := &gameScreen{
		deps:       d,
		cfg:        cfg,
		keymap:     keymap,
		styles:     styles,
		g:          g,
		board:      ui.BoardView{Scale: ui.Compact, Cursor: game.Point{Col: n / 2, Row: n / 2}, ShowCursor: true},
		started:    now,
		created:    created,
		storeID:    storeID,
		session:    cfg.Session,
		remoteSide: remote,
	}
	if cfg.Codes {
		s.corr = newCorrespondence(storeID, remote)
		if err := s.corr.resend(g); err != nil {
			s.corr.note = "the code for your last move could not be rebuilt: " + err.Error()
		}
	}
	if res := g.Result(); res.Over() {
		// A finished game was recorded when it finished. Opening it again must
		// not add a second leaderboard row for the same game.
		s.recorded = true
		s.stopped = true
		s.notice = s.resultText(res)
	}
	return s, nil
}

// Init implements tea.Model. The first window size arrives as a message, so
// there is nothing to size here; what does start is the network event loop and
// the opponent engine when it has the move.
func (s *gameScreen) Init() tea.Cmd {
	var cmds []tea.Cmd
	if s.session != nil {
		cmds = append(cmds, s.watchSession())
	}
	if c := s.maybeBotMove(); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (s *gameScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := s.update(msg)
	s.autosave()
	return model, cmd
}

// autosave stores the game whenever the recorded position has moved on.
//
// Saving used to happen only when the player left the screen or the game ended,
// so a game in progress existed nowhere but in memory: closing the terminal
// window or killing the process lost it, which the documented promise that games
// are saved as they are played did not survive. Writing after every change keeps
// that promise for every way a position can change — a move played here, a bot's
// reply, a move arriving over the network, a code pasted into a correspondence
// game — without each of those having to remember to.
//
// A finished game is stored by finish, which also rates it, so this leaves those
// alone rather than racing it.
func (s *gameScreen) autosave() {
	if s.deps.Games == nil || s.leaving || s.g.Result().Over() {
		return
	}
	at := s.g.Entries()
	if at == s.savedAt {
		return
	}
	if err := s.save(false); err != nil {
		// Worth saying once: a player who has been told the game is kept should
		// hear that it is not. Repeating it every move would bury the game.
		if !s.saveFailed {
			s.saveFailed = true
			s.message = "this game is not being saved: " + err.Error()
		}
		return
	}
	s.savedAt = at
	s.saveFailed = false
}

func (s *gameScreen) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return s, s.handleKey(msg)
	case tea.PasteMsg:
		// A paste on a correspondence game can only be a code, so it opens the
		// exchange rather than being dropped for want of a focused field. A
		// finished game has nothing to apply, but it may still have a last code
		// to send, so the exchange opens without taking the text.
		if s.corr != nil {
			s.corr.open = true
			if !s.g.Result().Over() {
				s.corr.paste(msg.Content)
			}
		}
	case botMoveMsg:
		return s, s.applyBotMove(msg)
	case botTickMsg:
		if s.botThinking && !s.leaving {
			s.spinner++
			return s, s.spinnerTick()
		}
	case hintMsg:
		s.hint.apply(msg)
	case netEventMsg:
		return s, s.applyNetEvent(msg.ev)
	case netClosedMsg:
		if !s.leaving && !s.stopped && !s.g.Result().Over() {
			s.stop("the connection to the opponent ended — the game is saved and can be resumed")
		}
	}
	return s, nil
}

// View implements tea.Model.
func (s *gameScreen) View() tea.View {
	s.board.LastMove, s.board.ShowLastMove = s.lastPeg()
	arr := ui.Arrange(s.width, s.height, s.g.Size())
	var frame string
	switch {
	case arr.TooSmall:
		frame = ui.Compose(arr, nil, nil, "", s.styles)
	case s.corr != nil && s.corr.open:
		frame = s.exchangeFrame(s.width, s.height)
	default:
		s.board.Scale = arr.Scale
		s.board.Digits = s.linkDigits()
		s.board.Highlights = s.highlights()
		board := s.board.Render(s.g, s.styles, arr.BoardAvailW, arr.BoardAvailH)
		frame = ui.Compose(arr, board, s.panelLines(arr), s.statusLine(arr), s.styles)
	}
	v := tea.NewView(frame)
	v.AltScreen = true
	return v
}

// --- keys -------------------------------------------------------------------

func (s *gameScreen) handleKey(m tea.KeyPressMsg) tea.Cmd {
	key := m.String()
	ctx := ui.CtxBoard
	if s.linkMode {
		ctx = ui.CtxLink
	}
	// ctrl+c ends the program from anywhere, including out of a confirmation.
	// It still saves, because a player who kills the program has not abandoned
	// the game.
	if b, ok := s.keymap.Lookup(ctx, key); ok && b.Action == ui.ActQuit && key != "q" {
		return s.quitProgram()
	}
	if s.corr != nil && s.corr.open {
		return s.exchangeKey(m)
	}
	if s.confirm != gaNone {
		return s.handleConfirm(key)
	}
	s.message = ""
	if b, ok := gameKeyLookup(phasePlay, key); ok {
		return s.handleGameAction(b.action)
	}
	b, ok := s.keymap.Lookup(ctx, key)
	if !ok {
		return nil
	}
	switch b.Action {
	case ui.ActMoveLeft:
		s.moveCursor(-1, 0)
	case ui.ActMoveRight:
		s.moveCursor(1, 0)
	case ui.ActMoveUp:
		s.moveCursor(0, -1)
	case ui.ActMoveDown:
		s.moveCursor(0, 1)
	case ui.ActJumpLeft:
		s.moveCursor(-ui.JumpStep, 0)
	case ui.ActJumpRight:
		s.moveCursor(ui.JumpStep, 0)
	case ui.ActJumpUp:
		s.moveCursor(0, -ui.JumpStep)
	case ui.ActJumpDown:
		s.moveCursor(0, ui.JumpStep)
	case ui.ActEdgeTop:
		s.moveCursorTo(s.board.Cursor.Col, 0)
	case ui.ActEdgeBottom:
		s.moveCursorTo(s.board.Cursor.Col, s.g.Size()-1)
	case ui.ActEdgeLeft:
		s.moveCursorTo(0, s.board.Cursor.Row)
	case ui.ActEdgeRight:
		s.moveCursorTo(s.g.Size()-1, s.board.Cursor.Row)
	case ui.ActPlacePeg:
		s.placePeg()
	case ui.ActConfirm:
		return s.confirmKey()
	case ui.ActLinkMode:
		s.toggleLinkMode()
	case ui.ActToggleLink:
		s.toggleLink(key)
	case ui.ActAbortTurn:
		s.abortTurn()
	case ui.ActExitMode:
		s.linkMode = false
	case ui.ActQuit:
		return s.leave()
	}
	return nil
}

// confirmKey is what enter does, which depends on where the turn has got to: it
// clears a hotseat handover, then places the peg, then commits the turn. Once
// the game is over it leaves the screen.
func (s *gameScreen) confirmKey() tea.Cmd {
	switch {
	case s.g.Result().Over() || s.stopped:
		return s.leave()
	case s.handover:
		s.handover = false
		s.message = s.toMoveText()
		return nil
	}
	if ok, why := s.canAct(); !ok {
		s.message = why
		return nil
	}
	if s.g.Staged().PegPlaced {
		return s.commitTurn()
	}
	s.placePeg()
	return nil
}

func (s *gameScreen) handleConfirm(key string) tea.Cmd {
	b, ok := gameKeyLookup(phaseConfirm, key)
	if !ok {
		return nil
	}
	pending := s.confirm
	s.confirm = gaNone
	if b.action == gaNo {
		s.message = "carrying on"
		return nil
	}
	if pending == gaResign {
		return s.resign()
	}
	return nil
}

func (s *gameScreen) handleGameAction(a gameAction) tea.Cmd {
	switch a {
	case gaHint:
		return s.askHint()
	case gaSwap:
		return s.takeSwap()
	case gaDraw:
		return s.draw()
	case gaLiftPeg:
		s.liftPeg()
	case gaCode:
		if s.corr == nil {
			s.message = "this game is not played by codes"
		} else {
			s.corr.open = true
		}
	case gaResign:
		if side, ok := s.actingSide(); !ok {
			s.message = "there is nobody here to resign"
		} else if s.g.Result().Over() || s.stopped {
			s.message = "the game is already over"
		} else {
			s.confirm = gaResign
			s.message = fmt.Sprintf("resign as %s? y/n", side)
		}
	}
	return nil
}

// --- cursor -----------------------------------------------------------------

func (s *gameScreen) moveCursor(dCol, dRow int) {
	c := s.board.Cursor
	s.moveCursorTo(c.Col+dCol, c.Row+dRow)
}

// moveCursorTo clamps to the board and steps off an absent corner along the
// axis of motion, so the cursor always rests on a hole that exists.
func (s *gameScreen) moveCursorTo(col, row int) {
	n := s.g.Size()
	from := s.board.Cursor
	col, row = gsClamp(col, 0, n-1), gsClamp(row, 0, n-1)
	if (col == 0 || col == n-1) && (row == 0 || row == n-1) {
		switch {
		case col != from.Col:
			col += gsSign(from.Col - col)
		case row != from.Row:
			row += gsSign(from.Row - row)
		default:
			return
		}
	}
	s.board.Cursor = game.Point{Col: col, Row: row}
}

// --- the turn ---------------------------------------------------------------

// canAct reports whether a key that changes the game should be obeyed, and if
// not, what to tell the player.
func (s *gameScreen) canAct() (bool, string) {
	switch {
	case s.g.Result().Over():
		return false, s.notice
	case s.stopped:
		return false, s.notice
	case s.handover:
		return false, s.handoverPrompt()
	case !s.mover().Human():
		return false, s.waitingText()
	}
	return true, ""
}

func (s *gameScreen) placePeg() {
	if ok, why := s.canAct(); !ok {
		s.message = why
		return
	}
	if err := s.g.PlacePeg(s.board.Cursor); err != nil {
		s.message = s.explain(err)
		return
	}
	s.hint.clear()
	s.message = fmt.Sprintf("peg at %s — %s commits, %s aborts",
		s.board.Cursor, s.keyLabel(ui.ActConfirm), s.keyLabel(ui.ActAbortTurn))
}

// liftPeg lifts one of the mover's own pegs, with every link attached to it.
// Only a ruleset that opts into peg removal allows this at all, and the printed
// rules put removals before the turn's peg goes down, so the engine refuses it
// afterwards; the refusal says so rather than leaving the player guessing.
func (s *gameScreen) liftPeg() {
	if ok, why := s.canAct(); !ok {
		s.message = why
		return
	}
	p := s.board.Cursor
	if err := s.g.RemovePeg(p); err != nil {
		s.message = s.explain(err)
		return
	}
	s.hint.clear()
	s.message = fmt.Sprintf("%s lifted, with its links — %s places this turn's peg",
		p, s.keyLabel(ui.ActPlacePeg))
}

func (s *gameScreen) abortTurn() {
	if ok, why := s.canAct(); !ok {
		s.message = why
		return
	}
	s.g.AbortTurn()
	s.linkMode = false
	s.hint.clear()
	s.message = "turn aborted — nothing staged"
}

func (s *gameScreen) commitTurn() tea.Cmd {
	res, err := s.g.CommitTurn()
	if err != nil {
		s.message = s.explain(err)
		return nil
	}
	s.linkMode = false
	s.hint.clear()
	if s.session != nil {
		notation, nerr := s.g.MoveNotation(s.g.Entries() - 1)
		if nerr != nil {
			s.stop("this move could not be written down to send: " + nerr.Error())
			return nil
		}
		if serr := s.session.SendMove(notation); serr != nil {
			s.stop("the move could not be sent: " + serr.Error())
			return nil
		}
		if !s.checkSync() {
			return nil
		}
	}
	if !s.codeForLastEntry("move") {
		return nil
	}
	if res.Over() {
		return s.finish()
	}
	s.message = s.toMoveText()
	return s.passTurn()
}

// passTurn is what follows a completed turn on an unfinished game: a hotseat
// game waits for the other player to take the keyboard, and an engine seat
// starts thinking.
func (s *gameScreen) passTurn() tea.Cmd {
	if s.hotseat() {
		s.handover = true
		s.message = s.handoverPrompt()
		return nil
	}
	return s.maybeBotMove()
}

func (s *gameScreen) takeSwap() tea.Cmd {
	if ok, why := s.canAct(); !ok {
		s.message = why
		return nil
	}
	if !s.g.CanSwap() {
		s.message = "the swap option is not on offer"
		return nil
	}
	if err := s.g.Swap(); err != nil {
		s.message = s.explain(err)
		return nil
	}
	s.hint.clear()
	if s.session != nil {
		notation, nerr := s.g.MoveNotation(s.g.Entries() - 1)
		if nerr != nil {
			s.stop("the swap could not be written down to send: " + nerr.Error())
			return nil
		}
		if serr := s.session.SendMove(notation); serr != nil {
			s.stop("the swap could not be sent: " + serr.Error())
			return nil
		}
		if !s.checkSync() {
			return nil
		}
	}
	if !s.codeForLastEntry("swap") {
		return nil
	}
	s.message = "swap taken — the opening peg is yours"
	return s.passTurn()
}

func (s *gameScreen) draw() tea.Cmd {
	side, ok := s.actingSide()
	if !ok {
		s.message = "there is nobody here to offer a draw"
		return nil
	}
	if s.g.Result().Over() || s.stopped {
		s.message = "the game is over"
		return nil
	}
	if s.g.DrawOfferedBy() == side.Opponent() {
		if err := s.g.AcceptDraw(side); err != nil {
			s.message = s.explain(err)
			return nil
		}
		if s.session != nil {
			if err := s.session.SendDrawAccept(); err != nil {
				s.message = "the acceptance could not be sent: " + err.Error()
			}
		}
		if !s.codeForLastEntry("acceptance") {
			return nil
		}
		return s.finish()
	}
	if err := s.g.OfferDraw(side); err != nil {
		s.message = s.explain(err)
		return nil
	}
	if s.session != nil {
		if err := s.session.SendDrawOffer(); err != nil {
			s.message = "the offer could not be sent: " + err.Error()
			return nil
		}
	}
	if !s.codeForLastEntry("draw offer") {
		return nil
	}
	// A draw offer is not a turn: the same player still has the move, and may
	// make it with the offer standing.
	s.message = fmt.Sprintf("draw offered as %s — it stays open and does not use your turn", side)
	return nil
}

func (s *gameScreen) resign() tea.Cmd {
	side, ok := s.actingSide()
	if !ok {
		return nil
	}
	if err := s.g.Resign(side); err != nil {
		s.message = s.explain(err)
		return nil
	}
	if s.session != nil {
		if err := s.session.SendResign(); err != nil {
			s.message = "the resignation could not be sent: " + err.Error()
		}
	}
	if !s.codeForLastEntry("resignation") {
		return nil
	}
	return s.finish()
}

// --- link editing -----------------------------------------------------------

func (s *gameScreen) toggleLinkMode() {
	if s.linkMode {
		s.linkMode = false
		return
	}
	if ok, why := s.canAct(); !ok {
		s.message = why
		return
	}
	s.linkMode = true
	if s.g.At(s.board.Cursor) != s.g.Turn() {
		s.message = "link mode: move onto one of your own pegs to see its links"
	}
}

// linkDigits is the link-mode overlay: the direction digit drawn on every
// knight neighbour of the cursor that holds one of the mover's pegs. The digit
// sits on the hole it acts on, so the player reads the target and presses it.
func (s *gameScreen) linkDigits() map[game.Point]rune {
	if !s.linkMode || s.g.At(s.board.Cursor) != s.g.Turn() {
		return nil
	}
	digits := make(map[game.Point]rune, game.NumDirs)
	for dir := game.Dir(0); dir < game.NumDirs; dir++ {
		target := s.board.Cursor.Add(dir)
		if s.g.Exists(target) && s.g.At(target) == s.g.Turn() {
			digits[target] = rune('1' + dir)
		}
	}
	return digits
}

// toggleLink is what a link-mode digit does to the link between the cursor's
// peg and the neighbour the digit sits on.
//
// A turn is removals, then its one peg, then link edits, then the commit. The
// engine enforces the removal half of that itself; the screen holds the rest by
// asking for the turn's peg before a new link is made, so a turn always has one
// shape. Nothing becomes unreachable: a link that could have been made before
// the peg can be made after it, and putting back a link this turn took off is
// undoing a removal rather than adding a link, so that stays available
// throughout.
func (s *gameScreen) toggleLink(key string) {
	if ok, why := s.canAct(); !ok {
		s.message = why
		return
	}
	if s.g.At(s.board.Cursor) != s.g.Turn() {
		s.message = "link mode: move onto one of your own pegs first"
		return
	}
	dir := game.Dir(key[0] - '1')
	a := s.board.Cursor
	b := a.Add(dir)
	l, knight := game.NewLink(a, b)
	if !knight {
		return
	}
	switch {
	case s.g.HasLink(l):
		if err := s.g.RemoveLink(a, b); err != nil {
			s.message = s.explainLink(l, err)
			return
		}
		s.hint.clear()
		s.message = fmt.Sprintf("%s taken off", l)
	case s.stagedRemoval(l):
		if err := s.g.AddLink(a, b); err != nil {
			s.message = s.explainLink(l, err)
			return
		}
		s.hint.clear()
		s.message = fmt.Sprintf("%s put back", l)
	case !s.g.Staged().PegPlaced:
		s.message = fmt.Sprintf("new links come after this turn's peg — %s places it",
			s.keyLabel(ui.ActPlacePeg))
	default:
		if err := s.g.AddLink(a, b); err != nil {
			s.message = s.explainLink(l, err)
			return
		}
		s.hint.clear()
		s.message = fmt.Sprintf("%s linked", l)
	}
}

// stagedRemoval reports whether this turn took the link off the board.
func (s *gameScreen) stagedRemoval(l game.Link) bool {
	want := l.Canonical()
	for _, r := range s.g.Staged().Removed {
		if r.Canonical() == want {
			return true
		}
	}
	return false
}

// explainLink turns a refused link edit into the reason the engine gave. A
// crossing names the link that actually blocks it, which is the one piece of
// information the player needs to pick a different edge.
func (s *gameScreen) explainLink(l game.Link, err error) string {
	switch {
	case errors.Is(err, game.ErrLinkCrosses):
		if blocker, ok := s.g.LinkBlockedBy(l, s.g.Turn()); ok {
			return fmt.Sprintf("%s would cross %s, which is already on the board", l, blocker)
		}
		return fmt.Sprintf("%s would cross a link already on the board", l)
	case errors.Is(err, game.ErrRemoveAfterPeg):
		return fmt.Sprintf("removals come before this turn's peg — %s aborts the turn",
			s.keyLabel(ui.ActAbortTurn))
	case errors.Is(err, game.ErrLinkingLocked):
		return "these rules link automatically: there is nothing to edit by hand"
	case errors.Is(err, game.ErrRemovalLocked):
		return "these rules keep links from earlier turns on the board"
	case errors.Is(err, game.ErrNotOwnPeg):
		return fmt.Sprintf("%s needs one of your pegs at both ends", l)
	case errors.Is(err, game.ErrLinkExists):
		return fmt.Sprintf("%s is already there", l)
	case errors.Is(err, game.ErrNoSuchLink):
		return fmt.Sprintf("there is no %s to take off", l)
	}
	return s.explain(err)
}

// explain renders an engine refusal for a player. Each case is a sentinel, so
// the wording follows the rule that was broken rather than guessing from text.
func (s *gameScreen) explain(err error) string {
	switch {
	case errors.Is(err, game.ErrOccupied):
		return "that hole already holds a peg"
	case errors.Is(err, game.ErrCornerHole):
		return "the corner holes do not exist on a TwixT board"
	case errors.Is(err, game.ErrOffBoard):
		return "that is off the board"
	case errors.Is(err, game.ErrOpponentBorder):
		return fmt.Sprintf("that border %s is your opponent's: you may not play in it",
			gsBorderWord(s.g.Turn().Opponent()))
	case errors.Is(err, game.ErrPegAlreadySet):
		return fmt.Sprintf("one peg per turn — %s commits, %s aborts",
			s.keyLabel(ui.ActConfirm), s.keyLabel(ui.ActAbortTurn))
	case errors.Is(err, game.ErrNoPegPlaced):
		return fmt.Sprintf("a turn places exactly one peg — %s places one", s.keyLabel(ui.ActPlacePeg))
	case errors.Is(err, game.ErrRemoveAfterPeg):
		return fmt.Sprintf("removals come before this turn's peg — %s aborts the turn",
			s.keyLabel(ui.ActAbortTurn))
	case errors.Is(err, game.ErrPegRemovalOff):
		return "these rules do not allow lifting a peg once it is placed"
	case errors.Is(err, game.ErrSwapUnavailable):
		return "the swap option is not on offer"
	case errors.Is(err, game.ErrNoDrawOffer):
		return "there is no draw offer to accept"
	case errors.Is(err, game.ErrNotYourTurn):
		return "it is not your move"
	case errors.Is(err, game.ErrGameOver):
		return "the game is over"
	}
	return err.Error()
}

// --- the opponent engine ----------------------------------------------------

// maybeBotMove starts the engine when the side to move is one. The search runs
// in a command on its own copy of the position, so Update never blocks and the
// keyboard stays live; the context is cancelled when the player leaves.
func (s *gameScreen) maybeBotMove() tea.Cmd {
	if s.stopped || s.leaving || s.botThinking || s.g.Result().Over() {
		return nil
	}
	engine := s.mover().Bot
	if engine == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelBot()
	s.botCancel = cancel
	s.botThinking = true
	s.botGen++
	gen := s.botGen
	s.spinner = 0
	pos := s.g.Clone()
	return tea.Batch(
		func() tea.Msg {
			move, err := engine.Move(ctx, pos)
			return botMoveMsg{gen: gen, move: move, err: err}
		},
		s.spinnerTick(),
	)
}

func (s *gameScreen) spinnerTick() tea.Cmd {
	return tea.Tick(gsSpinnerInterval, func(time.Time) tea.Msg { return botTickMsg{} })
}

func (s *gameScreen) applyBotMove(msg botMoveMsg) tea.Cmd {
	if msg.gen != s.botGen || s.leaving {
		return nil
	}
	s.botThinking = false
	s.cancelBot()
	if s.stopped || s.g.Result().Over() {
		return nil
	}
	if msg.err != nil {
		s.stop("the opponent engine has no move: " + msg.err.Error())
		return nil
	}
	if _, err := s.g.PlayPeg(msg.move); err != nil {
		s.stop(fmt.Sprintf("the opponent engine offered %s, which the rules refuse: %s",
			msg.move, s.explain(err)))
		return nil
	}
	s.hint.clear()
	s.message = fmt.Sprintf("%s played %s", s.seatName(s.g.Turn().Opponent()), msg.move)
	if s.g.Result().Over() {
		return s.finish()
	}
	return s.maybeBotMove()
}

func (s *gameScreen) cancelBot() {
	if s.botCancel != nil {
		s.botCancel()
		s.botCancel = nil
	}
}

// --- hints ------------------------------------------------------------------

// askHint consults the engine on the player's own position. It is only offered
// when the game was set up with hints and an engine to ask, and only on the
// human's turn, since advice about somebody else's move is not advice.
func (s *gameScreen) askHint() tea.Cmd {
	if !s.cfg.Hints || s.cfg.HintFor == nil {
		s.message = hintOff
		return nil
	}
	if ok, why := s.canAct(); !ok {
		s.message = why
		return nil
	}
	if s.hint.running {
		s.message = "the engine is already working on that"
		return nil
	}
	if s.hint.shown || s.hint.unavailable != "" {
		s.hint.clear()
		return nil
	}
	if s.g.Staged().PegPlaced {
		s.message = fmt.Sprintf("advice is for the move you have not made yet — %s aborts the turn",
			s.keyLabel(ui.ActAbortTurn))
		return nil
	}
	return s.hint.ask(s.cfg.HintFor, s.g.Clone())
}

// --- remote play ------------------------------------------------------------

// watchSession waits for one event from the opponent. It is re-issued after
// every event, which is how a channel is read from inside the update loop
// without blocking it.
func (s *gameScreen) watchSession() tea.Cmd {
	events := s.session.Events()
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return netClosedMsg{}
		}
		return netEventMsg{ev}
	}
}

func (s *gameScreen) applyNetEvent(ev netplay.Event) tea.Cmd {
	if s.leaving {
		return nil
	}
	again := s.watchSession()
	switch ev.Kind {
	case netplay.EventConnected:
		s.netNote = "connected: " + s.opponentName()
		return again
	case netplay.EventMove:
		if err := s.g.PlayNotation(ev.Move); err != nil {
			s.stop(fmt.Sprintf("the opponent sent %q, which does not fit this position: %s. "+
				"The two games are no longer the same game, so play stops here", ev.Move, s.explain(err)))
			return nil
		}
		s.hint.clear()
		s.message = fmt.Sprintf("%s played %s", s.opponentName(), ev.Move)
	case netplay.EventResign:
		if err := s.g.Resign(s.remoteSide); err != nil {
			s.stop("the opponent resigned but the game would not take it: " + s.explain(err))
			return nil
		}
	case netplay.EventDrawOffer:
		if err := s.g.OfferDraw(s.remoteSide); err != nil {
			s.message = s.explain(err)
			return again
		}
		s.message = fmt.Sprintf("%s offers a draw — %s accepts", s.opponentName(), s.gameKeyLabel(gaDraw))
		return again
	case netplay.EventDrawAccept:
		if err := s.g.AcceptDraw(s.remoteSide); err != nil {
			s.stop("the opponent accepted a draw the game does not know about: " + s.explain(err))
			return nil
		}
	case netplay.EventDisconnected:
		text := ev.Text
		if text == "" {
			text = "the opponent's connection dropped"
		}
		s.stop(text + " — the game is saved and can be resumed")
		return nil
	case netplay.EventError:
		if errors.Is(ev.Err, netplay.ErrDiverged) {
			s.stop("this end and the opponent no longer hold the same position, " +
				"so play stops rather than carrying on from a board you do not agree on")
			return nil
		}
		text := ev.Text
		if text == "" && ev.Err != nil {
			text = ev.Err.Error()
		}
		s.stop("the connection failed: " + text)
		return nil
	default:
		return again
	}
	if !s.checkSync() {
		return nil
	}
	if s.g.Result().Over() {
		s.finish()
		return nil
	}
	return tea.Batch(again, s.maybeBotMove())
}

// checkSync compares this end's position with the copy the session keeps in
// step with the opponent's. They are separate games, so a disagreement is a
// divergence whether or not the protocol has noticed it yet, and the honest
// answer is to stop.
func (s *gameScreen) checkSync() bool {
	r, ok := s.session.(netplay.Resumable)
	if !ok {
		return true
	}
	if netplay.PositionHash(s.g) == netplay.PositionHash(r.Position()) {
		return true
	}
	s.stop("this end and the opponent no longer hold the same position, " +
		"so play stops rather than carrying on from a board you do not agree on")
	return false
}

func (s *gameScreen) opponentName() string {
	switch {
	case s.session != nil:
		if name := s.session.OpponentName(); name != "" {
			return name
		}
	case s.corr != nil:
		// There is no connection to ask, so the seat's own label is all there
		// is, and it is what the invite carried.
		if name := s.cfg.Seats[s.remoteSide].Label; name != "" {
			return name
		}
	}
	return "the opponent"
}

// --- leaving, recording, saving ---------------------------------------------

// stop ends play with a reason the player can read. The position is kept on
// screen: what has happened is exactly what the player needs to see.
func (s *gameScreen) stop(reason string) {
	s.stopped = true
	s.notice = reason
	s.message = ""
	s.linkMode = false
	s.hint.clear()
	s.cancelBot()
	s.botThinking = false
}

// finish records and saves a game that has just ended, once.
func (s *gameScreen) finish() tea.Cmd {
	res := s.g.Result()
	if !res.Over() || s.recorded {
		return nil
	}
	s.recorded = true
	s.stopped = true
	s.linkMode = false
	s.handover = false
	s.hint.clear()
	s.cancelBot()
	s.botThinking = false
	s.notice = s.resultText(res)
	s.message = ""

	var problems []string
	if err := s.record(res); err != nil {
		problems = append(problems, "the leaderboard was not updated: "+err.Error())
	}
	if err := s.save(true); err != nil {
		problems = append(problems, "the game was not saved: "+err.Error())
	}
	if len(problems) > 0 {
		s.notice += " (" + strings.Join(problems, "; ") + ")"
	}
	return nil
}

// record writes the single leaderboard row for this game.
//
// One row per game, never one per player: the board credits both participants
// from that one row by reading it backwards, so a hotseat game recorded once
// per player would be counted twice. A hotseat game is written from the
// vertical seat's point of view.
func (s *gameScreen) record(res game.Result) error {
	if s.deps.Board == nil {
		return nil
	}
	player, side, opponent := s.rowSides()
	if player == "" || opponent == "" {
		// Nothing to credit: an unnamed seat cannot appear on a leaderboard.
		return nil
	}
	outcome := leaderboard.DrawOutcome
	switch res.Winner() {
	case side:
		outcome = leaderboard.Win
	case side.Opponent():
		outcome = leaderboard.Loss
	}
	now := s.deps.Clock()
	return s.deps.Board.Record(leaderboard.Result{
		Played:   now,
		Player:   player,
		Opponent: opponent,
		Outcome:  outcome,
		Side:     side.String(),
		Moves:    s.g.Ply(),
		Ruleset:  s.g.Rules().Canonical(),
		Duration: now.Sub(s.started),
	})
}

// rowSides is the single point of view a game is recorded and stored from.
func (s *gameScreen) rowSides() (player string, side game.Player, opponent string) {
	if local, only := s.cfg.LocalSide(); only {
		return s.cfg.Seats[local].Profile, local, s.cfg.Opponent(local)
	}
	return s.cfg.Seats[game.Vertical].Profile, game.Vertical, s.cfg.Seats[game.Horizontal].Profile
}

func (s *gameScreen) save(finished bool) error {
	if s.deps.Games == nil {
		return nil
	}
	rec, err := s.g.Record()
	if err != nil {
		return err
	}
	player, side, opponent := s.rowSides()
	return s.deps.Games.Put(gamestore.Saved{
		ID:       s.storeID,
		Kind:     s.cfg.Kind,
		Created:  s.created,
		Player:   player,
		Side:     side.String(),
		Opponent: opponent,
		Record:   rec.Encode(),
		Finished: finished,
	})
}

// depart is the tidying every way out of this screen shares: the searches are
// stopped, the staged turn is dropped, an unfinished game is saved so it can be
// picked up again, and the opponent is told the connection is going.
//
// It is idempotent, because the shell also calls it through Depart on the way
// out and a screen may already have departed by then.
func (s *gameScreen) depart() error {
	if s.leaving {
		return nil
	}
	s.leaving = true
	s.cancelBot()
	s.botThinking = false
	s.hint.stop()
	// A staged turn is not part of the position and cannot be stored; dropping
	// it is what the engine does on abort anyway.
	s.g.AbortTurn()
	var err error
	if !s.g.Result().Over() {
		err = s.save(false)
		if err == nil {
			// Say so where the player will actually see it. Which place that
			// is depends on what happens next: a departure that ends the
			// program has to leave the line for the command line, because the
			// interface takes its own output with it, while a departure that
			// comes back to the menu has a screen to say it on and should say
			// it there, at the time. Only the shell knows which of the two
			// this is, so the line is left here for it to collect. Without it
			// the player is left not knowing whether the game they were part
			// way through survived.
			s.departNote = fmt.Sprintf("game saved as %s — %s", s.storeID, s.resumeHint())
		}
	}
	if s.session != nil {
		s.session.Close()
	}
	if err != nil {
		return fmt.Errorf("saving the game: %w", err)
	}
	return nil
}

// resumeHint says how to pick this game up again, which differs by how it is
// being played: a correspondence game is opened by name from the command line,
// and everything else from the menu.
func (s *gameScreen) resumeHint() string {
	if s.cfg.Kind == gamestore.Correspondence {
		return fmt.Sprintf("open it again with: twixtui play correspondence --game %s", s.storeID)
	}
	return "pick it up from Continue a saved game"
}

// Depart satisfies Departing, so that the shell answering the global quit key
// itself does not skip the save. Without it, leaving with the plain letter saved
// an unfinished game and leaving with the control key discarded it, which is the
// same act as far as the player is concerned.
//
// This is the departure that ends the program, so anything the screen has to
// tell the player goes to the command line: there will be no screen left to
// read it on.
func (s *gameScreen) Depart() {
	err := s.depart()
	if note := s.DepartNote(); note != "" {
		s.deps.note("%s", note)
	}
	if err != nil {
		// There is no screen left to show this on, so the best that can be done
		// is to put it where a player looking for their lost game will find it.
		fmt.Fprintf(os.Stderr, "twixtui: %v\n", err)
	}
}

// DepartNote satisfies Noting. It hands over the line depart left and forgets
// it, so that one departure is announced once, wherever the shell puts it.
func (s *gameScreen) DepartNote() string {
	note := s.departNote
	s.departNote = ""
	return note
}

// leave hands control back to the shell.
func (s *gameScreen) leave() tea.Cmd {
	if err := s.depart(); err != nil {
		return Fail(err)
	}
	return Back()
}

// quitProgram ends the program. The game is saved on the way out just as it is
// when the player leaves by hand, and a save that failed is reported rather
// than swallowed, because there is no screen left to show it on.
func (s *gameScreen) quitProgram() tea.Cmd {
	return Done(DoneMsg{Quit: true, Err: s.depart()})
}

// --- panel and status -------------------------------------------------------

func (s *gameScreen) highlights() []game.Point {
	var out []game.Point
	if st := s.g.Staged(); st.PegPlaced {
		out = append(out, st.Peg)
	}
	return append(out, s.hint.highlights()...)
}

// panelLines builds the information panel, most useful line first.
//
// A short terminal takes the panel's space from the bottom, so the order is the
// order of usefulness: what the player has to act on now — whose turn it is, a
// swap that lasts one turn, advice they asked for, the edits they have staged —
// comes before what can be read at leisure. The keys go last because the status
// line always carries the essential ones.
func (s *gameScreen) panelLines(arr ui.Arrangement) []string {
	if arr.Panel == ui.PanelNone {
		return nil
	}
	w := arr.PanelW
	var lines []string
	add := func(text string) { lines = append(lines, gsTruncate(text, w)) }
	addWrapped := func(text string) { lines = append(lines, gsWrap(text, w)...) }
	blank := func() {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}

	if s.notice != "" {
		addWrapped(s.style(s.styles.Message, s.notice))
	} else {
		add(s.headlineText())
	}
	if s.swapOffered() {
		blank()
		addWrapped(s.style(s.styles.Message, s.swapText()))
	}
	if hint := s.hint.lines(w); len(hint) > 0 {
		blank()
		lines = append(lines, hint...)
	}
	if s.linkMode {
		blank()
		lines = append(lines, s.linkModeLines(w)...)
	}
	if staged := s.stagedLines(); len(staged) > 0 {
		blank()
		for _, l := range staged {
			add(l)
		}
	}

	blank()
	for _, side := range []game.Player{game.Vertical, game.Horizontal} {
		// Two spaces, never a tab: a terminal expands a tab to the next
		// eight-column stop, which threw the seat list out of alignment
		// whenever the rows above it changed width.
		mark := "  "
		if side == s.g.Turn() && !s.g.Result().Over() {
			mark = "> "
		}
		glyph := s.style(s.pegStyle(side), s.pegGlyph(side))
		// The axis reminder is the first thing to go when the panel is narrow:
		// losing "joins left and right" costs a player much less than losing
		// half the opponent's name.
		subject := side.String() + " " + s.seatName(side)
		body := fitLabelled(subject, gsAxisText(side), " · ", w-ansi.StringWidth(mark+glyph)-1)
		add(mark + glyph + " " + body)
	}

	blank()
	add(fmt.Sprintf("move %d", s.g.Ply()+1))
	if last := s.lastMoveText(); last != "" {
		add("last " + last)
	}
	// A standing offer is only news while the game is running; leaving it under
	// the result read as though the finished game were still being negotiated.
	if by := s.g.DrawOfferedBy(); by != game.NoPlayer && !s.g.Result().Over() {
		add("draw offered by " + by.String())
	}
	if s.netNote != "" {
		add(s.netNote)
	}
	if s.corr != nil {
		add(s.corrPanelLine())
	}

	blank()
	title := "twixtui"
	if s.cfg.Kind != "" {
		title += " · " + string(s.cfg.Kind)
	}
	add(s.style(s.styles.PanelTitle, title))
	add(s.style(s.styles.PanelText, s.rulesLine()))

	blank()
	entries := s.helpEntries()
	// The label column is as wide as the widest key plus a space. Six suits the
	// board keys and used to be written out, which left a control key's label
	// touching its description.
	labelW := 6
	for _, e := range entries {
		if n := ansi.StringWidth(e.Label) + 1; n > labelW {
			labelW = n
		}
	}
	for _, e := range entries {
		add(s.style(s.styles.Label, gsPad(e.Label, labelW)) + e.Help)
	}
	return lines
}

// helpEntries is the contextual key help: the board keys for the context in
// force, then the game keys that apply now. Both come from their tables, so the
// help cannot describe a key the screen does not have.
func (s *gameScreen) helpEntries() []ui.HelpEntry {
	if s.confirm != gaNone {
		var out []ui.HelpEntry
		for _, b := range gameBindings {
			if b.phases&phaseConfirm != 0 {
				out = append(out, ui.HelpEntry{Label: b.label, Help: b.help})
			}
		}
		return out
	}
	ctx := ui.CtxBoard
	if s.linkMode {
		ctx = ui.CtxLink
	}
	out := s.splitQuitHelp(ctx, s.keymap.HelpEntries(ctx))
	for _, b := range gameBindings {
		if b.phases&phasePlay == 0 {
			continue
		}
		if b.action == gaHint && (!s.cfg.Hints || s.cfg.HintFor == nil) {
			continue
		}
		if b.action == gaSwap && !s.swapOffered() {
			continue
		}
		if b.action == gaLiftPeg && !s.g.Rules().PegRemoval {
			continue
		}
		if b.action == gaCode && s.corr == nil {
			continue
		}
		out = append(out, ui.HelpEntry{Label: b.label, Help: b.help})
	}
	return out
}

// nested satisfies nesting: the shell says whether leaving this game comes back
// to a screen or ends the program.
func (s *gameScreen) nested(hasReturn bool) { s.returns = hasReturn }

// splitQuitHelp gives the two quit keys a line each in a game that was opened
// over another screen, because there they do different things: the plain letter
// leaves the game and comes back, the control form ends the program. They share
// one binding, so one description covered both outcomes and was therefore wrong
// about one of them. A game that is the whole program keeps the single line the
// keymap gives, where the shared wording is accurate: there, both keys quit.
//
// The control keys are read off the binding rather than written out again here,
// which is the same rule the shell uses to decide which of them it answers
// itself.
func (s *gameScreen) splitQuitHelp(ctx ui.Context, entries []ui.HelpEntry) []ui.HelpEntry {
	b, ok := s.keymap.ByAction(ctx, ui.ActQuit)
	if !s.returns || !ok {
		return entries
	}
	out := make([]ui.HelpEntry, 0, len(entries)+1)
	for _, e := range entries {
		if e.Label != b.Label || e.Help != b.Help {
			out = append(out, e)
			continue
		}
		out = append(out, ui.HelpEntry{Label: b.Label, Help: "leave the game"})
		for _, k := range b.Keys {
			if strings.HasPrefix(k, "ctrl+") {
				out = append(out, ui.HelpEntry{Label: k, Help: "leave and end the program"})
			}
		}
	}
	return out
}

// headlineText is the one line that says what the game is waiting for.
func (s *gameScreen) headlineText() string {
	if res := s.g.Result(); res.Over() {
		return s.style(s.styles.Message, s.resultText(res))
	}
	switch {
	case s.handover:
		return s.style(s.styles.Message, s.handoverPrompt())
	case s.botThinking:
		return s.style(s.styles.PanelText, fmt.Sprintf("%s is thinking %s",
			s.seatName(s.g.Turn()), gsSpinnerFrames[s.spinner%len(gsSpinnerFrames)]))
	case !s.mover().Human():
		return s.style(s.styles.PanelText, s.waitingText())
	}
	return s.style(s.styles.PanelText, s.toMoveText())
}

func (s *gameScreen) toMoveText() string {
	side := s.g.Turn()
	return fmt.Sprintf("%s %s to move: %s", s.pegGlyph(side), side, s.seatName(side))
}

// handoverPrompt is the hotseat gate. Two people share one keyboard, so the
// screen stops between turns and names who should be at it: without this the
// player who has just moved can place their opponent's peg by carrying on.
func (s *gameScreen) handoverPrompt() string {
	side := s.g.Turn()
	return fmt.Sprintf("%s's turn — %s, press %s when you have the keyboard",
		side, s.seatName(side), s.keyLabel(ui.ActConfirm))
}

func (s *gameScreen) waitingText() string {
	seat := s.mover()
	switch {
	case seat.Bot != nil:
		return fmt.Sprintf("waiting for the %s engine", seat.Bot.Tier())
	case seat.Remote:
		return "waiting for " + s.opponentName()
	}
	return s.toMoveText()
}

func (s *gameScreen) resultText(res game.Result) string {
	var who string
	if w := res.Winner(); w == game.NoPlayer {
		who = "drawn"
	} else {
		who = fmt.Sprintf("%s (%s) wins", s.seatName(w), w)
	}
	why := ""
	switch res.Reason {
	case game.Connection:
		why = "a chain from border to border"
	case game.NoMovesLeft:
		why = "no legal move left"
	case game.Resignation:
		why = "resignation"
	case game.Agreement:
		why = "agreement"
	}
	out := "game over: " + who
	if why != "" {
		out += " by " + why
	}
	return out + " after " + plural(s.g.Ply(), "move")
}

// lastPeg reports the hole the most recent peg went into. Entries that place no
// peg — a resignation, a draw offer or its acceptance — leave the mark where it
// was, because the board has not changed and the last peg played is still the
// last peg played.
func (s *gameScreen) lastPeg() (game.Point, bool) {
	history := s.g.History()
	for i := len(history) - 1; i >= 0; i-- {
		switch history[i].Kind {
		case game.PlaceMove, game.SwapMove:
			return history[i].Peg, true
		}
	}
	return game.Point{}, false
}

// lastMoveText describes the most recent record entry.
//
// The transcript's own spelling of the entries that are not moves is meant for a
// file, not a panel: "v:draw?" is exact and unreadable. A move is written as the
// hole, which is what a player would say anyway.
func (s *gameScreen) lastMoveText() string {
	if s.g.Entries() == 0 {
		return ""
	}
	entry := s.g.History()[s.g.Entries()-1]
	switch entry.Kind {
	case game.SwapMove:
		return fmt.Sprintf("%s took the swap", entry.Player)
	case game.ResignMove:
		return fmt.Sprintf("%s resigned", entry.Player)
	case game.DrawOfferMove:
		return fmt.Sprintf("%s offered a draw", entry.Player)
	case game.DrawAcceptMove:
		return fmt.Sprintf("%s accepted the draw", entry.Player)
	}
	notation, err := s.g.MoveNotation(s.g.Entries() - 1)
	if err != nil {
		return ""
	}
	return notation
}

// stagedLines shows the uncommitted edits of the turn in progress, so the
// player can see exactly what committing will do.
func (s *gameScreen) stagedLines() []string {
	st := s.g.Staged()
	var out []string
	if st.PegPlaced {
		out = append(out, "staged peg "+st.Peg.String())
		if names := gsLinkNames(st.Peg, st.AutoLinks); len(names) > 0 {
			out = append(out, "  links "+strings.Join(names, " "))
		}
		// A peg that reaches one of its own and links to none of them looks
		// like the board failed to notice, so the links it could not take are
		// named along with the ones it did.
		if names := s.crossedLinks(st.Peg); len(names) > 0 {
			out = append(out, "  blocked "+strings.Join(names, " "))
		}
	}
	if len(st.Added) > 0 {
		out = append(out, "  added "+gsLinkList(st.Added))
	}
	if len(st.Removed) > 0 {
		out = append(out, "  removed "+gsLinkList(st.Removed))
	}
	if len(st.RemovedPegs) > 0 {
		names := make([]string, len(st.RemovedPegs))
		for i, p := range st.RemovedPegs {
			names[i] = p.String()
		}
		out = append(out, "  lifted "+strings.Join(names, " "))
	}
	return out
}

// crossedLinks names the links between the turn's peg and its own knight
// neighbours that a link already on the board is in the way of. They are the
// difference between the links a placement offers and the ones it takes, and
// the only reason a placement takes fewer than it reaches.
func (s *gameScreen) crossedLinks(peg game.Point) []string {
	var names []string
	for dir := game.Dir(0); dir < game.NumDirs; dir++ {
		target := peg.Add(dir)
		if !s.g.Exists(target) || s.g.At(target) != s.g.Turn() {
			continue
		}
		l, knight := game.NewLink(peg, target)
		if !knight || s.g.HasLink(l) {
			continue
		}
		if _, blocked := s.g.LinkBlockedBy(l, s.g.Turn()); blocked {
			names = append(names, l.String())
		}
	}
	return names
}

// linkModeLines say what link mode is editing and what each digit will do to
// it, which is the whole affordance: the digits are on the board, their meaning
// is here.
func (s *gameScreen) linkModeLines(width int) []string {
	out := []string{s.style(s.styles.LinkDigit, "link mode")}
	if s.g.At(s.board.Cursor) != s.g.Turn() {
		return append(out, gsWrap("move onto one of your own pegs: the digits appear on the pegs it can link to", width)...)
	}
	out = append(out, gsWrap("editing links at "+s.board.Cursor.String(), width)...)
	if s.g.Staged().PegPlaced {
		out = append(out, gsWrap("this turn's peg is down: digits add a link or withdraw one", width)...)
	} else {
		out = append(out, gsWrap("before you place a peg you can only take links off: a digit removes that link. Adding one comes after the peg is down.", width)...)
	}
	digits := s.linkDigits()
	if len(digits) == 0 {
		return append(out, gsWrap("no peg of yours is a knight's move away", width)...)
	}
	for dir := game.Dir(0); dir < game.NumDirs; dir++ {
		target := s.board.Cursor.Add(dir)
		if _, ok := digits[target]; !ok {
			continue
		}
		out = append(out, gsTruncate(fmt.Sprintf("  %d %s  %-9s %s",
			dir+1, target, s.linkVerb(target), dir), width))
	}
	return out
}

// linkVerb says what the digit on a hole will do, which is what makes the
// overlay readable: a link of the player's own shows as one to remove, and one
// that is in the way of a link already on the board shows as neither.
//
// A crossing is checked before the turn's order is, because "after peg" would
// promise the link becomes available once the peg is down, and a blocked link
// never does: placing a peg only ever adds links, so what blocks the edge now
// still blocks it afterwards. The digit is kept rather than dropped, so the
// player can still press it and be told which link is in the way.
func (s *gameScreen) linkVerb(target game.Point) string {
	l, knight := game.NewLink(s.board.Cursor, target)
	switch {
	case !knight:
		return ""
	case s.g.HasLink(l):
		return "remove"
	}
	if _, blocked := s.g.LinkBlockedBy(l, s.g.Turn()); blocked {
		return "blocked"
	}
	switch {
	case s.stagedRemoval(l):
		return "put back"
	case !s.g.Staged().PegPlaced:
		return "after peg"
	}
	return "add"
}

func (s *gameScreen) swapOffered() bool {
	return s.g.CanSwap() && !s.stopped && !s.handover && s.mover().Human()
}

// swapText explains the pie rule in terms of this position: the option lasts
// one turn only and is easy to miss, so it says what taking it does to the peg
// already on the board.
func (s *gameScreen) swapText() string {
	text := fmt.Sprintf("swap on offer: %s takes the opening peg %s for yourself instead of answering it",
		s.gameKeyLabel(gaSwap), s.openingPeg())
	// The peg reflects across the board's diagonal, which for a hole on that
	// diagonal is where it already is. Saying "mirrored to M13" about a peg on
	// M13 reads like a fault, so the mirror is only mentioned when it moves.
	if p, ok := s.openingPoint(); ok {
		if mirrored := (game.Point{Col: p.Row, Row: p.Col}); mirrored != p {
			text += fmt.Sprintf(", which moves to %s so it is legal for you", mirrored)
		}
	}
	return text + ". This turn only."
}

func (s *gameScreen) openingPoint() (game.Point, bool) {
	for _, m := range s.g.History() {
		if m.Kind == game.PlaceMove {
			return m.Peg, true
		}
	}
	return game.Point{}, false
}

func (s *gameScreen) openingPeg() string {
	if p, ok := s.openingPoint(); ok {
		return p.String()
	}
	return ""
}

// statusLine is the one line that is always there. It carries the answer to the
// last keypress when there is one, and otherwise the keys that matter now; when
// the terminal is too narrow for a panel it also carries whose turn it is,
// because there is nowhere else for that to go.
func (s *gameScreen) statusLine(arr ui.Arrangement) string {
	if s.message != "" {
		return s.style(s.styles.Status, gsTruncate(s.message, arr.Width))
	}
	var parts []string
	if arr.Panel == ui.PanelNone {
		if s.notice != "" {
			return s.style(s.styles.Status, gsTruncate(s.notice, arr.Width))
		}
		parts = append(parts, gsPlain(s.headlineText()))
		if h := s.hint.statusText(); h != "" {
			parts = append(parts, h)
		}
	}
	switch {
	case s.confirm != gaNone:
		parts = append(parts, "y yes · n no")
	case s.g.Result().Over() || s.stopped:
		if s.corr != nil && len(s.corr.pending) > 0 {
			// The last code still has to reach the opponent, or their copy of
			// the game never ends.
			parts = append(parts, s.gameKeyLabel(gaCode)+" the code to send")
		}
		parts = append(parts, s.keyLabel(ui.ActQuit)+" leave")
	case s.handover:
		parts = append(parts, s.keyLabel(ui.ActConfirm)+" ready")
	case s.linkMode:
		parts = append(parts, s.keymap.HintLine(ui.CtxLink, ui.ActToggleLink, ui.ActExitMode, ui.ActConfirm))
	default:
		parts = append(parts, s.keymap.HintLine(ui.CtxBoard,
			ui.ActPlacePeg, ui.ActConfirm, ui.ActLinkMode, ui.ActAbortTurn), s.quitHint())
		if s.corr != nil {
			parts = append(parts, s.gameKeyLabel(gaCode)+" exchange")
		}
		if s.swapOffered() {
			parts = append(parts, s.gameKeyLabel(gaSwap)+" swap")
		}
		if s.cfg.Hints && s.cfg.HintFor != nil {
			parts = append(parts, s.gameKeyLabel(gaHint)+" hint")
		}
		if s.g.Rules().PegRemoval {
			parts = append(parts, s.gameKeyLabel(gaLiftPeg)+" lift peg")
		}
		parts = append(parts, s.gameKeyLabel(gaDraw)+" draw", s.gameKeyLabel(gaResign)+" resign")
	}
	return s.style(s.styles.Status, gsTruncate(strings.Join(parts, " · "), arr.Width))
}

// quitHint is the terse form of the quit key for the status line. A game opened
// over another screen is left rather than quit, and one word is all the line
// has room to say about it; the help panel carries the fuller version. The
// wording for a game that is the whole program comes from the keymap, so the
// usual case cannot drift from the binding.
func (s *gameScreen) quitHint() string {
	b, ok := s.keymap.ByAction(ui.CtxBoard, ui.ActQuit)
	if !ok {
		return ""
	}
	if s.returns {
		return b.Label + " leave"
	}
	return b.Label + " " + b.Short
}

// --- small helpers ----------------------------------------------------------

func (s *gameScreen) mover() Seat { return s.cfg.Seats[s.g.Turn()] }

func (s *gameScreen) hotseat() bool {
	return s.cfg.Seats[game.Vertical].Human() && s.cfg.Seats[game.Horizontal].Human()
}

// actingSide is the side a resignation or a draw offer is made for. With one
// player at the keyboard it is always their side, on turn or not, because
// either may be done while the opponent is thinking. In a hotseat game it is
// the side to move: the other player is not at the keyboard.
func (s *gameScreen) actingSide() (game.Player, bool) {
	if side, only := s.cfg.LocalSide(); only {
		return side, true
	}
	if s.mover().Human() {
		return s.g.Turn(), true
	}
	return game.NoPlayer, false
}

func (s *gameScreen) seatName(side game.Player) string {
	seat := s.cfg.Seats[side]
	switch {
	case seat.Label != "":
		return seat.Label
	case seat.Profile != "":
		return seat.Profile
	case seat.Bot != nil:
		return seat.Bot.Tier().String() + " engine"
	case seat.Remote:
		return s.opponentName()
	}
	return side.String()
}

// rulesLine names the ruleset and the board the way a player would say it,
// rather than as the two bare tokens the transcript format uses.
func (s *gameScreen) rulesLine() string {
	rs := s.g.Rules()
	name := rs.PresetName()
	if name == "" {
		name = "custom"
	}
	return fmt.Sprintf("%s rules · %dx%d", name, rs.Size, rs.Size)
}

// rulesName is the bare preset name, for anywhere a short token is wanted.
func (s *gameScreen) rulesName() string {
	rs := s.g.Rules()
	if name := rs.PresetName(); name != "" {
		return name
	}
	return "custom"
}

func (s *gameScreen) pegGlyph(side game.Player) string {
	if side == game.Horizontal {
		return gsPegHorizontal
	}
	return gsPegVertical
}

func (s *gameScreen) pegStyle(side game.Player) lipgloss.Style {
	if side == game.Horizontal {
		return s.styles.PegHorizontal
	}
	return s.styles.PegVertical
}

// keyLabel is the label of a board action, read from the keymap so that help
// text and the real bindings cannot drift apart.
func (s *gameScreen) keyLabel(a ui.Action) string {
	ctx := ui.CtxBoard
	if s.linkMode {
		ctx = ui.CtxLink
	}
	if b, ok := s.keymap.ByAction(ctx, a); ok {
		return b.Label
	}
	if b, ok := s.keymap.ByAction(ui.CtxBoard, a); ok {
		return b.Label
	}
	return ""
}

func (s *gameScreen) gameKeyLabel(a gameAction) string {
	for _, b := range gameBindings {
		if b.action == a {
			return b.label
		}
	}
	return ""
}

// gsAxisText names the pair of borders a side is joining, short enough to sit on
// the seat's own line even in a narrow panel.
func gsAxisText(side game.Player) string {
	if side == game.Horizontal {
		return "left-right"
	}
	return "top-bottom"
}

// gsBorderWord names the kind of line a side's two borders are. Vertical joins
// the top and bottom rows, horizontal the left and right columns, so a refusal
// that calls every border a row is wrong half the time — and wrong exactly for
// the player being refused, since the borders nobody else may enter are the
// opponent's. The engine's own sentinel is worded for rows; this is the reading
// a player at the board needs.
func gsBorderWord(side game.Player) string {
	if side == game.Horizontal {
		return "column"
	}
	return "row"
}

func gsLinkNames(from game.Point, mask uint8) []string {
	if mask == 0 {
		return nil
	}
	names := make([]string, 0, bits.OnesCount8(mask))
	for dir := game.Dir(0); dir < game.NumDirs; dir++ {
		if mask&(1<<dir) == 0 {
			continue
		}
		if l, ok := game.NewLink(from, from.Add(dir)); ok {
			names = append(names, l.String())
		}
	}
	return names
}

func gsLinkList(links []game.Link) string {
	names := make([]string, len(links))
	for i, l := range links {
		names[i] = l.Canonical().String()
	}
	return strings.Join(names, " ")
}

func gsClamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func gsSign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func gsPad(text string, width int) string {
	if n := ansi.StringWidth(text); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text
}

// gsTruncate and gsWrap are the panel's names for the package's one truncator
// and wrapper. The truncator marks what it cut: a hard clip reads as though the
// product mangled the line, where a marked one reads as a shortened line.
func gsTruncate(text string, width int) string {
	return truncateText(text, width)
}

func gsWrap(text string, width int) []string {
	return wrapText(strings.TrimSpace(text), width)
}

// gsPlain strips styling so a styled fragment can be folded into a line that is
// styled again as a whole.
func gsPlain(text string) string { return ansi.Strip(text) }

func (s *gameScreen) style(st lipgloss.Style, text string) string {
	if s.styles == nil || s.styles.Plain || text == "" {
		return text
	}
	return st.Render(text)
}

// serialBot serialises calls to one engine.
//
// bot.Bot is documented as not safe for concurrent use: it carries the working
// state of its search. The screen runs searches in commands, on their own
// goroutines, and the same engine may be asked for a move and for a hint, so the
// calls have to take turns. Tier is a constant of the engine and is read while
// drawing, so it does not wait.
type serialBot struct {
	lock   *sync.Mutex
	engine bot.Bot
}

func (s *serialBot) Tier() bot.Tier { return s.engine.Tier() }

func (s *serialBot) Move(ctx context.Context, g *game.Game) (game.Point, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.engine.Move(ctx, g)
}

func (s *serialBot) Hint(ctx context.Context, g *game.Game) (bot.Hint, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.engine.Hint(ctx, g)
}
