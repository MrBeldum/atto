package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Port of src/api/openai-responses-shared.ts: message and tool conversion
// and stream processing shared by openai-responses and
// openai-codex-responses. Grammar (custom) tools, additional_tools and
// client tool search are not ported.

func encodeTextSignatureV1(id, phase string) string {
	b, _ := json.Marshal(TextSignatureV1{V: 1, ID: id, Phase: phase})
	return string(b)
}

func parseTextSignature(signature string) (id, phase string, ok bool) {
	if signature == "" {
		return "", "", false
	}
	if strings.HasPrefix(signature, "{") {
		var p struct {
			V     int     `json:"v"`
			ID    *string `json:"id"`
			Phase string  `json:"phase"`
		}
		if json.Unmarshal([]byte(signature), &p) == nil && p.V == 1 && p.ID != nil {
			if p.Phase == "commentary" || p.Phase == "final_answer" {
				return *p.ID, p.Phase, true
			}
			return *p.ID, "", true
		}
	}
	return signature, "", true
}

func convertToolResultOutput(model *Model, content []Content) any {
	text := ContentText(content, "\n")
	var images []*ImageContent
	for _, c := range content {
		if im, ok := c.(*ImageContent); ok {
			images = append(images, im)
		}
	}
	if len(images) == 0 || !model.SupportsImages() {
		switch {
		case text != "":
			return text
		case len(images) > 0:
			return "(see attached image)"
		}
		return "(no tool output)"
	}
	out := []object{}
	if text != "" {
		out = append(out, obj("type", "input_text", "text", text))
	}
	for _, im := range images {
		out = append(out, obj("type", "input_image", "detail", "auto", "image_url", "data:"+im.MimeType+";base64,"+im.Data))
	}
	return out
}

// ConvertResponsesMessagesOptions tune ConvertResponsesMessages.
type ConvertResponsesMessagesOptions struct {
	// IncludeSystemPrompt sends the leading system message as an input
	// item (default true; Codex sends it as "instructions").
	ExcludeSystemPrompt            bool
	SupportsMidConvoSystemMessages bool
}

var nonIDPartChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
var trailingUnderscores = regexp.MustCompile(`_+$`)

