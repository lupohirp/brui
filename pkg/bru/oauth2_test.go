package bru

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a minimal OAuth2 authorization server.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	hits       map[string]int // grant_type -> token endpoint calls
	challenges map[string]string
	expiresIn  int
	jwtTokens  bool
	lastAPI    *http.Request
	issued     int
}

const (
	testClientID     = "brui-client"
	testClientSecret = "s3cr/t+=" // characters that break naive encoding
)

// testManager never launches a real browser.
func testManager() *OAuth2Manager {
	om := NewOAuth2Manager("")
	om.FindBrowser = func() string { return "" }
	return om
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{t: t, hits: map[string]int{}, challenges: map[string]string{}, expiresIn: 3600}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", f.authorize)
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastAPI = r.Clone(context.Background())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"auth": %q, "query": %q}`, r.Header.Get("Authorization"), r.URL.RawQuery)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != testClientID || q.Get("redirect_uri") == "" || q.Get("state") == "" {
		http.Error(w, "bad authorize request: "+r.URL.RawQuery, http.StatusBadRequest)
		return
	}
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	if q.Get("code_challenge_method") == "S256" {
		f.mu.Lock()
		f.challenges[code] = q.Get("code_challenge")
		f.mu.Unlock()
	}
	redirect := q.Get("redirect_uri") + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(q.Get("state"))
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (f *fakeIdP) fail(w http.ResponseWriter, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `{"error": %q, "error_description": %q}`, code, desc)
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.fail(w, "invalid_request", err.Error())
		return
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != testClientID || secret != testClientSecret {
		f.fail(w, "invalid_client", "bad client credentials")
		return
	}
	grant := r.PostForm.Get("grant_type")
	f.mu.Lock()
	f.hits[grant]++
	f.mu.Unlock()

	switch grant {
	case GrantClientCredentials:
	case GrantPassword:
		if r.PostForm.Get("username") != "alice" || r.PostForm.Get("password") != "wonderland" {
			f.fail(w, "invalid_grant", "wrong username or password")
			return
		}
	case GrantAuthorizationCode:
		code := r.PostForm.Get("code")
		f.mu.Lock()
		challenge, hasPKCE := f.challenges[code]
		f.mu.Unlock()
		if !strings.HasPrefix(code, "code-") || r.PostForm.Get("redirect_uri") == "" {
			f.fail(w, "invalid_grant", "bad code")
			return
		}
		if hasPKCE {
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				f.fail(w, "invalid_grant", "PKCE verification failed")
				return
			}
		}
	case "refresh_token":
		if !strings.HasPrefix(r.PostForm.Get("refresh_token"), "refresh-") {
			f.fail(w, "invalid_grant", "bad refresh token")
			return
		}
	default:
		f.fail(w, "unsupported_grant_type", grant)
		return
	}

	f.mu.Lock()
	f.issued++
	n := f.issued
	f.mu.Unlock()
	access := fmt.Sprintf("%s-token-%d", grant, n)
	if f.jwtTokens {
		payload, _ := json.Marshal(map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
		access = "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	}
	resp := map[string]any{
		"access_token": access, "token_type": "Bearer",
		"refresh_token": fmt.Sprintf("refresh-%d", n), "id_token": fmt.Sprintf("id-%d", n),
	}
	if f.expiresIn > 0 {
		resp["expires_in"] = f.expiresIn
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeIdP) hitCount(grant string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[grant]
}

func (f *fakeIdP) config(grant string) *OAuth2Config {
	return &OAuth2Config{
		GrantType:        grant,
		AuthorizationURL: f.srv.URL + "/authorize",
		AccessTokenURL:   "{{idp}}/token",
		ClientID:         testClientID,
		ClientSecret:     testClientSecret,
		Username:         "alice",
		Password:         "wonderland",
		Scope:            "openid profile",
		AutoFetchToken:   true,
	}
}

func (f *fakeIdP) coll() *BrunoCollection {
	return &BrunoCollection{Path: "/tmp/coll", RuntimeVariables: map[string]any{"idp": f.srv.URL}}
}

func (f *fakeIdP) apiRequest(cfg *OAuth2Config) *BrunoRequest {
	return &BrunoRequest{Method: MethodGet, URL: f.srv.URL + "/api", AuthType: "oauth2", AuthOAuth2: cfg}
}

func TestParseOAuth2Block(t *testing.T) {
	req := ParseRequest(`get {
  url: https://x.io
  auth: oauth2
}

