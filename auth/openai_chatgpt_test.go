package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1_800_000_000, 0)

func fakeTokenServer(t *testing.T, forms *[]url.Values) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("token request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		*forms = append(*forms, r.PostForm)
		resp := map[string]any{"access_token": "acc-" + r.PostForm.Get("grant_type"), "refresh_token": "ref-new", "expires_in": 3600, "scope": scope}
		if r.PostForm.Get("grant_type") == "authorization_code" {
			resp["id_token"] = "idt"
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestPKCEChallenge(t *testing.T) {
	v, c := PKCE()
	sum := sha256.Sum256([]byte(v))
	if c != base64.RawURLEncoding.EncodeToString(sum[:]) || strings.ContainsAny(v+c, "+/=") || len(v) < 43 {
		t.Fatalf("verifier %q challenge %q", v, c)
	}
	// RFC 7636 appendix B vector.
	sum = sha256.Sum256([]byte("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("S256 vector: %s", got)
	}
	if v2, _ := PKCE(); v2 == v {
		t.Fatal("verifier not random")
	}
}

func TestDeviceID(t *testing.T) {
	id := NewDeviceID()
	if len(id) != 36 || id[14] != '4' || id == NewDeviceID() {
		t.Fatalf("device id %q", id)
	}
}

func TestAuthURL(t *testing.T) {
	c := New("ABCDEF12-3456-4789-8abc-def012345678")
	u, _ := url.Parse(c.AuthURL("http://127.0.0.1:1455/auth/callback", "st", "ch", "no"))
	q := u.Query()
	if u.Host != "auth.openai.com" || q.Get("client_id") != "dynamic_agent_client" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") != "ch" || q.Get("state") != "st" || q.Get("ext_agent_host_id") != "urn:uuid:abcdef12-3456-4789-8abc-def012345678" ||
		q.Get("resource") != "https://api.openai.com/v1" || !strings.Contains(q.Get("scope"), "chatgpt.tokens.use.direct") {
		t.Fatalf("auth url %s", u)
	}
}

func TestParsePasted(t *testing.T) {
	const redir = "http://127.0.0.1:1455/auth/callback"
	code, id, err := ParsePasted(" "+redir+"?code=abc&state=S&client_id=cid \n", redir, "S")
	if err != nil || code != "abc" || id != "cid" {
		t.Fatalf("%q %q %v", code, id, err)
	}
	for name, in := range map[string]string{
		"not a url":     "abc",
		"wrong host":    "http://evil.example/auth/callback?code=a&state=S&client_id=c",
		"wrong path":    "http://127.0.0.1:1455/other?code=a&state=S&client_id=c",
		"state":         redir + "?code=a&state=X&client_id=c",
		"no state":      redir + "?code=a&client_id=c",
		"no code":       redir + "?state=S&client_id=c",
		"no client id":  redir + "?code=a&state=S",
		"provider fail": redir + "?error=access_denied&state=S",
	} {
		if _, _, err := ParsePasted(in, redir, "S"); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestRefresh(t *testing.T) {
	var forms []url.Values
	srv := fakeTokenServer(t, &forms)
	defer srv.Close()
	c := &ChatGPT{TokenURL: srv.URL, Now: func() time.Time { return t0 }}
	cred, err := c.Refresh(context.Background(), Credential{Refresh: "ref-old", ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}
	f := forms[0]
	if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "ref-old" || f.Get("client_id") != "cid" {
		t.Fatalf("form %v", f)
	}
	wantExp := t0.Add(time.Hour - ExpiryMargin).UnixMilli()
	if cred.Access != "acc-refresh_token" || cred.Refresh != "ref-new" || cred.Expires != wantExp || cred.ClientID != "cid" {
		t.Fatalf("cred %+v", cred)
	}
	if cred.Expired(t0) || !cred.Expired(t0.Add(time.Hour)) {
		t.Fatal("expiry margin")
	}
	if _, err := c.Refresh(context.Background(), Credential{Refresh: "x"}); err == nil {
		t.Fatal("refresh without client id must fail")
	}
}

func TestTokenValidation(t *testing.T) {
	bad := map[string]map[string]any{
		"no scope":   {"access_token": "a", "refresh_token": "r", "expires_in": 60, "scope": "openid"},
		"no refresh": {"access_token": "a", "expires_in": 60, "scope": scope},
		"no expiry":  {"access_token": "a", "refresh_token": "r", "scope": scope},
	}
	for name, resp := range bad {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(resp) }))
		c := &ChatGPT{TokenURL: srv.URL}
		if _, err := c.Refresh(context.Background(), Credential{ClientID: "c", Refresh: "r"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 400) }))
	defer srv.Close()
	if _, err := (&ChatGPT{TokenURL: srv.URL}).Refresh(context.Background(), Credential{ClientID: "c"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("http error: %v", err)
	}
}

// loginWith drives Login like a browser would: it reads the authorization
// URL, then either hits the loopback callback or answers the paste prompt.
func loginWith(t *testing.T, paste bool, busy bool) (Credential, []url.Values) {
	var forms []url.Values
	srv := fakeTokenServer(t, &forms)
	defer srv.Close()
	addr := "127.0.0.1:0"
	if busy {
		// Hold a port so Login cannot bind it.
		hold := httptest.NewServer(http.NotFoundHandler())
		defer hold.Close()
		addr = strings.TrimPrefix(hold.URL, "http://")
	}
	c := &ChatGPT{AuthorizeURL: "http://auth.invalid/authorize", TokenURL: srv.URL, ListenAddr: addr, DeviceID: NewDeviceID(), Now: func() time.Time { return t0 }}

	urls := make(chan string, 1)
	pasted := make(chan string, 1)
	ui := UI{ShowURL: func(u string) { urls <- u }}
	if paste {
		ui.ReadPasted = func() (string, error) { return <-pasted, nil }
	}
	go func() {
		u, _ := url.Parse(<-urls)
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=thecode&client_id=issued&state="
		if paste {
			pasted <- "garbage"           // ignored, prompt repeats
			pasted <- cb + "wrong"        // state mismatch, ignored
			pasted <- cb + q.Get("state") // accepted
			return
		}
		if resp, err := http.Get(cb + "wrong"); err == nil { // mismatched state must not finish the login
			if resp.StatusCode != 400 {
				t.Errorf("mismatched callback status %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
		resp, err := http.Get(cb + q.Get("state"))
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cred, err := c.Login(ctx, ui)
	if err != nil {
		t.Fatal(err)
	}
	return cred, forms
}

func TestLoginViaCallback(t *testing.T) {
	cred, forms := loginWith(t, false, false)
	f := forms[0]
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "thecode" || f.Get("client_id") != "issued" ||
		f.Get("code_verifier") == "" || f.Get("resource") != "https://api.openai.com/v1" || !strings.HasPrefix(f.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatalf("exchange form %v", f)
	}
	if cred.Access != "acc-authorization_code" || cred.ClientID != "issued" || cred.Refresh != "ref-new" || cred.Expires != t0.Add(time.Hour-ExpiryMargin).UnixMilli() {
		t.Fatalf("cred %+v", cred)
	}
}

func TestLoginViaPasteWhenPortBusy(t *testing.T) {
	cred, forms := loginWith(t, true, true)
	if cred.ClientID != "issued" || forms[0].Get("code") != "thecode" {
		t.Fatalf("cred %+v form %v", cred, forms[0])
	}
}

func TestLoginCancelled(t *testing.T) {
	c := &ChatGPT{ListenAddr: "127.0.0.1:0", DeviceID: NewDeviceID(), AuthorizeURL: "http://x"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Login(ctx, UI{}); err == nil {
		t.Fatal("expected cancellation")
	}
}
