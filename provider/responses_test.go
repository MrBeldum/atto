package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sse writes events as a Responses stream.
func sse(w http.ResponseWriter, events ...string) {
	for _, e := range events {
		fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
	}
}

const reasoningItem = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"ENC"}`

func TestResponsesStreamTextReasoningUsage(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header
		if r.URL.Path != "/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sse(w,
			`{"type":"response.created","response":{}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`,
			`{"type":"response.reasoning_summary_part.added","output_index":0}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"plan"}`,
			`{"type":"response.reasoning_summary_part.added","output_index":0}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"more"}`,
			`{"type":"response.output_item.done","output_index":0,"item":`+reasoningItem+`}`,
			`{"type":"response.output_text.delta","output_index":1,"delta":"Hel"}`,
			`{"type":"response.output_text.delta","output_index":1,"delta":"lo"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"Hello"}]}}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":64},"output_tokens":20}}}`,
		)
	}))
	defer srv.Close()

	c := &ResponsesClient{BaseURL: srv.URL, APIKey: "sk-test", Headers: map[string]string{"session_id": "$session"}}
	var reasoning, text string
	res, err := c.Stream(context.Background(), Request{
		SessionID: "sess-1", Model: "gpt-5.2", Effort: "high", MaxTokens: 4096,
		Messages: []Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}},
	}, Handler{OnReasoning: func(s string) { reasoning += s }, OnText: func(s string) { text += s }})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "plan\n\nmore" || text != "Hello" || res.Message.Content != "Hello" || res.Message.ReasoningContent != reasoning {
		t.Fatalf("reasoning %q text %q msg %+v", reasoning, text, res.Message)
	}
	if res.Usage != (Usage{PromptTokens: 100, CompletionTokens: 20, CachedTokens: 64}) || res.FinishReason != "stop" {
		t.Fatalf("usage %+v finish %q", res.Usage, res.FinishReason)
	}
	rs := res.Message.Reasoning
	if rs == nil || rs.Model != "gpt-5.2" || len(rs.Items) != 1 || !strings.Contains(string(rs.Items[0]), `"ENC"`) {
		t.Fatalf("reasoning state %+v", rs)
	}

	if body["stream"] != true || body["store"] != false || body["instructions"] != "be brief" ||
		body["prompt_cache_key"] != "sess-1" || body["max_output_tokens"] != float64(4096) {
		t.Fatalf("body %v", body)
	}
	r := body["reasoning"].(map[string]any)
	if r["effort"] != "high" || r["summary"] != "auto" || fmt.Sprint(body["include"]) != "[reasoning.encrypted_content]" {
		t.Fatalf("reasoning param %v include %v", r, body["include"])
	}
	if hdr.Get("Authorization") != "Bearer sk-test" || hdr.Get("session_id") != "sess-1" {
		t.Fatalf("headers %v", hdr)
	}
}

