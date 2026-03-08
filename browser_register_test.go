package main

import (
	"testing"
)

func TestNewBrowserRegister(t *testing.T) {
	cfg := &Config{Proxy: "http://127.0.0.1:9999", Headless: true}
	br := NewBrowserRegister(cfg)
	if br == nil {
		t.Fatalf("expected non-nil browser register")
	}
	if br.config != cfg {
		t.Fatalf("expected config pointer to be retained")
	}
	if br.httpClient == nil || br.httpClient.client == nil {
		t.Fatalf("expected http client to be initialized")
	}
}

func TestGenerateHumanTrack(t *testing.T) {
	br := NewBrowserRegister(DefaultConfig())
	track := br.generateHumanTrack(0, 0, 100, 0)
	if len(track) < 30 || len(track) > 50 {
		t.Fatalf("unexpected track length: %d", len(track))
	}
	if track[0].X < -1 || track[len(track)-1].X < 90 {
		t.Fatalf("unexpected track boundaries: first=%v last=%v", track[0], track[len(track)-1])
	}
}
