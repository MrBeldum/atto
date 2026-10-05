package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/server/web/internal/build"
)

// dist/ is committed: it must be what src/ builds to. This compares the
// hash recorded at generation with the sources, so it needs neither the
// network nor Tailwind.
func TestDistIsFresh(t *testing.T) {
	built, err := Built()
	if err != nil {
		t.Fatal(err)
	}
	src, err := build.SourceHash(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	if built.Source != src {
		t.Fatalf("server/web/dist is stale: src/ or gen.go changed since it was built. Run `go generate ./server/web` and commit dist/.")
	}
}

func TestIndexReferencesBundledAssets(t *testing.T) {
	built, err := Built()
	if err != nil {
		t.Fatal(err)
	}
	page, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.js", "app.css"} {
		data, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		v := hex.EncodeToString(sum[:])[:12]
		if built.Assets[name] != v {
			t.Fatalf("%s: build.json says %s, the file hashes to %s", name, built.Assets[name], v)
		}
		if !strings.Contains(string(page), name+"?v="+v) {
			t.Fatalf("index.html does not load %s?v=%s", name, v)
		}
	}
	if !strings.Contains(string(page), `<div id="app">`) {
		t.Fatal("index.html has no mount point")
	}
}
