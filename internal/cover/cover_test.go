package cover

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// boxSizes are the shapes a menu actually offers plus the shapes a resizing
// terminal briefly passes through: the contract's promises have to hold in
// the awkward ones as much as in the pretty ones.
var boxSizes = [][2]int{
	{1, 1}, {2, 2}, {5, 3}, {7, 50}, {200, 4},
	{24, 10}, {24, 12}, {40, 20}, {60, 30}, {80, 24}, {80, 40}, {120, 40}, {120, 60},
}

var depths = []Depth{DepthMono, Depth256, DepthTrueColour}

// testImage is a small synthetic photograph: a bright disc on a dark ground,
// enough structure for every projection to have something to disagree about.
func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 120, 90))
	for y := range 90 {
		for x := range 120 {
			dx, dy := x-60, y-45
			if dx*dx+dy*dy < 30*30 {
				img.SetRGBA(x, y, color.RGBA{220, 180, 90, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{40, 30, 60, 255})
			}
		}
	}
	return img
}

// withPhoto runs a test with a player-supplied picture configured, restoring
// the shipped picture afterwards so tests cannot leak into one another.
func withPhoto(t *testing.T, img image.Image, fn func()) {
	t.Helper()
	userPhoto = img
	defer func() { userPhoto = nil }()
	fn()
}

func TestRenderStaysInsideBox(t *testing.T) {
	check := func(what string, w, h int, depth Depth, art Art) {
		t.Helper()
		lines := Render(w, h, depth, art)
		if len(lines) > h {
			t.Errorf("%s at %dx%d depth %d: %d lines for a %d-row box", what, w, h, depth, len(lines), h)
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("%s at %dx%d depth %d: line %d is %d cells wide: %q", what, w, h, depth, i, got, l)
			}
		}
	}
	for _, s := range boxSizes {
		for _, d := range depths {
			check("homage", s[0], s[1], d, Homage)
			check("shipped picture", s[0], s[1], d, Photo)
		}
	}
	withPhoto(t, testImage(), func() {
		for _, s := range boxSizes {
			for _, d := range depths {
				check("player picture", s[0], s[1], d, Photo)
			}
		}
	})
}

func TestRenderRefusesDegenerateBoxes(t *testing.T) {
	for _, s := range [][2]int{{0, 10}, {10, 0}, {0, 0}, {-3, 7}, {7, -3}} {
		if lines := Render(s[0], s[1], DepthTrueColour, Homage); len(lines) != 0 {
			t.Errorf("a %dx%d box produced %d lines", s[0], s[1], len(lines))
		}
	}
}

func TestMonoCarriesNoEscapes(t *testing.T) {
	check := func(what string, lines []string) {
		t.Helper()
		for i, l := range lines {
			for _, r := range l {
				// A tab is not exempt. It is a control byte a terminal expands
				// by a width the caller cannot predict, so a line carrying one
				// can exceed the box this package promises to stay inside, and
				// the promise is measured in cells. Exempting it left an
				// artwork free to emit tabs and still pass.
				if r == 0x1b || r < 0x20 || r == 0x7f {
					t.Fatalf("%s: monochrome line %d holds control character %#x: %q", what, i, r, l)
				}
			}
		}
	}
	for _, s := range boxSizes {
		check(fmt.Sprintf("homage %dx%d", s[0], s[1]), Render(s[0], s[1], DepthMono, Homage))
		check(fmt.Sprintf("shipped picture %dx%d", s[0], s[1]), Render(s[0], s[1], DepthMono, Photo))
	}
	withPhoto(t, testImage(), func() {
		for _, s := range boxSizes {
			check(fmt.Sprintf("player picture %dx%d", s[0], s[1]), Render(s[0], s[1], DepthMono, Photo))
		}
	})
}

// TestDepthsSpeakTheirOwnANSI pins the dialect per depth: 24-bit colour must
// not leak into the 256-colour rendering, where a terminal that asked for the
// smaller palette may show garbage instead of a nearby colour.
func TestDepthsSpeakTheirOwnANSI(t *testing.T) {
	join := func(lines []string) string { return strings.Join(lines, "\n") }

	tc := join(Render(60, 30, DepthTrueColour, Homage))
	if !strings.Contains(tc, ";2;") {
		t.Error("the true-colour homage carries no 24-bit sequences")
	}
	p256 := join(Render(60, 30, Depth256, Homage))
	if !strings.Contains(p256, ";5;") {
		t.Error("the 256-colour homage carries no palette sequences")
	}
	if strings.Contains(p256, ";2;") {
		t.Error("the 256-colour homage leaks 24-bit sequences")
	}
	if mono := join(Render(60, 30, DepthMono, Homage)); strings.Contains(mono, "\x1b") {
		t.Error("the monochrome homage carries escapes")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a := Render(80, 24, DepthTrueColour, Homage)
	b := Render(80, 24, DepthTrueColour, Homage)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Error("two renders of the same homage differ")
	}
	withPhoto(t, testImage(), func() {
		a := Render(80, 24, Depth256, Photo)
		b := Render(80, 24, Depth256, Photo)
		if strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Error("two renders of the same photograph differ")
		}
	})
}

