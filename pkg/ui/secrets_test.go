package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func secretsModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	envDir := filepath.Join(dir, "environments")
	os.MkdirAll(envDir, 0o755)
	envFile := filepath.Join(envDir, "Dev.bru")
	os.WriteFile(envFile, []byte("vars {\n  host: https://api.example.com\n}\nvars:secret [\n  PASSWORD\n]\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Req.bru"), []byte("meta {\n  name: Req\n  type: http\n  seq: 1\n}\n\nget {\n  url: {{host}}/login?p={{PASSWORD}}\n  body: none\n  auth: none\n}\n"), 0o644)
	coll, err := bru.LoadCollection(dir)
	if err != nil {
		t.Fatal(err)
	}
	coll.Secrets = bru.NewSecretStore(filepath.Join(t.TempDir(), "secrets.json"), "")
	coll.ActiveEnvironment = "Dev"
	m := NewModel(coll)
	m.Width, m.Height = 150, 42
	return m, envFile
}

func key(m Model, k tea.KeyMsg) Model {
	nm, _ := m.Update(k)
	return nm.(Model)
}

func runes(m Model, s string) Model { return key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }

func TestSecretsInEnvironmentManager(t *testing.T) {
	m, envFile := secretsModel(t)
	m = runes(m, "e") // environment manager
	m.EnvModalSelectedIdx = 1
	m = key(m, tea.KeyMsg{Type: tea.KeyTab}) // focus variables: PASSWORD, host
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "PASSWORD [secret]: not set") {
		t.Fatalf("secret not listed:\n%s", view)
	}

	// Edit the secret: typed characters are masked until Ctrl+T.
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.EnvPromptAction != "edit_secret" {
		t.Fatalf("expected the secret editor, got %q", m.EnvPromptAction)
	}
	m = runes(m, "hunter2")
	if view := ansi.Strip(m.View()); strings.Contains(view, "hunter2") || !strings.Contains(view, "•••••••") {
		t.Fatalf("secret visible while typing:\n%s", view)
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if !strings.Contains(ansi.Strip(m.View()), "hunter2") {
		t.Fatal("Ctrl+T must reveal the value")
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(m.StatusMsg, "hunter2") || !strings.Contains(m.StatusMsg, "saved") {
		t.Fatalf("status: %q", m.StatusMsg)
	}
	if v, src, ok := bru.LookupVariable("PASSWORD", m.Coll); !ok || v != "hunter2" || src != bru.SourceSecret {
		t.Fatalf("lookup: %q %q %v", v, src, ok)
	}
	if data, _ := os.ReadFile(envFile); strings.Contains(string(data), "hunter2") {
		t.Fatal("secret value written to the .bru file")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "PASSWORD [secret]: ••••••••  (brui)") {
		t.Fatalf("stored secret not shown masked:\n%s", view)
	}

	// X turns `host` into a secret: its value leaves the file.
	m = key(m, tea.KeyMsg{Type: tea.KeyDown})
	m = runes(m, "x")
	data, _ := os.ReadFile(envFile)
	if strings.Contains(string(data), "api.example.com") || !strings.Contains(string(data), "  PASSWORD,\n  host\n") {
		t.Fatalf("host not moved to secrets:\n%s", data)
	}
	if v, _ := bru.GetVariable("host", m.Coll); v != "https://api.example.com" {
		t.Fatalf("host value lost: %q", v)
	}
	// ...and back to a plain variable.
	m = runes(m, "x")
	data, _ = os.ReadFile(envFile)
	if !strings.Contains(string(data), "host: https://api.example.com") || strings.Contains(string(data), "host\n]") {
		t.Fatalf("host not restored:\n%s", data)
	}
}

func TestHoverMasksSecrets(t *testing.T) {
	m, _ := secretsModel(t)
	m.Coll.Secrets.Set(m.Coll.Path, "Dev", "PASSWORD", "hunter2")
	m.SelectedRequest = m.Coll.Requests[0]
	x, y := cellOf(t, m, "{{PASSWORD}}")
	m = hover(m, x+3, y)
	if m.Hover == nil || !m.Hover.Found {
		t.Fatalf("no tooltip: %+v", m.Hover)
	}
	view := ansi.Strip(m.View())
	if strings.Contains(view, "hunter2") || !strings.Contains(view, "(7 characters)") || !strings.Contains(view, "secret") {
		t.Fatalf("tooltip must mask the secret:\n%s", view)
	}

	m.Coll.Secrets.Delete(m.Coll.Path, "Dev", "PASSWORD")
	m = hover(m, 0, 0)
	m = hover(m, x+3, y)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "secret not set") {
		t.Fatalf("missing secret not explained:\n%s", view)
	}
}
