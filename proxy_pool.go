package main

import (
	"bytes"
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

const (
	ProxySourceStatic = "static"
	ProxySourceClash  = "clash"
)

type ProxyStatus struct {
	ID             string
	Source         string
	URL            string
	Node           string
	Group          string
	IP             string
	Country        string
	City           string
	Available      bool
	LastCheck      time.Time
	Error          string
	RuntimeDisabled bool
}

type ProxyAssignment struct {
	ID       string
	Source   string
	ProxyURL string
	Node     string
	Group    string
}

func (a *ProxyAssignment) Label() string {
	if a == nil {
		return ""
	}
	if a.Source == ProxySourceClash {
		return fmt.Sprintf("Clash[%s/%s]", a.Group, a.Node)
	}
	return maskProxyURL(a.ProxyURL)
}

type ProxyPool struct {
	proxies     []*ProxyStatus
	mu          sync.RWMutex
	index       int
	clash       *clashClient
	clashSelect sync.Mutex
}

type clashClient struct {
	baseURL    string
	secret     string
	httpClient *http.Client
}

func NewProxyPool(proxyURLs []string) *ProxyPool {
	pool := &ProxyPool{
		proxies: make([]*ProxyStatus, len(proxyURLs)),
	}
	for i, p := range proxyURLs {
		pool.proxies[i] = &ProxyStatus{
			ID:     fmt.Sprintf("static:%d", i),
			Source: ProxySourceStatic,
			URL:    p,
		}
	}
	return pool
}

func NewClashProxyPool(cfg *ClashConfig) (*ProxyPool, error) {
	if cfg == nil {
		return nil, fmt.Errorf("clash 配置为空")
	}
	group := strings.TrimSpace(cfg.ProxyGroup)
	if group == "" {
		return nil, fmt.Errorf("clash.proxy_group 不能为空")
	}

	transportURL, err := normalizeProxyURL(cfg.MixedProxy)
	if err != nil {
		return nil, fmt.Errorf("clash.mixed_proxy 无效: %w", err)
	}

	client, err := newClashClient(cfg.ExternalController, cfg.Secret)
	if err != nil {
		return nil, err
	}

	nodes, err := client.ListGroupNodes(group, cfg.Include, cfg.Exclude)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("clash 代理组 %q 没有可用节点", group)
	}

	pool := &ProxyPool{
		proxies: make([]*ProxyStatus, 0, len(nodes)),
		clash:   client,
	}
	for _, node := range nodes {
		pool.proxies = append(pool.proxies, &ProxyStatus{
			ID:     "clash:" + node,
			Source: ProxySourceClash,
			URL:    transportURL,
			Node:   node,
			Group:  group,
		})
	}

	return pool, nil
}

func (p *ProxyPool) TestAll() {
	Println("\n🔍 测试代理池...")

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5)

	for i := range p.proxies {
		if p.proxies[i].Source == ProxySourceClash {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			p.testProxy(idx)
		}(i)
	}
	for i := range p.proxies {
		if p.proxies[i].Source == ProxySourceClash {
			p.testProxy(i)
		}
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
	Printf("\n✅ 可用代理: %d/%d\n", available, len(p.proxies))
}

