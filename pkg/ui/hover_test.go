package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func hoverModel() Model {
	req := &bru.BrunoRequest{
		ID: "r", Name: "R", Method: bru.MethodGet,
		URL: "{{baseUrl}}/users/{{missing}}",
		Headers: []bru.BruHeader{
			{Name: "Authorization", Value: "Bearer {{token}}", Enabled: true},
			{Name: "X-Request-Id", Value: "{{$uuid}}", Enabled: true},
		},
	}
	coll := &bru.BrunoCollection{
		Requests:          []*bru.BrunoRequest{req},
		Environments:      []*bru.BrunoEnvironment{{Name: "Dev", Variables: map[string]string{"baseUrl": "https://dev.example.com"}}},
		ActiveEnvironment: "Dev",
		RuntimeVariables:  map[string]any{"token": "abc123"},
	}
	m := NewModel(coll)
	m.Width, m.Height = 140, 40
	return m
}

// cellOf returns the screen cell of the first occurrence of text.
func cellOf(t *testing.T, m Model, text string) (x, y int) {
	t.Helper()
	for y, l := range strings.Split(ansi.Strip(m.baseView()), "\n") {
		if i := strings.Index(l, text); i >= 0 {
			return ansi.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("%q not on screen", text)
	return 0, 0
}

func hover(m Model, x, y int) Model {
	nm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	return nm.(Model)
}

func TestHoverShowsVariableValue(t *testing.T) {
	m := hoverModel()
	cases := []struct {
		target, name, value, source string
		found                       bool
	}{
		{"{{baseUrl}}", "baseUrl", "https://dev.example.com", "env: Dev", true},
		{"{{token}}", "token", "abc123", "runtime", true},
		{"{{$uuid}}", "$uuid", "generated when the request is sent", "dynamic", true},
		{"{{missing}}", "missing", "", "", false},
	}
	for _, c := range cases {
		x, y := cellOf(t, m, c.target)
		m = hover(m, x+3, y) // somewhere inside the braces
		h := m.Hover
		if h == nil || h.Name != c.name || h.Found != c.found || h.Value != c.value || h.Source != c.source {
			t.Fatalf("%s: got %+v", c.target, h)
		}
		view := ansi.Strip(m.View())
		want := c.value
		if !c.found {
			want = "undefined"
		}
		if !strings.Contains(view, want) {
			t.Errorf("%s: tooltip does not show %q", c.target, want)
		}
	}

	// Moving off the variable or pressing a key hides the tooltip.
	x, y := cellOf(t, m, "{{baseUrl}}")
	m = hover(m, x+2, y)
	m = hover(m, x-2, y)
	if m.Hover != nil {
		t.Error("tooltip should disappear off the variable")
	}
	m = hover(m, x+2, y)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if nm.(Model).Hover != nil {
		t.Error("key press should hide the tooltip")
	}
}

// Hovering must never act like a click.
func TestHoverDoesNotClick(t *testing.T) {
	m := hoverModel()
	before := m.ActiveRespTab
	_, _, _, reqH, _ := m.layoutDimensions()
	sidebarW, _, _, _, _ := m.layoutDimensions()
	for _, tr := range m.respTabRanges() {
		m = hover(m, sidebarW+1+tr.start, 5+reqH+1)
	}
	if m.ActiveRespTab != before || m.IsSending || m.IsEditingURL {
		t.Errorf("motion triggered an action: tab=%s sending=%v editingURL=%v", m.ActiveRespTab, m.IsSending, m.IsEditingURL)
	}
}