func TestResponsesFunctionCallRoundTrip(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if len(bodies) == 1 {
			sse(w,
				`{"type":"response.output_item.done","output_index":0,"item":`+reasoningItem+`}`,
				`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"bash","arguments":""}}`,
				`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"command\":"}`,
				`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"ls\"}"}`,
				`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"bash","arguments":"{\"command\":\"ls\"}"}}`,
				`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
			)
			return
		}
		sse(w,
			`{"type":"response.output_text.delta","output_index":0,"delta":"done"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":30,"input_tokens_details":{"cached_tokens":10},"output_tokens":1}}}`,
		)
	}))
	defer srv.Close()

	c := &ResponsesClient{BaseURL: srv.URL, APIKey: "k"}
	tools := []Tool{{Type: "function", Function: ToolFunction{Name: "bash", Description: "run", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	var started []string
	msgs := []Message{{Role: "system", Content: "s"}, {Role: "user", Content: "list"}}
	res, err := c.Stream(context.Background(), Request{Model: "gpt-5.2", Effort: "medium", Messages: msgs, Tools: tools},
		Handler{OnToolCall: func(i int, id, name string) { started = append(started, fmt.Sprint(i, id, name)) }})
	if err != nil {
		t.Fatal(err)
	}
	tc := res.Message.ToolCalls
	if len(tc) != 1 || tc[0].ID != "call_9" || tc[0].Function.Arguments != `{"command":"ls"}` || res.FinishReason != "tool_calls" {
		t.Fatalf("calls %+v finish %q", tc, res.FinishReason)
	}
	if len(started) != 1 || started[0] != "0call_9bash" {
		t.Fatalf("OnToolCall %v", started)
	}
	tool0 := bodies[0]["tools"].([]any)[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "bash" || tool0["parameters"] == nil {
		t.Fatalf("tool %v", tool0)
	}

	msgs = append(msgs, res.Message, Message{Role: "tool", ToolCallID: "call_9", Content: "a.txt"})
	if _, err := c.Stream(context.Background(), Request{Model: "gpt-5.2", Effort: "medium", Messages: msgs, Tools: tools}, Handler{}); err != nil {
		t.Fatal(err)
	}
	input := bodies[1]["input"].([]any)
	var types []string
	for _, it := range input {
		m := it.(map[string]any)
		ty, _ := m["type"].(string)
		if ty == "" {
			ty = m["role"].(string)
		}
		types = append(types, ty)
	}
	// The encrypted reasoning item must come back, in order, before the call.
	if strings.Join(types, ",") != "user,reasoning,function_call,function_call_output" {
		t.Fatalf("input order %v", types)
	}
	if input[1].(map[string]any)["encrypted_content"] != "ENC" {
		t.Fatalf("reasoning item not replayed: %v", input[1])
	}
	out := input[3].(map[string]any)
	if out["call_id"] != "call_9" || out["output"] != "a.txt" {
		t.Fatalf("output item %v", out)
	}
}

func TestResponsesErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		events []string
		want   string
	}{
		"http":    {status: 429, want: "429"},
		"failed":  {events: []string{`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`}, want: "server_error: boom"},
		"event":   {events: []string{`{"type":"response.output_text.delta","delta":"par"}`, `{"type":"error","code":"rate_limit","message":"slow down"}`}, want: "rate_limit: slow down"},
		"cutoff":  {events: []string{`{"type":"response.output_text.delta","delta":"par"}`}, want: "before the response completed"},
		"ratecap": {status: 429, events: []string{`{"error":"subscription_sharing_usage_limit_exceeded"}`}, want: "chatgpt.com/settings/usage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					io.WriteString(w, strings.Join(tc.events, ""))
					return
				}
				sse(w, tc.events...)
			}))
			defer srv.Close()
			c := &ResponsesClient{BaseURL: srv.URL}
			res, err := c.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, Handler{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if name == "event" && res.Message.Content != "par" {
				t.Fatalf("partial text lost: %+v", res.Message)
			}
		})
	}
}

func TestResponsesIncompleteLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":2}}}`)
	}))
	defer srv.Close()
	res, err := (&ResponsesClient{BaseURL: srv.URL}).Stream(context.Background(), Request{Model: "m"}, Handler{})
	if err != nil || res.FinishReason != "length" {
		t.Fatalf("%v %q", err, res.FinishReason)
	}
}

