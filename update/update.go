// Package update installs new atto releases from GitHub: `atto update`,
// and a once-a-day check that tells the user when a release is out. atto
// never updates itself without being asked.
//
// Releases carry one raw binary per platform (atto_<os>_<arch>[.exe]) and
// a checksums.txt; install.sh and install.ps1 use the same layout.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/config"
)

const (
	Repo    = "sebastianrcnt/atto"
	apiURL  = "https://api.github.com/repos/" + Repo + "/releases/latest"
	dlURL   = "https://github.com/" + Repo + "/releases/download/"
	Install = "curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | sh"
)

// Version is set at build time (-ldflags "-X .../update.Version=v0.1.0").
// Builds without it report the module version (go install ...@v0.1.0) or
// "dev".
var Version = ""

func Current() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// Asset is this platform's binary name in a release.
func Asset() string {
	name := "atto_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

var client = &http.Client{Timeout: 60 * time.Second}

// Latest returns the newest release tag.
func Latest(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checking for releases: %s", resp.Status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Tag == "" {
		return "", fmt.Errorf("no release found")
	}
	return r.Tag, nil
}

// Newer reports whether release a is newer than b (v1.2.3 tags; anything
// unparsable, like "dev", is older than every release).
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka:
		return false
	case !okb:
		return true
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "-") // pre-release and pseudo-version suffixes
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Managed returns how to update a binary another tool installed ("" when
// atto may replace it itself).
func Managed(exe string) string {
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/"):
		return "brew upgrade atto"
	case strings.Contains(p, "/go/bin/") || os.Getenv("GOBIN") != "" && strings.HasPrefix(exe, os.Getenv("GOBIN")):
		return "go install github.com/" + Repo + "/cmd/atto@latest"
	}
	return ""
}

// Install downloads tag's binary, checks it against the release's
// checksums.txt and replaces exe with it.
func InstallRelease(ctx context.Context, tag, exe string) error {
	sums, err := fetch(ctx, dlURL+tag+"/checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want, err := checksum(string(sums), Asset())
	if err != nil {
		return err
	}
	bin, err := fetch(ctx, dlURL+tag+"/"+Asset(), 256<<20)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(bin); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: refusing to install", Asset())
	}
	return replace(exe, bin)
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// checksum finds name in a sha256sum-style list.
func checksum(sums, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("the release has no %s", name)
}

// replace swaps exe for data. The new file is written next to exe and
// renamed over it, so a failure never leaves a half-written binary. Windows
// can't overwrite a running executable but can rename it, so the old one
// moves to exe.old first (removed on the next start, see Cleanup).
func replace(exe string, data []byte) error {
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Cleanup removes the binary a Windows update left behind.
func Cleanup() {
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

// checkFile caches the daily release check.
func checkFile() string { return filepath.Join(config.Dir(), "update-check.json") }

type check struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

// Available returns a newer release tag, or "". It asks GitHub at most once
// a day and otherwise answers from the cache, so it is cheap to call at
// startup.
func Available(ctx context.Context) string {
	var c check
	if data, err := os.ReadFile(checkFile()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if time.Since(c.Checked) > 24*time.Hour {
		tag, err := Latest(ctx)
		c.Checked = time.Now() // failures wait a day too
		if err == nil {
			c.Latest = tag
		}
		if data, err := json.Marshal(c); err == nil {
			_ = os.WriteFile(checkFile(), data, 0o644)
		}
	}
	if _, ok := parse(Current()); ok && Newer(c.Latest, Current()) {
		return c.Latest // dev builds aren't told about releases
	}
	return ""
}
