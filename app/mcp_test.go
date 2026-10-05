package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/core"
	"github.com/sebastianrcnt/atto/mcp"
	"github.com/sebastianrcnt/atto/tui"
)

// mcpApp is a TUI whose project has two servers in its .mcp.json waiting
// for approval.
func mcpApp(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	proj := t.TempDir()
	if err := os.Mkdir(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".mcp.json"), []byte(`{"mcpServers": {
		"one": {"command": "run-one", "args": ["--flag"]}, "two": {"command": "run-two"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.agent.Cwd = proj
	a.mcp = core.LoadMCP(a.agent)
	t.Cleanup(func() { a.mcp.Close() })
	return a
}

func statusOf(t *testing.T, a *App, name string) string {
	t.Helper()
	infos, _ := a.mcp.Servers(context.Background())
	for _, in := range infos {
		if in.Name == name {
			return in.Status
		}
	}
	t.Fatalf("no server %s", name)
	return ""
}

// choose answers the open approval prompt with choice and returns its title.
func choose(t *testing.T, a *App, choice string) string {
	t.Helper()
	sel, ok := a.modal.(*tui.SelectList)
	if !ok {
		t.Fatalf("expected the approval prompt, got %T", a.modal)
	}
	for i, it := range sel.Items {
		if it.Value == choice {
			sel.Selected = i
		}
	}
	title := sel.Title
	sel.HandleInput("\r")
	return title
}

func TestMCPApprovalPromptAtSessionStart(t *testing.T) {
	a := mcpApp(t)
	a.ui.Do(func() {
		a.askMCPApprovals()
		title := choose(t, a, mcpAllow)
		if want := "This project wants to start MCP server one: run-one --flag. Allow?"; title != want {
			t.Errorf("title %q, want %q", title, want)
		}
		// The next server is asked right after.
		if title := choose(t, a, mcpDeny); !strings.Contains(title, "server two: run-two") {
			t.Errorf("second title %q", title)
		}
		if a.modal != nil {
			t.Errorf("a prompt is still open: %T", a.modal)
		}
	})
	if got := statusOf(t, a, "one"); got != mcp.NotStarted {
		t.Errorf("one = %s, want approved (not started)", got)
	}
	if got := statusOf(t, a, "two"); got != mcp.DeniedStatus {
		t.Errorf("two = %s", got)
	}
	// Not asked again this run.
	a.ui.Do(func() {
		a.askMCPApprovals()
		if a.modal != nil {
			t.Errorf("asked again about %T", a.modal)
		}
	})
}

func TestMCPApprovalPromptAllowAllAndEsc(t *testing.T) {
	a := mcpApp(t)
	a.ui.Do(func() {
		a.askMCPApprovals()
		choose(t, a, mcpAllowAll)
		if a.modal != nil {
			t.Errorf("allow all still asks: %T", a.modal)
		}
	})
	if statusOf(t, a, "one") != mcp.NotStarted || statusOf(t, a, "two") != mcp.NotStarted {
		t.Errorf("allow all: %s, %s", statusOf(t, a, "one"), statusOf(t, a, "two"))
	}

	// Esc leaves a server unapproved (it is asked about the next session).
	b := mcpApp(t)
	b.ui.Do(func() {
		b.askMCPApprovals()
		b.modal.HandleInput("\x1b")
		b.modal.HandleInput("\x1b")
		if b.modal != nil {
			t.Errorf("esc did not close the prompts: %T", b.modal)
		}
	})
	if got := statusOf(t, b, "one"); got != mcp.NeedsApproval {
		t.Errorf("after esc: %s", got)
	}
}

func TestMCPApprovalPromptWaitsForAFreeScreen(t *testing.T) {
	a := mcpApp(t)
	a.ui.Do(func() {
		other := &tui.SelectList{Title: "something else"}
		a.openModal(other)
		a.askMCPApprovals()
		if a.modal != tui.Component(other) {
			t.Errorf("the approval prompt replaced another dialog: %T", a.modal)
		}
	})
}
