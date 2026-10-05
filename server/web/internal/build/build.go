// Package build is what the web client's generator (server/web/gen.go)
// and its staleness test share: the manifest it writes, and the hash of
// the sources dist/ was built from.
package build

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Manifest is dist/build.json: what dist was built from.
type Manifest struct {
	// Source is SourceHash of the files dist was built from.
	Source   string `json:"source"`
	Preact   string `json:"preact"`
	Tailwind string `json:"tailwind"`
	// Assets maps each asset to the hash its URL carries (app.js?v=…).
	Assets map[string]string `json:"assets"`
}

// SourceHash hashes what dist is built from: gen.go and every file under
// src/, in root (server/web). Line endings are normalized, so a checkout
// that turns LF into CRLF hashes the same.
func SourceHash(root fs.FS) (string, error) {
	files := []string{"gen.go"}
	err := fs.WalkDir(root, "src", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(path.Base(p), ".") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		raw, err := fs.ReadFile(root, f)
		if err != nil {
			return "", err
		}
		raw = []byte(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		h.Write([]byte(f + "\x00"))
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
