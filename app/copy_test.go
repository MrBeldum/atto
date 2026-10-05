package app

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/clipboard"
	"github.com/sebastianrcnt/atto/tui"
)

func TestOSC52(t *testing.T) {
	t.Setenv("TMUX", "")
	if got := tui.OSC52("hi"); got != "\x1b]52;c;aGk=\x07" {
		t.Fatalf("%q", got)
	}
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if got := tui.OSC52("hi"); got != "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\\x1b]52;c;aGk=\x07" {
		t.Fatalf("tmux passthrough: %q", got)
	}
}

// fakeCopy records the copy commands that would run.
type fakeCopy struct {
	env              map[string]string
	clipErr, primErr error
	tmuxFail         map[string]bool // joined args that fail
	ran              []string
}

func (f *fakeCopy) copyEnv() copyEnv {
	return copyEnv{
		getenv:    func(k string) string { return f.env[k] },
		clipboard: func(string) error { f.ran = append(f.ran, "clipboard"); return f.clipErr },
		primary:   func(string) error { f.ran = append(f.ran, "primary"); return f.primErr },
		tmux: func(stdin string, args ...string) error {
			cmd := strings.Join(args, " ")
			f.ran = append(f.ran, "tmux "+cmd)
			if f.tmuxFail[cmd] {
				return errors.New("exit status 1")
			}
			return nil
		},
	}
}

func TestCopyPaths(t *testing.T) {
	noPrimary := clipboard.ErrNoCopyTool
	cases := []struct {
		name string
		f    fakeCopy
		ran  []string
		note string
	}{
		{"local", fakeCopy{primErr: noPrimary}, []string{"clipboard", "primary"},
			"Copied 5 chars (clipboard)"},
		{"local linux", fakeCopy{}, []string{"clipboard", "primary"},
			"Copied 5 chars (clipboard + primary)"},
		{"local tmux", fakeCopy{env: map[string]string{"TMUX": "x"}, primErr: noPrimary},
			[]string{"clipboard", "primary", "tmux load-buffer -w -"},
			"Copied 5 chars (clipboard + tmux buffer)"},
		{"old tmux", fakeCopy{env: map[string]string{"TMUX": "x"}, primErr: noPrimary, tmuxFail: map[string]bool{"load-buffer -w -": true}},
			[]string{"clipboard", "primary", "tmux load-buffer -w -", "tmux load-buffer -"},
			"Copied 5 chars (clipboard + tmux buffer)"},
		{"ssh", fakeCopy{env: map[string]string{"SSH_CONNECTION": "1 2 3 4"}}, nil,
			"Sent 5 chars via OSC 52; check your terminal's clipboard settings if paste fails"},
		{"ssh tmux", fakeCopy{env: map[string]string{"SSH_TTY": "/dev/pts/1", "TMUX": "x"}},
			[]string{"tmux load-buffer -w -"}, "Copied 5 chars (tmux buffer + OSC 52)"},
		{"no tool", fakeCopy{clipErr: clipboard.ErrNoCopyTool, primErr: noPrimary}, []string{"clipboard", "primary"},
			"Sent 5 chars via OSC 52; check your terminal's clipboard settings if paste fails"},
		{"tool fails", fakeCopy{clipErr: errors.New("boom"), primErr: noPrimary}, []string{"clipboard", "primary"},
			"Sent 5 chars via OSC 52; the system clipboard failed: boom"},
	}
	for _, c := range cases {
		r := c.f.copyEnv().run("héllo")
		if !slices.Equal(c.f.ran, c.ran) {
			t.Errorf("%s: ran %q want %q", c.name, c.f.ran, c.ran)
		}
		if got := r.note(); got != c.note {
			t.Errorf("%s: note %q want %q", c.name, got, c.note)
		}
	}
	if got := (copyResult{n: 1, clipboard: true}).note(); got != "Copied 1 char (clipboard)" {
		t.Errorf("singular: %q", got)
	}
}

// rawTerm records what is written to the terminal.
type rawTerm struct {
	nullTerm
	mu  sync.Mutex
	out strings.Builder
}

func (r *rawTerm) Write(s string) { r.mu.Lock(); r.out.WriteString(s); r.mu.Unlock() }

func TestCopySelectionToast(t *testing.T) {
	t.Setenv("TMUX", "")
	term := &rawTerm{}
	f := &fakeCopy{primErr: clipboard.ErrNoCopyTool}
	a := &App{ui: tui.New(term), copyEnv: f.copyEnv()}
	a.ui.Do(func() { a.copySelection("hi") })
	deadline := time.Now().Add(5 * time.Second)
	var text string
	for text == "" && time.Now().Before(deadline) {
		a.ui.Do(func() { text = a.toast.text })
		time.Sleep(5 * time.Millisecond)
	}
	if text != "Copied 2 chars (clipboard)" {
		t.Fatalf("toast %q", text)
	}
	term.mu.Lock()
	out := term.out.String()
	term.mu.Unlock()
	if !strings.Contains(out, tui.OSC52("hi")) {
		t.Fatalf("no OSC 52: %q", out)
	}
	var shown []string
	a.ui.Do(func() { shown = a.renderToast(60) })
	if len(shown) != 1 || !strings.Contains(tui.StripEscapes(shown[0]), text) {
		t.Fatalf("toast line %q", shown)
	}
	// A new toast replaces the old one; an old one disappears.
	a.showToast("second")
	if got := tui.StripEscapes(a.renderToast(60)[0]); got != "  second" {
		t.Fatalf("replaced: %q", got)
	}
	a.toast.until = time.Now().Add(-time.Second)
	if a.renderToast(60) != nil {
		t.Fatal("expired toast still shown")
	}
}

func TestTmuxMouseOff(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var calls [][]string
	run := func(out string, err error) func(...string) (string, error) {
		return func(args ...string) (string, error) {
			calls = append(calls, args)
			return out, err
		}
	}
	if tmuxMouseOff(env(nil), run("off\n", nil)) || len(calls) != 0 {
		t.Fatal("outside tmux: no hint, tmux not run")
	}
	in := env(map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"})
	if !tmuxMouseOff(in, run("off\n", nil)) {
		t.Fatal("mouse off: hint")
	}
	if !slices.Equal(calls[0], []string{"show", "-gv", "mouse"}) {
		t.Fatalf("ran %q", calls[0])
	}
	if tmuxMouseOff(in, run("on\n", nil)) || tmuxMouseOff(in, run("", errors.New("no server"))) {
		t.Fatal("mouse on or tmux failing: no hint")
	}
}

func TestMouseDisabled(t *testing.T) {
	off, on := false, true
	env := func(v string) func(string) string { return func(string) string { return v } }
	cases := []struct {
		setting *bool
		env     string
		want    bool
	}{
		{nil, "", false}, {&on, "", false}, {&off, "", true},
		{nil, "1", true}, {&on, "1", true}, {nil, "0", false},
	}
	for _, c := range cases {
		if got := mouseDisabled(c.setting, env(c.env)); got != c.want {
			t.Errorf("mouseDisabled(%v, %q) = %v", c.setting, c.env, got)
		}
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
