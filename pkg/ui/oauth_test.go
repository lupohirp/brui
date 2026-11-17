package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// fakeAuthServer issues a token for any authorization code and echoes the
// Authorization header on /api.
func fakeAuthServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=abc&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-" + r.PostForm.Get("grant_type"), "expires_in": 600})
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// startBatch runs a tea.Batch like Bubble Tea: each command in its own
// goroutine, messages delivered on the returned channel.
func startBatch(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 8)
	msg := cmd()
	cmds := []tea.Cmd{func() tea.Msg { return msg }}
	if b, ok := msg.(tea.BatchMsg); ok {
		cmds = b
	}
	for _, c := range cmds {
		go func(c tea.Cmd) {
			if m := c(); m != nil {
				out <- m
			}
		}(c)
	}
	return out
}

func next(t *testing.T, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

func TestOAuthLoginDialogFlow(t *testing.T) {
	srv := fakeAuthServer(t)
	cfg := &bru.OAuth2Config{
		GrantType: bru.GrantAuthorizationCode, AuthorizationURL: srv.URL + "/authorize",
		AccessTokenURL: srv.URL + "/token", CallbackURL: "https://app.example.com/login-token.html",
		ClientID: "c", PKCE: true, AutoFetchToken: true,
	}
	req := &bru.BrunoRequest{ID: "r", Name: "WEB", Method: bru.MethodGet, URL: srv.URL + "/api", AuthType: "oauth2", AuthOAuth2: cfg}
	m := NewModel(&bru.BrunoCollection{Requests: []*bru.BrunoRequest{req}, RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 140, 40

	var authURL string
	m.OAuth.OpenBrowser = func(u string) error { authURL = u; return nil }
	m.OAuth.FindBrowser = func() string { return "" } // exercise the paste flow
	var copied string
	origCopy := copyToClipboard
	copyToClipboard = func(s string) { copied = s }
	t.Cleanup(func() { copyToClipboard = origCopy })

	msgs := startBatch(m.sendCurrentRequest())
	promptMsg, ok := next(t, msgs).(oauthPromptMsg)
	if !ok {
		t.Fatal("expected the login prompt")
	}
	nm, _ := m.Update(promptMsg)
	m = nm.(Model)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "OAuth2 login") || !strings.Contains(view, "login-token.html") {
		t.Fatalf("login dialog not shown:\n%s", view)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	m = nm.(Model)
	if copied != authURL || !strings.Contains(copied, "code_challenge=") {
		t.Errorf("Ctrl+Y should copy the login URL, got %q", copied)
	}

	// The user logs in, then pastes the redirect URL (bracketed paste).
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(resp.Header.Get("Location")), Paste: true})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.OAuthPrompt != nil {
		t.Fatal("dialog should close after submitting")
	}

	done, ok := next(t, msgs).(requestDoneMsg)
	if !ok {
		t.Fatal("expected the request to complete")
	}
	nm, _ = m.Update(done)
	m = nm.(Model)
	if m.LastResponse == nil || m.LastResponse.Body != "Bearer tok-authorization_code" || !strings.Contains(m.StatusMsg, "OAuth2: new token") {
		t.Fatalf("request not authorized: status=%q resp=%+v", m.StatusMsg, m.LastResponse)
	}

	m.ActiveTab = "auth"
	if view := ansi.Strip(m.View()); !strings.Contains(view, "valid (expires in") || !strings.Contains(view, "authorization_code") {
		t.Errorf("Auth tab should show the valid token:\n%s", view)
	}

	// O forces a new login; Esc in the dialog cancels it.
	msgs = startBatch(m.fetchOAuthToken())
	promptMsg = next(t, msgs).(oauthPromptMsg)
	nm, _ = m.Update(promptMsg)
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	nm, _ = m.Update(next(t, msgs))
	m = nm.(Model)
	if m.IsSending || m.OAuthPrompt != nil || m.StatusMsg != "OAuth2 login cancelled" {
		t.Fatalf("cancel: sending=%v prompt=%v status=%q", m.IsSending, m.OAuthPrompt != nil, m.StatusMsg)
	}
}

func TestOAuthAutomaticDialog(t *testing.T) {
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 140, 40
	nm, _ := m.Update(oauthPromptMsg{prompt: bru.AuthCodePrompt{Automatic: true, Browser: "chromium",
		CallbackURL: "https://app.example.com/login-token.html", AuthURL: "https://idp/authorize", Paste: make(chan string, 1)},
		ch: make(chan bru.AuthCodePrompt)})
	m = nm.(Model)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "login window (chromium) has opened") || strings.Contains(view, "paste it here") {
		t.Fatalf("automatic dialog wrong:\n%s", view)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.OAuthPrompt == nil || m.OAuthPrompt.Text != "" {
		t.Fatal("typing/Enter must be ignored while the automatic login runs")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.(Model).OAuthPrompt != nil {
		t.Fatal("Esc should close the dialog")
	}
}

func TestOAuthFallbackDialogShowsReasonAndHint(t *testing.T) {
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 140, 40
	nm, _ := m.Update(oauthPromptMsg{prompt: bru.AuthCodePrompt{CallbackURL: "https://app.example.com/cb", AuthURL: "https://idp/authorize",
		Fallback: "automatic login unavailable: chromium exited", Paste: make(chan string, 1)}, ch: make(chan bru.AuthCodePrompt)})
	m = nm.(Model)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Automatic login failed") || !strings.Contains(view, "paste it here") || strings.Contains(view, "Tip:") {
		t.Fatalf("fallback dialog wrong:\n%s", view)
	}

	m.OAuthPrompt.Prompt.Fallback = ""
	m.OAuthPrompt.Prompt.NoControllableBrowser = true
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "Tip: install") || !strings.Contains(view, "BRUI_CHROME") {
		t.Fatalf("missing install hint:\n%s", view)
	}
}
