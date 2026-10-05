package config

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// ModelsFile is the contents of models.json.
//
//	{
//	  "providers": {
//	    "llama-cpp": {
//	      "baseUrl": "http://host:8081/v1",
//	      "api": "openai-completions",
//	      "apiKey": "$ENV_VAR or literal (optional)",
//	      "extraBody": {"chat_template_kwargs": {"reasoning_effort": "$effort", "enable_thinking": "$thinking"}},
//	      "models": [{"id": "orca-local", "efforts": ["off","low","medium","high"], "contextWindow": 262144, "maxTokens": 32768}]
//	    }
//	  }
//	}
type ModelsFile struct {
	Providers map[string]Provider `json:"providers"`
	auth      map[string]AuthEntry
}

type Provider struct {
	Name           string   `json:"name,omitempty"`
	BaseURL        string   `json:"baseUrl"`
	API            string   `json:"api,omitempty"`
	APIKey         string   `json:"apiKey,omitempty"` // literal or "$ENV_VAR"
	Env            []string `json:"env,omitempty"`    // env vars to read the key from
	MaxTokensField string   `json:"maxTokensField,omitempty"`
	// Headers are sent with every request. "$session" is replaced with the
	// session ID (e.g. OpenCode's x-opencode-session routing header).
	Headers   map[string]string `json:"headers,omitempty"`
	ExtraBody map[string]any    `json:"extraBody,omitempty"`
	Models    []Model           `json:"models"`
}

// ResolveAPIKey finds the key for provider name: an explicit apiKey (literal
// or "$ENV_VAR"), then auth.json, then the provider's env vars.
func (p Provider) ResolveAPIKey(name string, auth map[string]AuthEntry) string {
	key, _ := p.resolveKey(name, auth)
	return key
}

// resolveKey is ResolveAPIKey that also reports whether the key is an OAuth
// access token, which expires and must be fetched through OAuthToken.
func (p Provider) resolveKey(name string, auth map[string]AuthEntry) (key string, oauth bool) {
	if strings.HasPrefix(p.APIKey, "$") {
		if v := os.Getenv(p.APIKey[1:]); v != "" {
			return v, false
		}
	} else if p.APIKey != "" {
		return p.APIKey, false
	}
	if e, ok := auth[name]; ok && e.Secret() != "" {
		return e.Secret(), e.Type == "oauth"
	}
	for _, env := range p.Env {
		if v := os.Getenv(env); v != "" {
			return v, false
		}
	}
	return "", false
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// API overrides the provider's wire API for this model (e.g. GPT models
	// on a gateway that serves the rest over chat completions).
	API     string   `json:"api,omitempty"`
	Efforts []string `json:"efforts,omitempty"` // reasoning effort levels, in order
	// EffortMap works like pi's thinkingLevelMap: each level maps to the
	// value sent for "$effort", and null marks a level as unsupported
	// (it is removed from Efforts). A mapped level not in Efforts is added.
	// Unmapped levels are sent as-is.
	EffortMap     map[string]*string `json:"effortMap,omitempty"`
	ContextWindow int                `json:"contextWindow,omitempty"`
	MaxTokens     int                `json:"maxTokens,omitempty"`
	// ExtraBody is merged over the provider's; a null value removes a key.
	ExtraBody map[string]any `json:"extraBody,omitempty"`
}

func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// effortOrder is the canonical order for levels added through EffortMap.
var effortOrder = []string{"off", "none", "minimal", "low", "medium", "high", "xhigh", "max", "on"}

// Levels returns the available effort levels: Efforts minus levels mapped
// to null, plus mapped levels not already listed.
func (m Model) Levels() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range m.Efforts {
		if v, ok := m.EffortMap[l]; ok && v == nil {
			continue
		}
		out = append(out, l)
		seen[l] = true
	}
	for _, l := range effortOrder {
		if v, ok := m.EffortMap[l]; ok && v != nil && !seen[l] {
			out = insertOrdered(out, l)
			seen[l] = true
		}
	}
	return out
}

func insertOrdered(levels []string, l string) []string {
	rank := func(x string) int {
		for i, o := range effortOrder {
			if o == x {
				return i
			}
		}
		return len(effortOrder)
	}
	for i, x := range levels {
		if rank(x) > rank(l) {
			return append(levels[:i], append([]string{l}, levels[i:]...)...)
		}
	}
	return append(levels, l)
}

