// Package theme holds the colour schemes the board and the interface are drawn
// with, and remembers which one the player chose.
//
// Colours are stored as data rather than as styles so that this package does not
// depend on the rendering layer: the interface maps these roles onto whatever
// style type it uses. A role left empty means "do not colour this", which is how
// the monochrome theme works and how a terminal that reports no colour support is
// handled without a second code path.
package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Background says which terminal a scheme is drawn for.
//
// No scheme paints a background: pegs and links sit on whatever colour the
// player's terminal already is. A scheme is therefore only legible against one
// end of the range, and saying which one is what stops a scheme being written
// that is legible against neither. The default scheme used to be exactly that:
// its darker player was near-black, invisible on a dark terminal, while its
// panel text was near-white, invisible on a light one.
type Background uint8

// Which terminal a scheme suits.
const (
	// AnyBackground sets no colours at all, so it cannot clash with either.
	AnyBackground Background = iota
	// DarkBackground is drawn to be read against a dark terminal.
	DarkBackground
	// LightBackground is drawn to be read against a light terminal.
	LightBackground
)

// Theme names the colour of every role the interface draws.
type Theme struct {
	Name    string
	Summary string

	// Suits is the terminal this scheme is legible against.
	Suits Background
	// VerticalPeg and HorizontalPeg colour the two players' pegs, and their
	// links take the matching link colour. These two must stay clearly distinct
	// from each other in every theme.
	VerticalPeg  string
	VerticalLink string

	HorizontalPeg  string
	HorizontalLink string

	// Grid is the empty holes and the board frame.
	Grid string
	// BorderRow tints the border rows so a player can see which edges are theirs.
	BorderRow string

	// Cursor is the hole the player is pointing at.
	Cursor string
	// Highlight marks holes the interface is calling out, such as a hint or a
	// tutorial step.
	Highlight string
	// LastMove marks the move just played.
	LastMove string

	// Text, Dim and Warning are the information panel's foreground colours.
	Text    string
	Dim     string
	Warning string
}

// Monochrome reports whether the theme asks for no colour at all.
func (t Theme) Monochrome() bool { return t.VerticalPeg == "" && t.HorizontalPeg == "" }

// themes are the built-in colour schemes. Colours are hex so they render the
// same on any truecolor terminal; a terminal with fewer colours degrades them
// through the rendering layer rather than here.
var themes = []Theme{
	{
		Name:    "classic",
		Summary: "red and indigo, after the printed board game, for a dark terminal",
		Suits:   DarkBackground,

		VerticalPeg:  "#e05252",
		VerticalLink: "#a83b3b",

		// The printed game's second player is black, which cannot be drawn on a
		// dark terminal: it was near-black here and effectively invisible. Indigo
		// keeps the darker, cooler character of that side while staying legible,
		// and the two players are told apart by shape in any case.
		HorizontalPeg:  "#7d8cc4",
		HorizontalLink: "#57639b",

		Grid:      "#7a7a85",
		BorderRow: "#9a9aa5",

		Cursor:    "#f0c040",
		Highlight: "#5fd38d",
		LastMove:  "#e0e0e6",

		// Panel text is left unset so it inherits the terminal's own foreground.
		// Naming a near-white here is what made this scheme unreadable on a light
		// terminal, and the terminal's own choice is right by construction.
		Text:    "",
		Dim:     "#8a8a95",
		Warning: "#e0952a",
	},
	{
		Name:    "slate",
		Summary: "muted blue and amber, for a dark terminal",
		Suits:   DarkBackground,

		VerticalPeg:  "#5fa8d3",
		VerticalLink: "#3d7fa3",

		HorizontalPeg:  "#e0a458",
		HorizontalLink: "#b07c3c",

		Grid:      "#5c6670",
		BorderRow: "#7a848e",

		Cursor:    "#f2f2f2",
		Highlight: "#8ed081",
		LastMove:  "#c9d1d9",

		Text:    "#d7dde3",
		Dim:     "#7d8994",
		Warning: "#e5c07b",
	},
	{
		Name:    "paper",
		Summary: "dark ink, for a light terminal",
		Suits:   LightBackground,

		VerticalPeg:  "#9b2226",
		VerticalLink: "#bb4a4a",

		HorizontalPeg:  "#1d3557",
		HorizontalLink: "#456990",

		Grid:      "#9a9a92",
		BorderRow: "#77776f",

		Cursor:    "#0a6e4a",
		Highlight: "#8a4a00",
		LastMove:  "#3d3d38",

		Text:    "#22221e",
		Dim:     "#6b6b64",
		Warning: "#8a5a00",
	},
	{
		Name:    "mono",
		Summary: "no colour, distinguishes players by shape alone",
		Suits:   AnyBackground,
	},
}

// Default is the theme used when the player has not chosen one.
const Default = "classic"

// Names returns the theme names in a stable order.
func Names() []string {
	out := make([]string, 0, len(themes))
	for _, t := range themes {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// Get returns the named theme.
func Get(name string) (Theme, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range themes {
		if t.Name == name {
			return t, nil
		}
	}
	return Theme{}, fmt.Errorf("unknown theme %q (known: %s)", name, strings.Join(Names(), ", "))
}

// All returns every built-in theme.
func All() []Theme {
	out := make([]Theme, len(themes))
	copy(out, themes)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// settings is the on-disk shape of the player's choice.
type settings struct {
	Theme string `json:"theme"`
}

const settingsFile = "theme.json"

// Selected returns the theme the player chose, falling back to the default when
// nothing has been chosen or the stored name is no longer known. A corrupt or
// unreadable settings file is not worth failing a game over, so it degrades to
// the default and reports the reason.
func Selected(dir string) (Theme, error) {
	raw, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if errors.Is(err, os.ErrNotExist) {
		t, _ := Get(Default)
		return t, nil
	}
	if err != nil {
		t, _ := Get(Default)
		return t, fmt.Errorf("reading theme setting: %w", err)
	}
	var s settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t, _ := Get(Default)
		return t, fmt.Errorf("theme setting is not valid JSON: %w", err)
	}
	t, err := Get(s.Theme)
	if err != nil {
		fallback, _ := Get(Default)
		return fallback, err
	}
	return t, nil
}

// Select records the player's choice. The write is atomic so an interrupted save
// cannot leave an unreadable settings file behind.
func Select(dir, name string) (Theme, error) {
	t, err := Get(name)
	if err != nil {
		return Theme{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Theme{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(settings{Theme: t.Name}, "", "  ")
	if err != nil {
		return Theme{}, err
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, settingsFile+".tmp-*")
	if err != nil {
		return Theme{}, fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return Theme{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Theme{}, err
	}
	if err := tmp.Close(); err != nil {
		return Theme{}, err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, settingsFile)); err != nil {
		return Theme{}, fmt.Errorf("saving the theme setting: %w", err)
	}
	return t, nil
}
