package ui

import (
	"strconv"
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

// ScaleFor picks the drawing scale for an n-hole board from the space the
// board itself is going to get: the detail scale when its whole block fits
// there, the compact one otherwise, which a viewport then clips if even that
// does not fit.
//
// The space to pass is the board's share, not the terminal's. A caller that
// takes rows away from the board before drawing — the tutorial gives most of
// the screen to its prose — and asks about the terminal instead will choose
// detail for a board that then has to be clipped, when compact would have
// shown the whole of it.
func ScaleFor(width, height, n int) Scale {
	if w, h := Detail.BlockSize(n); w <= width && h <= height {
		return Detail
	}
	return Compact
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

	arr.Scale = ScaleFor(width, availH, n)

	blockW, blockH := arr.Scale.BlockSize(n)
	arr.BoardW = min(blockW, width)
	arr.BoardH = min(blockH, availH)
	arr.BoardAvailW = width
	arr.BoardAvailH = availH

	if width-arr.BoardW >= panelGap+panelMinWidth {
		arr.Panel = PanelSide
		arr.PanelW = min(width-arr.BoardW-panelGap, panelMaxWidth)
		// A side panel is a column of its own and may use every row above the
		// status line, not only the rows the board happens to occupy. Tying it
		// to the board's height cut the key help off in the middle of the list
		// while blank rows went unused below the board — eleven of them at
		// 130x38 — which makes a cut that is merely tidy look like a fault.
		arr.PanelH = availH
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
		rows := max(len(board), min(len(panel), arr.PanelH))
		for i := range rows {
			var b, p string
			if i < len(board) {
				b = board[i]
			}
			if i < len(panel) {
				p = wholeWords(panel[i], arr.PanelW)
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
			lines = append(lines, wholeWords(p, arr.PanelW))
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
	lines = append(lines, wholeWords(status, arr.Width))

	for i, l := range lines {
		if ansi.StringWidth(l) > arr.Width {
			lines[i] = ansi.Truncate(l, arr.Width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// The mark a shortened line ends with. It is the same character the screens
// that build those lines use; this package only recognises it, and recognising
// it is the whole of the contract between them.
const truncationMark = "…"

// itemSep separates the hint items a status line is a list of.
const itemSep = " · "

// wholeWords pulls a line's truncation mark back to a boundary a reader can
// trust. A line that reaches its width and ends in the mark was cut to fit, and
// the cut fell wherever the width fell: "○ horizontal intermedia…", or a status
// line ending "r r…" where the resign key was. Nothing here knows what was
// dropped, but it can refuse to show a piece of a word.
//
// What the cut landed on is readable from what it left. A cut inside the
// separator between two items left the item before it whole, and only the
// remnant goes; a cut anywhere else may have landed inside a word, so a list of
// hint items gives up its last item and any other line its last word. Saying
// less properly beats saying more in pieces, and what is given up is the part
// that carried no information anyway. A line with nothing whole in front of the
// mark keeps its cut: one shortened word still names something, a bare mark
// names nothing.
//
// A line whose author wrote a mark of their own and which happens to fill the
// width exactly cannot be told from a cut one, and loses its last word to this.
// That is the price: a word fewer on a line that already ended in a mark, which
// is a smaller wrong than a word the program made up.
//
// Board lines are not offered here. They are a grid of glyphs, they carry no
// mark, and their overflow is clipped rather than shortened.
func wholeWords(line string, width int) string {
	// Every line of every frame comes through here, and almost none of them
	// carry a mark. Rule those out with a scan before anything is copied: the
	// mark cannot be the tail of a styled line, which ends in a reset, so the
	// cheap test is for the character anywhere in it.
	if width <= 0 || !strings.Contains(line, truncationMark) {
		return line
	}
	plain := strings.TrimRight(ansi.Strip(line), " ")
	body, marked := strings.CutSuffix(plain, truncationMark)
	if !marked || ansi.StringWidth(plain) < width {
		return line
	}
	keep := ""
	// The remnants a cut can leave of the separator, longest first. They are
	// derived from it so the two cannot drift apart.
	for _, remnant := range []string{itemSep, strings.TrimRight(itemSep, " "), " "} {
		if after, cut := strings.CutSuffix(body, remnant); cut {
			keep = after
			break
		}
	}
	if keep == "" {
		if i := strings.LastIndex(body, itemSep); i > 0 {
			keep = body[:i]
		} else if i := strings.LastIndexByte(body, ' '); i > 0 {
			keep = strings.TrimRight(body[:i], " ")
		}
	}
	if keep == "" {
		return line
	}
	return ansi.Truncate(line, ansi.StringWidth(keep)+1, truncationMark)
}

// composeTooSmall renders the explicit too-small notice. It never panics,
// whatever the size.
//
// The notice is the one message that must fit, because it appears precisely
// when the terminal is too small to hold anything: clipped to "terminal too
// sm" it reads as a program that is broken rather than as a window that is.
// Each line therefore has a series of forms, widest first, and the widest one
// that fits whole is used; a line with no form short enough is dropped rather
// than cut. The first line's last form is a single character, since at three
// columns saying that something is wrong is the whole of what can be said.
// Because only whole forms are emitted, nothing here can overflow the width,
// which matters: this is the one frame Compose does not clip afterwards.
func composeTooSmall(arr Arrangement, st *Styles) string {
	if arr.Width < 1 || arr.Height < 1 {
		return ""
	}
	// The size is derived rather than written out, so the bounds and the
	// notice that quotes them cannot drift apart.
	size := strconv.Itoa(MinWidth) + "x" + strconv.Itoa(MinHeight)
	msg := make([]string, 0, 2)
	if m := widestFit(arr.Width, "terminal too small", "too small", "small", "!"); m != "" {
		msg = append(msg, m)
	}
	if m := widestFit(arr.Width, "need "+size, size); m != "" {
		msg = append(msg, m)
	}
	if len(msg) > arr.Height {
		msg = msg[:arr.Height]
	}
	pre := max(0, (arr.Height-len(msg))/2)
	lines := make([]string, 0, arr.Height)
	for range pre {
		lines = append(lines, "")
	}
	for _, m := range msg {
		indent := max(0, (arr.Width-len(m))/2)
		lines = append(lines, strings.Repeat(" ", indent)+st.apply(styLabel, m))
	}
	return strings.Join(lines, "\n")
}

// widestFit returns the first of forms that fits in width, or "" when none
// does. Callers list forms widest first. The forms are plain ASCII, so their
// byte length is their width on screen.
func widestFit(width int, forms ...string) string {
	for _, f := range forms {
		if len(f) <= width {
			return f
		}
	}
	return ""
}
