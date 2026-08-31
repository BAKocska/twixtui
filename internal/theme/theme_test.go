package theme

import (
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

// TestColoursAreWellFormed catches a mistyped colour, which is otherwise
// invisible until someone looks at the board on a terminal that renders it.
func TestColoursAreWellFormed(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for _, th := range All() {
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
			if !hex.MatchString(value) {
				t.Errorf("%s.%s = %q, which is not a six-digit lower-case hex colour", th.Name, role, value)
			}
		}
	}
}

// TestPlayersAreDistinguishable checks the one property a theme must have: the
// two sides cannot share a colour, or the board becomes unreadable.
func TestPlayersAreDistinguishable(t *testing.T) {
	for _, th := range All() {
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
