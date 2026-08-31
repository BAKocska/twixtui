package app

import (
	"fmt"
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

	at     int
	board  ui.BoardView
	width  int
	height int
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
		switch m.String() {
		case "q", "esc":
			return s, Back()
		case "right", "l", "space", "enter", "n":
			s.seek(s.at + 1)
		case "left", "h", "p":
			s.seek(s.at - 1)
		case "down", "j":
			s.seek(s.at + 5)
		case "up", "k":
			s.seek(s.at - 5)
		case "g", "home":
			s.seek(0)
		case "G", "end":
			s.seek(len(s.positions) - 1)
		}
	}
	return s, nil
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
	status := fmt.Sprintf("move %d of %d   left and right step   up and down five   g and G ends   q leaves",
		s.at, len(s.positions)-1)
	return tea.NewView(ui.Compose(arr, board, panel, status, s.deps.Styles))
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
		out = append(out, wrapTo(l, width)...)
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

// wrapTo breaks a line to fit a width, keeping whole words where it can. A word
// longer than the width is cut, because leaving it to overflow would break the
// frame's size invariant.
func wrapTo(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, w := range words {
			for len(w) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, w[:width])
				w = w[width:]
			}
			switch {
			case line == "":
				line = w
			case len(line)+1+len(w) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
