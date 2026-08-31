package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/theme"
	"github.com/BAKocska/twixtui/internal/ui"
)

// shellPlainStyles keeps the frames in these tests free of escape sequences, so
// an assertion about a line's width is an assertion about what is on screen.
var shellPlainStyles = ui.PlainStyles()

// shellKeyPress builds the message a real terminal produces for a key name, and
// is checked against Bubble Tea's own encoding by TestKeyNamesEncodeAsExpected.
// Every key these screens bind goes through here, so a binding written against
// a name the terminal never produces cannot pass.
func shellKeyPress(s string) tea.KeyPressMsg {
	var k tea.Key
	switch s {
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	case "up":
		k = tea.Key{Code: tea.KeyUp}
	case "down":
		k = tea.Key{Code: tea.KeyDown}
	case "space":
		k = tea.Key{Code: tea.KeySpace, Text: " "}
	case "backspace":
		k = tea.Key{Code: tea.KeyBackspace}
	case "tab":
		k = tea.Key{Code: tea.KeyTab}
	case "pgup":
		k = tea.Key{Code: tea.KeyPgUp}
	case "pgdown":
		k = tea.Key{Code: tea.KeyPgDown}
	default:
		if rest, ok := strings.CutPrefix(s, "ctrl+"); ok && len([]rune(rest)) == 1 {
			k = tea.Key{Code: []rune(rest)[0], Mod: tea.ModCtrl}
			break
		}
		r := []rune(s)
		if len(r) != 1 {
			panic("shellKeyPress cannot encode " + s)
		}
		k = tea.Key{Code: r[0], Text: s}
	}
	return tea.KeyPressMsg(k)
}

// TestKeyNamesEncodeAsExpected pins the key names the screens dispatch on to
// the strings Bubble Tea actually reports. Without this, every other key test
// would be checking the test helper against itself.
func TestKeyNamesEncodeAsExpected(t *testing.T) {
	for _, name := range []string{
		"a", "Z", "0", "enter", "esc", "up", "down", "space", "backspace", "tab",
		"pgup", "pgdown", "ctrl+c", "ctrl+p", "ctrl+n", "ctrl+u", "ctrl+w",
	} {
		if got := shellKeyPress(name).String(); got != name {
			t.Errorf("key %q encodes as %q", name, got)
		}
	}
}

// shellSend delivers a key to a model, checking the encoding on the way.
func shellSend(t *testing.T, m tea.Model, key string) tea.Cmd {
	t.Helper()
	press := shellKeyPress(key)
	if got := press.String(); got != key {
		t.Fatalf("key %q encodes as %q", key, got)
	}
	_, cmd := m.Update(press)
	return cmd
}

// shellTestDeps builds collaborators backed by a temporary directory, so a test
// never touches the state of whoever runs it.
func shellTestDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	profiles, err := profile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	board, err := leaderboard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	games, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	th, err := theme.Get(theme.Default)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		ConfigDir: dir,
		Profiles:  profiles,
		Board:     board,
		Games:     games,
		Theme:     th,
		Styles:    &shellPlainStyles,
		Keymap:    ui.DefaultKeymap(),
		Now:       func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) },
	}
}

// shellAssertFits is the invariant every frame must satisfy at every terminal
// size: no line wider than the terminal, and no more lines than it has rows. A
// frame that breaks it corrupts the display, which is the failure R3 is about.
func shellAssertFits(t *testing.T, what, frame string, width, height int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if frame == "" {
		lines = nil
	}
	if len(lines) > height {
		t.Errorf("%s at %dx%d: %d lines, want at most %d", what, width, height, len(lines), height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > width {
			t.Errorf("%s at %dx%d: line %d is %d cells wide: %q", what, width, height, i, w, l)
		}
	}
}

// shellSizes are the terminal sizes every screen is required to survive: the
// documented minimum, a small pane, the conventional default, and a wide one.
var shellSizes = [][2]int{{20, 8}, {40, 12}, {80, 24}, {200, 60}}

// shellProbe is a screen that records what the shell sent it, so a test can
// assert the shell really routed a message instead of merely not crashing.
type shellProbe struct {
	name    string
	width   int
	height  int
	resizes int
	keys    []string

	// done, when set, is emitted the next time a key arrives: that is how a
	// test makes a screen finish.
	done *DoneMsg
	// deaf stands in for a screen that is busy and answers nothing.
	deaf bool
}

func (p *shellProbe) Init() tea.Cmd { return nil }

func (p *shellProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if p.deaf {
		return p, nil
	}
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.resizes++
	case tea.KeyPressMsg:
		p.keys = append(p.keys, m.String())
		if p.done != nil {
			return p, Done(*p.done)
		}
	}
	return p, nil
}

