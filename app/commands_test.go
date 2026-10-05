package app

import (
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/tui"
)

func TestSuggestionList(t *testing.T) {
	a := &App{editor: tui.NewEditor("› ")}
	a.editor.SetText("/")
	lines := a.renderSuggestions(80)
	if len(lines) != maxSuggestions+1 || !strings.Contains(tui.StripEscapes(lines[maxSuggestions]), "(1/") {
		t.Fatalf("a long list shows %d rows and a position: %q", maxSuggestions, lines)
	}

	a.suggestionKey("up") // wraps to the last command
	m, sel := a.suggestion()
	if sel != len(m)-1 {
		t.Fatalf("up from the top selects the last, got %d", sel)
	}
	lines = a.renderSuggestions(80)
	if !strings.Contains(tui.StripEscapes(lines[maxSuggestions-1]), "/"+m[sel].name) {
		t.Fatalf("the window follows the selection: %q", lines)
	}

	a.editor.SetText("/co") // compact, copy, context
	a.suggestionKey("down")
	a.suggestionKey("tab")
	if got := a.editor.Text(); got != "/copy " {
		t.Fatalf("tab completes the selection: %q", got)
	}

	a.editor.SetText("/go")
	if !a.suggestionKey("enter") || a.editor.Text() != "/goal " {
		t.Fatalf("enter on a command with a required argument only completes it: %q", a.editor.Text())
	}
	a.editor.SetText("/cle")
	if a.suggestionKey("enter") || a.editor.Text() != "/clear" {
		t.Fatalf("enter completes and lets the editor submit: %q", a.editor.Text())
	}

	a.editor.SetText("/")
	a.suggestionKey("escape")
	if len(a.renderSuggestions(80)) != 0 {
		t.Fatal("esc closes the list")
	}
	a.editor.SetText("/m")
	if len(a.renderSuggestions(80)) == 0 {
		t.Fatal("typing reopens it")
	}
}
