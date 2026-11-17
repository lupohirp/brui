package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// The params tab shows query and path params read from a real-format .bru
// file, and editing the URL keeps params in sync.
func TestFixtureParamsTabAndURLSync(t *testing.T) {
	coll, err := bru.LoadCollection("../bru/testdata/collection")
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(coll)
	m.Width, m.Height = 140, 40
	for i, it := range m.VisibleItems {
		if it.Type == "request" && it.Request.Name == "Get User" {
			m.SelectedIdx = i
			m.selectCurrentItem()
		}
	}
	if m.SelectedRequest == nil || m.SelectedRequest.Name != "Get User" {
		t.Fatal("Get User not selected")
	}
	m.ActiveTab = "params"
	view := ansi.Strip(m.View())
	for _, want := range []string{"expand = roles", "# debug = true", ":id = {{userId}}"} {
		if !strings.Contains(view, want) {
			t.Errorf("params tab missing %q", want)
		}
	}

	m.IsEditingURL = true
	m.URLEditText = "{{baseUrl}}/users/:id?expand=all"
	m.commitPendingEdits()
	var names []string
	for _, p := range m.SelectedRequest.Params {
		names = append(names, p.Name+"="+p.Value)
	}
	if got := strings.Join(names, ","); got != "debug=true,id={{userId}},expand=all" {
		t.Errorf("params after URL edit: %s", got)
	}
}
