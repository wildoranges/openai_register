package main

import "testing"

func TestRandomFingerprintProfileCoherence(t *testing.T) {
	br := NewBrowserRegister(DefaultConfig())

	allowed := map[string]struct {
		uaContains string
		minCores   int
		maxCores   int
		minMemory  int
		maxMemory  int
		minWidth   int
		minHeight  int
	}{
		"Linux x86_64": {
			uaContains: "Linux x86_64",
			minCores:   4,
			maxCores:   16,
			minMemory:  4,
			maxMemory:  16,
			minWidth:   1366,
			minHeight:  768,
		},
	}

	for i := 0; i < 40; i++ {
		p := br.randomFingerprintProfile()
		rule, ok := allowed[p.Platform]
		if !ok {
			t.Fatalf("unexpected platform: %s", p.Platform)
		}
		if len(p.NavigatorLanguages) == 0 {
			t.Fatalf("navigator languages should not be empty")
		}
		if p.AcceptLanguage == "" {
			t.Fatalf("accept-language should not be empty")
		}
		if p.HardwareConcurrency < rule.minCores || p.HardwareConcurrency > rule.maxCores {
			t.Fatalf("unexpected cores: %d", p.HardwareConcurrency)
		}
		if p.DeviceMemory < rule.minMemory || p.DeviceMemory > rule.maxMemory {
			t.Fatalf("unexpected device memory: %d", p.DeviceMemory)
		}
		if p.WindowWidth < rule.minWidth || p.WindowHeight < rule.minHeight {
			t.Fatalf("unexpected window size: %dx%d", p.WindowWidth, p.WindowHeight)
		}
		if p.ConnectionRTT < 10 {
			t.Fatalf("unexpected rtt: %d", p.ConnectionRTT)
		}
		if p.ConnectionDownlink < 2 {
			t.Fatalf("unexpected downlink: %d", p.ConnectionDownlink)
		}
		if !contains(p.UserAgent, rule.uaContains) {
			t.Fatalf("user agent/platform mismatch: %s / %s", p.UserAgent, p.Platform)
		}
	}
}

func TestToJSStringArray(t *testing.T) {
	out := toJSStringArray([]string{"en-US", "en"})
	if out != `["en-US","en"]` {
		t.Fatalf("unexpected js array: %s", out)
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
