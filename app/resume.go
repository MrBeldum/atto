package app

import (
	"encoding/json"
	"fmt"
	"github.com/sebastianrcnt/atto/core"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

// resumePicker lists saved sessions, codex-style: newest first, filtered
// to the current directory, type to search. Tab toggles Cwd/All, shift+tab
// toggles Active/Archived, ctrl+a archives (or unarchives) the selection.
// Navigation, search and scrolling are the shared SelectList's; this adds the
// toggles and draws two-line rows.
type resumePicker struct {
	cwd       string
	all       bool
	archived  bool
	list      *tui.SelectList
	current   string // path of the open session, marked in the list
	onPick    func(session.Summary)
	onArchive func(session.Summary)
	onCancel  func()
}

const resumeVisible = 6

func newResumePicker(cwd, current string) *resumePicker {
	p := &resumePicker{cwd: cwd, current: current}
	p.list = &tui.SelectList{
		MaxVisible:   resumeVisible,
		Filterable:   true,
		FilterPrompt: "Search: ",
		FilterHint:   "Type to search",
		RenderRow:    p.row,
	}
	p.list.OnSelect = func(it tui.SelectItem) { p.onPick(it.Data.(session.Summary)) }
	p.list.OnCancel = func() { p.onCancel() }
	p.load()
	return p
}

// load refreshes the sessions from disk; the selection stays in range.
func (p *resumePicker) load() {
	dir := p.cwd
	if p.all {
		dir = ""
	}
	sums, _ := session.List(dir, p.archived)
	p.list.Items = nil
	for _, s := range sums {
		// The label is what searching matches: name, first message and ID.
		p.list.Items = append(p.list.Items, tui.SelectItem{
			Label: strings.Join(strings.Fields(s.Name+" "+s.Preview), " "),
			Value: s.ID,
			Data:  s,
		})
	}
	switch {
	case len(sums) == 0 && p.archived:
		p.list.Empty = "  No archived sessions"
	case len(sums) == 0:
		p.list.Empty = "  No saved sessions"
	default:
		p.list.Empty = "  No results for your search"
	}
}

func (p *resumePicker) HandleInput(data string) {
	switch tui.Key(data) {
	case "tab":
		p.all = !p.all
		p.list.Selected = 0
		p.load()
	case "shift+tab":
		p.archived = !p.archived
		p.list.Selected = 0
		p.load()
	case "ctrl+a":
		if it, ok := p.list.Current(); ok {
			p.onArchive(it.Data.(session.Summary))
			p.load()
		}
	default:
		p.list.HandleInput(data)
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

func toggle(a, b string, second bool) string {
	if second {
		return tui.Dim(a+" | ") + tui.Bold(b)
	}
	return tui.Bold(a) + tui.Dim(" | "+b)
}

func (p *resumePicker) Render(width int) []string {
	archiveKey := "ctrl+a archive"
	if p.archived {
		archiveKey = "ctrl+a unarchive"
	}
	out := []string{
		tui.Bold("Resume a previous session"),
		tui.Truncate(tui.Dim("Filter: ")+toggle("Cwd", "All", p.all)+tui.Dim(" (tab)  Status: ")+toggle("Active", "Archived", p.archived)+tui.Dim(" (shift+tab)"), width, "…"),
		tui.Truncate(tui.Dim("↑↓ select · enter resume · "+archiveKey+" · esc cancel"), width, "…"),
	}
	return append(out, p.list.Render(width)...)
}

// row draws a session as two lines: its title, then when and how long.
func (p *resumePicker) row(it tui.SelectItem, selected bool, width int) []string {
	s := it.Data.(session.Summary)
	preview := strings.Join(strings.Fields(s.Preview), " ")
	title := preview
	if s.Name != "" {
		title = tui.Bold(s.Name) + tui.Dim("  "+preview)
	}
	if title == "" {
		title = "(no message yet)"
	}
	marker := "  "
	if selected {
		marker = tui.FG(6, "› ")
	}
	meta := fmt.Sprintf("    %s · %d messages", relTime(s.Updated), s.Messages)
	if s.Path == p.current {
		meta += " · current"
	}
	if p.all {
		meta += " · ⌁ " + shortPath(s.Cwd)
	}
	return []string{tui.Truncate(marker+title, width, "…"), tui.Truncate(tui.Dim(meta), width, "…")}
}

// cmdResume opens the session picker. It works mid-turn too: picking a
// session interrupts the running turn and switches once it has stopped.
func (a *App) cmdResume(string) {
	p := newResumePicker(a.cwd, a.sess.Path)
	if items := p.list.Items; len(items) > 1 && items[0].Data.(session.Summary).Path == a.sess.Path {
		p.list.Selected = 1 // the open session is first; default to the one before it
	}
	p.onCancel = a.closeModal
	p.onArchive = func(s session.Summary) {
		var err error
		if s.Archived {
			_, err = session.Unarchive(s.Path)
		} else {
			if s.Path == a.sess.Path {
				a.sess.Close() // reopened lazily; a new session starts below
			}
			_, err = session.Archive(s.Path)
			if err == nil && s.Path == a.sess.Path && !a.busy {
				a.reset()
				a.newSession()
				a.notice("Archived the current conversation and started a new one.")
			}
		}
		if err != nil {
			a.errorNotice(err)
		}
	}
	p.onPick = func(s session.Summary) {
		a.closeModal()
		if s.Path == a.sess.Path {
			return // already open
		}
		path := s.Path
		if s.Archived {
			var err error
			if path, err = session.Unarchive(s.Path); err != nil {
				a.errorNotice(err)
				return
			}
		}
		if a.busy {
			a.pendingResume = path
			a.cancel()
			return
		}
		a.resume(path)
	}
	a.openModal(p)
}

// resume loads a session file, restores the agent and redraws the
// transcript, then keeps appending to the same file.
func (a *App) resume(path string) {
	saved, file, err := core.Open(path)
	if err != nil {
		a.errorNotice(err)
		return
	}
	h := saved.Header
	a.leaveSession()
	a.reset()
	a.sess.Close()
	a.sess = file
	core.Bind(a.agent, a.hooks, a.sess, h.Time, true) // the session's own date keeps the prefix cache
	a.setLiveSession(h.ID)
	a.sessionStartHook("resume")
	branch := saved.Branch()
	a.agent.Restore(branch)
	a.ctxTokens = a.agent.ContextTokens()
	a.usage.fromEntries(saved.Entries)
	a.recModel, a.recEffort, a.sessName = "", "", saved.Name
	if ref, ok := a.models.Find("", saved.Model); ok {
		a.agent.SetModel(ref)
		a.recModel = saved.Model
	}
	if saved.Effort != "" {
		a.agent.SetEffort(saved.Effort)
		a.recEffort = saved.Effort
	}
	entries := saved.Entries
	a.replay(branch)
	a.restoreGoal(entries)
	if h.Cwd != a.cwd {
		a.notice("Resumed a session from %s; commands run in %s.", shortPath(h.Cwd), shortPath(a.cwd))
	}
	label := h.Time.Local().Format("2006-01-02 15:04")
	if a.sessName != "" {
		label = fmt.Sprintf("%q (%s)", a.sessName, label)
	}
	a.notice("Resumed session %s.", label)
	a.statusTrigger()
}

// replay rebuilds transcript blocks from session entries: pass the active
// branch (session.Active), not the whole file.
func (a *App) replay(entries []session.Entry) {
	tools := map[string]*toolBlock{}
	for _, e := range entries {
		switch e.Type {
		case session.TypeCompaction:
			c := &compactBlock{auto: e.Auto, before: e.TokensBefore, expander: expander{d: &a.details}}
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
					t := &thinkingBlock{done: true, dur: time.Duration(e.ThinkingMs) * time.Millisecond, expander: expander{d: &a.details}}
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
					b := &toolBlock{args: args, timeout: agent.DefaultBashTimeout, expander: expander{d: &a.details}}
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