func (p *shellProbe) View() tea.View {
	return tea.NewView(textFrame(&shellPlainStyles, p.width, p.height, []string{p.name}, p.name+" status"))
}

func TestShellPushesAndPopsScreens(t *testing.T) {
	first := &shellProbe{name: "first"}
	second := &shellProbe{name: "second", done: &DoneMsg{}}
	s := NewShell(shellTestDeps(t), first)
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if got := len(s.stack); got != 1 {
		t.Fatalf("depth %d after construction, want 1", got)
	}
	s.Push(second)
	if got := len(s.stack); got != 2 {
		t.Fatalf("depth %d after Push, want 2", got)
	}
	if !strings.Contains(s.View().Content, "second") {
		t.Error("the pushed screen is not the one being drawn")
	}

	// The pushed screen finishes with a plain DoneMsg, which pops it.
	cmd := shellSend(t, s, "enter")
	if cmd == nil {
		t.Fatal("the screen did not emit its DoneMsg")
	}
	s.Update(cmd())
	if got := len(s.stack); got != 1 {
		t.Fatalf("depth %d after the pop, want 1", got)
	}
	if !strings.Contains(s.View().Content, "first") {
		t.Error("the revealed screen is not the one being drawn")
	}
	if first.resizes < 2 {
		t.Errorf("the revealed screen was told the size %d times, want it re-sized on reveal", first.resizes)
	}
	if len(first.keys) != 0 {
		t.Errorf("the buried screen saw keys %v", first.keys)
	}
}

