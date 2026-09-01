package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// The shell answers the global quit key itself, so that a busy screen cannot
// trap the player. That meant the key never reached the screen, and a screen's
// own handling of it was dead code: leaving a game with the plain letter saved
// the unfinished game while leaving with the control key discarded it. These
// tests pin the protocol that fixed it.

// departingScreen records whether it was allowed to finish.
type departingScreen struct {
	name      string
	departed  *int
	departLog *[]string
}

func (d departingScreen) Init() tea.Cmd { return nil }

func (d departingScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return d, nil }

func (d departingScreen) View() tea.View { return tea.NewView(d.name) }

func (d departingScreen) Depart() {
	*d.departed++
	*d.departLog = append(*d.departLog, d.name)
}

// plainScreen does not implement Departing, which must not stop the shell.
type plainScreen struct{}

func (plainScreen) Init() tea.Cmd                       { return nil }
func (plainScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return plainScreen{}, nil }
func (plainScreen) View() tea.View                      { return tea.NewView("plain") }

func TestGlobalQuitKeyLetsTheScreenFinish(t *testing.T) {
	count := 0
	var order []string
	shell := NewShell(shellTestDeps(t), departingScreen{name: "game", departed: &count, departLog: &order})

	_, cmd := shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("the global quit key produced no command")
	}
	if count != 1 {
		t.Fatalf("Depart was called %d times, want 1", count)
	}
	if msg := cmd(); msg == nil {
		t.Error("the quit command produced no message")
	}
}

// TestQuitFinishesEveryScreenInnermostFirst matters because a game can sit under
// another screen, and the game is the one with something to save.
func TestQuitFinishesEveryScreenInnermostFirst(t *testing.T) {
	count := 0
	var order []string
	shell := NewShell(shellTestDeps(t), departingScreen{name: "outer", departed: &count, departLog: &order})
	shell.Push(departingScreen{name: "inner", departed: &count, departLog: &order})

	shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if count != 2 {
		t.Fatalf("Depart was called %d times, want 2", count)
	}
	if len(order) != 2 || order[0] != "inner" || order[1] != "outer" {
		t.Errorf("screens finished in order %v, want inner then outer", order)
	}
}

// TestQuitByMessageAlsoFinishesTheScreen covers the other way the program ends:
// a screen asking for it rather than a key.
func TestQuitByMessageAlsoFinishesTheScreen(t *testing.T) {
	count := 0
	var order []string
	shell := NewShell(shellTestDeps(t), departingScreen{name: "game", departed: &count, departLog: &order})

	_, cmd := shell.Update(DoneMsg{Quit: true})
	if cmd == nil {
		t.Fatal("a quit message produced no command")
	}
	if count != 1 {
		t.Errorf("Depart was called %d times, want 1", count)
	}
}

// TestDepartIsNotCalledTwice checks the shell does not finish a screen that has
// already gone, which would double-save or double-close a connection.
func TestDepartIsNotCalledTwice(t *testing.T) {
	count := 0
	var order []string
	shell := NewShell(shellTestDeps(t), departingScreen{name: "game", departed: &count, departLog: &order})

	shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if count != 1 {
		t.Errorf("Depart was called %d times across two quit keys, want 1", count)
	}
}

func TestQuitWorksWithoutDeparting(t *testing.T) {
	shell := NewShell(shellTestDeps(t), plainScreen{})
	_, cmd := shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("a screen that does not implement Departing broke the quit path")
	}
}

