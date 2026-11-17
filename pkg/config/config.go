// Package config persists user preferences (e.g. the selected theme) across
// sessions in $XDG_CONFIG_HOME/brui/config.json (~/.config/brui/config.json).
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Config struct {
	Theme string `json:"theme,omitempty"`
	// Insecure disables TLS certificate verification (same as --insecure).
	Insecure bool `json:"insecure,omitempty"`
}

// Path returns the location of the config file. BRUI_CONFIG overrides it.
func Path() (string, error) {
	if p := os.Getenv("BRUI_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "brui", "config.json"), nil
}

// Load reads the config file. A missing file yields an empty Config and no error.
func Load() (Config, error) {
	var c Config
	p, err := Path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

// Save writes the config file atomically, creating the directory if needed.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