func (p *ProxyPool) testProxy(idx int) {
	p.mu.RLock()
	proxy := p.proxies[idx]
	p.mu.RUnlock()

	if proxy.Source == ProxySourceClash {
		if err := p.selectClashNode(proxy); err != nil {
			p.setProxyFailure(proxy.ID, fmt.Sprintf("切换节点失败: %v", err))
			Printf("  ❌ %s - %s\n", proxy.Node, err)
			return
		}
	}

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		p.setProxyFailure(proxy.ID, fmt.Sprintf("解析URL失败: %v", err))
		if proxy.Source == ProxySourceClash {
			Printf("  ❌ %s - %s\n", proxy.Node, err)
		} else {
			Printf("  ❌ %s - %s\n", maskProxyURL(proxy.URL), err)
		}
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 15 * time.Second,
	}

	ipInfo := p.getProxyIP(client)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://auth.openai.com/", nil)
	if err != nil {
		p.setProxyFailure(proxy.ID, err.Error())
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		p.setProxyFailure(proxy.ID, fmt.Sprintf("连接失败: %v", err))
		if proxy.Source == ProxySourceClash {
			Printf("  ❌ %s - %v\n", proxy.Node, err)
		} else {
			Printf("  ❌ %s - %v\n", maskProxyURL(proxy.URL), err)
		}
		return
	}
	defer resp.Body.Close()

	p.mu.Lock()
	proxy.Available = true
	proxy.LastCheck = time.Now()
	proxy.Error = ""
	proxy.IP = ""
	proxy.Country = ""
	proxy.City = ""
	if ipInfo != "" {
		if idx := strings.Index(ipInfo, " ("); idx > 0 {
			proxy.IP = ipInfo[:idx]
			proxy.Country = ipInfo[idx+2 : len(ipInfo)-1]
		} else {
			proxy.IP = ipInfo
		}
	}
	p.mu.Unlock()

	if proxy.Source == ProxySourceClash {
		if ipInfo != "" {
			Printf("  ✅ Clash[%s] → %s\n", proxy.Node, ipInfo)
		} else {
			Printf("  ✅ Clash[%s] (可用，状态: %d)\n", proxy.Node, resp.StatusCode)
		}
		return
	}

	if ipInfo != "" {
		Printf("  ✅ %s → %s\n", maskProxyURL(proxy.URL), ipInfo)
	} else {
		Printf("  ✅ %s (可用，状态: %d)\n", maskProxyURL(proxy.URL), resp.StatusCode)
	}
}

func (p *ProxyPool) setProxyFailure(proxyID, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, proxy := range p.proxies {
		if proxy.ID == proxyID {
			proxy.Available = false
			proxy.Error = message
			proxy.LastCheck = time.Now()
			return
		}
	}
}

func (p *ProxyPool) reactivateRuntimeFailedLocked() int {
	reactivated := 0
	for _, proxy := range p.proxies {
		if proxy.RuntimeDisabled {
			proxy.Available = true
			proxy.RuntimeDisabled = false
			proxy.Error = ""
			reactivated++
		}
	}
	return reactivated
}

func (p *ProxyPool) getProxyIP(client *http.Client) string {
	ipServices := []string{
		"https://icanhazip.com",
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
	}

	for _, service := range ipServices {
		ip := p.tryGetIP(client, service)
		if ip != "" {
			location := p.getIPLocation(ip)
			if location != "" {
				return fmt.Sprintf("%s (%s)", ip, location)
			}
			return ip
		}
	}
	return ""
}

func (p *ProxyPool) tryGetIP(client *http.Client, rawURL string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "curl/7.68.0")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	ip := strings.TrimSpace(string(body))
	if len(ip) > 0 && len(ip) < 50 {
		for _, c := range ip {
			if (c < '0' || c > '9') && c != '.' && c != ':' {
				return ""
			}
		}
		return ip
	}
	return ""
}

func (p *ProxyPool) getIPLocation(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://ip-api.com/json/"+ip+"?fields=status,country,city", nil)
	if err != nil {
		return ""
	}

	client := &http.Client{Timeout: 10 * time.Second}
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
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}

	if result.Status == "success" {
		if result.City != "" {
			return result.Country + ", " + result.City
		}
		return result.Country
	}
	return ""
}

func (p *ProxyPool) GetNextAssignment() *ProxyAssignment {
	p.mu.RLock()
	total := len(p.proxies)
	p.mu.RUnlock()
	if total == 0 {
		return nil
	}

	for attempt := 0; attempt < 2; attempt++ {
		for range total {
			candidate := p.nextAvailable()
			if candidate == nil {
				break
			}
			if candidate.Source == ProxySourceClash {
				if err := p.selectClashNode(candidate); err != nil {
					p.markRuntimeFailure(candidate.ID, fmt.Sprintf("切换节点失败: %v", err))
					continue
				}
			}
			return p.toAssignment(candidate)
		}

		if attempt == 0 {
			reactivated := p.reactivateRuntimeFailed()
			if reactivated == 0 {
				break
			}
			Printf("🔄 代理池已全部失败，重新激活 %d 个运行期失败节点后继续尝试\n", reactivated)
		}
	}

	return nil
}

