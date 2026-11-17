package bru

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OAuth2 grant types supported by brui.
const (
	GrantAuthorizationCode = "authorization_code"
	GrantPassword          = "password"
	GrantClientCredentials = "client_credentials"
)

// tokenExpirySkew treats tokens as expired slightly early, so a request never
// leaves with a token that expires in flight.
const tokenExpirySkew = 10 * time.Second

// parseOAuth2Block parses the content of an `auth:oauth2` block.
func parseOAuth2Block(lines []string) *OAuth2Config {
	kv := map[string]string{}
	for _, l := range lines {
		if k, v := parseKeyValue(l); k != "" && !strings.HasPrefix(k, "~") {
			kv[k] = v
		}
	}
	return &OAuth2Config{
		GrantType:            kv["grant_type"],
		CallbackURL:          kv["callback_url"],
		AuthorizationURL:     kv["authorization_url"],
		AccessTokenURL:       kv["access_token_url"],
		RefreshTokenURL:      kv["refresh_token_url"],
		ClientID:             kv["client_id"],
		ClientSecret:         kv["client_secret"],
		Username:             kv["username"],
		Password:             kv["password"],
		Scope:                kv["scope"],
		State:                kv["state"],
		PKCE:                 parseBool(kv["pkce"], false),
		CredentialsPlacement: kv["credentials_placement"],
		CredentialsID:        kv["credentials_id"],
		TokenPlacement:       kv["token_placement"],
		TokenHeaderPrefix:    kv["token_header_prefix"],
		TokenQueryKey:        kv["token_query_key"],
		TokenSource:          kv["token_source"],
		AutoFetchToken:       parseBool(kv["auto_fetch_token"], true),
		AutoRefreshToken:     parseBool(kv["auto_refresh_token"], false),
	}
}

func parseBool(s string, def bool) bool {
	if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
		return b
	}
	return def
}

// Resolve returns a copy with {{variables}} interpolated and Bruno's defaults
// applied to empty settings.
func (c OAuth2Config) Resolve(coll *BrunoCollection) OAuth2Config {
	for _, f := range []*string{
		&c.CallbackURL, &c.AuthorizationURL, &c.AccessTokenURL, &c.RefreshTokenURL,
		&c.ClientID, &c.ClientSecret, &c.Username, &c.Password, &c.Scope, &c.State,
		&c.CredentialsID, &c.TokenHeaderPrefix, &c.TokenQueryKey,
	} {
		*f = strings.TrimSpace(Interpolate(*f, coll))
	}
	defaults := []struct {
		f   *string
		def string
	}{
		{&c.CredentialsPlacement, "body"},
		{&c.CredentialsID, "credentials"},
		{&c.TokenPlacement, "header"},
		{&c.TokenQueryKey, "access_token"},
		{&c.TokenSource, "access_token"},
	}
	for _, d := range defaults {
		if *d.f == "" {
			*d.f = d.def
		}
	}
	// An explicitly empty header prefix is meaningful in Bruno only when the
	// key is present; brui treats empty as the common "Bearer".
	if c.TokenHeaderPrefix == "" {
		c.TokenHeaderPrefix = "Bearer"
	}
	return c
}

// EffectiveOAuth2 returns the OAuth2 configuration that applies to req
// (its own, or the collection's when auth is inherited), or nil.
func EffectiveOAuth2(req *BrunoRequest, coll *BrunoCollection) *OAuth2Config {
	switch req.AuthType {
	case "oauth2":
		return req.AuthOAuth2
	case "inherit", "":
		if coll != nil && coll.CollectionAuthType == "oauth2" {
			return coll.CollectionAuthOAuth2
		}
	}
	return nil
}

// OAuth2Token is a token obtained from an authorization server.
type OAuth2Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"` // zero when unknown
	ObtainedAt   time.Time `json:"obtained_at"`
}

// Valid reports whether the token can still be used at time now.
func (t *OAuth2Token) Valid(now time.Time) bool {
	if t == nil || (t.AccessToken == "" && t.IDToken == "") {
		return false
	}
	return t.ExpiresAt.IsZero() || now.Before(t.ExpiresAt.Add(-tokenExpirySkew))
}

func (t *OAuth2Token) value(source string) string {
	if source == "id_token" && t.IDToken != "" {
		return t.IDToken
	}
	return t.AccessToken
}

