// Package agent runs the model ↔ tool loop. It knows nothing about the UI:
// progress is reported through an emit callback with the event types below.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"atto/config"
	"atto/provider"
	"atto/session"
)

// ErrMaxSteps is returned by Run when MaxSteps model calls were made.
var ErrMaxSteps = errors.New("stopped: reached the maximum number of steps")

// Events emitted during Run and Compact.
type (
	ReasoningDelta struct{ Text string }
	TextDelta      struct{ Text string }
	// ToolStart fires when a bash command begins executing.
	ToolStart struct {
		ID      string
		Args    BashArgs
		Timeout time.Duration
	}
	ToolOutput struct {
		ID    string
		Chunk string
	}
	ToolEnd struct {
		ID     string
		Result BashResult
	}
	// StepEnd fires after each model response. Context is the estimated
	// context size afterwards.
	StepEnd struct {
		Usage   provider.Usage
		Context int
	}
	// SteerCommitted fires when steering messages are added to the
	// conversation of the running turn.
	SteerCommitted struct{ Texts []string }
	// CompactStart, CompactDelta and CompactEnd bracket a compaction.
	CompactStart struct{ Auto bool }
	CompactDelta struct{ Text string }
	CompactEnd   struct {
		Notes         string
		Before, After int // estimated context tokens
		Elapsed       time.Duration
	}
)

type Agent struct {
	Cwd string

	// Model settings may change from the UI while a turn runs; they are
	// read once per request.
	cfgMu   sync.Mutex
	client  *provider.Client
	model   config.ModelRef
	effort  string
	env     []string // extra environment for bash commands
	lastReq []byte   // most recent request body, for inspection
	sessID  string   // sent to providers that route by session

	// Record, if set, receives every change to the conversation, for
	// persistence. Called on the goroutine running Run/Compact.
	Record func(session.Entry)

	system   string
	messages []provider.Message
	// MaxSteps stops a turn after this many model calls (0: unlimited).
	MaxSteps int

	// LastUsage is the usage of the most recent model call.
	LastUsage provider.Usage
	// sinceUsage counts characters appended after the last reported usage.
	sinceUsage int

	steerMu sync.Mutex
	steers  []string
}

// Steer queues a message for the running turn. It is added to the
// conversation at the next step boundary: after the current tool calls
// finish, or when the model stops (in which case the turn continues).
// Safe to call from any goroutine.
func (a *Agent) Steer(text string) {
	a.steerMu.Lock()
	a.steers = append(a.steers, text)
	a.steerMu.Unlock()
}

// DrainSteers removes and returns steering messages not yet committed.
func (a *Agent) DrainSteers() []string {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	s := a.steers
	a.steers = nil
	return s
}

// commitSteers appends pending steers as a user message.
func (a *Agent) commitSteers(emit func(any)) bool {
	s := a.DrainSteers()
	if len(s) == 0 {
		return false
	}
	a.appendMessage(provider.Message{Role: "user", Content: strings.Join(s, "\n\n")}, session.Entry{})
	emit(SteerCommitted{s})
	return true
}

func New(model config.ModelRef, effort, cwd string) *Agent {
	// A default ID so session-routed providers work even without a saved
	// session (e.g. atto -p); SetSession replaces it.
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	a := &Agent{Cwd: cwd, effort: effort, sessID: hex.EncodeToString(id)}
	a.SetModel(model)
	a.SetStart(time.Now())
	return a
}

// SetStart rebuilds the system prompt for a session that started at t.
// The prompt embeds the session's start date rather than today's, so it
// stays byte-identical for the whole session (and across resumes) and the
// prefix cache survives midnight. Call only while no turn is running.
func (a *Agent) SetStart(t time.Time) {
	a.system = systemPrompt(a.Cwd, t)
}

// SetSession sets the session ID (for provider routing headers) and extra
// environment variables for bash commands.
func (a *Agent) SetSession(id string, env []string) {
	a.cfgMu.Lock()
	a.sessID, a.env = id, env
	a.cfgMu.Unlock()
}

// SetModel switches the model; history is kept. Takes effect on the next
// request.
func (a *Agent) SetModel(m config.ModelRef) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.model = m
	a.client = &provider.Client{
		BaseURL:        m.Provider.BaseURL,
		APIKey:         m.APIKey,
		MaxTokensField: m.Provider.MaxTokensField,
		ExtraBody:      m.RequestBody(),
		EffortMap:      m.Model.WireEfforts(),
		Headers:        m.Provider.Headers,
		OnRequest: func(b []byte) {
			a.cfgMu.Lock()
			a.lastReq = b
			a.cfgMu.Unlock()
		},
	}
	if lv := m.Model.Levels(); len(lv) > 0 && !contains(lv, a.effort) {
		a.effort = lv[len(lv)/2]
	}
}

