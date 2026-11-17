package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// hoverInfo describes the {{variable}} currently under the mouse pointer.
type hoverInfo struct {
	Name   string
	Value  string
	Source string // human-readable origin, empty when undefined
	Found  bool
	X, Y   int // pointer position
}

// View renders the screen plus, when hovering a variable, its tooltip.
func (m Model) View() string {
	base := m.baseView()
	if m.Hover == nil || m.Width <= 0 || m.Height <= 0 {
		return base
	}
	return m.overlayTooltip(base)
}

// updateHover finds the {{variable}} drawn at screen cell (x, y), if any.
// It works on the rendered screen, so it covers every pane and modal.
func (m *Model) updateHover(x, y int) {
	m.Hover = nil
	lines := strings.Split(m.baseView(), "\n")
	if y < 0 || y >= len(lines) {
		return
	}
	line := ansi.Strip(lines[y])
	for _, loc := range varRegex.FindAllStringSubmatchIndex(line, -1) {
		start := ansi.StringWidth(line[:loc[0]])
		end := start + ansi.StringWidth(line[loc[0]:loc[1]])
		if x < start || x >= end {
			continue
		}
		name := line[loc[2]:loc[3]]
		h := &hoverInfo{Name: name, X: x, Y: y}
		switch {
		case bru.IsDynamicVariable(name):
			h.Found, h.Source, h.Value = true, "dynamic", "generated when the request is sent"
		default:
			if val, src, ok := bru.LookupVariable(name, m.Coll); ok {
				h.Found, h.Value = true, val
				h.Source = src
				if src == bru.SourceEnv {
					h.Source = "env: " + m.Coll.ActiveEnvironment
				}
			}
		}
		m.Hover = h
		return
	}
}

const maxTooltipValueWidth = 72

func (m Model) overlayTooltip(base string) string {
	h := m.Hover
	t := m.Theme
	bg := lipgloss.Color(t.ModalBg)
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Background(bg).Bold(true)
	subStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext)).Background(bg)
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Fg)).Background(bg)

	maxW := maxTooltipValueWidth
	if limit := m.Width - 6; limit < maxW {
		maxW = limit
	}
	if maxW < 10 {
		return base
	}

	var rows []string
	if h.Found {
		value := h.Value
		if value == "" {
			value = `""` // make empty values visible
		}
		header := nameStyle.Render("{{"+h.Name+"}}") + subStyle.Render("  "+h.Source)
		rows = append(rows, header)
		valueLines := strings.Split(value, "\n")
		for i, l := range valueLines {
			if i == 4 {
				rows = append(rows, subStyle.Render(fmt.Sprintf("… %d more lines", len(valueLines)-4)))
				break
			}
			rows = append(rows, valStyle.Render(ansi.Truncate(l, maxW, "…")))
		}
	} else {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Error)).Background(bg)
		rows = append(rows, nameStyle.Render("{{"+h.Name+"}}"), errStyle.Render("undefined in the active environment"))
	}

	box := lipgloss.NewStyle().
		Background(bg).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.BorderFocus)).
		BorderBackground(bg).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))

	boxLines := strings.Split(box, "\n")
	boxH, boxW := len(boxLines), ansi.StringWidth(boxLines[0])

	// Prefer just below the pointer, otherwise above it; keep it on screen.
	row := h.Y + 1
	if row+boxH > m.Height {
		row = h.Y - boxH
	}
	if row < 0 {
		row = 0
	}
	col := h.X - 2
	if col+boxW > m.Width {
		col = m.Width - boxW
	}
	if col < 0 {
		col = 0
	}
	return overlayAt(base, box, row, col, m.Width)
}

// overlayAt draws box over base with its top-left corner at (row, col),
// preserving the ANSI styling of the uncovered parts of each line.
func overlayAt(base, box string, row, col, screenW int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	boxW := ansi.StringWidth(boxLines[0])

	for r := 0; r < len(boxLines) && row+r < len(baseLines); r++ {
		bLine := baseLines[row+r]

		left := ansi.Truncate(bLine, col, "")
		if w := ansi.StringWidth(left); w < col {
			left += strings.Repeat(" ", col-w)
		}

		right := ansi.TruncateLeft(bLine, col+boxW, "")
		if remain := screenW - (col + boxW); remain > 0 {
			if w := ansi.StringWidth(right); w < remain {
				right += strings.Repeat(" ", remain-w)
			}
		}

		baseLines[row+r] = left + boxLines[r] + right
	}
	return strings.Join(baseLines, "\n")
}
