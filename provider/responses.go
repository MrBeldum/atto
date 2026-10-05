package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReasoningState holds the encrypted reasoning items an assistant turn
// produced. With store:false the server keeps no state, so these are sent
// back verbatim before the turn's message and tool calls. They are only
// valid for the model that made them.
type ReasoningState struct {
	Model string            `json:"model"`
	Items []json.RawMessage `json:"items"`
}

const (
	chatGPTBaseURL = "https://api.openai.com/v1"
	// promptCacheKeyMax is the longest prompt_cache_key the API accepts.
	promptCacheKeyMax = 64
	// callIDMax is the longest call_id the Responses API accepts.
	callIDMax = 64
)

// ResponsesClient speaks the OpenAI Responses API (POST {base}/responses).
type ResponsesClient struct {
	BaseURL string
	APIKey  string
	// KeyFunc, if set, supplies the key per request (OAuth refresh) and
	// takes precedence over APIKey.
	KeyFunc func(context.Context) (string, error)
	// EffortMap translates effort levels to the API's values; a level
	// mapped here (e.g. "off" -> "none") is sent even though "off"
	// otherwise omits the reasoning field.
	EffortMap map[string]string
	// NoReasoning never sends a reasoning field (non-reasoning models).
	NoReasoning bool
	// ExtraBody is merged into every request body.
	ExtraBody map[string]any
	// Headers are added to every request; "$session" becomes the session ID.
	Headers map[string]string
	HTTP    *http.Client
	// OnRequest, if set, receives every request body before it is sent.
	OnRequest func(body []byte)
}

// chatGPTSignIn reports whether key is a Sign in with ChatGPT token (API
// keys start with "sk-"). That path rejects max_output_tokens.
func (c *ResponsesClient) chatGPTSignIn(key string) bool {
	return strings.TrimRight(c.BaseURL, "/") == chatGPTBaseURL && key != "" && !strings.HasPrefix(key, "sk-")
}

// reasoningParam builds the "reasoning" field for effort, or nil to omit it.
func (c *ResponsesClient) reasoningParam(effort string) map[string]any {
	if c.NoReasoning || effort == "" {
		return nil
	}
	mapped, ok := c.EffortMap[effort]
	if effort == "off" {
		if !ok {
			return nil
		}
		return map[string]any{"effort": mapped}
	}
	if !ok {
		mapped = effort
	}
	return map[string]any{"effort": mapped, "summary": "auto"}
}

func (c *ResponsesClient) body(req Request, key string) ([]byte, error) {
	instructions, input := responsesInput(req.Messages, req.Model)
	b := map[string]any{}
	for k, v := range c.ExtraBody {
		if v != nil {
			b[k] = v
		}
	}
	b["model"] = req.Model
	b["input"] = input
	b["stream"] = true
	b["store"] = false
	if instructions != "" {
		b["instructions"] = instructions
	}
	if k := req.SessionID; k != "" {
		b["prompt_cache_key"] = k[:min(len(k), promptCacheKeyMax)]
	}
	if req.MaxTokens > 0 && !c.chatGPTSignIn(key) {
		// The API rejects values below 16.
		b["max_output_tokens"] = max(req.MaxTokens, 16)
	}
	if len(req.Tools) > 0 {
		b["tools"] = responsesTools(req.Tools)
		if req.ToolChoice != "" {
			b["tool_choice"] = req.ToolChoice
		}
	}
	if r := c.reasoningParam(req.Effort); r != nil {
		b["reasoning"] = r
		if _, thinking := r["summary"]; thinking {
			b["include"] = []string{"reasoning.encrypted_content"}
		}
	}
	return json.Marshal(b)
}

// responsesTools converts function tools to Responses' flat form.
func responsesTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		params := t.Function.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, map[string]any{
			"type": "function", "name": t.Function.Name,
			"description": t.Function.Description, "parameters": params,
		})
	}
	return out
}

// callID makes a chat-completions tool call ID acceptable to the Responses
// API (at most 64 characters). It must map equally for a call and its output.
func callID(id string) string {
	if len(id) <= callIDMax {
		return id
	}
	return id[:callIDMax]
}

// responsesInput converts the conversation to Responses input items. The
// first system message becomes "instructions"; later ones become developer
// messages. Reasoning items are replayed only for the model that produced
// them, and only when a message or tool call follows (the API rejects a
// dangling reasoning item).
func responsesInput(msgs []Message, model string) (instructions string, input []any) {
	input = []any{}
	haveInstructions := false
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if !haveInstructions {
				instructions, haveInstructions = m.Content, true
				continue
			}
			input = append(input, map[string]any{"role": "developer", "content": m.Content})
		case "user":
			input = append(input, map[string]any{"role": "user", "content": responsesUserContent(m)})
		case "assistant":
			if m.Content == "" && len(m.ToolCalls) == 0 {
				continue
			}
			if m.Reasoning != nil && m.Reasoning.Model == model {
				for _, it := range m.Reasoning.Items {
					input = append(input, it)
				}
			}
			if m.Content != "" {
				input = append(input, map[string]any{
					"type": "message", "role": "assistant", "status": "completed",
					"content": []map[string]any{{"type": "output_text", "text": m.Content, "annotations": []any{}}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type": "function_call", "call_id": callID(tc.ID), "name": tc.Function.Name, "arguments": args,
				})
			}
		case "tool":
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID(m.ToolCallID), "output": m.Content,
			})
		}
	}
	return instructions, input
}

type respItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type respEvent struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	Arguments   string          `json:"arguments"`
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	// "error" events carry these at the top level.
	Message  string `json:"message"`
	Code     string `json:"code"`
	Response *struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}

// Stream sends req and streams the response. On error (including context
// cancellation) the returned Result holds whatever was received so far.
func (c *ResponsesClient) Stream(ctx context.Context, req Request, h Handler) (Result, error) {
	var res Result
	res.Message.Role = "assistant"

	key, err := bearerKey(ctx, c.APIKey, c.KeyFunc)
	if err != nil {
		return res, err
	}
	body, err := c.body(req, key)
	if err != nil {
		return res, err
	}
	if c.OnRequest != nil {
		c.OnRequest(body)
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	setCommonHeaders(hr, key, c.Headers, req.SessionID)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hr)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return res, apiError(resp.Status, strings.TrimSpace(string(msg)))
	}

	var text, reasoning strings.Builder
	var calls []ToolCall
	var items []json.RawMessage
	byOutput := map[int]int{}  // output_index -> index in calls
	streamed := map[int]bool{} // output_index -> text arrived as deltas
	finished := false
	emitText := func(s string) {
		text.WriteString(s)
		if h.OnText != nil {
			h.OnText(s)
		}
	}
	emitReasoning := func(s string) {
		reasoning.WriteString(s)
		if h.OnReasoning != nil {
			h.OnReasoning(s)
		}
	}
	// call returns the tool call for an output index, creating it if the
	// server skipped output_item.added.
	call := func(oi int) *ToolCall {
		i, ok := byOutput[oi]
		if !ok {
			i = len(calls)
			byOutput[oi] = i
			calls = append(calls, ToolCall{Type: "function"})
		}
		return &calls[i]
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
scan:
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ev respEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta", "response.refusal.delta":
			streamed[ev.OutputIndex] = true
			emitText(ev.Delta)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emitReasoning(ev.Delta)
		case "response.reasoning_summary_part.added":
			// Separate summary parts the way the reasoning view expects.
			if reasoning.Len() > 0 {
				emitReasoning("\n\n")
			}
		case "response.output_item.added":
			var it respItem
			if json.Unmarshal(ev.Item, &it) == nil && it.Type == "function_call" {
				tc := call(ev.OutputIndex)
				tc.ID, tc.Function.Name, tc.Function.Arguments = it.CallID, it.Name, it.Arguments
				if h.OnToolCall != nil {
					h.OnToolCall(byOutput[ev.OutputIndex], tc.ID, tc.Function.Name)
				}
			}
		case "response.function_call_arguments.delta":
			call(ev.OutputIndex).Function.Arguments += ev.Delta
		case "response.function_call_arguments.done":
			if ev.Arguments != "" {
				call(ev.OutputIndex).Function.Arguments = ev.Arguments
			}
		case "response.output_item.done":
			var it respItem
			if json.Unmarshal(ev.Item, &it) != nil {
				break
			}
			switch it.Type {
			case "function_call":
				_, seen := byOutput[ev.OutputIndex]
				tc := call(ev.OutputIndex)
				tc.ID, tc.Function.Name, tc.Function.Arguments = it.CallID, it.Name, it.Arguments
				if !seen && h.OnToolCall != nil {
					h.OnToolCall(byOutput[ev.OutputIndex], tc.ID, tc.Function.Name)
				}
			case "reasoning":
				items = append(items, append(json.RawMessage(nil), ev.Item...))
			case "message":
				if !streamed[ev.OutputIndex] {
					for _, part := range it.Content {
						emitText(part.Text)
					}
				}
			}
		case "response.completed", "response.incomplete", "response.done":
			finished = true
			r := ev.Response
			if r == nil {
				break scan
			}
			if u := r.Usage; u != nil {
				res.Usage.PromptTokens, res.Usage.CompletionTokens = u.InputTokens, u.OutputTokens
				if d := u.InputTokensDetails; d != nil {
					res.Usage.CachedTokens = d.CachedTokens
				}
			}
			if r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens" {
				res.FinishReason = "length"
			}
			break scan
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil {
				msg = ev.Response.Error.Message
				if ev.Response.Error.Code != "" {
					msg = ev.Response.Error.Code + ": " + msg
				}
			}
			err = fmt.Errorf("server error: %s", msg)
			break scan
		case "error":
			msg := ev.Message
			if ev.Code != "" {
				msg = ev.Code + ": " + msg
			}
			err = fmt.Errorf("server error: %s", msg)
			break scan
		}
	}
	if err == nil {
		err = sc.Err()
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && !finished {
		err = fmt.Errorf("stream ended before the response completed")
	}

	res.Message.Content = text.String()
	res.Message.ReasoningContent = reasoning.String()
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", i)
		}
	}
	res.Message.ToolCalls = calls
	if len(items) > 0 {
		res.Message.Reasoning = &ReasoningState{Model: req.Model, Items: items}
	}
	if res.FinishReason == "" {
		res.FinishReason = "stop"
		if len(calls) > 0 {
			res.FinishReason = "tool_calls"
		}
	}
	return res, err
}

// apiError formats an HTTP error, adding a hint for the one failure users
// of Sign in with ChatGPT can fix themselves.
func apiError(status, body string) error {
	if strings.Contains(body, "subscription_sharing_usage_limit_exceeded") {
		body += "\nCheck your ChatGPT usage: https://chatgpt.com/settings/usage"
	}
	return fmt.Errorf("%s: %s", status, body)
}
