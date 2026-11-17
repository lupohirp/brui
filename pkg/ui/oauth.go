package ui

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/muesli/termenv"
)

// oauthPromptState is the open "complete the login in your browser" dialog.
type oauthPromptState struct {
	Prompt bru.AuthCodePrompt
	Text   string // pasted redirect URL or code
	Cursor int
	Note   string // feedback line (copied, reopened, ...)
}

// oauthPromptMsg delivers a login prompt from a background run.
type oauthPromptMsg struct {
	prompt bru.AuthCodePrompt
	ch     <-chan bru.AuthCodePrompt
}

// oauthDoneMsg is delivered when a manual token fetch completes.
type oauthDoneMsg struct {
	token *bru.OAuth2Token
	err   error
}

// copyToClipboard is replaceable in tests; OSC 52 works in most terminals,
// including over SSH.
var copyToClipboard = termenv.Copy

// newPromptChannel returns the PromptFunc handed to a background run and the
// command that forwards its prompts to Update. The run closes the channel
// (via the returned done func) when it finishes, ending the listener.
func newPromptChannel(ctx context.Context) (bru.PromptFunc, tea.Cmd, func()) {
	ch := make(chan bru.AuthCodePrompt)
	prompt := func(p bru.AuthCodePrompt) {
		select {
		case ch <- p:
		case <-ctx.Done():
		}
	}
	return prompt, waitForPrompt(ch), func() { close(ch) }
}

func waitForPrompt(ch <-chan bru.AuthCodePrompt) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return oauthPromptMsg{prompt: p, ch: ch}
	}
}

// fetchOAuthToken forces a new token for the selected request (key O).
func (m *Model) fetchOAuthToken() tea.Cmd {
	if m.SelectedRequest == nil {
		return nil
	}
	if bru.EffectiveOAuth2(m.SelectedRequest, m.Coll) == nil {
		m.StatusMsg = "This request does not use OAuth2 (auth mode oauth2, or inherit from an oauth2 collection)"
		return nil
	}
	if m.IsSending {
		m.StatusMsg = "A request is already in flight..."
		return nil
	}
	req := m.SelectedRequest.Clone()
	coll := m.Coll.CloneForExecution()
	om := m.OAuth
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelSend = cancel
	m.IsSending = true
	m.StatusMsg = "Getting OAuth2 token...  [Esc] Cancel"
	prompt, listen, done := newPromptChannel(ctx)
	run := func() tea.Msg {
		defer cancel()
		defer done()
		tok, err := om.Fetch(ctx, req, coll, prompt)
		return oauthDoneMsg{token: tok, err: err}
	}
	return tea.Batch(run, listen)
}

func (m *Model) applyOAuthDone(msg oauthDoneMsg) {
	m.IsSending = false
	m.cancelSend = nil
	m.OAuthPrompt = nil
	switch {
	case msg.err != nil && strings.Contains(msg.err.Error(), context.Canceled.Error()):
		m.StatusMsg = "OAuth2 login cancelled"
	case msg.err != nil:
		m.StatusMsg = fmt.Sprintf("OAuth2 error: %v", msg.err)
	default:
		m.StatusMsg = "OAuth2 token obtained" + expiryText(msg.token, time.Now())
	}
}

func expiryText(t *bru.OAuth2Token, now time.Time) string {
	if t == nil || t.ExpiresAt.IsZero() {
		return ""
	}
	if d := t.ExpiresAt.Sub(now); d > 0 {
		return fmt.Sprintf(" (expires in %s)", d.Round(time.Second))
	}
	return " (expired)"
}

