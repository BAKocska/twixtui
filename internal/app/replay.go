package app

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/ui"
)

// ReplayScreen walks through a finished game one move at a time.
//
// Positions are materialised up front by replaying the record, rather than by
// undoing from the end. Replaying forwards is the same path a saved game takes
// when it is loaded, so a record that cannot be replayed is refused here too
// instead of appearing to work until the player steps back through it.
type ReplayScreen struct {
	deps  Deps
	saved gamestore.Saved

	// positions[i] is the game after i entries have been applied, so
	// positions[0] is the empty board.
	positions []*game.Game
	// labels[i] describes the entry that produced positions[i+1].
	labels []string

	at    int
	board ui.BoardView
	// hints are the footer's key parts, built once from the keymap because the
	// keys cannot change while the screen is up.
	hints         []string
	width, height int
}

// replayJump is how many moves the up and down keys travel. It is deliberately
// not the keymap's JumpStep, which is a distance across a board rather than a
// distance through a record.
const replayJump = 5

// replayKey is one thing this screen does together with every key that does
// it: a board action from the shared keymap, whose label the footer shows, and
// any key of replay's own for the same thing. Dispatch and the footer are both
// built from this, so a key answered in silence, or a footer naming a key the
// screen ignores, cannot happen. Neighbouring rows with the same help share
// one footer part.
type replayKey struct {
	action ui.Action
	own    []string
	help   string
	// seek is where the key moves to, given the position shown and the last
	// one. A row without it leaves the screen.
	seek func(at, last int) int
}

// replayKeys is the whole of what the replay screen answers, in footer order.
// Stepping through a record is not moving a cursor over holes, so the wording is
// replay's own, but the keys are the keymap's: the product teaches h, j, k and l
// on every other board, and a replay is no place to teach something else.
var replayKeys = []replayKey{
	{ui.ActMoveRight, []string{"n"}, "next", func(at, _ int) int { return at + 1 }},
	{ui.ActMoveLeft, []string{"p"}, "back", func(at, _ int) int { return at - 1 }},
	{ui.ActMoveDown, nil, "five", func(at, _ int) int { return at + replayJump }},
	{ui.ActMoveUp, nil, "five", func(at, _ int) int { return at - replayJump }},
	{ui.ActEdgeTop, nil, "ends", func(_, _ int) int { return 0 }},
	{ui.ActEdgeBottom, nil, "ends", func(_, last int) int { return last }},
	{ui.ActQuit, []string{"esc"}, "leaves", nil},
}

// replayHints renders the footer's key parts, taking the labels from the keymap
// so that a rebinding moves the hint along with the key.
func replayHints(km ui.Keymap) []string {
	parts := make([]string, 0, len(replayKeys))
	labels := make([]string, 0, 4)
	help := ""
	flush := func() {
		if len(labels) > 0 {
			parts = append(parts, strings.Join(labels, " ")+" "+help)
			labels = labels[:0]
		}
	}
	for _, r := range replayKeys {
		if r.help != help {
			flush()
			help = r.help
		}
		if b, ok := km.ByAction(ui.CtxBoard, r.action); ok && b.Label != "" {
			labels = append(labels, b.Label)
		}
		labels = append(labels, r.own...)
	}
	flush()
	return parts
}

// NewReplayScreen prepares a stored game for review.
func NewReplayScreen(d Deps, saved gamestore.Saved) (Screen, error) {
	final, err := saved.Game()
	if err != nil {
		return nil, err
	}
	rs := final.Rules()

	positions := make([]*game.Game, 0, final.Entries()+1)
	labels := make([]string, 0, final.Entries())

	start, err := game.New(rs)
	if err != nil {
		return nil, err
	}
	positions = append(positions, start)

	// Rebuild move by move, keeping a snapshot after each entry.
	step, err := game.New(rs)
	if err != nil {
		return nil, err
	}
	for i := range final.Entries() {
		notation, err := final.MoveNotation(i)
		if err != nil {
			return nil, fmt.Errorf("reading entry %d of %s: %w", i+1, saved.ID, err)
		}
		if err := step.PlayNotation(notation); err != nil {
			return nil, fmt.Errorf("replaying entry %d of %s (%q): %w", i+1, saved.ID, notation, err)
		}
		positions = append(positions, step.Clone())
		labels = append(labels, notation)
	}

	return &ReplayScreen{
		deps:      d,
		saved:     saved,
		positions: positions,
		labels:    labels,
		at:        len(positions) - 1,
		board:     ui.BoardView{Scale: ui.Compact},
		hints:     replayHints(shellKeymap(d)),
	}, nil
}

