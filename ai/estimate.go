package ai

import (
	"encoding/json"
	"unicode/utf16"
)

// Port of src/utils/estimate.ts: about 4 characters per token. Lengths
// count UTF-16 code units, as JavaScript's string length does.

const (
	charsPerToken       = 4
	estimatedImageChars = 4800
)

func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

func ceilDiv(n, d int) int { return (n + d - 1) / d }

// CalculateContextTokens is the context size a usage report describes.
func CalculateContextTokens(u Usage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

func EstimateTextTokens(text string) int { return ceilDiv(jsLen(text), charsPerToken) }

func estimateContentChars(content []Content) int {
	n := 0
	for _, c := range content {
		switch b := c.(type) {
		case *TextContent:
			n += jsLen(b.Text)
		case *ImageContent:
			n += estimatedImageChars
		}
	}
	return n
}

func EstimateMessageTokens(msg Message) int {
	switch m := msg.(type) {
	case *SystemMessage:
		return EstimateTextTokens(GetSystemMessageText(m)) + estimateJSONTokens(m.ToolsAdded, len(m.ToolsAdded)) +
			estimateJSONTokens(m.ToolsRemoved, len(m.ToolsRemoved))
	case *UserMessage:
		if m.IsText() {
			return EstimateTextTokens(m.Text)
		}
		return ceilDiv(estimateContentChars(m.Parts), charsPerToken)
	case *ToolResultMessage:
		return ceilDiv(estimateContentChars(m.Content), charsPerToken)
	case *AssistantMessage:
		n := 0
		for _, c := range m.Content {
			switch b := c.(type) {
			case *TextContent:
				n += jsLen(b.Text)
			case *ThinkingContent:
				n += jsLen(b.Thinking)
			case *ToolCall:
				args, _ := json.Marshal(b.Arguments)
				n += jsLen(b.Name) + len(args)
			}
		}
		return ceilDiv(n, charsPerToken)
	}
	return 0
}

func estimateJSONTokens(v any, n int) int {
	if n == 0 {
		return 0
	}
	b, _ := json.Marshal(v)
	return EstimateTextTokens(string(b))
}

// ContextUsageEstimate is the result of EstimateContextTokens.
type ContextUsageEstimate struct {
	Tokens, UsageTokens, TrailingTokens int
	LastUsageIndex                      int // -1 when no usage applies
}

func messageTimestamp(msg Message) int64 {
	switch m := msg.(type) {
	case *SystemMessage:
		return m.Timestamp
	case *UserMessage:
		return m.Timestamp
	case *AssistantMessage:
		return m.Timestamp
	case *ToolResultMessage:
		return m.Timestamp
	}
	return 0
}

// EstimateContextTokens uses the last applicable assistant usage report
// plus an estimate of what followed it.
func EstimateContextTokens(messages []Message) ContextUsageEstimate {
	latest := int64(-1 << 62)
	usageIndex := -1
	var usage Usage
	for i, msg := range messages {
		if m, ok := msg.(*AssistantMessage); ok {
			if m.Timestamp >= latest && m.StopReason != StopAborted && m.StopReason != StopError && CalculateContextTokens(m.Usage) > 0 {
				usage, usageIndex = m.Usage, i
			}
		}
		latest = max(latest, messageTimestamp(msg))
	}
	if usageIndex >= 0 {
		u := CalculateContextTokens(usage)
		trailing := 0
		for _, m := range messages[usageIndex+1:] {
			trailing += EstimateMessageTokens(m)
		}
		return ContextUsageEstimate{Tokens: u + trailing, UsageTokens: u, TrailingTokens: trailing, LastUsageIndex: usageIndex}
	}
	tokens := 0
	for _, m := range messages {
		tokens += EstimateMessageTokens(m)
	}
	return ContextUsageEstimate{Tokens: tokens, TrailingTokens: tokens, LastUsageIndex: -1}
}
