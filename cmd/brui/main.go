package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/ui"
	"github.com/muesli/termenv"
)

func main() {
	targetPath := "."
	if len(os.Args) > 1 {
		targetPath = os.Args[1]
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	coll, err := bru.LoadCollection(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading collection: %v\n", err)
		os.Exit(1)
	}

	m := ui.NewModel(coll)

	// Force 24-bit truecolor so Catppuccin hex palettes are rendered exactly.
	// Without this, lipgloss downsamples to ANSI16/256 when COLORTERM is unset
	// (e.g. Macchiato turns navy blue, Frappe turns flat gray).
	// Override with BRUI_COLOR=256 or BRUI_COLOR=16 on terminals lacking truecolor.
	switch os.Getenv("BRUI_COLOR") {
	case "256":
		lipgloss.SetColorProfile(termenv.ANSI256)
	case "16":
		lipgloss.SetColorProfile(termenv.ANSI)
	default:
		if os.Getenv("NO_COLOR") == "" {
			lipgloss.SetColorProfile(termenv.TrueColor)
		}
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running brui: %v\n", err)
		os.Exit(1)
	}
}
