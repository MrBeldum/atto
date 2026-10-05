package ai

import "strings"

// Port of src/utils/transcript.ts (the parts the OpenAI APIs use).

// CreateInitialSystemMessage builds the leading system message for a prompt
// and tool set, or nil when both are empty.
func CreateInitialSystemMessage(systemPrompt string, tools []Tool) *SystemMessage {
	if systemPrompt == "" && len(tools) == 0 {
		return nil
	}
	return &SystemMessage{Role: "system", Content: systemPrompt, ToolsAdded: tools}
}

// NormalizeContext folds Context.SystemPrompt and Context.Tools into a
// leading system message. It is the only producer of TranscriptContext.
func NormalizeContext(c Context) TranscriptContext {
	if m := CreateInitialSystemMessage(c.SystemPrompt, c.Tools); m != nil {
		return TranscriptContext{Messages: append([]Message{m}, c.Messages...)}
	}
	return TranscriptContext{Messages: c.Messages}
}

// GetInitialSystemMessage returns the leading system message, if any.
func GetInitialSystemMessage(messages []Message) *SystemMessage {
	if len(messages) > 0 {
		if m, ok := messages[0].(*SystemMessage); ok {
			return m
		}
	}
	return nil
}

// GetCurrentTools resolves the tools available after every transcript
// delta, in first-declaration order.
func GetCurrentTools(messages []Message) []Tool {
	var order []string
	tools := map[string]Tool{}
	for _, msg := range messages {
		m, ok := msg.(*SystemMessage)
		if !ok {
			continue
		}
		for _, t := range m.ToolsRemoved {
			delete(tools, t.Name)
		}
		for _, t := range m.ToolsAdded {
			if _, ok := tools[t.Name]; !ok {
				order = append(order, t.Name)
			}
			tools[t.Name] = t
		}
	}
	var out []Tool
	seen := map[string]bool{}
	for _, n := range order {
		if t, ok := tools[n]; ok && !seen[n] {
			out = append(out, t)
			seen[n] = true
		}
	}
	return out
}

// GetCurrentSystemMessage replays every system message into one leading
// message holding the current prompt and tools.
func GetCurrentSystemMessage(messages []Message) *SystemMessage {
	var content []string
	var sections []SystemSection
	found := false
	var ts int64
	for _, msg := range messages {
		m, ok := msg.(*SystemMessage)
		if !ok {
			continue
		}
		if !found {
			ts, found = m.Timestamp, true
		}
		if m.Content != "" {
			content = append(content, m.Content)
		}
		for _, s := range m.Sections {
			i := -1
			for j := range sections {
				if sections[j].Name == s.Name {
					i = j
				}
			}
			switch {
			case s.Text == nil && i >= 0:
				sections = append(sections[:i], sections[i+1:]...)
			case s.Text != nil && i >= 0:
				sections[i] = s
			case s.Text != nil:
				sections = append(sections, s)
			}
		}
	}
	tools := GetCurrentTools(messages)
	if !found && len(tools) == 0 {
		return nil
	}
	return &SystemMessage{Role: "system", Content: strings.Join(content, "\n\n"), Sections: sections, ToolsAdded: tools, Timestamp: ts}
}

// CollapseSystemMessages rebuilds the transcript for APIs without
// mid-conversation system messages: the replayed system message leads and
// later ones are dropped.
func CollapseSystemMessages(c TranscriptContext) TranscriptContext {
	head := GetCurrentSystemMessage(c.Messages)
	out := make([]Message, 0, len(c.Messages)+1)
	if head != nil {
		out = append(out, head)
	}
	for _, m := range c.Messages {
		if _, ok := m.(*SystemMessage); !ok {
			out = append(out, m)
		}
	}
	return TranscriptContext{Messages: out}
}

// ResolveTranscript keeps later system messages when the model accepts
// them, otherwise collapses them.
func ResolveTranscript(c TranscriptContext, supportsMidConvoSystemMessages bool) TranscriptContext {
	if supportsMidConvoSystemMessages {
		return c
	}
	return CollapseSystemMessages(c)
}

// GetDeclaredTools returns every tool definition the transcript references,
// in first-declaration order.
func GetDeclaredTools(messages []Message) []Tool {
	var out []Tool
	idx := map[string]int{}
	for _, msg := range messages {
		m, ok := msg.(*SystemMessage)
		if !ok {
			continue
		}
		for _, t := range m.ToolsAdded {
			if i, ok := idx[t.Name]; ok {
				out[i] = t
			} else {
				idx[t.Name] = len(out)
				out = append(out, t)
			}
		}
	}
	return out
}

func hasNonAdditiveToolChanges(messages []Message) bool {
	declared := map[string]bool{}
	for _, msg := range messages {
		m, ok := msg.(*SystemMessage)
		if !ok {
			continue
		}
		if len(m.ToolsRemoved) > 0 {
			return true
		}
		for _, t := range m.ToolsAdded {
			if declared[t.Name] {
				return true
			}
			declared[t.Name] = true
		}
	}
	return false
}

// TranscriptTools splits tool declarations between the request's tools
// field and in-place additions.
type TranscriptTools struct {
	RequestTools     []Tool
	AnchorsAdditions bool
}

func ResolveTranscriptTools(messages []Message, supportsToolAdditions bool) TranscriptTools {
	if supportsToolAdditions && !hasNonAdditiveToolChanges(messages) {
		var tools []Tool
		if m := GetInitialSystemMessage(messages); m != nil {
			tools = m.ToolsAdded
		}
		return TranscriptTools{RequestTools: tools, AnchorsAdditions: true}
	}
	return TranscriptTools{RequestTools: GetCurrentTools(messages)}
}
