package config

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"atto/auth"
)

// AuthEntry is one provider's credentials in auth.json. The format matches
// pi's (~/.pi/agent/auth.json):
//
//	{"<provider>": {"type": "api_key", "key": "…"}}
//	{"<provider>": {"type": "oauth", "access": "…", "refresh": "…", "expires": <unix ms>, "clientId": "…"}}
type AuthEntry struct {
	Type     string   `json:"type"`
	Key      string   `json:"key,omitempty"`
	Access   string   `json:"access,omitempty"`
	Refresh  string   `json:"refresh,omitempty"`
	Expires  int64    `json:"expires,omitempty"` // unix ms
	ClientID string   `json:"clientId,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// Secret is the bearer token the entry provides: the API key, or the
// current OAuth access token (which may need a refresh first).
func (e AuthEntry) Secret() string {
	if e.Type == "oauth" {
		return e.Access
	}
	return e.Key
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

// updateAuth rewrites auth.json (mode 0600) after fn edits its raw entries,
// so entries this version does not understand survive.
func updateAuth(fn func(raw map[string]json.RawMessage) error) error {
	raw := map[string]json.RawMessage{}
	if data, err := os.ReadFile(AuthPath()); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := fn(raw); err != nil {
		return err
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	// Write-then-rename so a crash never leaves a truncated credentials file.
	tmp, err := os.CreateTemp(filepath.Dir(AuthPath()), ".auth-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), AuthPath())
}

func setEntry(provider string, e AuthEntry) error {
	return updateAuth(func(raw map[string]json.RawMessage) error {
		b, err := json.Marshal(e)
		raw[provider] = b
		return err
	})
}

// SetAPIKey stores an API key for provider in auth.json (mode 0600),
// preserving other entries.
func SetAPIKey(provider, key string) error {
	return setEntry(provider, AuthEntry{Type: "api_key", Key: key})
}

// SetOAuth stores a login credential for provider, preserving other entries.
func SetOAuth(provider string, c auth.Credential) error {
	return setEntry(provider, AuthEntry{
		Type: "oauth", Access: c.Access, Refresh: c.Refresh,
		Expires: c.Expires, ClientID: c.ClientID, Scopes: c.Scopes,
	})
}

// RemoveAuth deletes provider's entry; it reports whether one existed.
func RemoveAuth(provider string) (bool, error) {
	found := false
	err := updateAuth(func(raw map[string]json.RawMessage) error {
		_, found = raw[provider]
		delete(raw, provider)
		return nil
	})
	return found, err
}

// DeviceID returns this installation's stable UUID, creating it on first use.
func DeviceID() (string, error) {
	path := filepath.Join(Dir(), "device-id")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 36 {
		return string(b[:36]), nil
	}
	id := auth.NewDeviceID()
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return "", err
	}
	return id, os.WriteFile(path, []byte(id+"\n"), 0o600)
}

// oauthMu serializes refreshes within the process, so concurrent requests
// (agents, subagents) do not spend the same refresh token twice.
var oauthMu sync.Mutex

// chatGPT is the OAuth configuration used for refreshes; tests replace it.
var chatGPT = func() *auth.ChatGPT { return auth.New("") }

// OAuthToken returns a valid access token for provider, refreshing and
// persisting the credential when it is about to expire.
func OAuthToken(ctx context.Context, provider string) (string, error) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	entries, err := LoadAuth()
	if err != nil {
		return "", err
	}
	e, ok := entries[provider]
	if !ok || e.Type != "oauth" {
		return "", errors.New("not logged in; run: atto login " + provider)
	}
	cred := auth.Credential{Access: e.Access, Refresh: e.Refresh, Expires: e.Expires, ClientID: e.ClientID, Scopes: e.Scopes}
	if !cred.Expired(time.Now()) {
		return cred.Access, nil
	}
	fresh, err := chatGPT().Refresh(ctx, cred)
	if err != nil {
		return "", err
	}
	if err := SetOAuth(provider, fresh); err != nil {
		return "", err
	}
	return fresh.Access, nil
}