// SetEffort changes the reasoning effort for the next request.
func (a *Agent) SetEffort(e string) {
	a.cfgMu.Lock()
	a.effort = e
	a.cfgMu.Unlock()
}

// LastRequest returns the body of the most recent request sent.
func (a *Agent) LastRequest() []byte {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.lastReq
}

// Breakdown sizes the parts of the context, in characters. Call only while
// no turn is running.
type Breakdown struct {
	System, Tools, User, Notes, Assistant, Reasoning, ToolCalls, ToolResults int
	Messages                                                                 int
}

func (b Breakdown) Total() int {
	return b.System + b.Tools + b.User + b.Notes + b.Assistant + b.Reasoning + b.ToolCalls + b.ToolResults
}

func (a *Agent) Breakdown() Breakdown {
	b := Breakdown{System: len(a.system), Messages: len(a.messages)}
	for _, t := range a.tools() {
		b.Tools += len(t.Function.Name) + len(t.Function.Description) + len(t.Function.Parameters)
	}
	for _, m := range a.messages {
		switch m.Role {
		case "user":
			if strings.HasPrefix(m.Content, SummaryPrefix) {
				b.Notes += len(m.Content)
			} else {
				b.User += len(m.Content)
			}
		case "assistant":
			b.Assistant += len(m.Content)
			b.Reasoning += len(m.ReasoningContent)
			for _, tc := range m.ToolCalls {
				b.ToolCalls += len(tc.Function.Name) + len(tc.Function.Arguments)
			}
		case "tool":
			b.ToolResults += len(m.Content)
		}
	}
	return b
}

// SystemPrompt returns the system prompt in use.
func (a *Agent) SystemPrompt() string { return a.system }

// Effort returns the effort in use.
func (a *Agent) Effort() string {
	_, e := a.Current()
	return e
}

// Current returns the model and effort in use.
func (a *Agent) Current() (config.ModelRef, string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.model, a.effort
}

