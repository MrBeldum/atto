package app

import (
	"os"
	"strings"

	"github.com/sebastianrcnt/atto/clipboard"
	"github.com/sebastianrcnt/atto/tui"
)

// copyText puts text on the clipboard two ways: an OSC 52 sequence, which
// the terminal handles and which works over SSH (iTerm2, Windows Terminal,
// kitty, WezTerm, Ghostty; not Terminal.app), and, when atto runs locally,
// the OS clipboard command (package clipboard). It returns a short note for the user. Call it
// from the UI goroutine.
func (a *App) copyText(text string) string {
	a.ui.WriteRaw(tui.OSC52(text))
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return "sent to your terminal's clipboard (OSC 52)"
	}
	if err := clipboard.WriteText(text); err != nil {
		return "sent to the terminal's clipboard (OSC 52); the system clipboard failed: " + err.Error()
	}
	return "copied to the clipboard"
}

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