// TestHomageLegibleAtMinSize holds the composition to MinSize's word: at the
// smallest size the box promises, the wordmark's five letters stand apart on
// the top row and every element of the scene — horizon, board holes, both
// players' pegs — has survived, in monochrome, the weakest rendering there
// is.
func TestHomageLegibleAtMinSize(t *testing.T) {
	w, h := MinSize(Homage)
	lines := Render(w, h, DepthMono, Homage)
	if len(lines) != h {
		t.Fatalf("the homage fills %d of the %d rows it asked for", len(lines), h)
	}

	if letters := len(strings.Fields(lines[0])); letters < 5 {
		t.Errorf("the wordmark row splits into %d groups, want at least the 5 letters: %q", letters, lines[0])
	}

	all := strings.Join(lines, "\n")
	for _, want := range []struct {
		glyph string
		what  string
	}{
		{"█", "solid peg or letter strokes"},
		{"▓", "the black player's shaded pegs"},
		{"▄", "the board's horizon"},
		{"•", "the board's peg holes"},
	} {
		if !strings.Contains(all, want.glyph) {
			t.Errorf("the minimum-size homage lost %s (%q)", want.what, want.glyph)
		}
	}
}

func TestMinSizeIsSane(t *testing.T) {
	for _, art := range []Art{Homage, Photo} {
		w, h := MinSize(art)
		if w <= 0 || h <= 0 {
			t.Errorf("MinSize(%d) = %dx%d", art, w, h)
		}
	}
}

// TestShippedPictureRenders holds Photo to its word: with nothing configured
// it projects the embedded reduction, not the homage and not a blank box.
func TestShippedPictureRenders(t *testing.T) {
	userPhoto = nil
	got := strings.Join(Render(60, 30, DepthTrueColour, Photo), "\n")
	if got == "" {
		t.Fatal("the shipped picture rendered to nothing")
	}
	if got == strings.Join(Render(60, 30, DepthTrueColour, Homage), "\n") {
		t.Error("the shipped picture renders as the homage; the embedded asset did not decode")
	}
}

// TestBestFollowsTheEvaluation pins the default choice per box and depth to
// the rule the evaluation settled: homage in monochrome and in boxes too
// coarse for the picture, the projection once the grid can carry it, and the
// environment override beating both.
func TestBestFollowsTheEvaluation(t *testing.T) {
	userPhoto = nil
	cases := []struct {
		w, h  int
		depth Depth
		want  Art
	}{
		{80, 40, DepthTrueColour, Photo},
		{120, 60, DepthTrueColour, Photo},
		{80, 40, Depth256, Photo},
		{80, 24, DepthTrueColour, Homage},
		{40, 20, DepthTrueColour, Homage},
		{24, 10, DepthTrueColour, Homage},
		{80, 40, DepthMono, Homage},
		{120, 60, DepthMono, Homage},
	}
	for _, c := range cases {
		if got := Best(c.w, c.h, c.depth); got != c.want {
			t.Errorf("Best(%d, %d, depth %d) = %d, want %d", c.w, c.h, c.depth, got, c.want)
		}
	}
	t.Setenv(EnvArt, "homage")
	ParseEnvironment()
	if got := Best(120, 60, DepthTrueColour); got != Homage {
		t.Errorf("Best ignores %s=homage", EnvArt)
	}
	t.Setenv(EnvArt, "photo")
	ParseEnvironment()
	if got := Best(24, 10, DepthMono); got != Photo {
		t.Errorf("Best ignores %s=photo", EnvArt)
	}
}

func TestSetPhotoAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, testImage()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer func() { userPhoto = nil }()

	t.Setenv(EnvImage, "")
	if ok, err := FromEnvironment(); ok || err != nil {
		t.Errorf("an unset %s should be quietly absent, got ok=%v err=%v", EnvImage, ok, err)
	}
	t.Setenv(EnvImage, filepath.Join(dir, "no-such-file.png"))
	if ok, err := FromEnvironment(); ok || err == nil {
		t.Errorf("a dangling %s should be reported, got ok=%v err=%v", EnvImage, ok, err)
	}
	t.Setenv(EnvImage, path)
	if ok, err := FromEnvironment(); !ok || err != nil {
		t.Fatalf("FromEnvironment with a valid image: ok=%v err=%v", ok, err)
	}

	got := strings.Join(Render(60, 30, DepthTrueColour, Photo), "\n")
	want := strings.Join(Render(60, 30, DepthTrueColour, Homage), "\n")
	if got == want {
		t.Error("a configured photograph still renders the homage")
	}

	if err := SetPhoto(filepath.Join(dir, "cover.txt")); err == nil {
		t.Error("SetPhoto accepted a path that does not exist")
	}
	junk := filepath.Join(dir, "junk.png")
	if err := os.WriteFile(junk, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPhoto(junk); err == nil {
		t.Error("SetPhoto accepted a file that is not an image")
	}
}
