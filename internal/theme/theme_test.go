package theme

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNamesAndGet(t *testing.T) {
	names := Names()
	if len(names) < 4 {
		t.Fatalf("expected at least four themes, got %v", names)
	}
	for _, n := range names {
		th, err := Get(n)
		if err != nil {
			t.Errorf("Get(%q): %v", n, err)
			continue
		}
		if th.Name != n {
			t.Errorf("Get(%q).Name = %q", n, th.Name)
		}
		if th.Summary == "" {
			t.Errorf("theme %q has no summary", n)
		}
	}
	if _, err := Get("nonexistent"); err == nil {
		t.Error("Get accepted an unknown theme")
	}
	// The error should list what is available, since the whole point of the
	// message is to save the player a second command.
	_, err := Get("nonexistent")
	for _, n := range names {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("the error for an unknown theme does not mention %q: %v", n, err)
		}
	}
	if _, err := Get("  CLASSIC  "); err != nil {
		t.Errorf("theme names should be matched case- and space-insensitively: %v", err)
	}
	if _, err := Get(Default); err != nil {
		t.Errorf("the default theme %q does not exist: %v", Default, err)
	}
}

// everyTheme returns the schemes the tests below walk. Each of those tests
// asserts a property of every theme and says nothing at all about a theme it
// was not handed, so an All that quietly returned a subset would leave four
// green tests measuring nothing. Cross-checking against Names is what makes
// that impossible: the two are built from the same table by different code, so
// they can only agree when both are whole, and they are compared in order, so a
// scheme added to the table in the wrong place is caught here too.
func everyTheme(t *testing.T) []Theme {
	t.Helper()
	all, names := All(), Names()
	if len(all) != len(names) {
		t.Fatalf("All() has %d themes and Names() has %d", len(all), len(names))
	}
	for i, th := range all {
		if th.Name != names[i] {
			t.Fatalf("All()[%d] is %q where Names() has %q", i, th.Name, names[i])
		}
	}
	return all
}

// TestColoursAreWellFormed catches a mistyped colour, which is otherwise
// invisible until someone looks at the board on a terminal that renders it.
func TestColoursAreWellFormed(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for _, th := range everyTheme(t) {
		roles := map[string]string{
			"VerticalPeg":    th.VerticalPeg,
			"VerticalLink":   th.VerticalLink,
			"HorizontalPeg":  th.HorizontalPeg,
			"HorizontalLink": th.HorizontalLink,
			"Grid":           th.Grid,
			"BorderRow":      th.BorderRow,
			"Cursor":         th.Cursor,
			"Highlight":      th.Highlight,
			"LastMove":       th.LastMove,
			"Text":           th.Text,
			"Dim":            th.Dim,
			"Warning":        th.Warning,
		}
		if th.Monochrome() {
			for role, value := range roles {
				if value != "" {
					t.Errorf("%s is monochrome but sets %s to %q", th.Name, role, value)
				}
			}
			continue
		}
		for role, value := range roles {
			// Text may be empty, which means the terminal's own foreground is
			// used. That is deliberate: naming a colour there makes a scheme
			// wrong on one end of the background range or the other.
			if value == "" && role == "Text" {
				continue
			}
			if !hex.MatchString(value) {
				t.Errorf("%s.%s = %q, which is not a six-digit lower-case hex colour", th.Name, role, value)
			}
		}
	}
}

// TestPlayersAreDistinguishable checks the one property a theme must have: the
// two sides cannot share a colour, or the board becomes unreadable.
func TestPlayersAreDistinguishable(t *testing.T) {
	for _, th := range everyTheme(t) {
		if th.Monochrome() {
			continue
		}
		if th.VerticalPeg == th.HorizontalPeg {
			t.Errorf("%s gives both players the same peg colour %q", th.Name, th.VerticalPeg)
		}
		if th.VerticalLink == th.HorizontalLink {
			t.Errorf("%s gives both players the same link colour %q", th.Name, th.VerticalLink)
		}
		if th.VerticalPeg == th.Grid || th.HorizontalPeg == th.Grid {
			t.Errorf("%s draws a peg the same colour as an empty hole", th.Name)
		}
	}
}

func TestSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Nothing chosen yet: the default, and no error.
	th, err := Selected(dir)
	if err != nil {
		t.Fatalf("Selected on a fresh directory: %v", err)
	}
	if th.Name != Default {
		t.Errorf("Selected = %q, want the default %q", th.Name, Default)
	}

	chosen, err := Select(dir, "paper")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Name != "paper" {
		t.Errorf("Select returned %q", chosen.Name)
	}
	again, err := Selected(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "paper" {
		t.Errorf("Selected after Select = %q, want paper", again.Name)
	}

	if _, err := Select(dir, "nonexistent"); err == nil {
		t.Error("Select accepted an unknown theme")
	}
	// A refused choice must not have changed the stored one.
	still, _ := Selected(dir)
	if still.Name != "paper" {
		t.Errorf("a refused Select changed the stored theme to %q", still.Name)
	}
}

// TestSelectLeavesNoTemporaryFiles checks the atomic write cleans up after
// itself, so the configuration directory does not fill with debris.
func TestSelectLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	for range 5 {
		if _, err := Select(dir, "slate"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly the settings file, got %d entries", len(entries))
	}
}

