package provider

import (
	"context"
	"net/http"
)

// Streamer sends one request to a model server and streams the reply. It is
// implemented per wire API: Client (chat completions) and ResponsesClient.
type Streamer interface {
	Stream(ctx context.Context, req Request, h Handler) (Result, error)
}

// Provider API names, as set in models.json ("api").
const (
	APICompletions = "openai-completions"
	APIResponses   = "openai-responses"
)

// bearerKey returns the key to send: fn (refreshing OAuth tokens) wins over
// the static key.
func bearerKey(ctx context.Context, static string, fn func(context.Context) (string, error)) (string, error) {
	if fn == nil {
		return static, nil
	}
	return fn(ctx)
}

// setCommonHeaders applies the headers every request carries. "$session" in
// custom headers becomes the session ID (skipped when there is none).
func setCommonHeaders(hr *http.Request, key string, custom map[string]string, sessionID string) {
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("User-Agent", UserAgent)
	hr.Header.Set("Accept", "text/event-stream")
	if key != "" {
		hr.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range custom {
		if v == "$session" {
			if sessionID == "" {
				continue
			}
			v = sessionID
		}
		hr.Header.Set(k, v)
	}
}
