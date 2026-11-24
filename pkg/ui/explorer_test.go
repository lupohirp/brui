package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func explorerModel() Model {
	health := &bru.BrunoRequest{ID: "health", Name: "Health", Method: bru.MethodGet, URL: "https://x/health"}
	getUser := &bru.BrunoRequest{ID: "users/get", Name: "Get User", Method: bru.MethodGet, URL: "{{host}}/users/1"}
	createUser := &bru.BrunoRequest{ID: "users/create", Name: "Create User", Method: bru.MethodPost, URL: "{{host}}/users"}
	deleteThing := &bru.BrunoRequest{ID: "admin/sub/del", Name: "Delete Thing", Method: bru.MethodDelete, URL: "{{host}}/things/9"}
	sub := &bru.BrunoFolder{ID: "admin/sub", Name: "sub", Requests: []*bru.BrunoRequest{deleteThing}}
	coll := &bru.BrunoCollection{
		RuntimeVariables: map[string]any{},
		Requests:         []*bru.BrunoRequest{health},
		Folders: []*bru.BrunoFolder{
			{ID: "admin", Name: "admin", Folders: []*bru.BrunoFolder{sub}},
			{ID: "users", Name: "users", Requests: []*bru.BrunoRequest{getUser, createUser}},
		},
	}
	m := NewModel(coll)
	m.Width, m.Height = 140, 40
	return m
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}
	return m
}

func selected(m Model) string {
	it := m.VisibleItems[m.SelectedIdx]
	if it.Type == "folder" {
		return "folder:" + it.Folder.Name
	}
	return it.Request.Name
}

func TestExplorerArrowsFoldAndUnfold(t *testing.T) {
	m := explorerModel()
	// Tree: Health, admin, sub, Delete Thing, users, Get User, Create User.
	m.SelectedIdx = 3 // Delete Thing
	m = press(m, "left")
	if selected(m) != "folder:sub" {
		t.Fatalf("← on a request must select its folder, got %s", selected(m))
	}
	m = press(m, "left")
	if m.ExpandedFolders["admin/sub"] || selected(m) != "folder:sub" {
		t.Fatal("← on an open folder must close it")
	}
	m = press(m, "left")
	if selected(m) != "folder:admin" {
		t.Fatalf("← on a closed folder must go to the parent, got %s", selected(m))
	}
	m = press(m, "left")
	if m.ExpandedFolders["admin"] {
		t.Fatal("admin should be closed")
	}
	m = press(m, "right")
	if !m.ExpandedFolders["admin"] || selected(m) != "folder:admin" {
		t.Fatal("→ on a closed folder must open it")
	}
	m = press(m, "right", "right")
	if selected(m) != "folder:sub" || !m.ExpandedFolders["admin/sub"] {
		t.Fatalf("→ on an open folder enters it, then opens the child: %s", selected(m))
	}
	m = press(m, "right")
	if selected(m) != "Delete Thing" || m.SelectedRequest == nil || m.SelectedRequest.Name != "Delete Thing" {
		t.Fatalf("→ must reach the request, got %s", selected(m))
	}
	m = press(m, "right") // no-op on a request
	if selected(m) != "Delete Thing" {
		t.Fatal("→ on a request must do nothing")
	}
}

func TestExplorerSearch(t *testing.T) {
	m := explorerModel()
	m.SelectedIdx = 0
	m = press(m, "/", "c", "r", "e", "a", "t", "e", " ", "u", "s")
	if m.Search == nil || m.Search.Query != "create us" {
		t.Fatalf("search not active: %+v", m.Search)
	}
	if len(m.VisibleItems) != 2 || selected(m) != "Create User" || m.SelectedRequest.Name != "Create User" {
		t.Fatalf("results: %d items, selected %s", len(m.VisibleItems), selected(m))
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "/ create us") || !strings.Contains(view, "Create User") || strings.Contains(view, "Get User") {
		t.Fatalf("search box / results not rendered:\n%s", view)
	}

	// Typing "q" or "e" in the search does not trigger shortcuts.
	m = press(m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "q")
	if m.Search == nil || m.Search.Query != "q" {
		t.Fatalf("q must be typed, not quit: %+v", m.Search)
	}

	// The URL and folder are searchable too; Enter opens the result.
	m = press(m, "backspace", "t", "h", "i", "n", "g", "s", "/", "9")
	if selected(m) != "Delete Thing" {
		t.Fatalf("URL search: %s", selected(m))
	}
	m = press(m, "enter")
	if m.Search != nil || selected(m) != "Delete Thing" || !m.ExpandedFolders["admin"] || !m.ExpandedFolders["admin/sub"] {
		t.Fatalf("enter must close the search and reveal the request: %v %s", m.Search, selected(m))
	}
	if len(m.VisibleItems) != 7 {
		t.Fatalf("full tree not restored: %d items", len(m.VisibleItems))
	}

	// Esc restores the previous selection.
	m.SelectedIdx = 0
	m.selectCurrentItem()
	m = press(m, "/", "g", "e", "t", "esc")
	if m.Search != nil || selected(m) != "Health" || m.SelectedRequest.Name != "Health" {
		t.Fatalf("esc must restore: %s", selected(m))
	}

	// No results.
	m = press(m, "/", "z", "z", "z")
	if len(m.VisibleItems) != 0 || !strings.Contains(ansi.Strip(m.View()), "no matching requests") {
		t.Fatal("empty result not shown")
	}
	m = press(m, "esc")
	if len(m.VisibleItems) != 7 {
		t.Fatal("tree not restored after esc")
	}
}

func TestSidebarClickAfterKeyboardScroll(t *testing.T) {
	var reqs []*bru.BrunoRequest
	for i := 0; i < 60; i++ {
		reqs = append(reqs, &bru.BrunoRequest{ID: fmt.Sprint(i), Name: fmt.Sprintf("Req %02d", i), Method: bru.MethodGet, URL: "https://x"})
	}
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}, Requests: reqs})
	m.Width, m.Height = 120, 30
	for i := 0; i < 45; i++ {
		m = press(m, "down")
	}
	// Click the first visible tree row: it must be the row drawn there.
	x, y := cellOf(t, m, "Req ")
	nm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(Model)
	firstDrawn := ""
	for _, l := range strings.Split(ansi.Strip(m.baseView()), "\n") {
		if i := strings.Index(l, "Req "); i >= 0 {
			firstDrawn = l[i : i+6]
			break
		}
	}
	if m.SelectedRequest == nil || m.SelectedRequest.Name != firstDrawn {
		t.Fatalf("clicked %q but selected %v", firstDrawn, m.SelectedRequest)
	}
}
