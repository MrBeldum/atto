package provider

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/sebastianrcnt/atto/ai"
)

func TestClientToolCallDeltas(t *testing.T) {
	srv, _, _ := serveSSE(t,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\": \"ec"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ho hi\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"bash","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	c := &Client{Model: ai.Model{ID: "m", Api: ai.ApiOpenAICompletions, Provider: "local", BaseURL: srv.URL, Input: []string{"text"}}}
	var log []string
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, Handler{
		OnToolCallStart: func(i int) { log = append(log, fmt.Sprintf("start %d", i)) },
		OnToolCallDelta: func(i int, args string) { log = append(log, fmt.Sprintf("delta %d %s", i, args)) },
		OnToolCall:      func(i int, id, name string) { log = append(log, fmt.Sprintf("end %d %s", i, id)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The arguments come as what was received so far, per call.
	want := []string{
		"start 0",
		`delta 0 {"command": "ec`,
		`delta 0 {"command": "echo hi"}`,
		"start 1",
		"delta 1 {}",
		"end 0 c1",
		"end 1 c2",
	}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("got %q\nwant %q", log, want)
	}
}

func TestClientResponsesToolCallDeltas(t *testing.T) {
	srv, _, _ := serveSSE(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"command\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"ls\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`)
	c := &Client{Model: ai.Model{ID: "gpt-x", Api: ai.ApiOpenAIResponses, Provider: "openai", BaseURL: srv.URL}, APIKey: "sk-1"}
	var last string
	starts := 0
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, Handler{
		OnToolCallStart: func(int) { starts++ },
		OnToolCallDelta: func(_ int, args string) { last = args },
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || last != `{"command":"ls"}` {
		t.Fatalf("starts %d, last %q", starts, last)
	}
}
