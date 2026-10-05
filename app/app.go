// Package app is the interactive terminal front end: it wires the agent's
// events into TUI components and handles input and slash commands.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/events"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/hooks"
	"github.com/sebastianrcnt/atto/jobs"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
	"github.com/sebastianrcnt/atto/update"
)

// Version is the release this binary was built from (see update.Current).
var Version = update.Current()

type Options struct {
	Inline   bool   // render inline instead of fullscreen
	Continue bool   // resume the latest session in this directory
	Resume   bool   // open the resume picker at startup
	Model    string // provider/id to use instead of the default
	Effort   string // effort to use instead of the default
	Session  string // resume the session with this ID
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
	hooks  *hooks.Runner // nil when no hooks are configured

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
	usage     usageStats

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

	// Inbox: events waiting for delivery, and counts for the status line.
	pendingEvents        []events.Event
	jobCount, timerCount int

	goal goalState

	// Slash command list: selection, the text it belongs to, and the text
	// for which Esc closed it.
	sugSel               int
	sugFor, sugDismissed string

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
	effort := opts.Effort
	if effort == "" {
		effort = settings.DefaultEffort
	}
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
	if opts.Inline || settings.Renderer == "inline" || (settings.Renderer == "" && legacyConsole()) {
		a.ui.Mode = tui.Inline
	}
	hookCfg, err := config.LoadHooks(cwd)
	if err != nil {
		return err
	}
	if a.hooks = hooks.New(hookCfg, cwd); a.hooks != nil {
		a.agent.Hooks = a.hooks
	}
	a.build()
	a.newSession()
	a.sessionStartHook("startup")
	a.statusCmd = settings.StatusLine != nil && settings.StatusLine.Command != ""
	a.startStatusLine(settings.StatusLine)
	if config.CatalogStale() {
		go a.refreshCatalog()
	}

	switch {
	case opts.Session != "":
		if path, err := session.Find(opts.Session); err == nil {
			a.resume(path)
		} else {
			a.errorNotice(err)
		}
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
	go a.watchInbox()
	if settings.UpdateCheck == nil || *settings.UpdateCheck {
		go a.checkUpdate()
	}
	<-a.quit
	a.ui.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
	})
	a.ui.Stop()
	a.sess.Close()
	_ = goal.Clear(a.sess.ID) // the session file keeps the goal's snapshot
	// Like codex, background jobs end with the session that started them.
	if n := jobs.KillAll(a.sess.ID); n > 0 {
		fmt.Printf("atto: stopped %d background job(s)\n", n)
	}
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
	a.ui.Pin = a.pinnedPrompt
	a.addHeader()
}

// leaveSession stops the jobs of the session being left (/clear,
// /resume): like codex, background processes belong to their session.
func (a *App) leaveSession() {
	if a.sess == nil {
		return
	}
	_ = goal.Clear(a.sess.ID) // the session file keeps the goal's snapshot
	if n := jobs.KillAll(a.sess.ID); n > 0 {
		a.notice("Stopped %d background job(s) of the previous conversation.", n)
	}
	a.jobCount, a.timerCount, a.pendingEvents = 0, 0, nil
}

// newSession starts recording into a fresh session file.
func (a *App) newSession() {
	a.leaveSession()
	a.sess.Close()
	a.sess = session.New(a.cwd)
	a.agent.Record = a.sess.Append
	a.agent.SetStart(time.Now())
	a.agent.SetSession(a.sess.ID, sessionEnv(a.sess.ID))
	a.hooks.SetSession(a.sess.ID, a.sess.Path)
	a.setLiveSession(a.sess.ID)
	a.goal = goalState{}
	a.recModel, a.recEffort, a.sessName = "", "", ""
	a.statusTrigger()
}

// sessionStartHook runs SessionStart hooks in the background.
func (a *App) sessionStartHook(source string) {
	if a.hooks == nil {
		return
	}
	go func() {
		notices := a.hooks.SessionStart(context.Background(), source)
		a.ui.Do(func() {
			for _, n := range notices {
				a.notice("%s", n)
			}
		})
	}()
}

// sessionEnv lets commands the agent runs find this session ("atto history")
// and the atto binary itself.
func sessionEnv(id string) []string {
	env := []string{"ATTO_SESSION_ID=" + id, config.EnvAgent + "=1"}
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
	if a.suggestionKey(tui.Key(data)) {
		return true
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
	a.goal.turnTools, a.goal.budgetSent = 0, false
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
			case errors.Is(err, agent.ErrPromptBlocked), errors.Is(err, agent.ErrStoppedByHook):
				// The hook's reason was already shown.
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
		a.goal.turnTools++
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
		a.usage.add(e.Usage)
		a.goalStep(e.Usage.PromptTokens, e.Usage.CachedTokens, e.Usage.CompletionTokens)
		a.statusTrigger()
	case agent.SteerCommitted:
		var user []string // events were already shown when they arrived
		for _, t := range e.Texts {
			if !isEvent(t) {
				user = append(user, t)
			}
		}
		a.pendingSteers = a.pendingSteers[min(len(user), len(a.pendingSteers)):]
		a.endStream()
		if len(user) > 0 {
			a.add(&userBlock{text: strings.Join(user, "\n\n")})
		}
	case agent.HookNotice:
		style := tui.Dim
		if e.Blocked {
			style = func(s string) string { return tui.FG(3, s) }
		}
		a.add(&noticeBlock{text: "⚑ " + e.Event + ": " + e.Message, style: style})
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

func (a *App) efforts() []string { return a.model().Model.Levels() }

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

// pinnedPrompt returns the latest prompt that has scrolled above the view,
// shown as a thin bar on the first row so the question stays visible
// (codex does the same in fullscreen).
func (a *App) pinnedPrompt(firstVisible, width int) string {
	var last *userBlock
	a.ui.Body.Each(func(c tui.Component, start, end int) {
		if g, ok := c.(gap); ok {
			if u, ok := g.Component.(*userBlock); ok && end-1 <= firstVisible {
				last = u
			}
		}
	})
	if last == nil {
		return ""
	}
	return last.pinLine(width)
}

// legacyConsole reports a classic Windows console (conhost), where the
// alternate screen and mouse reporting are unreliable; Windows Terminal
// and VS Code set WT_SESSION or TERM_PROGRAM. Set "renderer" in
// settings.json to override.
func legacyConsole() bool {
	return runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" && os.Getenv("TERM_PROGRAM") == ""
}
