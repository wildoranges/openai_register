package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewLocalProxyForwarderWithAuth(t *testing.T) {
	f, err := NewLocalProxyForwarder("http://proxy-host:port")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.targetURL.Host != "127.0.0.1:8080" {
		t.Fatalf("unexpected host: %s", f.targetURL.Host)
	}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if f.authHeader != expected {
		t.Fatalf("unexpected auth header: %s", f.authHeader)
	}
}

func TestNewLocalProxyForwarderWithoutAuth(t *testing.T) {
	f, err := NewLocalProxyForwarder("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.authHeader != "" {
		t.Fatalf("expected empty auth header, got %q", f.authHeader)
	}
}

func TestNewLocalProxyForwarderInvalidURL(t *testing.T) {
	_, err := NewLocalProxyForwarder("http://%gh&%ij")
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "解析代理URL失败") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProxyForwarderStartStop(t *testing.T) {
	f, err := NewLocalProxyForwarder("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	addr, err := f.Start()
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("unexpected listen addr: %s", addr)
	}
	f.Stop()
}