func (m *Model) handleOAuthPromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.OAuthPrompt
	if st.Prompt.Automatic {
		// brui completes the login by itself; only cancel and copy apply.
		switch msg.Type {
		case tea.KeyEsc:
			m.OAuthPrompt = nil
			m.cancelCurrentRequest()
		case tea.KeyCtrlY:
			copyToClipboard(st.Prompt.AuthURL)
			st.Note = "Login URL copied to the clipboard"
		}
		return *m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.OAuthPrompt = nil
		m.cancelCurrentRequest()
	case tea.KeyEnter:
		if strings.TrimSpace(st.Text) == "" {
			st.Note = "Paste the URL you were redirected to (it contains code=...)"
			return *m, nil
		}
		select {
		case st.Prompt.Paste <- st.Text:
		default:
		}
		m.OAuthPrompt = nil
		m.StatusMsg = "Exchanging authorization code for a token...  [Esc] Cancel"
	case tea.KeyCtrlY:
		copyToClipboard(st.Prompt.AuthURL)
		st.Note = "Login URL copied to the clipboard"
	case tea.KeyCtrlO:
		if err := openBrowserFn(st.Prompt.AuthURL); err != nil {
			st.Note = fmt.Sprintf("Could not open the browser: %v (use Ctrl+Y to copy the URL)", err)
		} else {
			st.Note = "Browser opened again"
		}
	case tea.KeyCtrlU:
		st.Text, st.Cursor = "", 0
	case tea.KeyBackspace:
		if st.Cursor > 0 {
			st.Text, st.Cursor = deleteBefore(st.Text, st.Cursor)
		}
	case tea.KeyLeft:
		st.Cursor = prevBoundary(st.Text, st.Cursor)
	case tea.KeyRight:
		st.Cursor = nextBoundary(st.Text, st.Cursor)
	case tea.KeyHome, tea.KeyCtrlA:
		st.Cursor = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		st.Cursor = len(st.Text)
	case tea.KeyRunes, tea.KeySpace:
		ch := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			ch = " "
		}
		ch = strings.NewReplacer("\n", "", "\r", "").Replace(ch) // pasted URLs may carry a newline
		st.Text = st.Text[:st.Cursor] + ch + st.Text[st.Cursor:]
		st.Cursor += len(ch)
	}
	return *m, nil
}

// openBrowserFn reopens the login page; replaceable in tests.
var openBrowserFn = func(u string) error { return bru.NewOAuth2Manager("").OpenBrowser(u) }

func (m Model) renderOAuthPromptModal() string {
	st := m.OAuthPrompt
	t := m.Theme
	bg := lipgloss.Color(t.ModalBg)
	w := m.Width - 10
	if w > 100 {
		w = 100
	}
	if w < 40 {
		w = 40
	}
	innerW := w - 4
	text := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Fg)).Background(bg)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext)).Background(bg)
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Background(bg).Bold(true)
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Warn)).Background(bg)

	box := lipgloss.NewStyle().
		Width(w).
		Background(bg).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.BorderFocus)).
		BorderBackground(bg).
		Padding(0, 1)

	var rows []string
	add := func(s string) { rows = append(rows, s) }
	wrap := func(style lipgloss.Style, s string) {
		for _, l := range strings.Split(ansi.Wordwrap(s, innerW, " /&?"), "\n") {
			add(style.Render(l))
		}
	}

	add(accent.Render("OAuth2 login (authorization_code)"))
	add("")
	if st.Prompt.Automatic {
		wrap(text, fmt.Sprintf("A login window (%s) has opened. Log in there: brui catches the redirect to %s, gets the token and closes the window automatically.",
			st.Prompt.Browser, st.Prompt.CallbackURL))
		add("")
		if st.Note != "" {
			add(warn.Render(ansi.Truncate(st.Note, innerW, "…")))
		} else {
			add(sub.Render("Waiting for the login to complete..."))
		}
		add(sub.Render(ansi.Truncate("Login URL: "+st.Prompt.AuthURL, innerW, "…")))
		add("")
		add(sub.Render("[Ctrl+Y] Copy login URL   [Esc] Cancel"))
		return box.Render(strings.Join(rows, "\n"))
	}
	if st.Prompt.Fallback != "" {
		wrap(warn, "Automatic login failed ("+st.Prompt.Fallback+"); falling back to manual login.")
		add("")
	}
	if st.Prompt.BrowserErr != nil {
		wrap(warn, fmt.Sprintf("Could not open the browser (%v). Press Ctrl+Y to copy the login URL and open it yourself.", st.Prompt.BrowserErr))
	} else {
		wrap(text, "A browser window was opened on the login page. Complete the login there.")
	}
	add("")
	if st.Prompt.Listening {
		wrap(text, "brui is listening on "+st.Prompt.CallbackURL+" and will continue automatically after the login.")
		wrap(sub, "If the browser cannot reach it, paste the URL you were redirected to below.")
	} else {
		wrap(text, "After the login the browser goes to "+st.Prompt.CallbackURL+".")
		wrap(text, "Copy that full URL from the address bar (it contains code=...) and paste it here:")
	}
	add("")

	before, at, after := splitAtCursor(st.Text, st.Cursor)
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("#11111b")).Background(lipgloss.Color(t.Accent)).Render(at)
	input := before + cursor + after
	if ansi.StringWidth(input) > innerW-2 { // keep the cursor end visible
		input = ansi.TruncateLeft(input, ansi.StringWidth(input)-(innerW-3), "…")
	}
	add(lipgloss.NewStyle().Background(lipgloss.Color(t.Surface)).Foreground(lipgloss.Color(t.Fg)).Width(innerW).Render(" " + input))
	if st.Note != "" {
		add(warn.Render(ansi.Truncate(st.Note, innerW, "…")))
	} else {
		add("")
	}
	add(sub.Render(ansi.Truncate("Login URL: "+st.Prompt.AuthURL, innerW, "…")))
	add("")
	add(sub.Render("[Enter] Submit   [Ctrl+Y] Copy login URL   [Ctrl+O] Reopen browser   [Esc] Cancel"))

	if st.Prompt.NoControllableBrowser {
		add("")
		wrap(sub, "Tip: "+browserInstallHint())
	}
	return box.Render(strings.Join(rows, "\n"))
}