// AutoCompactLimit follows codex: 90% of the context window, further capped
// so the largest possible response still fits.
func AutoCompactLimit(m config.Model) int {
	if m.ContextWindow <= 0 {
		return 0
	}
	limit := m.ContextWindow * 9 / 10
	if m.MaxTokens > 0 {
		limit = min(limit, m.ContextWindow-m.MaxTokens)
	}
	return max(limit, 0)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Reset clears the conversation.
func (a *Agent) Reset() {
	a.DrainSteers()
	a.messages = nil
	a.LastUsage = provider.Usage{}
	a.sinceUsage = 0
}

// Restore rebuilds the conversation from session entries.
func (a *Agent) Restore(entries []session.Entry) {
	a.Reset()
	for _, e := range entries {
		switch e.Type {
		case session.TypeMessage:
			if e.Message == nil {
				continue
			}
			a.messages = append(a.messages, *e.Message)
			if e.Usage != nil {
				a.LastUsage, a.sinceUsage = *e.Usage, 0
			} else {
				a.sinceUsage += messageChars(*e.Message)
			}
		case session.TypeCompaction:
			a.messages = append([]provider.Message(nil), e.Replacement...)
			a.LastUsage = provider.Usage{}
			a.sinceUsage = len(a.system)
			for _, m := range a.messages {
				a.sinceUsage += messageChars(m)
			}
		}
	}
}

func messageChars(m provider.Message) int {
	n := len(m.Content) + len(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	return n
}

// ContextTokens estimates the current context size: the last reported
// usage plus ~4 characters per token for anything appended since.
func (a *Agent) ContextTokens() int {
	return a.LastUsage.PromptTokens + a.LastUsage.CompletionTokens + a.sinceUsage/4
}

func (a *Agent) needsCompact() bool {
	m, _ := a.Current()
	limit := AutoCompactLimit(m.Model)
	return limit > 0 && len(a.messages) > 0 && a.ContextTokens() >= limit
}

// appendMessage adds m to the conversation and records it. meta carries
// extra fields for the session entry.
func (a *Agent) appendMessage(m provider.Message, meta session.Entry) {
	a.messages = append(a.messages, m)
	a.sinceUsage += messageChars(m)
	if a.Record != nil {
		meta.Type = session.TypeMessage
		meta.Message = &m
		a.Record(meta)
	}
}

func (a *Agent) tools() []provider.Tool {
	return []provider.Tool{{
		Type: "function",
		Function: provider.ToolFunction{
			Name:        "bash",
			Description: bashDescription,
			Parameters:  bashSchema,
		},
	}}
}

// request builds a request and returns the client to send it with.
func (a *Agent) request(extra ...provider.Message) (*provider.Client, provider.Request) {
	model, effort := a.Current()
	a.cfgMu.Lock()
	client, sessID := a.client, a.sessID
	a.cfgMu.Unlock()
	msgs := make([]provider.Message, 0, len(a.messages)+len(extra)+1)
	msgs = append(msgs, provider.Message{Role: "system", Content: a.system})
	msgs = append(msgs, a.messages...)
	msgs = append(msgs, extra...)
	return client, provider.Request{
		SessionID: sessID,
		Model:     model.Model.ID,
		Messages:  msgs,
		Tools:     a.tools(),
		Effort:    effort,
		MaxTokens: model.Model.MaxTokens,
	}
}

// Run sends input and loops through tool calls until the model stops.
// Compaction runs automatically before the turn and between tool calls
// when the context passes AutoCompactLimit.
func (a *Agent) Run(ctx context.Context, input string, emit func(any)) error {
	if a.needsCompact() {
		if err := a.compact(ctx, emit, true); err != nil {
			return err
		}
	}
	a.appendMessage(provider.Message{Role: "user", Content: input}, session.Entry{})

	for step := 1; ; step++ {
		if a.MaxSteps > 0 && step > a.MaxSteps {
			return ErrMaxSteps
		}
		var thinkStart, thinkEnd time.Time
		h := provider.Handler{
			OnReasoning: func(s string) {
				if thinkStart.IsZero() {
					thinkStart = time.Now()
				}
				emit(ReasoningDelta{s})
			},
			OnText: func(s string) {
				if !thinkStart.IsZero() && thinkEnd.IsZero() {
					thinkEnd = time.Now()
				}
				emit(TextDelta{s})
			},
		}
		client, req := a.request()
		res, err := client.Stream(ctx, req, h)
		var thinkMs int64
		if !thinkStart.IsZero() {
			if thinkEnd.IsZero() {
				thinkEnd = time.Now()
			}
			thinkMs = thinkEnd.Sub(thinkStart).Milliseconds()
		}
		if err != nil {
			// Keep partial text so the transcript matches what the user saw,
			// but drop half-formed tool calls.
			if res.Message.Content != "" {
				res.Message.ToolCalls = nil
				a.appendMessage(res.Message, session.Entry{ThinkingMs: thinkMs})
			}
			return err
		}
		usage := res.Usage
		a.appendMessage(res.Message, session.Entry{Usage: &usage, ThinkingMs: thinkMs})
		a.LastUsage, a.sinceUsage = usage, 0
		emit(StepEnd{Usage: usage, Context: a.ContextTokens()})

		if len(res.Message.ToolCalls) == 0 {
			if a.commitSteers(emit) {
				continue
			}
			return nil
		}
		for _, tc := range res.Message.ToolCalls {
			var content string
			var meta session.Entry
			if ctx.Err() != nil {
				content = "[canceled by user]"
				meta.Tool = &session.ToolMeta{Canceled: true, ExitCode: -1}
			} else {
				content, meta.Tool = a.runTool(ctx, tc, emit)
			}
			a.appendMessage(provider.Message{Role: "tool", ToolCallID: tc.ID, Content: content}, meta)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.commitSteers(emit)
		if a.needsCompact() {
			if err := a.compact(ctx, emit, true); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) runTool(ctx context.Context, tc provider.ToolCall, emit func(any)) (string, *session.ToolMeta) {
	fail := func(msg string) (string, *session.ToolMeta) {
		return "error: " + msg, &session.ToolMeta{ExitCode: -1}
	}
	if tc.Function.Name != "bash" {
		return fail(fmt.Sprintf("unknown tool %q; the only tool is bash", tc.Function.Name))
	}
	var args BashArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return fail("invalid arguments: " + err.Error())
	}
	if strings.TrimSpace(args.Command) == "" {
		return fail("command is empty")
	}
	if args.Description == "" {
		args.Description = firstLine(args.Command)
	}
	emit(ToolStart{ID: tc.ID, Args: args, Timeout: args.timeout()})
	a.cfgMu.Lock()
	env := a.env
	a.cfgMu.Unlock()
	res := RunBash(ctx, a.Cwd, env, args, func(s string) { emit(ToolOutput{ID: tc.ID, Chunk: s}) })
	emit(ToolEnd{ID: tc.ID, Result: res})
	return res.ForModel(args), &session.ToolMeta{
		Description: args.Description,
		ExitCode:    res.ExitCode,
		DurationMs:  res.Duration.Milliseconds(),
		TimedOut:    res.TimedOut,
		Canceled:    res.Canceled,
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

const compactPrompt = `Context checkpoint: the conversation is about to be compacted. Write handoff notes so that you can continue this work with no other context.

If earlier handoff notes appear above, fold them into one updated set: keep what is still relevant, drop what is stale.

Include:
- The user's goal and any constraints or preferences they stated
- Progress so far and what was learned: key files, commands, findings, decisions and why
- Current state: what works, what is broken, open errors
- Remaining steps
- References: distinctive search terms for details left out of the notes (error message fragments, file names, identifiers, experiment names), so they can be found again in the full transcript

The full transcript stays searchable after compaction, so long logs and finished exploration need not be copied; but everything needed to continue must be in the notes. Be specific: exact file paths, function names, commands, error messages. Stay under %d words. Output only the notes, no preamble. Do not call tools.`

// CompactNoteWords bounds the length of handoff notes.
const CompactNoteWords = 700

// SummaryPrefix introduces handoff notes in the compacted history.
const SummaryPrefix = "[atto handoff notes] The conversation was compacted. Earlier messages were replaced by these notes, written by you from the full history; the tool state they describe (files, processes) is still in place. Build on them and avoid redoing finished work. If you need a detail the notes leave out, search the full transcript with `atto history grep <regexp>` and read an entry with `atto history show <n>`.\n\n"

// keepUserTokens is how much recent user text survives compaction (codex
// keeps 20k tokens of user messages).
const keepUserTokens = 20000

// Compact replaces the conversation with handoff notes written by the model.
func (a *Agent) Compact(ctx context.Context, emit func(any)) error {
	return a.compact(ctx, emit, false)
}

// compact asks the model for handoff notes. The request reuses the full
// existing prefix (system, tools, history) and appends the instruction at the
// end, so the prefix cache stays warm. The new history is the most recent
// user messages (up to keepUserTokens) followed by the notes.
func (a *Agent) compact(ctx context.Context, emit func(any), auto bool) error {
	if len(a.messages) == 0 {
		return fmt.Errorf("nothing to compact")
	}
	start := time.Now()
	before := a.ContextTokens()
	emit(CompactStart{Auto: auto})

	client, req := a.request(provider.Message{Role: "user", Content: fmt.Sprintf(compactPrompt, CompactNoteWords)})
	req.ToolChoice = "none"
	res, err := client.Stream(ctx, req, provider.Handler{
		OnText: func(s string) { emit(CompactDelta{s}) },
	})
	if err != nil {
		return fmt.Errorf("compaction failed: %w", err)
	}
	notes := strings.TrimSpace(res.Message.Content)
	if notes == "" {
		notes = "(no notes available)"
	}

	// Most recent user messages, newest first within the budget, kept in
	// chronological order. Earlier notes are not kept: the new notes fold
	// them in.
	var kept []provider.Message
	budget := keepUserTokens * 4
	for i := len(a.messages) - 1; i >= 0 && budget > 0; i-- {
		m := a.messages[i]
		if m.Role != "user" || strings.HasPrefix(m.Content, SummaryPrefix) {
			continue
		}
		if len(m.Content) > budget {
			m.Content = m.Content[len(m.Content)-budget:] + "\n[truncated]"
		}
		budget -= len(m.Content)
		kept = append([]provider.Message{m}, kept...)
	}
	replacement := append(kept, provider.Message{Role: "user", Content: SummaryPrefix + notes})

	a.messages = replacement
	a.LastUsage = provider.Usage{}
	a.sinceUsage = len(a.system)
	for _, m := range replacement {
		a.sinceUsage += messageChars(m)
	}
	if a.Record != nil {
		a.Record(session.Entry{Type: session.TypeCompaction, Replacement: replacement, Notes: notes, TokensBefore: before, Auto: auto})
	}
	emit(CompactEnd{Notes: notes, Before: before, After: a.ContextTokens(), Elapsed: time.Since(start)})
	return nil
}

func systemPrompt(cwd string, start time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are atto, a coding agent running in the user's terminal.

You have one tool, bash. Use it for everything: exploring (ls, rg, cat, sed -n), editing files (heredocs, sed, python scripts, patch), building, and testing.
Every bash call needs a short description of what it does, shown to the user, e.g. "JIT compile atto.py", "Run unit tests", "Read main.go".
Commands time out after 60 seconds by default; set timeout for longer builds or tests.
The full transcript of this session, including anything removed by compaction, can be searched with "atto history grep <regexp>" and read with "atto history show <n>".

Work autonomously: investigate, make the change, verify it. Keep replies concise and plain; the user sees your tool calls.

Environment:
- Working directory: %s
- Platform: %s/%s
- Session started: %s
`, cwd, runtime.GOOS, runtime.GOARCH, start.Format("2006-01-02"))

	for _, p := range []string{filepath.Join(config.Dir(), "AGENTS.md"), filepath.Join(cwd, "AGENTS.md")} {
		if data, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			fmt.Fprintf(&b, "\n# Instructions from %s\n\n%s\n", p, strings.TrimSpace(string(data)))
		}
	}
	return b.String()
}
