// Package app is the interactive terminal front end: it wires the agent's
// events into TUI components and handles input and slash commands.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atto/agent"
	"atto/config"
	"atto/session"
	"atto/tui"
)

const Version = "0.0.3-dev"

type Options struct {
	Inline   bool   // render inline instead of fullscreen
	Continue bool   // resume the latest session in this directory
	Resume   bool   // open the resume picker at startup
	Model    string // provider/id to use instead of the default
}

// modal is a picker shown in place of the editor.
type modal interface {
	tui.Component
	tui.InputHandler
}

type App struct {
	ui     *tui.TUI
	models config.ModelsFile
	agent  *agent.Agent
	sess   *session.Writer

	editor *tui.Editor
	modal  modal

	busy     bool
	runKind  string // "turn" or "compact" while busy
	cancel   context.CancelFunc
	runStart time.Time
	activity string
	// ctxTokens mirrors the agent's context estimate; updated from events so
	// rendering never reads agent state while a turn runs.
	ctxTokens int

	// Codex-style pending input: Enter during a turn steers it (delivered
	// after the next tool call); Tab queues a follow-up turn.
	pendingSteers            []string
	queued                   []string
	sendSteersAfterInterrupt bool
	queuePaused              bool

	// Blocks receiving the current stream.
	thinking *thinkingBlock
	text     *textBlock
	tools    map[string]*toolBlock
	compact  *compactBlock

	// details expands every collapsible block (ctrl+t).
	details details
	// Last model/effort written to the session, to record changes.
	recModel, recEffort string
	sessName            string
	// pendingResume is a session to switch to once the running turn stops.
	pendingResume string

	// Status line state.
	gitBranch   string
	statusCmd   bool     // a custom statusLine command is configured
	statusLines []string // its latest output
	statusWake  chan struct{}

	cwd      string
	quit     chan struct{}
	quitOnce sync.Once
}

func Run(opts Options) error {
	if err := config.Ensure(); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("%s: %w", config.SettingsPath(), err)
	}
	models, err := config.LoadModels()
	if err != nil {
		return fmt.Errorf("%s: %w", config.ModelsPath(), err)
	}
	all := models.List()
	if len(all) == 0 {
		return fmt.Errorf("no models configured; add a provider to %s", config.ModelsPath())
	}
	model, ok := pickModel(models, settings, opts.Model)
	if !ok && opts.Model != "" {
		return fmt.Errorf("unknown model %q (see: atto models)", opts.Model)
	}
	if !ok {
		model = all[0]
	}
	effort := settings.DefaultEffort
	if effort == "" {
		effort = "medium"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	a := &App{
		ui:     tui.New(tui.NewProcessTerminal()),
		models: models,
		agent:  agent.New(model, effort, cwd),
		tools:  map[string]*toolBlock{},
		cwd:    cwd,
		quit:   make(chan struct{}),
	}
	if opts.Inline || settings.Renderer == "inline" {
		a.ui.Mode = tui.Inline
	}
	a.build()
	a.newSession()
	a.statusCmd = settings.StatusLine != nil && settings.StatusLine.Command != ""
	a.startStatusLine(settings.StatusLine)
	if config.CatalogStale() {
		go a.refreshCatalog()
	}

	switch {
	case opts.Continue:
		if s, ok := session.Latest(cwd); ok {
			a.resume(s.Path)
		} else {
			a.notice("No previous session in this directory.")
		}
	case opts.Resume:
		a.cmdResume("")
	}

	if err := a.ui.Start(); err != nil {
		return err
	}
	<-a.quit
	a.ui.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
	})
	a.ui.Stop()
	a.sess.Close()
	return nil
}

func (a *App) build() {
	a.editor = tui.NewEditor(tui.FG(6, "› "))
	a.editor.Rule = tui.Dim
	a.editor.OnSubmit = a.submit

	a.ui.Footer.Add(tui.Func(a.renderActivity), tui.Func(a.renderPending), tui.Func(a.renderInput), tui.Func(a.renderSuggestions), tui.Func(a.renderStatus))
	a.ui.SetFocus(a.editor)
	a.ui.OnInput = a.onInput
	a.ui.PaddingX = 1
	a.ui.GapY = 1
	a.addHeader()
}

// newSession starts recording into a fresh session file.
func (a *App) newSession() {
	a.sess.Close()
	a.sess = session.New(a.cwd)
	a.agent.Record = a.sess.Append
	a.agent.SetStart(time.Now())
	a.agent.SetSession(a.sess.ID, sessionEnv(a.sess.ID))
	a.recModel, a.recEffort, a.sessName = "", "", ""
	a.statusTrigger()
}

