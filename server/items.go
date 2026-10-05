package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"atto/agent"
	"atto/session"
)

const outputKeep = 64 * 1024 // command output kept on items

// itemMapper turns agent events of one turn into item notifications.
type itemMapper struct {
	s      *Server
	t      *thread
	turnID string

	reasoning *Item
	message   *Item
	compact   *Item
	commands  map[string]*Item
}

func (m *itemMapper) started(it *Item) {
	m.s.notify(m.t, "item/started", map[string]any{"turnId": m.turnID, "item": *it})
}

func (m *itemMapper) delta(it *Item, d string) {
	m.s.notify(m.t, "item/delta", map[string]any{"turnId": m.turnID, "itemId": it.ID, "delta": d})
}

func (m *itemMapper) completed(it *Item) {
	m.t.mu.Lock()
	m.t.items = append(m.t.items, *it)
	m.t.mu.Unlock()
	m.s.notify(m.t, "item/completed", map[string]any{"turnId": m.turnID, "item": *it})
}

func (m *itemMapper) newItem(typ string) *Item {
	m.t.mu.Lock()
	id := m.t.nextItemID()
	m.t.mu.Unlock()
	return &Item{ID: id, Type: typ}
}

// closeText completes open reasoning/message items.
func (m *itemMapper) closeText() {
	for _, p := range []**Item{&m.reasoning, &m.message} {
		if *p != nil {
			m.completed(*p)
			*p = nil
		}
	}
}

// closeOpen completes everything still open when the turn ends.
func (m *itemMapper) closeOpen() {
	m.closeText()
	for id, it := range m.commands {
		it.Status = "failed"
		m.completed(it)
		delete(m.commands, id)
	}
	if m.compact != nil {
		m.compact.Status = "failed"
		m.completed(m.compact)
		m.compact = nil
	}
}

func (m *itemMapper) user(text string) {
	it := m.newItem(ItemUser)
	it.Text = text
	m.started(it)
	m.completed(it)
}

func (m *itemMapper) event(ev any) {
	if m.commands == nil {
		m.commands = map[string]*Item{}
	}
	switch e := ev.(type) {
	case userInput:
		m.user(e.text)
	case agent.ReasoningDelta:
		if m.reasoning == nil {
			m.reasoning = m.newItem(ItemReasoning)
			m.started(m.reasoning)
		}
		m.reasoning.Text += e.Text
		m.delta(m.reasoning, e.Text)
	case agent.TextDelta:
		if m.reasoning != nil {
			m.completed(m.reasoning)
			m.reasoning = nil
		}
		if m.message == nil {
			m.message = m.newItem(ItemAgent)
			m.started(m.message)
		}
		m.message.Text += e.Text
		m.delta(m.message, e.Text)
	case agent.ToolStart:
		m.closeText()
		it := m.newItem(ItemCommand)
		it.Description, it.Command, it.Status = e.Args.Description, e.Args.Command, "inProgress"
		m.commands[e.ID] = it
		m.started(it)
	case agent.ToolOutput:
		if it := m.commands[e.ID]; it != nil {
			if len(it.Output) < outputKeep {
				it.Output += e.Chunk
			}
			m.delta(it, e.Chunk)
		}
	case agent.ToolEnd:
		if it := m.commands[e.ID]; it != nil {
			r := e.Result
			code := r.ExitCode
			it.ExitCode, it.DurationMs, it.TimedOut = &code, r.Duration.Milliseconds(), r.TimedOut
			it.Status = "completed"
			if code != 0 || r.Err != nil {
				it.Status = "failed"
			}
			m.completed(it)
			delete(m.commands, e.ID)
		}
	case agent.StepEnd:
		m.closeText()
		m.t.mu.Lock()
		m.t.usage.PromptTokens += e.Usage.PromptTokens
		m.t.usage.CachedTokens += e.Usage.CachedTokens
		m.t.usage.CompletionTokens += e.Usage.CompletionTokens
		m.t.ctxTokens = e.Context
		m.t.mu.Unlock()
	case agent.SteerCommitted:
		m.closeText()
		m.user(strings.Join(e.Texts, "\n\n"))
	case agent.CompactStart:
		m.closeText()
		m.compact = m.newItem(ItemCompaction)
		m.compact.Auto, m.compact.Status = e.Auto, "inProgress"
		m.started(m.compact)
	case agent.CompactDelta:
		if m.compact != nil {
			m.compact.Text += e.Text
			m.delta(m.compact, e.Text)
		}
	case agent.CompactEnd:
		if c := m.compact; c != nil {
			c.Text, c.TokensBefore, c.TokensAfter, c.Status = e.Notes, e.Before, e.After, "completed"
			m.completed(c)
			m.compact = nil
		}
	case agent.HookNotice:
		m.s.notify(m.t, "hook", map[string]any{"turnId": m.turnID, "event": e.Event, "message": e.Message, "blocked": e.Blocked})
	}
}

// ItemsFromEntries rebuilds a thread's items from its session file.
func ItemsFromEntries(threadID string, entries []session.Entry) []Item {
	var out []Item
	n := 0
	next := func(typ string) Item {
		n++
		return Item{ID: fmt.Sprintf("%s-i%d", threadID, n), Type: typ, Status: "completed"}
	}
	cmds := map[string]int{} // tool call ID -> index in out
	for _, e := range entries {
		switch e.Type {
		case session.TypeCompaction:
			it := next(ItemCompaction)
			it.Text, it.Auto, it.TokensBefore = e.Notes, e.Auto, e.TokensBefore
			out = append(out, it)
		case session.TypeMessage:
			msg := e.Message
			if msg == nil {
				continue
			}
			switch msg.Role {
			case "user":
				it := next(ItemUser)
				it.Text = msg.Content
				out = append(out, it)
			case "assistant":
				if strings.TrimSpace(msg.ReasoningContent) != "" {
					it := next(ItemReasoning)
					it.Text = msg.ReasoningContent
					out = append(out, it)
				}
				if strings.TrimSpace(msg.Content) != "" {
					it := next(ItemAgent)
					it.Text = msg.Content
					out = append(out, it)
				}
				for _, tc := range msg.ToolCalls {
					var args agent.BashArgs
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
					it := next(ItemCommand)
					it.Description, it.Command, it.Status = args.Description, args.Command, "failed"
					cmds[tc.ID] = len(out)
					out = append(out, it)
				}
			case "tool":
				if i, ok := cmds[msg.ToolCallID]; ok {
					it := &out[i]
					it.Output = msg.Content
					if t := e.Tool; t != nil {
						code := t.ExitCode
						it.ExitCode, it.DurationMs, it.TimedOut = &code, t.DurationMs, t.TimedOut
						it.Status = "completed"
						if code != 0 {
							it.Status = "failed"
						}
					}
				}
			}
		}
	}
	return out
}
