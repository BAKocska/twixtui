package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BAKocska/twixtui/internal/game"
)

// The small choices a player makes once and then forgets: which rules a new
// game's form starts at, how big the board is, and whether a game against the
// computer offers hints. They are stored per machine, in their own file beside
// theme.json, and deliberately not per profile. The precedent is the theme:
// what suits a terminal — its size, its colours, the board that fits its panes
// — is a property of the machine the terminal is on, and these defaults follow
// the board size more than they follow a person. Per machine also means the
// defaults exist before any profile does, so the form a fresh install walks
// into is already the configured one. The cost is that two people sharing a
// machine share the defaults; each game's form can still answer differently,
// so what they share is only where the highlight starts.

// gameDefaults is the stored shape. The zero value means the documented
// defaults — standard rules, the standard board, hints offered — so a missing
// or unreadable file behaves exactly like a fresh install.
type gameDefaults struct {
	// Rules is a preset name; empty means the standard box rules.
	Rules string `json:"rules,omitempty"`
	// Size is the board; 0 means the standard board.
	Size int `json:"size,omitempty"`
	// Hints says whether a game against the computer offers advice; nil means
	// it does, which is what R15 gives a game with an engine in it.
	Hints *bool `json:"hints,omitempty"`
}

const defaultsFile = "defaults.json"

// loadDefaults reads the stored choices. A corrupt or unreadable file is not
// worth refusing to open the menu over, so it degrades to the documented
// defaults, and a stored value that no longer names anything — a preset that
// was removed, a size outside the legal board — degrades alone rather than
// dragging the readable values down with it.
func loadDefaults(dir string) gameDefaults {
	var d gameDefaults
	raw, err := os.ReadFile(filepath.Join(dir, defaultsFile))
	if err != nil {
		return gameDefaults{}
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return gameDefaults{}
	}
	if d.Rules != "" {
		if _, err := game.Preset(d.Rules); err != nil {
			d.Rules = ""
		}
	}
	if d.Size != 0 && (d.Size < game.MinSize || d.Size > game.MaxSize) {
		d.Size = 0
	}
	return d
}

// save records the choices. The write is atomic for the same reason the theme's
// is: an interrupted save must not leave an unreadable file that then costs the
// player every stored choice at once.
func (d gameDefaults) save(dir string) error {
	// 0700 to agree with every other writer under the configuration directory;
	// whichever of them runs first decides the mode.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, defaultsFile+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, defaultsFile))
}

// ruleset resolves the stored choice into the rules a new game's form starts
// at. It is what keeps F5 true with defaults in the picture: pressing enter
// through the questions gives the game these defaults describe, and with
// nothing stored that is still exactly the game `twixtui play bot` starts.
func (d gameDefaults) ruleset() game.Ruleset {
	rs := game.Std
	if d.Rules != "" {
		if p, err := game.Preset(d.Rules); err == nil {
			rs = p
		}
	}
	if d.Size != 0 {
		rs.Size = d.Size
	}
	return rs
}

// hintsOffered says whether a game against the computer should offer advice.
func (d gameDefaults) hintsOffered() bool { return d.Hints == nil || *d.Hints }
