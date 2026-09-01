package app

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// Correspondence play is the game screen with nothing behind the opponent's
// seat: no socket, no relay, no clock. The two players exchange short codes
// through whatever they already use to talk. netplay owns the codes; this file
// owns the exchange, which is the part a player touches.
//
// Two decisions here are worth stating, because both are why the mode works at
// all rather than merely compiling.
//
// The exchange is drawn instead of the board, not beside it. A code is no use
// unless it can be copied in one piece, and the ordinary frame has nowhere to
// put one: the panel is at most 36 columns wide and ui.Compose clips every line
// to the terminal, while a move code is about fifty characters. So the code goes
// on a line of its own, with nothing else on it and no shortening, and the
// layout below budgets the rows the terminal will need to wrap it into.
//
// Every entry this end adds to the record becomes a code — a committed turn, a
// swap, a draw offer, its acceptance, a resignation — because the opponent's
// copy counts entries and refuses one that skips a step. Codes are held until a
// code arrives from the opponent, which is proof they had ours: netplay refuses
// a code whose entry count does not match the record it is applied to, so a
// reply can only exist if everything before it went in.

// correspondence is the state of the exchange for one game.
type correspondence struct {
	// id is the identifier the codes are bound to, which is also the key the
	// game is stored under, so a code made for another game is refused.
	id string
	// remote is the seat the codes come from and go to.
	remote game.Player

	// open shows the exchange in place of the board.
	open bool
	// edit is where the opponent's code is pasted.
	edit lineEdit
	// pending are the codes this end has produced that the opponent has not
	// yet shown any sign of having. There is more than one when a draw offer
	// and the move made with it standing are both waiting.
	pending []pendingCode
	// note is what became of the last code, produced or pasted. It stays until
	// the next one, because the player leaves the program to go and paste a
	// code somewhere else and comes back to this.
	note string
}

// pendingCode is one code waiting to be sent, with the move it carries so the
// player can see what they are sending.
type pendingCode struct {
	move string
	code string
}

func newCorrespondence(id string, remote game.Player) *correspondence {
	return &correspondence{id: id, remote: remote}
}

// produce turns the entry this end has just added into the code the opponent
// needs.
func (c *correspondence) produce(g *game.Game) error {
	code, err := netplay.EncodeLastMove(g, c.id)
	if err != nil {
		return err
	}
	move, err := g.MoveNotation(g.Entries() - 1)
	if err != nil {
		return err
	}
	c.pending = append(c.pending, pendingCode{move: move, code: code})
	c.note = "your code is below — send it to your opponent"
	return nil
}

// resend rebuilds the codes for the entries at the end of the record that this
// end made, which are the ones the opponent cannot have seen.
//
// Closing the program between turns is the normal way this mode is played, and
// the codes live only in memory, so without this a player who shut the terminal
// before copying their code would have no way to produce it again and the game
// would stall with neither end able to move it on.
//
// The trailing run is exactly what is outstanding: a code from the opponent adds
// an entry of theirs, so everything after their last entry is ours and
// unanswered.
func (c *correspondence) resend(g *game.Game) error {
	history := g.History()
	outstanding := 0
	for i := len(history) - 1; i >= 0 && history[i].Player != c.remote; i-- {
		outstanding++
	}
	if outstanding == 0 {
		return nil
	}
	entries := make([]netplay.Entry, len(history))
	for i := range history {
		notation, err := g.MoveNotation(i)
		if err != nil {
			return err
		}
		entries[i] = netplay.Entry{Side: history[i].Player, Move: notation}
	}
	// netplay writes a whole game as one code per line, which is the only way
	// to ask it for the code of an entry that is not the last one.
	block, err := netplay.EncodeTranscript(c.id, g.Rules(), entries)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	if len(lines) != len(entries) {
		return fmt.Errorf("the record has %d entries but produced %d codes", len(entries), len(lines))
	}
	for i := len(entries) - outstanding; i < len(entries); i++ {
		c.pending = append(c.pending, pendingCode{move: entries[i].Move, code: lines[i]})
	}
	c.note = "your opponent has not answered this yet — send it if you have not already"
	return nil
}