// TestGameScreenImplementsDeparting is the connection between the protocol and
// the bug it fixed. If the game screen ever stops implementing it, an unfinished
// game is silently lost again on the control-key path, and no other test in the
// package would notice.
func TestGameScreenImplementsDeparting(t *testing.T) {
	screen, err := NewGameScreen(gsTestDeps(t), gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := screen.(Departing); !ok {
		t.Error("the game screen no longer implements Departing, so quitting will discard an unfinished game")
	}
}

// TestLeavingAnUnfinishedGameSaysSo covers the other half of the quit problem.
// The game was saved but nothing told the player, and the interface cannot tell
// them itself: anything drawn on the way out goes with the alternate screen. So
// the screen leaves a note for the command line to print afterwards.
func TestLeavingAnUnfinishedGameSaysSo(t *testing.T) {
	var notes []string
	d := gsTestDeps(t)
	d.Note = func(line string) { notes = append(notes, line) }

	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	shell := NewShell(d, screen)
	shell.Update(tea.WindowSizeMsg{Width: 90, Height: 28})

	// Play a move so there is something worth saving.
	shell.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	shell.Update(tea.KeyPressMsg{Code: 'e', Text: "enter"})

	shell.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if len(notes) == 0 {
		t.Fatal("leaving an unfinished game left no note, so the player is not told it was saved")
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "saved") {
		t.Errorf("the note does not say the game was saved: %q", joined)
	}
	// It must name the game, or the player cannot find it again.
	saved := d.Games.List()
	if len(saved) == 0 {
		t.Fatal("no game was actually stored")
	}
	if !strings.Contains(joined, saved[0].ID) {
		t.Errorf("the note does not name the saved game %s: %q", saved[0].ID, joined)
	}
}

// TestAFinishedGameLeavesNoSaveNote checks the note is about the game being
// picked up again, so a game that has ended does not offer to resume itself.
func TestAFinishedGameLeavesNoSaveNote(t *testing.T) {
	var notes []string
	d := gsTestDeps(t)
	d.Note = func(line string) { notes = append(notes, line) }

	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	gs, ok := screen.(*gameScreen)
	if !ok {
		t.Fatalf("unexpected screen type %T", screen)
	}
	if err := gs.g.Resign(gs.g.Turn()); err != nil {
		t.Fatal(err)
	}
	gs.Depart()

	for _, line := range notes {
		if strings.Contains(line, "pick it up") || strings.Contains(line, "saved as") {
			t.Errorf("a finished game offered to be resumed: %q", line)
		}
	}
}

// TestLeavingAGameOpenedFromTheMenuComesBack is the navigation half of the same
// complaint the save note answers. A menu that replaced itself with the game left
// nowhere to go back to, so leaving the game ended the whole program: no rematch,
// no other opponent, no leaderboard, quit and start again.
func TestLeavingAGameOpenedFromTheMenuComesBack(t *testing.T) {
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("ada"); err != nil {
		t.Fatal(err)
	}
	menu := NewMenu(d, "ada")
	shell := NewShell(d, menu)
	shell.Update(tea.WindowSizeMsg{Width: 90, Height: 28})

	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	// The menu opens a game the way it does in the product.
	if cmd := shell.Push(screen); cmd != nil {
		cmd()
	}
	if got := len(shell.stack); got != 2 {
		t.Fatalf("the stack is %d deep after opening a game from the menu, want 2", got)
	}

	// Leaving the game must come back to the menu rather than end the program.
	_, cmd := shell.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			shell.Update(msg)
		}
	}
	if got := len(shell.stack); got != 1 {
		t.Fatalf("the stack is %d deep after leaving the game, want 1", got)
	}
	if _, ok := shell.top().(*Menu); !ok {
		t.Errorf("leaving the game landed on %T, want the menu", shell.top())
	}
}

// TestLeavingTheOnlyScreenEndsTheProgram is the other side of it: a game started
// straight from the command line has nothing behind it, so leaving it should end
// the run, which is what makes the save note the only thing that tells the player
// where their game went.
func TestLeavingTheOnlyScreenEndsTheProgram(t *testing.T) {
	d := gsTestDeps(t)
	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	shell := NewShell(d, screen)
	shell.Update(tea.WindowSizeMsg{Width: 90, Height: 28})

	_, cmd := shell.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("leaving the only screen produced no command")
	}
	msg := cmd()
	done, ok := msg.(DoneMsg)
	if !ok {
		t.Fatalf("leaving the only screen produced %T, want a DoneMsg", msg)
	}
	if _, quit := shell.Update(done); quit == nil {
		t.Error("leaving the only screen did not end the program")
	}
}

