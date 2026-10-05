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
}

type Provider struct {
	BaseURL        string         `json:"baseUrl"`
	API            string         `json:"api,omitempty"`
	APIKey         string         `json:"apiKey,omitempty"`
	MaxTokensField string         `json:"maxTokensField,omitempty"`
	ExtraBody      map[string]any `json:"extraBody,omitempty"`
	Models         []Model        `json:"models"`
}

// ResolvedAPIKey expands a "$NAME" key from the environment.
func (p Provider) ResolvedAPIKey() string {
	if strings.HasPrefix(p.APIKey, "$") {
		return os.Getenv(p.APIKey[1:])
	}
	return p.APIKey
}

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Efforts       []string `json:"efforts,omitempty"` // reasoning effort levels, in order
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
}

func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// ModelRef is a model together with the provider that serves it.
type ModelRef struct {
	ProviderName string
	Provider     Provider
	Model        Model
}

// LoadModels reads models.json; a missing file yields no providers.
func LoadModels() (ModelsFile, error) {
	var m ModelsFile
	data, err := os.ReadFile(ModelsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
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
			out = append(out, ModelRef{ProviderName: n, Provider: p, Model: mod})
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