// apply plays a pasted code onto g and reports what it played. A refusal comes
// back already worded for the player.
func (c *correspondence) apply(g *game.Game, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("there is no code here yet: paste the one your opponent sent you")
	}
	if _, err := netplay.ApplyMove(g, c.id, text); err != nil {
		return "", errors.New(c.refusal(g, text, err))
	}
	// A code fits a record of exactly the length it was made against, so the
	// opponent had every code this end has produced. Holding them any longer
	// would show the player something to send twice.
	c.pending = c.pending[:0]
	// What is shown is the engine's own notation for the entry, not the text
	// the code carried. netplay had to find that text playable, which is a
	// weaker thing than fit to print: it is bytes out of a pasted string, and
	// the screen is the one place they must not arrive raw.
	move := "their move"
	if canonical, err := g.MoveNotation(g.Entries() - 1); err == nil {
		move = canonical
	}
	return move, nil
}

// refusal says which kind of wrong a refused code is, keeping netplay's own
// words for the detail.
//
// The categories cannot be told apart with errors.Is: netplay wraps nearly all
// of them in ErrBadCode, deliberately, because to the protocol they are one
// thing. To a player they are four different things to do — ask for the code
// again, look for the right game, do nothing because it is already played, or
// find the code that went missing — so the category is worked out here from
// what the code itself says.
func (c *correspondence) refusal(g *game.Game, text string, err error) string {
	mc, readErr := netplay.Inspect(text)
	switch {
	case readErr != nil:
		return "that code did not arrive intact: " + readErr.Error()
	case mc.Game != netplay.GameDigest(c.id):
		return "that code belongs to a different game: " + err.Error()
	case mc.Entries < g.Entries():
		return "that code has already been applied: " + err.Error()
	case mc.Entries > g.Entries():
		return "an earlier code is missing: " + err.Error()
	case !strings.HasPrefix(netplay.PositionHash(g), mc.Before):
		return "that code was made for a different position: " + err.Error()
	}
	return "that code does not fit this game: " + err.Error()
}

// paste puts pasted text into the field. Whitespace is collapsed rather than
// kept: a code copied out of a chat arrives with a trailing newline, and one
// the chat folded arrives with a newline in the middle, and neither should turn
// a one-line field into two. netplay ignores the spaces that are left.
func (c *correspondence) paste(text string) {
	if fields := strings.Fields(text); len(fields) > 0 {
		c.edit.insert(strings.Join(fields, " "))
	}
}

// --- the game screen's side of it -------------------------------------------

// codeForLastEntry turns the entry this end has just added into a code and puts
// the game on disk. It reports whether play can carry on.
//
// The save is here rather than only on the way out because this is how a
// correspondence game is played: open it, make one move, close it. A move that
// produced a code the opponent can apply but that this end never wrote down is
// the one failure the mode cannot recover from.
func (s *gameScreen) codeForLastEntry(what string) bool {
	if s.corr == nil {
		return true
	}
	if err := s.corr.produce(s.g); err != nil {
		s.stop("this " + what + " could not be turned into a code to send: " + err.Error())
		return false
	}
	s.corr.open = true
	if !s.g.Result().Over() {
		// A finished game is saved by finish, with its result.
		if err := s.save(false); err != nil {
			s.corr.note = "the game was not saved: " + err.Error()
		}
	}
	return true
}

// exchangeKey is the keyboard while the exchange is showing. The field owns
// every text key, including the letters the board uses, because a player who is
// pasting a code is not playing.
func (s *gameScreen) exchangeKey(m tea.KeyPressMsg) tea.Cmd {
	over := s.g.Result().Over()
	switch m.String() {
	case "esc":
		s.corr.open = false
		return nil
	case "enter":
		if over || strings.TrimSpace(s.corr.edit.value()) == "" {
			// Nothing to apply, so enter is the way back to the board, which is
			// what the player wants after copying their own code out.
			s.corr.open = false
			return nil
		}
		return s.applyPastedCode()
	}
	if over {
		// The field is not drawn once the game is over, and a field that took
		// text it could only refuse would be a lie about what is left to do.
		return nil
	}
	s.corr.edit.key(m)
	return nil
}