// AuthCodePrompt asks the user to complete an authorization_code login.
type AuthCodePrompt struct {
	AuthURL     string
	CallbackURL string
	Listening   bool  // a local server is waiting for the redirect
	BrowserErr  error // the browser could not be opened automatically
	// Automatic is set when brui drives its own login window (Browser) and
	// will complete the flow by itself; nothing needs to be pasted.
	Automatic bool
	Browser   string
	// Fallback explains why automatic login could not be used, if it was tried.
	Fallback string
	// NoControllableBrowser suggests installing Chromium for automatic login.
	NoControllableBrowser bool
	// Paste receives the redirect URL (or bare code) typed by the user. It is
	// buffered; send at most once.
	Paste chan<- string
}

// PromptFunc delivers an AuthCodePrompt to the user interface.
type PromptFunc func(AuthCodePrompt)

// OAuth2Manager obtains, caches, refreshes and optionally persists tokens.
// It is safe for concurrent use.
type OAuth2Manager struct {
	mu      sync.Mutex // guards tokens/loaded; held only briefly
	fetchMu sync.Mutex // serializes token acquisition (one login at a time)
	tokens  map[string]*OAuth2Token
	loaded  bool
	path    string // persistence file; "" keeps tokens in memory only

	// OpenBrowser opens the authorization URL in the default browser;
	// replaceable for tests.
	OpenBrowser func(string) error
	// FindBrowser locates a Chromium-based browser for automatic login
	// ("" = none); replaceable for tests.
	FindBrowser func() string
	// LoginWindow is a native login window (macOS WebKit), tried before
	// FindBrowser; nil when the platform or binary does not provide one.
	LoginWindow *LoginWindow
	now         func() time.Time
}

// LoginWindow runs an interactive login in a window brui controls. Run opens
// authURL, waits until the authorization server redirects to callback and
// returns that URL (without loading it). It returns ErrLoginWindowClosed if
// the user closes the window and an error wrapping ErrBrowserLaunch if the
// window cannot be shown.
type LoginWindow struct {
	Name string
	Run  func(ctx context.Context, authURL, callback string) (string, error)
}

// NewOAuth2Manager returns a manager that persists tokens to path (0600), or
// keeps them in memory only when path is "".
func NewOAuth2Manager(path string) *OAuth2Manager {
	return &OAuth2Manager{
		tokens:      map[string]*OAuth2Token{},
		path:        path,
		OpenBrowser: openBrowser,
		FindBrowser: FindControllableBrowser,
		now:         time.Now,
	}
}

// DefaultTokenCachePath is where tokens are persisted between sessions.
func DefaultTokenCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "brui", "oauth2-tokens.json")
}

func oauth2CacheKey(collPath string, c OAuth2Config) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		collPath, c.GrantType, c.AccessTokenURL, c.ClientID, c.CredentialsID, c.Scope, c.Username,
	}, "\x00")))
	return hex.EncodeToString(h[:16])
}

func (om *OAuth2Manager) loadLocked() {
	if om.loaded {
		return
	}
	om.loaded = true
	if om.path == "" {
		return
	}
	data, err := os.ReadFile(om.path)
	if err != nil {
		return
	}
	var stored map[string]*OAuth2Token
	if json.Unmarshal(data, &stored) == nil {
		for k, t := range stored {
			om.tokens[k] = t
		}
	}
}

func (om *OAuth2Manager) saveLocked() {
	if om.path == "" {
		return
	}
	// Drop tokens that can never be used again.
	keep := map[string]*OAuth2Token{}
	for k, t := range om.tokens {
		if t.Valid(om.now()) || t.RefreshToken != "" {
			keep[k] = t
		}
	}
	data, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(om.path), 0o700); err != nil {
		return
	}
	tmp := om.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, om.path)
	}
}

func (om *OAuth2Manager) get(key string) *OAuth2Token {
	om.mu.Lock()
	defer om.mu.Unlock()
	om.loadLocked()
	return om.tokens[key]
}

func (om *OAuth2Manager) put(key string, t *OAuth2Token) {
	om.mu.Lock()
	defer om.mu.Unlock()
	om.loadLocked()
	om.tokens[key] = t
	om.saveLocked()
}

