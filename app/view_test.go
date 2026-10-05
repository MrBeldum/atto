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
	g.Click(4) // "Show less", the last line
	if len(b.Render(40)) != 1 {
		t.Fatal("second click should collapse")
	}

	// ctrl+t expands everything; a click on "Show less" still collapses.
	d.on, d.gen = true, d.gen+1
	if !b.expanded() {
		t.Fatal("ctrl+t should expand")
	}
	b.Render(40) // clicks hit what was last drawn
	g.Click(4)   // "Show less", the last line
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

func TestDisplayLinesTrimsPadding(t *testing.T) {
	got := displayLines("\r\nUptime  Free\r\n3h      3.9\r\n        \r\n        \r\n\r\n\r")
	if len(got) != 2 || got[0] != "Uptime  Free" || got[1] != "3h      3.9" {
		t.Fatalf("%q", got)
	}
	if got := displayLines("  \r\n\r\n"); len(got) != 0 {
		t.Fatalf("blank output has no lines: %q", got)
	}
}

func TestClickTogglesOnlyHeaderAndDisclosure(t *testing.T) {
	b := &toolBlock{expander: expander{d: &details{}}, done: true}
	b.args.Description = "list"
	b.args.Command = "ls"
	b.append("1\n2\n3\n4\n5\n6\n")
	out := b.Render(60) // header, $ ls, 3 output lines, "+ 3 lines"
	if b.Click(1) || b.Click(2) || b.expanded() {
		t.Fatal("a click on the command or the output toggled")
	}
	if !b.Click(len(out)-1) || !b.expanded() {
		t.Fatal("the disclosure line expands")
	}
	out = b.Render(60)
	if b.Click(3) || !b.expanded() {
		t.Fatal("a click in the expanded output toggled")
	}
	if !b.Click(0) || b.expanded() {
		t.Fatal("the header collapses")
	}

	// Nothing more to show: not clickable at all.
	short := &toolBlock{expander: expander{d: &details{}}, done: true}
	short.args.Command = "true"
	short.append("ok\n")
	short.Render(60)
	if short.Click(0) {
		t.Fatal("a block with nothing hidden toggled")
	}

	th := &thinkingBlock{expander: expander{d: &details{}}, done: true}
	th.Render(60)
	if th.Click(0) {
		t.Fatal("empty thinking toggled")
	}
	th.text.WriteString("a\nb")
	th.Render(60)
	if !th.Click(0) {
		t.Fatal("thinking header expands")
	}
	th.Render(60) // header, a, b, Show less
	if th.Click(1) || !th.Click(3) || th.expanded() {
		t.Fatal("thinking: only header and Show less toggle")
	}

	c := &compactBlock{expander: expander{d: &details{}}}
	c.notes.WriteString("notes")
	c.Render(60)
	if !c.Click(0) {
		t.Fatal("compaction header expands")
	}
	n := len(c.Render(60))
	if c.Click(1) || !c.Click(n-1) {
		t.Fatal("compaction: only header and Show less toggle")
	}
}
