// Package assets embeds the artwork the interface ships. Like the docs
// package, it exists so there is exactly one copy: the bytes in the
// repository are the bytes in the binary.
package assets

import _ "embed"

// CoverPNG is the project's reduction of the 1962 box lid composition — the
// picture the cover's Photo art projects. It is 213x320 in sixteen colours:
// the cover only ever becomes character cells, so past about twice the
// densest cell grid more pixels are dead weight in every binary, and flat
// poster art carries no more than a handful of tones to begin with.
// README.md in this directory records what the picture is and how the
// reduction is regenerated.
//
//go:embed cover.png
var CoverPNG []byte
