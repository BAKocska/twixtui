package ui

import (
	lipgloss "charm.land/lipgloss/v2"
)

// Board glyphs. Every distinction the board draws must survive with colour
// disabled, so the glyph set alone separates the two players (filled versus
// hollow peg), empty holes, absent corners (blank), the cursor (square
// brackets), highlights (round brackets) and the eight link directions.
//
// A shallow link (column ±2, row ±1) is far shallower than any diagonal a
// terminal cell can draw: four screen columns for every row. Drawn as a ramp of
// scan lines at differing heights it read as a row of detached dashes rather
// than a connection, so it is drawn as a connected polyline of box-drawing
// pieces, assembled per cell from the edges the line joins. See linkBits.
const (
	glyphHole          = '·'
	glyphPegVertical   = '●'
	glyphPegHorizontal = '○'
	glyphCursorLeft    = '['
	glyphCursorRight   = ']'
	glyphMarkLeft      = '('
	glyphMarkRight     = ')'

	// Steep links (column ±1, row ±2) render at slope 1 in screen cells under
	// both scales, so a plain diagonal works.
	glyphRise = '╱'
	glyphFall = '╲'

	// A cell where a diagonal meets another stroke.
	glyphCross = '╳'

	glyphUp    = '↑'
	glyphDown  = '↓'
	glyphLeft  = '←'
	glyphRight = '→'
)

// styleID tags each canvas cell with the role it plays, so line emission can
// style runs of cells without the renderer knowing anything about colour.
type styleID uint8

const (
	styNone styleID = iota
	styHole
	styPegVertical
	styPegHorizontal
	styLinkVertical
	styLinkHorizontal
	styCursor
	styHighlight
	styLinkDigit
	styLabel
	numStyleIDs
)

// Styles holds every style the view layer uses. The zero value styles nothing;
// use DefaultStyles for the coloured set and PlainStyles when colour must be
// off (NO_COLOR). Rendering never depends on these for meaning — they are
// reinforcement over an already unambiguous glyph set.
type Styles struct {
	// Plain suppresses all styling, leaving pure text.
	Plain bool

	Hole           lipgloss.Style
	PegVertical    lipgloss.Style
	PegHorizontal  lipgloss.Style
	LinkVertical   lipgloss.Style
	LinkHorizontal lipgloss.Style
	Cursor         lipgloss.Style
	Highlight      lipgloss.Style
	LinkDigit      lipgloss.Style
	Label          lipgloss.Style

	PanelTitle lipgloss.Style
	PanelText  lipgloss.Style
	Status     lipgloss.Style
	Message    lipgloss.Style
	TooSmall   lipgloss.Style
}

// DefaultStyles returns the coloured style set. Colours come from the ANSI-16
// palette so they follow the user's terminal theme, and the colour profile
// machinery in Bubble Tea degrades them further when the terminal is limited.
func DefaultStyles() Styles {
	return Styles{
		Hole:           lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		PegVertical:    lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true),
		PegHorizontal:  lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		LinkVertical:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		LinkHorizontal: lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		Cursor:         lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true),
		Highlight:      lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		LinkDigit:      lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true),
		Label:          lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		PanelTitle:     lipgloss.NewStyle().Bold(true),
		PanelText:      lipgloss.NewStyle(),
		Status:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Message:        lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		TooSmall:       lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true),
	}
}

// PlainStyles returns a style set that applies nothing, for NO_COLOR and for
// tests that assert on raw glyphs.
func PlainStyles() Styles {
	return Styles{Plain: true}
}

// apply renders s in the style for id, or returns it untouched in plain mode.
func (st *Styles) apply(id styleID, s string) string {
	if st == nil || st.Plain {
		return s
	}
	var style *lipgloss.Style
	switch id {
	case styHole:
		style = &st.Hole
	case styPegVertical:
		style = &st.PegVertical
	case styPegHorizontal:
		style = &st.PegHorizontal
	case styLinkVertical:
		style = &st.LinkVertical
	case styLinkHorizontal:
		style = &st.LinkHorizontal
	case styCursor:
		style = &st.Cursor
	case styHighlight:
		style = &st.Highlight
	case styLinkDigit:
		style = &st.LinkDigit
	case styLabel:
		style = &st.Label
	default:
		return s
	}
	return style.Render(s)
}
