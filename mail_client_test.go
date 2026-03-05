package main

import (
	"net/http"
	"testing"
)

func TestNewHTTPClientTimeout(t *testing.T) {
	c := NewHTTPClient()
	if c == nil || c.client == nil {
		t.Fatalf("expected non-nil client")
	}
	if c.client.Timeout.Seconds() != 60 {
		t.Fatalf("expected timeout 60s, got %v", c.client.Timeout)
	}
}

func TestNewHTTPClientWithProxyInvalidURL(t *testing.T) {
	c := NewHTTPClientWithProxy("://bad")
	if c == nil || c.client == nil {
		t.Fatalf("expected non-nil client")
	}
	if c.client.Transport != nil {
		t.Fatalf("expected nil transport for invalid proxy url")
	}
}

func TestSetDefaultHeaders(t *testing.T) {
	c := NewHTTPClient()
	req, err := http.NewRequest("GET", "https://example.com", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	c.SetDefaultHeaders(req)
	if req.Header.Get("User-Agent") == "" {
		t.Fatalf("expected user-agent set")
	}
	if req.Header.Get("Accept") == "" {
		t.Fatalf("expected accept set")
	}
}

func TestExtractOTPCode(t *testing.T) {
	code := extractOTPCode("hello Your ChatGPT code is 123456 world")
	if code != "123456" {
		t.Fatalf("expected 123456, got %q", code)
	}
}

func TestExtractVerifyLinkFromOTP(t *testing.T) {
	link := extractVerifyLink("Your verification code is 654321")
	if link != "OTP:654321" {
		t.Fatalf("expected OTP:654321, got %q", link)
	}
}

func TestExtractVerifyLinkFromURL(t *testing.T) {
	link := extractVerifyLink(`go https://auth.openai.com/authorize?token=abc123&x=1 now`)
	if link == "" {
		t.Fatalf("expected non-empty link")
	}
	if link[:8] != "https://" {
		t.Fatalf("expected https link, got %q", link)
	}
}

func TestCheckMailContentSubjectFilter(t *testing.T) {
	c := NewHTTPClient()
	if got := c.checkMailContent("weekly report", "", "", ""); got != "" {
		t.Fatalf("expected empty for unrelated subject, got %q", got)
	}
}
