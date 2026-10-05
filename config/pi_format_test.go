package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/ai"
)

// Fixtures shaped after pi's formats (coding-agent auth-storage.ts and
// model-config.ts); none are real credentials.

const piAuthJSON = `{
  "openai": {
    "type": "oauth",
    "access": "acc-token",
    "refresh": "ref-token",
    "expires": 4102444800000.5,
    "clientId": "client-issued",
    "scopes": ["openid", "chatgpt.tokens.use.direct"]
  },
  "openai-codex": {
    "type": "oauth",
    "access": "codex-acc",
    "refresh": "codex-ref",
    "expires": 4102444800000,
    "accountId": "acct-123",
    "futureField": {"nested": true}
  },
  "opencode": {"type": "api_key", "key": "literal-key"},
  "deepseek": {"type": "api_key", "key": "$PI_FIXTURE_KEY"},
  "cloudflare-workers-ai": {"type": "api_key", "key": "${CF_FIXTURE}", "env": {"CF_FIXTURE": "from-env-map"}},
  "groq": {"type": "api_key", "key": "!echo cmd-key"}
}`

func TestPiAuthJSON(t *testing.T) {
	setupDir(t)
	t.Setenv("PI_FIXTURE_KEY", "env-key")
	ClearConfigValueCache()
	if err := os.WriteFile(AuthPath(), []byte(piAuthJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAuth()
	if err != nil {
		t.Fatal(err)
	}
	o := got["openai"]
	if o.Type != "oauth" || o.Access != "acc-token" || o.Expires != 4102444800000 || o.ClientID != "client-issued" || len(o.Scopes) != 2 {
		t.Fatalf("openai %+v", o)
	}
	c := got["openai-codex"]
	if c.AccountID != "acct-123" || string(c.Extra["futureField"]) != `{"nested": true}` {
		t.Fatalf("codex %+v", c)
	}
	for id, want := range map[string]string{"opencode": "literal-key", "deepseek": "env-key", "cloudflare-workers-ai": "from-env-map", "groq": "cmd-key"} {
		if k := ResolvedKey(got[id]); k != want {
			t.Errorf("%s key %q, want %q", id, k, want)
		}
	}

	// Rewriting one entry keeps the others byte-for-byte meaningful,
	// including fields atto does not know.
	if err := SetAPIKey("new", "k"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(AuthPath())
	var raw map[string]map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["openai-codex"]["futureField"] == nil || raw["openai"]["clientId"] != "client-issued" || len(raw) != 7 {
		t.Fatalf("rewrite lost data: %s", data)
	}
	// A credential round-trips through atto's types with extras intact.
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"accountId":"acct-123"`) || !strings.Contains(string(b), `"futureField":{"nested":true}`) {
		t.Fatalf("marshal %s", b)
	}
}

const piModelsJSON = `{
  // pi allows comments in models.json
  "providers": {
    "vllm": {
      "baseUrl": "http://gpu:8000/v1",
      "api": "openai-completions",
      "apiKey": "!echo vllm-key",
      "compat": {"supportsDeveloperRole": false, "thinkingFormat": "chat-template",
                 "chatTemplateKwargs": {"enable_thinking": {"$var": "thinking.enabled"}}},
      "headers": {"X-Team": "$PI_FIXTURE_TEAM"},
      "models": [
        {"id": "qwen3", "name": "Qwen 3", "reasoning": true,
         "thinkingLevelMap": {"minimal": null, "xhigh": "high"},
         "input": ["text", "image"], "cost": {"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 0},
         "samplingParams": {"top_k": 20}}
      ]
    },
    "plain": {
      "baseUrl": "https://api.example.com/v1",
      "api": "openai-completions",
      "models": [{"id": "pi-style", "reasoning": true}]
    },
    "openai": {
      "modelOverrides": {"gpt-5.2": {"name": "GPT 5.2 (override)", "thinkingLevelMap": {"xhigh": null}}}
    }
  }
}`

func TestPiModelsJSON(t *testing.T) {
	setupDir(t)
	writeCatalog(t)
	t.Setenv("PI_FIXTURE_TEAM", "core")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	ClearConfigValueCache()
	if err := os.WriteFile(ModelsPath(), []byte(piModelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	q, ok := m.Find("vllm", "qwen3")
	if !ok || q.APIKey != "vllm-key" || q.Model.DisplayName() != "Qwen 3" || !q.Model.Images() {
		t.Fatalf("qwen3 %+v", q)
	}
	if got := strings.Join(q.Model.Levels(), ","); got != "off,low,medium,high,xhigh" {
		t.Fatalf("levels %s", got)
	}
	if h := q.RequestHeaders(); h["X-Team"] != "core" {
		t.Fatalf("headers %v", h)
	}
	am := q.AIModel()
	if !am.Reasoning || am.Compat.ThinkingFormat != "chat-template" || am.Cost.Output != 2 || am.SamplingParams["top_k"] != float64(20) {
		t.Fatalf("ai model %+v", am)
	}
	// pi defaults for unset limits of a pi-format model.
	if q.Model.ContextWindow != 128000 || q.Model.MaxTokens != 16384 {
		t.Fatalf("defaults %d %d", q.Model.ContextWindow, q.Model.MaxTokens)
	}

	// A pi-format model gets pi's detected compat: developer role, store,
	// max_completion_tokens, reasoning_effort.
	plain, _ := m.Find("plain", "pi-style")
	pm := plain.AIModel()
	c := ai.GetCompletionsCompat(&pm)
	if !c.SupportsDeveloperRole || !c.SupportsStore || c.MaxTokensField != "max_completion_tokens" || !c.SupportsReasoningEffort {
		t.Fatalf("pi compat %+v", c)
	}

	gpt, ok := m.Find("openai", "gpt-5.2")
	if !ok || gpt.Model.Name != "GPT 5.2 (override)" || strings.Contains(strings.Join(gpt.Model.Levels(), ","), "xhigh") {
		t.Fatalf("override %+v levels %v", gpt.Model, gpt.Model.Levels())
	}
}

func TestAttoModelsJSONKeepsWireFormat(t *testing.T) {
	setupDir(t)
	os.WriteFile(ModelsPath(), []byte(`{"providers":{"llama":{"baseUrl":"http://h:8081/v1","api":"openai-completions",
	  "extraBody":{"chat_template_kwargs":{"reasoning_effort":"$effort"}},
	  "models":[{"id":"orca","efforts":["off","high"],"effortMap":{"off":"none"},"contextWindow":1000}]}}}`), 0o644)
	m, err := LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Find("llama", "orca")
	if *r.Model.EffortMap["off"] != "none" || r.Model.MaxTokens != 0 {
		t.Fatalf("effortMap migration / no pi defaults: %+v", r.Model)
	}
	am := r.AIModel()
	c := ai.GetCompletionsCompat(&am)
	if c.SupportsDeveloperRole || c.SupportsStore || c.SupportsReasoningEffort || c.MaxTokensField != "max_tokens" {
		t.Fatalf("atto-format compat %+v", c)
	}
	if strings.Join(am.Efforts, ",") != "off,high" || am.ExtraBody["chat_template_kwargs"] == nil {
		t.Fatalf("ai model %+v", am)
	}
}

func TestCodexProviderAfterLogin(t *testing.T) {
	setupDir(t)
	if m, _ := LoadModels(); m.HasKey("openai-codex") {
		t.Fatal("codex shown without login")
	}
	if err := os.WriteFile(AuthPath(), []byte(piAuthJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadModels()
	r, ok := m.Find("openai-codex", "gpt-5.2")
	if !ok || r.API() != "openai-codex-responses" || r.KeyFunc == nil || r.Provider.BaseURL != "https://chatgpt.com/backend-api" {
		t.Fatalf("codex ref %+v", r)
	}
	if _, ok := m.Find("openai-codex", "gpt-4.1"); ok {
		t.Fatal("non-GPT-5 model on codex")
	}
}

func writeCatalog(t *testing.T) {
	t.Helper()
	os.MkdirAll(Dir()+"/cache", 0o755)
	os.WriteFile(catalogPath(), []byte(openaiFixture), 0o644)
}
