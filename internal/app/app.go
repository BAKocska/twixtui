// Package app is the interactive shell: the screens a player moves between and
// the state they share. The view layer in internal/ui draws; this package
// decides what is drawn and what the keys do.
package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/theme"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Deps are the collaborators every screen needs. It is passed by value; the
// stores inside it are shared and safe for concurrent use.
type Deps struct {
	// ConfigDir is where profiles, games and settings live.
	ConfigDir string

	Profiles *profile.Store
	Board    *leaderboard.Board
	Games    *gamestore.Store

	Theme  theme.Theme
	Styles *ui.Styles
	Keymap ui.Keymap

	// Now is the clock, injected so that a test can pin the durations that end
	// up on the leaderboard.
	Now func() time.Time

	// Note lets a screen leave a line for the player to read after the
	// interface has closed. Anything printed while the alternate screen is up
	// disappears with it, so a screen that saved a game on the way out has
	// nowhere to say so: the player was left not knowing whether their game
	// survived. Nil means nobody is listening.
	Note func(string)
}

// note leaves a line for the player to read after the interface closes.
func (d Deps) note(format string, args ...any) {
	if d.Note == nil {
		return
	}
	d.Note(fmt.Sprintf(format, args...))
}

// Clock returns the time, defaulting to the real one.
func (d Deps) Clock() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Screen is one place the player can be. Screens are Bubble Tea models; they
// hand control back by emitting a DoneMsg rather than by returning tea.Quit, so
// that the shell decides what happens next.
type Screen interface {
	tea.Model
}

// DoneMsg asks the shell to leave the screen that sent it.
//
// A screen that wants to be replaced sets Next. A screen that wants to go back
// to where it came from leaves Next nil. A screen that failed sets Err, which
// the shell shows before going back. Quit ends the program.
type DoneMsg struct {
	Next Screen
	Err  error
	Quit bool
}

// Done is a command that emits a DoneMsg, which is what a screen returns from
// Update when it is finished.
func Done(msg DoneMsg) tea.Cmd {
	return func() tea.Msg { return msg }
}

// Back leaves the current screen and returns to the previous one.
func Back() tea.Cmd { return Done(DoneMsg{}) }

// Fail leaves the current screen and reports why.
func Fail(err error) tea.Cmd { return Done(DoneMsg{Err: err}) }

// Replace swaps the current screen for another. The screen asking is finished
// and will not be returned to.
func Replace(s Screen) tea.Cmd { return Done(DoneMsg{Next: s}) }

// OpenMsg asks the shell to show another screen on top of the one that sent it,
// which stays underneath and is returned to when the new one is finished.
type OpenMsg struct {
	Screen Screen
}

// Open shows another screen without giving up this one.
//
// This is the difference between a menu that launches a game and a menu that is
// consumed by launching one. With Replace, leaving the game emptied the stack and
// ended the program, so there was no way back to the menu the player had started
// from.
func Open(s Screen) tea.Cmd {
	return func() tea.Msg { return OpenMsg{Screen: s} }
}

// Quit ends the program from anywhere.
func Quit() tea.Cmd { return Done(DoneMsg{Quit: true}) }

// Seat says who is playing one side of a game.
type Seat struct {
	// Profile is the local player's name, empty for a bot or a remote opponent.
	Profile string
	// Bot is the opponent engine, nil unless this seat is a bot.
	Bot bot.Bot
	// Remote marks a seat driven by a networked opponent.
	Remote bool
	// Label is what the interface calls this seat.
	Label string
}

// Human reports whether this seat is played at this keyboard.
func (s Seat) Human() bool { return s.Bot == nil && !s.Remote }

// GameConfig describes a game to be played. The command line builds one of
// these and hands it to NewGameScreen.
type GameConfig struct {
	Kind  gamestore.Kind
	Rules game.Ruleset

	// Seats says who plays each side. Both sides must be filled.
	Seats map[game.Player]Seat

	// Hints allows the player to ask for advice, which only makes sense when
	// there is an engine to ask.
	Hints bool
	// HintFor is the engine consulted for hints, which is the opponent bot in a
	// game against one and may be a separate engine otherwise.
	HintFor bot.Bot

	// Session is the network connection for a live remote game, nil otherwise.
	Session netplay.Session

	// Codes drives a remote seat by move codes the players exchange by hand,
	// with no connection at all. It is mutually exclusive with Session: a
	// correspondence game has a remote seat and no session, which is the one
	// case where that combination is legitimate.
	Codes bool

	// Resume, when set, continues a stored game instead of starting a new one.
	Resume *gamestore.Saved
	// StoreID is the identifier the game is saved under, and for a
	// correspondence game it is also the identifier its move codes are bound
	// to, so a code from another game is refused. Empty means allocate one.
	StoreID string
}

// LocalSide returns the side the player at this keyboard is on, and whether
// exactly one side is local. In a hotseat game both sides are local, so this
// reports false.
func (c GameConfig) LocalSide() (game.Player, bool) {
	var found game.Player
	n := 0
	for side, seat := range c.Seats {
		if seat.Human() {
			found = side
			n++
		}
	}
	return found, n == 1
}

// Opponent returns the leaderboard name for the side opposing the local player.
func (c GameConfig) Opponent(local game.Player) string {
	seat := c.Seats[local.Opponent()]
	switch {
	case seat.Bot != nil:
		return leaderboard.BotName(seat.Bot.Tier().String())
	case seat.Remote:
		return leaderboard.RemoteName(seat.Label)
	default:
		return seat.Profile
	}
}
