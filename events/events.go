// Package events is a per-session inbox for things that should reach the
// agent without the user typing: background jobs finishing, timers firing,
// monitors matching. Producers are often separate processes (a job
// supervisor, `atto timer` run by the model), so the inbox is a directory:
// one JSON file per event, written atomically, removed when consumed.
//
// The front end (TUI, daemon) drains the inbox: when the agent is idle an
// event starts a turn; when it is busy the event is delivered like a steer,
// after the next tool call. Either way it is appended to the conversation,
// so the prompt prefix (and its cache) is untouched.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/fsutil"
)

// Prefix marks event text in the conversation.
const Prefix = "[atto event] "

type Event struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"` // "job", "timer", "monitor", "goal"
	Text   string    `json:"text"`   // what the model sees (after Prefix)
	Title  string    `json:"title"`  // one-line summary for the UI
}

// Dir is the inbox for a session.
func Dir(session string) string { return filepath.Join(config.Dir(), "inbox", session) }

func randID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeAtomic writes data to path via a temp file and rename, so readers
// never see a partial file.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, data, 0o644)
}

// Push adds an event to a session's inbox.
func Push(session string, e Event) error {
	if session == "" {
		return fmt.Errorf("no session")
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", e.Time.UnixNano(), randID())
	return writeAtomic(filepath.Join(Dir(session), name), data)
}

// Drain removes and returns pending events, oldest first.
func Drain(session string) []Event {
	ents, err := os.ReadDir(Dir(session))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []Event
	for _, n := range names {
		p := filepath.Join(Dir(session), n)
		data, err := os.ReadFile(p)
		_ = os.Remove(p)
		if err != nil {
			continue
		}
		var e Event
		if json.Unmarshal(data, &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Pending reports whether the inbox has events (used by `atto sleep`).
func Pending(session string) bool {
	ents, _ := os.ReadDir(Dir(session))
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			return true
		}
	}
	return false
}

// Wake signals waiters (`atto sleep`, `atto job wait`) that the user sent
// input, so they return early and the agent sees it sooner.
func Wake(session string) {
	if session != "" {
		_ = writeAtomic(filepath.Join(Dir(session), ".wake"), []byte(time.Now().Format(time.RFC3339Nano)))
	}
}

// WokenSince reports whether Wake was called after t.
func WokenSince(session string, t time.Time) bool {
	st, err := os.Stat(filepath.Join(Dir(session), ".wake"))
	return err == nil && st.ModTime().After(t)
}

// Format renders events as one message for the model.
func Format(evs []Event) string {
	var b strings.Builder
	for i, e := range evs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(Prefix + e.Text)
	}
	return b.String()
}