// Cached returns the cached token for req's effective OAuth2 config without
// any network access, plus the resolved config (nil if req has no OAuth2).
func (om *OAuth2Manager) Cached(req *BrunoRequest, coll *BrunoCollection) (*OAuth2Token, *OAuth2Config) {
	cfg := EffectiveOAuth2(req, coll)
	if cfg == nil {
		return nil, nil
	}
	rc := cfg.Resolve(coll)
	return om.get(oauth2CacheKey(coll.Path, rc)), &rc
}

// Clear forgets the cached token for req's effective OAuth2 config.
func (om *OAuth2Manager) Clear(req *BrunoRequest, coll *BrunoCollection) {
	cfg := EffectiveOAuth2(req, coll)
	if cfg == nil {
		return
	}
	key := oauth2CacheKey(coll.Path, cfg.Resolve(coll))
	om.mu.Lock()
	defer om.mu.Unlock()
	om.loadLocked()
	delete(om.tokens, key)
	om.saveLocked()
}

// Fetch always obtains a new token for req's effective OAuth2 config (manual
// "get token"), ignoring the cache.
func (om *OAuth2Manager) Fetch(ctx context.Context, req *BrunoRequest, coll *BrunoCollection, prompt PromptFunc) (*OAuth2Token, error) {
	cfg := EffectiveOAuth2(req, coll)
	if cfg == nil {
		return nil, errors.New("request does not use OAuth2")
	}
	rc := cfg.Resolve(coll)
	om.fetchMu.Lock()
	defer om.fetchMu.Unlock()
	tok, err := om.fetchNew(ctx, rc, prompt)
	if err != nil {
		return nil, err
	}
	om.put(oauth2CacheKey(coll.Path, rc), tok)
	return tok, nil
}

// token returns a usable token for rc following Bruno's rules: a valid cached
// token is reused; an expired one is refreshed when auto_refresh_token is on
// and a refresh token exists; otherwise a new one is fetched when
// auto_fetch_token is on. note describes what happened ("" = cache hit).
func (om *OAuth2Manager) token(ctx context.Context, rc OAuth2Config, collPath string, prompt PromptFunc) (tok *OAuth2Token, note string, err error) {
	key := oauth2CacheKey(collPath, rc)
	om.fetchMu.Lock()
	defer om.fetchMu.Unlock()

	cached := om.get(key)
	if cached.Valid(om.now()) {
		return cached, "", nil
	}
	if cached != nil && cached.RefreshToken != "" && rc.AutoRefreshToken {
		if t, rerr := om.refresh(ctx, rc, cached.RefreshToken); rerr == nil {
			om.put(key, t)
			return t, "token refreshed", nil
		} else if !rc.AutoFetchToken {
			return nil, "", fmt.Errorf("refresh failed: %w", rerr)
		}
	}
	if !rc.AutoFetchToken {
		if cached != nil {
			return nil, "token expired (auto-fetch off, press O to get a new one)", nil
		}
		return nil, "no token (auto-fetch off, press O to get one)", nil
	}
	t, err := om.fetchNew(ctx, rc, prompt)
	if err != nil {
		return nil, "", err
	}
	om.put(key, t)
	return t, "new token", nil
}

// checkResolved reports config fields that still reference undefined
// variables, which would otherwise produce confusing server-side errors.
func checkResolved(rc OAuth2Config) error {
	fields := []struct{ name, value string }{
		{"access_token_url", rc.AccessTokenURL}, {"client_id", rc.ClientID}, {"client_secret", rc.ClientSecret},
	}
	switch rc.GrantType {
	case GrantAuthorizationCode:
		fields = append(fields, struct{ name, value string }{"authorization_url", rc.AuthorizationURL},
			struct{ name, value string }{"callback_url", rc.CallbackURL})
	case GrantPassword:
		fields = append(fields, struct{ name, value string }{"username", rc.Username},
			struct{ name, value string }{"password", rc.Password})
	}
	for _, f := range fields {
		if m := varRegex.FindString(f.value); m != "" {
			return fmt.Errorf("%s uses %s, which is not defined in the active environment", f.name, m)
		}
	}
	return nil
}

