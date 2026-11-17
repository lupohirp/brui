package ui

import (
	"fmt"
	"testing"

	"github.com/lupohirp/brui/pkg/bru"
)

// Mouse motion re-renders the screen continuously, so View must be
// deterministic: identical state must produce identical output.
func TestViewIsDeterministic(t *testing.T) {
	m := hoverModel()
	m.LastResponse = &bru.HTTPResponse{Status: 200, StatusText: "200 OK", Body: `{"a":1}`,
		Headers: map[string]string{"Content-Type": "json", "Date": "x", "Server": "s", "Vary": "v", "X-A": "1", "X-B": "2", "X-C": "3"}}
	for _, tab := range []string{"body", "headers", "console"} {
		m.ActiveRespTab = tab
		first := m.View()
		for i := 0; i < 30; i++ {
			if m.View() != first {
				t.Fatalf("tab %s: View output changes between identical renders", tab)
			}
		}
	}
}

func TestAutocompleteOrderIsStable(t *testing.T) {
	m := hoverModel()
	for i := 0; i < 20; i++ {
		m.Coll.Environments[0].Variables[string(rune('a'+i))+"Var"] = "v"
	}
	m.IsEditingURL, m.URLEditText = true, "{{"
	m.URLCursor = len(m.URLEditText)
	m.updateAutocomplete()
	first := fmt.Sprint(m.ACCandidates)
	for i := 0; i < 20; i++ {
		m.updateAutocomplete()
		if got := fmt.Sprint(m.ACCandidates); got != first {
			t.Fatalf("autocomplete candidates change between identical updates:\n%s\n%s", first, got)
		}
	}
}
