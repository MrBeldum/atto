package app

import (
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

func saveSession(t *testing.T, cwd, text string) string {
	t.Helper()
	w := session.New(cwd)
	w.Append(session.Entry{Type: session.TypeMessage, Message: &provider.Message{Role: "user", Content: text}})
	w.Close()
	return w.Path
}

func TestResumePicker(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	saveSession(t, "/work/a", "fix the parser")
	saveSession(t, "/work/a", "write docs")
	saveSession(t, "/work/b", "elsewhere")

	p := newResumePicker("/work/a", "")
	text := func() string { return tui.StripEscapes(strings.Join(p.Render(80), "\n")) }
	if n := len(p.list.Items); n != 2 {
		t.Fatalf("only this directory's sessions: %d", n)
	}
	if out := text(); !strings.Contains(out, "Search: Type to search") || !strings.Contains(out, "1 messages") {
		t.Fatalf("header, search line and two-line rows: %q", out)
	}

	for _, k := range []string{"d", "o", "c"} { // search
		p.HandleInput(k)
	}
	if n := len(p.list.Visible()); n != 1 {
		t.Fatalf("typing searches: %d", n)
	}
	p.HandleInput("x")
	if out := text(); !strings.Contains(out, "No results for your search") {
		t.Fatalf("empty search: %q", out)
	}
	p.onCancel = func() { t.Fatal("esc clears the search first") }
	p.HandleInput("\x1b")
	p.HandleInput("\x1b[Z") // shift+tab: archived
	if out := text(); !strings.Contains(out, "No archived sessions") {
		t.Fatalf("archived toggle: %q", out)
	}
	p.HandleInput("\x1b[Z")

	p.HandleInput("\t") // all directories
	if n := len(p.list.Items); n != 3 || !strings.Contains(text(), "⌁") {
		t.Fatalf("tab lists all directories: %d", n)
	}

	var archived, picked session.Summary
	p.onArchive = func(s session.Summary) { archived = s; _, _ = session.Archive(s.Path) }
	p.onPick = func(s session.Summary) { picked = s }
	p.HandleInput("\x01") // ctrl+a
	if archived.Path == "" || len(p.list.Items) != 2 {
		t.Fatalf("ctrl+a archives and reloads: %q %d", archived.Path, len(p.list.Items))
	}
	p.HandleInput("\r")
	if picked.Path == "" || picked.Path == archived.Path {
		t.Fatalf("enter picks the selection: %q", picked.Path)
	}

	called := false
	p.onCancel = func() { called = true }
	p.HandleInput("\x1b")
	if !called {
		t.Fatal("esc cancels")
	}
}

func TestResumePickerScrolls(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	for i := 0; i < 9; i++ {
		saveSession(t, "/w", "message "+string(rune('a'+i)))
	}
	p := newResumePicker("/w", "")
	out := strings.Join(p.Render(80), "\n")
	if got := strings.Count(out, "messages"); got != resumeVisible || !strings.Contains(tui.StripEscapes(out), "(1/9)") {
		t.Fatalf("%d rows and an indicator: %q", got, out)
	}
	p.HandleInput("\x1b[A")
	if p.list.Selected != 8 {
		t.Fatalf("up wraps: %d", p.list.Selected)
	}
}