// sessionEnv lets commands the agent runs find this session ("atto history")
// and the atto binary itself.
func sessionEnv(id string) []string {
	env := []string{"ATTO_SESSION_ID=" + id}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func (a *App) model() config.ModelRef {
	m, _ := a.agent.Current()
	return m
}

func (a *App) effort() string {
	_, e := a.agent.Current()
	return e
}

func (a *App) addHeader() {
	a.ui.Body.Add(tui.Func(func(width int) []string {
		return []string{
			tui.Truncate(tui.Bold("atto")+tui.Dim(" "+Version+"  ·  "+a.model().Model.DisplayName()), width, "…"),
			tui.Truncate(tui.Dim("/ commands · enter steer · tab queue · shift+tab effort · ctrl+t details · esc interrupt"), width, "…"),
		}
	}))
}

func (a *App) add(c tui.Component) { a.ui.Body.Add(gap{c}) }

func (a *App) notice(format string, args ...any) {
	a.add(&noticeBlock{text: fmt.Sprintf(format, args...), style: tui.Dim})
}

func (a *App) errorNotice(err error) {
	a.add(&noticeBlock{text: "Error: " + err.Error(), style: func(s string) string { return tui.FG(1, s) }})
}

func (a *App) doQuit() { a.quitOnce.Do(func() { close(a.quit) }) }

// --- input ---

func (a *App) onInput(data string) bool {
	if a.modal != nil {
		return false // the focused modal handles everything
	}
	switch tui.Key(data) {
	case "shift+tab":
		a.cycleEffort()
		return true
	case "ctrl+t":
		a.details.on = !a.details.on
		a.details.gen++
		return true
	case "escape":
		if a.busy {
			if len(a.pendingSteers) > 0 {
				a.sendSteersAfterInterrupt = true
			}
			a.cancel()
			return true
		}
	case "ctrl+c":
		switch {
		case a.busy:
			a.cancel()
		case a.editor.Text() != "":
			a.editor.SetText("")
		default:
			a.doQuit()
		}
		return true
	case "ctrl+d":
		if a.editor.Text() == "" && !a.busy {
			a.doQuit()
			return true
		}
	case "ctrl+l":
		a.ui.Redraw()
		return true
	case "tab":
		if m := a.matchingCommands(); len(m) > 0 {
			a.editor.SetText("/" + m[0].name + " ")
			return true
		}
		if strings.TrimSpace(a.editor.Text()) != "" {
			a.queueFromEditor()
			return true
		}
	case "shift+left":
		if len(a.queued) > 0 {
			a.editLastQueued()
			return true
		}
	}
	return false
}

func (a *App) submit(text string) {
	a.ui.ScrollToBottom()
	switch {
	case text == "":
		// Enter on an empty prompt resumes a paused queue.
		if !a.busy && len(a.queued) > 0 {
			a.queuePaused = false
			a.maybeSendNextQueued()
		}
	case strings.HasPrefix(text, "/"):
		a.runCommand(text)
	case a.busy && a.runKind == "turn":
		a.steer(text)
	case a.busy:
		a.enqueue(text)
	default:
		a.startTurn(text)
	}
}

// recordSettings writes model/effort entries when they changed since the
// last one, so a resumed session picks them back up.
func (a *App) recordSettings() {
	m, e := a.agent.Current()
	if id := m.ProviderName + "/" + m.Model.ID; id != a.recModel {
		a.sess.Append(session.Entry{Type: session.TypeModel, Provider: m.ProviderName, Model: m.Model.ID})
		a.recModel = id
	}
	if e != a.recEffort {
		a.sess.Append(session.Entry{Type: session.TypeEffort, Effort: e})
		a.recEffort = e
	}
}

func (a *App) startTurn(text string) {
	a.add(&userBlock{text: text})
	a.runKind = "turn"
	a.recordSettings()
	a.start("Thinking", func(ctx context.Context, emit func(any)) error {
		return a.agent.Run(ctx, text, emit)
	})
}

// start runs fn in the background, routing its events into the UI.
func (a *App) start(activity string, fn func(context.Context, func(any)) error) {
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.cancel = true, cancel
	a.runStart, a.activity = time.Now(), activity

	go func() { // keep the spinner and timers moving
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.ui.RequestRender()
			}
		}
	}()
	go func() {
		err := fn(ctx, func(ev any) { a.ui.Do(func() { a.onEvent(ev) }) })
		ctxTokens := a.agent.ContextTokens() // safe: the run is over
		a.ui.Do(func() {
			a.endStream()
			if a.compact != nil && a.compact.running { // failed or canceled
				a.ui.Body.Remove(gap{a.compact})
				a.compact = nil
			}
			a.busy = false
			a.ctxTokens = ctxTokens
			cancel()
			a.cancel = nil
			switch {
			case errors.Is(err, context.Canceled):
				a.notice("Interrupted.")
			case err != nil:
				a.errorNotice(err)
			}
			if werr := a.sess.Err(); werr != nil {
				a.errorNotice(fmt.Errorf("saving session: %w", werr))
			}
			a.statusTrigger()
			a.afterRun(err)
		})
	}()
}

