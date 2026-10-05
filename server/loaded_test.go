package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/events"
)

// thread/start and thread/resume say what the thread loaded; an `atto
// reload` from its agent is applied and reported to the model and client.
func TestThreadContextAndReload(t *testing.T) {
	var mu sync.Mutex
	var lasts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		lasts = append(lasts, body.Messages[len(body.Messages)-1].Content)
		mu.Unlock()
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("ATTO_DIR", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fake":{"baseUrl":"`+srv.URL+`","models":[{"id":"m","contextWindow":10000}]}}}`), 0o644)
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Be brief."), 0o644)
	s := New("test", cwd)
	t.Cleanup(s.Close)
	var nmu sync.Mutex
	var reloaded []map[string]any
	s.Notify = func(method string, params map[string]any) {
		if method == "thread/reloaded" {
			nmu.Lock()
			reloaded = append(reloaded, params)
			nmu.Unlock()
		}
	}

	th := call(t, s, "thread/start", map[string]any{"model": "fake/m"})
	ctx, _ := th["context"].(map[string]any)
	model, _ := ctx["model"].(map[string]any)
	instr, _ := ctx["instructions"].([]any)
	if model["source"] != "flag" || model["id"] != "fake/m" || len(instr) != 1 {
		t.Fatalf("context %v", th["context"])
	}
	id := th["threadId"].(string)

	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Be very brief."), 0o644)
	if err := events.RequestReload(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(lasts)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reload was not reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	last := lasts[0]
	mu.Unlock()
	if !strings.HasPrefix(last, events.Prefix+"Reload applied. changed AGENTS file") || !strings.Contains(last, "rebuilt") {
		t.Fatalf("model told %q", last)
	}
	nmu.Lock()
	defer nmu.Unlock()
	if len(reloaded) != 1 || reloaded[0]["promptChanged"] != true {
		t.Fatalf("notifications %v", reloaded)
	}
}
