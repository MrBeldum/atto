package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
)

// A session a background run is writing is not resumed.
func TestResumeRefusesLockedSession(t *testing.T) {
	work := setup(t)
	w := session.New(work)
	w.Append(session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: "user", Content: "u1"}})
	w.Close()
	if _, err := session.LockFor(w.Path, os.Getppid()); err != nil {
		t.Fatal(err)
	}
	s := New("test", work)
	t.Cleanup(s.Close)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "thread/resume", "params": map[string]any{"threadId": w.ID}})
	resp := s.Handle(context.Background(), b)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "running in the background") {
		t.Fatalf("resume: %+v", resp)
	}
}
