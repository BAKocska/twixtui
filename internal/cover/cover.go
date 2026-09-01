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
	"io"
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
// photoMu guards userPhoto. Rendering is a read and configuring is a write, and
// the two can happen at once as soon as anything offers the picture as a setting
// while a menu is on screen. The shipped picture needs no lock: sync.Once already
// orders its one write against every read.
var (
	photoMu     sync.RWMutex
	userPhoto   image.Image
	shippedOnce sync.Once
	shippedImg  image.Image
)

// currentPhoto returns the picture Photo projects: the player's, if one was
// configured, otherwise the shipped reduction. A nil return means the
// embedded asset failed to decode, which a build would have to go out of its
// way to achieve; Render answers it with the homage rather than a blank box.
func currentPhoto() image.Image {
	photoMu.RLock()
	user := userPhoto
	photoMu.RUnlock()
	if user != nil {
		return user
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

	// The header is read before the pixels, because a file's size on disk says
	// nothing about the allocation decoding it will ask for: a seventy-kilobyte
	// PNG can declare twelve thousand pixels a side and cost hundreds of
	// megabytes, held for the life of the process. A picture is only ever drawn
	// as character cells, so anything past a few thousand pixels a side is
	// detail nobody will see, and refusing it is cheaper than carrying it.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("reading the header of %s: %w", path, err)
	}
	if cfg.Width > maxPhotoSide || cfg.Height > maxPhotoSide {
		return fmt.Errorf("%s is %dx%d, larger than the %d-pixel limit either side; "+
			"a terminal cannot use that detail, so scale it down first",
			path, cfg.Width, cfg.Height, maxPhotoSide)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	photoMu.Lock()
	userPhoto = img
	photoMu.Unlock()
	return nil
}

// maxPhotoSide bounds either dimension of a player-supplied picture. The
// densest grid a terminal offers is a few hundred cells across and the
// projection samples four dots a cell, so a couple of thousand pixels is
// already more than can be resolved.
const maxPhotoSide = 4096

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

// artOverride reports the artwork the environment named, if it named one.
//
// It reads a value the environment was already parsed into rather than the
// environment itself, because Best is called for every frame the menu draws.
// Reading and validating there was how a misspelt name came to be announced on
// every frame, over the picture it was complaining about. Parsing happens once,
// in ParseEnvironment, which the command line calls before anything is on screen.
func artOverride() (Art, bool) {
	envMu.RLock()
	defer envMu.RUnlock()
	return envArt, envArtSet
}

// setArtOverride records the artwork the environment named. Only
// ParseEnvironment calls it.
func setArtOverride(a Art, ok bool) {
	envMu.Lock()
	envArt, envArtSet = a, ok
	envMu.Unlock()
}

var (
	envMu     sync.RWMutex
	envArt    Art
	envArtSet bool
)

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
// evaluation of both artworks settled. That evaluation, the converters that were
// tried and dropped, and the sizes each artwork wins at are written up in
// docs/COVER.md; it used to cite a file in the development tree, which no reader
// of the repository could ever have, since that tree is not published. In monochrome the homage always answers: it is drawn
// for runes, where a dithered projection is noise. In colour the projection
// answers once the grid its picture actually occupies is fine enough to keep
// the wordmark and the figure readable — under that, the homage. A player
// who wants the other answer passes their choice to Render; EnvArt overrides
// this default from the environment.
func Best(w, h int, depth Depth) Art {
	if art, ok := artOverride(); ok {
		return art
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
