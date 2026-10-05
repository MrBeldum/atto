package app

import (
	"slices"
	"strings"
	"sync"

	"github.com/sebastianrcnt/atto/core"
	"github.com/sebastianrcnt/atto/extensions"
	"github.com/sebastianrcnt/atto/tui"
)

// extUI is what extensions show: status line items and widgets (bands of
// lines above the input), each under "<extension>/<key>" in the order
// they were first set. Touched on the UI goroutine only.
type extUI struct {
	statusKeys []string
	status     map[string]string
	widgetKeys []string
	widgets    map[string][]string
}

func (u *extUI) setStatus(key, text string) {
	if u.status == nil {
		u.status = map[string]string{}
	}
	if text == "" {
		delete(u.status, key)
		u.statusKeys = slices.DeleteFunc(u.statusKeys, func(k string) bool { return k == key })
		return
	}
	if _, ok := u.status[key]; !ok {
		u.statusKeys = append(u.statusKeys, key)
	}
	u.status[key] = text
}

func (u *extUI) setWidget(key string, lines []string) {
	if u.widgets == nil {
		u.widgets = map[string][]string{}
	}
	if lines == nil {
		delete(u.widgets, key)
		u.widgetKeys = slices.DeleteFunc(u.widgetKeys, func(k string) bool { return k == key })
		return
	}
	if _, ok := u.widgets[key]; !ok {
		u.widgetKeys = append(u.widgetKeys, key)
	}
	u.widgets[key] = lines
}

func (u *extUI) clear(ext string) {
	for _, k := range slices.Clone(u.statusKeys) {
		if strings.HasPrefix(k, ext+"/") {
			u.setStatus(k, "")
		}
	}
	for _, k := range slices.Clone(u.widgetKeys) {
		if strings.HasPrefix(k, ext+"/") {
			u.setWidget(k, nil)
		}
	}
}

// tuiHost is the TUI as extensions see it. Extensions call it from their
// own goroutines while the UI goroutine may be waiting for them (a
// reload, session_end), so it never waits for the UI: each call is queued
// and applied in order by one goroutine of its own.
type tuiHost struct {
	a    *App
	mu   sync.Mutex
	jobs []func()
	wake chan struct{}
}

func newTUIHost(a *App) *tuiHost {
	h := &tuiHost{a: a, wake: make(chan struct{}, 1)}
	go h.run()
	return h
}

func (h *tuiHost) run() {
	for {
		select {
		case <-h.wake:
		case <-h.a.quit:
			return
		}
		for {
			h.mu.Lock()
			if len(h.jobs) == 0 {
				h.mu.Unlock()
				break
			}
			fn := h.jobs[0]
			h.jobs = h.jobs[1:]
			h.mu.Unlock()
			h.a.ui.Do(fn)
		}
	}
}

// do queues fn for the UI goroutine.
func (h *tuiHost) do(fn func()) {
	h.mu.Lock()
	h.jobs = append(h.jobs, fn)
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *tuiHost) HasUI() bool { return true }

func (h *tuiHost) Notify(ext, text, level string) {
	h.do(func() {
		style := tui.Dim
		switch level {
		case "warning":
			style = func(s string) string { return tui.FG(3, s) }
		case "error":
			style = func(s string) string { return tui.FG(1, s) }
		}
		h.a.add(&noticeBlock{text: "[" + ext + "] " + text, style: style})
	})
}

func (h *tuiHost) SetStatus(ext, key, text string) {
	h.do(func() { h.a.extUI.setStatus(ext+"/"+key, text) })
}

func (h *tuiHost) SetWidget(ext, key string, lines []string) {
	h.do(func() { h.a.extUI.setWidget(ext+"/"+key, lines) })
}

func (h *tuiHost) ClearUI(ext string) {
	h.do(func() { h.a.extUI.clear(ext) })
}

func (h *tuiHost) SendMessage(text string) {
	h.do(func() { h.a.sendExtensionMessage(text) })
}

