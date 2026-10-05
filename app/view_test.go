package app

import (
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/tui"
)

func TestExpanderOverridesDetails(t *testing.T) {
	d := &details{}
	b := &thinkingBlock{expander: expander{d: d}, done: true}
	b.text.WriteString("line one\nline two")
	g := gap{b}

	if len(b.Render(40)) != 1 {
		t.Fatal("should start collapsed")
	}
	g.Click(1) // header line (after the gap line)
	if len(b.Render(40)) < 3 {
		t.Fatal("click should expand")
	}
	g.Click(3)
	if len(b.Render(40)) != 1 {
		t.Fatal("second click should collapse")
	}

	// ctrl+t expands everything; a click on "Show less" still collapses.
	d.on, d.gen = true, d.gen+1
	if !b.expanded() {
		t.Fatal("ctrl+t should expand")
	}
	g.Click(3)
	if b.expanded() {
		t.Fatal("click should collapse even with details on")
	}
	// Toggling ctrl+t again resets per-block choices.
	d.on, d.gen = false, d.gen+1
	d.on, d.gen = true, d.gen+1
	if !b.expanded() {
		t.Fatal("ctrl+t should reset per-block state")
	}
}

func TestThinkingClickWhileStreaming(t *testing.T) {
	b := &thinkingBlock{expander: expander{d: &details{}}}
	b.text.WriteString(strings.Repeat("word ", 400))
	collapsed := len(b.Render(40))
	if !b.Click(0) {
		t.Fatal("streaming thinking should be clickable")
	}
	if expanded := len(b.Render(40)); expanded <= collapsed {
		t.Fatalf("expanded %d <= collapsed %d", expanded, collapsed)
	}
}

func TestAssistantBulletNotDoubled(t *testing.T) {
	b := &textBlock{}
	b.text.WriteString("- apple\n- pear")
	if got := StripLine(b.Render(40)[0]); got != "  • apple" {
		t.Fatalf("got %q", got)
	}
	c := &textBlock{}
	c.text.WriteString("Plain answer.")
	if got := StripLine(c.Render(40)[0]); got != "• Plain answer." {
		t.Fatalf("got %q", got)
	}
}

func StripLine(s string) string { return tui.StripEscapes(s) }
