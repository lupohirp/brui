package bru

import (
	"context"
	"encoding/base64"
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
	"strings"
	"time"
)

// Automatic authorization_code login, the way Bruno does it: brui starts a
// Chromium-based browser it controls through the Chrome DevTools Protocol,
// opens the login page, intercepts the navigation to callback_url before the
// page loads, takes the code and closes the window.

// ErrNoControllableBrowser means no Chrome/Chromium/Edge/Brave was found.
var ErrNoControllableBrowser = errors.New("no Chromium-based browser found for automatic login")

// ErrBrowserLaunch marks failures to start or attach to a login window
// (native or browser); the flow then tries the next method, finally paste.
var ErrBrowserLaunch = errors.New("automatic login unavailable")

// ErrLoginWindowClosed is returned when the user closes the login window.
var ErrLoginWindowClosed = errors.New("the login window was closed before the login completed")

// FindControllableBrowser returns the path of a browser brui can drive via
// the DevTools protocol, or "" if none is installed. BRUI_CHROME overrides it.
func FindControllableBrowser() string {
	if p := os.Getenv("BRUI_CHROME"); p != "" {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
		return ""
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		apps := []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Chromium.app/Contents/MacOS/Chromium",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Vivaldi.app/Contents/MacOS/Vivaldi",
			"Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
		roots := []string{"/Applications"}
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		for _, root := range roots {
			for _, app := range apps {
				candidates = append(candidates, filepath.Join(root, app))
			}
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`))
		}
	default:
		candidates = []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser", "brave",
			"vivaldi-stable", "vivaldi", "/snap/bin/chromium",
		}
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

// controlledBrowser is a running browser instance with a private profile.
type controlledBrowser struct {
	cmd    *exec.Cmd
	dir    string
	port   string
	exited chan struct{}
}

func (b *controlledBrowser) close() {
	if b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
	<-b.exited
	_ = os.RemoveAll(b.dir)
}

// launchControlledBrowser starts exe with remote debugging on a random port
// and a throwaway profile, and waits for its DevTools endpoint.
func launchControlledBrowser(ctx context.Context, exe string) (*controlledBrowser, error) {
	dir, err := os.MkdirTemp("", "brui-login-*")
	if err != nil {
		return nil, err
	}
	// The port is chosen here rather than with --remote-debugging-port=0:
	// sandboxed builds (snap Chromium on Ubuntu) have a private /tmp, so the
	// DevToolsActivePort file they write is not visible to brui.
	port, err := freeLocalPort()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.Command(exe,
		"--user-data-dir="+dir,
		"--remote-debugging-port="+port,
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic", // no keyring prompt on a fresh profile
		"--disable-sync",
		"--app=about:blank", // a plain window without address bar, like Bruno's
	)
	// Stdout/Stderr stay nil (discarded) so the TUI is not corrupted.
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("cannot start %s: %w", filepath.Base(exe), err)
	}
	b := &controlledBrowser{cmd: cmd, dir: dir, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(b.exited) }()

	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	probe := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			if p, _, ok := strings.Cut(string(data), "\n"); ok && strings.TrimSpace(p) != "" {
				b.port = strings.TrimSpace(p)
				return b, nil
			}
		}
		if resp, err := probe.Get("http://127.0.0.1:" + port + "/json/version"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				b.port = port
				return b, nil
			}
		}
		select {
		case <-tick.C:
		case <-b.exited:
			os.RemoveAll(dir)
			return nil, fmt.Errorf("%s exited before opening the login window", filepath.Base(exe))
		case <-deadline.C:
			b.close()
			return nil, fmt.Errorf("%s did not start its DevTools endpoint", filepath.Base(exe))
		case <-ctx.Done():
			b.close()
			return nil, ctx.Err()
		}
	}
}

func freeLocalPort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	return port, err
}

// pageWebSocketURL returns the DevTools websocket of the browser's page.
func (b *controlledBrowser) pageWebSocketURL(ctx context.Context) (string, error) {
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+b.port+"/json/list", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" && t.WebSocketDebuggerURL != "" {
					return t.WebSocketDebuggerURL, nil
				}
			}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", errors.New("the browser did not expose a page to control")
}

// SameEndpoint reports whether u points at callback (same scheme, host and
// path; query and fragment ignored).
func SameEndpoint(u, callback string) bool {
	a, err1 := url.Parse(u)
	b, err2 := url.Parse(callback)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) &&
		strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/")
}

const loginDoneHTML = `<!doctype html><title>brui</title><body style="font-family:sans-serif;padding:2em">` +
	`<h3>Login complete</h3><p>You can return to brui.</p></body>`

// cdpLogin drives the page at wsURL through the login: it intercepts every
// document request, lets them through until one targets callback, answers
// that one locally (so the real callback page never sees the code) and
// returns the authorization response it carried.
func cdpLogin(ctx context.Context, wsURL, authURL, callback string) (callbackResult, error) {
	ws, err := wsDial(ctx, wsURL)
	if err != nil {
		return callbackResult{}, fmt.Errorf("cannot connect to the browser: %w", err)
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()

	id := 0
	send := func(method string, params any) error {
		id++
		msg, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		return ws.WriteText(msg)
	}
	if err := send("Fetch.enable", map[string]any{
		"patterns": []map[string]string{{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}},
	}); err != nil {
		return callbackResult{}, err
	}
	if err := send("Page.navigate", map[string]any{"url": authURL}); err != nil {
		return callbackResult{}, err
	}

	for {
		raw, err := ws.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return callbackResult{}, ctx.Err()
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "reset") {
				return callbackResult{}, ErrLoginWindowClosed
			}
			return callbackResult{}, err
		}
		var ev struct {
			Method string `json:"method"`
			Params struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		switch ev.Method {
		case "Fetch.requestPaused":
			if !SameEndpoint(ev.Params.Request.URL, callback) {
				_ = send("Fetch.continueRequest", map[string]any{"requestId": ev.Params.RequestID})
				continue
			}
			_ = send("Fetch.fulfillRequest", map[string]any{
				"requestId":       ev.Params.RequestID,
				"responseCode":    200,
				"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
				"body":            base64.StdEncoding.EncodeToString([]byte(loginDoneHTML)),
			})
			return parseRedirect(ev.Params.Request.URL), nil
		case "Inspector.detached", "Target.targetDestroyed":
			return callbackResult{}, ErrLoginWindowClosed
		}
	}
}

// browserLogin runs the whole automatic login in a controlled browser.
func browserLogin(ctx context.Context, exe, authURL, callback string) (callbackResult, error) {
	b, err := launchControlledBrowser(ctx, exe)
	if err != nil {
		if ctx.Err() != nil {
			return callbackResult{}, ctx.Err()
		}
		return callbackResult{}, fmt.Errorf("%w: %v", ErrBrowserLaunch, err)
	}
	defer b.close()
	wsURL, err := b.pageWebSocketURL(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return callbackResult{}, ctx.Err()
		}
		return callbackResult{}, fmt.Errorf("%w: %v", ErrBrowserLaunch, err)
	}
	// The user closing the window kills the page connection; also watch the
	// process so we notice immediately.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		select {
		case <-b.exited:
			cancel(ErrLoginWindowClosed)
		case <-ctx.Done():
		}
	}()
	res, err := cdpLogin(ctx, wsURL, authURL, callback)
	if err != nil && errors.Is(context.Cause(ctx), ErrLoginWindowClosed) {
		return callbackResult{}, ErrLoginWindowClosed
	}
	return res, err
}
