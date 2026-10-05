package auth

// Ported from pi (https://github.com/earendil-works/pi), Copyright (c) 2025
// Mario Zechner, MIT License; see THIRD_PARTY_NOTICES.

// Port of pi's src/auth/oauth/openai-chatgpt.ts: "Sign in with ChatGPT",
// an OAuth 2.0 public-client flow (dynamic client registration, PKCE,
// loopback redirect) whose access token is sent directly to the OpenAI
// API. It does not use the Codex CLI's client (see openai_codex.go).
//
// atto differences: when port 1455 is taken, atto falls back to the
// pasted redirect URL (pi fails), and the redirect URI follows the port
// actually bound (tests bind port 0).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// dynamicClientID asks the server to register a fresh client for this
	// login; the issued client ID comes back on the redirect.
	dynamicClientID = "dynamic_agent_client"
	agentNameHint   = "atto"
	authorizeURL    = "https://auth.openai.com/api/accounts/authorize"
	tokenURL        = "https://auth.openai.com/api/accounts/oauth/token"
	resource        = "https://api.openai.com/v1"
	callbackAddr    = "127.0.0.1:1455"
	callbackPath    = "/auth/callback"
	directScope     = "chatgpt.tokens.use.direct"
	scope           = "openid profile email offline_access resource.invoke " + directScope
	// ExpiryMargin is subtracted from the real expiry so a request never
	// starts with a token about to expire.
	ExpiryMargin = 3 * time.Minute
)

// ChatGPT holds the endpoints; tests point them at fake servers.
type ChatGPT struct {
	AuthorizeURL string
	TokenURL     string
	// ListenAddr is the loopback address for the redirect (default
	// 127.0.0.1:1455, the port the registration expects).
	ListenAddr string
	// DeviceID is a stable per-installation UUID.
	DeviceID string
	HTTP     *http.Client
	Now      func() time.Time
}

// New returns the production configuration.
func New(deviceID string) *ChatGPT {
	return &ChatGPT{AuthorizeURL: authorizeURL, TokenURL: tokenURL, ListenAddr: callbackAddr, DeviceID: deviceID}
}

func (c *ChatGPT) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *ChatGPT) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// UI is how a login talks to the user.
type UI struct {
	// ShowURL receives the authorization URL to open in a browser.
	ShowURL func(url string)
	// Notice receives progress and recoverable problems.
	Notice func(msg string)
	// ReadPasted blocks for a pasted redirect URL; it may be nil. It is
	// called again after an unusable paste and its goroutine is abandoned
	// once the login ends.
	ReadPasted func() (string, error)
}

func (u UI) notice(format string, args ...any) {
	if u.Notice != nil {
		u.Notice(fmt.Sprintf(format, args...))
	}
}

// AuthURL builds the authorization URL.
func (c *ChatGPT) AuthURL(redirectURI, state, challenge, nonce string) string {
	q := url.Values{
		"client_id":             {dynamicClientID},
		"agent_name_hint":       {agentNameHint},
		"ext_agent_host_id":     {"urn:uuid:" + strings.ToLower(c.DeviceID)},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"resource":              {resource},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"nonce":                 {nonce},
	}
	return c.AuthorizeURL + "?" + q.Encode()
}

type authResult struct{ code, clientID string }

// resultFromQuery validates a redirect's query parameters.
func resultFromQuery(q url.Values, state string) (authResult, error) {
	if e := q.Get("error"); e != "" {
		return authResult{}, fmt.Errorf("ChatGPT authorization failed: %s", e)
	}
	code := q.Get("code")
	if code == "" {
		return authResult{}, errors.New("missing authorization code")
	}
	if q.Get("state") == "" {
		return authResult{}, errors.New("missing OAuth state")
	}
	if q.Get("state") != state {
		return authResult{}, errors.New("OAuth state mismatch")
	}
	id := strings.TrimSpace(q.Get("client_id"))
	if id == "" {
		return authResult{}, errors.New("the redirect did not contain an issued client ID")
	}
	return authResult{code, id}, nil
}

// ParsePasted extracts the authorization result from a pasted redirect URL.
func ParsePasted(input, redirectURI, state string) (code, clientID string, err error) {
	u, err := url.Parse(strings.TrimSpace(input))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", errors.New("paste the full redirect URL from the browser")
	}
	want, _ := url.Parse(redirectURI)
	if u.Scheme != want.Scheme || u.Host != want.Host || u.Path != want.Path {
		return "", "", fmt.Errorf("the pasted URL must start with %s", redirectURI)
	}
	r, err := resultFromQuery(u.Query(), state)
	return r.code, r.clientID, err
}

func page(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>atto</title><body style=\"font-family:sans-serif;margin:3em\"><p>%s</p>", html.EscapeString(msg))
}

