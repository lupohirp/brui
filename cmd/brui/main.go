package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/config"
	"github.com/lupohirp/brui/pkg/ui"
	"github.com/muesli/termenv"
)

const usage = `Usage: brui [flags] [collection-dir]

Open a Bruno collection (defaults to the current directory).

Flags:
  -k, --insecure   skip TLS certificate verification (or "insecure": true in config)
  -h, --help       show this help

Environment:
  HTTPS_PROXY, HTTP_PROXY, NO_PROXY   proxy settings for outgoing requests
  SSL_CERT_FILE                       extra CA bundle to trust
  BRUI_COLOR=256|16                   force a reduced color palette
  BRUI_CONFIG                         config file path (default ~/.config/brui/config.json)
`

type options struct {
	path     string
	insecure bool
}

// parseArgs accepts flags before or after the collection path.
func parseArgs(args []string) (options, error) {
	opts := options{path: "."}
	pathSet := false
	for _, a := range args {
		switch {
		case a == "-k" || a == "--insecure":
			opts.insecure = true
		case a == "-h" || a == "--help":
			return opts, errHelp
		case strings.HasPrefix(a, "-") && a != "-":
			return opts, fmt.Errorf("unknown flag: %s", a)
		case pathSet:
			return opts, fmt.Errorf("unexpected argument: %s", a)
		default:
			opts.path, pathSet = a, true
		}
	}
	return opts, nil
}

var errHelp = errors.New("help requested")

func main() {
	opts, err := parseArgs(os.Args[1:])
	if errors.Is(err, errHelp) {
		fmt.Print(usage)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n%s", err, usage)
		os.Exit(2)
	}

	absPath, err := filepath.Abs(opts.path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}
	if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", absPath)
		os.Exit(1)
	}

	coll, err := bru.LoadCollection(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading collection: %v\n", err)
		os.Exit(1)
	}

	m := ui.NewModel(coll)

	// Restore the last selected theme and persist future changes.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not read config: %v\n", err)
	}
	if cfg.Theme != "" && !m.SetTheme(cfg.Theme) {
		fmt.Fprintf(os.Stderr, "Warning: unknown theme %q in config, using default\n", cfg.Theme)
	}
	m.OnThemeChange = func(name string) error {
		cfg.Theme = name
		return config.Save(cfg)
	}

	if opts.insecure || cfg.Insecure {
		bru.SetInsecureSkipVerify(true)
		m.StatusMsg = "Ready - WARNING: TLS certificate verification is disabled"
	}

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

	p := tea.NewProgram(m, tea.WithAltScreen(),
		// All-motion mouse tracking reports hover (not just drags), used to
		// show the value of the {{variable}} under the pointer.
		tea.WithMouseAllMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running brui: %v\n", err)
		os.Exit(1)
	}
}
