package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/config"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v0.1.10", "v0.1.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.1.0", "dev", true},
		{"dev", "v0.1.0", false},
		{"v0.2.0", "v0.2.0-0.20261005-abcdef", false}, // pseudo-version of the same base
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestChecksum(t *testing.T) {
	sums := "AAAA  atto_linux_amd64\nbbbb *atto_windows_arm64.exe\n"
	if s, err := checksum(sums, "atto_linux_amd64"); err != nil || s != "aaaa" {
		t.Fatal(s, err)
	}
	if s, err := checksum(sums, "atto_windows_arm64.exe"); err != nil || s != "bbbb" {
		t.Fatal(s, err)
	}
	if _, err := checksum(sums, "atto_plan9_386"); err == nil {
		t.Fatal("missing asset must fail")
	}
}

func TestReplace(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "atto")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("got %q", b)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
}

func TestManaged(t *testing.T) {
	t.Setenv("GOBIN", "")
	if Managed("/opt/homebrew/Cellar/atto/0.1.0/bin/atto") == "" || Managed("/Users/x/go/bin/atto") == "" {
		t.Fatal("brew and go install builds are managed")
	}
	if Managed("/Users/x/.local/bin/atto") != "" {
		t.Fatal("the install script's location is ours")
	}
}

func TestAvailableUsesCache(t *testing.T) {
	t.Setenv(config.EnvDir, t.TempDir())
	data, _ := json.Marshal(check{Checked: time.Now(), Latest: "v9.0.0"})
	os.WriteFile(checkFile(), data, 0o644)

	old := Version
	defer func() { Version = old }()
	Version = "v0.1.0"
	if got := Available(context.Background()); got != "v9.0.0" {
		t.Fatalf("got %q", got)
	}
	Version = "dev"
	if got := Available(context.Background()); got != "" {
		t.Fatalf("dev builds get no notice, got %q", got)
	}
}