func (p *ProxyPool) nextAvailable() *ProxyStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return nil
	}

	start := p.index
	for {
		proxy := p.proxies[p.index]
		p.index = (p.index + 1) % len(p.proxies)
		if proxy.Available {
			return proxy
		}
		if p.index == start {
			return nil
		}
	}
}

func (p *ProxyPool) toAssignment(proxy *ProxyStatus) *ProxyAssignment {
	if proxy == nil {
		return nil
	}
	return &ProxyAssignment{
		ID:       proxy.ID,
		Source:   proxy.Source,
		ProxyURL: proxy.URL,
		Node:     proxy.Node,
		Group:    proxy.Group,
	}
}

func (p *ProxyPool) selectClashNode(proxy *ProxyStatus) error {
	if proxy == nil || proxy.Source != ProxySourceClash {
		return nil
	}
	if p.clash == nil {
		return fmt.Errorf("clash 客户端未初始化")
	}
	p.clashSelect.Lock()
	defer p.clashSelect.Unlock()
	return p.clash.SelectNode(proxy.Group, proxy.Node)
}

func (p *ProxyPool) GetNext() string {
	assignment := p.GetNextAssignment()
	if assignment == nil {
		return ""
	}
	return assignment.ProxyURL
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

func (p *ProxyPool) PrintPoolSummary() {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.proxies) == 0 {
		return
	}

	Println("\n📊 代理池最终状态:")
	Println("====================================")

	available := 0
	failed := 0
	for _, proxy := range p.proxies {
		if proxy.Available {
			available++
		} else {
			failed++
		}
	}

	Printf("总节点数: %d | 可用: %d | 失败: %d\n", len(p.proxies), available, failed)
	Println("------------------------------------")

	for _, proxy := range p.proxies {
		status := "✅ 可用"
		if !proxy.Available {
			status = "❌ 失败"
		}

		if proxy.Source == ProxySourceClash {
			Printf("  %s Clash[%s/%s] %s\n", status, proxy.Group, proxy.Node, proxy.Error)
		} else {
			Printf("  %s %s %s\n", status, maskProxyURL(proxy.URL), proxy.Error)
		}
	}

	Println("====================================")
}

func (p *ProxyPool) reactivateRuntimeFailed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reactivateRuntimeFailedLocked()
}

func (p *ProxyPool) markRuntimeFailure(proxyID, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, proxy := range p.proxies {
		if proxy.ID == proxyID {
			proxy.Available = false
			proxy.RuntimeDisabled = true
			proxy.Error = message
			proxy.LastCheck = time.Now()
			return
		}
	}
}

func (p *ProxyPool) MarkFailed(proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if proxy.URL == proxyURL {
			proxy.Available = false
			proxy.RuntimeDisabled = true
			proxy.Error = "注册失败"
			proxy.LastCheck = time.Now()
			Printf("⚠️ 代理标记为失败: %s\n", maskProxyURL(proxyURL))
			break
		}
	}

	available := 0
	for _, proxy := range p.proxies {
		if proxy.Available {
			available++
		}
	}
	Printf("📊 代理池状态: %d/%d 可用\n", available, len(p.proxies))
}

func (p *ProxyPool) MarkFailedAssignment(assignment *ProxyAssignment) {
	if assignment == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if proxy.ID == assignment.ID {
			proxy.Available = false
			proxy.RuntimeDisabled = true
			proxy.Error = "注册失败"
			proxy.LastCheck = time.Now()
			Printf("⚠️ 代理标记为失败: %s\n", assignment.Label())
			break
		}
	}

	available := 0
	for _, proxy := range p.proxies {
		if proxy.Available {
			available++
		}
	}
	Printf("📊 代理池状态: %d/%d 可用\n", available, len(p.proxies))
}

func normalizeProxyURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("为空")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("缺少 host")
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "http"
	}
	return parsed.String(), nil
}

