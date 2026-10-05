// Package docs embeds documentation the binary prints, so it is at hand
// wherever atto is installed.
package docs

import _ "embed"

// Extensions is extensions.md, the guide to writing extensions
// (atto extensions docs).
//
//go:embed extensions.md
var Extensions string
