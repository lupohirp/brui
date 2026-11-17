package config

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	t.Setenv("BRUI_CONFIG", filepath.Join(t.TempDir(), "nope", "config.json"))
	c, err := Load()
	if err != nil || c.Theme != "" {
		t.Fatalf("want empty config, got %+v err=%v", c, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("BRUI_CONFIG", filepath.Join(t.TempDir(), "sub", "config.json"))
	if err := Save(Config{Theme: "Dracula"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.Theme != "Dracula" {
		t.Fatalf("want Dracula, got %+v err=%v", c, err)
	}
}
