package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/BAKocska/twixtui/internal/theme"
)

// StylesFor maps a colour scheme onto the style set the view layer uses.
//
// A scheme with no colours, which is both the monochrome theme and what a
// terminal reporting no colour support gets, produces the plain style set rather
// than a set of empty styles: rendering then takes the same path as a test
// asserting on raw glyphs, so there is one behaviour to reason about instead of
// two that only look alike.
func StylesFor(t theme.Theme) Styles {
	if t.Monochrome() {
		return PlainStyles()
	}
	colour := func(hex string) lipgloss.Style {
		if hex == "" {
			return lipgloss.NewStyle()
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
	}
	return Styles{
		Hole:           colour(t.Grid),
		PegVertical:    colour(t.VerticalPeg).Bold(true),
		PegHorizontal:  colour(t.HorizontalPeg).Bold(true),
		LinkVertical:   colour(t.VerticalLink),
		LinkHorizontal: colour(t.HorizontalLink),
		Cursor:         colour(t.Cursor).Bold(true),
		Highlight:      colour(t.Highlight),
		LinkDigit:      colour(t.Highlight).Bold(true),
		Label:          colour(t.BorderRow),
		PanelTitle:     colour(t.Text).Bold(true),
		PanelText:      colour(t.Text),
		Status:         colour(t.Dim),
		Message:        colour(t.Warning),
		TooSmall:       colour(t.Warning).Bold(true),
	}
}
