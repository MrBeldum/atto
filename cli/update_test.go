package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/update"
)

func fakeReleases(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.0.2"}`)
	})
	mux.HandleFunc("/api/tags/edge", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"v0.0.3-dev.14+abc1234"}`)
	})
	for tag, body := range map[string]string{"edge": "edge-binary", "v0.0.2": "stable-binary"} {
		sum := sha256.Sum256([]byte(body))
		mux.HandleFunc("/dl/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), update.Asset())
		})
		mux.HandleFunc("/dl/"+tag+"/"+update.Asset(), func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(update.SetEndpoints(srv.URL+"/api/", srv.URL+"/dl/"))
}

func readSettings(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(config.SettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdateChannelSwitch(t *testing.T) {
	t.Setenv(config.EnvDir, t.TempDir())
	t.Setenv("GOBIN", "")
	fakeReleases(t)
	// A settings field Settings doesn't know must survive the write.
	os.WriteFile(config.SettingsPath(), []byte(`{"defaultModel":"m","futureThing":{"a":1}}`), 0o644)
	exe := filepath.Join(t.TempDir(), "atto")
	os.WriteFile(exe, []byte("old"), 0o755)

	// stable -> edge installs the edge build and saves the channel.
	var out bytes.Buffer
	if err := runUpdate([]string{"-channel", "edge"}, &out, "v0.0.2", exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "edge-binary" {
		t.Fatalf("exe = %q\n%s", b, out.String())
	}
	m := readSettings(t)
	if m["updateChannel"] != "edge" || m["defaultModel"] != "m" || m["futureThing"] == nil {
		t.Fatalf("settings = %v", m)
	}

	// The saved channel now drives a plain `atto update`: already current.
	out.Reset()
	if err := runUpdate(nil, &out, "v0.0.3-dev.14+abc1234", exe); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest edge release") {
		t.Fatal(out.String())
	}

	// Without -channel stable, an edge build is never rolled back.
	out.Reset()
	if err := runUpdate([]string{"-check"}, &out, "v0.0.3-dev.20+abc1234", exe); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest edge release") {
		t.Fatal(out.String())
	}

	// edge -> stable is an explicit downgrade and says so.
	out.Reset()
	if err := runUpdate([]string{"-channel", "stable"}, &out, "v0.0.3-dev.14+abc1234", exe); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Switched to stable: atto v0.0.3-dev.14+abc1234 → v0.0.2") {
		t.Fatal(out.String())
	}
	if b, _ := os.ReadFile(exe); string(b) != "stable-binary" {
		t.Fatalf("exe = %q", b)
	}
	if m := readSettings(t); m["updateChannel"] != "stable" || m["defaultModel"] != "m" {
		t.Fatalf("settings = %v", m)
	}
}

func TestUpdateChannelFlagValidation(t *testing.T) {
	t.Setenv(config.EnvDir, t.TempDir())
	var out bytes.Buffer
	if err := runUpdate([]string{"-channel", "nightly"}, &out, "v0.0.2", "x"); err == nil {
		t.Fatal("unknown channel must fail")
	}
	if _, err := os.Stat(config.SettingsPath()); !os.IsNotExist(err) {
		t.Fatal("a bad channel must not be saved")
	}
}

func TestUpdateCheckDoesNotInstall(t *testing.T) {
	t.Setenv(config.EnvDir, t.TempDir())
	fakeReleases(t)
	exe := filepath.Join(t.TempDir(), "atto")
	os.WriteFile(exe, []byte("old"), 0o755)
	var out bytes.Buffer
	if err := runUpdate([]string{"-check"}, &out, "v0.0.1", exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" || !strings.Contains(out.String(), "v0.0.2 is available") {
		t.Fatalf("%q %s", b, out.String())
	}
}