// browserInstallHint tells how to enable the automatic, Bruno-like login.
func browserInstallHint() string {
	const tail = " for a fully automatic login (or set BRUI_CHROME to its path)."
	switch runtime.GOOS {
	case "darwin":
		return "install Google Chrome, Edge, Brave or Chromium (brew install --cask chromium)" + tail
	case "linux":
		return "install Chrome or Chromium (e.g. sudo apt install chromium, sudo dnf install chromium)" + tail
	default:
		return "install Chrome, Edge or Chromium" + tail
	}
}

// oauthAuthTabLines renders the Auth tab for requests using OAuth2.
func (m Model) oauthAuthTabLines(req *bru.BrunoRequest) []string {
	cfg := bru.EffectiveOAuth2(req, m.Coll)
	if cfg == nil {
		return nil
	}
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	ok := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Success))
	bad := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Warn))

	mode := "oauth2"
	if req.AuthType != "oauth2" {
		mode = "inherit → collection oauth2"
	}
	field := func(label, value string) string {
		return fmt.Sprintf(" %s %s", sub.Render(fmt.Sprintf("%-15s", label)), highlightVariables(value, m.Theme))
	}
	secret := func(v string) string {
		if v == "" {
			return ""
		}
		if varRegex.MatchString(v) && strings.HasPrefix(v, "{{") && strings.HasSuffix(v, "}}") {
			return v // a variable reference is not the secret itself
		}
		return "••••••••"
	}

	lines := []string{fmt.Sprintf(" Auth Mode: [%s]", mode)}
	lines = append(lines, field("Grant", cfg.GrantType))
	if cfg.GrantType == bru.GrantAuthorizationCode {
		lines = append(lines, field("Authorize URL", cfg.AuthorizationURL), field("Callback URL", cfg.CallbackURL))
	}
	lines = append(lines, field("Token URL", cfg.AccessTokenURL), field("Client ID", cfg.ClientID))
	if cfg.ClientSecret != "" {
		lines = append(lines, field("Client Secret", secret(cfg.ClientSecret)))
	}
	if cfg.GrantType == bru.GrantPassword {
		lines = append(lines, field("Username", cfg.Username), field("Password", secret(cfg.Password)))
	}
	if cfg.Scope != "" {
		lines = append(lines, field("Scope", cfg.Scope))
	}
	flags := fmt.Sprintf("pkce=%v  auto_fetch=%v  auto_refresh=%v", cfg.PKCE, cfg.AutoFetchToken, cfg.AutoRefreshToken)
	lines = append(lines, field("Options", flags))

	tok, _ := m.OAuth.Cached(req, m.Coll)
	now := time.Now()
	var status string
	switch {
	case tok == nil:
		status = bad.Render("none") + sub.Render("  — fetched on send (auto_fetch) or with [O]")
		if !cfg.AutoFetchToken {
			status = bad.Render("none") + sub.Render("  — press [O] to get one")
		}
	case tok.Valid(now):
		status = ok.Render("valid"+expiryText(tok, now)) + sub.Render(fmt.Sprintf("  obtained %s", tok.ObtainedAt.Format("15:04:05")))
	default:
		status = bad.Render("expired")
		if tok.RefreshToken != "" {
			status += sub.Render("  (refresh token available)")
		}
	}
	lines = append(lines, "", fmt.Sprintf(" %s %s", sub.Render(fmt.Sprintf("%-15s", "Token")), status))
	lines = append(lines, sub.Render(" [O] Get a new token (replaces the cached one)"))
	return lines
}
