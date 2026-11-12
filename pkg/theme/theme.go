package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Name        string
	Bg          string
	Fg          string
	Surface     string
	Border      string
	BorderFocus string
	Selection   string
	SelectionFg string
	Accent      string
	Subtext     string
	Get         string
	Post        string
	Put         string
	Patch       string
	Delete      string
	Success     string
	Warn        string
	Error       string
}

// Catppuccin Mocha (Default)
var Mocha = Theme{
	Name:        "Catppuccin Mocha",
	Bg:          "", // Use terminal native default
	Fg:          "#cdd6f4",
	Surface:     "#313244",
	Border:      "#45475a",
	BorderFocus: "#cba6f7",
	Selection:   "#45475a",
	SelectionFg: "#cdd6f4",
	Accent:      "#cba6f7", // Mauve
	Subtext:     "#a6adc8",
	Get:         "#a6e3a1", // Green
	Post:        "#f9e2af", // Yellow
	Put:         "#89b4fa", // Blue
	Patch:       "#fab387", // Peach
	Delete:      "#f38ba8", // Red
	Success:     "#a6e3a1",
	Warn:        "#fab387",
	Error:       "#f38ba8",
}

// Catppuccin Macchiato
var Macchiato = Theme{
	Name:        "Catppuccin Macchiato",
	Bg:          "",
	Fg:          "#cad3f5",
	Surface:     "#363a4f",
	Border:      "#494d64",
	BorderFocus: "#c6a0f6",
	Selection:   "#494d64",
	SelectionFg: "#cad3f5",
	Accent:      "#c6a0f6",
	Subtext:     "#a5adcb",
	Get:         "#a6da95",
	Post:        "#eed49f",
	Put:         "#8aadf4",
	Patch:       "#f5a97f",
	Delete:      "#ed8796",
	Success:     "#a6da95",
	Warn:        "#f5a97f",
	Error:       "#ed8796",
}

// Catppuccin Frappé
var Frappe = Theme{
	Name:        "Catppuccin Frappé",
	Bg:          "",
	Fg:          "#c6d0f5",
	Surface:     "#414559",
	Border:      "#51576d",
	BorderFocus: "#ca9ee6",
	Selection:   "#51576d",
	SelectionFg: "#c6d0f5",
	Accent:      "#ca9ee6",
	Subtext:     "#a5adce",
	Get:         "#a6d189",
	Post:        "#e5c890",
	Put:         "#8caaee",
	Patch:       "#ef9f76",
	Delete:      "#e78284",
	Success:     "#a6d189",
	Warn:        "#ef9f76",
	Error:       "#e78284",
}

var AllThemes = []Theme{Mocha, Macchiato, Frappe}

func MethodColor(method string, t Theme) lipgloss.Color {
	switch method {
	case "GET":
		return lipgloss.Color(t.Get)
	case "POST":
		return lipgloss.Color(t.Post)
	case "PUT":
		return lipgloss.Color(t.Put)
	case "PATCH":
		return lipgloss.Color(t.Patch)
	case "DELETE":
		return lipgloss.Color(t.Delete)
	default:
		return lipgloss.Color(t.Subtext)
	}
}
