//go:build !windows

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
)

func cmd(event, matcher, command string) map[string][]config.HookMatcher {
	return map[string][]config.HookMatcher{event: {{Matcher: matcher, Hooks: []config.HookSpec{{Type: "command", Command: command}}}}}
}

func TestPreToolUseExit2Blocks(t *testing.T) {
	r := New(cmd("PreToolUse", "Bash", `grep -q 'rm -rf' && { echo "no rm -rf" >&2; exit 2; } || exit 0`), t.TempDir())
	_, o := r.PreToolUse(context.Background(), agent.BashArgs{Command: "rm -rf /tmp/x"})
	if !o.Block || o.Reason != "no rm -rf" {
		t.Fatalf("expected block: %+v", o)
	}
	_, o = r.PreToolUse(context.Background(), agent.BashArgs{Command: "ls"})
	if o.Block {
		t.Fatalf("ls should pass: %+v", o)
	}
}

func TestPreToolUseJSONDecisionAndUpdatedInput(t *testing.T) {
	r := New(cmd("PreToolUse", "", `echo '{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"ls -la"}}}'`), t.TempDir())
	args, o := r.PreToolUse(context.Background(), agent.BashArgs{Command: "ls"})
	if o.Block || args.Command != "ls -la" {
		t.Fatalf("got %+v %+v", args, o)
	}
	r = New(cmd("PreToolUse", "bash|edit", `echo '{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"needs review"}}'`), t.TempDir())
	_, o = r.PreToolUse(context.Background(), agent.BashArgs{Command: "ls"})
	if !o.Block || !strings.Contains(o.Reason, "needs review") {
		t.Fatalf("ask should deny: %+v", o)
	}
	// A matcher for another tool does not run.
	r = New(cmd("PreToolUse", "Write", `exit 2`), t.TempDir())
	if _, o = r.PreToolUse(context.Background(), agent.BashArgs{Command: "ls"}); o.Block {
		t.Fatal("non-matching hook ran")
	}
}

func TestHookInputAndErrors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "in.json")
	r := New(cmd("UserPromptSubmit", "", "cat > "+out+"; echo 'remember: be brief'"), dir)
	r.SetSession("s1", "/t.jsonl")
	o := r.UserPromptSubmit(context.Background(), "hello")
	if o.Context != "remember: be brief" {
		t.Fatalf("plain stdout should become context: %+v", o)
	}
	var in map[string]any
	b, _ := os.ReadFile(out)
	_ = json.Unmarshal(b, &in)
	if in["prompt"] != "hello" || in["session_id"] != "s1" || in["hook_event_name"] != "UserPromptSubmit" || in["cwd"] != dir {
		t.Fatalf("input %v", in)
	}
	r = New(cmd("Stop", "", "echo oops >&2; exit 1"), dir)
	if o := r.Stop(context.Background(), false); o.Block || len(o.Notices) != 1 {
		t.Fatalf("exit 1 should be a non-blocking notice: %+v", o)
	}
}

func TestHTTPHook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["tool_name"] == "Bash" {
			fmt.Fprint(w, `{"decision":"block","reason":"http says no"}`)
		}
	}))
	defer srv.Close()
	r := New(map[string][]config.HookMatcher{"PostToolUse": {{Hooks: []config.HookSpec{{Type: "http", URL: srv.URL}}}}}, t.TempDir())
	if o := r.PostToolUse(context.Background(), agent.BashArgs{}, agent.BashResult{}, ""); !o.Block || o.Reason != "http says no" {
		t.Fatalf("%+v", o)
	}
}

// End to end: a PreToolUse hook blocks the command and the model sees why;
// a Stop hook makes the turn continue once.
func TestAgentWithHooks(t *testing.T) {
	var mu sync.Mutex
	var reqs [][]map[string]any
	replies := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"description\":\"x\",\"command\":\"rm -rf /\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`{"choices":[{"delta":{"content":"tests done"},"finish_reason":"stop"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		i := len(reqs)
		reqs = append(reqs, body.Messages)
		mu.Unlock()
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", replies[i])
	}))
	defer srv.Close()

	cfg := cmd("PreToolUse", "Bash", `echo "destructive" >&2; exit 2`)
	cfg["Stop"] = []config.HookMatcher{{Hooks: []config.HookSpec{{Type: "command",
		Command: `grep -q '"stop_hook_active":false' && echo '{"decision":"block","reason":"run the tests first"}' || true`}}}}
	a := agent.New(config.ModelRef{Provider: config.Provider{BaseURL: srv.URL}, Model: config.Model{ID: "m"}}, "", t.TempDir())
	a.Hooks = New(cfg, t.TempDir())
	var notices []string
	if err := a.Run(context.Background(), "go", func(ev any) {
		if n, ok := ev.(agent.HookNotice); ok {
			notices = append(notices, n.Event+": "+n.Message)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 3 {
		t.Fatalf("expected 3 requests (tool, stop, continued), got %d", len(reqs))
	}
	tool := reqs[1][len(reqs[1])-1]
	if !strings.Contains(tool["content"].(string), "Blocked by a PreToolUse hook: destructive") {
		t.Fatalf("model should see the block reason: %v", tool)
	}
	last := reqs[2][len(reqs[2])-1]
	if last["content"] != "[Stop hook] run the tests first" {
		t.Fatalf("stop hook reason missing: %v", last)
	}
	if len(notices) != 2 {
		t.Fatalf("notices %v", notices)
	}
}
