package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TempMailProvider 临时邮箱服务提供者
type TempMailProvider struct {
	Name        string
	GenerateURL string
	CheckURL    string
	Headers     map[string]string
	Priority    int // 优先级，数字越小优先级越高
}

// ServiceStatus 服务状态跟踪
type ServiceStatus struct {
	FailCount      int
	LastFail       time.Time
	PriorityAdjust int // 优先级调整值，越大优先级越低
	LastAdjustTime time.Time
}

// HTTPClient HTTP客户端
type HTTPClient struct {
	client           *http.Client
	serviceStatus    map[string]*ServiceStatus
	statusMutex      sync.RWMutex
	lastMailProvider string
	lastMailMutex    sync.RWMutex
}

// 临时邮箱服务列表（按优先级排序）
var tempMailProviders = []TempMailProvider{
	{
		Name:        "chatgpt.org.uk",
		GenerateURL: "https://mail.chatgpt.org.uk/api/generate-email?api_key=YOUR_API_KEY",
		CheckURL:    "https://mail.chatgpt.org.uk/api/emails?api_key=YOUR_API_KEY&email=%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
			"X-API-Key":  "YOUR_API_KEY",
		},
		Priority: 1,
	},
	{
		Name:        "Mail.tm",
		GenerateURL: "https://api.mail.tm/domains",
		CheckURL:    "https://api.mail.tm/messages",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"},
		Priority:    2,
	},
	{
		Name:        "DuckMail",
		GenerateURL: "https://api.duckmail.sbs/domains",
		CheckURL:    "https://api.duckmail.sbs/messages",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"},
		Priority:    3,
	},
	{
		Name:        "tempmail.plus",
		GenerateURL: "https://tempmail.plus/api/v1/mail",
		CheckURL:    "https://tempmail.plus/api/v1/mail/%s",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"},
		Priority:    4,
	},
}

func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client:        &http.Client{Timeout: 60 * time.Second},
		serviceStatus: make(map[string]*ServiceStatus),
	}
}

func NewHTTPClientWithProxy(proxyURL string) *HTTPClient {
	client := &http.Client{Timeout: 60 * time.Second}

	if proxyURL != "" {
		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyParsed)}
		}
	}

	return &HTTPClient{
		client:        client,
		serviceStatus: make(map[string]*ServiceStatus),
	}
}

// getServicePriority 获取服务的动态优先级（原始优先级 + 调整值）
func (c *HTTPClient) getServicePriority(name string, originalPriority int) int {
	c.statusMutex.RLock()
	defer c.statusMutex.RUnlock()

	if status, exists := c.serviceStatus[name]; exists {
		return originalPriority + status.PriorityAdjust
	}
	return originalPriority
}

// markServiceFailed 标记服务失败，连续3次失败后降低优先级
func (c *HTTPClient) markServiceFailed(name string) {
	c.statusMutex.Lock()
	defer c.statusMutex.Unlock()

	status, exists := c.serviceStatus[name]
	if !exists {
		status = &ServiceStatus{}
		c.serviceStatus[name] = status
	}

	status.FailCount++
	status.LastFail = time.Now()

	// 连续失败3次则降低优先级（增加优先级调整值）
	if status.FailCount >= 3 && status.PriorityAdjust < 10 {
		status.PriorityAdjust += 1
		status.LastAdjustTime = time.Now()
		Printf("[调整] 服务 %s 连续失败 %d 次，优先级降低（当前调整值: +%d）\n", name, status.FailCount, status.PriorityAdjust)
	}
}

// markServiceSuccess 标记服务成功，恢复优先级
func (c *HTTPClient) markServiceSuccess(name string) {
	c.statusMutex.Lock()
	defer c.statusMutex.Unlock()

	if status, exists := c.serviceStatus[name]; exists {
		status.FailCount = 0
		// 成功后恢复优先级
		if status.PriorityAdjust > 0 {
			status.PriorityAdjust = 0
			Printf("[恢复] 服务 %s 优先级已恢复\n", name)
		}
	}
}