// WireEfforts returns the level -> value translations to send.
func (m Model) WireEfforts() map[string]string {
	out := map[string]string{}
	for k, v := range m.EffortMap {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// RequestBody merges the provider's and the model's extra body fields.
func (r ModelRef) RequestBody() map[string]any {
	out := map[string]any{}
	for k, v := range r.Provider.ExtraBody {
		out[k] = v
	}
	for k, v := range r.Model.ExtraBody {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

// ModelRef is a model together with the provider that serves it.
type ModelRef struct {
	ProviderName string
	Provider     Provider
	Model        Model
	APIKey       string
	// KeyFunc, set for OAuth logins, returns a fresh access token per
	// request; APIKey then only marks that credentials exist.
	KeyFunc func(context.Context) (string, error)
}

// API returns the wire API for the model: its own, else the provider's,
// else chat completions.
func (r ModelRef) API() string {
	if r.Model.API != "" {
		return r.Model.API
	}
	if r.Provider.API != "" {
		return r.Provider.API
	}
	return "openai-completions"
}

// LoadModels returns the configured providers: built-in catalog providers
// (e.g. OpenCode) whose API key is available, overlaid with models.json.
// A models.json provider with the same name overrides the preset's fields,
// and its models override preset models with the same ID.
func LoadModels() (ModelsFile, error) {
	var user ModelsFile
	data, err := os.ReadFile(ModelsPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return user, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &user); err != nil {
			return user, err
		}
	}
	auth, err := LoadAuth()
	if err != nil {
		return user, err
	}
	out := ModelsFile{Providers: map[string]Provider{}}
	for name, p := range CatalogProviders() {
		if _, configured := user.Providers[name]; configured || p.ResolveAPIKey(name, auth) != "" {
			out.Providers[name] = p
		}
	}
	for name, up := range user.Providers {
		out.Providers[name] = mergeProvider(out.Providers[name], up)
	}
	out.auth = auth
	return out, nil
}

func mergeProvider(base, over Provider) Provider {
	if base.BaseURL == "" {
		return over
	}
	if over.Name != "" {
		base.Name = over.Name
	}
	if over.BaseURL != "" {
		base.BaseURL = over.BaseURL
	}
	if over.API != "" {
		base.API = over.API
	}
	if over.APIKey != "" {
		base.APIKey = over.APIKey
	}
	if len(over.Env) > 0 {
		base.Env = over.Env
	}
	if over.MaxTokensField != "" {
		base.MaxTokensField = over.MaxTokensField
	}
	for k, v := range over.Headers {
		if base.Headers == nil {
			base.Headers = map[string]string{}
		}
		base.Headers[k] = v
	}
	for k, v := range over.ExtraBody {
		if base.ExtraBody == nil {
			base.ExtraBody = map[string]any{}
		}
		base.ExtraBody[k] = v
	}
	models := append([]Model(nil), base.Models...)
	for _, m := range over.Models {
		replaced := false
		for i := range models {
			if models[i].ID == m.ID {
				models[i], replaced = mergeModel(models[i], m), true
			}
		}
		if !replaced {
			models = append(models, m)
		}
	}
	base.Models = models
	return base
}

// mergeModel overlays the fields set in over onto base. Maps merge per key,
// so models.json can adjust a single effort mapping of a catalog model.
func mergeModel(base, over Model) Model {
	if over.Name != "" {
		base.Name = over.Name
	}
	if over.API != "" {
		base.API = over.API
	}
	if len(over.Efforts) > 0 {
		base.Efforts = over.Efforts
	}
	if over.ContextWindow > 0 {
		base.ContextWindow = over.ContextWindow
	}
	if over.MaxTokens > 0 {
		base.MaxTokens = over.MaxTokens
	}
	if len(over.EffortMap) > 0 {
		merged := map[string]*string{}
		for k, v := range base.EffortMap {
			merged[k] = v
		}
		for k, v := range over.EffortMap {
			merged[k] = v
		}
		base.EffortMap = merged
	}
	if len(over.ExtraBody) > 0 {
		merged := map[string]any{}
		for k, v := range base.ExtraBody {
			merged[k] = v
		}
		for k, v := range over.ExtraBody {
			merged[k] = v
		}
		base.ExtraBody = merged
	}
	return base
}

// List returns every configured model, ordered by provider name.
func (m ModelsFile) List() []ModelRef {
	names := make([]string, 0, len(m.Providers))
	for n := range m.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []ModelRef
	for _, n := range names {
		p := m.Providers[n]
		key, oauth := p.resolveKey(n, m.auth)
		for _, mod := range p.Models {
			ref := ModelRef{ProviderName: n, Provider: p, Model: mod, APIKey: key}
			if oauth {
				ref.KeyFunc = func(ctx context.Context) (string, error) { return OAuthToken(ctx, n) }
			}
			out = append(out, ref)
		}
	}
	return out
}

// Find looks a model up by id, optionally qualified as "provider/id".
// An empty provider matches any.
func (m ModelsFile) Find(provider, id string) (ModelRef, bool) {
	if provider == "" {
		if p, rest, ok := strings.Cut(id, "/"); ok {
			if _, exists := m.Providers[p]; exists {
				provider, id = p, rest
			}
		}
	}
	for _, r := range m.List() {
		if r.Model.ID == id && (provider == "" || r.ProviderName == provider) {
			return r, true
		}
	}
	return ModelRef{}, false
}
