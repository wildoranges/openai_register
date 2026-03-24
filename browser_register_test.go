package main

import (
	"net/http"
	"strconv"
	"strings"
	"net/url"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
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
	if br.proxyURL != cfg.Proxy {
		t.Fatalf("expected proxyURL %q, got %q", cfg.Proxy, br.proxyURL)
	}
	if got := br.effectiveProxyURL(); got != cfg.Proxy {
		t.Fatalf("expected effective proxy %q, got %q", cfg.Proxy, got)
	}
	assertHTTPClientProxy(t, br.httpClient.client, cfg.Proxy)
}

func TestNewBrowserRegisterWithProxyPreservesExplicitProxy(t *testing.T) {
	cfg := &Config{Proxy: "http://127.0.0.1:9999", Headless: true}
	explicitProxy := "http://127.0.0.1:8888"
	br := NewBrowserRegisterWithProxy(cfg, explicitProxy)

	if br == nil {
		t.Fatalf("expected non-nil browser register")
	}
	if br.config != cfg {
		t.Fatalf("expected config pointer to be retained")
	}
	if br.proxyURL != explicitProxy {
		t.Fatalf("expected stored proxy %q, got %q", explicitProxy, br.proxyURL)
	}
	if got := br.effectiveProxyURL(); got != explicitProxy {
		t.Fatalf("expected effective proxy %q, got %q", explicitProxy, got)
	}
	assertHTTPClientProxy(t, br.httpClient.client, explicitProxy)

	proxyAwareClient := br.newProxyAwareHTTPClient(30)
	assertHTTPClientProxy(t, proxyAwareClient, explicitProxy)
}

func TestNewBrowserRegisterWithWebMailPreservesExplicitProxy(t *testing.T) {
	cfg := &Config{Proxy: "http://127.0.0.1:9999", Headless: true}
	explicitProxy := "http://127.0.0.1:7777"
	br := NewBrowserRegisterWithWebMail(cfg, explicitProxy, true)

	if br == nil {
		t.Fatalf("expected non-nil browser register")
	}
	if br.webMailClient == nil {
		t.Fatalf("expected web mail client to be initialized")
	}
	if br.proxyURL != explicitProxy {
		t.Fatalf("expected stored proxy %q, got %q", explicitProxy, br.proxyURL)
	}
	if got := br.effectiveProxyURL(); got != explicitProxy {
		t.Fatalf("expected effective proxy %q, got %q", explicitProxy, got)
	}
}

func TestNewBrowserRegisterOAuthPreservesExplicitProxy(t *testing.T) {
	cfg := &Config{Proxy: "http://127.0.0.1:9999", Headless: true}
	explicitProxy := "http://127.0.0.1:6666"
	br := NewBrowserRegisterOAuth(cfg, explicitProxy)

	if br == nil {
		t.Fatalf("expected non-nil oauth browser register")
	}
	if br.BrowserRegister == nil {
		t.Fatalf("expected embedded browser register")
	}
	if br.proxyURL != explicitProxy {
		t.Fatalf("expected stored proxy %q, got %q", explicitProxy, br.proxyURL)
	}
	if got := br.effectiveProxyURL(); got != explicitProxy {
		t.Fatalf("expected effective proxy %q, got %q", explicitProxy, got)
	}
	assertHTTPClientProxy(t, br.httpClient.client, explicitProxy)
	assertHTTPClientProxy(t, br.newProxyAwareHTTPClient(30), explicitProxy)
}

func TestApplyLauncherProxyPreservesExplicitProxyScheme(t *testing.T) {
	cfg := &Config{Proxy: "http://127.0.0.1:9999", Headless: true}
	explicitProxy := "socks5://127.0.0.1:1080"
	br := NewBrowserRegisterWithProxy(cfg, explicitProxy)

	l := launcher.New()
	l, localProxy, err := br.applyLauncherProxy(l)
	if err != nil {
		t.Fatalf("expected launcher proxy setup to succeed: %v", err)
	}
	if localProxy != nil {
		t.Fatalf("expected no local proxy forwarder for unauthenticated proxy")
	}
	if got := l.Get("proxy-server"); got != explicitProxy {
		t.Fatalf("expected launcher proxy %q, got %q", explicitProxy, got)
	}
}


func TestRandomBirthdateUsesSafeMonthAndDayRange(t *testing.T) {
	for range 200 {
		birthdate := randomBirthdate()
		parts := strings.Split(birthdate, "-")
		if len(parts) != 3 {
			t.Fatalf("expected yyyy-mm-dd format, got %q", birthdate)
		}
		month, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("expected numeric month in %q: %v", birthdate, err)
		}
		day, err := strconv.Atoi(parts[2])
		if err != nil {
			t.Fatalf("expected numeric day in %q: %v", birthdate, err)
		}
		if month < 1 || month > 12 {
			t.Fatalf("expected month range 1-12, got %d from %q", month, birthdate)
		}
		if day < 1 || day > 28 {
			t.Fatalf("expected day range 1-28, got %d from %q", day, birthdate)
		}
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

func assertHTTPClientProxy(t *testing.T, client *http.Client, expectedProxy string) {
	t.Helper()

	if client == nil {
		t.Fatalf("expected http client")
	}

	if expectedProxy == "" {
		if client.Transport != nil {
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("expected *http.Transport, got %T", client.Transport)
			}
			if transport.Proxy != nil {
				proxyURL, err := transport.Proxy(newProxyRequest(t))
				if err != nil {
					t.Fatalf("unexpected proxy lookup error: %v", err)
				}
				if proxyURL != nil {
					t.Fatalf("expected no proxy, got %q", proxyURL.String())
				}
			}
		}
		return
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatalf("expected proxy function to be configured")
	}

	proxyURL, err := transport.Proxy(newProxyRequest(t))
	if err != nil {
		t.Fatalf("unexpected proxy lookup error: %v", err)
	}
	if proxyURL == nil {
		t.Fatalf("expected proxy URL %q, got nil", expectedProxy)
	}
	if proxyURL.String() != expectedProxy {
		t.Fatalf("expected proxy URL %q, got %q", expectedProxy, proxyURL.String())
	}
}

func newProxyRequest(t *testing.T) *http.Request {
	t.Helper()

	reqURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("failed to parse request URL: %v", err)
	}

	return &http.Request{URL: reqURL}
}