func newClashClient(externalController, secret string) (*clashClient, error) {
	baseURL, err := normalizeProxyURL(externalController)
	if err != nil {
		return nil, fmt.Errorf("clash.external_controller 无效: %w", err)
	}
	return &clashClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		secret:     strings.TrimSpace(secret),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *clashClient) ListGroupNodes(group string, include, exclude []string) ([]string, error) {
	respBody, err := c.doJSON(http.MethodGet, c.baseURL+"/proxies", nil)
	if err != nil {
		return nil, fmt.Errorf("获取 clash 节点失败: %w", err)
	}

	var data struct {
		Proxies map[string]struct {
			Type string   `json:"type"`
			All  []string `json:"all"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("解析 clash 节点失败: %w", err)
	}

	groupProxy, ok := data.Proxies[group]
	if !ok {
		return nil, fmt.Errorf("clash 代理组 %q 不存在", group)
	}

	filtered := filterNodeNames(groupProxy.All, include, exclude)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("clash 代理组 %q 过滤后无节点", group)
	}

	return filtered, nil
}

func (c *clashClient) SelectNode(group, node string) error {
	body, err := json.Marshal(map[string]string{"name": node})
	if err != nil {
		return err
	}
	_, err = c.doJSON(http.MethodPut, c.baseURL+"/proxies/"+url.PathEscape(group), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("切换节点 %q 失败: %w", node, err)
	}
	return nil
}

func (c *clashClient) doJSON(method, rawURL string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	return respBody, nil
}

func filterNodeNames(nodes, include, exclude []string) []string {
	includeLower := make([]string, 0, len(include))
	for _, item := range include {
		if trimmed := strings.TrimSpace(strings.ToLower(item)); trimmed != "" {
			includeLower = append(includeLower, trimmed)
		}
	}

	excludeLower := make([]string, 0, len(exclude))
	for _, item := range exclude {
		if trimmed := strings.TrimSpace(strings.ToLower(item)); trimmed != "" {
			excludeLower = append(excludeLower, trimmed)
		}
	}

	filtered := make([]string, 0, len(nodes))
	for _, node := range nodes {
		name := strings.TrimSpace(node)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)

		if len(includeLower) > 0 {
			matched := false
			for _, keyword := range includeLower {
				if strings.Contains(lower, keyword) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		excluded := false
		for _, keyword := range excludeLower {
			if strings.Contains(lower, keyword) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		filtered = append(filtered, name)
	}

	return filtered
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

type ProxyTestResult struct {
	Available bool
	IP        string
	Country   string
	City      string
	Error     string
}

func TestSingleProxy(proxyURL string) *ProxyTestResult {
	result := &ProxyTestResult{}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		result.Error = fmt.Sprintf("解析URL失败: %v", err)
		return result
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsedURL),
		},
		Timeout: 15 * time.Second,
	}

	ipServices := []string{
		"https://icanhazip.com",
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
	}

	var proxyIP string
	for _, service := range ipServices {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", service, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "curl/7.68.0")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		ip := strings.TrimSpace(string(body))
		if len(ip) > 0 && len(ip) < 50 {
			valid := true
			for _, c := range ip {
				if (c < '0' || c > '9') && c != '.' && c != ':' {
					valid = false
					break
				}
			}
			if valid {
				proxyIP = ip
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://auth.openai.com/", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("连接OpenAI失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	result.Available = true

	if proxyIP != "" {
		result.IP = proxyIP
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()

		locReq, _ := http.NewRequestWithContext(ctx2, "GET", "http://ip-api.com/json/"+proxyIP+"?fields=status,country,city", nil)
		locClient := &http.Client{Timeout: 10 * time.Second}
		locResp, err := locClient.Do(locReq)
		if err == nil {
			body, _ := io.ReadAll(locResp.Body)
			locResp.Body.Close()

			var locResult struct {
				Status  string `json:"status"`
				Country string `json:"country"`
				City    string `json:"city"`
			}
			if json.Unmarshal(body, &locResult) == nil && locResult.Status == "success" {
				result.Country = locResult.Country
				result.City = locResult.City
			}
		}
	}

	return result
}
