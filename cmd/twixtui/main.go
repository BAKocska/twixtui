// Command twixtui plays TwixT in the terminal.
package main

import (
	"os"

	"github.com/BAKocska/twixtui/internal/cli"
)

// Build information, replaced at release time through -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.SetBuildInfo(version, commit, date)
	os.Exit(cli.Execute())
}
