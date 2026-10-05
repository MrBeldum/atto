package config

import (
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
	if strings.HasPrefix(p.APIKey, "$") {
		if v := os.Getenv(p.APIKey[1:]); v != "" {
			return v
		}
	} else if p.APIKey != "" {
		return p.APIKey
	}
	if e, ok := auth[name]; ok && e.Key != "" {
		return e.Key
	}
	for _, env := range p.Env {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return ""
}

type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Efforts []string `json:"efforts,omitempty"` // reasoning effort levels, in order
	// EffortMap translates an effort level to the value sent for "$effort"
	// (e.g. {"max": "xhigh"}). Unmapped levels are sent as-is.
	EffortMap     map[string]string `json:"effortMap,omitempty"`
	ContextWindow int               `json:"contextWindow,omitempty"`
	MaxTokens     int               `json:"maxTokens,omitempty"`
	// ExtraBody is merged over the provider's; a null value removes a key.
	ExtraBody map[string]any `json:"extraBody,omitempty"`
}

func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
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
				models[i], replaced = m, true
			}
		}
		if !replaced {
			models = append(models, m)
		}
	}
	base.Models = models
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
		for _, mod := range p.Models {
			out = append(out, ModelRef{ProviderName: n, Provider: p, Model: mod, APIKey: p.ResolveAPIKey(n, m.auth)})
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
