package config

import (
	"sort"

	"github.com/sebastianrcnt/atto/auth"
)

// LoginProvider is one entry of the login pickers (pi:
// AuthSelectorProvider).
type LoginProvider struct {
	ID    string
	Name  string
	OAuth bool
	// Status describes stored or ambient credentials ("" when none).
	Status string
}

// LoginProviders lists what /login offers: the OAuth logins, then the
// providers that take an API key (built-in ones and models.json's).
func LoginProviders(models ModelsFile) []LoginProvider {
	stored, _ := LoadAuth()
	status := func(id string) string {
		if e, ok := stored[id]; ok {
			if e.Type == "oauth" {
				return "logged in"
			}
			return "key saved"
		}
		if p, ok := models.Providers[id]; ok && models.hasKey(id, p) {
			return "configured"
		}
		return ""
	}
	var out []LoginProvider
	for _, p := range auth.OAuthProviders() {
		out = append(out, LoginProvider{ID: p.ID, Name: p.Name, OAuth: true, Status: status(p.ID)})
	}
	seen := map[string]bool{}
	for _, cp := range catalogProviders {
		if cp.api == "openai-codex-responses" {
			continue // subscription login only
		}
		seen[cp.name] = true
		out = append(out, LoginProvider{ID: cp.name, Name: cp.display + " API key", Status: status(cp.name)})
	}
	var user []string
	for id := range models.Providers {
		if !seen[id] && auth.GetOAuthProvider(id) == nil {
			user = append(user, id)
		}
	}
	sort.Strings(user)
	for _, id := range user {
		name := models.Providers[id].Name
		if name == "" {
			name = id
		}
		out = append(out, LoginProvider{ID: id, Name: name + " API key", Status: status(id)})
	}
	return out
}

// StoredCredentials lists the providers with an auth.json entry (for
// /logout), sorted.
func StoredCredentials() []string {
	stored, _ := LoadAuth()
	ids := make([]string, 0, len(stored))
	for id := range stored {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
