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

	// The peg just played, marked so it can be found on a large board without
	// reading coordinates off the panel. It is a glyph rather than only a colour
	// because every distinction here has to survive colour being off.
	glyphPegVerticalLast   = '◉'
	glyphPegHorizontalLast = '◎'
	glyphCursorLeft        = '['
	glyphCursorRight       = ']'
	glyphMarkLeft          = '('
	glyphMarkRight         = ')'

	// An overlay also has to be visible where a link stroke owns the cells
	// either side of the hole, which at the compact scale is what a peg looks
	// like the moment it is linked. An overlay left with no bracket falls back
	// to the hole's own cell, and a mark there still has to say what the hole
	// holds, because an overlay may never hide a peg.
	//
	// Three shapes for the three things the fallback can mean: a diamond is the
	// cursor, a square is a highlight, a triangle is a highlighted hole with
	// the cursor on it, which is what a peg is for the rest of the turn that
	// staged it. Within each shape the fill says what the hole holds, the same
	// way round as the pegs themselves: solid is the vertical player, and an
	// empty hole is the plain outline, so a peg can never be read as an empty
	// hole. The horizontal player is the outline with a mark inside it, except
	// in the triangle, which has no such form in the fonts a terminal can be
	// relied on for, and uses the inverted outline instead.
	glyphCursorHole              = '◇'
	glyphCursorPegVertical       = '◆'
	glyphCursorPegHorizontal     = '◈'
	glyphMarkHole                = '□'
	glyphMarkPegVertical         = '■'
	glyphMarkPegHorizontal       = '▣'
	glyphCursorMarkHole          = '△'
	glyphCursorMarkPegVertical   = '▲'
	glyphCursorMarkPegHorizontal = '▽'

	// Steep links (column ±1, row ±2) render at slope 1 in screen cells under
	// both scales, so a plain diagonal works.
	glyphRise = '╱'
	glyphFall = '╲'

	// A cell where a diagonal meets another stroke.
	glyphCross = '╳'

	// A peg with a link's horizontal run passing through its cell. A shallow
	// link has to cross the column of holes between its two ends, and where both
	// of those holes hold pegs there is no free cell left: the run either gives
	// way, which breaks the link, or it is drawn through the peg. These say both
	// things at once, and keep the filled-or-hollow distinction that names the
	// owner, so nothing is lost that the plain peg said.
	glyphPegVerticalBridge   = '⊕'
	glyphPegHorizontalBridge = '⊖'

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
	styLastMove
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
	LastMove       lipgloss.Style
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
		LastMove:       lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true),
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
	case styLastMove:
		style = &st.LastMove
	case styLabel:
		style = &st.Label
	default:
		return s
	}
	return style.Render(s)
}
