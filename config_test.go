package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Headless != true {
		t.Fatalf("expected headless=true, got %v", cfg.Headless)
	}
	if cfg.Timeout != 60 {
		t.Fatalf("expected timeout=60, got %d", cfg.Timeout)
	}
	if cfg.OutputDir != "./creds" {
		t.Fatalf("expected output dir ./creds, got %s", cfg.OutputDir)
	}
	if cfg.Count != 1 {
		t.Fatalf("expected count=1, got %d", cfg.Count)
	}
}

func TestLoadConfigMissingFileUsesDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-exist.json")
	cfg, err := LoadConfig(missing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 60 || cfg.Count != 1 {
		t.Fatalf("expected default config, got %+v", *cfg)
	}
}

func TestSaveAndLoadConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	original := &Config{
		Proxy:     "http://proxy-host:port",
		Headless:  false,
		Timeout:   42,
		Debug:     true,
		OutputDir: "./out",
		Count:     3,
	}

	if err := SaveConfig(original, path); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if *loaded != *original {
		t.Fatalf("round-trip mismatch\nloaded=%+v\norig=%+v", *loaded, *original)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatalf("expected error for invalid json")
	}
}
