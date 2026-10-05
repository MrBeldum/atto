// Package web is the web client that atto serve and /remote serve: a
// Preact app written in TypeScript (src/), styled with Tailwind, and
// built into dist/ by gen.go. dist/ is committed, so building atto needs
// neither the network nor any of those tools; run `go generate
// ./server/web` after changing src/ (a test fails until you do).
package web

//go:generate go run gen.go

import (
	"embed"
	"encoding/json"
	"io/fs"

	"github.com/sebastianrcnt/atto/server/web/internal/build"
)

//go:embed dist
var dist embed.FS

// FS holds the built client: index.html, app.js, app.css and build.json.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Built reads dist/build.json.
func Built() (build.Manifest, error) {
	var m build.Manifest
	raw, err := fs.ReadFile(FS(), "build.json")
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}
