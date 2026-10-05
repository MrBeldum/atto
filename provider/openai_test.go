package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamParsesReasoningTextAndToolCalls(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, c := range []string{
			`{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5},"timings":{"cache_n":7}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n: keep-alive\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, ExtraBody: map[string]any{
		"chat_template_kwargs": map[string]any{"reasoning_effort": "$effort", "enable_thinking": "$thinking"},
	}}
	var reasoning, text string
	res, err := c.Stream(context.Background(), Request{Model: "m", Effort: "off", MaxTokens: 9}, Handler{
		OnReasoning: func(s string) { reasoning += s },
		OnText:      func(s string) { text += s },
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "hmm" || text != "ok" || res.Message.ReasoningContent != "hmm" {
		t.Fatalf("reasoning %q text %q", reasoning, text)
	}
	tc := res.Message.ToolCalls
	if len(tc) != 1 || tc[0].ID != "c1" || tc[0].Function.Name != "bash" || tc[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("tool calls %+v", tc)
	}
	if res.Usage != (Usage{PromptTokens: 10, CompletionTokens: 5, CachedTokens: 7}) || res.FinishReason != "tool_calls" {
		t.Fatalf("usage %+v finish %q", res.Usage, res.FinishReason)
	}
	kw := body["chat_template_kwargs"].(map[string]any)
	if kw["reasoning_effort"] != "off" || kw["enable_thinking"] != false || body["max_tokens"] != float64(9) {
		t.Fatalf("request body %v", body)
	}
}
