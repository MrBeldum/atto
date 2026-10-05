// Package app is the interactive terminal front end: it wires the agent's
// events into TUI components and handles input and slash commands.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/core"
	"github.com/sebastianrcnt/atto/core/transcript"
	"github.com/sebastianrcnt/atto/events"
	"github.com/sebastianrcnt/atto/hooks"
	"github.com/sebastianrcnt/atto/images"
	"github.com/sebastianrcnt/atto/provider"
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
	Prompt   string // first message, submitted once the UI is up (atto "fix the build")
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
	// login replaces the browser, clipboard and device ID in tests.
	login loginHooks
	// clipboard reads an image for Ctrl+V / Alt+V.
	clipboard func(context.Context) (provider.Image, error)

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
	queued                   []queuedInput
	sendSteersAfterInterrupt bool
	queuePaused              bool

	// items makes the transcript's items from agent events and session
	// entries (see items.go); these are the blocks of the items being
	// streamed, tools by item ID.
	items    transcript.Builder
	thinking *thinkingBlock
	text     *textBlock
	tools    map[string]*toolBlock
	compact  *compactBlock
	// steered collects the user messages of a committed steer, shown as
	// one block; replaying is set while blocks come from saved entries.
	steered   []string
	replaying bool

	// details expands every collapsible block (ctrl+t).
	details details
	// Last model/effort written to the session, to record changes.
	recModel, recEffort string
	sessName            string
	// pendingResume is a session to switch to once the running turn stops.
	pendingResume string
	// pendingTree is a /tree entry to move to once the running turn stops.
	pendingTree string
	// esc detects Esc twice on an empty prompt; escAction is what it opens.
	esc       doubleEsc
	escAction string

	// Inbox: events waiting for delivery, and counts for the status line.
	pendingEvents        []events.Event
	jobCount, timerCount int

	goal core.GoalDriver

	// Slash command list: selection, the text it belongs to, and the text
	// for which Esc closed it.
	sugList      *tui.SelectList
	sugDismissed string

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
	settings, models, err := core.Load()
	if err != nil {
		return err
	}
	model, err := core.PickModel(models, settings, opts.Model)
	noModels := errors.Is(err, core.ErrNoModels)
	if err != nil && !noModels {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ag, hk, err := core.NewAgent(cwd, model, core.Effort(settings, opts.Effort))
	if err != nil {
		return err
	}

	a := &App{
		ui:     tui.New(tui.NewProcessTerminal()),
		models: models,
		agent:  ag,
		hooks:  hk,
		tools:  map[string]*toolBlock{},
		cwd:    cwd,
		quit:   make(chan struct{}),

		clipboard: images.SystemClipboardImage,
	}
	if opts.Inline || rendererMode(settings.Renderer) == tui.Inline {
		a.ui.Mode = tui.Inline
	}
	a.escAction = settings.DoubleEscapeAction
	a.build()
	if noModels {
		// First run: start anyway and say how to get a model, like pi.
		a.notice("%s", core.NoModelsHint())
	}
	a.newSession()
	_, skillWarnings := a.agent.Skills()
	for _, w := range skillWarnings {
		a.notice("Skill: %s", w)
	}
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
	if opts.Prompt != "" {
		// As if typed: goes through submit, so a leading "/" is a command too.
		a.ui.Do(func() { a.submit(opts.Prompt, nil) })
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
	if n := core.Leave(a.sess.ID); n > 0 {
		fmt.Printf("atto: stopped %d background job(s)\n", n)
	}
	return nil
}

func (a *App) build() {
	a.editor = tui.NewEditor(tui.FG(6, "› "))
	a.editor.Rule = tui.Dim
	a.editor.OnSubmit = a.submit
	a.editor.OnPaste = a.pasteImagePath

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
	if n := core.Leave(a.sess.ID); n > 0 {
		a.notice("Stopped %d background job(s) of the previous conversation.", n)
	}
	a.jobCount, a.timerCount, a.pendingEvents = 0, 0, nil
}

// newSession starts recording into a fresh session file.
func (a *App) newSession() {
	a.leaveSession()
	a.sess.Close()
	a.sess = session.New(a.cwd)
	core.Bind(a.agent, a.hooks, a.sess, time.Now(), true)
	a.setLiveSession(a.sess.ID)
	a.resetGoal()
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
			tui.Truncate(tui.Bold("atto")+tui.Dim(" "+Version+"  ·  "+a.headerModel()), width, "…"),
			tui.Truncate(tui.Dim("/ commands · enter steer · tab queue · shift+tab effort · ctrl+t details · esc interrupt · esc esc go back"), width, "…"),
		}
	}))
}

