package ui

import (
	"testing"

	"github.com/lupohirp/brui/pkg/bru"
)

func TestSetThemeAndCyclePersists(t *testing.T) {
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	if !m.SetTheme("tokyo night") || m.Theme.Name != "Tokyo Night" {
		t.Fatalf("SetTheme failed, got %q", m.Theme.Name)
	}
	if m.SetTheme("does-not-exist") {
		t.Fatal("unknown theme should not match")
	}
	var saved string
	m.OnThemeChange = func(name string) error { saved = name; return nil }
	m.cycleTheme()
	if saved != "Dracula" || m.Theme.Name != "Dracula" {
		t.Fatalf("want Dracula persisted, got saved=%q current=%q", saved, m.Theme.Name)
	}
}
