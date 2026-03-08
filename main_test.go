package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMainSimModeRun(t *testing.T) {
	tmp := t.TempDir()

	configPath := filepath.Join(tmp, "config.json")
	configJSON := `{"proxy":"","headless":true,"timeout":60,"debug":false,"output_dir":"creds","count":1}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	bin := filepath.Join(tmp, "openai-register-test-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "/openai_register"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(out))
	}

	run := exec.Command(bin, "--sim", "1")
	run.Dir = tmp
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("sim run failed: %v\n%s", err, string(out))
	}

	if _, err := os.Stat(filepath.Join(tmp, "creds", "openai_credentials.json")); err != nil {
		t.Fatalf("expected credentials output file: %v", err)
	}
}
