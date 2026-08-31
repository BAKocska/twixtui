package cli

import "github.com/BAKocska/twixtui/docs"

// The reference documents are embedded once, in the docs package, so that the
// text the command prints and the text in the repository cannot drift apart.
var (
	docsRules      = docs.Rules
	docsProvenance = docs.RulesProvenance
)
