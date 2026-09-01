package cli

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/app"
)

// runInteractive is what the bare command does: ask who is playing, then show
// the menu. Asking for a profile first is deliberate — the leaderboard needs a
// name, and being asked once on launch is less annoying than being asked at the
// end of a game you have just won.
func runInteractive(cmd *cobra.Command, opts *options) error {
	deps, player, err := opts.deps()
	if err != nil {
		return err
	}
	return runScreens(cmd, deps, func(deps app.Deps) (app.Screen, error) {
		if player == "" {
			// Nobody chosen yet: the picker doubles as the first-run path,
			// where it takes a name rather than showing an empty list.
			return app.NewPicker(deps, "Who is playing?"), nil
		}
		return app.NewMenu(deps, player), nil
	})
}

// terminalSize reports the terminal's size, falling back to a conventional
// default when output is not a terminal, which is what happens when a listing is
// piped into a file or a pager.
func terminalSize() (width, height int) {
	const (
		fallbackWidth  = 80
		fallbackHeight = 24
	)
	fd := os.Stdout.Fd()
	if !term.IsTerminal(fd) {
		return fallbackWidth, fallbackHeight
	}
	w, h, err := term.GetSize(fd)
	if err != nil || w <= 0 || h <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return w, h
}

// isTerminal reports whether output written to w reaches a terminal. A listing
// that a person is reading is laid out for the screen it is on; the same listing
// on its way into a file or another program is left as it is.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// stdoutIsTerminal reports whether the process's own output is a terminal, which
// is what decides whether colour is worth emitting at all.
func stdoutIsTerminal() bool {
	return term.IsTerminal(os.Stdout.Fd())
}