func normalizeIDPart(part string) string {
	s := nonIDPartChars.ReplaceAllString(part, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return trailingUnderscores.ReplaceAllString(s, "")
}

// ConvertResponsesMessages converts the transcript to Responses input items.
func ConvertResponsesMessages(model *Model, context TranscriptContext, allowedToolCallProviders map[string]bool, opts ConvertResponsesMessagesOptions) []any {
	normalized := ResolveTranscript(context, opts.SupportsMidConvoSystemMessages)
	messages := []any{}

	buildForeignItemID := func(itemID string) string {
		s := "fc_" + ShortHash(itemID)
		if len(s) > 64 {
			s = s[:64]
		}
		return s
	}
	normalizeToolCallID := func(id string, _ *Model, source *AssistantMessage) string {
		if !allowedToolCallProviders[model.Provider] || !strings.Contains(id, "|") {
			return normalizeIDPart(id)
		}
		callID, itemID, _ := strings.Cut(id, "|")
		itemID, _, _ = strings.Cut(itemID, "|")
		normalizedCallID := normalizeIDPart(callID)
		foreign := source.Provider != model.Provider || source.Api != model.Api
		var normalizedItemID string
		if foreign {
			normalizedItemID = buildForeignItemID(itemID)
		} else {
			normalizedItemID = normalizeIDPart(itemID)
		}
		// OpenAI requires function call item ids to start with "fc".
		if !strings.HasPrefix(normalizedItemID, "fc_") {
			normalizedItemID = normalizeIDPart("fc_" + normalizedItemID)
		}
		return normalizedCallID + "|" + normalizedItemID
	}

	transformed := TransformMessages(normalized.Messages, model, normalizeToolCallID)
	instructionRole := "system"
	if model.Reasoning && (model.Compat == nil || model.Compat.SupportsDeveloperRole == nil || *model.Compat.SupportsDeveloperRole) {
		instructionRole = "developer"
	}

	msgIndex := 0
	for sourceIndex, msg := range transformed {
		_, isSystem := msg.(*SystemMessage)
		leading := sourceIndex == 0 && isSystem
		switch m := msg.(type) {
		case *SystemMessage:
			if !leading || !opts.ExcludeSystemPrompt {
				text := RenderSystemMessageUpdate(m)
				if leading {
					text = GetSystemMessageText(m)
				}
				if text != "" {
					messages = append(messages, obj("role", instructionRole, "content", text))
				}
			}
		case *UserMessage:
			if m.IsText() {
				messages = append(messages, obj("role", "user", "content", []object{obj("type", "input_text", "text", m.Text)}))
				break
			}
			content := []object{}
			for _, item := range m.Parts {
				switch b := item.(type) {
				case *TextContent:
					content = append(content, obj("type", "input_text", "text", b.Text))
				case *ImageContent:
					content = append(content, obj("type", "input_image", "detail", "auto", "image_url", "data:"+b.MimeType+";base64,"+b.Data))
				}
			}
			if len(content) == 0 {
				continue
			}
			messages = append(messages, obj("role", "user", "content", content))
		case *AssistantMessage:
			var output []any
			sameProviderAndApi := m.Provider == model.Provider && m.Api == model.Api
			sameModel := sameProviderAndApi && m.Model == model.ID
			differentModel := sameProviderAndApi && m.Model != model.ID
			textIndex := 0
			for _, block := range m.Content {
				switch b := block.(type) {
				case *ThinkingContent:
					if b.ThinkingSignature != "" {
						output = append(output, json.RawMessage(b.ThinkingSignature))
					}
				case *TextContent:
					id, phase, _ := parseTextSignature(b.TextSignature)
					fallback := fmt.Sprintf("msg_pi_%d", msgIndex)
					if textIndex > 0 {
						fallback = fmt.Sprintf("msg_pi_%d_%d", msgIndex, textIndex)
					}
					textIndex++
					switch {
					case id == "":
						id = fallback
					case len(id) > 64:
						id = "msg_" + ShortHash(id)
					}
					item := obj("type", "message", "role", "assistant",
						"content", []object{obj("type", "output_text", "text", b.Text, "annotations", []any{})},
						"status", "completed", "id", id)
					if phase != "" {
						item.set("phase", phase)
					}
					output = append(output, item)
				case *ToolCall:
					callID, itemID, _ := strings.Cut(b.ID, "|")
					// Omit the item id for another model's calls (OpenAI pairs
					// fc_ ids with reasoning items) and for ids of another type.
					item := obj("type", "function_call")
					if !differentModel && strings.HasPrefix(itemID, "fc_") {
						item.set("id", itemID)
					}
					item = append(item, field{"call_id", callID}, field{"name", b.Name}, field{"arguments", toolCallArguments(b)})
					if sameModel && b.Namespace != "" {
						item.set("namespace", b.Namespace)
					}
					output = append(output, item)
				}
			}
			if len(output) == 0 {
				continue
			}
			messages = append(messages, output...)
		case *ToolResultMessage:
			callID, _, _ := strings.Cut(m.ToolCallID, "|")
			messages = append(messages, obj("type", "function_call_output", "call_id", callID, "output", convertToolResultOutput(model, m.Content)))
		}
		if !leading {
			msgIndex++
		}
	}
	return messages
}

// ConvertResponsesTools converts tools to Responses' flat function form.
// strict is sent only when supportsStrictMode; its value is defaultStrict
// (nil sends null, as Codex does).
func ConvertResponsesTools(tools []Tool, supportsStrictMode bool, defaultStrict *bool) []object {
	out := make([]object, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		fn := obj("type", "function", "name", t.Name, "description", t.Description, "parameters", params)
		if supportsStrictMode {
			if defaultStrict == nil {
				fn.set("strict", nil)
			} else {
				fn.set("strict", *defaultStrict)
			}
		}
		out = append(out, fn)
	}
	return out
}

// responsesEvent is the subset of a Responses stream event pi reads.
type responsesEvent struct {
	Type        string          `json:"type"`
	OutputIndex int             `json:"output_index"`
	Delta       string          `json:"delta"`
	Arguments   *string         `json:"arguments"`
	Item        json.RawMessage `json:"item"`
	Response    *responseObject `json:"response"`
	Code        any             `json:"code"`
	Message     string          `json:"message"`
}

type responseObject struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	ServiceTier       string            `json:"service_tier"`
	EndTurn           *bool             `json:"end_turn"`
	Output            []json.RawMessage `json:"output"`
	IncompleteDetails *struct {
		Reason any `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

type responsesItem struct {
	Type             string  `json:"type"`
	ID               string  `json:"id"`
	CallID           string  `json:"call_id"`
	Name             string  `json:"name"`
	Arguments        *string `json:"arguments"`
	Namespace        string  `json:"namespace"`
	Phase            string  `json:"phase"`
	EncryptedContent string  `json:"encrypted_content"`
	Summary          []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type responsesSlot struct {
	kind         string // "thinking", "text", "toolCall"
	thinking     *ThinkingContent
	text         *TextContent
	toolCall     *ToolCall
	partialJSON  *string
	contentIndex int
}

// ResponsesStreamOptions tune ProcessResponsesStream.
type ResponsesStreamOptions struct {
	ServiceTier             string
	ResolveServiceTier      func(response, request string) string
	ApplyServiceTierPricing func(usage *Usage, serviceTier string)
}

// ProcessResponsesStream turns Responses events into stream events,
// filling output. next returns the next event, or ok false at the end.
func ProcessResponsesStream(next func() (responsesEvent, bool, error), output *AssistantMessage, stream *AssistantMessageEventStream, model *Model, opts ResponsesStreamOptions) error {
	sawTerminal := false
	slots := map[int]*responsesSlot{}
	reasoningByID := map[string]*ThinkingContent{}

	push := func(ev AssistantMessageEvent) { ev.Partial = output; stream.Push(ev) }
	applyPhase := func(it responsesItem) {
		if it.Type == "message" && it.Phase == "final_answer" {
			output.StopReason = StopStop
		}
	}
	createSlot := func(oi int, raw json.RawMessage) *responsesSlot {
		var it responsesItem
		if json.Unmarshal(raw, &it) != nil {
			return nil
		}
		var s *responsesSlot
		switch it.Type {
		case "reasoning":
			b := NewThinking("")
			output.Content = append(output.Content, b)
			s = &responsesSlot{kind: "thinking", thinking: b, contentIndex: len(output.Content) - 1}
			slots[oi] = s
			push(AssistantMessageEvent{Type: EventThinkingStart, ContentIndex: s.contentIndex})
		case "message":
			applyPhase(it)
			b := NewText("")
			output.Content = append(output.Content, b)
			s = &responsesSlot{kind: "text", text: b, contentIndex: len(output.Content) - 1}
			slots[oi] = s
			push(AssistantMessageEvent{Type: EventTextStart, ContentIndex: s.contentIndex})
		case "function_call":
			b := NewToolCall(it.CallID+"|"+it.ID, it.Name, nil)
			b.Namespace = it.Namespace
			partial := ""
			if it.Arguments != nil {
				partial = *it.Arguments
			}
			output.Content = append(output.Content, b)
			s = &responsesSlot{kind: "toolCall", toolCall: b, partialJSON: &partial, contentIndex: len(output.Content) - 1}
			slots[oi] = s
			push(AssistantMessageEvent{Type: EventToolCallStart, ContentIndex: s.contentIndex})
		}
		return s
	}
	getSlot := func(oi int, kind string) *responsesSlot {
		if s := slots[oi]; s != nil && s.kind == kind {
			return s
		}
		return nil
	}
	// Azure can omit encrypted_content from output_item.done and send it
	// only in response.completed; backfill it for stateless replay.
	backfill := func(out []json.RawMessage) {
		for _, raw := range out {
			var it responsesItem
			if json.Unmarshal(raw, &it) != nil || it.Type != "reasoning" || it.EncryptedContent == "" {
				continue
			}
			b := reasoningByID[it.ID]
			if b == nil || b.ThinkingSignature == "" {
				continue
			}
			var stored map[string]any
			if json.Unmarshal([]byte(b.ThinkingSignature), &stored) != nil {
				continue
			}
			if s, _ := stored["encrypted_content"].(string); s != "" {
				continue
			}
			stored["encrypted_content"] = it.EncryptedContent
			nb, _ := json.Marshal(stored)
			b.ThinkingSignature = string(nb)
		}
	}
	finalize := func(r *responseObject) {
		sawTerminal = true
		if r == nil {
			r = &responseObject{}
		}
		backfill(r.Output)
		if r.ID != "" {
			output.ResponseID = r.ID
		}
		if u := r.Usage; u != nil {
			cached, write := 0, 0
			if d := u.InputTokensDetails; d != nil {
				cached, write = d.CachedTokens, d.CacheWriteTokens
			}
			reasoning := 0
			if d := u.OutputTokensDetails; d != nil {
				reasoning = d.ReasoningTokens
			}
			// OpenAI includes cached and cache-write tokens in input_tokens.
			output.Usage = Usage{
				Input: max(0, u.InputTokens-cached-write), Output: u.OutputTokens,
				CacheRead: cached, CacheWrite: write, Reasoning: &reasoning, TotalTokens: u.TotalTokens,
			}
		}
		CalculateCost(model, &output.Usage)
		if opts.ApplyServiceTierPricing != nil {
			tier := r.ServiceTier
			if opts.ResolveServiceTier != nil {
				tier = opts.ResolveServiceTier(r.ServiceTier, opts.ServiceTier)
			} else if tier == "" {
				tier = opts.ServiceTier
			}
			opts.ApplyServiceTierPricing(&output.Usage, tier)
		}
		incompleteReason := ""
		if r.IncompleteDetails != nil {
			incompleteReason, _ = r.IncompleteDetails.Reason.(string)
		}
		output.RawStopReason = r.Status
		if incompleteReason != "" {
			output.RawStopReason = r.Status + "." + incompleteReason
		}
		stop, msg := mapResponsesStopReason(r.Status, incompleteReason)
		output.StopReason, output.ErrorMessage = stop, msg
		if hasToolCall(output) && output.StopReason == StopStop {
			output.StopReason = StopToolUse
		}
	}

	for {
		ev, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		switch ev.Type {
		case "response.created":
			if ev.Response != nil {
				output.ResponseID = ev.Response.ID
			}
		case "response.output_item.added":
			createSlot(ev.OutputIndex, ev.Item)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if s := getSlot(ev.OutputIndex, "thinking"); s != nil {
				s.thinking.Thinking += ev.Delta
				push(AssistantMessageEvent{Type: EventThinkingDelta, ContentIndex: s.contentIndex, Delta: ev.Delta})
			}
		case "response.reasoning_summary_part.done":
			if s := getSlot(ev.OutputIndex, "thinking"); s != nil {
				s.thinking.Thinking += "\n\n"
				push(AssistantMessageEvent{Type: EventThinkingDelta, ContentIndex: s.contentIndex, Delta: "\n\n"})
			}
		case "response.output_text.delta", "response.refusal.delta":
			if s := getSlot(ev.OutputIndex, "text"); s != nil {
				s.text.Text += ev.Delta
				push(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: s.contentIndex, Delta: ev.Delta})
			}
		case "response.function_call_arguments.delta":
			if s := getSlot(ev.OutputIndex, "toolCall"); s != nil && s.partialJSON != nil {
				*s.partialJSON += ev.Delta
				s.toolCall.Arguments = ParseStreamingJSON(*s.partialJSON)
				push(AssistantMessageEvent{Type: EventToolCallDelta, ContentIndex: s.contentIndex, Delta: ev.Delta})
			}
		case "response.function_call_arguments.done":
			if s := getSlot(ev.OutputIndex, "toolCall"); s != nil && s.partialJSON != nil && ev.Arguments != nil {
				prev := *s.partialJSON
				*s.partialJSON = *ev.Arguments
				s.toolCall.Arguments = ParseStreamingJSON(*ev.Arguments)
				if strings.HasPrefix(*ev.Arguments, prev) && len(*ev.Arguments) > len(prev) {
					push(AssistantMessageEvent{Type: EventToolCallDelta, ContentIndex: s.contentIndex, Delta: (*ev.Arguments)[len(prev):]})
				}
			}
		case "response.output_item.done":
			var it responsesItem
			if json.Unmarshal(ev.Item, &it) != nil {
				break
			}
			applyPhase(it)
			s := slots[ev.OutputIndex]
			if s == nil {
				s = createSlot(ev.OutputIndex, ev.Item)
			}
			if s == nil {
				break
			}
			switch {
			case it.Type == "reasoning" && s.kind == "thinking":
				var parts []string
				for _, p := range it.Summary {
					parts = append(parts, p.Text)
				}
				summary := strings.Join(parts, "\n\n")
				parts = nil
				for _, p := range it.Content {
					parts = append(parts, p.Text)
				}
				content := strings.Join(parts, "\n\n")
				switch {
				case summary != "":
					s.thinking.Thinking = summary
				case content != "":
					s.thinking.Thinking = content
				}
				// The item replays verbatim on later turns (store: false).
				s.thinking.ThinkingSignature = string(ev.Item)
				reasoningByID[it.ID] = s.thinking
				push(AssistantMessageEvent{Type: EventThinkingEnd, ContentIndex: s.contentIndex, Content: s.thinking.Thinking})
				delete(slots, ev.OutputIndex)
			case it.Type == "message" && s.kind == "text":
				var b strings.Builder
				for _, c := range it.Content {
					if c.Type == "output_text" {
						b.WriteString(c.Text)
					} else {
						b.WriteString(c.Refusal)
					}
				}
				s.text.Text = b.String()
				s.text.TextSignature = encodeTextSignatureV1(it.ID, it.Phase)
				push(AssistantMessageEvent{Type: EventTextEnd, ContentIndex: s.contentIndex, Content: s.text.Text})
				delete(slots, ev.OutputIndex)
			case it.Type == "function_call" && s.kind == "toolCall" && s.partialJSON != nil:
				args := *s.partialJSON
				if it.Arguments != nil && *it.Arguments != "" {
					args = *it.Arguments
				}
				if args == "" {
					args = "{}"
				}
				s.toolCall.Arguments = ParseStreamingJSON(args)
				s.toolCall.RawArguments = args
				if it.Namespace != "" {
					s.toolCall.Namespace = it.Namespace
				}
				s.partialJSON = nil
				push(AssistantMessageEvent{Type: EventToolCallEnd, ContentIndex: s.contentIndex, ToolCall: s.toolCall})
				delete(slots, ev.OutputIndex)
			}
		case "response.completed", "response.incomplete":
			finalize(ev.Response)
		case "error":
			return fmt.Errorf("Error Code %v: %s", ev.Code, ev.Message)
		case "response.failed":
			sawTerminal = true
			msg := "Unknown error (no error details in response)"
			if r := ev.Response; r != nil {
				output.RawStopReason = r.Status
				switch {
				case r.Error != nil:
					code, m := r.Error.Code, r.Error.Message
					if code == "" {
						code = "unknown"
					}
					if m == "" {
						m = "no message"
					}
					msg = code + ": " + m
				case r.IncompleteDetails != nil && r.IncompleteDetails.Reason != nil:
					msg = fmt.Sprintf("incomplete: %v", r.IncompleteDetails.Reason)
				}
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if !sawTerminal {
		return fmt.Errorf("OpenAI Responses stream ended before a terminal response event")
	}
	// Every tool call in the final message runs; refuse calls whose
	// output_item.done never arrived.
	if output.StopReason == StopToolUse {
		for _, s := range slots {
			if s.kind == "toolCall" && s.partialJSON != nil {
				return fmt.Errorf("OpenAI Responses stream completed with an unfinished tool call: %s (%s)", s.toolCall.Name, s.toolCall.ID)
			}
		}
	}
	return nil
}

func mapResponsesStopReason(status, incompleteReason string) (StopReason, string) {
	switch status {
	case "", "completed", "in_progress", "queued":
		return StopStop, ""
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return StopLength, ""
		}
		if incompleteReason != "" {
			return StopError, "Response incomplete: " + incompleteReason
		}
		return StopError, "Response incomplete without a provider reason"
	case "failed", "cancelled":
		return StopError, ""
	}
	return StopError, "Unhandled stop reason: " + status
}

// sseResponsesEvents adapts an SSE event source to ProcessResponsesStream.
func sseResponsesEvents(src *sseSource) func() (responsesEvent, bool, error) {
	return func() (responsesEvent, bool, error) {
		for {
			ev, ok, err := src.next()
			if err != nil || !ok {
				return responsesEvent{}, false, err
			}
			if ev.Data == "" || strings.HasPrefix(ev.Data, "[DONE]") {
				continue
			}
			var re responsesEvent
			if json.Unmarshal([]byte(ev.Data), &re) != nil {
				continue
			}
			return re, true, nil
		}
	}
}
