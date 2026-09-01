package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/theme"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Shell is the root Bubble Tea model: it owns the stack of screens, the
// terminal size, the alternate screen and the error banner, and it is the only
// thing in the program that calls tea.Quit.
//
// Screens hand control back with a DoneMsg instead of quitting or pushing
// themselves, so the rule for what happens when a screen finishes lives in one
// place and a screen can be reached from more than one place without knowing
// where it will return to.
type Shell struct {
	deps  Deps
	stack []Screen

	width, height int
	// sized records whether a terminal size has arrived yet, so that a screen
	// pushed before the first tea.WindowSizeMsg is not told a size of zero.
	sized bool

	// banner is a failure the player has not acknowledged yet. It is drawn
	// over whatever is on screen: a program that exits on an error nobody got
	// to read is indistinguishable from a crash.
	banner error

	// quitKeys are the keys the shell answers itself instead of passing down,
	// taken from the keymap rather than written out a second time here.
	quitKeys []string
}

// NewShell returns the root model showing first.
func NewShell(d Deps, first Screen) *Shell {
	s := &Shell{deps: d, quitKeys: globalQuitKeys(shellKeymap(d))}
	if first != nil {
		s.stack = append(s.stack, first)
	}
	return s
}

// Init implements tea.Model.
func (s *Shell) Init() tea.Cmd {
	if top := s.top(); top != nil {
		return top.Init()
	}
	return nil
}

// Update implements tea.Model. Everything except the shell's own messages and
// the global quit key goes to the screen on top.
func (s *Shell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = m.Width, m.Height
		s.sized = true

	case DoneMsg:
		return s, s.leave(m)

	case OpenMsg:
		if m.Screen == nil {
			return s, nil
		}
		return s, s.Push(m.Screen)

	case ThemeChangedMsg:
		// The shell draws its own banner, so it needs the new styles too. The
		// message then goes on to the screen, which keeps its own copy.
		s.deps.Theme = m.Theme
		if m.Styles != nil {
			s.deps.Styles = m.Styles
		}

	case tea.KeyPressMsg:
		key := m.String()
		for _, q := range s.quitKeys {
			if key == q {
				return s, s.quit()
			}
		}
		if s.banner != nil {
			// Any key dismisses. The key is consumed rather than also acting
			// on the screen underneath, which the player cannot fully see
			// while the banner covers it.
			s.banner = nil
			if len(s.stack) == 0 {
				return s, s.quit()
			}
			return s, nil
		}
	}

	top := s.top()
	if top == nil {
		return s, nil
	}
	next, cmd := top.Update(msg)
	s.setTop(next)
	return s, cmd
}

