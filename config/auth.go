package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// AuthEntry is one provider's credentials in auth.json. The format matches
// pi's (~/.pi/agent/auth.json): {"<provider>": {"type": "api_key", "key": "…"}}.
type AuthEntry struct {
	Type string `json:"type"`
	Key  string `json:"key,omitempty"`
}

// LoadAuth reads auth.json; a missing file yields no entries.
func LoadAuth() (map[string]AuthEntry, error) {
	out := map[string]AuthEntry{}
	data, err := os.ReadFile(AuthPath())
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(data, &out)
}

// SetAPIKey stores an API key for provider in auth.json (mode 0600),
// preserving other entries.
func SetAPIKey(provider, key string) error {
	raw := map[string]json.RawMessage{}
	if data, err := os.ReadFile(AuthPath()); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entry, _ := json.Marshal(AuthEntry{Type: "api_key", Key: key})
	raw[provider] = entry
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(AuthPath(), append(out, '\n'), 0o600)
}
