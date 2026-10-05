package app

import (
	"testing"

	"github.com/sebastianrcnt/atto/tui"
)

func TestOSC52(t *testing.T) {
	t.Setenv("TMUX", "")
	if got := tui.OSC52("hi"); got != "\x1b]52;c;aGk=\x07" {
		t.Fatalf("%q", got)
	}
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if got := tui.OSC52("hi"); got != "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\" {
		t.Fatalf("tmux passthrough: %q", got)
	}
}

func TestLastAnswerUnwrapsGap(t *testing.T) {
	b := &textBlock{}
	b.text.WriteString("the answer")
	got := lastAnswer([]tui.Component{gap{b}, gap{&eventBlock{title: "later"}}})
	if got != "the answer" {
		t.Fatalf("%q", got)
	}
}