func (c *HTTPClient) setLastMailProvider(name string) {
	c.lastMailMutex.Lock()
	defer c.lastMailMutex.Unlock()
	c.lastMailProvider = name
}

func (c *HTTPClient) getLastMailProvider() string {
	c.lastMailMutex.RLock()
	defer c.lastMailMutex.RUnlock()
	return c.lastMailProvider
}

func (c *HTTPClient) SetDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
}

// GetTempEmail 获取临时邮箱（按动态优先级轮询）
func (c *HTTPClient) GetTempEmail() (string, error) {
	Println("\n📧 正在获取临时邮箱...")

	// 创建服务列表副本并按动态优先级排序
	type providerWithPriority struct {
		provider TempMailProvider
		priority int
	}
	var sortedProviders []providerWithPriority
	for _, p := range tempMailProviders {
		sortedProviders = append(sortedProviders, providerWithPriority{
			provider: p,
			priority: c.getServicePriority(p.Name, p.Priority),
		})
	}

	// 按优先级排序（数字越小优先级越高）
	for i := 0; i < len(sortedProviders); i++ {
		for j := i + 1; j < len(sortedProviders); j++ {
			if sortedProviders[j].priority < sortedProviders[i].priority {
				sortedProviders[i], sortedProviders[j] = sortedProviders[j], sortedProviders[i]
			}
		}
	}

	// 按排序后的顺序遍历所有服务
	for _, pp := range sortedProviders {
		provider := pp.provider
		Printf("[%s] 尝试获取邮箱（优先级: %d）\n", provider.Name, pp.priority)

		var email string
		var err error

		switch provider.Name {
		case "Mail.tm":
			email, err = c.getMailTmEmail(provider)
		case "DuckMail":
			email, err = c.getDuckMailEmail(provider)
		default:
			email, err = c.getGenericEmail(provider)
		}

		if err != nil {
			Printf("[%s] 获取失败: %v\n", provider.Name, err)
			c.markServiceFailed(provider.Name)
			continue
		}

		if email != "" {
			c.markServiceSuccess(provider.Name)
			c.setLastMailProvider(provider.Name)
			Printf("[%s] ✅ 获取邮箱成功: %s\n", provider.Name, email)
			return email, nil
		}
	}

	return "", fmt.Errorf("所有临时邮箱服务都不可用")
}

// getMailTmEmail 从 Mail.tm 获取邮箱
func (c *HTTPClient) getMailTmEmail(provider TempMailProvider) (string, error) {
	// 获取可用域名
	resp, err := c.client.Get(provider.GenerateURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var domainsResp struct {
		Data []struct {
			Domain string `json:"domain"`
		} `json:"hydra:member"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &domainsResp); err != nil {
		// 尝试另一种格式
		var altResp struct {
			Data []struct {
				Domain string `json:"domain"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &altResp); len(altResp.Data) == 0 {
			return "", fmt.Errorf("获取域名失败")
		}
		domainsResp.Data = altResp.Data
	}

	if len(domainsResp.Data) == 0 {
		return "", fmt.Errorf("没有可用域名")
	}

	domain := domainsResp.Data[0].Domain
	address := fmt.Sprintf("%s@%s", randomString(10), domain)
	password := "TempPass" + randomString(8) + "!"

	// 创建账户
	createReq := map[string]string{"address": address, "password": password}
	createBody, _ := json.Marshal(createReq)

	req, _ := http.NewRequest("POST", "https://api.mail.tm/accounts", strings.NewReader(string(createBody)))
	req.Header.Set("Content-Type", "application/json")

	resp2, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 && resp2.StatusCode != 201 {
		return "", fmt.Errorf("创建账户失败: %d", resp2.StatusCode)
	}

	// 保存账户信息用于后续邮件检查
	currentMailService = &MailService{
		Name:   "Mail.tm",
		Email:  address,
		Domain: domain,
	}
	c.setMailTmPassword(password)

	Printf("[Mail.tm] 已创建账户: %s\n", address)
	return address, nil
}

