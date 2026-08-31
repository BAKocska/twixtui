package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Minimum terminal size for showing a board. Below either bound the frame is
// an explicit too-small notice instead: the smallest useful board view needs
// the row-number gutter plus a handful of hole columns, and the letters row
// plus a few board rows plus the status line.
const (
	MinWidth  = 20
	MinHeight = 6
)

// Panel sizing. A side panel needs room for the turn line and key help; wider
// than panelMaxWidth adds nothing, and remaining columns are left blank.
const (
	panelMinWidth  = 24
	panelMaxWidth  = 36
	panelGap       = 2
	panelMinHeight = 4
)

// PanelPlacement says where the information panel goes.
type PanelPlacement uint8

// Panel placements: none (status line only), beside the board, below it.
const (
	PanelNone PanelPlacement = iota
	PanelSide
	PanelBottom
)

// Arrangement is the layout decision for one terminal size: which scale to
// draw the board at, how much space the board gets, and where the information
// panel sits. A status line is always reserved at the bottom except in the
// too-small state.
type Arrangement struct {
	Width, Height int
	TooSmall      bool
	Scale         Scale

	// BoardAvailW and BoardAvailH bound what BoardView.Render may use.
	BoardAvailW, BoardAvailH int
	// BoardW and BoardH are the actual block size after clipping to the
	// available space, used to place the panel without wasted columns.
	BoardW, BoardH int

	Panel          PanelPlacement
	PanelW, PanelH int
}

// Arrange decides the layout for a terminal of width×height showing an n-hole
// board. The rules, in order: the detail scale is used when its full board
// fits; otherwise the compact scale, with a viewport when even that does not
// fit. The panel goes beside the board when there is width for it, else below
// when there is height, else information lives in the status line alone.
func Arrange(width, height, n int) Arrangement {
	arr := Arrangement{Width: width, Height: height}
	if width < MinWidth || height < MinHeight {
		arr.TooSmall = true
		return arr
	}
	availH := height - 1 // status line

	arr.Scale = Compact
	if w, h := Detail.BlockSize(n); w <= width && h <= availH {
		arr.Scale = Detail
	}

	blockW, blockH := arr.Scale.BlockSize(n)
	arr.BoardW = min(blockW, width)
	arr.BoardH = min(blockH, availH)
	arr.BoardAvailW = width
	arr.BoardAvailH = availH

	if width-arr.BoardW >= panelGap+panelMinWidth {
		arr.Panel = PanelSide
		arr.PanelW = min(width-arr.BoardW-panelGap, panelMaxWidth)
		arr.PanelH = arr.BoardH
		arr.BoardAvailW = width - panelGap - arr.PanelW
	} else if availH-arr.BoardH >= panelMinHeight {
		arr.Panel = PanelBottom
		arr.PanelW = width
		arr.PanelH = availH - arr.BoardH
		arr.BoardAvailH = arr.BoardH
	}
	return arr
}

// Compose assembles the final frame from the rendered board, the panel lines
// and the status line, clipped so that no line exceeds the arrangement's width
// and no more than its height lines are emitted. Board lines must come from
// BoardView.Render with the arrangement's board bounds.
func Compose(arr Arrangement, board, panel []string, status string, st *Styles) string {
	if arr.TooSmall {
		return composeTooSmall(arr, st)
	}
	lines := make([]string, 0, arr.Height)
	switch arr.Panel {
	case PanelSide:
		rows := max(len(board), min(len(panel), arr.BoardH))
		for i := range rows {
			var b, p string
			if i < len(board) {
				b = board[i]
			}
			if i < len(panel) {
				p = panel[i]
			}
			if p == "" {
				lines = append(lines, b)
				continue
			}
			b += strings.Repeat(" ", max(0, arr.BoardW+panelGap-ansi.StringWidth(b)))
			lines = append(lines, b+p)
		}
	case PanelBottom:
		lines = append(lines, board...)
		free := arr.PanelH
		for _, p := range panel {
			if free == 0 {
				break
			}
			lines = append(lines, p)
			free--
		}
	default:
		lines = append(lines, board...)
	}

	// Pin the status line to the bottom row.
	for len(lines) < arr.Height-1 {
		lines = append(lines, "")
	}
	if len(lines) > arr.Height-1 {
		lines = lines[:arr.Height-1]
	}
	lines = append(lines, status)

	for i, l := range lines {
		if ansi.StringWidth(l) > arr.Width {
			lines[i] = ansi.Truncate(l, arr.Width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// composeTooSmall renders the explicit too-small notice, itself clipped to the
// terminal. It never panics, whatever the size.
func composeTooSmall(arr Arrangement, st *Styles) string {
	if arr.Width < 1 || arr.Height < 1 {
		return ""
	}
	msg := []string{"terminal too small", "need 20x6"}
	if len(msg) > arr.Height {
		msg = msg[:arr.Height]
	}
	pre := max(0, (arr.Height-len(msg))/2)
	lines := make([]string, 0, arr.Height)
	for range pre {
		lines = append(lines, "")
	}
	for _, m := range msg {
		if len(m) > arr.Width {
			m = m[:arr.Width]
		}
		indent := max(0, (arr.Width-len(m))/2)
		lines = append(lines, strings.Repeat(" ", indent)+st.apply(styLabel, m))
	}
	return strings.Join(lines, "\n")
}
