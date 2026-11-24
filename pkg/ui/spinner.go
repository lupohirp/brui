package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// "Request in progress" indicator: a ring of dots with a rotating head,
// drawn in the middle of the response pane while a request (or an OAuth2
// token fetch) is running.

const spinnerInterval = 90 * time.Millisecond

// spinnerTickMsg advances the animation; gen discards ticks from an earlier
// request so only one tick chain is ever alive.
type spinnerTickMsg struct{ gen int }

func spinnerTick(gen int) tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{gen: gen} })
}

// startSpinner starts the animation for a new in-flight operation.
func (m *Model) startSpinner(label string) tea.Cmd {
	m.spinGen++
	m.spinFrame = 0
	m.sendStarted = time.Now()
	m.sendLabel = label
	return spinnerTick(m.spinGen)
}

func (m *Model) onSpinnerTick(msg spinnerTickMsg) tea.Cmd {
	if msg.gen != m.spinGen || !m.IsSending {
		return nil // stale chain or finished: stop
	}
	m.spinFrame++
	return spinnerTick(msg.gen)
}

// ringDots are the ring positions (row, column) in clockwise order.
var ringDots = [8][2]int{{0, 2}, {0, 5}, {0, 8}, {1, 10}, {2, 8}, {2, 5}, {2, 2}, {1, 0}}

const ringWidth = 11

// ringRows draws the ring for the current frame: the head is bright and
// followed by a fading trail.
func (m Model) ringRows() []string {
	t := m.Theme
	head := m.spinFrame % len(ringDots)
	colors := map[int]lipgloss.Style{
		0: lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true),
		1: lipgloss.NewStyle().Foreground(lipgloss.Color(t.BorderFocus)),
		2: lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext)),
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Border))

	grid := [3][ringWidth]string{}
	for r := range grid {
		for c := range grid[r] {
			grid[r][c] = " "
		}
	}
	for i, pos := range ringDots {
		age := (head - i + len(ringDots)) % len(ringDots) // 0 = head
		st, ok := colors[age]
		if !ok {
			st = dim
		}
		grid[pos[0]][pos[1]] = st.Render("●")
	}
	rows := make([]string, 3)
	for r := range grid {
		rows[r] = strings.Join(grid[r][:], "")
	}
	return rows
}

func (m Model) elapsedText() string {
	d := time.Since(m.sendStarted)
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

// spinnerLines fills the response body area (width x height) with the
// indicator, centered.
func (m Model) spinnerLines(width, height int) []string {
	t := m.Theme
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext))

	title := accent.Render("Request in progress") + sub.Render("  "+m.elapsedText())
	var block []string
	if height >= 8 {
		block = append(m.ringRows(), "")
		block = append(block, title)
		if m.sendLabel != "" {
			block = append(block, sub.Render(m.sendLabel))
		}
		block = append(block, sub.Render("[Esc] Cancel"))
	} else {
		frames := []string{"◐", "◓", "◑", "◒"}
		block = []string{accent.Render(frames[m.spinFrame%len(frames)]) + " " + title + sub.Render("   [Esc] Cancel")}
	}

	top := max((height-len(block))/2, 0)
	lines := make([]string, 0, height)
	for i := 0; i < top; i++ {
		lines = append(lines, "")
	}
	for _, b := range block {
		b = ansi.Truncate(b, width, "…")
		pad := max((width-ansi.StringWidth(b))/2, 0)
		lines = append(lines, strings.Repeat(" ", pad)+b)
	}
	return lines
}
