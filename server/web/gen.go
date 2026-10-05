//go:build ignore

// gen builds the web client into dist/: `go generate ./server/web`.
//
// It needs the network once: the Tailwind standalone CLI (for this OS and
// architecture) and Preact's npm tarball are downloaded into the user cache
// directory and checked against the SHA-256 sums pinned below. Node is not
// needed: Tailwind's CLI is a single binary, and esbuild (already one of
// atto's modules) bundles the TypeScript. Types are not checked; esbuild
// only strips them.
//
// Set ATTO_WEBGEN_CACHE to use another cache directory.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/sebastianrcnt/atto/server/web/internal/build"
)

const (
	tailwindVersion = "v4.3.3"
	preactVersion   = "11.0.0"
	preactSHA256    = "37774f9b023f4012d30b15d469b641c9e74510c35d2f3d8393501db887fa3abd"
)

// tailwindSHA256 are the release assets' sums, by GOOS/GOARCH.
var tailwindSHA256 = map[string]struct{ asset, sum string }{
	"darwin/arm64":  {"tailwindcss-macos-arm64", "cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d"},
	"darwin/amd64":  {"tailwindcss-macos-x64", "7922e0953f2110c05976e3bf58f14e643d90427575e766b7d433f5f80cbee7e1"},
	"linux/arm64":   {"tailwindcss-linux-arm64", "55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195"},
	"linux/amd64":   {"tailwindcss-linux-x64", "dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a"},
	"windows/amd64": {"tailwindcss-windows-x64.exe", "e0e260ce048014e9268f6237ff18f8ccf02cef521cbd0ae04e82c2cdf7aa3955"},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	cache, err := cacheDir()
	if err != nil {
		return err
	}
	tw, err := tailwind(cache)
	if err != nil {
		return err
	}
	nodeModules, err := preact(cache)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "atto-webgen")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// CSS: Tailwind scans src/ (see the @source in app.css).
	cssOut := filepath.Join(tmp, "app.css")
	cmd := exec.Command(tw, "-i", filepath.Join("src", "app.css"), "-o", cssOut, "--minify")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailwind: %w", err)
	}
	css, err := os.ReadFile(cssOut)
	if err != nil {
		return err
	}

	// JS: Preact with React's names, so components written for React run.
	res := api.Build(api.BuildOptions{
		EntryPoints:       []string{filepath.Join("src", "main.tsx")},
		Bundle:            true,
		Write:             false,
		Outfile:           "app.js",
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsEndOfFile,
		JSX:               api.JSXAutomatic,
		JSXImportSource:   "preact",
		NodePaths:         []string{nodeModules},
		Alias: map[string]string{
			"react":             "preact/compat",
			"react-dom":         "preact/compat",
			"react/jsx-runtime": "preact/jsx-runtime",
		},
		Banner:   map[string]string{"js": "/*! atto web client. Bundles Preact " + preactVersion + " (MIT, https://preactjs.com) and components adapted from Beautiful UI (MIT, https://www.beautifului.dev); see atto's THIRD_PARTY_NOTICES. */"},
		Define:   map[string]string{"process.env.NODE_ENV": `"production"`},
		LogLevel: api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		return fmt.Errorf("esbuild: %d errors", len(res.Errors))
	}
	var js []byte
	for _, f := range res.OutputFiles {
		if filepath.Base(f.Path) == "app.js" {
			js = f.Contents
		}
	}
	if js == nil {
		return fmt.Errorf("esbuild wrote no app.js")
	}

	source, err := build.SourceHash(os.DirFS("."))
	if err != nil {
		return err
	}
	b := build.Manifest{Source: source, Preact: preactVersion, Tailwind: tailwindVersion,
		Assets: map[string]string{"app.js": short(js), "app.css": short(css)}}
	page, err := os.ReadFile(filepath.Join("src", "index.html"))
	if err != nil {
		return err
	}
	html := strings.NewReplacer("{{js}}", "app.js?v="+b.Assets["app.js"], "{{css}}", "app.css?v="+b.Assets["app.css"]).Replace(string(page))
	manifest, _ := json.MarshalIndent(b, "", "  ")

	if err := os.RemoveAll("dist"); err != nil {
		return err
	}
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"app.js": js, "app.css": css, "index.html": []byte(html), "build.json": append(manifest, '\n')} {
		if err := os.WriteFile(filepath.Join("dist", name), data, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "gen: dist/app.js %d bytes, dist/app.css %d bytes\n", len(js), len(css))
	return nil
}

func short(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:12]
}

func cacheDir() (string, error) {
	dir := os.Getenv("ATTO_WEBGEN_CACHE")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "atto-webgen")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// tailwind returns the path of the pinned Tailwind CLI, downloading it
// on first use.
func tailwind(cache string) (string, error) {
	pin, ok := tailwindSHA256[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("no Tailwind standalone CLI pinned for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	path := filepath.Join(cache, tailwindVersion+"-"+pin.asset)
	if sumFile(path) == pin.sum {
		return path, nil
	}
	url := "https://github.com/tailwindlabs/tailwindcss/releases/download/" + tailwindVersion + "/" + pin.asset
	data, err := download(url, pin.sum)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// preact unpacks the pinned Preact tarball into a node_modules directory
// for esbuild and returns that directory.
func preact(cache string) (string, error) {
	nodeModules := filepath.Join(cache, "preact-"+preactVersion, "node_modules")
	dir := filepath.Join(nodeModules, "preact")
	stamp := filepath.Join(dir, ".sha256")
	if b, err := os.ReadFile(stamp); err == nil && string(b) == preactSHA256 {
		return nodeModules, nil
	}
	data, err := download("https://registry.npmjs.org/preact/-/preact-"+preactVersion+".tgz", preactSHA256)
	if err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name, ok := strings.CutPrefix(h.Name, "package/")
		if !ok || h.Typeflag != tar.TypeReg || strings.Contains(name, "..") {
			continue
		}
		out := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return "", err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(out, body, 0o644); err != nil {
			return "", err
		}
	}
	return nodeModules, os.WriteFile(stamp, []byte(preactSHA256), 0o644)
}

func download(url, sum string) ([]byte, error) {
	fmt.Fprintln(os.Stderr, "gen: downloading", url)
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != sum {
		return nil, fmt.Errorf("%s: SHA-256 %x, want %s", url, got, sum)
	}
	return data, nil
}

func sumFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
