// Package provider talks to model servers. Currently: OpenAI-compatible
// chat completions with SSE streaming (llama.cpp, vLLM, SGLang, OpenRouter…).
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

// UserAgent identifies atto to providers (some CDNs reject generic agents).
var UserAgent = "atto"

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
}

type Request struct {
	SessionID  string // substituted for "$session" in headers
	Model      string
	Messages   []Message
	Tools      []Tool
	ToolChoice string // "", "auto", "none"
	Effort     string // reasoning effort; "off" disables thinking
	MaxTokens  int
}

// Handler receives streaming deltas. Any field may be nil.
type Handler struct {
	OnReasoning func(delta string)
	OnText      func(delta string)
	OnToolCall  func(index int, id, name string)
}

type Result struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// Client is an OpenAI-compatible chat completions client.
type Client struct {
	BaseURL string
	APIKey  string
	// MaxTokensField names the output limit field ("max_tokens" by default).
	MaxTokensField string
	// ExtraBody is merged into every request body. String placeholders:
	//   "$effort"       the effort level (via EffortMap); the key is dropped
	//                   when effort is "off" and EffortMap has no "off"
	//   "$thinking"     true unless effort is "off"
	//   "$thinkingType" "enabled" or "disabled"
	ExtraBody map[string]any
	EffortMap map[string]string
	// Headers are added to every request; "$session" becomes the session ID.
	Headers map[string]string
	HTTP    *http.Client
	// OnRequest, if set, receives every request body before it is sent.
	OnRequest func(body []byte)
}

// substitute resolves placeholders in v. keep is false when the value
// should be omitted entirely.
func (c *Client) substitute(v any, effort string) (out any, keep bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case string:
		switch x {
		case "$effort":
			if mapped, ok := c.EffortMap[effort]; ok {
				return mapped, true
			}
			if effort == "off" || effort == "" {
				return nil, false
			}
			return effort, true
		case "$thinking":
			return effort != "off", true
		case "$thinkingType":
			if effort == "off" {
				return "disabled", true
			}
			return "enabled", true
		}
		return x, true
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, vv := range x {
			if r, ok := c.substitute(vv, effort); ok {
				m[k] = r
			}
		}
		return m, true
	case []any:
		var a []any
		for _, vv := range x {
			if r, ok := c.substitute(vv, effort); ok {
				a = append(a, r)
			}
		}
		return a, true
	}
	return v, true
}

func (c *Client) body(req Request) ([]byte, error) {
	b := map[string]any{}
	for k, v := range c.ExtraBody {
		if r, ok := c.substitute(v, req.Effort); ok {
			b[k] = r
		}
	}
	b["model"] = req.Model
	b["messages"] = req.Messages
	b["stream"] = true
	b["stream_options"] = map[string]any{"include_usage": true}
	if req.MaxTokens > 0 {
		f := c.MaxTokensField
		if f == "" {
			f = "max_tokens"
		}
		b[f] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		b["tools"] = req.Tools
		if req.ToolChoice != "" {
			b["tool_choice"] = req.ToolChoice
		}
	}
	return json.Marshal(b)
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Timings *struct {
		CacheN int `json:"cache_n"`
	} `json:"timings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Stream sends req and streams the response. On error (including context
// cancellation) the returned Result holds whatever was received so far.
func (c *Client) Stream(ctx context.Context, req Request, h Handler) (Result, error) {
	var res Result
	res.Message.Role = "assistant"

	body, err := c.body(req)
	if err != nil {
		return res, err
	}
	if c.OnRequest != nil {
		c.OnRequest(body)
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("User-Agent", UserAgent)
	hr.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		if v == "$session" {
			if req.SessionID == "" {
				continue
			}
			v = req.SessionID
		}
		hr.Header.Set(k, v)
	}
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
		return res, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	var text, reasoning strings.Builder
	var calls []ToolCall
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Error != nil {
			err = fmt.Errorf("server error: %s", ch.Error.Message)
			break
		}
		if ch.Usage != nil {
			res.Usage.PromptTokens = ch.Usage.PromptTokens
			res.Usage.CompletionTokens = ch.Usage.CompletionTokens
			if d := ch.Usage.PromptTokensDetails; d != nil {
				res.Usage.CachedTokens = d.CachedTokens
			}
		}
		if ch.Timings != nil {
			res.Usage.CachedTokens = max(res.Usage.CachedTokens, ch.Timings.CacheN)
		}
		for _, choice := range ch.Choices {
			d := choice.Delta
			if r := d.ReasoningContent + d.Reasoning; r != "" {
				reasoning.WriteString(r)
				if h.OnReasoning != nil {
					h.OnReasoning(r)
				}
			}
			if d.Content != "" {
				text.WriteString(d.Content)
				if h.OnText != nil {
					h.OnText(d.Content)
				}
			}
			for _, tc := range d.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, ToolCall{Type: "function"})
				}
				call := &calls[tc.Index]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Function.Name += tc.Function.Name
					if h.OnToolCall != nil {
						h.OnToolCall(tc.Index, call.ID, call.Function.Name)
					}
				}
				call.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != nil {
				res.FinishReason = *choice.FinishReason
			}
		}
	}
	if err == nil {
		err = sc.Err()
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}

	res.Message.Content = text.String()
	res.Message.ReasoningContent = reasoning.String()
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", i)
		}
	}
	res.Message.ToolCalls = calls
	return res, err
}
