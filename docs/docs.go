// Package docs embeds the project's reference documents so that the command line
// can print them without needing the repository or a network connection.
//
// The documents live here rather than in a copy under the command's own package
// so that there is exactly one version of each: what a reader sees on the
// repository page is byte-for-byte what `twixtui rules show` prints.
package docs

import _ "embed"

// Rules is the player-facing rules of the game as twixtui implements them.
//
//go:embed rules.md
var Rules string

// RulesProvenance records which source supports which rule, and where the
// sources disagree.
//
//go:embed RULES-PROVENANCE.md
var RulesProvenance string
