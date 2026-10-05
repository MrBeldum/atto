// Package auth holds credentials and the OAuth logins. It ports pi's
// src/auth (types.ts, oauth/pkce.ts, oauth/callback-server.ts,
// oauth/device-code.ts, oauth/openai-chatgpt.ts, oauth/openai-codex.ts).
//
// Portions Copyright (c) 2025 Mario Zechner, MIT License; see
// THIRD_PARTY_NOTICES.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Credential is one provider's entry in auth.json, in pi's format
// (auth/types.ts Credential):
//
//	{"type": "api_key", "key": "…", "env": {"NAME": "…"}}
//	{"type": "oauth", "access": "…", "refresh": "…", "expires": <unix ms>, …}
//
// OAuth credentials carry provider fields beside the tokens (clientId and
// scopes for openai, accountId for openai-codex). Fields this version
// does not know are kept in Extra and written back unchanged.
type Credential struct {
	Type    string            `json:"type"`
	Key     string            `json:"key,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Access  string            `json:"access,omitempty"`
	Refresh string            `json:"refresh,omitempty"`
	// Expires is unix milliseconds, refresh margin already subtracted.
	Expires   int64    `json:"expires,omitempty"`
	ClientID  string   `json:"clientId,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	AccountID string   `json:"accountId,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

var knownCredentialFields = map[string]bool{
	"type": true, "key": true, "env": true, "access": true, "refresh": true,
	"expires": true, "clientId": true, "scopes": true, "accountId": true,
}

func (c *Credential) UnmarshalJSON(b []byte) error {
	type plain Credential
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	// pi writes Date.now() arithmetic, which JSON may carry as a float.
	var exp float64
	if e, ok := raw["expires"]; ok {
		if err := json.Unmarshal(e, &exp); err != nil {
			return fmt.Errorf("expires: %w", err)
		}
		delete(raw, "expires")
	}
	rest, _ := json.Marshal(raw)
	var p plain
	if err := json.Unmarshal(rest, &p); err != nil {
		return err
	}
	p.Expires = int64(math.Floor(exp))
	for k, v := range raw {
		if !knownCredentialFields[k] {
			if p.Extra == nil {
				p.Extra = map[string]json.RawMessage{}
			}
			p.Extra[k] = v
		}
	}
	*c = Credential(p)
	return nil
}

func (c Credential) MarshalJSON() ([]byte, error) {
	type plain Credential
	b, err := json.Marshal(plain(c))
	if err != nil || len(c.Extra) == 0 {
		return b, err
	}
	// Known fields first, in declaration order; then extras, sorted.
	var extra map[string]json.RawMessage
	for k, v := range c.Extra {
		if !knownCredentialFields[k] {
			if extra == nil {
				extra = map[string]json.RawMessage{}
			}
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		return b, nil
	}
	e, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	return append(append(b[:len(b)-1], ','), e[1:]...), nil
}

// Secret is the bearer token the entry provides: the API key, or the
// current OAuth access token (which may need a refresh first).
func (c Credential) Secret() string {
	if c.Type == "oauth" {
		return c.Access
	}
	return c.Key
}

// Expired reports whether the access token should be refreshed.
func (c Credential) Expired(now time.Time) bool { return now.UnixMilli() >= c.Expires }

// --- oauth/pkce.ts ---

// PKCE returns a random code verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	verifier = randomValue()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS entropy source failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b)
}

// NewDeviceID returns a random UUID (v4) for ext_agent_host_id.
func NewDeviceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
