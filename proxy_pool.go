package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ProxyStatus struct {
	URL       string
	IP        string
	Country   string
	City      string
	Available bool
	LastCheck time.Time
	Error     string
}

type ProxyPool struct {
	proxies []*ProxyStatus
	mu      sync.RWMutex
	index   int
}

func NewProxyPool(proxyURLs []string) *ProxyPool {
	pool := &ProxyPool{
		proxies: make([]*ProxyStatus, len(proxyURLs)),
	}
	for i, p := range proxyURLs {
		pool.proxies[i] = &ProxyStatus{URL: p}
	}
	return pool
}

func (p *ProxyPool) TestAll() {
	fmt.Println("\n🔍 测试代理池...")

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5)

	for i := range p.proxies {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			p.testProxy(idx)
		}(i)
	}
	wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	available := 0
	for _, proxy := range p.proxies {
		if proxy.Available {
			available++
		}
	}
	fmt.Printf("\n✅ 可用代理: %d/%d\n", available, len(p.proxies))
}

func (p *ProxyPool) testProxy(idx int) {
	proxy := p.proxies[idx]

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		proxy.Error = fmt.Sprintf("解析URL失败: %v", err)
		fmt.Printf("  ❌ %s - %s\n", maskProxyURL(proxy.URL), proxy.Error)
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 15 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://auth.openai.com/", nil)
	if err != nil {
		proxy.Error = err.Error()
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		proxy.Error = fmt.Sprintf("连接失败: %v", err)
		fmt.Printf("  ❌ %s - %s\n", maskProxyURL(proxy.URL), proxy.Error)
		return
	}
	defer resp.Body.Close()

	ipInfo := p.getProxyIP(client)
	if ipInfo != "" {
		fmt.Printf("  ✅ %s → %s\n", maskProxyURL(proxy.URL), ipInfo)
	} else {
		fmt.Printf("  ✅ %s (状态: %d)\n", maskProxyURL(proxy.URL), resp.StatusCode)
	}

	p.mu.Lock()
	proxy.Available = true
	proxy.LastCheck = time.Now()
	proxy.Error = ""
	if ipInfo != "" {
		if idx := strings.Index(ipInfo, " ("); idx > 0 {
			proxy.IP = ipInfo[:idx]
			proxy.Country = ipInfo[idx+2 : len(ipInfo)-1]
		} else {
			proxy.IP = ipInfo
		}
	}
	p.mu.Unlock()
}

func (p *ProxyPool) getProxyIP(client *http.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://ip-api.com/json?fields=status,country,city,query", nil)
	if err != nil {
		return ""
	}

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var result struct {
		Status  string `json:"status"`
		Country string `json:"country"`
		City    string `json:"city"`
		Query   string `json:"query"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}

	if result.Status == "success" {
		country := result.Country
		if result.City != "" {
			country = result.Country + ", " + result.City
		}
		return fmt.Sprintf("%s (%s)", result.Query, country)
	}
	return ""
}

func (p *ProxyPool) GetNext() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return ""
	}

	startIdx := p.index
	for {
		proxy := p.proxies[p.index]
		p.index = (p.index + 1) % len(p.proxies)

		if proxy.Available {
			return proxy.URL
		}

		if p.index == startIdx {
			for _, pr := range p.proxies {
				if pr.Available {
					return pr.URL
				}
			}
			return p.proxies[0].URL
		}
	}
}

func (p *ProxyPool) GetAvailableCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, proxy := range p.proxies {
		if proxy.Available {
			count++
		}
	}
	return count
}

func (p *ProxyPool) MarkFailed(proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if proxy.URL == proxyURL {
			proxy.Available = false
			proxy.Error = "注册失败"
			break
		}
	}
}

func maskProxyURL(proxyURL string) string {
	if strings.Contains(proxyURL, "@") {
		parts := strings.Split(proxyURL, "@")
		if len(parts) == 2 {
			protocol := ""
			if idx := strings.Index(parts[0], "://"); idx != -1 {
				protocol = parts[0][:idx+3]
				parts[0] = parts[0][idx+3:]
			}
			if colonIdx := strings.Index(parts[0], ":"); colonIdx != -1 {
				user := parts[0][:colonIdx]
				return protocol + user + ":***@" + parts[1]
			}
		}
	}
	return proxyURL
}
