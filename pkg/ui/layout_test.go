package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lupohirp/brui/pkg/bru"
)

func TestDogSprite(t *testing.T) {
	E := lipgloss.Color("#c97a2b")
	Y := lipgloss.Color("#f4aa42")
	K := lipgloss.Color("#11111b")
	R := lipgloss.Color("#ea5a47")

	dog1 := lipgloss.NewStyle().Foreground(E).Render("▄█") +
		lipgloss.NewStyle().Foreground(Y).Render("█████") +
		lipgloss.NewStyle().Foreground(E).Render("█▄")

	dog2 := lipgloss.NewStyle().Foreground(E).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(K).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(Y).Render("█") +
		lipgloss.NewStyle().Foreground(Y).Background(K).Render("▀") +
		lipgloss.NewStyle().Foreground(Y).Render("█") +
		lipgloss.NewStyle().Foreground(K).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Render("▀")

	dog3 := "  " +
		lipgloss.NewStyle().Foreground(Y).Render("▀▀") +
		lipgloss.NewStyle().Foreground(R).Render("█") +
		lipgloss.NewStyle().Foreground(Y).Render("▀▀") +
		"  "

	w1 := lipgloss.Width(dog1)
	w2 := lipgloss.Width(dog2)
	w3 := lipgloss.Width(dog3)

	if w1 != 9 || w2 != 9 || w3 != 9 {
		t.Fatalf("Dog sprite widths mismatch: w1=%d w2=%d w3=%d", w1, w2, w3)
	}
}

func TestModelViewStrictDimensions(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "APIGEE",
		ActiveEnvironment: "Local",
		Environments: []*bru.BrunoEnvironment{
			{
				Name: "Local",
				Variables: map[string]string{
					"FQDN": "http://localhost:8080",
					"Path": "api/v1",
				},
			},
		},
		CollectionHeaders: []bru.BruHeader{
			{Name: "X-Collection-Id", Value: "12345", Enabled: true},
		},
		Folders: []*bru.BrunoFolder{
			{
				ID:   "f1",
				Name: "Auth",
				Requests: []*bru.BrunoRequest{
					{
						ID:       "r1",
						Name:     "Login OTP",
						Method:   bru.MethodPost,
						URL:      "{{FQDN}}/{{Path}}/auth/otp",
						Headers:  []bru.BruHeader{{Name: "Content-Type", Value: "application/json", Enabled: true}},
						BodyType: "json",
						BodyContent: `{"phone": "123456789"}`,
					},
					{
						ID:     "r2",
						Name:   "Reset Password",
						Method: bru.MethodPut,
						URL:    "{{FQDN}}/{{Path}}/reset/password/send/otp",
					},
				},
			},
		},
	}

	sizes := [][2]int{
		{80, 24},
		{100, 30},
		{120, 35},
		{160, 45},
	}

	for _, size := range sizes {
		w := size[0]
		h := size[1]

		m := NewModel(coll)
		m.Width = w
		m.Height = h

		view := m.View()
		renderedW := lipgloss.Width(view)
		renderedH := lipgloss.Height(view)

		if renderedW != w {
			t.Errorf("At %dx%d, rendered width was %d", w, h, renderedW)
		}
		if renderedH != h {
			t.Errorf("At %dx%d, rendered height was %d", w, h, renderedH)
		}

		lines := strings.Split(view, "\n")
		if len(lines) != h {
			t.Errorf("At %dx%d, split lines count was %d", w, h, len(lines))
		}
		for lineIdx, line := range lines {
			lineWidth := lipgloss.Width(line)
			if lineWidth > w {
				t.Errorf("At %dx%d, line %d width %d exceeded %d: %q", w, h, lineIdx, lineWidth, w, line)
			}
		}
	}
}

