package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/tui"
)

// copyText puts text on the clipboard two ways: an OSC 52 sequence, which
// the terminal handles and which works over SSH (iTerm2, Windows Terminal,
// kitty, WezTerm, Ghostty; not Terminal.app), and, when atto runs locally,
// the OS clipboard command. It returns a short note for the user. Call it
// from the UI goroutine.
func (a *App) copyText(text string) string {
	a.ui.WriteRaw(tui.OSC52(text))
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return "sent to your terminal's clipboard (OSC 52)"
	}
	if err := systemCopy(text); err != nil {
		return "sent to the terminal's clipboard (OSC 52); the system clipboard failed: " + err.Error()
	}
	return "copied to the clipboard"
}

// copyCommand is the OS clipboard command for this platform, or nil.
func copyCommand(goos string, have func(string) bool, getenv func(string) string) []string {
	switch goos {
	case "darwin":
		return []string{"pbcopy"}
	case "windows":
		// Set-Clipboard reads base64 from stdin: console encodings would
		// mangle non-ASCII text passed directly.
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"Set-Clipboard -Value ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())))"}
	}
	switch {
	case getenv("WAYLAND_DISPLAY") != "" && have("wl-copy"):
		return []string{"wl-copy"}
	case getenv("DISPLAY") != "" && have("xclip"):
		return []string{"xclip", "-selection", "clipboard"}
	case getenv("DISPLAY") != "" && have("xsel"):
		return []string{"xsel", "--clipboard", "--input"}
	}
	return nil
}

func systemCopy(text string) error {
	have := func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	args := copyCommand(runtime.GOOS, have, os.Getenv)
	if args == nil {
		return errNoClipboard
	}
	input := text
	if runtime.GOOS == "windows" {
		input = base64.StdEncoding.EncodeToString([]byte(text))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return &copyError{msg}
		}
		return err
	}
	return nil
}

type copyError struct{ msg string }

func (e *copyError) Error() string { return e.msg }

var errNoClipboard = &copyError{"no clipboard command (install wl-clipboard or xclip)"}

// cmdCopy copies the last answer as markdown.
func (a *App) cmdCopy(string) {
	text := lastAnswer(a.ui.Body.Children)
	if text == "" {
		a.notice("Nothing to copy yet.")
		return
	}
	a.notice("Last answer (%d chars) %s.", len([]rune(text)), a.copyText(text))
}

// lastAnswer is the text of the last assistant answer in the transcript.
func lastAnswer(children []tui.Component) string {
	for i := len(children) - 1; i >= 0; i-- {
		c := children[i]
		if g, ok := c.(gap); ok { // a.add wraps blocks in a gap
			c = g.Component
		}
		if t, ok := c.(*textBlock); ok {
			if text := strings.TrimSpace(t.text.String()); text != "" {
				return text
			}
		}
	}
	return ""
}
