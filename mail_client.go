package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// HTTPClient HTTP客户端
type HTTPClient struct {
	client        *http.Client
	apiKey        string
	serviceStatus map[string]*ServiceStatus
	statusMutex   sync.RWMutex
}

// ServiceStatus 服务状态跟踪
type ServiceStatus struct {
	FailCount int
	LastFail  time.Time
}

// chatgptOrgUKAPIKey 存储 API Key（可通过环境变量或文件设置）
var chatgptOrgUKAPIKey string
var apiKeyOnce sync.Once

func getChatGPTOrgUKAPIKey() string {
	apiKeyOnce.Do(func() {
		// 优先从环境变量获取
		chatgptOrgUKAPIKey = os.Getenv("CHATGPT_ORG_UK_API_KEY")
		if chatgptOrgUKAPIKey != "" {
			return
		}
		// 其次从 .gptapi 文件获取
		data, err := os.ReadFile(".gptapi")
		if err == nil {
			chatgptOrgUKAPIKey = strings.TrimSpace(string(data))
		}
	})
	return chatgptOrgUKAPIKey
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

func (c *HTTPClient) SetDefaultHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://mail.chatgpt.org.uk")
}

// GetTempEmail 获取临时邮箱（仅使用 chatgpt.org.uk）
func (c *HTTPClient) GetTempEmail() (string, error) {
	Println("\n📧 正在获取临时邮箱...")

	apiKey := getChatGPTOrgUKAPIKey()
	var apiURL string
	var headers map[string]string

	if apiKey != "" {
		Printf("[chatgpt.org.uk] 使用专属 API Key: %s...\n", apiKey[:min(10, len(apiKey))])
		apiURL = fmt.Sprintf("https://mail.chatgpt.org.uk/api/generate-email?api_key=YOUR_API_KEY", apiKey)
		headers = map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
			"X-API-Key":  apiKey,
		}
	} else {
		Println("[chatgpt.org.uk] 使用全球共享配额")
		apiURL = "https://mail.chatgpt.org.uk/api/generate-email?api_key=YOUR_API_KEY"
		headers = map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
		}
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	Printf("[chatgpt.org.uk] API响应: %s\n", string(body)[:min(200, len(body))])

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Email string `json:"email"`
		} `json:"data"`
		Usage struct {
			RemainingTotal int `json:"remaining_total"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if !result.Success {
		return "", fmt.Errorf("API 返回失败")
	}

	if result.Data.Email == "" {
		return "", fmt.Errorf("未获取到邮箱地址")
	}

	if result.Usage.RemainingTotal > 0 {
		Printf("[chatgpt.org.uk] 剩余配额: %d\n", result.Usage.RemainingTotal)
	}

	Printf("[chatgpt.org.uk] ✅ 获取邮箱成功: %s\n", result.Data.Email)
	return result.Data.Email, nil
}

// CheckEmail 检查邮件
func (c *HTTPClient) CheckEmail(email string) (string, error) {
	return c.CheckEmailSkipUsed(email, nil)
}

// CheckEmailSkipUsed 检查邮件，跳过已使用的OTP
func (c *HTTPClient) CheckEmailSkipUsed(email string, usedOTPs map[string]bool) (string, error) {
	maxRetries := 30

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("无效的邮箱格式")
	}
	Printf("📬 检查邮箱: %s\n", email)

	apiKey := getChatGPTOrgUKAPIKey()
	var checkURL string
	var headers map[string]string

	if apiKey != "" {
		checkURL = fmt.Sprintf("https://mail.chatgpt.org.uk/api/emails?api_key=YOUR_API_KEY&email=%s", apiKey, url.QueryEscape(email))
		headers = map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
			"X-API-Key":  apiKey,
		}
	} else {
		checkURL = fmt.Sprintf("https://mail.chatgpt.org.uk/api/emails?api_key=YOUR_API_KEY&email=%s", url.QueryEscape(email))
		headers = map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
		}
	}

	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequest("GET", checkURL, nil)
		if err != nil {
			return "", err
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			Printf("  ⏳ 请求失败，重试... (%d/%d)\n", i+1, maxRetries)
			time.Sleep(5 * time.Second)
			continue
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
			Printf("  ⏳ 解析失败，重试... (%d/%d)\n", i+1, maxRetries)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, mail := range response.Data.Emails {
			if link := c.checkMailContentSkipUsed(mail.Subject, mail.HtmlContent, mail.Content, mail.Body, usedOTPs); link != "" {
				return link, nil
			}
		}

		Printf("  ⏳ 等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证邮件超时")
}

func (c *HTTPClient) checkMailContentSkipUsed(subject, htmlContent, content, body string, usedOTPs map[string]bool) string {
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
		link := extractVerifyLinkSkipUsed(fullContent, usedOTPs)
		if link != "" {
			Printf("✅ 提取到验证链接: %s\n", link)
			return link
		}
		Printf("  邮件中的验证码已使用，继续查找...\n")
	}
	return ""
}

func (c *HTTPClient) checkMailContent(subject, htmlContent, content, body string) string {
	return c.checkMailContentSkipUsed(subject, htmlContent, content, body, nil)
}

func extractVerifyLinkSkipUsed(content string, usedOTPs map[string]bool) string {
	if otp := extractOTPCode(content); otp != "" {
		if usedOTPs != nil && usedOTPs[otp] {
			Printf("  跳过已使用的OTP: %s\n", otp)
		} else {
			return "OTP:" + otp
		}
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

	return ""
}

func extractVerifyLink(content string) string {
	return extractVerifyLinkSkipUsed(content, nil)
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
