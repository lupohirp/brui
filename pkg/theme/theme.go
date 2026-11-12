package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Name        string
	Bg          string
	Fg          string
	Surface     string
	ModalBg     string
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
	Bg:          "#1e1e2e",
	Fg:          "#cdd6f4",
	Surface:     "#313244",
	ModalBg:     "#181825",
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
	Bg:          "#24273a",
	Fg:          "#cad3f5",
	Surface:     "#363a4f",
	ModalBg:     "#1e2030",
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
	Bg:          "#303446",
	Fg:          "#c6d0f5",
	Surface:     "#414559",
	ModalBg:     "#292c3c",
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

// Catppuccin Latte (Light)
var Latte = Theme{
	Name:        "Catppuccin Latte",
	Bg:          "#eff1f5",
	Fg:          "#4c4f69",
	Surface:     "#e6e9ef",
	ModalBg:     "#dce0e8",
	Border:      "#9ca0b0",
	BorderFocus: "#8839ef",
	Selection:   "#ccd0da",
	SelectionFg: "#4c4f69",
	Accent:      "#8839ef",
	Subtext:     "#6c6f85",
	Get:         "#40a02b",
	Post:        "#df8e1d",
	Put:         "#1e66f5",
	Patch:       "#fe640b",
	Delete:      "#d20f39",
	Success:     "#40a02b",
	Warn:        "#fe640b",
	Error:       "#d20f39",
}

// Tokyo Night
var TokyoNight = Theme{
	Name:        "Tokyo Night",
	Bg:          "#1a1b26",
	Fg:          "#c0caf5",
	Surface:     "#292e42",
	ModalBg:     "#16161e",
	Border:      "#414868",
	BorderFocus: "#bb9af7",
	Selection:   "#33467c",
	SelectionFg: "#c0caf5",
	Accent:      "#bb9af7",
	Subtext:     "#9aa5ce",
	Get:         "#9ece6a",
	Post:        "#ff9e64",
	Put:         "#7aa2f7",
	Patch:       "#7dcfff",
	Delete:      "#f7768e",
	Success:     "#9ece6a",
	Warn:        "#e0af68",
	Error:       "#f7768e",
}

// Dracula
var Dracula = Theme{
	Name:        "Dracula",
	Bg:          "#282a36",
	Fg:          "#f8f8f2",
	Surface:     "#44475a",
	ModalBg:     "#21222c",
	Border:      "#6272a4",
	BorderFocus: "#bd93f9",
	Selection:   "#44475a",
	SelectionFg: "#f8f8f2",
	Accent:      "#bd93f9",
	Subtext:     "#bfbfbf",
	Get:         "#50fa7b",
	Post:        "#ffb86c",
	Put:         "#8be9fd",
	Patch:       "#8be9fd",
	Delete:      "#ff5555",
	Success:     "#50fa7b",
	Warn:        "#ffb86c",
	Error:       "#ff5555",
}

var AllThemes = []Theme{Mocha, Macchiato, Frappe, Latte, TokyoNight, Dracula}


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