auth:oauth2 {
  grant_type: authorization_code
  callback_url: {{redirect}}
  authorization_url: https://idp/authorize
  access_token_url: https://idp/token
  refresh_token_url:
  client_id: abc
  client_secret: shh
  scope: openid oob profile
  state:
  pkce: true
  credentials_placement:
  token_source: access_token
  auto_fetch_token: true
  auto_refresh_token: false
}
`)
	c := req.AuthOAuth2
	if c == nil || c.GrantType != GrantAuthorizationCode || c.CallbackURL != "{{redirect}}" ||
		c.AccessTokenURL != "https://idp/token" || !c.PKCE || !c.AutoFetchToken || c.AutoRefreshToken ||
		c.Scope != "openid oob profile" {
		t.Fatalf("parsed config: %+v", c)
	}
	rc := c.Resolve(&BrunoCollection{RuntimeVariables: map[string]any{"redirect": "https://app/cb"}})
	if rc.CallbackURL != "https://app/cb" || rc.CredentialsPlacement != "body" || rc.TokenPlacement != "header" ||
		rc.TokenHeaderPrefix != "Bearer" || rc.CredentialsID != "credentials" {
		t.Fatalf("resolved defaults: %+v", rc)
	}
}

func TestClientCredentialsGrantAndCache(t *testing.T) {
	for _, placement := range []string{"body", "basic_auth_header"} {
		t.Run(placement, func(t *testing.T) {
			idp := newFakeIdP(t)
			cfg := idp.config(GrantClientCredentials)
			cfg.CredentialsPlacement = placement
			opts := RunOptions{OAuth2: testManager()}
			coll := idp.coll()

			res := RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts)
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			if !strings.Contains(res.Response.Body, `"auth": "Bearer client_credentials-token-1"`) || res.AuthNote != "new token" {
				t.Fatalf("token not applied: %s (note %q)", res.Response.Body, res.AuthNote)
			}
			res = RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts)
			if res.Err != nil || res.AuthNote != "" || idp.hitCount(GrantClientCredentials) != 1 {
				t.Fatalf("cached token not reused: err=%v note=%q hits=%d", res.Err, res.AuthNote, idp.hitCount(GrantClientCredentials))
			}
		})
	}
}

func TestPasswordGrantRefreshAndRefetch(t *testing.T) {
	idp := newFakeIdP(t)
	om := testManager()
	clock := time.Now()
	om.now = func() time.Time { return clock }
	opts := RunOptions{OAuth2: om}
	coll := idp.coll()
	cfg := idp.config(GrantPassword)
	cfg.AutoRefreshToken = true

	if res := RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts); res.Err != nil || res.AuthNote != "new token" {
		t.Fatalf("first fetch: %v %q", res.Err, res.AuthNote)
	}

	clock = clock.Add(2 * time.Hour) // token expired
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts)
	if res.Err != nil || res.AuthNote != "token refreshed" || idp.hitCount("refresh_token") != 1 {
		t.Fatalf("refresh: err=%v note=%q refreshes=%d", res.Err, res.AuthNote, idp.hitCount("refresh_token"))
	}
	if !strings.Contains(res.Response.Body, "refresh_token-token-2") {
		t.Fatalf("refreshed token not applied: %s", res.Response.Body)
	}

	// With auto refresh off, an expired token is replaced by a new fetch.
	cfg.AutoRefreshToken = false
	clock = clock.Add(2 * time.Hour)
	if res := RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts); res.Err != nil || idp.hitCount(GrantPassword) != 2 {
		t.Fatalf("refetch: err=%v password grants=%d", res.Err, idp.hitCount(GrantPassword))
	}

	// Wrong credentials surface the server's error and do not send the request.
	cfg.Password = "nope"
	cfg.Username = "bob" // different cache key
	res = RunWithScripts(context.Background(), idp.apiRequest(cfg), coll, opts)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "wrong username or password") || res.Response != nil {
		t.Fatalf("want invalid_grant error, got %v", res.Err)
	}
}

func freeLoopbackURL(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr + "/callback"
}

// Mirrors "Get Token - WEB": authorization_code + PKCE, and a post-response
// script that reads the injected Authorization header and decodes the JWT
// with Buffer.
func TestAuthorizationCodeLoopbackPKCEWithScript(t *testing.T) {
	idp := newFakeIdP(t)
	idp.jwtTokens = true
	idp.expiresIn = 0 // expiry must come from the JWT exp claim
	om := testManager()
	om.OpenBrowser = func(u string) error {
		go func() { // the "browser" follows the redirect to the loopback server
			if resp, err := http.Get(u); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = freeLoopbackURL(t)
	cfg.PKCE = true

	req := idp.apiRequest(cfg)
	req.PostResponseScript = `
		const authHeader = req.getHeader("Authorization");
		const token = authHeader.slice(7);
		bru.setEnvVar("access_token", token);
		const payload = JSON.parse(Buffer.from(token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/"), "base64").toString("utf8"));
		bru.setEnvVar("sub", payload.sub);`
	res := RunWithScripts(context.Background(), req, idp.coll(), RunOptions{OAuth2: om})
	if res.Err != nil || res.PostScriptErr != nil {
		t.Fatalf("errors: %v / %v", res.Err, res.PostScriptErr)
	}
	if res.RuntimeVariables["sub"] != "alice" || !strings.HasPrefix(fmt.Sprint(res.RuntimeVariables["access_token"]), "eyJ") {
		t.Fatalf("post-response script did not see the token: %v", res.RuntimeVariables)
	}
	tok, _ := om.Cached(req, idp.coll())
	if tok == nil || tok.ExpiresAt.IsZero() {
		t.Fatalf("JWT exp not used for expiry: %+v", tok)
	}
}

func TestAuthorizationCodePastedRedirect(t *testing.T) {
	idp := newFakeIdP(t)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	login := func(authURL string) string { // simulate the user logging in and copying the URL
		resp, err := noRedirect.Get(authURL)
		if err != nil {
			t.Error(err)
			return ""
		}
		resp.Body.Close()
		return resp.Header.Get("Location")
	}

	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/login-token.html" // not loopback: must be pasted
	cfg.PKCE = true

	var opened string
	om := testManager()
	om.OpenBrowser = func(u string) error { opened = u; return nil }
	prompt := func(p AuthCodePrompt) {
		if p.Listening || p.CallbackURL != cfg.CallbackURL {
			t.Errorf("unexpected prompt: %+v", p)
		}
		p.Paste <- "  " + login(p.AuthURL) + "\n"
	}
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: prompt})
	if res.Err != nil || !strings.Contains(res.Response.Body, "authorization_code-token-1") {
		t.Fatalf("pasted flow failed: %v", res.Err)
	}
	if !strings.Contains(opened, "code_challenge_method=S256") {
		t.Errorf("browser not opened with PKCE URL: %s", opened)
	}

	// A redirect carrying a different state is rejected.
	cfg.Scope = "other" // new cache key
	tampered := func(p AuthCodePrompt) {
		loc := login(p.AuthURL)
		p.Paste <- loc[:strings.Index(loc, "&state=")] + "&state=forged"
	}
	res = RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: tampered})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "state mismatch") {
		t.Fatalf("want state mismatch, got %v", res.Err)
	}

	// Cancelling while waiting for the login aborts cleanly.
	cfg.Scope = "third"
	ctx, cancel := context.WithCancel(context.Background())
	res = RunWithScripts(ctx, idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: func(AuthCodePrompt) { cancel() }})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "context canceled") {
		t.Fatalf("want cancellation, got %v", res.Err)
	}
}

func TestParseRedirect(t *testing.T) {
	cases := map[string]callbackResult{
		"https://app/cb?code=abc&state=xyz":     {code: "abc", state: "xyz"},
		"https://app/cb#code=abc&state=xyz":     {code: "abc", state: "xyz"},
		"abc123":                                {code: "abc123"},
		"code=abc&state=s":                      {code: "abc", state: "s"},
		"https://app/cb?error=access_denied&x=": {},
	}
	for in, want := range cases {
		got := parseRedirect(in)
		if strings.Contains(in, "error=") {
			if got.err == nil {
				t.Errorf("%s: want error", in)
			}
			continue
		}
		if got.code != want.code || got.state != want.state || got.err != nil {
			t.Errorf("%s: got %+v", in, got)
		}
	}
}

func TestTokenPlacementAndSource(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := idp.config(GrantClientCredentials)
	cfg.TokenPlacement = "url"
	cfg.TokenQueryKey = "tok"
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{})
	if res.Err != nil || !strings.Contains(res.Response.Body, `"query": "tok=client_credentials-token-1"`) || strings.Contains(res.Response.Body, "Bearer") {
		t.Fatalf("url placement: %v %s", res.Err, res.Response.Body)
	}

	cfg = idp.config(GrantClientCredentials)
	cfg.TokenSource = "id_token"
	cfg.TokenHeaderPrefix = "Token"
	res = RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{})
	if res.Err != nil || !strings.Contains(res.Response.Body, `"auth": "Token id-2"`) {
		t.Fatalf("id_token/prefix: %v %s", res.Err, res.Response.Body)
	}
}

func TestOAuth2InheritedFromCollection(t *testing.T) {
	idp := newFakeIdP(t)
	coll := idp.coll()
	coll.CollectionAuthType = "oauth2"
	coll.CollectionAuthOAuth2 = idp.config(GrantClientCredentials)
	req := &BrunoRequest{Method: MethodGet, URL: idp.srv.URL + "/api", AuthType: "inherit",
		Headers: []BruHeader{{Name: "authorization", Value: "stale", Enabled: true}}}
	res := RunWithScripts(context.Background(), req, coll, RunOptions{})
	if res.Err != nil || !strings.Contains(res.Response.Body, `"auth": "Bearer client_credentials-token-1"`) {
		t.Fatalf("inherited oauth2: %v %s", res.Err, res.Response.Body)
	}
}

func TestAutoFetchOffSendsWithoutToken(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := idp.config(GrantClientCredentials)
	cfg.AutoFetchToken = false
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{})
	if res.Err != nil || !strings.Contains(res.Response.Body, `"auth": ""`) || !strings.Contains(res.AuthNote, "press O") {
		t.Fatalf("auto-fetch off: %v %q %s", res.Err, res.AuthNote, res.Response.Body)
	}
	if idp.hitCount(GrantClientCredentials) != 0 {
		t.Fatal("token endpoint must not be called with auto_fetch_token off")
	}
}

func TestTokenPersistence(t *testing.T) {
	idp := newFakeIdP(t)
	path := filepath.Join(t.TempDir(), "cache", "tokens.json")
	cfg := idp.config(GrantPassword)
	req := idp.apiRequest(cfg)

	if _, err := NewOAuth2Manager(path).Fetch(context.Background(), req, idp.coll(), nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file must exist with 0600 permissions: %v %v", err, info.Mode())
	}
	tok, _ := NewOAuth2Manager(path).Cached(req, idp.coll())
	if !tok.Valid(time.Now()) || tok.AccessToken != "password-token-1" {
		t.Fatalf("token not restored from disk: %+v", tok)
	}
}

func TestJSShims(t *testing.T) {
	sr := NewScriptRunner(&BrunoCollection{RuntimeVariables: map[string]any{}})
	err := sr.RunPreRequest(&BrunoRequest{PreRequestScript: `
		bru.setVar("a", atob("aGVsbG8="));
		bru.setVar("b", btoa("hello"));
		bru.setVar("c", Buffer.from("eyJ4IjoxfQ", "base64").toString("utf8"));
		bru.setVar("d", Buffer.from("hi").toString("hex"));
		bru.setVar("e", Buffer.from("héllo").toString("base64"));`})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "hello", "b": "aGVsbG8=", "c": `{"x":1}`, "d": "6869", "e": "aMOpbGxv"}
	for k, v := range want {
		if got := sr.coll.RuntimeVariables[k]; got != v {
			t.Errorf("%s: want %q, got %v", k, v, got)
		}
	}
}

func TestUndefinedVariableInConfigIsReported(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "{{PW_REDIRECT_URI}}"
	om := testManager()
	om.OpenBrowser = func(string) error { t.Error("browser must not open with an unresolved config"); return nil }
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: func(AuthCodePrompt) {}})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "callback_url uses {{PW_REDIRECT_URI}}") {
		t.Fatalf("want a clear undefined-variable error, got %v", res.Err)
	}
}
