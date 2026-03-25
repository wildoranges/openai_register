package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShouldMarkProxyFailed(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unsupported email", err: ErrUnsupportedEmail, want: false},
		{name: "user exists", err: ErrUserAlreadyExists, want: false},
		{name: "login alias", err: ErrLoginFailedAliasUsed, want: false},
		{name: "proxy timeout", err: os.ErrDeadlineExceeded, want: true},
		{name: "token exchange network", err: newTestError("Token 兑换失败: 请求失败: dial tcp timeout"), want: true},
		{name: "region blocked", err: newTestError("当前IP/地区不支持OpenAI注册: unsupported country"), want: true},
		{name: "generic form error", err: newTestError("未找到邮箱输入框"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMarkProxyFailed(tt.err); got != tt.want {
				t.Fatalf("shouldMarkProxyFailed(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestShouldMarkProxyFailedOnEmailFetch(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		webmailMode bool
		want        bool
	}{
		{name: "nil", err: nil, webmailMode: true, want: false},
		{name: "non webmail", err: newTestError("无法从页面获取邮箱地址"), webmailMode: false, want: false},
		{name: "webmail page email fetch failure", err: newTestError("无法从页面获取邮箱地址"), webmailMode: true, want: true},
		{name: "webmail unrelated error", err: newTestError("请求超时"), webmailMode: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMarkProxyFailedOnEmailFetch(tt.err, tt.webmailMode); got != tt.want {
				t.Fatalf("shouldMarkProxyFailedOnEmailFetch(%v, %v) = %v, want %v", tt.err, tt.webmailMode, got, tt.want)
			}
		})
	}
}

type testError string

func (e testError) Error() string { return string(e) }

func newTestError(message string) error { return testError(message) }

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