func TestResponsesInput(t *testing.T) {
	long := strings.Repeat("c", 100)
	state := &ReasoningState{Model: "m", Items: []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)}}
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", Reasoning: state, ToolCalls: []ToolCall{{ID: long, Type: "function", Function: FunctionCall{Name: "f"}}}},
		{Role: "tool", ToolCallID: long, Content: "res"},
		{Role: "assistant", Reasoning: state}, // reasoning only: must be dropped
		{Role: "system", Content: "later"},
	}
	instr, input := responsesInput(msgs, "m")
	if instr != "sys" {
		t.Fatalf("instructions %q", instr)
	}
	b, _ := json.Marshal(input)
	got := string(b)
	for _, want := range []string{
		`{"content":[{"text":"hi","type":"input_text"}],"role":"user"}`,
		`{"type":"reasoning","id":"rs_1"}`,
		`"type":"output_text"`,
		`"arguments":"{}"`,
		`"call_id":"` + long[:64] + `"`,
		`{"content":"later","role":"developer"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("input missing %s in %s", want, got)
		}
	}
	if strings.Count(got, "reasoning") != 1 || strings.Contains(got, long) {
		t.Errorf("unexpected input %s", got)
	}
	// A different model cannot use another model's encrypted reasoning.
	_, input = responsesInput(msgs, "other")
	if b, _ := json.Marshal(input); strings.Contains(string(b), "rs_1") {
		t.Errorf("reasoning replayed across models: %s", b)
	}
}

func TestResponsesBodyEffortAndSignIn(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "x"}}
	build := func(c *ResponsesClient, req Request, key string) map[string]any {
		req.Messages = msgs
		b, err := c.body(req, key)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	c := &ResponsesClient{BaseURL: "https://api.openai.com/v1"}
	if m := build(c, Request{Model: "m", Effort: "off"}, "sk-x"); m["reasoning"] != nil || m["include"] != nil {
		t.Fatalf("off without a mapping must omit reasoning: %v", m)
	}
	c.EffortMap = map[string]string{"off": "none", "max": "xhigh"}
	if m := build(c, Request{Model: "m", Effort: "off"}, "sk-x"); fmt.Sprint(m["reasoning"]) != "map[effort:none]" || m["include"] != nil {
		t.Fatalf("off mapped: %v", m)
	}
	if m := build(c, Request{Model: "m", Effort: "max"}, "sk-x"); m["reasoning"].(map[string]any)["effort"] != "xhigh" {
		t.Fatalf("mapped effort: %v", m)
	}
	c.NoReasoning = true
	if m := build(c, Request{Model: "m", Effort: "high"}, "sk-x"); m["reasoning"] != nil {
		t.Fatalf("NoReasoning: %v", m)
	}
	// Sign in with ChatGPT rejects max_output_tokens; API keys and other hosts keep it.
	if m := build(c, Request{Model: "m", MaxTokens: 100}, "eyJ.access"); m["max_output_tokens"] != nil {
		t.Fatalf("sign-in token must omit max_output_tokens: %v", m)
	}
	if m := build(c, Request{Model: "m", MaxTokens: 100}, "sk-x"); m["max_output_tokens"] != float64(100) {
		t.Fatalf("api key: %v", m)
	}
	c.BaseURL = "https://opencode.ai/zen/v1"
	if m := build(c, Request{Model: "m", MaxTokens: 100}, "zen-key"); m["max_output_tokens"] != float64(100) {
		t.Fatalf("gateway: %v", m)
	}
}

func TestCompletionsDropsResponsesState(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	state := &ReasoningState{Model: "m", Items: []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)}}
	msgs := []Message{{Role: "assistant", Content: "a", Reasoning: state}}
	if _, err := (&Client{BaseURL: srv.URL}).Stream(context.Background(), Request{Model: "m", Messages: msgs}, Handler{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(body); strings.Contains(string(b), "responses_reasoning") {
		t.Fatalf("sent Responses state to chat completions: %s", b)
	}
	if msgs[0].Reasoning == nil {
		t.Fatal("caller's history was modified")
	}
}

func TestKeyFuncOverridesAPIKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		sse(w, `{"type":"response.completed","response":{}}`)
	}))
	defer srv.Close()
	c := &ResponsesClient{BaseURL: srv.URL, APIKey: "stale", KeyFunc: func(context.Context) (string, error) { return "fresh", nil }}
	if _, err := c.Stream(context.Background(), Request{Model: "m"}, Handler{}); err != nil || got != "Bearer fresh" {
		t.Fatalf("%v %q", err, got)
	}
}
