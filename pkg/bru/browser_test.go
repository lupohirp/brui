package bru

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCDP emulates a Chromium DevTools endpoint: /json/list plus a page
// websocket that reports document navigations as Fetch.requestPaused events
// and follows HTTP redirects like a browser would.
type fakeCDP struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	urls        map[string]string // requestId -> url
	fulfilled   string            // URL answered locally by brui
	continued   []string
	fetchOn     bool
	closeOnNav  bool // simulate the user closing the window
	nextRequest int
}

func newFakeCDP(t *testing.T) *fakeCDP {
	f := &fakeCDP{t: t, urls: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"type":"page","webSocketDebuggerUrl":"ws://%s/devtools/page/1"}]`, r.Host)
	})
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Browser":"FakeChrome/1.0"}`)
	})
	mux.HandleFunc("/devtools/page/1", f.serveWS)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCDP) port() string { return f.srv.URL[strings.LastIndex(f.srv.URL, ":")+1:] }

func (f *fakeCDP) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		f.t.Error(err)
		return
	}
	defer conn.Close()
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		wsAccept(r.Header.Get("Sec-WebSocket-Key")))
	rw.Flush()
	br := bufio.NewReader(rw)

	emit := func(u string) {
		f.mu.Lock()
		f.nextRequest++
		id := fmt.Sprintf("req-%d", f.nextRequest)
		f.urls[id] = u
		f.mu.Unlock()
		ev, _ := json.Marshal(map[string]any{"method": "Fetch.requestPaused",
			"params": map[string]any{"requestId": id, "request": map[string]string{"url": u}}})
		_ = writeFrame(conn, wsOpText, ev, false)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for {
		_, op, payload, err := readFrame(br)
		if err != nil || op == wsOpClose {
			return
		}
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(payload, &msg)
		var p struct {
			URL       string `json:"url"`
			RequestID string `json:"requestId"`
			Body      string `json:"body"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		ack, _ := json.Marshal(map[string]any{"id": msg.ID, "result": map[string]any{}})
		_ = writeFrame(conn, wsOpText, ack, false)

		switch msg.Method {
		case "Fetch.enable":
			f.mu.Lock()
			f.fetchOn = true
			f.mu.Unlock()
		case "Page.navigate":
			f.mu.Lock()
			on, closeNow := f.fetchOn, f.closeOnNav
			f.mu.Unlock()
			if !on {
				f.t.Error("navigation started before request interception was enabled")
			}
			if closeNow {
				return // the user closed the window
			}
			emit(p.URL)
		case "Fetch.continueRequest":
			f.mu.Lock()
			u := f.urls[p.RequestID]
			f.continued = append(f.continued, u)
			f.mu.Unlock()
			resp, err := noRedirect.Get(u)
			if err != nil {
				f.t.Error(err)
				return
			}
			resp.Body.Close()
			if loc := resp.Header.Get("Location"); loc != "" {
				emit(loc)
			}
		case "Fetch.fulfillRequest":
			f.mu.Lock()
			f.fulfilled = f.urls[p.RequestID]
			f.mu.Unlock()
		}
	}
}

// fakeBrowser writes a script that behaves like Chromium at startup: it
// publishes DevToolsActivePort in its --user-data-dir and keeps running.
func fakeBrowser(t *testing.T, cdp *fakeCDP) (exe, dirLog string) {
	dir := t.TempDir()
	exe = filepath.Join(dir, "fake-chrome")
	dirLog = filepath.Join(dir, "profiles.log")
	script := `#!/bin/sh
for a in "$@"; do case "$a" in --user-data-dir=*) d="${a#--user-data-dir=}";; esac; done
echo "$d" >> "` + dirLog + `"
printf '%s\n/devtools/browser/fake\n' "` + cdp.port() + `" > "$d/DevToolsActivePort"
exec sleep 60
`
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, dirLog
}

func TestAutomaticBrowserLogin(t *testing.T) {
	idp := newFakeIdP(t)
	cdp := newFakeCDP(t)
	exe, dirLog := fakeBrowser(t, cdp)

	om := testManager()
	om.FindBrowser = func() string { return exe }
	om.OpenBrowser = func(string) error { t.Error("the default browser must not be used"); return nil }
	var prompts []AuthCodePrompt
	prompt := func(p AuthCodePrompt) { prompts = append(prompts, p) }

	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/login-token.html"
	cfg.PKCE = true

	start := time.Now()
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: prompt})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !strings.Contains(res.Response.Body, "authorization_code-token-1") {
		t.Fatalf("token not applied: %s", res.Response.Body)
	}
	if len(prompts) != 1 || !prompts[0].Automatic || prompts[0].Browser != "fake-chrome" {
		t.Fatalf("expected one automatic prompt, got %+v", prompts)
	}
	cdp.mu.Lock()
	fulfilled := cdp.fulfilled
	cdp.mu.Unlock()
	if !strings.HasPrefix(fulfilled, cfg.CallbackURL+"?code=") {
		t.Fatalf("the callback must be answered by brui, not loaded: %q", fulfilled)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("automatic login too slow: %s", time.Since(start))
	}

	// The browser is killed and its throwaway profile removed.
	profile, _ := os.ReadFile(dirLog)
	dir := strings.TrimSpace(string(profile))
	if dir == "" {
		t.Fatal("fake browser was not started")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("temporary profile %s not removed", dir)
	}
}

func TestAutomaticLoginWindowClosed(t *testing.T) {
	idp := newFakeIdP(t)
	cdp := newFakeCDP(t)
	cdp.closeOnNav = true
	exe, _ := fakeBrowser(t, cdp)
	om := testManager()
	om.FindBrowser = func() string { return exe }

	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/cb"
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: func(AuthCodePrompt) {}})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "login window was closed") {
		t.Fatalf("want window-closed error, got %v", res.Err)
	}
}

func TestAutomaticLoginFallsBackToPaste(t *testing.T) {
	idp := newFakeIdP(t)
	om := testManager()
	om.FindBrowser = func() string { return "/nonexistent/chromium" }
	opened := false
	om.OpenBrowser = func(string) error { opened = true; return nil }

	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/cb"
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var kinds []string
	prompt := func(p AuthCodePrompt) {
		if p.Automatic {
			kinds = append(kinds, "automatic")
			return
		}
		kinds = append(kinds, "manual")
		if p.Fallback == "" || !strings.Contains(p.Fallback, "automatic login unavailable") {
			t.Errorf("fallback reason missing: %+v", p)
		}
		resp, err := noRedirect.Get(p.AuthURL)
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
		p.Paste <- resp.Header.Get("Location")
	}
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: prompt})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if strings.Join(kinds, ",") != "automatic,manual" || !opened {
		t.Fatalf("expected automatic attempt then manual fallback, got %v (default browser opened: %v)", kinds, opened)
	}
}

func TestWebSocketFraming(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 65535, 70000} {
		payload := []byte(strings.Repeat("x", n))
		for _, mask := range []bool{true, false} {
			var buf strings.Builder
			if err := writeFrame(&buf, wsOpText, payload, mask); err != nil {
				t.Fatal(err)
			}
			fin, op, got, err := readFrame(strings.NewReader(buf.String()))
			if err != nil || !fin || op != wsOpText || string(got) != string(payload) {
				t.Fatalf("n=%d mask=%v: fin=%v op=%d len=%d err=%v", n, mask, fin, op, len(got), err)
			}
		}
	}
}

func TestSameEndpoint(t *testing.T) {
	cb := "https://mytim.example.it/login-token.html"
	for u, want := range map[string]bool{
		cb + "?code=1&state=2":                       true,
		"https://MYTIM.example.it/login-token.html":  true,
		"https://mytim.example.it/login-token.html/": true,
		"https://mytim.example.it/other.html?code=1": false,
		"http://mytim.example.it/login-token.html":   false,
		"https://evil.example/login-token.html":      false,
	} {
		if got := SameEndpoint(u, cb); got != want {
			t.Errorf("%s: want %v", u, want)
		}
	}
}

// TestMain lets the test binary act as a sandboxed browser (see
// TestAutomaticLoginSandboxedBrowser): it listens on the port given with
// --remote-debugging-port and proxies to the fake CDP server, without
// writing DevToolsActivePort where brui can see it.
func TestMain(m *testing.M) {
	if target := os.Getenv("BRUI_FAKE_SANDBOXED_BROWSER"); target != "" {
		port := ""
		for _, a := range os.Args[1:] {
			if v, ok := strings.CutPrefix(a, "--remote-debugging-port="); ok {
				port = v
			}
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			os.Exit(3)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				os.Exit(0)
			}
			go func() {
				defer c.Close()
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				go io.Copy(up, c)
				io.Copy(c, up)
			}()
		}
	}
	os.Exit(m.Run())
}

func TestAutomaticLoginSandboxedBrowser(t *testing.T) {
	idp := newFakeIdP(t)
	cdp := newFakeCDP(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRUI_FAKE_SANDBOXED_BROWSER", strings.TrimPrefix(cdp.srv.URL, "http://"))
	om := testManager()
	om.FindBrowser = func() string { return self }

	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/cb"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prompt := func(p AuthCodePrompt) {
		if !p.Automatic {
			t.Errorf("fell back to manual login: %s", p.Fallback)
			cancel()
		}
	}
	res := RunWithScripts(ctx, idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: prompt})
	if res.Err != nil || !strings.Contains(res.Response.Body, "authorization_code-token-1") {
		t.Fatalf("login through the probed port failed: %v", res.Err)
	}
}

// fakeLoginWindow acts like the native window: it follows the IdP redirect
// and returns the callback URL without loading it.
func fakeLoginWindow(t *testing.T, calls *int) *LoginWindow {
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &LoginWindow{Name: "macOS WebKit", Run: func(ctx context.Context, authURL, callback string) (string, error) {
		*calls++
		resp, err := noRedirect.Get(authURL)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if !SameEndpoint(loc, callback) {
			t.Errorf("redirect %q does not target the callback %q", loc, callback)
		}
		return loc, nil
	}}
}

func TestNativeLoginWindowPreferred(t *testing.T) {
	idp := newFakeIdP(t)
	om := testManager()
	calls := 0
	om.LoginWindow = fakeLoginWindow(t, &calls)
	om.FindBrowser = func() string { t.Error("Chromium must not be used when the native window works"); return "" }
	om.OpenBrowser = func(string) error { t.Error("default browser must not open"); return nil }
	var prompts []AuthCodePrompt
	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/login-token.html"
	cfg.PKCE = true
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(),
		RunOptions{OAuth2: om, Prompt: func(p AuthCodePrompt) { prompts = append(prompts, p) }})
	if res.Err != nil || !strings.Contains(res.Response.Body, "authorization_code-token-1") {
		t.Fatalf("native login failed: %v", res.Err)
	}
	if calls != 1 || len(prompts) != 1 || !prompts[0].Automatic || prompts[0].Browser != "macOS WebKit" {
		t.Fatalf("calls=%d prompts=%+v", calls, prompts)
	}
}

func TestNativeLoginFallsBackToChromium(t *testing.T) {
	idp := newFakeIdP(t)
	cdp := newFakeCDP(t)
	exe, _ := fakeBrowser(t, cdp)
	om := testManager()
	om.LoginWindow = &LoginWindow{Name: "macOS WebKit", Run: func(context.Context, string, string) (string, error) {
		return "", fmt.Errorf("%w: WebKit unavailable", ErrBrowserLaunch)
	}}
	om.FindBrowser = func() string { return exe }
	var names []string
	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/cb"
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(),
		RunOptions{OAuth2: om, Prompt: func(p AuthCodePrompt) { names = append(names, p.Browser) }})
	if res.Err != nil || strings.Join(names, ",") != "macOS WebKit,fake-chrome" {
		t.Fatalf("err=%v prompts=%v", res.Err, names)
	}
}

func TestNativeLoginWindowClosed(t *testing.T) {
	idp := newFakeIdP(t)
	om := testManager()
	om.LoginWindow = &LoginWindow{Name: "macOS WebKit", Run: func(context.Context, string, string) (string, error) {
		return "", ErrLoginWindowClosed
	}}
	cfg := idp.config(GrantAuthorizationCode)
	cfg.CallbackURL = "https://app.example.com/cb"
	res := RunWithScripts(context.Background(), idp.apiRequest(cfg), idp.coll(), RunOptions{OAuth2: om, Prompt: func(AuthCodePrompt) {}})
	if !errors.Is(res.Err, ErrLoginWindowClosed) {
		t.Fatalf("want ErrLoginWindowClosed, got %v", res.Err)
	}
}