// TestAFinishedGameIsNotOfferedAgain reproduces a sequence a reviewer found by
// playing: open the saved-game list, resume a game, finish it by resignation,
// leave, and the list underneath still offers the game it had read before the
// resignation. Choosing it resumed the finished game at the position it held
// before the result, with the player who had just resigned back on the move, and
// leaving again wrote that position over the finished record. The recorded result
// was lost, and where the game had already been rated the rating log and the
// stored game disagreed.
func TestAFinishedGameIsNotOfferedAgain(t *testing.T) {
	d := shellTestDeps(t)
	for _, name := range []string{"Ann", "Ben"} {
		if _, err := d.Profiles.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	saved := mnSaveGame(t, d, gamestore.Hotseat, "Ann", "Ben")

	menu := NewMenu(d, "Ann")
	shell := NewShell(d, menu)
	shell.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The list is built from the store as it stands now.
	if cmd := menu.openSaved(); cmd != nil {
		cmd()
	}
	if _, ok := menu.form.(*chooser); !ok {
		t.Fatalf("the saved-game list did not open: %T", menu.form)
	}

	// The game is finished while a screen sits on top of the menu.
	done, err := d.Games.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	done.Finished = true
	if err := d.Games.Put(done); err != nil {
		t.Fatal(err)
	}

	// Coming back to the menu must not redisplay the list it read earlier.
	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	if cmd := shell.Push(screen); cmd != nil {
		cmd()
	}
	if cmd := shell.leave(DoneMsg{}); cmd != nil {
		cmd()
	}
	if menu.form != nil {
		t.Errorf("the menu is still showing the panel it built before the game ran: %T", menu.form)
	}

	// Even if something did hand the stale row back, resuming must refuse it.
	if _, err := menu.resumeConfig(saved); err == nil {
		t.Error("a finished game was accepted for resumption")
	} else if !strings.Contains(err.Error(), "over") {
		t.Errorf("refusal does not say the game is over: %v", err)
	}

	// And the record must still say the game finished.
	back, err := d.Games.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Finished {
		t.Error("the finished game was reopened in the store")
	}
}

// TestTheGameIsStoredAsItIsPlayed pins the promise that games are saved as they
// are played. Saving used to happen only on the way out of the screen, so a game
// in progress existed nowhere but in memory: closing the terminal window or
// killing the process lost it. Verified against the real binary too, with kill -9
// mid-game, after which the game was still listed at the right position.
func TestTheGameIsStoredAsItIsPlayed(t *testing.T) {
	d := shellTestDeps(t)
	screen, err := NewGameScreen(d, gsHotseat(12))
	if err != nil {
		t.Fatal(err)
	}
	screen.Update(tea.WindowSizeMsg{Width: 90, Height: 28})

	if got := len(d.Games.List()); got != 0 {
		t.Fatalf("%d games stored before anything was played", got)
	}

	// Play a peg and commit it through the keys, so the autosave sees exactly
	// what a player's turn produces.
	gs := screen.(*gameScreen)
	gsCommitAt(t, gs, game.Point{Col: 5, Row: 5})

	stored := d.Games.List()
	if len(stored) != 1 {
		t.Fatalf("%d games stored after one move, want 1", len(stored))
	}
	g, err := stored[0].Game()
	if err != nil {
		t.Fatalf("the stored game does not load: %v", err)
	}
	if g.Ply() != 1 {
		t.Errorf("the stored game holds %d moves, want 1", g.Ply())
	}
	if stored[0].Finished {
		t.Error("a game in progress was stored as finished")
	}

	// A second move updates the same game rather than adding another.
	gsCommitAt(t, gs, game.Point{Col: 6, Row: 7})
	stored = d.Games.List()
	if len(stored) != 1 {
		t.Fatalf("%d games stored after two moves, want 1", len(stored))
	}
	if g, err := stored[0].Game(); err != nil {
		t.Fatalf("the stored game does not load: %v", err)
	} else if g.Ply() != 2 {
		t.Errorf("the stored game holds %d moves, want 2", g.Ply())
	}
}

// gsCommitAt moves the cursor to a hole, places a peg and commits the turn.
func gsCommitAt(t *testing.T, s *gameScreen, p game.Point) {
	t.Helper()
	// Hotseat puts a handover between turns so the next player can take the
	// keyboard; clear it before playing.
	if s.handover {
		if cmd := s.handleKey(gsKeyMsg(t, "enter")); cmd != nil {
			cmd()
		}
	}
	s.board.Cursor = p
	for _, key := range []string{"space", "enter"} {
		if cmd := s.handleKey(gsKeyMsg(t, key)); cmd != nil {
			cmd()
		}
		s.autosave()
	}
}