func (c *HTTPClient) getDuckMailEmail(provider TempMailProvider) (string, error) {
	resp, err := c.client.Get(provider.GenerateURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var domainsResp struct {
		Data []struct {
			Domain string `json:"domain"`
		} `json:"hydra:member"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &domainsResp); err != nil {
		var altResp struct {
			Data []struct {
				Domain string `json:"domain"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &altResp); len(altResp.Data) == 0 {
			return "", fmt.Errorf("获取域名失败")
		}
		domainsResp.Data = altResp.Data
	}

	if len(domainsResp.Data) == 0 {
		return "", fmt.Errorf("没有可用域名")
	}

	domain := domainsResp.Data[0].Domain
	address := fmt.Sprintf("%s@%s", randomString(10), domain)
	password := "DuckPass" + randomString(8) + "!"

	createReq := map[string]interface{}{
		"address":   address,
		"password":  password,
		"expiresIn": 3600000,
	}
	createBody, _ := json.Marshal(createReq)

	req, _ := http.NewRequest("POST", "https://api.duckmail.sbs/accounts", strings.NewReader(string(createBody)))
	req.Header.Set("Content-Type", "application/json")

	resp2, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 && resp2.StatusCode != 201 {
		body2, _ := io.ReadAll(resp2.Body)
		return "", fmt.Errorf("创建账户失败: %d - %s", resp2.StatusCode, string(body2))
	}

	currentMailService = &MailService{
		Name:   "DuckMail",
		Email:  address,
		Domain: domain,
	}
	c.setDuckMailPassword(password)

	Printf("[DuckMail] 已创建账户: %s\n", address)
	return address, nil
}

// mailTmPassword 存储 Mail.tm 密码
var mailTmPassword string
var mailTmPasswordMutex sync.RWMutex

var duckMailPassword string
var duckMailPasswordMutex sync.RWMutex

func (c *HTTPClient) setMailTmPassword(password string) {
	mailTmPasswordMutex.Lock()
	defer mailTmPasswordMutex.Unlock()
	mailTmPassword = password
}

func (c *HTTPClient) getMailTmPassword() string {
	mailTmPasswordMutex.RLock()
	defer mailTmPasswordMutex.RUnlock()
	return mailTmPassword
}

func (c *HTTPClient) setDuckMailPassword(password string) {
	duckMailPasswordMutex.Lock()
	defer duckMailPasswordMutex.Unlock()
	duckMailPassword = password
}

func (c *HTTPClient) getDuckMailPassword() string {
	duckMailPasswordMutex.RLock()
	defer duckMailPasswordMutex.RUnlock()
	return duckMailPassword
}

