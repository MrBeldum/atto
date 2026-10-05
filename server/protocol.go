// Package server exposes atto's agent over JSON-RPC 2.0 so any frontend
// (CLI, web, mobile, editor) can drive it. The model follows codex
// app-server: threads (conversations, persisted as sessions) contain turns
// (one user input and everything it causes), which produce items (user
// messages, reasoning, assistant messages, command executions,
// compactions, events, goal messages, hook messages; see
// core/transcript). Items stream as started → delta* → completed
// notifications.
//
// Requests:
//
//	initialize                                     → {name, version, protocolVersion, eventId}
//	models/list                                    → {models: [{id, name, contextWindow, efforts, hasKey, images}]}
//	thread/start   {cwd?, model?, effort?}         → thread + context
//	thread/resume  {threadId}                      → thread + items + context
//	               context: what the thread loaded (AGENTS files, skills,
//	               hooks, configuration, model and effort and where they
//	               came from), as stream-json's init event has it
//	thread/read    {threadId}                      → thread + items
//	thread/list    {cwd?, archived?}               → {threads: [...]}
//	thread/setModel {threadId, model}              → thread
//	thread/setEffort {threadId, effort}            → thread
//	thread/compact {threadId}                      → {turnId}
//	thread/rollback {threadId, numTurns?}          → thread + items + {input}
//	turn/start     {threadId, input, images?}      → {turnId}
//	               images: [{mimeType, data}], data base64 (or a data: URL);
//	               PNG, JPEG, GIF or WebP, at most 10 of 10 MB each, for
//	               models that take images
//	turn/steer     {threadId, input}               → {}
//	turn/interrupt {threadId}                      → {}
//	turn/background {threadId}                     → {}  (Ctrl+B: the running command becomes a job)
//
// Notifications (all carry threadId):
//
//	turn/started   {turnId}
//	item/started   {turnId, item}
//	item/delta     {turnId, itemId, delta}
//	item/updated   {turnId, item}  (a command the model is still writing, pending: its description and command so far; again when it starts running)
//	item/completed {turnId, item}
//	hook           {event, message, blocked}  (also a hook item during a turn)
//	event          {title}  (inbox event delivered to the thread)
//	thread/reloaded {context, changes, promptChanged, error?}  (atto reload run by the agent)
//	extension/notify {extension, message, level}  (ctx.ui.notify; extensions have no other UI here)
//	turn/completed {turnId, status, error?, usage, contextTokens}
//
// Over HTTP, thread/start, thread/resume and thread/read results carry
// eventId: the items are as of that event (those still streaming
// included), so follow the thread from there (GET /events?lastEventId=).
// When the events after the one a client resumes from are not known any
// more (the server restarted, or the client was away for longer than the
// server keeps events), the stream starts with
//
//	events/reset   {eventId}  (no threadId: read the thread again)
//
// Live session (atto's /remote, see Live): the server has one thread, the
// TUI's session. initialize says {live: true, threadId}; thread/start and
// thread/rollback are refused; turn/start and turn/steer both send the
// input as if typed in the terminal (a turn, a steer or a queued turn:
// {status, turnId}). More notifications:
//
//	thread/switched {threadId, previousThreadId}  (/clear, /resume or /tree in the terminal: thread/read again)
//	thread/updated  {thread}  (model, effort, name or busy changed)
//	goal/updated    {goal}  (the goal changed; null when cleared; see GoalInfo)
//	prompt/open     {prompt}  (the terminal opened a picker or an input; see Prompt)
//	prompt/closed   {id, how, by}  (how: answered, cancelled or closed; by: terminal or remote)
//
// and one more request:
//
//	prompt/answer  {threadId, id, index? | text? | cancel?}  → {}
//	               answers the open prompt as if in the terminal: index
//	               picks an option of a select, text submits an input,
//	               cancel is Esc. The first answer wins, from either side;
//	               a prompt that is no longer open is refused.
//
// thread/read's result carries the open prompt and the goal as well.
package server

import (
	"encoding/json"

	"github.com/sebastianrcnt/atto/core"
)

const ProtocolVersion = 1

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeServer         = -32000
)

