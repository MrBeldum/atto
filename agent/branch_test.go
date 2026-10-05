package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
)

// A branch summary is asked for like compaction notes: the conversation
// as it stands, then the instruction, naming where the branch starts.
func TestSummarizeBranch(t *testing.T) {
	srv, seen := fakeServer(t, text("  tried X  "))
	a := newTestAgent(srv.URL)
	msg := func(role, content string) session.Entry {
		return session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: role, Content: content}}
	}
	entries := []session.Entry{msg("user", "u1"), msg("assistant", "a1"), msg("user", "u2\nmore"), msg("assistant", "a2")}
	a.Restore(entries)
	var events []any
	s, err := a.SummarizeBranch(context.Background(), entries[2:], "the tests", func(ev any) { events = append(events, ev) })
	if err != nil || s != "tried X" {
		t.Fatalf("summary %q, %v", s, err)
	}
	req := seen()[0]
	if len(req) != 6 || req[4]["content"] != "a2" {
		t.Fatalf("request should be the whole conversation plus the prompt: %v", req)
	}
	prompt, _ := req[5]["content"].(string)
	if !strings.Contains(prompt, `from the user message that begins "u2 more" onward`) || !strings.HasSuffix(prompt, "Additional focus: the tests") {
		t.Fatalf("prompt %q", prompt)
	}
	if len(a.messages) != 4 {
		t.Fatal("summarizing must not change the conversation")
	}
	if _, ok := events[0].(BranchSummaryStart); !ok {
		t.Fatalf("events %v", events)
	}
	if e, ok := events[len(events)-1].(BranchSummaryEnd); !ok || e.Summary != "tried X" {
		t.Fatalf("events %v", events)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SummarizeBranch(ctx, entries[2:], "", func(any) {}); err != context.Canceled {
		t.Fatalf("canceled: %v", err)
	}
	if HasBranchContent([]session.Entry{{Type: session.TypeBranch}}) || !HasBranchContent(entries[3:]) {
		t.Fatal("HasBranchContent")
	}
}
