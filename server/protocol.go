// Package server exposes atto's agent over JSON-RPC 2.0 so any frontend
// (CLI, web, mobile, editor) can drive it. The model follows codex
// app-server: threads (conversations, persisted as sessions) contain turns
// (one user input and everything it causes), which produce items (user
// messages, reasoning, assistant messages, command executions,
// compactions). Items stream as started → delta* → completed
// notifications.
//
// Requests:
//
//	initialize                                     → {name, version, protocolVersion}
//	models/list                                    → {models: [...]}
//	thread/start   {cwd?, model?, effort?}         → thread
//	thread/resume  {threadId}                      → thread + items
//	thread/read    {threadId}                      → thread + items
//	thread/list    {cwd?, archived?}               → {threads: [...]}
//	thread/setModel {threadId, model}              → thread
//	thread/setEffort {threadId, effort}            → thread
//	thread/compact {threadId}                      → {turnId}
//	turn/start     {threadId, input}               → {turnId}
//	turn/steer     {threadId, input}               → {}
//	turn/interrupt {threadId}                      → {}
//
// Notifications (all carry threadId):
//
//	turn/started   {turnId}
//	item/started   {turnId, item}
//	item/delta     {turnId, itemId, delta}
//	item/completed {turnId, item}
//	hook           {event, message, blocked}
//	turn/completed {turnId, status, error?, usage, contextTokens}
package server

import "encoding/json"

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
)

// Item is one unit of a turn's output.
type Item struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"` // message, reasoning, notes

	// commandExecution
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	Output      string `json:"output,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	TimedOut    bool   `json:"timedOut,omitempty"`
	Status      string `json:"status,omitempty"` // inProgress, completed, failed

	// compaction
	Auto         bool `json:"auto,omitempty"`
	TokensBefore int  `json:"tokensBefore,omitempty"`
	TokensAfter  int  `json:"tokensAfter,omitempty"`
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
}
