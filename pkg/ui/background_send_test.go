package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lupohirp/brui/pkg/bru"
)

func TestSendRunsInBackgroundAndEscCancels(t *testing.T) {
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()

	req := &bru.BrunoRequest{ID: "r", Name: "Slow", Method: bru.MethodGet, URL: srv.URL,
		PreRequestScript: `bru.setVar("fromScript", "1")`}
	m := NewModel(&bru.BrunoCollection{Requests: []*bru.BrunoRequest{req}, RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 120, 40

	cmd := m.sendCurrentRequest()
	if cmd == nil || !m.IsSending {
		t.Fatal("request should be in flight")
	}
	done := make(chan tea.Msg, 1)
	go func() { // run only the request command of the batch, like Bubble Tea would
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			msg = b[0]()
		}
		done <- msg
	}()

	if !strings.Contains(m.View(), "[Esc] Cancel") {
		t.Error("help should offer [Esc] Cancel while sending")
	}
	select { // the pre-request script has run once the server sees the request
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the server")
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)

	select {
	case msg := <-done:
		nm, _ = m.Update(msg)
		m = nm.(Model)
	case <-time.After(3 * time.Second):
		t.Fatal("Esc did not cancel the request")
	}
	if m.IsSending || m.StatusMsg != "Request cancelled" {
		t.Errorf("state after cancel: sending=%v status=%q", m.IsSending, m.StatusMsg)
	}
	if m.Coll.RuntimeVariables["fromScript"] != "1" {
		t.Error("runtime vars set before cancellation should be merged")
	}
}

func TestUTF8Editing(t *testing.T) {
	req := &bru.BrunoRequest{ID: "r", Name: "R", Method: bru.MethodGet, URL: "https://x.io/café"}
	m := NewModel(&bru.BrunoCollection{Requests: []*bru.BrunoRequest{req}, RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 120, 40
	key := func(k tea.KeyType) {
		nm, _ := m.Update(tea.KeyMsg{Type: k})
		m = nm.(Model)
	}
	typeText := func(s string) {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
		m = nm.(Model)
	}

	// URL editor: Backspace removes the whole "é", arrows step over it.
	m.IsEditingURL, m.URLEditText, m.URLCursor = true, req.URL, len(req.URL)
	key(tea.KeyBackspace)
	if m.URLEditText != "https://x.io/caf" || !utf8.ValidString(m.URLEditText) {
		t.Fatalf("backspace: %q", m.URLEditText)
	}
	typeText("è")
	key(tea.KeyLeft)
	typeText("ü")
	key(tea.KeyRight)
	key(tea.KeyLeft)
	key(tea.KeyDelete)
	if m.URLEditText != "https://x.io/cafü" {
		t.Fatalf("arrows/delete: %q", m.URLEditText)
	}
	if !utf8.ValidString(m.View()) {
		t.Fatal("URL editor rendered invalid UTF-8")
	}
	key(tea.KeyEsc)

	// Content editor: up/down keep the column in characters.
	m.ActiveTab = "body"
	m.IsEditingContent, m.ContentEditText = true, "àèì\nabcdef"
	m.ContentCursor = len("àè") // column 2 on the first line
	key(tea.KeyDown)
	if m.ContentCursor != len("àèì\nab") {
		t.Fatalf("down: cursor %d", m.ContentCursor)
	}
	key(tea.KeyUp)
	if m.ContentCursor != len("àè") {
		t.Fatalf("up: cursor %d", m.ContentCursor)
	}
	key(tea.KeyBackspace)
	if m.ContentEditText != "àì\nabcdef" || !utf8.ValidString(m.View()) {
		t.Fatalf("content backspace: %q", m.ContentEditText)
	}
}
