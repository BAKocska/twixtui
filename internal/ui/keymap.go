package ui

import "strings"

// Action is something the board UI can do in response to a key.
type Action uint8

// Actions. Movement actions apply in every context; link actions only in link
// mode. ActConfirm is context-sensitive: it places the peg when none is staged
// and commits the turn otherwise.
const (
	ActNone Action = iota
	ActMoveLeft
	ActMoveRight
	ActMoveUp
	ActMoveDown
	ActJumpLeft
	ActJumpRight
	ActJumpUp
	ActJumpDown
	ActEdgeTop
	ActEdgeBottom
	ActEdgeLeft
	ActEdgeRight
	ActPlacePeg
	ActConfirm
	ActLinkMode
	ActToggleLink
	ActAbortTurn
	ActExitMode
	ActQuit
)

// JumpStep is how many holes the shifted movement keys jump.
const JumpStep = 3

// Context is a keymap context. Contexts form a bitmask so one binding can
// serve several.
type Context uint8

// The two contexts: normal board navigation, and link mode where the digit
// keys toggle links.
const (
	CtxBoard Context = 1 << iota
	CtxLink
)

// Binding maps keys to an action. Keys are Bubble Tea key strings as returned
// by tea.KeyPressMsg.String(). Every key here is reliable inside a terminal
// multiplexer: unmodified printables, uppercase letters, and the basic special
// keys — no modified arrows, no protocol-dependent combinations.
type Binding struct {
	Action   Action
	Keys     []string
	Contexts Context
	// Label is the short key name shown in help, Help the one-line meaning,
	// Short an optional terse verb for one-line hint strings.
	Label string
	Help  string
	Short string
}

// Keymap is an ordered list of bindings: one source of truth for dispatch,
// help text and documentation.
type Keymap []Binding

// DefaultKeymap returns the standard bindings.
func DefaultKeymap() Keymap {
	both := CtxBoard | CtxLink
	return Keymap{
		{ActMoveLeft, []string{"h", "left"}, both, "h/←", "move left", "move"},
		{ActMoveDown, []string{"j", "down"}, both, "j/↓", "move down", ""},
		{ActMoveUp, []string{"k", "up"}, both, "k/↑", "move up", ""},
		{ActMoveRight, []string{"l", "right"}, both, "l/→", "move right", ""},
		{ActJumpLeft, []string{"H"}, both, "H", "jump left", ""},
		{ActJumpDown, []string{"J"}, both, "J", "jump down", ""},
		{ActJumpUp, []string{"K"}, both, "K", "jump up", ""},
		{ActJumpRight, []string{"L"}, both, "L", "jump right", ""},
		{ActEdgeTop, []string{"g"}, both, "g", "top edge", ""},
		{ActEdgeBottom, []string{"G"}, both, "G", "bottom edge", ""},
		{ActEdgeLeft, []string{"0"}, both, "0", "left edge", ""},
		{ActEdgeRight, []string{"$"}, both, "$", "right edge", ""},
		{ActPlacePeg, []string{"space"}, CtxBoard, "space", "place peg", "place"},
		{ActConfirm, []string{"enter"}, both, "enter", "place / commit turn", "commit"},
		{ActLinkMode, []string{"x"}, both, "x", "link mode on/off", "links"},
		{ActToggleLink, []string{"1", "2", "3", "4", "5", "6", "7", "8"}, CtxLink, "1-8", "toggle that link", "toggle link"},
		{ActAbortTurn, []string{"a"}, both, "a", "abort turn", "abort"},
		{ActExitMode, []string{"esc"}, CtxLink, "esc", "leave link mode", "done"},
		{ActQuit, []string{"q", "ctrl+c"}, both, "q", "quit", "quit"},
	}
}

// Lookup resolves a key in a context. The pressed key is also returned to the
// caller through the binding's Keys, so multi-key bindings such as the link
// digits can recover which key fired.
func (km Keymap) Lookup(ctx Context, key string) (Binding, bool) {
	for _, b := range km {
		if b.Contexts&ctx == 0 {
			continue
		}
		for _, k := range b.Keys {
			if k == key {
				return b, true
			}
		}
	}
	return Binding{}, false
}

// ByAction finds the binding for an action in a context.
func (km Keymap) ByAction(ctx Context, a Action) (Binding, bool) {
	for _, b := range km {
		if b.Contexts&ctx != 0 && b.Action == a {
			return b, true
		}
	}
	return Binding{}, false
}

// HintLine renders a terse status hint for the given actions, with key labels
// taken from the bindings so hint text can never drift from the real map.
func (km Keymap) HintLine(ctx Context, actions ...Action) string {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		if b, ok := km.ByAction(ctx, a); ok && b.Short != "" {
			parts = append(parts, b.Label+" "+b.Short)
		}
	}
	return strings.Join(parts, " · ")
}

// HelpEntry is one help line: a key label and what it does.
type HelpEntry struct {
	Label string
	Help  string
}

// HelpEntries returns the help rows for a context, in keymap order.
func (km Keymap) HelpEntries(ctx Context) []HelpEntry {
	entries := make([]HelpEntry, 0, len(km))
	for _, b := range km {
		if b.Contexts&ctx == 0 {
			continue
		}
		entries = append(entries, HelpEntry{Label: b.Label, Help: b.Help})
	}
	return entries
}

// HelpTable renders the whole keymap as aligned plain-text rows, one binding
// per line, for documentation and the tutorial.
func (km Keymap) HelpTable(ctx Context) string {
	entries := km.HelpEntries(ctx)
	width := 0
	for _, e := range entries {
		width = max(width, len([]rune(e.Label)))
	}
	var b strings.Builder
	for _, e := range entries {
		pad := width - len([]rune(e.Label))
		b.WriteString(e.Label + strings.Repeat(" ", pad) + "  " + e.Help + "\n")
	}
	return b.String()
}