// getGenericEmail 通用邮箱获取方法
func (c *HTTPClient) getGenericEmail(provider TempMailProvider) (string, error) {
	req, err := http.NewRequest("GET", provider.GenerateURL, nil)
	if err != nil {
		return "", err
	}

	for k, v := range provider.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	Printf("[%s] API响应: %s\n", provider.Name, string(body)[:min(200, len(body))])

	// 尝试多种解析格式
	var result1 struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(body, &result1); result1.Email != "" {
		return result1.Email, nil
	}

	var result2 struct {
		Success bool `json:"success"`
		Data    struct {
			Email string `json:"email"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &result2); result2.Data.Email != "" {
		return result2.Data.Email, nil
	}

	var result3 struct {
		Mail string `json:"mail"`
	}
	if json.Unmarshal(body, &result3); result3.Mail != "" {
		return result3.Mail, nil
	}

	return "", fmt.Errorf("无法解析邮箱")
}

type MailService struct {
	Name   string
	Email  string
	Token  string
	Domain string
}

var currentMailService *MailService

// CheckEmail 检查邮件（轮询所有服务）
func (c *HTTPClient) CheckEmail(email string) (string, error) {
	maxRetries := 30

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("无效的邮箱格式")
	}
	login, domain := parts[0], parts[1]
	Printf("📬 检查邮箱: %s (login=%s, domain=%s)\n", email, login, domain)

	for i := 0; i < maxRetries; i++ {
		if currentMailService != nil && currentMailService.Name == "Mail.tm" {
			if link := c.checkMailTm(); link != "" {
				return link, nil
			}
		}
		if currentMailService != nil && currentMailService.Name == "DuckMail" {
			if link := c.checkDuckMail(); link != "" {
				return link, nil
			}
		}

		for _, provider := range tempMailProviders {
			if provider.Name == "Mail.tm" || provider.Name == "DuckMail" {
				continue
			}

			var link string
			if provider.CheckURL != "" {
				if strings.Contains(provider.CheckURL, "%s") {
					apiURL := fmt.Sprintf(provider.CheckURL, url.QueryEscape(email))
					link = c.checkGenericMail(provider, apiURL)
				}
			}

			if link != "" {
				return link, nil
			}
		}

		Printf("  ⏳ 等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(5 * time.Second)
	}

	provider := c.getLastMailProvider()
	if provider != "" {
		c.markServiceFailed(provider)
		Printf("[超时] 服务 %s 等待验证邮件超时，已标记失败\n", provider)
	}

	return "", fmt.Errorf("等待验证邮件超时")
}

// checkMailTm 检查 Mail.tm 邮件
func (c *HTTPClient) checkMailTm() string {
	if currentMailService == nil || currentMailService.Email == "" {
		return ""
	}

	password := c.getMailTmPassword()
	if password == "" {
		return ""
	}

	token := c.getMailTmToken(currentMailService.Email, password)
	if token == "" {
		return ""
	}

	messages := c.getMailTmMessages(token)
	for _, msg := range messages {
		if link := c.checkMailTmMessage(token, msg.ID); link != "" {
			return link
		}
	}
	return ""
}

func (c *HTTPClient) getMailTmToken(email, password string) string {
	reqBody := map[string]string{"address": email, "password": password}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", "https://api.mail.tm/token", strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
	}
	respBody, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(respBody, &result); result.Token != "" {
		return result.Token
	}
	return ""
}

func (c *HTTPClient) getMailTmMessages(token string) []struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
} {
	req, err := http.NewRequest("GET", "https://api.mail.tm/messages", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var messages []struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &messages)
	return messages
}

func (c *HTTPClient) checkMailTmMessage(token, messageID string) string {
	req, err := http.NewRequest("GET", "https://api.mail.tm/messages/"+messageID, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var msg struct {
		Subject string `json:"subject"`
		Text    string `json:"text"`
		HTML    string `json:"html"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return ""
	}

	return c.checkMailContent(msg.Subject, msg.HTML, msg.Text, "")
}

func (c *HTTPClient) checkDuckMail() string {
	if currentMailService == nil || currentMailService.Email == "" {
		return ""
	}

	password := c.getDuckMailPassword()
	if password == "" {
		return ""
	}

	token := c.getDuckMailToken(currentMailService.Email, password)
	if token == "" {
		return ""
	}

	messages := c.getDuckMailMessages(token)
	for _, msg := range messages {
		if link := c.checkDuckMailMessage(token, msg.ID); link != "" {
			return link
		}
	}
	return ""
}

func (c *HTTPClient) getDuckMailToken(email, password string) string {
	reqBody := map[string]string{"address": email, "password": password}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", "https://api.duckmail.sbs/token", strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
	}
	respBody, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(respBody, &result); result.Token != "" {
		return result.Token
	}
	return ""
}

func (c *HTTPClient) getDuckMailMessages(token string) []struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
} {
	req, err := http.NewRequest("GET", "https://api.duckmail.sbs/messages", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var messages []struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &messages)
	return messages
}

