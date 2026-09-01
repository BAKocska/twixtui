package cover

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestABadArtworkNameIsReportedOnceAndNotWhileDrawing is the regression for a
// defect introduced while fixing a quieter one. A misspelt artwork name used to
// be reported from Best, which is called for every frame a menu draws: the
// complaint repeated for as long as the menu was open, and since the program has
// switched the terminal to its alternate screen by then, it was written over the
// picture it was complaining about.
//
// The rule is that nothing on a drawing path reports anything. The complaint
// belongs to ParseEnvironment, which the command line calls once before any of
// this is on screen.
func TestABadArtworkNameIsReportedOnceAndNotWhileDrawing(t *testing.T) {
	t.Setenv(EnvArt, "Photograph")

	problems := ParseEnvironment()
	if len(problems) != 1 {
		t.Fatalf("ParseEnvironment returned %d problems, want the one bad artwork name: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Error(), EnvArt) {
		t.Errorf("the complaint does not name the variable: %v", problems[0])
	}

	// Whatever Best does with the value, it must do it silently. This is the
	// assertion that guards the defect: stability alone would still pass if the
	// choice were announced on every frame.
	out := captureStandardStreams(t, func() {
		first := Best(120, 40, DepthTrueColour)
		for i := range 500 {
			if got := Best(120, 40, DepthTrueColour); got != first {
				t.Fatalf("render %d chose %v, the one before chose %v", i, got, first)
			}
			// A render is the path that mattered: the complaint used to be
			// printed from inside the artwork selection Render performs.
			_ = Render(120, 40, DepthTrueColour, first)
		}
	})
	if out != "" {
		t.Errorf("drawing wrote %d bytes to the terminal:\n%s", len(out), out)
	}
}

// captureStandardStreams runs fn with both standard streams redirected and
// returns whatever it wrote. The cover package must write to neither while
// drawing: by the time a menu is on screen the program has switched the terminal
// to its alternate buffer, so anything written lands on top of the picture.
func captureStandardStreams(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "streams")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