func TestModelAutocomplete(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "TestColl",
		ActiveEnvironment: "Dev",
		Environments: []*bru.BrunoEnvironment{
			{
				Name: "Dev",
				Variables: map[string]string{
					"FQDN-Reset": "https://auth.example.com",
					"Path-Reset": "v2",
				},
			},
		},
	}
	m := NewModel(coll)
	m.IsEditingURL = true
	m.URLEditText = "{{FQ"
	m.URLCursor = len(m.URLEditText)
	m.updateAutocomplete()

	if len(m.ACCandidates) == 0 {
		t.Fatalf("Expected autocomplete candidates for '{{FQ', got none")
	}
	if m.ACCandidates[0].Name != "FQDN-Reset" {
		t.Errorf("Expected candidate FQDN-Reset, got %s", m.ACCandidates[0].Name)
	}

	// Test accept
	m.acceptAutocomplete()
	if m.URLEditText != "{{FQDN-Reset}}" {
		t.Errorf("Expected accepted URL to be '{{FQDN-Reset}}', got '%s'", m.URLEditText)
	}
}

func TestMouseInteractions(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "APIGEE",
		ActiveEnvironment: "Local",
		Environments: []*bru.BrunoEnvironment{
			{Name: "Local", Variables: map[string]string{"A": "1"}},
			{Name: "Staging", Variables: map[string]string{"A": "2"}},
		},
		Folders: []*bru.BrunoFolder{
			{
				ID:   "f1",
				Name: "Auth",
				Requests: []*bru.BrunoRequest{
					{ID: "r1", Name: "Login", Method: bru.MethodPost, URL: "http://api.com/login"},
					{ID: "r2", Name: "Reset", Method: bru.MethodPut, URL: "http://api.com/reset"},
				},
			},
		},
	}

	m := NewModel(coll)
	m.Width = 100
	m.Height = 30

	// 1. Click on folder to collapse
	folderClick := tea.MouseMsg{
		X:      5,
		Y:      6, // Line 6 is folder row
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ := m.Update(folderClick)
	m = newM.(Model)
	if m.ExpandedFolders["f1"] {
		t.Errorf("Expected folder f1 to be collapsed after click, but was expanded")
	}

	// Click again to expand
	newM, _ = m.Update(folderClick)
	m = newM.(Model)
	if !m.ExpandedFolders["f1"] {
		t.Errorf("Expected folder f1 to be expanded after second click")
	}

	// 2. Click on second request (row 2 -> Y=8)
	req2Click := tea.MouseMsg{
		X:      5,
		Y:      8,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(req2Click)
	m = newM.(Model)
	if m.SelectedRequest == nil || m.SelectedRequest.ID != "r2" {
		t.Errorf("Expected selected request to be r2, got %v", m.SelectedRequest)
	}

	// 3. Click on Method badge in Request pane (X: sidebarW+2 = 32, Y: 6)
	methodClick := tea.MouseMsg{
		X:      32,
		Y:      6,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	curMethod := m.SelectedRequest.Method
	newM, _ = m.Update(methodClick)
	m = newM.(Model)
	if m.SelectedRequest.Method == curMethod {
		t.Errorf("Expected method to cycle after clicking method badge")
	}

	// 4. Click on URL to edit (X: 45, Y: 6)
	urlClick := tea.MouseMsg{
		X:      45,
		Y:      6,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(urlClick)
	m = newM.(Model)
	if !m.IsEditingURL {
		t.Errorf("Expected IsEditingURL to be true after clicking URL")
	}

	// 5. Click on Tab 3 (Body) (X: sidebarW + 35 = 65, Y: 7)
	tabClick := tea.MouseMsg{
		X:      65,
		Y:      7,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(tabClick)
	m = newM.(Model)
	if m.ActiveTab != "body" {
		t.Errorf("Expected ActiveTab to be 'body' after click, got '%s'", m.ActiveTab)
	}

	// 6. Mouse wheel scroll on sidebar
	sidebarWheelDown := tea.MouseMsg{
		X:      10,
		Y:      10,
		Button: tea.MouseButtonWheelDown,
	}
	newM, _ = m.Update(sidebarWheelDown)
	m = newM.(Model)

	// 7. Click on Env button in status bar (X: 35, Y: 29)
	envClick := tea.MouseMsg{
		X:      35,
		Y:      29,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	prevEnv := m.Coll.ActiveEnvironment
	newM, _ = m.Update(envClick)
	m = newM.(Model)
	if m.Coll.ActiveEnvironment == prevEnv {
		t.Errorf("Expected ActiveEnvironment to cycle after clicking Env in status bar")
	}
}