func (c *HTTPClient) checkDuckMailMessage(token, messageID string) string {
	req, err := http.NewRequest("GET", "https://api.duckmail.sbs/messages/"+messageID, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var msg struct {
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    []string `json:"html"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return ""
	}

	htmlContent := ""
	if len(msg.HTML) > 0 {
		htmlContent = msg.HTML[0]
	}

	return c.checkMailContent(msg.Subject, htmlContent, msg.Text, "")
}

// checkGenericMail 通用邮件检查
func (c *HTTPClient) checkGenericMail(provider TempMailProvider, apiURL string) string {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return ""
	}

	for k, v := range provider.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Emails []struct {
				Subject     string `json:"subject"`
				Content     string `json:"content"`
				HtmlContent string `json:"html_content"`
				Body        string `json:"body"`
			} `json:"emails"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		var directEmails []struct {
			Subject     string `json:"subject"`
			Content     string `json:"content"`
			HtmlContent string `json:"html_content"`
			Body        string `json:"body"`
		}
		if json.Unmarshal(body, &directEmails) == nil {
			for _, mail := range directEmails {
				if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
					return link
				}
			}
		}
		return ""
	}

	for _, mail := range response.Data.Emails {
		if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
			return link
		}
	}

	return ""
}

func (c *HTTPClient) checkMailContent(subject, htmlContent, content, body string) string {
	subjectLower := strings.ToLower(subject)
	if strings.Contains(subjectLower, "verify") ||
		strings.Contains(subjectLower, "openai") ||
		strings.Contains(subjectLower, "chatgpt") ||
		strings.Contains(subjectLower, "验证") ||
		strings.Contains(subjectLower, "confirm") {

		fullContent := htmlContent
		if fullContent == "" {
			fullContent = content
		}
		if fullContent == "" {
			fullContent = body
		}

		Printf("📧 找到验证邮件: %s\n", subject)
		if link := extractVerifyLink(fullContent); link != "" {
			Printf("✅ 提取到验证链接: %s\n", link)
			return link
		}
	}
	return ""
}

func extractVerifyLink(content string) string {
	if otp := extractOTPCode(content); otp != "" {
		return "OTP:" + otp
	}

	patterns := []string{
		`https://auth.openai.com/authorize?`,
		`https://chat.open.ai.com/auth/`,
		`https://platform.openai.com/`,
		`https://auth.openai.com/`,
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			end := idx
			for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
				end++
			}
			if end > idx {
				link := content[idx:end]
				link = strings.ReplaceAll(link, "&amp;", "&")
				return link
			}
		}
	}

	if idx := strings.Index(content, "token="); idx != -1 {
		start := idx
		for start > 0 && content[start-1] != '"' && content[start-1] != '\'' && content[start-1] != ' ' {
			start--
		}
		end := idx
		for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
			end++
		}
		return content[start:end]
	}

	return ""
}

func extractOTPCode(content string) string {
	patterns := []string{
		"Your ChatGPT code is ",
		"Your verification code is ",
		"verification code: ",
		"code: ",
		"Your code is ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			start := idx + len(pattern)
			code := ""
			for i := 0; i < 20 && start+i < len(content); i++ {
				c := content[start+i]
				if c >= '0' && c <= '9' {
					code += string(c)
					if len(code) == 6 {
						return code
					}
				} else if c == ' ' || c == '\n' || c == '\r' || c == '<' {
					if len(code) >= 4 {
						return code
					}
					continue
				} else if len(code) > 0 {
					break
				}
			}
			if len(code) >= 4 {
				return code
			}
		}
	}

	// 尝试直接提取6位数字
	for i := 0; i < len(content)-5; i++ {
		if content[i] >= '0' && content[i] <= '9' {
			code := ""
			for j := 0; j < 6 && i+j < len(content); j++ {
				if content[i+j] >= '0' && content[i+j] <= '9' {
					code += string(content[i+j])
				}
			}
			if len(code) == 6 {
				return code
			}
		}
	}

	return ""
}
