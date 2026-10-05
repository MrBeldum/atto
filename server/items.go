package server

import (
	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/core/transcript"
	"github.com/sebastianrcnt/atto/session"
)

// itemMapper sends one turn's items to clients. The thread's transcript
// builder makes the items from the agent's events; this turns them into
// item/started, item/delta, item/updated and item/completed notifications
// and keeps the completed ones on the thread.
type itemMapper struct {
	s      *Server
	t      *thread
	turnID string
}

// handler is the transcript handler for the turn.
func (m *itemMapper) handler() transcript.Handler {
	return transcript.Handler{
		Started: func(it *transcript.Item) {
			m.s.notify(m.t, "item/started", map[string]any{"turnId": m.turnID, "item": wireItem(it)})
		},
		Delta: func(it *transcript.Item, d string) {
			m.s.notify(m.t, "item/delta", map[string]any{"turnId": m.turnID, "itemId": it.ID, "delta": d})
		},
		Updated: func(it *transcript.Item) {
			m.s.notify(m.t, "item/updated", map[string]any{"turnId": m.turnID, "item": wireItem(it)})
		},
		Completed: func(it *transcript.Item) {
			w := wireItem(it)
			m.t.mu.Lock()
			m.t.items = append(m.t.items, w)
			m.t.mu.Unlock()
			m.s.notify(m.t, "item/completed", map[string]any{"turnId": m.turnID, "item": w})
		},
	}
}

// event passes an agent event (or transcript.Input) to the builder, and
// keeps the usage the turn reports.
func (m *itemMapper) event(ev any) {
	m.t.feed.Lock()
	defer m.t.feed.Unlock()
	m.t.tr.Event(ev)
	switch e := ev.(type) {
	case agent.StepEnd:
		m.t.mu.Lock()
		m.t.usage.PromptTokens += e.Usage.PromptTokens
		m.t.usage.CachedTokens += e.Usage.CachedTokens
		m.t.usage.CompletionTokens += e.Usage.CompletionTokens
		m.t.ctxTokens = e.Context
		m.t.mu.Unlock()
	case agent.HookNotice:
		// Also as the notification clients had before hook items.
		m.s.notify(m.t, "hook", map[string]any{"turnId": m.turnID, "event": e.Event, "message": e.Message, "blocked": e.Blocked})
	}
}

// closeOpen completes everything still open when the turn ends.
func (m *itemMapper) closeOpen() {
	m.t.feed.Lock()
	defer m.t.feed.Unlock()
	m.t.tr.End()
}

// wireItem is the protocol form of a transcript item.
func wireItem(it *transcript.Item) Item {
	w := Item{ID: it.ID, Text: it.Text, Status: string(it.Status)}
	switch it.Kind {
	case transcript.User:
		w.Type = ItemUser
	case transcript.Assistant:
		w.Type = ItemAgent
	case transcript.Reasoning:
		w.Type, w.DurationMs = ItemReasoning, it.Duration.Milliseconds()
	case transcript.Event:
		w.Type = ItemEvent
	case transcript.Goal:
		w.Type = ItemGoal
	case transcript.Hook:
		w.Type, w.HookEvent, w.Blocked = ItemHook, it.HookEvent, it.Blocked
	case transcript.Notice:
		w.Type = ItemNotice
	case transcript.GoalStatus:
		w.Type = ItemGoalStatus
		if g := it.GoalState; g != nil {
			w.GoalStatus, w.Text = string(g.Status), g.Note
		}
	case transcript.Tool:
		w.Type, w.Description, w.Command, w.Output, w.Pending = ItemCommand, it.Description, it.Command, it.Output, it.Pending
		if r := it.Result; r != nil {
			code := r.ExitCode
			w.ExitCode, w.DurationMs, w.TimedOut = &code, it.Duration.Milliseconds(), r.TimedOut
			if r.Job > 0 { // still running
				w.ExitCode, w.Job, w.Background = nil, r.Job, r.Background
			}
		}
	case transcript.Shell:
		// A command the user ran in the TUI ("!cmd"), shown as a command.
		w.Type, w.Description, w.Command, w.Output = ItemCommand, "user command", it.Command, it.Output
		if r := it.Result; r != nil {
			code := r.ExitCode
			w.ExitCode, w.DurationMs = &code, it.Duration.Milliseconds()
		}
	case transcript.ExtText:
		w.Type, w.Text = ItemNotice, it.Title+"\n"+it.Text
	case transcript.BranchSummary:
		w.Type, w.DurationMs = ItemBranchSummary, it.Duration.Milliseconds()
	case transcript.Compaction:
		w.Type, w.Auto, w.TokensBefore, w.TokensAfter = ItemCompaction, it.Auto, it.TokensBefore, it.TokensAfter
	}
	return w
}

// ItemsFromEntries rebuilds a thread's items from its session file: pass
// the active branch.
func ItemsFromEntries(threadID string, entries []session.Entry) []Item {
	items := transcript.FromEntries(itemPrefix(threadID), entries)
	out := make([]Item, len(items))
	for i := range items {
		out[i] = wireItem(&items[i])
	}
	return out
}

func itemPrefix(threadID string) string { return threadID + "-i" }