func (a *App) headerModel() string {
	if m := a.model().Model; m.ID != "" {
		return m.DisplayName()
	}
	return "no model (/login)"
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
		a.esc.reset() // an Esc that closed the "/" list is not a first Esc
		return true
	}
	if tui.Key(data) != "escape" {
		a.esc.reset()
	}
	switch tui.Key(data) {
	case "shift+tab":
		a.cycleEffort()
		return true
	case "ctrl+t":
		a.details.on = !a.details.on
		a.details.gen++ // the expanded blocks are confirmation enough
		return true
	case "escape":
		if a.busy {
			a.esc.reset()
			if len(a.pendingSteers) > 0 {
				a.sendSteersAfterInterrupt = true
			}
			a.cancel()
			return true
		}
		return a.onEscape()
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
	case "ctrl+v":
		a.pasteClipboardImage()
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
	if data == "\x1bv" { // alt+v: where the terminal keeps Ctrl+V for pasting text
		a.pasteClipboardImage()
		return true
	}
	return false
}

func (a *App) submit(text string, att []tui.Attachment) {
	a.ui.ScrollToBottom()
	if len(att) > 0 && !strings.HasPrefix(text, "/") {
		a.submitWithImages(text, att)
		return
	}
	switch {
	case text == "":
		// Enter on an empty prompt resumes a paused queue.
		if !a.busy && len(a.queued) > 0 {
			a.queuePaused = false
			a.maybeSendNextQueued()
		}
	case strings.HasPrefix(text, "/"):
		a.runCommand(text)
	case a.noModel():
		a.restoreToEditor([]string{text})
	case a.busy && a.runKind == "turn":
		a.steer(text)
	case a.busy:
		a.enqueue(text, nil)
	default:
		a.startTurn(text, nil)
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

// startTurn runs a turn for text and its image attachments.
func (a *App) startTurn(text string, att []tui.Attachment) {
	imgs := attachedImages(att)
	for _, im := range imgs {
		if err := images.Save(im); err != nil {
			a.errorNotice(fmt.Errorf("saving image: %w", err))
			a.restoreToEditor([]string{text}, att...)
			return
		}
	}
	a.tr().Event(transcript.Input{Text: text, Images: imgs})
	a.runKind = "turn"
	a.recordSettings()
	a.start("Thinking", func(ctx context.Context, emit func(any)) error {
		return a.agent.RunWithImages(ctx, text, imgs, emit)
	})
}

// start runs fn in the background, routing its events into the UI.
func (a *App) start(activity string, fn func(context.Context, func(any)) error) {
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.cancel = true, cancel
	a.runStart, a.activity = time.Now(), activity
	if a.runKind == "turn" {
		a.goal.BeginTurn()
	}

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
			a.tr().End() // a compaction that did not finish disappears
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
	line := tui.FG(6, frame) + " " + a.activity + "…" + tui.Dim("  "+tui.FormatDuration(el.Truncate(time.Second))+" · esc to interrupt")
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

// legacyConsole reports a console that can't do the fullscreen renderer:
// only Windows before 10 1809 (build 17763), whose conhost lacks the
// alternate screen and VT input. Newer conhost and Windows Terminal both
// work, and environment variables can't tell them apart reliably (Windows
// Terminal as the default console sets no WT_SESSION, nor does sshd). Set
// "renderer" in settings.json to override.
func legacyConsole() bool { return windowsBuild() > 0 && windowsBuild() < 17763 }

// rendererMode is the mode the "renderer" setting asks for; empty (or
// anything unknown) lets atto pick, see legacyConsole.
func rendererMode(setting string) tui.Mode {
	switch setting {
	case "inline":
		return tui.Inline
	case "fullscreen":
		return tui.Fullscreen
	}
	if legacyConsole() {
		return tui.Inline
	}
	return tui.Fullscreen
}

// cmdTui shows or changes the renderer. The choice is saved in
// settings.json and applied to the running screen at once.
func (a *App) cmdTui(arg string) {
	name := func(m tui.Mode) string {
		if m == tui.Inline {
			return "inline"
		}
		return "fullscreen"
	}
	if arg == "" {
		a.notice("Renderer: %s. Choices: auto (pick for this terminal), fullscreen, inline. Usage: /tui <choice>", name(a.ui.Mode))
		return
	}
	var value string
	switch arg {
	case "auto":
	case "fullscreen", "inline":
		value = arg
	default:
		a.notice("Unknown renderer %q. Choices: auto, fullscreen, inline.", arg)
		return
	}
	// An empty value is the same as no setting: atto picks again.
	if err := config.UpdateSettings(map[string]any{"renderer": value}); err != nil {
		a.errorNotice(err)
		return
	}
	mode := rendererMode(value)
	a.ui.SetMode(mode)
	a.notice("Renderer set to %s.", arg+map[bool]string{true: " (" + name(mode) + ")"}[arg == "auto"])
}
