package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/fsutil"
	"github.com/sebastianrcnt/atto/tui"
)

func TestRankFiles(t *testing.T) {
	files := []fsutil.Entry{
		{Path: "app", Dir: true},
		{Path: "app/app.go"},
		{Path: "app/commands.go"},
		{Path: "tui", Dir: true},
		{Path: "tui/editor.go"},
		{Path: "docs/deep/app.go"},
		{Path: "xaxpxp.txt"},
		{Path: "README.md"},
	}
	rank := func(q string, limit int) []string {
		var out []string
		for _, e := range rankFiles(files, q, limit) {
			out = append(out, e.Path)
		}
		return out
	}
	if got := rank("app", 10); !slices.Equal(got, []string{"app", "app/app.go", "docs/deep/app.go", "xaxpxp.txt"}) {
		t.Errorf("score, then depth, then length: %q", got)
	}
	if got := rank("", 3); !slices.Equal(got, []string{"app", "tui", "README.md"}) {
		t.Errorf("no query: shallowest and shortest first: %q", got)
	}
	if got := rank("tui/ed", 10); !slices.Equal(got, []string{"tui/editor.go"}) {
		t.Errorf("a / matches the whole path: %q", got)
	}
	if got := rank("cmd", 10); !slices.Equal(got, []string{"app/commands.go"}) {
		t.Errorf("fuzzy on the name: %q", got)
	}
	if got := rank("zzz", 10); len(got) != 0 {
		t.Errorf("no match: %q", got)
	}
}

// typeKeys feeds keys the way the TUI does: the app first, then the editor.
func typeKeys(a *App, keys ...string) {
	a.ui.Do(func() {
		for _, k := range keys {
			if !a.onInput(k) {
				a.editor.HandleInput(k)
			}
		}
	})
}

// waitMention renders until the "@" list shows want.
func waitMention(t *testing.T, a *App, want string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lines []string
		a.ui.Do(func() { lines = a.renderSuggestions(80) })
		plain := tui.StripEscapes(strings.Join(lines, "\n"))
		if strings.Contains(plain, want) {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("the list never showed %q: %q", want, plain)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMentionCompletion(t *testing.T) {
	a := testApp(t)
	a.cwd = t.TempDir()
	for _, f := range []string{"src/main.go", "src/util.go", "my docs/notes.md", "README.md", "ignored/x.go"} {
		p := filepath.Join(a.cwd, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(a.cwd, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	typeKeys(a, "l", "o", "o", "k", " ", "@", "m", "a", "i")
	lines := waitMention(t, a, "main.go")
	if plain := tui.StripEscapes(strings.Join(lines, "\n")); !strings.Contains(plain, "src/main.go") || strings.Contains(plain, "x.go") {
		t.Fatalf("name and path, ignored files left out: %q", plain)
	}
	typeKeys(a, "\r")
	var text string
	a.ui.Do(func() { text = a.editor.Text() })
	if text != "look @src/main.go " {
		t.Fatalf("enter inserts the file and a space: %q", text)
	}
	var shown []string
	a.ui.Do(func() { shown = a.renderSuggestions(80) })
	if len(shown) != 0 {
		t.Fatalf("the list closes after a file: %q", shown)
	}

	// A folder: tab inserts it and completion goes on inside it.
	typeKeys(a, "@", "s", "r", "c")
	waitMention(t, a, "src/")
	typeKeys(a, "\t")
	a.ui.Do(func() { text = a.editor.Text() })
	if text != "look @src/main.go @src/" {
		t.Fatalf("tab inserts the folder: %q", text)
	}
	waitMention(t, a, "util.go")

	// A path with a space is quoted.
	typeKeys(a, "\x17", "@", "n", "o", "t", "e") // ctrl+w
	waitMention(t, a, "notes.md")
	typeKeys(a, "\t")
	a.ui.Do(func() { text = a.editor.Text() })
	if text != `look @src/main.go @"my docs/notes.md" ` {
		t.Fatalf("quoted: %q", text)
	}

	// Esc closes the list and leaves the text alone; nothing is expanded
	// on send.
	typeKeys(a, "@", "R")
	waitMention(t, a, "README.md")
	typeKeys(a, "\x1b")
	a.ui.Do(func() { text, shown = a.editor.Text(), a.renderSuggestions(80) })
	if len(shown) != 0 || !strings.HasSuffix(text, "@R") {
		t.Fatalf("esc closes the list: %q %q", text, shown)
	}
	var sent string
	a.ui.Do(func() { sent, _ = a.editor.Commit() })
	if sent != `look @src/main.go @"my docs/notes.md" @R` {
		t.Fatalf("sent as typed: %q", sent)
	}

	// Inside a word nothing opens.
	typeKeys(a, "a", "@", "s")
	a.ui.Do(func() { shown = a.renderSuggestions(80) })
	if len(shown) != 0 {
		t.Fatalf("no list inside a word: %q", shown)
	}
}
