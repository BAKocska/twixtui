package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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
