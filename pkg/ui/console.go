package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Response pane tabs, in Tab-key cycling order.
var respTabs = []struct{ id, label string }{
	{"body", "Body"},
	{"headers", "Headers"},
	{"console", "Console"},
}

func nextRespTab(cur string) string {
	for i, t := range respTabs {
		if t.id == cur {
			return respTabs[(i+1)%len(respTabs)].id
		}
	}
	return respTabs[0].id
}

func (m *Model) setRespTab(id string) {
	m.ActiveRespTab = id
	m.RespScroll = 0
	if id == "console" {
		m.scrollConsoleToEnd()
	}
}

// respStatusPrefix is the left part of the response header line.
func (m Model) respStatusPrefix() string {
	if m.LastResponse == nil {
		return " Status: [ Idle ]"
	}
	r := m.LastResponse
	statusColor := m.Theme.Success
	if r.Status >= 400 {
		statusColor = m.Theme.Error
	}
	stBadge := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Bold(true).Render(fmt.Sprintf("%d %s", r.Status, r.StatusText))
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	return fmt.Sprintf(" %s   %s   %s", stBadge, sub.Render(fmt.Sprintf("Time: %dms", r.DurationMs)), sub.Render(fmt.Sprintf("Size: %dB", r.SizeBytes)))
}

// consoleEntryCount counts log entries, excluding per-run separators.
func (m Model) consoleEntryCount() (total, errors int) {
	for _, e := range m.ScriptLogs {
		switch e.Type {
		case "run":
			continue
		case "error":
			errors++
		}
		total++
	}
	return total, errors
}

func (m Model) respTabLabels() []string {
	labels := make([]string, len(respTabs))
	for i, t := range respTabs {
		label := t.label
		if t.id == "console" {
			if n, errs := m.consoleEntryCount(); errs > 0 {
				label = fmt.Sprintf("Console (%d, %d!)", n, errs)
			} else if n > 0 {
				label = fmt.Sprintf("Console (%d)", n)
			}
		}
		if t.id == m.ActiveRespTab {
			labels[i] = "[ " + label + " ]"
		} else {
			labels[i] = "  " + label + "  "
		}
	}
	return labels
}

type tabRange struct {
	id         string
	start, end int // columns relative to the pane's inner area, end exclusive
}

// respTabRanges returns where each tab label is drawn, for mouse hit-testing.
// It mirrors the layout built in renderResponsePane.
func (m Model) respTabRanges() []tabRange {
	x := ansi.StringWidth(m.respStatusPrefix()) + 3
	labels := m.respTabLabels()
	out := make([]tabRange, len(labels))
	for i, l := range labels {
		w := ansi.StringWidth(l)
		out[i] = tabRange{id: respTabs[i].id, start: x, end: x + w}
		x += w + 1
	}
	return out
}

// consoleLines renders script console output, one run section per request.
func (m Model) consoleLines() []string {
	if len(m.ScriptLogs) == 0 {
		return []string{" No script output yet: console.log() from pre-request and post-response scripts shows up here."}
	}
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	var lines []string
	for _, e := range m.ScriptLogs {
		if e.Type == "run" {
			lines = append(lines, sub.Render(fmt.Sprintf(" ── %s  %s ──", e.Timestamp, e.Message)))
			continue
		}
		color := m.Theme.Fg
		switch e.Type {
		case "warn":
			color = m.Theme.Warn
		case "error":
			color = m.Theme.Error
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		prefix := fmt.Sprintf(" %s %-5s ", e.Timestamp, e.Type)
		for i, l := range strings.Split(e.Message, "\n") {
			if i == 0 {
				lines = append(lines, sub.Render(prefix)+style.Render(l))
			} else {
				lines = append(lines, strings.Repeat(" ", len(prefix))+style.Render(l))
			}
		}
	}
	return lines
}

// respBodyCapacity is the number of content rows in the response pane.
func (m Model) respBodyCapacity() int {
	_, _, _, _, respH := m.layoutDimensions()
	if c := respH - 4; c > 0 {
		return c
	}
	return 0
}

func (m *Model) scrollConsoleToEnd() {
	m.RespScroll = 0
	if over := len(m.consoleLines()) - m.respBodyCapacity(); over > 0 {
		m.RespScroll = over
	}
}
