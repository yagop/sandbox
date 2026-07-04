package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	body := `{
		"allow_all": true,
		"secrets": {"gh": {"type":"basic","env":"GH_TOKEN","username":"x-access-token"}},
		"rules": [{"host":"api.github.com","inject":"gh"},{"host":"proxy.golang.org"}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(p); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !config.AllowAll {
		t.Error("allow_all should be true")
	}
	if len(config.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(config.Rules))
	}
	if config.Rules[0].Host != "api.github.com" || config.Rules[0].Inject != "gh" {
		t.Errorf("rule0 = %+v", config.Rules[0])
	}
	if config.Rules[1].Inject != "" {
		t.Errorf("rule1 should have no inject, got %q", config.Rules[1].Inject)
	}
	s, ok := config.Secrets["gh"]
	if !ok || s.Type != "basic" || s.EnvVar != "GH_TOKEN" || s.Username != "x-access-token" {
		t.Errorf("secret gh = %+v ok=%v", s, ok)
	}
}

func TestLoadConfigBadJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(`{ not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(p); err == nil {
		t.Fatal("expected error on malformed json")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if err := loadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error on missing file")
	}
}
