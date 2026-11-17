package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func TestConsoleTab(t *testing.T) {
	req := &bru.BrunoRequest{ID: "r", Name: "R", Method: bru.MethodGet, URL: "https://x.io"}
	m := NewModel(&bru.BrunoCollection{Requests: []*bru.BrunoRequest{req}, RuntimeVariables: map[string]any{}})
	m.Width, m.Height = 140, 40

	// A failed pre-request: no response, but logs must be reachable.
	m.applyRunResult(&bru.RunResult{
		Err: errBoom,
		Logs: []bru.ConsoleLogEntry{
			{Timestamp: "10:00:00", Type: "info", Message: "{\n  \"a\": 1\n}"},
			{Timestamp: "10:00:01", Type: "error", Message: "request script error: boom"},
		},
	}, "GET R")
	if !strings.Contains(m.StatusMsg, "Console") {
		t.Errorf("status should point to the console: %q", m.StatusMsg)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Console (2, 1!)") {
		t.Error("console tab should show entry and error counts")
	}

	// Mouse click on the Console tab label.
	_, _, _, reqH, _ := m.layoutDimensions()
	sidebarW, _, _, _, _ := m.layoutDimensions()
	var consoleX int
	for _, tr := range m.respTabRanges() {
		if tr.id == "console" {
			consoleX = sidebarW + 1 + tr.start
		}
	}
	nm, _ := m.Update(tea.MouseMsg{X: consoleX, Y: 5 + reqH + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(Model)
	if m.ActiveRespTab != "console" {
		t.Fatalf("click did not select console, tab=%q", m.ActiveRespTab)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"── ", "GET R", `"a": 1`, "request script error: boom", "[Ctrl+L] Clear Console"} {
		if !strings.Contains(view, want) {
			t.Errorf("console view missing %q", want)
		}
	}

	// Tab cycles body -> headers -> console -> body.
	m.ActiveRespTab = "body"
	for _, want := range []string{"headers", "console", "body"} {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = nm.(Model)
		if m.ActiveRespTab != want {
			t.Fatalf("tab cycle: want %s, got %s", want, m.ActiveRespTab)
		}
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = nm.(Model)
	if len(m.ScriptLogs) != 0 {
		t.Error("ctrl+l should clear the console")
	}
}

type boomErr struct{}

func (boomErr) Error() string { return "pre-request script: boom" }

var errBoom = boomErr{}