// Ask shows a select list (select, confirm) or a line input in place of
// the editor. Another dialog already open gets the question its default
// answer at once.
func (h *tuiHost) Ask(ext string, q extensions.Question, answer func(any)) {
	h.do(func() {
		a := h.a
		def := func() any {
			if q.Kind == "confirm" {
				return false
			}
			return nil
		}
		if a.modal != nil {
			answer(def())
			return
		}
		title := q.Title + tui.Dim("  ("+ext+")")
		switch q.Kind {
		case "select", "confirm":
			l := &tui.SelectList{Title: title}
			if q.Kind == "confirm" {
				l.Items = []tui.SelectItem{{Label: "Yes", Value: "yes"}, {Label: "No", Value: "no"}}
			}
			for _, o := range q.Options {
				l.Items = append(l.Items, tui.SelectItem{Label: o, Value: o})
			}
			l.OnCancel = func() {
				a.closeModal()
				answer(def())
			}
			l.OnSelect = func(it tui.SelectItem) {
				a.closeModal()
				if q.Kind == "confirm" {
					answer(it.Value == "yes")
					return
				}
				answer(it.Value)
			}
			a.openModal(l)
		default:
			in := &labelInput{title: title, hint: "enter submit  esc cancel"}
			in.onDone = func(ok bool, text string) {
				a.closeModal()
				if !ok {
					answer(nil)
					return
				}
				answer(text)
			}
			a.openModal(labelModal{in})
		}
	})
}

// sendExtensionMessage is atto.sendMessage: it steers a running turn,
// follows a compaction, or starts a turn.
func (a *App) sendExtensionMessage(text string) {
	switch {
	case strings.TrimSpace(text) == "":
	case a.noModel():
	case a.busy && a.runKind == "turn":
		a.steer(text)
	case a.busy:
		a.enqueue(text, nil)
	default:
		a.startTurn(text, nil)
	}
}

// renderWidgets draws the extensions' widgets above the input.
func (a *App) renderWidgets(width int) []string {
	if a.modal != nil || len(a.extUI.widgetKeys) == 0 {
		return nil
	}
	var out []string
	for _, k := range a.extUI.widgetKeys {
		for _, l := range a.extUI.widgets[k] {
			out = append(out, tui.Truncate(" "+l, width, "…"))
		}
	}
	return out
}

// extensionStatus is the status line items extensions set.
func (a *App) extensionStatus() []string {
	var out []string
	for _, k := range a.extUI.statusKeys {
		out = append(out, a.extUI.status[k])
	}
	return out
}

// extensionCommands are the slash commands extensions registered, except
// those a built-in command shadows.
func (a *App) extensionCommands() []command {
	if a.ext == nil {
		return nil
	}
	var out []command
	for _, c := range a.ext.Commands() {
		if slices.ContainsFunc(commands, func(b command) bool { return b.name == c.Name }) {
			continue
		}
		desc := c.Description
		if desc != "" {
			desc += " "
		}
		desc += "(extension " + c.Ext + ")"
		out = append(out, command{c.Name, "[args]", desc, func(a *App, arg string) { a.runExtensionCommand(c.Name, arg) }})
	}
	return out
}

func (a *App) runExtensionCommand(name, arg string) {
	if a.ext == nil || !a.ext.RunCommand(name, arg) {
		a.notice("The extension command /%s is gone (reloaded?).", name)
	}
}

// cmdExtensions lists the extensions, or approves a project extension.
func (a *App) cmdExtensions(arg string) {
	fields := strings.Fields(arg)
	switch {
	case len(fields) == 0:
		var rows []core.Row
		for _, s := range a.collect().Details() {
			if s.Title == "Extensions" {
				rows = s.Rows
			}
		}
		lines := []string{"Extensions (docs: docs/extensions.md; types: atto extensions types):"}
		for _, r := range rows {
			lines = append(lines, "  "+r.Label+"  "+r.Text)
		}
		a.notice("%s", strings.Join(lines, "\n"))
	case fields[0] == "approve" && len(fields) == 2:
		s, err := extensions.Approve(a.cwd, fields[1])
		if err != nil {
			a.errorNotice(err)
			return
		}
		a.notice("Approved %s (%s); reloading.", s.Name, shortPath(s.Path))
		a.requestReload(false)
	default:
		a.notice("Usage: /extensions [approve <name>]")
	}
}
