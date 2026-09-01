package app

import "github.com/BAKocska/twixtui/internal/cover"

// The seam in coverart.go exists so that the menu's layout could be written and
// tested before the artwork did, and so that a front screen with no artwork
// remains a supported state rather than an accident. This is the file that joins
// the two, and it is deliberately the only place in the app package that knows
// the cover package exists: everything else goes through the two variables.
//
// The depth mapping is the part worth stating. The program resolves whether
// colour is allowed exactly once, in internal/cli, folding together --no-color,
// NO_COLOR, whether the output is a terminal at all, and a monochrome scheme; the
// menu is handed the answer as its styles' plain flag. Anything that emits an
// escape byte in that state is a defect, so plain maps to cover.DepthMono, which
// the cover package tests to emit no control bytes at all. Where colour is
// allowed the artwork is asked for true colour: a terminal that cannot manage it
// degrades the colour rather than the layout, and the 256-colour palette was
// tuned so that quantising loses a shade rather than a shape.
//
// Which of the two artworks answers is cover.Best's decision, not the menu's. It
// weighs the box the menu has left over against what each artwork needs, and the
// player can override it from the environment. Repeating any of that here would
// give one rule two spellings.
func init() {
	coverMinSize = func() (w, h int) {
		return cover.MinSize(cover.Best(coverProbeW, coverProbeH, cover.DepthTrueColour))
	}
	coverRender = func(w, h int, plain bool) []string {
		depth := cover.DepthTrueColour
		if plain {
			depth = cover.DepthMono
		}
		return cover.Render(w, h, depth, cover.Best(w, h, depth))
	}
}

// coverProbeW and coverProbeH are the box coverMinSize asks about. The minimum
// is wanted before the box is known — the menu uses it to decide whether to
// offer the artwork a column at all — and the two artworks differ by two rows in
// what they need. Asking about a generous box gets the answer for whichever
// artwork a generous box would use, which is the one whose minimum matters: if
// the box turns out to be smaller than that, Best falls back to the artwork with
// the smaller minimum and the picture still fits.
const (
	coverProbeW = 200
	coverProbeH = 60
)