func (a *App) endStream() {
	if a.thinking != nil {
		a.thinking.finish()
	}
	a.thinking, a.text = nil, nil
}

func (a *App) onEvent(ev any) {
	switch e := ev.(type) {
	case agent.ReasoningDelta:
		if a.thinking == nil {
			a.thinking = &thinkingBlock{start: time.Now(), expander: expander{d: &a.details}}
			a.add(a.thinking)
		}
		a.thinking.text.WriteString(e.Text)
	case agent.TextDelta:
		if a.thinking != nil {
			a.thinking.finish()
		}
		if a.text == nil {
			if strings.TrimSpace(e.Text) == "" {
				return
			}
			a.text = &textBlock{}
			a.add(a.text)
		}
		a.text.text.WriteString(e.Text)
	case agent.ToolStart:
		a.endStream()
		b := &toolBlock{args: e.Args, timeout: e.Timeout, start: time.Now(), expander: expander{d: &a.details}}
		a.tools[e.ID] = b
		a.add(b)
		a.activity = e.Args.Description
	case agent.ToolOutput:
		if b := a.tools[e.ID]; b != nil {
			b.append(e.Chunk)
		}
	case agent.ToolEnd:
		if b := a.tools[e.ID]; b != nil {
			b.done, b.res = true, e.Result
			delete(a.tools, e.ID)
		}
		a.activity = "Thinking"
	case agent.StepEnd:
		a.endStream()
		a.ctxTokens = e.Context
		a.statusTrigger()
	case agent.SteerCommitted:
		a.pendingSteers = a.pendingSteers[min(len(e.Texts), len(a.pendingSteers)):]
		a.endStream()
		a.add(&userBlock{text: strings.Join(e.Texts, "\n\n")})
	case agent.CompactStart:
		a.endStream()
		a.compact = &compactBlock{auto: e.Auto, running: true, expander: expander{d: &a.details}}
		a.add(a.compact)
		a.activity = "Compacting context"
	case agent.CompactDelta:
		if a.compact != nil {
			a.compact.notes.WriteString(e.Text)
		}
	case agent.CompactEnd:
		if c := a.compact; c != nil {
			c.running = false
			c.notes.Reset()
			c.notes.WriteString(e.Notes)
			c.before, c.after, c.elapsed = e.Before, e.After, e.Elapsed
		}
		a.compact = nil
		a.ctxTokens = e.After
		a.activity = "Thinking"
		a.notice("Long threads and repeated compactions can make the model less accurate. Start a new conversation (/clear) when you can.")
	}
}

// --- effort ---

func (a *App) efforts() []string { return a.model().Model.Efforts }

func (a *App) cycleEffort() {
	levels := a.efforts()
	if len(levels) == 0 {
		return
	}
	i := 0
	for j, l := range levels {
		if l == a.effort() {
			i = j + 1
		}
	}
	a.setEffort(levels[i%len(levels)], false)
}

func (a *App) setEffort(level string, announce bool) {
	a.agent.SetEffort(level)
	a.statusTrigger()
	if err := config.UpdateSettings(map[string]any{"defaultEffort": level}); err != nil {
		a.errorNotice(err)
	}
	if announce {
		a.notice("Effort set to %s.", level)
	}
}

// --- footer rendering ---

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (a *App) renderActivity(width int) []string {
	if !a.busy {
		return nil
	}
	el := time.Since(a.runStart)
	frame := spinnerFrames[int(el/(80*time.Millisecond))%len(spinnerFrames)]
	line := tui.FG(6, frame) + " " + a.activity + "…" + tui.Dim("  "+fmtDur(el.Truncate(time.Second))+" · esc to interrupt")
	return []string{"", tui.Truncate(line, width, "…")}
}

func (a *App) renderInput(width int) []string {
	if a.modal != nil {
		return append([]string{""}, a.modal.Render(width)...)
	}
	return a.editor.Render(width)
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func effortStyle(level string) string {
	switch level {
	case "off", "minimal":
		return tui.Dim(level)
	case "low":
		return tui.FG(4, level)
	case "medium":
		return tui.FG(6, level)
	case "high":
		return tui.FG(3, level)
	default: // xhigh, max
		return tui.FG(5, tui.Bold(level))
	}
}

// refreshCatalog updates the models.dev catalog in the background and
// reloads the model list, so newly available providers appear in /model.
func (a *App) refreshCatalog() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := config.RefreshCatalog(ctx); err != nil {
		return // offline is fine; the cached catalog (if any) stays in use
	}
	models, err := config.LoadModels()
	if err != nil {
		return
	}
	a.ui.Do(func() {
		before := len(a.models.List())
		a.models = models
		if n := len(models.List()); n > before {
			a.notice("Model catalog updated: %d models available (/model).", n)
		}
	})
}