// Init satisfies tea.Model.
func (s *ReplayScreen) Init() tea.Cmd { return nil }

// Update satisfies tea.Model.
func (s *ReplayScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = m.Width, m.Height
	case tea.KeyPressMsg:
		return s, s.press(m.String())
	}
	return s, nil
}

// press interprets one key through the same table the footer is built from, so
// that the keys the screen answers and the keys it names are one list.
func (s *ReplayScreen) press(key string) tea.Cmd {
	action := ui.ActNone
	// The control forms of a binding are the shell's: it answers them before
	// this screen is asked, so the footer does not name them and dispatch does
	// not claim them.
	if !strings.HasPrefix(key, "ctrl+") {
		if b, ok := shellKeymap(s.deps).Lookup(ui.CtxBoard, key); ok {
			action = b.Action
		}
	}
	for _, r := range replayKeys {
		if r.action != action && !slices.Contains(r.own, key) {
			continue
		}
		if r.seek == nil {
			return Back()
		}
		s.seek(r.seek(s.at, len(s.positions)-1))
		return nil
	}
	return nil
}

func (s *ReplayScreen) seek(to int) {
	s.at = min(max(to, 0), len(s.positions)-1)
}

// current returns the position being shown.
func (s *ReplayScreen) current() *game.Game { return s.positions[s.at] }

// View satisfies tea.Model.
func (s *ReplayScreen) View() tea.View {
	arr := ui.Arrange(s.width, s.height, s.current().Size())
	if arr.TooSmall {
		return tea.NewView(ui.Compose(arr, nil, nil, "", s.deps.Styles))
	}
	s.board.Scale = arr.Scale
	board := s.board.Render(s.current(), s.deps.Styles, arr.BoardAvailW, arr.BoardAvailH)
	panel := s.panel(arr.PanelW)
	status := s.status(arr.Width)
	return tea.NewView(ui.Compose(arr, board, panel, status, s.deps.Styles))
}

// status is the bottom row: where in the record the player is, then what the
// keys do, dropped from the end when the terminal is too narrow for all of it.
func (s *ReplayScreen) status(width int) string {
	parts := make([]string, 0, len(s.hints)+1)
	parts = append(parts, fmt.Sprintf("move %d of %d", s.at, len(s.positions)-1))
	return hintLine(width, append(parts, s.hints...)...)
}

// panel describes the game and where in it the player is.
func (s *ReplayScreen) panel(width int) []string {
	if width <= 0 {
		return nil
	}
	g := s.current()
	lines := []string{
		s.saved.ID,
		s.saved.Player + " vs " + s.saved.Opponent,
		g.Rules().Describe(),
		"",
		fmt.Sprintf("shown: move %d of %d", s.at, len(s.positions)-1),
	}
	if s.at > 0 {
		lines = append(lines, "last: "+s.labels[s.at-1])
	} else {
		lines = append(lines, "last: the opening position")
	}
	if s.at < len(s.labels) {
		lines = append(lines, "next: "+s.labels[s.at])
	}
	lines = append(lines, "", "result: "+describeOutcome(s.positions[len(s.positions)-1].Result()))

	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, wrapText(l, width)...)
	}
	return out
}

// describeOutcome renders a result in words, matching the wording used elsewhere.
func describeOutcome(r game.Result) string {
	if !r.Over() {
		return "still being played"
	}
	reason := map[game.Reason]string{
		game.Connection:  "by completing a chain",
		game.NoMovesLeft: "with no legal moves left",
		game.Resignation: "by resignation",
		game.Agreement:   "by agreement",
	}[r.Reason]
	switch r.Outcome {
	case game.Draw:
		return "drawn " + reason
	case game.VerticalWins:
		return "vertical won " + reason
	case game.HorizontalWins:
		return "horizontal won " + reason
	}
	return "unknown"
}
