package app

import (
	"slices"
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

func TestCopyCommand(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	has := func(names ...string) func(string) bool {
		return func(n string) bool { return slices.Contains(names, n) }
	}
	none := env(nil)
	if c := copyCommand("darwin", has(), none); c[0] != "pbcopy" {
		t.Fatal(c)
	}
	if c := copyCommand("windows", has(), none); c[0] != "powershell.exe" {
		t.Fatal(c)
	}
	if c := copyCommand("linux", has("wl-copy", "xclip"), env(map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})); c[0] != "wl-copy" {
		t.Fatal(c)
	}
	if c := copyCommand("linux", has("xclip"), env(map[string]string{"DISPLAY": ":0"})); c[0] != "xclip" {
		t.Fatal(c)
	}
	if c := copyCommand("linux", has("xclip"), none); c != nil {
		t.Fatal("no display: no command, OSC 52 only", c)
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