// applyPastedCode plays the opponent's code, saves, and goes back to the board.
func (s *gameScreen) applyPastedCode() tea.Cmd {
	move, err := s.corr.apply(s.g, s.corr.edit.value())
	if err != nil {
		s.corr.note = err.Error()
		return nil
	}
	s.corr.edit.setValue("")
	s.hint.clear()
	s.corr.note = fmt.Sprintf("applied %s from %s", move, s.opponentName())
	s.corr.open = false
	s.message = fmt.Sprintf("%s: %s — %s", s.opponentName(), move, s.toMoveText())
	if s.g.Result().Over() {
		return s.finish()
	}
	if err := s.save(false); err != nil {
		s.corr.note = "the game was not saved: " + err.Error()
	}
	return nil
}

// corrPanelLine is the one line the ordinary panel gives correspondence play:
// what the exchange is waiting for, and the key that opens it.
func (s *gameScreen) corrPanelLine() string {
	key := s.gameKeyLabel(gaCode)
	switch {
	case s.g.Result().Over():
		return key + " the exchange"
	case len(s.corr.pending) == 1:
		return key + " a code to send"
	case len(s.corr.pending) > 1:
		return fmt.Sprintf("%s %d codes to send", key, len(s.corr.pending))
	case s.g.Turn() == s.corr.remote:
		return key + " paste their code"
	}
	return key + " the exchange"
}

// --- drawing the exchange ---------------------------------------------------

// exchangeLine is one line of the exchange.
type exchangeLine struct {
	text string
	// whole marks a line that must not be shortened. A code with a character
	// missing is worse than no code at all, so it is emitted at its full length
	// and the terminal wraps it if it must; rows accounts for what that costs.
	whole bool
}

// rows is how many terminal rows the line occupies at this width.
func (l exchangeLine) rows(width int) int {
	if !l.whole || width <= 0 {
		return 1
	}
	w := ansi.StringWidth(l.text)
	if w <= width {
		return 1
	}
	return (w + width - 1) / width
}

// exchangeFrame draws the exchange in place of the board, with the status line
// pinned to the bottom row as every other frame has it.
func (s *gameScreen) exchangeFrame(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	body := make([]string, 0, height)
	used := 0
	for _, l := range s.exchangeLines(width) {
		cost := l.rows(width)
		if used+cost > height-1 {
			break
		}
		text := l.text
		if !l.whole {
			text = gsTruncate(text, width)
		}
		body = append(body, text)
		used += cost
	}
	for used < height-1 {
		body = append(body, "")
		used++
	}
	body = append(body, s.style(s.styles.Status, gsTruncate(s.exchangeStatus(), width)))
	return strings.Join(body, "\n")
}

// exchangeLines is the content of the exchange, most urgent first, so that a
// short terminal keeps the code and loses the explanation rather than the other
// way round.
func (s *gameScreen) exchangeLines(width int) []exchangeLine {
	c := s.corr
	var out []exchangeLine
	add := func(text string) { out = append(out, exchangeLine{text: text}) }
	wrap := func(text string) {
		for _, l := range gsWrap(text, width) {
			add(l)
		}
	}
	blank := func() {
		if len(out) > 0 {
			add("")
		}
	}

	add(s.style(s.styles.PanelTitle, "correspondence · game "+c.id))
	blank()

	switch {
	case len(c.pending) == 1:
		wrap(fmt.Sprintf("your %s — send this code to %s:", c.pending[0].move, s.opponentName()))
	case len(c.pending) > 1:
		wrap(fmt.Sprintf("send these %d codes to %s, in this order:", len(c.pending), s.opponentName()))
	case s.g.Result().Over():
		wrap("the game is over: there is nothing left to send")
	case s.g.Turn() == c.remote:
		wrap(fmt.Sprintf("nothing to send: it is %s's move", s.opponentName()))
	default:
		wrap("nothing to send yet: make your move on the board and its code appears here")
	}
	for _, p := range c.pending {
		add("")
		out = append(out, exchangeLine{text: p.code, whole: true})
	}

	if !s.g.Result().Over() {
		blank()
		wrap(fmt.Sprintf("paste %s's code here:", s.opponentName()))
		add(c.edit.render(s.styles, width))
	}
	if c.note != "" {
		blank()
		for _, l := range gsWrap(s.style(s.styles.Message, c.note), width) {
			add(l)
		}
	}
	return out
}

func (s *gameScreen) exchangeStatus() string {
	if s.g.Result().Over() {
		return "esc back to the board"
	}
	return "enter apply · esc back to the board · ctrl+u clear"
}
