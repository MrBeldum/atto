package app

import (
	"os"
	"strings"

	"atto/config"
	"atto/session"
	"atto/tui"
)

type command struct {
	name string
	args string
	desc string
	run  func(a *App, arg string)
}

var commands []command

func init() {
	commands = []command{
		{"model", "[id]", "Switch model", (*App).cmdModel},
		{"effort", "[level]", "Set reasoning effort (also shift+tab)", (*App).cmdEffort},
		{"compact", "", "Compact the conversation into handoff notes", (*App).cmdCompact},
		{"resume", "", "Resume a saved conversation", (*App).cmdResume},
		{"name", "<name>", "Name this conversation", (*App).cmdName},
		{"rename", "<name>", "Rename this conversation", (*App).cmdName},
		{"archive", "", "Archive this conversation and start a new one", (*App).cmdArchive},
		{"clear", "", "Start a new conversation", (*App).cmdClear},
		{"quit", "", "Exit atto", (*App).cmdQuit},
		{"exit", "", "Exit atto", (*App).cmdQuit},
	}
}

// matchingCommands returns commands matching a partially typed "/name".
func (a *App) matchingCommands() []command {
	t := a.editor.Text()
	if !strings.HasPrefix(t, "/") || strings.ContainsAny(t, " \n") {
		return nil
	}
	var out []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, t[1:]) {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) renderSuggestions(width int) []string {
	if a.modal != nil {
		return nil
	}
	var out []string
	for i, c := range a.matchingCommands() {
		name := "/" + c.name
		if c.args != "" {
			name += " " + c.args
		}
		name += strings.Repeat(" ", max(0, 18-tui.VisibleWidth(name)))
		line := "  " + name + tui.Dim(c.desc)
		if i == 0 {
			line = tui.FG(6, "› "+name) + tui.Dim(c.desc)
		}
		out = append(out, tui.Truncate(line, width, "…"))
	}
	return out
}

func (a *App) runCommand(text string) {
	name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	arg = strings.TrimSpace(arg)
	var match []command
	for _, c := range commands {
		if c.name == name {
			match = []command{c}
			break
		}
		if strings.HasPrefix(c.name, name) {
			match = append(match, c)
		}
	}
	switch len(match) {
	case 0:
		a.notice("Unknown command /%s.", name)
	case 1:
		match[0].run(a, arg)
	default:
		a.notice("Ambiguous command /%s.", name)
	}
}

func (a *App) openModal(m modal) {
	a.modal = m
	a.ui.SetFocus(m)
}

func (a *App) closeModal() {
	a.modal = nil
	a.ui.SetFocus(a.editor)
	a.maybeSendNextQueued()
}

func (a *App) cmdModel(arg string) {
	if arg != "" {
		ref, ok := a.models.Find("", arg)
		if !ok {
			a.notice("Unknown model %q.", arg)
			return
		}
		a.setModel(ref)
		return
	}
	cur := a.model()
	p := &tui.SelectList{Title: "Select model (enter to choose, esc to cancel)", Filterable: true}
	for i, r := range a.models.List() {
		detail := r.ProviderName
		if r.APIKey == "" && r.Provider.Env != nil {
			detail += " (no key: atto auth set " + r.ProviderName + ")"
		}
		if r.Model.ContextWindow > 0 {
			detail += " · " + fmtTokens(r.Model.ContextWindow) + " ctx"
		}
		p.Items = append(p.Items, tui.SelectItem{Label: r.Model.DisplayName(), Detail: detail, Value: r.ProviderName + "/" + r.Model.ID})
		if r.ProviderName == cur.ProviderName && r.Model.ID == cur.Model.ID {
			p.Selected = i
		}
	}
	p.OnCancel = a.closeModal
	p.OnSelect = func(it tui.SelectItem) {
		a.closeModal()
		if ref, ok := a.models.Find("", it.Value); ok {
			a.setModel(ref)
		}
	}
	a.openModal(p)
}

func (a *App) setModel(ref config.ModelRef) {
	a.agent.SetModel(ref)
	err := config.UpdateSettings(map[string]any{
		"defaultProvider": ref.ProviderName,
		"defaultModel":    ref.Model.ID,
		"defaultEffort":   a.effort(),
	})
	if err != nil {
		a.errorNotice(err)
	}
	a.notice("Model set to %s (%s).", ref.Model.DisplayName(), ref.ProviderName)
	a.statusTrigger()
}

func (a *App) cmdEffort(arg string) {
	levels := a.efforts()
	if len(levels) == 0 {
		a.notice("%s has no effort levels.", a.model().Model.DisplayName())
		return
	}
	if arg != "" {
		for _, l := range levels {
			if l == arg {
				a.setEffort(l, true)
				return
			}
		}
		a.notice("Unknown effort %q. Levels: %s.", arg, strings.Join(levels, ", "))
		return
	}
	p := &tui.SelectList{Title: "Reasoning effort (enter to choose, esc to cancel)"}
	for i, l := range levels {
		p.Items = append(p.Items, tui.SelectItem{Label: effortStyle(l), Value: l})
		if l == a.effort() {
			p.Selected = i
		}
	}
	p.OnCancel = a.closeModal
	p.OnSelect = func(it tui.SelectItem) {
		a.closeModal()
		a.setEffort(it.Value, true)
	}
	a.openModal(p)
}

func (a *App) cmdCompact(string) {
	if a.busy {
		a.enqueue("/compact")
		return
	}
	a.runKind = "compact"
	a.recordSettings()
	a.start("Compacting context", a.agent.Compact)
}

// reset clears the transcript and pending input (for /clear and /resume).
func (a *App) reset() {
	a.agent.Reset()
	a.queued, a.pendingSteers, a.queuePaused = nil, nil, false
	a.ctxTokens = 0
	a.ui.Body.Clear()
	a.ui.Redraw()
	a.ui.ScrollToBottom()
	a.addHeader()
}

func (a *App) cmdClear(string) {
	if a.busy {
		a.enqueue("/clear")
		return
	}
	a.reset()
	a.newSession()
	a.notice("Started a new conversation.")
}

func (a *App) cmdQuit(string) { a.doQuit() }

func (a *App) cmdName(arg string) {
	if arg == "" {
		if a.sessName == "" {
			a.notice("This conversation has no name. Usage: /name <name>")
		} else {
			a.notice("This conversation is named %q.", a.sessName)
		}
		return
	}
	a.sessName = arg
	a.sess.Append(session.Entry{Type: session.TypeName, Name: arg})
	a.notice("Named this conversation %q.", arg)
	a.statusTrigger()
}

func (a *App) cmdArchive(string) {
	if a.busy {
		a.notice("Still working — press esc to interrupt first.")
		return
	}
	path := a.sess.Path
	a.sess.Close()
	if _, err := os.Stat(path); err != nil {
		a.notice("Nothing to archive yet.")
		return
	}
	if _, err := session.Archive(path); err != nil {
		a.errorNotice(err)
		return
	}
	a.reset()
	a.newSession()
	a.notice("Archived the conversation. Find it with /resume (shift+tab shows archived).")
}