// Login runs the browser flow and returns the credential. The redirect is
// received on a loopback listener; if that port is taken, or the browser is
// on another machine, the user can paste the redirect URL instead.
func (c *ChatGPT) Login(ctx context.Context, ui UI) (Credential, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	verifier, challenge := PKCE()
	state, nonce := randomValue(), randomValue()

	results := make(chan authResult, 1)
	errs := make(chan error, 1)
	redirectURI := "http://" + callbackAddr + callbackPath
	ln, listenErr := net.Listen("tcp", c.ListenAddr)
	if listenErr == nil {
		redirectURI = "http://" + ln.Addr().String() + callbackPath
		mux := http.NewServeMux()
		mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
			res, err := resultFromQuery(r.URL.Query(), state)
			switch {
			case r.URL.Query().Get("error") != "":
				page(w, http.StatusBadRequest, "ChatGPT was not connected: "+r.URL.Query().Get("error"))
				select {
				case errs <- err:
				default:
				}
			case err != nil:
				page(w, http.StatusBadRequest, err.Error())
			default:
				page(w, http.StatusOK, "ChatGPT authentication completed. You can close this window.")
				select {
				case results <- res:
				default:
				}
			}
		})
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(ln)
		defer srv.Close()
	} else {
		ui.notice("port %s is busy (another login or the Codex CLI?); paste the redirect URL when asked", c.ListenAddr)
	}

	if ui.ReadPasted != nil {
		go func() {
			for ctx.Err() == nil {
				line, err := ui.ReadPasted()
				if err != nil {
					if listenErr != nil {
						select {
						case errs <- fmt.Errorf("no redirect received: %w", err):
						default:
						}
					}
					return
				}
				code, id, err := ParsePasted(line, redirectURI, state)
				if err != nil {
					ui.notice("%v", err)
					continue
				}
				select {
				case results <- authResult{code, id}:
				default:
				}
				return
			}
		}()
	}
	if ui.ShowURL != nil {
		ui.ShowURL(c.AuthURL(redirectURI, state, challenge, nonce))
	}

	select {
	case res := <-results:
		ui.notice("Exchanging the authorization code for tokens...")
		return c.exchange(ctx, res, verifier, redirectURI)
	case err := <-errs:
		return Credential{}, err
	case <-ctx.Done():
		return Credential{}, errors.New("login cancelled")
	}
}

type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
	IDToken      string  `json:"id_token"`
	Scope        string  `json:"scope"`
}

func (c *ChatGPT) requestToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	var tr tokenResponse
	req, err := http.NewRequestWithContext(ctx, "POST", c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tr, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client().Do(req)
	if err != nil {
		return tr, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return tr, fmt.Errorf("OpenAI OAuth token request failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return tr, fmt.Errorf("OpenAI OAuth token response: %w", err)
	}
	return tr, nil
}

func (c *ChatGPT) credential(tr tokenResponse, clientID string) (Credential, error) {
	switch {
	case strings.TrimSpace(tr.AccessToken) == "":
		return Credential{}, errors.New("OpenAI OAuth token response has no access_token")
	case strings.TrimSpace(tr.RefreshToken) == "":
		return Credential{}, errors.New("OpenAI OAuth token response has no refresh_token")
	case tr.ExpiresIn <= 0:
		return Credential{}, errors.New("OpenAI OAuth token response has invalid expires_in")
	}
	scopes := strings.Fields(tr.Scope)
	ok := false
	for _, s := range scopes {
		ok = ok || s == directScope
	}
	if !ok {
		return Credential{}, fmt.Errorf("OpenAI OAuth grant did not include %s", directScope)
	}
	exp := c.now().Add(time.Duration(tr.ExpiresIn*float64(time.Second)) - ExpiryMargin)
	return Credential{Type: "oauth", Access: tr.AccessToken, Refresh: tr.RefreshToken, Expires: exp.UnixMilli(), ClientID: clientID, Scopes: scopes}, nil
}

func (c *ChatGPT) exchange(ctx context.Context, r authResult, verifier, redirectURI string) (Credential, error) {
	tr, err := c.requestToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {r.clientID},
		"code":          {r.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {resource},
	})
	if err != nil {
		return Credential{}, err
	}
	if strings.TrimSpace(tr.IDToken) == "" {
		return Credential{}, errors.New("OpenAI OAuth token response did not contain an ID token")
	}
	return c.credential(tr, r.clientID)
}

// Refresh trades the refresh token for a new credential.
func (c *ChatGPT) Refresh(ctx context.Context, old Credential) (Credential, error) {
	if strings.TrimSpace(old.ClientID) == "" {
		return Credential{}, errors.New("stored OpenAI credential has no client ID; run: atto login openai")
	}
	tr, err := c.requestToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {old.ClientID},
		"refresh_token": {old.Refresh},
		"resource":      {resource},
	})
	if err != nil {
		return Credential{}, err
	}
	return c.credential(tr, old.ClientID)
}
