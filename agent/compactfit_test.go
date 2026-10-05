package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
)

func msgs(roles ...string) []provider.Message {
	var out []provider.Message
	for i, r := range roles {
		out = append(out, provider.Message{Role: r, Content: strings.Repeat("x", 4000) + fmt.Sprint(i)})
	}
	return out
}

// The case from a real session: 232.4k tokens of conversation on a 262,144
// window whose maxTokens (32,768) no longer fit with it.
func TestFitCompactionLowersMaxTokens(t *testing.T) {
	m := config.Model{ContextWindow: 262144, MaxTokens: 32768}
	req := provider.Request{Messages: msgs("system", "user", "assistant", "user", "user"), MaxTokens: m.MaxTokens}
	if dropped := fitCompaction(&req, m, 232400, compactRoom); dropped != 0 {
		t.Fatalf("dropped %d with room to spare", dropped)
	}
	if req.MaxTokens >= 32768 || req.MaxTokens < compactRoom || 232400+req.MaxTokens > 262144 {
		t.Fatalf("max tokens %d", req.MaxTokens)
	}
}

// With too little room left, whole oldest turns go, a tool call keeping its
// result, and the system prompt and the compaction prompt stay.
func TestFitCompactionDropsOldestTurns(t *testing.T) {
	m := config.Model{ContextWindow: 10000, MaxTokens: 4000}
	all := msgs("system", "user", "assistant", "tool", "user", "assistant", "user", "user")
	req := provider.Request{Messages: append([]provider.Message(nil), all...), MaxTokens: m.MaxTokens}
	dropped := fitCompaction(&req, m, 8500, 3000)
	if dropped != 3 {
		t.Fatalf("dropped %d", dropped)
	}
	got := req.Messages
	if got[0].Role != "system" || got[1].Role != "user" || got[1].Content != all[4].Content || got[len(got)-1].Content != all[7].Content {
		t.Fatalf("kept %v", got)
	}
}

func TestContextExceeded(t *testing.T) {
	for _, s := range []string{
		"prompt (232400 tokens) + max tokens (32768) exceeds the context (262144); requests are never truncated",
		"This model's maximum context length is 128000 tokens",
		"context_length_exceeded",
	} {
		if !contextExceeded(errors.New(s)) {
			t.Errorf("not recognized: %q", s)
		}
	}
	if contextExceeded(errors.New("connection refused")) {
		t.Error("connection refused is not a context error")
	}
}

// A server that rejects a request too large for its window gets a smaller
// one: the compaction trims more and retries instead of failing for good.
func TestCompactionRetriesWhenTheServerSaysTooLong(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []json.RawMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sizes = append(sizes, len(body.Messages))
		first := len(sizes) == 1
		mu.Unlock()
		if first {
			http.Error(w, `{"error":{"message":"prompt (9000 tokens) + max tokens (2000) exceeds the context (10000); requests are never truncated"}}`, http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"NOTES\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	a := New(config.ModelRef{ProviderName: "t", Provider: config.Provider{BaseURL: srv.URL},
		Model: config.Model{ID: "m", ContextWindow: 40000}}, "", os.TempDir())
	for i := 0; i < 6; i++ {
		a.messages = append(a.messages,
			provider.Message{Role: "user", Content: strings.Repeat("u", 8000)},
			provider.Message{Role: "assistant", Content: strings.Repeat("a", 8000)})
	}
	a.LastUsage = provider.Usage{PromptTokens: 24000}
	var trimmed []int
	err := a.compact(context.Background(), func(ev any) {
		if e, ok := ev.(CompactTrimmed); ok {
			trimmed = append(trimmed, e.Messages)
		}
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[1] >= sizes[0] || len(trimmed) == 0 {
		t.Fatalf("request sizes %v, trimmed %v", sizes, trimmed)
	}
}
