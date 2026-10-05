package app

import (
	"bytes"
	"strings"
	"testing"

	"atto/provider"
	"atto/session"
)

func TestHistoryGrepShow(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := session.New("/w")
	w.Append(session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: "user", Content: "build it"}})
	w.Append(session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{
		{ID: "c1", Function: provider.FunctionCall{Name: "bash", Arguments: `{"description":"Build","command":"go build ./..."}`}},
	}}})
	w.Append(session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: "tool", ToolCallID: "c1", Content: "ok\nmain.go:3: undefined: Frobnicate\n[exit code 1]"}})
	w.Append(session.Entry{Type: session.TypeCompaction, Notes: "build fails; see References: Frobnicate"})
	w.Close()
	t.Setenv("ATTO_SESSION_ID", w.ID)

	var out bytes.Buffer
	if err := RunHistory([]string{"grep", "-i", "frobnicate"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"#3 tool output: Build: main.go:3: undefined: Frobnicate", "#4 compaction notes:", "2 matching lines in 2 entries"} {
		if !strings.Contains(got, want) {
			t.Errorf("grep output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := RunHistory([]string{"show", "-C", "1", "3"}, &out); err != nil {
		t.Fatal(err)
	}
	got = out.String()
	for _, want := range []string{"── #2 assistant ──\n[Build] $ go build ./...", "── #3 tool output: Build ──", "── #4 compaction notes ──"} {
		if !strings.Contains(got, want) {
			t.Errorf("show output missing %q:\n%s", want, got)
		}
	}
	if err := RunHistory([]string{"show", "99"}, &out); err == nil {
		t.Error("expected error for missing entry")
	}
}