func (om *OAuth2Manager) fetchNew(ctx context.Context, rc OAuth2Config, prompt PromptFunc) (*OAuth2Token, error) {
	if err := checkResolved(rc); err != nil {
		return nil, err
	}
	switch rc.GrantType {
	case GrantClientCredentials:
		form := url.Values{"grant_type": {GrantClientCredentials}}
		if rc.Scope != "" {
			form.Set("scope", rc.Scope)
		}
		return om.requestToken(ctx, rc, rc.AccessTokenURL, form)
	case GrantPassword:
		form := url.Values{"grant_type": {GrantPassword}, "username": {rc.Username}, "password": {rc.Password}}
		if rc.Scope != "" {
			form.Set("scope", rc.Scope)
		}
		return om.requestToken(ctx, rc, rc.AccessTokenURL, form)
	case GrantAuthorizationCode:
		return om.authorizationCode(ctx, rc, prompt)
	case "":
		return nil, errors.New("auth:oauth2 has no grant_type")
	default:
		return nil, fmt.Errorf("unsupported grant_type %q (supported: authorization_code, password, client_credentials)", rc.GrantType)
	}
}

func (om *OAuth2Manager) refresh(ctx context.Context, rc OAuth2Config, refreshToken string) (*OAuth2Token, error) {
	endpoint := rc.RefreshTokenURL
	if endpoint == "" {
		endpoint = rc.AccessTokenURL
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	if rc.Scope != "" {
		form.Set("scope", rc.Scope)
	}
	t, err := om.requestToken(ctx, rc, endpoint, form)
	if err == nil && t.RefreshToken == "" {
		t.RefreshToken = refreshToken // servers may omit it when it is unchanged
	}
	return t, err
}

// requestToken POSTs a token request and parses the response.
func (om *OAuth2Manager) requestToken(ctx context.Context, rc OAuth2Config, endpoint string, form url.Values) (*OAuth2Token, error) {
	if endpoint == "" {
		return nil, errors.New("auth:oauth2 has no access_token_url")
	}
	basic := rc.CredentialsPlacement == "basic_auth_header"
	if !basic {
		if rc.ClientID != "" {
			form.Set("client_id", rc.ClientID)
		}
		if rc.ClientSecret != "" {
			form.Set("client_secret", rc.ClientSecret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(rc.ClientID, rc.ClientSecret) // raw, as Bruno sends it
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("token response: %w", err)
	}
	return parseTokenResponse(resp.StatusCode, body, om.now())
}

func parseTokenResponse(status int, body []byte, now time.Time) (*OAuth2Token, error) {
	fields := map[string]any{}
	if json.Unmarshal(body, &fields) != nil {
		// Some servers (e.g. GitHub) answer form-encoded.
		if q, err := url.ParseQuery(string(body)); err == nil {
			for k := range q {
				fields[k] = q.Get(k)
			}
		}
	}
	str := func(k string) string {
		switch v := fields[k].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	if e := str("error"); e != "" {
		if d := str("error_description"); d != "" {
			return nil, fmt.Errorf("authorization server error: %s: %s", e, d)
		}
		return nil, fmt.Errorf("authorization server error: %s", e)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("token endpoint returned HTTP %d: %s", status, snippet(body))
	}
	t := &OAuth2Token{
		AccessToken:  str("access_token"),
		RefreshToken: str("refresh_token"),
		IDToken:      str("id_token"),
		TokenType:    str("token_type"),
		Scope:        str("scope"),
		ObtainedAt:   now,
	}
	if t.AccessToken == "" && t.IDToken == "" {
		return nil, fmt.Errorf("token response has no access_token: %s", snippet(body))
	}
	if secs, err := strconv.ParseFloat(str("expires_in"), 64); err == nil && secs > 0 {
		t.ExpiresAt = now.Add(time.Duration(secs * float64(time.Second)))
	} else if exp, ok := jwtExpiry(t.AccessToken); ok {
		t.ExpiresAt = exp
	}
	return t, nil
}

// jwtExpiry reads the exp claim of a JWT without verifying it.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// authorizationCode runs the authorization code flow (with PKCE when
// enabled): it opens the browser on the authorization URL and waits for the
// code, either from a local server (loopback callback_url) or pasted by the
// user through prompt.
func (om *OAuth2Manager) authorizationCode(ctx context.Context, rc OAuth2Config, prompt PromptFunc) (*OAuth2Token, error) {
	if rc.AuthorizationURL == "" {
		return nil, errors.New("auth:oauth2 has no authorization_url")
	}
	if rc.CallbackURL == "" {
		return nil, errors.New("auth:oauth2 has no callback_url")
	}

	state := rc.State
	if state == "" {
		state = randomString(16)
	}
	var verifier string
	authURL, err := url.Parse(rc.AuthorizationURL)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization_url: %w", err)
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", rc.ClientID)
	q.Set("redirect_uri", rc.CallbackURL)
	q.Set("state", state)
	if rc.Scope != "" {
		q.Set("scope", rc.Scope)
	}
	if rc.PKCE {
		verifier = randomString(32)
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	authURL.RawQuery = q.Encode()

	// How the code comes back, in order of preference:
	//  1. loopback callback_url: local server + default browser;
	//  2. a login window brui controls (fully automatic, like Bruno): the
	//     native one if available, then a Chromium-based browser;
	//  3. the user pastes the redirect URL.
	results := make(chan callbackResult, 1)
	listening := false
	if isLoopback(rc.CallbackURL) {
		if stop, err := listenForCallback(rc.CallbackURL, results); err == nil {
			defer stop()
			listening = true
		}
	}
	type autoLogin struct {
		name string
		run  func(ctx context.Context) callbackResult
	}
	// Sources are resolved lazily: Chromium is only looked up if the native
	// window is missing or fails to open.
	var sources []func() (autoLogin, bool)
	if !listening {
		sources = append(sources, func() (autoLogin, bool) {
			lw := om.LoginWindow
			if lw == nil || lw.Run == nil {
				return autoLogin{}, false
			}
			return autoLogin{lw.Name, func(ctx context.Context) callbackResult {
				u, err := lw.Run(ctx, authURL.String(), rc.CallbackURL)
				if err != nil {
					return callbackResult{err: err}
				}
				return parseRedirect(u)
			}}, true
		}, func() (autoLogin, bool) {
			if om.FindBrowser == nil {
				return autoLogin{}, false
			}
			exe := om.FindBrowser()
			if exe == "" {
				return autoLogin{}, false
			}
			return autoLogin{filepath.Base(exe), func(ctx context.Context) callbackResult {
				res, err := browserLogin(ctx, exe, authURL.String(), rc.CallbackURL)
				if err != nil {
					return callbackResult{err: err}
				}
				return res
			}}, true
		})
	}
	nextAuto := func() (autoLogin, bool) {
		for len(sources) > 0 {
			src := sources[0]
			sources = sources[1:]
			if a, ok := src(); ok {
				return a, true
			}
		}
		return autoLogin{}, false
	}
	first, haveAuto := nextAuto()
	if prompt == nil && !listening && !haveAuto {
		return nil, errors.New("authorization_code with a non-local callback_url needs the interactive UI or a Chromium-based browser")
	}

	paste := make(chan string, 1)
	base := AuthCodePrompt{AuthURL: authURL.String(), CallbackURL: rc.CallbackURL, Listening: listening, Paste: paste}
	manual := func(fallback string) {
		p := base
		p.BrowserErr = om.OpenBrowser(authURL.String())
		p.Fallback = fallback
		p.NoControllableBrowser = !listening && !haveAuto
		if prompt != nil {
			prompt(p)
		}
	}
	bctx, bcancel := context.WithCancel(ctx)
	defer bcancel() // closes any login window if the flow ends another way
	automatic := false
	startAuto := func(a autoLogin) {
		automatic = true
		go func() { results <- a.run(bctx) }()
		if prompt != nil {
			p := base
			p.Automatic, p.Browser = true, a.name
			prompt(p)
		}
	}
	if haveAuto {
		startAuto(first)
	} else {
		manual("")
	}

	var res callbackResult
	for {
		select {
		case res = <-results:
			if automatic && errors.Is(res.err, ErrBrowserLaunch) {
				if a, ok := nextAuto(); ok {
					startAuto(a)
					continue
				}
				if prompt != nil {
					automatic = false
					manual(res.err.Error())
					continue
				}
			}
		case p := <-paste:
			res = parseRedirect(p)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		break
	}
	if res.err != nil {
		return nil, res.err
	}
	if res.code == "" {
		return nil, errors.New("no authorization code received")
	}
	if res.state != "" && res.state != state {
		return nil, errors.New("state mismatch in authorization response (possible CSRF); try again")
	}

	form := url.Values{
		"grant_type":   {GrantAuthorizationCode},
		"code":         {res.code},
		"redirect_uri": {rc.CallbackURL},
	}
	if rc.PKCE {
		form.Set("code_verifier", verifier)
	}
	return om.requestToken(ctx, rc, rc.AccessTokenURL, form)
}

type callbackResult struct {
	code, state string
	err         error
}

// parseRedirect extracts code/state from a pasted redirect URL; a value
// without query parameters is taken as the bare authorization code.
func parseRedirect(s string) callbackResult {
	s = strings.TrimSpace(s)
	if s == "" {
		return callbackResult{err: errors.New("nothing pasted")}
	}
	query := ""
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		query = s[i+1:]
	} else if strings.Contains(s, "code=") {
		query = s
	}
	if query == "" {
		return callbackResult{code: s}
	}
	// The code may also arrive in the fragment.
	query = strings.ReplaceAll(query, "#", "&")
	q, err := url.ParseQuery(query)
	if err != nil {
		return callbackResult{err: fmt.Errorf("cannot parse pasted URL: %w", err)}
	}
	return resultFromQuery(q)
}

func resultFromQuery(q url.Values) callbackResult {
	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		return callbackResult{err: fmt.Errorf("authorization denied: %s", msg)}
	}
	return callbackResult{code: q.Get("code"), state: q.Get("state")}
}

func isLoopback(callback string) bool {
	u, err := url.Parse(callback)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// listenForCallback serves the loopback callback_url and delivers the first
// authorization response on results. stop shuts the server down.
func listenForCallback(callback string, results chan<- callbackResult) (stop func(), err error) {
	u, err := url.Parse(callback)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, err
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		res := resultFromQuery(r.URL.Query())
		if res.err == nil && res.code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		once.Do(func() { results <- res })
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		msg := "Login complete. You can close this tab and return to brui."
		if res.err != nil {
			msg = "Authorization failed: " + res.err.Error()
		}
		fmt.Fprintf(w, "<!doctype html><title>brui</title><p style=\"font-family:sans-serif\">%s</p>", htmlEscape(msg))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}, nil
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// openBrowser opens u in the user's browser without touching the terminal.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		if b := os.Getenv("BROWSER"); b != "" {
			cmd = exec.Command(b, u)
		} else {
			cmd = exec.Command("xdg-open", u)
		}
	}
	// Stdout/Stderr stay nil (discarded) so the TUI is not corrupted.
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// applyOAuth2 obtains a token for req's effective OAuth2 config (if any) and
// injects it into req, before the HTTP call and the post-response script.
func applyOAuth2(ctx context.Context, req *BrunoRequest, coll *BrunoCollection, opts RunOptions) (string, error) {
	cfg := EffectiveOAuth2(req, coll)
	if cfg == nil {
		if req.AuthType == "oauth2" {
			return "", errors.New("auth mode is oauth2 but the request has no auth:oauth2 block")
		}
		return "", nil
	}
	om := opts.OAuth2
	if om == nil {
		om = NewOAuth2Manager("")
	}
	rc := cfg.Resolve(coll)
	tok, note, err := om.token(ctx, rc, coll.Path, opts.Prompt)
	if err != nil || tok == nil {
		return note, err
	}
	injectToken(req, rc, tok.value(rc.TokenSource))
	return note, nil
}

func injectToken(req *BrunoRequest, rc OAuth2Config, token string) {
	if rc.TokenPlacement == "url" {
		req.Params = append(req.Params, BruParam{Name: rc.TokenQueryKey, Value: token, Enabled: true})
		return
	}
	value := token
	if rc.TokenHeaderPrefix != "" {
		value = rc.TokenHeaderPrefix + " " + token
	}
	headers := req.Headers[:0:0]
	for _, h := range req.Headers {
		if !strings.EqualFold(h.Name, "Authorization") {
			headers = append(headers, h)
		}
	}
	req.Headers = append(headers, BruHeader{Name: "Authorization", Value: value, Enabled: true})
}
