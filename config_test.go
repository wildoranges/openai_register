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
		Proxies:   []string{"http://proxy1:8080", "http://proxy2:8080"},
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

	if loaded.Proxy != original.Proxy {
		t.Fatalf("proxy mismatch: got %s, want %s", loaded.Proxy, original.Proxy)
	}
	if loaded.Headless != original.Headless {
		t.Fatalf("headless mismatch: got %v, want %v", loaded.Headless, original.Headless)
	}
	if loaded.Timeout != original.Timeout {
		t.Fatalf("timeout mismatch: got %d, want %d", loaded.Timeout, original.Timeout)
	}
	if loaded.Debug != original.Debug {
		t.Fatalf("debug mismatch: got %v, want %v", loaded.Debug, original.Debug)
	}
	if loaded.OutputDir != original.OutputDir {
		t.Fatalf("output dir mismatch: got %s, want %s", loaded.OutputDir, original.OutputDir)
	}
	if loaded.Count != original.Count {
		t.Fatalf("count mismatch: got %d, want %d", loaded.Count, original.Count)
	}
	if len(loaded.Proxies) != len(original.Proxies) {
		t.Fatalf("proxies length mismatch: got %d, want %d", len(loaded.Proxies), len(original.Proxies))
	}
	for i := range original.Proxies {
		if loaded.Proxies[i] != original.Proxies[i] {
			t.Fatalf("proxy[%d] mismatch: got %s, want %s", i, loaded.Proxies[i], original.Proxies[i])
		}
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
