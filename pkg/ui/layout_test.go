package ui

import (
	"strings"
	"testing"

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
