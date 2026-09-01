// Package cover draws the artwork behind the menu. Two artworks ship: Photo
// projects the picture in assets — the project's own flat-poster reduction
// of the 1962 box lid composition, whose provenance assets/README.md records
// — and Homage is a composition drawn from scratch in character art. Both
// exist because they fail differently: the projection carries the lid itself
// and wins wherever the grid is fine enough to hold it, while the homage is
// drawn in cells to begin with and survives the small boxes and the
// monochrome terminals that turn any projection to noise. Best says which
// one a given box deserves.
//
// Everything here is characters and ANSI colour. Terminal graphics protocols
// (kitty, sixel) reach a minority of terminals and fail unevenly through
// multiplexers, so the cover is drawn with the one facility every terminal
// has. The output is a slice of lines rather than a framed screen because
// the caller owns layout: it knows where the box is, this package only knows
// what goes inside it.
package cover

import (
	"bytes"
	"fmt"
	"image"
	// The formats the projector accepts: the shipped picture is PNG, and a
	// player pointing the cover at their own scan will be holding a JPEG as
	// often as not.
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"

	"github.com/BAKocska/twixtui/assets"
)

// Depth is how much colour the terminal may be given.
type Depth int

const (
	DepthMono       Depth = iota // no colour at all
	Depth256                     // 256-colour palette
	DepthTrueColour              // 24-bit
)

// Art selects which artwork to draw.
type Art int

const (
	Homage Art = iota // the project's own artwork, always available
	Photo             // a projection of an image file, when one is configured
)

// userPhoto is a player-supplied replacement for the shipped picture. The
// shipped picture itself is decoded once, on the first render that wants it,
// because a menu must not owe its first frame to a PNG decode it may never
// need.
var (
	userPhoto   image.Image
	shippedOnce sync.Once
	shippedImg  image.Image
)

// currentPhoto returns the picture Photo projects: the player's, if one was
// configured, otherwise the shipped reduction. A nil return means the
// embedded asset failed to decode, which a build would have to go out of its
// way to achieve; Render answers it with the homage rather than a blank box.
func currentPhoto() image.Image {
	if userPhoto != nil {
		return userPhoto
	}
	shippedOnce.Do(func() {
		img, _, err := image.Decode(bytes.NewReader(assets.CoverPNG))
		if err == nil {
			shippedImg = img
		}
	})
	return shippedImg
}

// SetPhoto decodes the image at path and makes Photo project it instead of
// the shipped picture. Where the path comes from — a flag, a settings file,
// EnvImage — is the caller's business; this package only insists the file is
// a decodable JPEG or PNG, and says which of those went wrong when it
// refuses one.
func SetPhoto(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	userPhoto = img
	return nil
}

// Render lays the artwork out to fit within w by h character cells and returns
// the lines to draw, which may be fewer and narrower than the box. Styling is
// embedded as ANSI unless depth is DepthMono. It never returns lines wider
// than w or more than h of them.
func Render(w, h int, depth Depth, art Art) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	if art == Photo {
		if img := currentPhoto(); img != nil {
			return renderPhoto(img, w, h, depth)
		}
	}
	return renderHomage(w, h, depth)
}

// MinSize reports the smallest box the artwork is legible in.
//
// The homage bound is where the compact wordmark and a three-peg scene still
// fit; below it the composition stops being the cover and becomes noise, and
// the caller should draw a plain title instead. The photograph bound is
// looser because legibility depends on the picture, but below roughly a
// thousand braille dots no picture survives, so that is where the line is
// drawn.
func MinSize(art Art) (w, h int) {
	if art == Photo {
		return 24, 12
	}
	return 24, 10
}

// Best says which artwork a box deserves, which is the rule the side-by-side
// evaluation of both artworks settled (.work/cover-evaluation.md in the
// development tree). In monochrome the homage always answers: it is drawn
// for runes, where a dithered projection is noise. In colour the projection
// answers once the grid its picture actually occupies is fine enough to keep
// the wordmark and the figure readable — under that, the homage. A player
// who wants the other answer passes their choice to Render; EnvArt overrides
// this default from the environment.
func Best(w, h int, depth Depth) Art {
	switch os.Getenv(EnvArt) {
	case "homage":
		return Homage
	case "photo":
		return Photo
	}
	if depth == DepthMono {
		return Homage
	}
	img := currentPhoto()
	if img == nil {
		return Homage
	}
	if cw, ch := fit(img, w, h); cw < 44 || ch < 22 {
		return Homage
	}
	return Photo
}

// renderPhoto projects the picture.
//
// The projection per depth is the one the evaluation picked: quadrant blocks
// wherever colour exists — on flat, hard-edged art, doubling the spatial
// resolution beats keeping a second full colour per cell at every size tried
// — and braille in mono, where resolution is the only currency left. Half
// blocks and a character ramp were built, compared and dropped; the
// evaluation records what they lost.
func renderPhoto(img image.Image, w, h int, depth Depth) []string {
	if depth == DepthMono {
		return projectBraille(img, w, h)
	}
	return projectQuadrant(img, w, h, depth)
}