// TestSelectedDegradesRatherThanFailing checks a broken settings file does not
// stop a game starting. Losing a colour preference is not worth refusing to run.
func TestSelectedDegradesRatherThanFailing(t *testing.T) {
	cases := map[string]string{
		"not json":       "{{{",
		"unknown theme":  `{"theme":"chartreuse"}`,
		"empty theme":    `{"theme":""}`,
		"wrong shape":    `[]`,
		"empty document": "",
	}
	for name, body := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		th, err := Selected(dir)
		if err == nil {
			t.Errorf("%s: expected a reported reason", name)
		}
		if th.Name != Default {
			t.Errorf("%s: fell back to %q, want the default %q", name, th.Name, Default)
		}
	}
}

func TestMonochrome(t *testing.T) {
	mono, err := Get("mono")
	if err != nil {
		t.Fatal(err)
	}
	if !mono.Monochrome() {
		t.Error("the mono theme does not report itself as monochrome")
	}
	classic, err := Get("classic")
	if err != nil {
		t.Fatal(err)
	}
	if classic.Monochrome() {
		t.Error("a coloured theme reports itself as monochrome")
	}
}

// relativeLuminance returns the perceptual lightness of a hex colour, on the
// scale the web contrast guidelines use: 0 is black, 1 is white.
func relativeLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("not a hex colour: %q", hex)
	}
	channel := func(s string) float64 {
		var v int
		if _, err := fmt.Sscanf(s, "%x", &v); err != nil {
			t.Fatalf("unreadable channel %q in %q", s, hex)
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	r, g, b := channel(hex[1:3]), channel(hex[3:5]), channel(hex[5:7])
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// TestEveryThemeIsLegibleAgainstTheBackgroundItClaims is the invariant the
// default scheme used to break.
//
// No scheme paints a background, so every colour sits on the terminal's own. A
// scheme drawn for a dark terminal therefore cannot use a near-black, and one
// drawn for a light terminal cannot use a near-white. The default scheme did
// both at once: its second player was near-black and its panel text near-white,
// so there was no terminal it was fully legible on. The bounds below are loose
// enough to leave the palettes room and tight enough to catch that.
func TestEveryThemeIsLegibleAgainstTheBackgroundItClaims(t *testing.T) {
	const (
		// Against a dark terminal, anything below this disappears.
		darkFloor = 0.10
		// Against a light terminal, anything above this disappears.
		lightCeiling = 0.62
	)
	for _, th := range everyTheme(t) {
		if th.Monochrome() {
			if th.Suits != AnyBackground {
				t.Errorf("%s sets no colours but claims to suit a particular background", th.Name)
			}
			continue
		}
		if th.Suits == AnyBackground {
			t.Errorf("%s sets colours, so it must say which terminal it is drawn for", th.Name)
			continue
		}
		roles := map[string]string{
			"VerticalPeg":    th.VerticalPeg,
			"VerticalLink":   th.VerticalLink,
			"HorizontalPeg":  th.HorizontalPeg,
			"HorizontalLink": th.HorizontalLink,
			"Grid":           th.Grid,
			"BorderRow":      th.BorderRow,
			"Cursor":         th.Cursor,
			"Highlight":      th.Highlight,
			"LastMove":       th.LastMove,
			"Dim":            th.Dim,
			"Warning":        th.Warning,
		}
		// Text may be empty, which means it inherits the terminal's own
		// foreground and is legible on any background by construction.
		if th.Text != "" {
			roles["Text"] = th.Text
		}
		for role, hex := range roles {
			lum := relativeLuminance(t, hex)
			switch th.Suits {
			case DarkBackground:
				if lum < darkFloor {
					t.Errorf("%s.%s is %s, luminance %.3f, too dark to see on the dark terminal it is drawn for",
						th.Name, role, hex, lum)
				}
			case LightBackground:
				if lum > lightCeiling {
					t.Errorf("%s.%s is %s, luminance %.3f, too light to see on the light terminal it is drawn for",
						th.Name, role, hex, lum)
				}
			}
		}
	}
}

// TestThemeRolesAreToldApart checks the colours a player has to distinguish at a
// glance are actually distinguishable, rather than three shades of one hue.
func TestThemeRolesAreToldApart(t *testing.T) {
	for _, th := range everyTheme(t) {
		if th.Monochrome() {
			continue
		}
		// The cursor, the highlight and the two players all appear on the board
		// at once and each means something different.
		onBoard := map[string]string{
			"vertical peg":   th.VerticalPeg,
			"horizontal peg": th.HorizontalPeg,
			"cursor":         th.Cursor,
			"highlight":      th.Highlight,
			"grid":           th.Grid,
		}
		for aName, a := range onBoard {
			for bName, b := range onBoard {
				if aName >= bName {
					continue
				}
				if a == b {
					t.Errorf("%s: %s and %s are the same colour %s", th.Name, aName, bName, a)
				}
			}
		}
	}
}

// TestSelectCreatesThePrivateConfigDirectory checks this writer agrees with the
// others about the mode. The configuration directory holds profiles, saved games
// and the result log, and whichever writer runs first decides the mode, so a
// single writer using a laxer one is enough to leave it world-readable.
func TestSelectCreatesThePrivateConfigDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "twixtui")
	if _, err := Select(dir, "slate"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("configuration directory created with mode %#o, want 0700", perm)
	}
	settings, err := os.Stat(filepath.Join(dir, settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := settings.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("settings file created with mode %#o, which is readable by others", perm)
	}
}
