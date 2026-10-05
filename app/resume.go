package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atto/agent"
	"atto/session"
	"atto/tui"
)

// resumePicker lists saved sessions, codex-style: newest first, filtered
// to the current directory (Tab toggles all), type to search.
type resumePicker struct {
	cwd      string
	all      bool
	query    string
	items    []session.Summary
	selected int
	onPick   func(session.Summary)
	onCancel func()
}

const resumeVisible = 6

func (p *resumePicker) load() {
	dir := p.cwd
	if p.all {
		dir = ""
	}
	p.items, _ = session.List(dir)
	p.selected = 0
}

func (p *resumePicker) filtered() []session.Summary {
	if p.query == "" {
		return p.items
	}
	q := strings.ToLower(p.query)
	var out []session.Summary
	for _, s := range p.items {
		if strings.Contains(strings.ToLower(s.Preview), q) || strings.Contains(s.ID, q) {
			out = append(out, s)
		}
	}
	return out
}

func (p *resumePicker) HandleInput(data string) {
	items := p.filtered()
	switch tui.Key(data) {
	case "up", "ctrl+p":
		p.selected = max(0, p.selected-1)
	case "down", "ctrl+n":
		p.selected = min(max(0, len(items)-1), p.selected+1)
	case "pageup":
		p.selected = max(0, p.selected-resumeVisible)
	case "pagedown":
		p.selected = min(max(0, len(items)-1), p.selected+resumeVisible)
	case "tab":
		p.all = !p.all
		p.load()
	case "enter":
		if p.selected < len(items) {
			p.onPick(items[p.selected])
		}
	case "escape":
		if p.query != "" {
			p.query, p.selected = "", 0
		} else {
			p.onCancel()
		}
	case "ctrl+c":
		p.onCancel()
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query, p.selected = string(r[:len(r)-1]), 0
		}
	default:
		if tui.Printable(data) {
			p.query += data
			p.selected = 0
		}
	}
}

func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 10*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func (p *resumePicker) Render(width int) []string {
	filter := tui.Bold("Cwd") + tui.Dim(" | All")
	if p.all {
		filter = tui.Dim("Cwd | ") + tui.Bold("All")
	}
	out := []string{
		tui.Bold("Resume a previous session"),
		tui.Truncate(tui.Dim("Filter: ")+filter+tui.Dim(" (tab) · ↑↓ select · enter resume · esc cancel"), width, "…"),
	}
	search := tui.Dim("Type to search")
	if p.query != "" {
		search = p.query
	}
	out = append(out, tui.Truncate(tui.Dim("Search: ")+search, width, "…"))

	items := p.filtered()
	switch {
	case len(p.items) == 0:
		return append(out, tui.Dim("  No saved sessions"))
	case len(items) == 0:
		return append(out, tui.Dim("  No results for your search"))
	}
	start := max(0, min(p.selected-resumeVisible/2, len(items)-resumeVisible))
	end := min(len(items), start+resumeVisible)
	for i := start; i < end; i++ {
		s := items[i]
		title := strings.Join(strings.Fields(s.Preview), " ")
		if title == "" {
			title = "(no message yet)"
		}
		marker := "  "
		if i == p.selected {
			marker = tui.FG(6, "› ")
			title = tui.FG(6, title)
		}
		out = append(out, tui.Truncate(marker+title, width, "…"))
		meta := fmt.Sprintf("    %s · %d messages", relTime(s.Updated), s.Messages)
		if p.all {
			meta += " · ⌁ " + shortPath(s.Cwd)
		}
		out = append(out, tui.Truncate(tui.Dim(meta), width, "…"))
	}
	if len(items) > resumeVisible {
		out = append(out, tui.Dim(fmt.Sprintf("  (%d/%d)", p.selected+1, len(items))))
	}
	return out
}

func (a *App) cmdResume(string) {
	if a.busy {
		a.notice("Still working — press esc to interrupt first.")
		return
	}
	p := &resumePicker{cwd: a.cwd}
	p.load()
	p.onCancel = a.closeModal
	p.onPick = func(s session.Summary) {
		a.closeModal()
		a.resume(s.Path)
	}
	a.openModal(p)
}

// resume loads a session file, restores the agent and redraws the
// transcript, then keeps appending to the same file.
func (a *App) resume(path string) {
	h, entries, err := session.Load(path)
	if err != nil {
		a.errorNotice(err)
		return
	}
	a.reset()
	a.sess.Close()
	a.sess = session.Resume(path, h)
	a.agent.Record = a.sess.Append
	a.agent.Restore(entries)
	a.ctxTokens = a.agent.ContextTokens()
	a.recModel, a.recEffort = "", ""

	// Restore the model and effort last used in the session.
	for _, e := range entries {
		switch e.Type {
		case session.TypeModel:
			if ref, ok := a.models.Find(e.Provider, e.Model); ok {
				a.agent.SetModel(ref)
				a.recModel = e.Provider + "/" + e.Model
			}
		case session.TypeEffort:
			a.agent.SetEffort(e.Effort)
			a.recEffort = e.Effort
		}
	}
	a.replay(entries)
	if h.Cwd != a.cwd {
		a.notice("Resumed a session from %s; commands run in %s.", shortPath(h.Cwd), shortPath(a.cwd))
	}
	a.notice("Resumed session from %s.", h.Time.Local().Format("2006-01-02 15:04"))
}

// replay rebuilds transcript blocks from session entries.
func (a *App) replay(entries []session.Entry) {
	tools := map[string]*toolBlock{}
	for _, e := range entries {
		switch e.Type {
		case session.TypeCompaction:
			c := &compactBlock{auto: e.Auto, before: e.TokensBefore, detailed: &a.detailed}
			c.notes.WriteString(e.Notes)
			a.add(c)
		case session.TypeMessage:
			m := e.Message
			if m == nil {
				continue
			}
			switch m.Role {
			case "user":
				a.add(&userBlock{text: m.Content})
			case "assistant":
				if strings.TrimSpace(m.ReasoningContent) != "" {
					t := &thinkingBlock{done: true, dur: time.Duration(e.ThinkingMs) * time.Millisecond, detailed: &a.detailed}
					t.text.WriteString(m.ReasoningContent)
					a.add(t)
				}
				if strings.TrimSpace(m.Content) != "" {
					t := &textBlock{}
					t.text.WriteString(m.Content)
					a.add(t)
				}
				for _, tc := range m.ToolCalls {
					var args agent.BashArgs
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
					if args.Description == "" {
						args.Description = tc.Function.Name
					}
					b := &toolBlock{args: args, timeout: agent.DefaultBashTimeout, detailed: &a.detailed}
					tools[tc.ID] = b
					a.add(b)
				}
			case "tool":
				b := tools[m.ToolCallID]
				if b == nil {
					continue
				}
				b.append(m.Content)
				b.done = true
				if t := e.Tool; t != nil {
					b.res = agent.BashResult{
						ExitCode: t.ExitCode,
						TimedOut: t.TimedOut,
						Canceled: t.Canceled,
						Duration: time.Duration(t.DurationMs) * time.Millisecond,
					}
				}
			}
		}
	}
	// Tool calls with no recorded result were interrupted.
	for _, b := range tools {
		if !b.done {
			b.done, b.res = true, agent.BashResult{Canceled: true, ExitCode: -1}
		}
	}
}
