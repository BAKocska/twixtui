package app

import "github.com/charmbracelet/x/ansi"

// The front screen's artwork comes from internal/cover, which is being written
// on its own branch. The menu reaches it through the two function variables
// below instead of an import, so this branch compiles and every layout rule is
// exercised before the artwork exists; while they are nil the front screen
// simply has no picture, which is also what it must do in a terminal too small
// for one. When the cover package lands, the integrator wires them in a small
// file of its own: coverMinSize from cover.MinSize, and coverRender from
// cover.Render, choosing the Art for the front screen and mapping plain to
// cover.DepthMono so that colour-off stays colour-off.
var (
	// coverMinSize reports the smallest canvas the artwork can use.
	coverMinSize func() (w, h int)
	// coverRender draws the artwork into at most w columns and h rows. plain
	// says colour is off, and the artwork must then emit no colour either.
	coverRender func(w, h int, plain bool) []string
)

// coverColumn returns the artwork laid out for a box of width×height, centred
// by leading blank lines and leading spaces alone, so nothing here puts
// trailing spaces on a frame. It returns nil when there is no artwork to have
// — the package is absent, the box is smaller than the art's minimum, or the
// renderer returned nothing — and the caller then gives the menu the whole
// terminal.
//
// Nothing here re-clips what Render returns. The contract says Render never
// exceeds its canvas, and if it ever did, the frame is still clipped exactly
// once, at the edge, by ui.Compose — the same single clipping point every
// screen goes through — so the fitting invariant does not rest on the cover
// package keeping its word.
func coverColumn(width, height int, plain bool) []string {
	if coverMinSize == nil || coverRender == nil || width < 1 || height < 1 {
		return nil
	}
	minW, minH := coverMinSize()
	if width < minW || height < minH {
		return nil
	}
	lines := coverRender(width, height, plain)
	if len(lines) == 0 {
		return nil
	}
	widest := 0
	for _, l := range lines {
		widest = max(widest, ansi.StringWidth(l))
	}
	// Centred, because a picture pinned into the top-left corner of a larger
	// blank area reads as a layout fault rather than as a cover.
	leftPad := padTo("", max(0, (width-widest)/2))
	out := make([]string, 0, max(0, (height-len(lines))/2)+len(lines))
	for range max(0, (height-len(lines))/2) {
		out = append(out, "")
	}
	for _, l := range lines {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, leftPad+l)
	}
	return out
}
