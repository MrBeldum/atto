package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Built-in providers come from the models.dev catalog (as pi does). Only
// the providers listed here are kept; the subset is cached in
// ~/.atto/cache/catalog.json and refreshed daily.

const (
	catalogURL = "https://models.dev/api.json"
	catalogTTL = 24 * time.Hour
)

// catalogProvider describes how to turn a models.dev provider into ours.
type catalogProvider struct {
	name    string
	display string
	baseURL string
	env     []string
	headers map[string]string
}

// OpenCode Zen and Go: OpenAI-compatible chat completions, API key auth,
// and a per-conversation routing header (pi: opencode-headers.ts).
var catalogProviders = []catalogProvider{
	{
		name: "opencode", display: "OpenCode Zen", baseURL: "https://opencode.ai/zen/v1",
		env: []string{"OPENCODE_API_KEY"}, headers: map[string]string{"x-opencode-session": "$session"},
	},
	{
		name: "opencode-go", display: "OpenCode Go", baseURL: "https://opencode.ai/zen/go/v1",
		env: []string{"OPENCODE_API_KEY"}, headers: map[string]string{"x-opencode-session": "$session"},
	},
}

// modelsDevModel is the subset of a models.dev model entry we use.
type modelsDevModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolCall  bool   `json:"tool_call"`
	Reasoning bool   `json:"reasoning"`
	Status    string `json:"status"`
	Limit     struct {
		Context int `json:"context"`
		Input   int `json:"input"`
		Output  int `json:"output"`
	} `json:"limit"`
	Provider *struct {
		NPM string `json:"npm"`
	} `json:"provider"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

func catalogPath() string { return filepath.Join(Dir(), "cache", "catalog.json") }

// CatalogStale reports whether the cached catalog is missing or old.
func CatalogStale() bool {
	st, err := os.Stat(catalogPath())
	return err != nil || time.Since(st.ModTime()) > catalogTTL
}

// RefreshCatalog downloads models.dev and caches the providers we use.
func RefreshCatalog(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", catalogURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("models.dev: %s", resp.Status)
	}
	var all map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&all); err != nil {
		return err
	}
	subset := map[string]json.RawMessage{}
	for _, cp := range catalogProviders {
		if raw, ok := all[cp.name]; ok {
			subset[cp.name] = raw
		}
	}
	data, err := json.Marshal(subset)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(catalogPath()), 0o755); err != nil {
		return err
	}
	tmp := catalogPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, catalogPath())
}

// CatalogProviders builds providers from the cached catalog (none if the
// cache is missing).
func CatalogProviders() map[string]Provider {
	out := map[string]Provider{}
	data, err := os.ReadFile(catalogPath())
	if err != nil {
		return out
	}
	var cached map[string]modelsDevProvider
	if json.Unmarshal(data, &cached) != nil {
		return out
	}
	for _, cp := range catalogProviders {
		src, ok := cached[cp.name]
		if !ok {
			continue
		}
		p := Provider{
			Name: cp.display, BaseURL: cp.baseURL, API: "openai-completions",
			Env: cp.env, Headers: cp.headers, MaxTokensField: "max_tokens",
		}
		for id, m := range src.Models {
			if mod, ok := catalogModel(cp.name, id, m); ok {
				p.Models = append(p.Models, mod)
			}
		}
		sort.Slice(p.Models, func(i, j int) bool { return p.Models[i].ID < p.Models[j].ID })
		out[cp.name] = p
	}
	return out
}

// Default output cap: models.dev lists maximums (up to 512k) that would
// leave little room for context; most turns need far less.
const catalogMaxTokens = 32768

// catalogModel converts a models.dev entry. Only models served over chat
// completions are kept; GPT, Claude and Gemini models on OpenCode use the
// Responses, Anthropic and Google APIs, which atto does not speak yet.
func catalogModel(provider, id string, m modelsDevModel) (Model, bool) {
	if !m.ToolCall || m.Status == "deprecated" {
		return Model{}, false
	}
	if m.Provider != nil && m.Provider.NPM != "" && m.Provider.NPM != "@ai-sdk/openai-compatible" {
		return Model{}, false
	}
	ctx := m.Limit.Context
	if m.Limit.Input > 0 {
		ctx = m.Limit.Input
	}
	mod := Model{
		ID: id, Name: m.Name, ContextWindow: ctx,
		MaxTokens: min(max(m.Limit.Output, 0), catalogMaxTokens),
	}
	if mod.MaxTokens == 0 {
		mod.MaxTokens = catalogMaxTokens
	}
	if !m.Reasoning {
		return mod, true
	}
	// Default: OpenAI-style reasoning_effort. Reasoning models on OpenCode
	// think by default, so "off" must be sent explicitly as "none"
	// (omitting the field still thinks).
	mod.Efforts = []string{"off", "low", "medium", "high"}
	mod.EffortMap = map[string]*string{"off": str("none")}
	mod.ExtraBody = map[string]any{"reasoning_effort": "$effort"}
	if strings.HasPrefix(id, "kimi-k2.6") {
		// Kimi K2.6 takes Anthropic-style thinking objects and rejects
		// reasoning_effort; thinking is only on/off.
		mod.Efforts = []string{"off", "on"}
		mod.EffortMap = map[string]*string{}
		mod.ExtraBody = map[string]any{"reasoning_effort": nil, "thinking": map[string]any{"type": "$thinkingType"}}
	}
	for _, q := range catalogEffortMaps {
		match := strings.HasPrefix(id, q.prefix)
		if exact, ok := strings.CutSuffix(q.prefix, "!"); ok {
			match = id == exact
		}
		if (q.provider == "" || q.provider == provider) && match {
			for k, v := range q.levels {
				mod.EffortMap[k] = v
			}
		}
	}
	return mod, true
}

func str(s string) *string { return &s }

// catalogEffortMaps are built-in effort mappings for catalog models, in
// the same form users write in models.json (null = level unsupported);
// models.json entries override them per level. A trailing "!" on the
// prefix means an exact ID match.
var catalogEffortMaps = []struct {
	provider, prefix string
	levels           map[string]*string
}{
	// pi: DeepSeek V4 exposes high and max (Flash also low).
	{"", "deepseek-v4", map[string]*string{"medium": nil, "max": str("max")}},
	{"", "deepseek-v4-pro", map[string]*string{"low": nil}},
	// pi: OpenCode Go GLM-5.2 takes only high and max.
	{"opencode-go", "glm-5.2!", map[string]*string{"off": nil, "low": nil, "medium": nil, "max": str("max")}},
	// Verified on OpenCode Go 2026-10-05: these reject or ignore "none"
	// (thinking-only), so "off" is unavailable. GLM-5.3 Flash can turn it off.
	{"", "glm-5.3!", map[string]*string{"off": nil}},
	{"", "kimi-k2.7-code", map[string]*string{"off": nil}},
	{"", "longcat-", map[string]*string{"off": nil}},
}