// View implements tea.Model. The shell owns the alternate screen, so no screen
// has to; only a screen's content is used.
func (s *Shell) View() tea.View {
	st := shellStyles(s.deps)
	var content string
	if top := s.top(); top != nil {
		content = top.View().Content
	} else {
		content = textFrame(st, s.width, s.height, nil, "")
	}
	if s.banner != nil {
		content = overlayBanner(st, content, s.banner, s.width, s.height)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// Push shows sc on top of the current screen, which stays on the stack and is
// revealed again when sc finishes.
func (s *Shell) Push(sc Screen) tea.Cmd {
	if sc == nil {
		return nil
	}
	s.stack = append(s.stack, sc)
	return tea.Batch(sc.Init(), s.sizeTop())
}

// leave acts on a screen that has finished.
func (s *Shell) leave(m DoneMsg) tea.Cmd {
	if m.Quit {
		return s.quit()
	}
	// The banner is set before the stack changes so that the error is shown
	// over whatever the player ends up looking at.
	if m.Err != nil {
		s.banner = m.Err
	}
	if len(s.stack) > 0 {
		s.stack = s.stack[:len(s.stack)-1]
	}
	if m.Next != nil {
		return s.Push(m.Next)
	}
	if len(s.stack) == 0 {
		if s.banner != nil {
			// Nothing left to go back to, but the failure has not been read
			// yet. Dismissing the banner ends the program.
			return nil
		}
		return s.quit()
	}
	// A buried screen received no size messages while it was covered, so it is
	// re-sized on the way back rather than redrawing at a stale size. It may
	// also be showing something it read from disk before the screen above it
	// ran, so it is told it is on top again first.
	if r, ok := s.top().(revealed); ok {
		r.revealed()
	}
	return s.sizeTop()
}

// revealed is implemented by a screen holding state that can go stale while
// another screen sits on top of it.
type revealed interface {
	revealed()
}

// sizeTop hands the remembered terminal size to the screen on top, so a screen
// that appears after the size is known never has to render a frame blind.
func (s *Shell) sizeTop() tea.Cmd {
	if !s.sized {
		return nil
	}
	top := s.top()
	if top == nil {
		return nil
	}
	next, cmd := top.Update(tea.WindowSizeMsg{Width: s.width, Height: s.height})
	s.setTop(next)
	return cmd
}

func (s *Shell) top() Screen {
	if len(s.stack) == 0 {
		return nil
	}
	return s.stack[len(s.stack)-1]
}

// setTop stores the model a screen's Update returned. Bubble Tea models may
// return a different value than the receiver, and a screen that does so would
// otherwise have its change dropped.
func (s *Shell) setTop(m tea.Model) {
	sc, ok := m.(Screen)
	if !ok || len(s.stack) == 0 {
		return
	}
	s.stack[len(s.stack)-1] = sc
}

// ThemeChangedMsg announces that the player chose a different colour scheme.
// The shell follows it for its own drawing and passes it on, so a screen
// holding its own copy of Deps can follow too.
type ThemeChangedMsg struct {
	Theme  theme.Theme
	Styles *ui.Styles
}

// Departing is implemented by a screen that has something to finish before the
// program ends, such as saving a game that is still in progress.
//
// The shell answers the global quit key itself so that a busy screen cannot trap
// the player, but that means the key never reaches the screen. Without this, a
// screen's own handling of it is dead code: quitting with the plain letter saved
// an unfinished game while quitting with the control key silently discarded it,
// which is the same act from the player's point of view.
type Departing interface {
	// Depart is called once, on the way out, before the program ends. It must
	// not block for long and must not expect to draw again.
	Depart()
}

// quit lets every screen on the stack finish, innermost first, and then ends the
// program. It is the only place that calls tea.Quit.
func (s *Shell) quit() tea.Cmd {
	for i := len(s.stack) - 1; i >= 0; i-- {
		if d, ok := s.stack[i].(Departing); ok {
			d.Depart()
		}
	}
	s.stack = nil
	return tea.Quit
}

// globalQuitKeys returns the keys the shell answers before the screen does:
// the control forms of the keymap's quit binding. A plain letter is left to
// the screen, which may be in the middle of taking a name.
func globalQuitKeys(km ui.Keymap) []string {
	b, ok := km.ByAction(ui.CtxBoard, ui.ActQuit)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(b.Keys))
	for _, k := range b.Keys {
		if strings.HasPrefix(k, "ctrl+") {
			keys = append(keys, k)
		}
	}
	return keys
}

// fallbackStyles is used when Deps carries none, which happens in a test that
// only cares about behaviour.
var fallbackStyles = ui.DefaultStyles()

// shellStyles returns the styles to draw with, never nil.
func shellStyles(d Deps) *ui.Styles {
	if d.Styles != nil {
		return d.Styles
	}
	return &fallbackStyles
}

// shellKeymap returns the key bindings to dispatch with. There is one keymap
// in the program; a screen that needs a key label asks this rather than
// writing the key out again.
func shellKeymap(d Deps) ui.Keymap {
	if len(d.Keymap) > 0 {
		return d.Keymap
	}
	return ui.DefaultKeymap()
}

// paint applies a style unless styling is off.
func paint(st *ui.Styles, s *lipgloss.Style, text string) string {
	if st == nil || st.Plain || text == "" {
		return text
	}
	return s.Render(text)
}

// textFrame lays out a screen made of text: content from the top row down, the
// status line pinned to the bottom row, nothing wider than the terminal and no
// more lines than it has rows.
//
// It goes through ui.Compose so that the clipping rule, the pinned status line
// and the too-small notice are the same code the board screens use. A screen
// with no board is an arrangement with no panel and the text where the board
// would go.
func textFrame(st *ui.Styles, width, height int, content []string, status string) string {
	arr := ui.Arrangement{
		Width:    width,
		Height:   height,
		TooSmall: width < ui.MinWidth || height < ui.MinHeight,
	}
	return ui.Compose(arr, content, nil, status, st)
}

// window returns at most n lines, scrolled so that the line at keep stays
// visible. A list longer than the terminal is the normal case in a small pane,
// and a selection that has scrolled out of sight is the bug this prevents.
func window(lines []string, n, keep int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	start := 0
	if keep >= n {
		start = keep - n + 1
	}
	if start > len(lines)-n {
		start = len(lines) - n
	}
	if start < 0 {
		start = 0
	}
	return lines[start : start+n]
}

// padTo pads s with spaces to width columns, counting terminal cells so that
// styled or non-Latin text still lines up. It never truncates: clipping is
// ui.Compose's job and happens once, at the edge of the frame.
func padTo(s string, width int) string {
	if w := ansi.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// clampLines drops lines beyond n, which is what keeps a sub-panel inside the
// space the frame gave it.
func clampLines(lines []string, n int) []string {
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

// overlayBanner draws an error over the top rows of a frame, leaving the rest
// of the screen visible so the player can still see what they were doing. The
// frame's size is unchanged: an overlay that grew the frame would break the
// fitting invariant on the very screens that are already going wrong.
func overlayBanner(st *ui.Styles, frame string, err error, width, height int) string {
	if width < 1 || height < 1 || err == nil {
		return frame
	}
	const dismiss = "press any key to continue"
	msg := wrapText("error: "+err.Error(), width)
	if len(msg) > height-1 {
		msg = msg[:height-1]
	}
	msg = append(msg, dismiss)
	msg = clampLines(msg, height)

	lines := strings.Split(frame, "\n")
	for i, m := range msg {
		styled := paint(st, &st.Message, ansi.Truncate(m, width, ""))
		if i < len(lines) {
			lines[i] = styled
			continue
		}
		lines = append(lines, styled)
	}
	return strings.Join(lines, "\n")
}

// hintLine joins key hints with a separator, dropping them from the end until
// the line fits. A narrow pane then keeps the first, most important hints
// instead of losing the tail of the line to truncation mid-word.
func hintLine(width int, parts ...string) string {
	const sep = " · "
	for n := len(parts); n > 1; n-- {
		line := strings.Join(parts[:n], sep)
		if ansi.StringWidth(line) <= width {
			return line
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// arrowKeys returns the special-key forms of a movement binding — the arrows,
// not the vim letters. A screen with a text field on it cannot use the letter
// forms, because those are characters the player is typing, so it narrows the
// shared binding rather than starting a second list of keys.
func arrowKeys(km ui.Keymap, a ui.Action) []string {
	b, ok := km.ByAction(ui.CtxBoard, a)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(b.Keys))
	for _, k := range b.Keys {
		if len([]rune(k)) > 1 && !strings.HasPrefix(k, "ctrl+") {
			keys = append(keys, k)
		}
	}
	return keys
}

// matchesKey reports whether key is one of keys.
func matchesKey(key string, keys []string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// The emacs-style movement pair. These are not in the shared keymap because
// they only exist where a text field owns the letter keys: in a list with a
// query line, "j" and "k" are characters the player is typing, so the list
// answers ctrl+p and ctrl+n as well, which no text field claims.
const (
	keyPrev = "ctrl+p"
	keyNext = "ctrl+n"
)

// navKeys are the keys a list answers, resolved once from the shared keymap so
// that a keypress does not rebuild them.
type navKeys struct {
	up, down, confirm, cancel []string
}

func newNavKeys(km ui.Keymap) navKeys {
	nk := navKeys{
		up:     arrowKeys(km, ui.ActMoveUp),
		down:   arrowKeys(km, ui.ActMoveDown),
		cancel: []string{"esc"},
	}
	if b, ok := km.ByAction(ui.CtxBoard, ui.ActConfirm); ok {
		nk.confirm = b.Keys
	}
	return nk
}

// move interprets key as movement within a list of n items, and reports
// whether it was a movement at all. Single steps wrap: the last entry of a
// menu is then one key away from the first, and holding a key down never
// looks like a screen that has stopped responding.
func (nk navKeys) move(key string, sel, n int) (int, bool) {
	if n <= 0 {
		return sel, false
	}
	const page = 5
	switch {
	case key == keyPrev || matchesKey(key, nk.up):
		return (sel - 1 + n) % n, true
	case key == keyNext || matchesKey(key, nk.down):
		return (sel + 1) % n, true
	case key == "pgup":
		return max(0, sel-page), true
	case key == "pgdown":
		return min(n-1, sel+page), true
	}
	return sel, false
}

func (nk navKeys) isConfirm(key string) bool { return matchesKey(key, nk.confirm) }
func (nk navKeys) isCancel(key string) bool  { return matchesKey(key, nk.cancel) }

// keyLabel names keys for a hint line, with the arrow keys shown as arrows.
// The names come from the bindings themselves, so a hint can never describe a
// key the screen does not answer.
func keyLabel(keys ...string) string {
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		switch k {
		case "up":
			names = append(names, "↑")
		case "down":
			names = append(names, "↓")
		case "":
		default:
			names = append(names, k)
		}
	}
	return strings.Join(names, "/")
}
