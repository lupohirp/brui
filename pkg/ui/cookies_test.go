package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func TestCookiesTab(t *testing.T) {
	old := bru.ActiveCookieJar()
	bru.UseCookieJar(bru.NewCookieJar(""))
	t.Cleanup(func() { bru.UseCookieJar(old) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "abc123", Path: "/", HttpOnly: true, MaxAge: 600})
	}))
	defer srv.Close()
	coll := &bru.BrunoCollection{RuntimeVariables: map[string]any{}, Requests: []*bru.BrunoRequest{{ID: "r", Name: "R", Method: bru.MethodGet, URL: srv.URL + "/login"}}}
	m := NewModel(coll)
	m.Width, m.Height = 150, 40
	m.SelectedRequest = coll.Requests[0]
	m.executeCurrentRequest()

	m.setRespTab("cookies")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Cookies (1)", "Set by the last response (1)", "JSESSIONID = abc123", "HttpOnly", "Cookie jar (1)", "127.0.0.1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = nm.(Model)
	if len(bru.ActiveCookieJar().List()) != 0 || !strings.Contains(m.StatusMsg, "cookies deleted") {
		t.Fatalf("Ctrl+L must clear the jar: %q", m.StatusMsg)
	}
}
