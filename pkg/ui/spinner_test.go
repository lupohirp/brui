package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/muesli/termenv"
)

func TestSpinnerWhileRequestInFlight(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	defer unblock() // runs before srv.Close even if the test fails early

	req := &bru.BrunoRequest{ID: "r", Name: "Slow One", Method: bru.MethodGet, URL: srv.URL}
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}, Requests: []*bru.BrunoRequest{req}})
	m.Width, m.Height = 140, 40
	m.SelectedRequest = req
	msgs := startBatch(m.sendCurrentRequest())

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Request in progress") || !strings.Contains(view, "GET Slow One") || !strings.Contains(view, "●") {
		t.Fatalf("no in-progress indicator:\n%s", view)
	}

	// The animation advances on ticks (colors are off in tests, so the frame
	// counter is checked rather than the drawing).
	var tick spinnerTickMsg
	select {
	case msg := <-msgs:
		tick = msg.(spinnerTickMsg)
	case <-time.After(2 * time.Second):
		t.Fatal("no spinner tick")
	}
	nm, cmd := m.Update(tick)
	m = nm.(Model)
	if cmd == nil || m.spinFrame != 1 {
		t.Fatal("tick must advance the ring and schedule the next one")
	}
	if _, cmd := m.Update(spinnerTickMsg{gen: tick.gen - 1}); cmd != nil {
		t.Fatal("a stale tick chain must stop")
	}

	unblock()
	for {
		msg := next(t, msgs)
		if _, ok := msg.(spinnerTickMsg); ok {
			continue // ticks still in flight
		}
		nm, _ = m.Update(msg)
		m = nm.(Model)
		break
	}
	if m.IsSending || strings.Contains(ansi.Strip(m.View()), "Request in progress") {
		t.Fatal("indicator must disappear when the response arrives")
	}
	if _, cmd := m.Update(spinnerTickMsg{gen: m.spinGen}); cmd != nil {
		t.Fatal("ticks must stop once the request is done")
	}
}

func TestSpinnerCompactWhenPaneIsSmall(t *testing.T) {
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	m.IsSending = true
	m.startSpinner("GET x")
	lines := m.spinnerLines(60, 3)
	if len(lines) != 2 || !strings.Contains(ansi.Strip(lines[1]), "Request in progress") {
		t.Fatalf("compact indicator: %q", lines)
	}
	_ = tea.Quit
}

func TestRingHeadRotates(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	seen := map[string]bool{}
	for f := 0; f < len(ringDots); f++ {
		m.spinFrame = f
		rows := m.ringRows()
		if w := ansi.StringWidth(rows[1]); w != ringWidth {
			t.Fatalf("ring row width %d", w)
		}
		seen[strings.Join(rows, "\n")] = true
	}
	if len(seen) != len(ringDots) {
		t.Fatalf("expected %d distinct frames, got %d", len(ringDots), len(seen))
	}
	m.spinFrame = len(ringDots)
	if !seen[strings.Join(m.ringRows(), "\n")] {
		t.Fatal("the animation must loop")
	}
}