func TestShellQuitEndsTheProgram(t *testing.T) {
	s := NewShell(shellTestDeps(t), &shellProbe{name: "first"})
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := s.Update(DoneMsg{Quit: true})
	if cmd == nil {
		t.Fatal("DoneMsg{Quit} produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("DoneMsg{Quit} produced %T, want tea.QuitMsg", cmd())
	}
}

func TestShellPoppingTheLastScreenEndsTheProgram(t *testing.T) {
	s := NewShell(shellTestDeps(t), &shellProbe{name: "only"})
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := s.Update(DoneMsg{})
	if cmd == nil {
		t.Fatal("popping the last screen produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("popping the last screen produced %T, want tea.QuitMsg", cmd())
	}
}

func TestShellShowsAChildErrorAndPopsIt(t *testing.T) {
	first := &shellProbe{name: "first"}
	failing := &shellProbe{name: "failing", done: &DoneMsg{Err: errors.New("the disk is on fire")}}
	s := NewShell(shellTestDeps(t), first)
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	s.Push(failing)

	cmd := shellSend(t, s, "enter")
	if cmd == nil {
		t.Fatal("the failing screen emitted nothing")
	}
	if _, quit := s.Update(cmd()); quit != nil {
		if _, ok := quit().(tea.QuitMsg); ok {
			t.Fatal("a child failure quit the program instead of reporting it")
		}
	}
	if got := len(s.stack); got != 1 {
		t.Fatalf("depth %d after the failure, want the failing screen popped", got)
	}
	frame := s.View().Content
	if !strings.Contains(frame, "the disk is on fire") {
		t.Errorf("the error is not on screen:\n%s", frame)
	}
	if !strings.Contains(frame, "press any key") {
		t.Errorf("the banner does not say how to dismiss it:\n%s", frame)
	}
	if !strings.Contains(frame, "first") {
		t.Error("the banner replaced the screen instead of covering it")
	}
	shellAssertFits(t, "shell with a banner", frame, 80, 24)

	// Dismissing consumes the key: it must not also act on the screen below.
	shellSend(t, s, "enter")
	if s.banner != nil {
		t.Error("a keypress did not dismiss the banner")
	}
	if len(first.keys) != 0 {
		t.Errorf("the dismissing key reached the screen below: %v", first.keys)
	}
	if got := s.View().Content; strings.Contains(got, "the disk is on fire") {
		t.Error("the banner is still drawn after being dismissed")
	}
}

func TestShellSizesAScreenPushedAfterResize(t *testing.T) {
	s := NewShell(shellTestDeps(t), &shellProbe{name: "first"})
	s.Update(tea.WindowSizeMsg{Width: 44, Height: 13})

	late := &shellProbe{name: "late"}
	s.Push(late)
	if late.resizes != 1 {
		t.Fatalf("the pushed screen was told the size %d times, want exactly 1", late.resizes)
	}
	if late.width != 44 || late.height != 13 {
		t.Fatalf("the pushed screen observed %dx%d, want 44x13", late.width, late.height)
	}

	// The same must hold for a screen that arrives through DoneMsg.Next.
	replacement := &shellProbe{name: "replacement"}
	s.Update(DoneMsg{Next: replacement})
	if replacement.width != 44 || replacement.height != 13 {
		t.Fatalf("the replacement screen observed %dx%d, want 44x13", replacement.width, replacement.height)
	}
}

func TestShellGlobalQuitKeyBypassesABusyScreen(t *testing.T) {
	busy := &shellProbe{name: "busy", deaf: true}
	s := NewShell(shellTestDeps(t), busy)
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cmd := shellSend(t, s, "ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}

	// Everything else still belongs to the screen, including the plain quit
	// letter: a screen taking a name must be able to type a "q".
	notBusy := &shellProbe{name: "typing"}
	s2 := NewShell(shellTestDeps(t), notBusy)
	s2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	shellSend(t, s2, "q")
	if len(notBusy.keys) != 1 || notBusy.keys[0] != "q" {
		t.Errorf("the screen saw %v, want the letter q passed down", notBusy.keys)
	}
}

func TestShellOwnsTheAlternateScreen(t *testing.T) {
	s := NewShell(shellTestDeps(t), &shellProbe{name: "first"})
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !s.View().AltScreen {
		t.Error("the shell does not set AltScreen, so no screen is on the alternate buffer")
	}
}

// TestShellSurvivesShrinkAndRegrow drives the resize through the shell rather
// than straight into a screen, which is how it arrives in a real terminal, and
// requires the screen underneath to come back exactly as it was.
func TestShellSurvivesShrinkAndRegrow(t *testing.T) {
	d := shellTestDeps(t)
	for _, n := range []string{"Anna", "Bernadett", "Cecilia"} {
		if _, err := d.Profiles.Create(n); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPicker(d, "Who is playing?")
	s := NewShell(d, p)
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	shellSend(t, s, "down")
	wanted := p.rows[p.sel].name
	before := s.View().Content
	shellAssertFits(t, "shell", before, 80, 24)

	s.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	shellAssertFits(t, "shell shrunk", s.View().Content, 20, 8)

	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if after := s.View().Content; after != before {
		t.Errorf("the frame changed across shrink and regrow:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := p.rows[p.sel].name; got != wanted {
		t.Errorf("the screen's selection moved to %q across resizes, want %q", got, wanted)
	}
}

func TestShellFrameFitsEverySize(t *testing.T) {
	for _, size := range shellSizes {
		w, h := size[0], size[1]
		s := NewShell(shellTestDeps(t), &shellProbe{name: "probe"})
		s.Update(tea.WindowSizeMsg{Width: w, Height: h})
		shellAssertFits(t, "shell", s.View().Content, w, h)

		s.banner = errors.New("a failure long enough that it cannot possibly fit on one line of a narrow terminal pane")
		shellAssertFits(t, "shell with a banner", s.View().Content, w, h)
	}
}

// TestShellRunsUnderBubbleTea drives the real program loop: the shell hosts the
// profile picker, is resized mid-run exactly as SIGWINCH would do it, and is
// ended by the global quit key.
func TestShellRunsUnderBubbleTea(t *testing.T) {
	deps := shellTestDeps(t)
	if _, err := deps.Profiles.Create("Bernadett"); err != nil {
		t.Fatal(err)
	}
	s := NewShell(deps, NewPicker(deps, "Who is playing?"))
	tm := teatest.NewTestModel(t, s, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Bernadett")
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.WindowSizeMsg{Width: 40, Height: 12})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Who is playing?")
	}, teatest.WithDuration(3*time.Second))

	tm.Send(shellKeyPress("ctrl+c"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