// Item types.
const (
	ItemUser       = "userMessage"
	ItemReasoning  = "reasoning"
	ItemAgent      = "agentMessage"
	ItemCommand    = "commandExecution"
	ItemCompaction = "compaction"
	ItemEvent      = "event" // [atto event]: a job exited, a timer fired, a monitor matched
	ItemGoal       = "goal"  // [atto goal]: a goal continuation or budget message for the model
	ItemHook       = "hook"  // a hook's message, or what it blocked
	ItemNotice     = "notice"
	ItemGoalStatus = "goalStatus"

	// ItemBranchSummary is a summary of a branch the session went back
	// from in atto's /tree; threads show it when they resume such a session.
	ItemBranchSummary = "branchSummary"
)

// Item is one unit of a turn's output: the protocol form of a
// transcript.Item.
type Item struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`   // message, reasoning, notes, hook message
	Status string `json:"status,omitempty"` // inProgress, completed, failed

	// commandExecution
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	Output      string `json:"output,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	TimedOut    bool   `json:"timedOut,omitempty"`
	// Job is the background job the command became (no exitCode then),
	// and Background why: requested, timeout or user.
	Job        int    `json:"job,omitempty"`
	Background string `json:"background,omitempty"`
	// Pending: the model is still writing the command (description and
	// command are what has arrived).
	Pending bool `json:"pending,omitempty"`

	// compaction
	Auto         bool `json:"auto,omitempty"`
	TokensBefore int  `json:"tokensBefore,omitempty"`
	TokensAfter  int  `json:"tokensAfter,omitempty"`

	// hook
	HookEvent string `json:"hookEvent,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`

	// goalStatus: the new status; text is its note
	GoalStatus string `json:"goalStatus,omitempty"`
}

// ThreadInfo describes a thread to clients.
type ThreadInfo struct {
	ID            string   `json:"threadId"`
	Cwd           string   `json:"cwd"`
	Name          string   `json:"name,omitempty"`
	Model         string   `json:"model"`
	Effort        string   `json:"effort"`
	Efforts       []string `json:"efforts,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	ContextTokens int      `json:"contextTokens"`
	Busy          bool     `json:"busy"`
	TurnID        string   `json:"turnId,omitempty"`
	Items         []Item   `json:"items,omitempty"`
	// Context is set in thread/start and thread/resume results.
	Context *core.Loaded `json:"context,omitempty"`
	// EventID, in thread/read and thread/resume results over HTTP, is the
	// latest event published when the items were read: follow the thread
	// from there (GET /events?lastEventId=).
	EventID int64 `json:"eventId,omitempty"`
	// Live marks the TUI's own session served by /remote.
	Live bool `json:"live,omitempty"`
	// Prompt is the live session's open picker or input, and Goal its
	// goal (live sessions only).
	Prompt *Prompt   `json:"prompt,omitempty"`
	Goal   *GoalInfo `json:"goal,omitempty"`
}

// Prompt kinds.
const (
	PromptSelect = "select"
	PromptInput  = "input"
)

// Prompt is a choice or a line of input the live session's terminal asks
// for (a picker, a confirmation, an extension's dialog), mirrored to the
// clients so they can answer it.
type Prompt struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // select or input
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`

	// select: the options, and the one selected at first. Filterable
	// pickers (/model, /resume) can be searched; the client filters.
	// Total is the number of options before they were capped, when it was.
	Options    []PromptOption `json:"options,omitempty"`
	Selected   int            `json:"selected"`
	Filterable bool           `json:"filterable,omitempty"`
	Total      int            `json:"total,omitempty"`
	Note       string         `json:"note,omitempty"`

	// input: the text so far and a placeholder.
	Text        string `json:"text,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

type PromptOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PromptAnswer is a client's answer to a prompt: Index for a select,
// Text for an input, or Cancel.
type PromptAnswer struct {
	Index  *int
	Text   *string
	Cancel bool
}

// GoalInfo is the live session's goal as the terminal shows it.
type GoalInfo struct {
	Objective string `json:"objective"`
	Status    string `json:"status"`      // active, paused, blocked, usage_limited, budget_limited, complete
	Label     string `json:"statusLabel"` // the status as atto words it: "stalled", "limited by budget"
	// Indicator is the status line's text ("Pursuing goal (12.5K / 50K)"),
	// Summary the goal's usage summary.
	Indicator string `json:"indicator"`
	Summary   string `json:"summary"`
	Note      string `json:"note,omitempty"`
	// Tokens is "12.5K" or "12.5K / 50K"; Elapsed the time spent, with
	// the running turn ("14m").
	Tokens     string `json:"tokens"`
	TokensUsed int    `json:"tokensUsed"`
	Budget     int    `json:"budget,omitempty"`
	Elapsed    string `json:"elapsed"`
	Seconds    int64  `json:"seconds"`
}
