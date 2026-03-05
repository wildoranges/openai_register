package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// LocalProxyForwarder 本地代理转发器 - 用于处理需要认证的代理
type LocalProxyForwarder struct {
	listener   net.Listener
	targetURL  *url.URL
	authHeader string
}

// NewLocalProxyForwarder 创建本地代理转发器
func NewLocalProxyForwarder(proxyURL string) (*LocalProxyForwarder, error) {
	targetURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理URL失败: %v", err)
	}

	// 构建Proxy-Authorization头
	var authHeader string
	if targetURL.User != nil {
		password, _ := targetURL.User.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(targetURL.User.Username() + ":" + password))
		authHeader = "Basic " + auth
	}

	return &LocalProxyForwarder{
		targetURL:  targetURL,
		authHeader: authHeader,
	}, nil
}

// Start 启动本地代理
func (lpf *LocalProxyForwarder) Start() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("启动本地代理监听失败: %v", err)
	}
	lpf.listener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go lpf.handleConnection(conn)
		}
	}()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return "", err
	}

	return "127.0.0.1:" + port, nil
}

// handleConnection 处理连接
func (lpf *LocalProxyForwarder) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// 读取客户端请求
	buf := make([]byte, 4096)
	n, err := clientConn.Read(buf)
	if err != nil {
		return
	}

	request := string(buf[:n])

	// 解析请求方法和目标
	lines := strings.Split(request, "\r\n")
	if len(lines) == 0 {
		return
	}

	// 检查是否是CONNECT请求
	if strings.HasPrefix(lines[0], "CONNECT") {
		// 对于CONNECT，直接连接目标服务器
		parts := strings.Fields(lines[0])
		if len(parts) < 2 {
			return
		}
		targetAddr := parts[1]

		// 连接到目标代理
		proxyConn, err := net.Dial("tcp", lpf.targetURL.Host)
		if err != nil {
			return
		}
		defer proxyConn.Close()

		// 发送带认证的CONNECT请求到上级代理
		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)
		if lpf.authHeader != "" {
			connectReq += fmt.Sprintf("Proxy-Authorization: %s\r\n", lpf.authHeader)
		}
		connectReq += "\r\n"

		proxyConn.Write([]byte(connectReq))

		// 读取代理响应
		respBuf := make([]byte, 1024)
		proxyN, err := proxyConn.Read(respBuf)
		if err != nil {
			return
		}

		// 检查响应是否成功
		resp := string(respBuf[:proxyN])
		if !strings.Contains(resp, "200") {
			fmt.Printf("代理连接失败: %s\n", resp)
			return
		}

		// 向客户端返回成功
		clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

		// 双向转发数据
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := proxyConn.Read(buf)
				if err != nil {
					return
				}
				clientConn.Write(buf[:n])
			}
		}()

		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			proxyConn.Write(buf[:n])
		}
	} else {
		// 对于HTTP请求，转发到代理
		proxyConn, err := net.Dial("tcp", lpf.targetURL.Host)
		if err != nil {
			return
		}
		defer proxyConn.Close()

		// 添加Proxy-Authorization头
		if lpf.authHeader != "" && !strings.Contains(request, "Proxy-Authorization") {
			// 在第一行后插入认证头
			newRequest := lines[0] + "\r\nProxy-Authorization: " + lpf.authHeader + "\r\n"
			for i := 1; i < len(lines); i++ {
				newRequest += lines[i] + "\r\n"
			}
			proxyConn.Write([]byte(newRequest))
		} else {
			proxyConn.Write(buf[:n])
		}

		// 双向转发
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := proxyConn.Read(buf)
				if err != nil {
					return
				}
				clientConn.Write(buf[:n])
			}
		}()

		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			proxyConn.Write(buf[:n])
		}
	}
}

// Stop 停止本地代理
func (lpf *LocalProxyForwarder) Stop() {
	if lpf.listener != nil {
		lpf.listener.Close()
	}
}

// Config 配置结构
type Config struct {
	Proxy     string `json:"proxy"`      // 代理地址，如 http://proxy-host:port
	Headless  bool   `json:"headless"`   // 是否无头模式
	Timeout   int    `json:"timeout"`    // 超时时间(秒)
	Debug     bool   `json:"debug"`      // 调试模式
	OutputDir string `json:"output_dir"` // 输出目录
	Count     int    `json:"count"`      // 注册账号数量
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		Headless:  true,
		Timeout:   60,
		Debug:     false,
		OutputDir: "/openai_register/creds",
		Count:     1,
	}
}

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	config := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		// 配置文件不存在，使用默认配置
		return config, nil
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	return config, nil
}

// SaveConfig 保存配置到文件
func SaveConfig(config *Config, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// TempMailProvider 临时邮箱服务
type TempMailProvider struct {
	Name        string
	GenerateURL string
	CheckURL    string
	Headers     map[string]string
}

var tempMailProviders = []TempMailProvider{
	// 主要服务 - 与zai2api相同，经过验证可靠
	{
		Name:        "chatgpt.org.uk",
		GenerateURL: "https://mail.chatgpt.org.uk/api/generate-email",
		CheckURL:    "https://mail.chatgpt.org.uk/api/emails?email=%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
		},
	},
	// 备用服务
	{
		Name:        "tempmail.plus",
		GenerateURL: "https://tempmail.plus/api/v1/mail",
		CheckURL:    "https://tempmail.plus/api/v1/mail/%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
		},
	},
}

// AccountCredentials 账号凭证
type AccountCredentials struct {
	Email        string    `json:"email"`
	Password     string    `json:"password"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token"`
	SessionID    string    `json:"session_id"`
	UserID       string    `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// HTTPClient HTTP客户端
type HTTPClient struct {
	client *http.Client
}

func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewHTTPClientWithProxy 创建带代理的HTTP客户端
func NewHTTPClientWithProxy(proxyURL string) *HTTPClient {
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	if proxyURL != "" {
		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyParsed),
			}
		}
	}

	return &HTTPClient{client: client}
}

func (c *HTTPClient) SetDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("DNT", "1")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("sec-ch-ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Linux"`)
}

// GetTempEmail 获取临时邮箱 - 优先使用chatgpt.org.uk (与zai2api相同)
func (c *HTTPClient) GetTempEmail() (string, error) {
	// 1. 优先尝试 chatgpt.org.uk 等主要服务 (zai2api使用的服务)
	for _, provider := range tempMailProviders {
		req, err := http.NewRequest("GET", provider.GenerateURL, nil)
		if err != nil {
			continue
		}

		for k, v := range provider.Headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			fmt.Printf("[%s] 请求失败: %v\n", provider.Name, err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("[%s] API响应: %s\n", provider.Name, string(body))

		// 尝试多种格式解析
		var result1 struct {
			Email string `json:"email"`
		}
		var result2 struct {
			Success bool `json:"success"`
			Data    struct {
				Email string `json:"email"`
			} `json:"data"`
		}
		var result3 struct {
			Mail string `json:"mail"`
		}

		if err := json.Unmarshal(body, &result2); err == nil && result2.Data.Email != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result2.Data.Email)
			return result2.Data.Email, nil
		}
		if err := json.Unmarshal(body, &result1); err == nil && result1.Email != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result1.Email)
			return result1.Email, nil
		}
		if err := json.Unmarshal(body, &result3); err == nil && result3.Mail != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result3.Mail)
			return result3.Mail, nil
		}
	}

	// 2. 尝试 1secmail API (备用)
	if email := c.get1secmailEmail(); email != "" {
		fmt.Printf("[1secmail] 获取邮箱: %s\n", email)
		return email, nil
	}

	// 3. 尝试 Mail.tm API (最后备选)
	if email := c.getMailTmEmail(); email != "" {
		fmt.Printf("[Mail.tm] 获取邮箱: %s\n", email)
		return email, nil
	}

	return "", fmt.Errorf("所有临时邮箱服务都不可用")
}

// get1secmailEmail 使用1secmail获取邮箱
func (c *HTTPClient) get1secmailEmail() string {
	// 获取随机邮箱
	resp, err := c.client.Get("https://www.1secmail.com/api/v1/?action=genRandomMailbox&count=1")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var emails []string
	if err := json.Unmarshal(body, &emails); err != nil || len(emails) == 0 {
		return ""
	}

	return emails[0]
}

// getMailTmEmail 使用Mail.tm获取邮箱
func (c *HTTPClient) getMailTmEmail() string {
	// 1. 获取域名列表
	resp, err := c.client.Get("https://api.mail.tm/domains")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var domainsResp struct {
		Data []struct {
			Domain string `json:"domain"`
		} `json:"hydra:member"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&domainsResp); err != nil || len(domainsResp.Data) == 0 {
		// 尝试另一种格式
		body, _ := io.ReadAll(resp.Body)
		var altResp struct {
			Data []struct {
				Domain string `json:"domain"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &altResp); len(altResp.Data) == 0 {
			return ""
		}
		domainsResp.Data = altResp.Data
	}

	domain := domainsResp.Data[0].Domain
	address := fmt.Sprintf("%s@%s", randomString(10), domain)

	// 2. 创建账号
	createReq := map[string]string{"address": address, "password": "TempPass123!"}
	createBody, _ := json.Marshal(createReq)

	req, _ := http.NewRequest("POST", "https://api.mail.tm/accounts", strings.NewReader(string(createBody)))
	req.Header.Set("Content-Type", "application/json")

	resp2, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 && resp2.StatusCode != 201 {
		return ""
	}

	return address
}

// MailService 邮箱服务信息
type MailService struct {
	Name   string
	Email  string
	Token  string // 用于Mail.tm等需要认证的服务
	Domain string
}

// currentMailService 当前使用的邮箱服务
var currentMailService *MailService

// CheckEmail 检查邮箱获取验证链接 - 支持多种服务
func (c *HTTPClient) CheckEmail(email string) (string, error) {
	maxRetries := 60

	// 解析邮箱域名
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("无效的邮箱格式")
	}
	login, domain := parts[0], parts[1]
	fmt.Printf("检查邮箱: %s (login=%s, domain=%s)\n", email, login, domain)

	for i := 0; i < maxRetries; i++ {
		// 优先尝试 chatgpt.org.uk 等主要服务
		for _, provider := range tempMailProviders {
			apiURL := fmt.Sprintf(provider.CheckURL, url.QueryEscape(email))
			req, err := http.NewRequest("GET", apiURL, nil)
			if err != nil {
				continue
			}

			for k, v := range provider.Headers {
				req.Header.Set(k, v)
			}

			resp, err := c.client.Do(req)
			if err != nil {
				fmt.Printf("[%s] 检查邮件失败: %v\n", provider.Name, err)
				continue
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// 每10次打印一次调试信息
			if i%10 == 0 {
				fmt.Printf("[%s] 检查结果: %s\n", provider.Name, string(body)[:min(200, len(body))])
			}

			// 解析邮件列表 - 兼容多种格式
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
				// 尝试直接数组格式
				var directEmails []struct {
					Subject     string `json:"subject"`
					Content     string `json:"content"`
					HtmlContent string `json:"html_content"`
					Body        string `json:"body"`
				}
				if json.Unmarshal(body, &directEmails) == nil {
					for _, mail := range directEmails {
						if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
							return link, nil
						}
					}
				}
				continue
			}

			// 查找OpenAI验证邮件
			for _, mail := range response.Data.Emails {
				if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
					return link, nil
				}
			}
		}

		// 尝试1secmail
		if link := c.check1secmail(login, domain); link != "" {
			return link, nil
		}

		fmt.Printf("  等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证邮件超时")
}

// checkMailContent 检查邮件内容是否包含验证链接
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

		fmt.Printf("找到验证邮件: %s\n", subject)
		if link := extractVerifyLink(fullContent); link != "" {
			fmt.Printf("提取到验证链接: %s\n", link)
			return link
		}
	}
	return ""
}

// check1secmail 检查1secmail邮箱
func (c *HTTPClient) check1secmail(login, domain string) string {
	apiURL := fmt.Sprintf("https://www.1secmail.com/api/v1/?action=getMessages&login=%s&domain=%s", login, domain)

	resp, err := c.client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var messages []struct {
		ID      int    `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Date    string `json:"date"`
	}

	if err := json.Unmarshal(body, &messages); err != nil || len(messages) == 0 {
		return ""
	}

	for _, msg := range messages {
		subject := strings.ToLower(msg.Subject)
		if strings.Contains(subject, "verify") ||
			strings.Contains(subject, "openai") ||
			strings.Contains(subject, "chatgpt") ||
			strings.Contains(subject, "验证") {

			detailURL := fmt.Sprintf("https://www.1secmail.com/api/v1/?action=readMessage&login=%s&domain=%s&id=%d", login, domain, msg.ID)
			detailResp, err := c.client.Get(detailURL)
			if err != nil {
				continue
			}
			detailBody, _ := io.ReadAll(detailResp.Body)
			detailResp.Body.Close()

			var detail struct {
				Body string `json:"body"`
				Text string `json:"textBody"`
				HTML string `json:"htmlBody"`
			}

			if json.Unmarshal(detailBody, &detail) == nil {
				content := detail.HTML
				if content == "" {
					content = detail.Body
				}
				if content == "" {
					content = detail.Text
				}
				if link := extractVerifyLink(content); link != "" {
					return link
				}
			}
		}
	}

	return ""
}

// extractVerifyLink 从邮件内容提取验证链接或OTP码
func extractVerifyLink(content string) string {
	// 优先检查OTP码格式: "Your ChatGPT code is XXXXXX"
	if otp := extractOTPCode(content); otp != "" {
		return "OTP:" + otp
	}

	// 查找OpenAI验证链接
	patterns := []string{
		`https://auth.openai.com/authorize?`,
		`https://chat.openai.com/auth/`,
		`https://platform.openai.com/`,
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			// 提取完整URL
			end := idx
			for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
				end++
			}
			if end > idx {
				link := content[idx:end]
				// 处理HTML编码
				link = strings.ReplaceAll(link, "&amp;", "&")
				return link
			}
		}
	}

	// 查找包含token的链接
	if idx := strings.Index(content, "token="); idx != -1 {
		start := idx
		// 向前查找URL开头
		for start > 0 && content[start-1] != '"' && content[start-1] != '\'' && content[start-1] != ' ' {
			start--
		}
		// 向后查找URL结尾
		end := idx
		for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
			end++
		}
		return content[start:end]
	}

	return ""
}

// extractOTPCode 从邮件内容提取OTP验证码
func extractOTPCode(content string) string {
	// 模式1: "Your ChatGPT code is XXXXXX"
	patterns := []string{
		"Your ChatGPT code is ",
		"Your verification code is ",
		"verification code: ",
		"code: ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			start := idx + len(pattern)
			// 提取6位数字
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
					// 遇到非数字字符且已开始收集，停止
					break
				}
			}
			if len(code) >= 4 {
				return code
			}
		}
	}

	// 模式2: 查找任意6位连续数字
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

// GeneratePassword 生成随机密码
func GeneratePassword() string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	length := 16 + rand.Intn(8)
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// randomString 生成随机字符串
func randomString(length int) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// BrowserRegister 浏览器自动化注册
type BrowserRegister struct {
	browser     *rod.Browser
	httpClient  *HTTPClient
	config      *Config
	fingerprint *DeviceFingerprint
}

func NewBrowserRegister(config *Config) *BrowserRegister {
	return &BrowserRegister{
		httpClient:  NewHTTPClientWithProxy(config.Proxy),
		config:      config,
		fingerprint: generateDeviceFingerprint(),
	}
}

func (br *BrowserRegister) openBrowser() (func(), error) {
	path, found := launcher.LookPath()
	if !found {
		return nil, fmt.Errorf("未找到系统浏览器")
	}
	l := launcher.New().Bin(path).Headless(br.config.Headless).
		Set("no-sandbox", "true").
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-infobars", "true").
		Set("excludeSwitches", "enable-automation").
		Set("useAutomationExtension", "false").
		Set("disable-gpu", "true").
		Set("disable-dev-shm-usage", "true").
		Set("disable-software-rasterizer", "true").
		Set("disable-web-security", "true").
		Set("disable-features", "IsolateOrigins,site-per-process").
		Set("window-size", fmt.Sprintf("%d,%d", br.fingerprint.ScreenWidth, br.fingerprint.ScreenHeight)).
		Set("user-agent", br.fingerprint.UserAgent)

	var localProxy *LocalProxyForwarder
	if br.config.Proxy != "" {
		proxyURL, err := url.Parse(br.config.Proxy)
		if err == nil {
			if proxyURL.User != nil {
				localProxy, err = NewLocalProxyForwarder(br.config.Proxy)
				if err != nil {
					return nil, fmt.Errorf("创建本地代理失败: %v", err)
				}
				localAddr, err := localProxy.Start()
				if err != nil {
					return nil, fmt.Errorf("启动本地代理失败: %v", err)
				}
				l = l.Set("proxy-server", localAddr)
			} else {
				l = l.Set("proxy-server", proxyURL.Host)
			}
		}
	}

	u, err := l.Launch()
	if err != nil {
		if localProxy != nil {
			localProxy.Stop()
		}
		return nil, fmt.Errorf("启动浏览器失败: %v", err)
	}

	br.browser = rod.New().ControlURL(u).MustConnect()
	cleanup := func() {
		if br.browser != nil {
			br.browser.MustClose()
		}
		if localProxy != nil {
			localProxy.Stop()
		}
	}
	return cleanup, nil
}

// Point 鼠标轨迹点
type Point struct {
	X, Y float64
}

// generateHumanTrack 生成人类化鼠标轨迹
func (br *BrowserRegister) generateHumanTrack(startX, startY, endX, endY float64) []Point {
	var movements []Point
	distance := endX - startX
	steps := 30 + rand.Intn(20)

	for i := 0; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		easedProgress := 1 - math.Pow(1-progress, 2)

		currentX := startX + distance*easedProgress
		yOffset := 14.7585*math.Pow(currentX-startX, 0.5190) - 3.9874
		yOffset = yOffset*0.1 + float64(rand.Intn(5)-2)

		currentY := startY + yOffset
		movements = append(movements, Point{X: currentX, Y: currentY})
	}

	return movements
}

// Register 执行注册流程
func (br *BrowserRegister) Register(email, password string) (*AccountCredentials, error) {
	// 启动浏览器
	path, found := launcher.LookPath()
	if !found {
		return nil, fmt.Errorf("未找到系统浏览器")
	}
	fmt.Printf("使用浏览器: %s\n", path)

	if br.config.Proxy != "" {
		fmt.Printf("使用代理: %s\n", br.config.Proxy)
	}

	l := launcher.New().Bin(path).Headless(br.config.Headless).
		Set("no-sandbox", "true").
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-infobars", "true").
		Set("excludeSwitches", "enable-automation").
		Set("useAutomationExtension", "false").
		Set("disable-gpu", "true").
		Set("disable-dev-shm-usage", "true").
		Set("disable-software-rasterizer", "true").
		Set("disable-web-security", "true").
		Set("disable-features", "IsolateOrigins,site-per-process").
		Set("window-size", fmt.Sprintf("%d,%d", br.fingerprint.ScreenWidth, br.fingerprint.ScreenHeight)).
		Set("user-agent", br.fingerprint.UserAgent)

	// 设置代理
	var localProxy *LocalProxyForwarder
	if br.config.Proxy != "" {
		proxyURL, err := url.Parse(br.config.Proxy)
		if err == nil {
			// 检查是否需要认证
			if proxyURL.User != nil {
				// 需要认证，启动本地代理转发器
				fmt.Println("代理需要认证，启动本地转发器...")
				localProxy, err = NewLocalProxyForwarder(br.config.Proxy)
				if err != nil {
					return nil, fmt.Errorf("创建本地代理失败: %v", err)
				}
				localAddr, err := localProxy.Start()
				if err != nil {
					return nil, fmt.Errorf("启动本地代理失败: %v", err)
				}
				fmt.Printf("本地代理已启动: %s\n", localAddr)
				l = l.Set("proxy-server", localAddr)
				defer localProxy.Stop()
			} else {
				// 不需要认证，直接使用
				l = l.Set("proxy-server", proxyURL.Host)
			}
		}
	}

	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %v", err)
	}

	br.browser = rod.New().ControlURL(u).MustConnect()
	defer br.browser.MustClose()

	// 使用直接的注册URL - auth.openai.com
	signupURL := "https://auth.openai.com/authorize?client_id=TdJIcbe16WoTHtN95nyywh5E4yOo6ItG&audience=https%3A%2F%2Fapi.openai.com%2Fv1&redirect_uri=https%3A%2F%2Fchatgpt.com%2Fapi%2Fauth%2Fcallback%2Flogin-web&scope=openid+email+profile+offline_access+model.request+model.read+organization.read+organization.write&response_type=code&response_mode=query&state=state_is_immaterial&code_challenge=challenge_is_immaterial&code_challenge_method=S256&screen_hint=signup"

	page := br.browser.MustPage(signupURL)

	fmt.Println("注入隐蔽脚本...")
	fmt.Printf("设备指纹: UA=%s, Screen=%dx%d, TZ=%s\n",
		br.fingerprint.UserAgent[:min(50, len(br.fingerprint.UserAgent))],
		br.fingerprint.ScreenWidth,
		br.fingerprint.ScreenHeight,
		br.fingerprint.Timezone)
	page.MustEval(`() => {
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		window.chrome = {runtime: {}};
	}`)

	// 捕获控制台错误
	page.MustEval(`() => {
		window.__errors = [];
		window.addEventListener('error', (e) => window.__errors.push(e.message));
		window.addEventListener('unhandledrejection', (e) => window.__errors.push(e.reason));
	}`)

	// 等待更长时间让React完全加载
	fmt.Println("等待React应用加载...")
	time.Sleep(10 * time.Second)
	// 等待页面加载和React渲染
	fmt.Println("等待页面加载...")
	time.Sleep(5 * time.Second)

	// 检查页面状态
	currentURL, _ := page.Eval("() => window.location.href")
	fmt.Printf("当前URL: %s\n", currentURL.Value.String())

	// 获取页面HTML片段
	htmlSnippet, _ := page.Eval("() => document.body ? document.body.innerHTML.substring(0, 500) : 'no body'")
	fmt.Printf("页面HTML片段: %s...\n", htmlSnippet.Value.String()[:min(200, len(htmlSnippet.Value.String()))])

	// 检查JS错误
	errors, _ := page.Eval("() => window.__errors || []")
	fmt.Printf("JS错误: %v\n", errors.Value)

	// 获取完整body内容
	fullBody, _ := page.Eval("() => document.body ? document.body.innerHTML : 'no body'")
	bodyStr := fullBody.Value.String()
	fmt.Printf("Body长度: %d\n", len(bodyStr))
	if len(bodyStr) > 500 {
		fmt.Printf("Body内容(前300字符): %s...\n", bodyStr[:300])
		fmt.Printf("Body内容(后300字符): ...%s\n", bodyStr[len(bodyStr)-300:])
	}
	// 检查是否有cf-挑战
	cfChallenge, _ := page.Eval("() => document.querySelector('iframe[src*=challenges]') ? true : false")
	fmt.Printf("Cloudflare iframe: %v\n", cfChallenge.Value)

	// 检查页面标题
	title, _ := page.Eval("() => document.title")
	fmt.Printf("页面标题: %v\n", title.Value)

	// 处理Cloudflare挑战
	br.handleCloudflare(page)
	time.Sleep(2 * time.Second)

	// 截图调试
	br.saveDebugScreenshot(page, "01_initial_load")

	// 等待表单加载
	fmt.Println("等待表单加载...")
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)
		url, _ := page.Eval("() => window.location.href")
		inputs, _ := page.Elements("input")
		fmt.Printf("  [%ds] URL: %s | Inputs: %d\n", i+1, url.Value.String(), len(inputs))

		// 检查页面上的其他元素
		buttons, _ := page.Elements("button")
		divs, _ := page.Elements("div")
		fmt.Printf("       Buttons: %d | Divs: %d\n", len(buttons), len(divs))

		if len(inputs) > 0 || len(buttons) >= 1 {
			fmt.Println("  页面已加载!")
			break
		}

		// 超时检查
		if i >= 15 {
			fmt.Println("  等待超时，继续尝试...")
			break
		}
	}

	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "02_after_wait")

	// 步骤0: 点击 "Sign up" 或 "Continue with email" 按钮
	fmt.Println("\n步骤0: 查找登录选项...")

	// 打印所有按钮文本
	allButtons, _ := page.Elements("button")
	fmt.Printf("找到 %d 个按钮:\n", len(allButtons))
	for i, btn := range allButtons {
		text, _ := btn.Eval("() => this.innerText || this.textContent || ''")
		fmt.Printf("  按钮 %d: %s\n", i+1, text.Value.String())
	}

	// 查找 Sign up 链接
	links, _ := page.Elements("a")
	fmt.Printf("找到 %d 个链接:\n", len(links))
	for i, link := range links {
		href, _ := link.Eval("() => this.href")
		text, _ := link.Eval("() => this.innerText || this.textContent || ''")
		fmt.Printf("  链接 %d: %s -> %s\n", i+1, text.Value.String(), href.Value.String())
	}

	// 尝试通过文本点击
	signupClicked := false
	for i := 0; i < 5; i++ {
		// 查找包含 "Sign up" 的按钮
		buttons, err := page.Elements("button")
		if err == nil {
			for _, btn := range buttons {
				text, err := btn.Eval("() => this.innerText || this.textContent || ''")
				if err != nil {
					continue
				}
				btnText := strings.ToLower(text.Value.String())
				if strings.Contains(btnText, "sign up") {
					// 使用JavaScript点击，更可靠
					btn.Eval("() => this.click()")
					fmt.Println("  已点击 'Sign up' 按钮")
					signupClicked = true
					break
				}
				// 新的登录流程：点击 "Continue" 按钮（纯文本，不带 Google/Apple/phone）
				if btnText == "continue" || btnText == "continue with email" {
					btn.Eval("() => this.click()")
					fmt.Println("  已点击 'Continue' 按钮")
					signupClicked = true
					break
				}
			}
		}
		if signupClicked {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !signupClicked {
		fmt.Println("⚠️ 未找到 Sign up 或 Continue 按钮")
		br.saveDebugScreenshot(page, "02_signup_not_found")
		return nil, fmt.Errorf("未找到 Sign up 或 Continue 按钮")
	}

	// 等待页面跳转和React SPA加载 - 需要等待较长时间
	fmt.Println("等待注册页面加载 (React SPA)...")
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)
		url, _ := page.Eval("() => window.location.href")
		inputs, _ := page.Elements("input")
		fmt.Printf("  [%ds] URL: %s | Inputs: %d\n", i+1, url.Value.String(), len(inputs))
		if len(inputs) > 0 {
			fmt.Println("  表单已加载!")
			break
		}
	}

	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "02_after_signup_click")

	// 步骤1: 输入邮箱
	fmt.Println("\n步骤1: 输入邮箱...")
	time.Sleep(1 * time.Second)

	emailSelectors := []string{
		"input[name='email']",
		"input[type='email']",
		"input[id*='email']",
		"input[autocomplete='email']",
		"input[autocomplete='username']",
		"input[placeholder*='email' i]",
		"input[id='email-input']",
		"input[data-testid='email-input']",
	}

	if !br.inputTextWithWait(page, emailSelectors, email, "Email", 15*time.Second) {
		fmt.Println("⚠️ 未找到邮箱输入框")
		br.saveDebugScreenshot(page, "03_email_not_found")
		// 尝试打印页面结构
		br.debugPageElements(page, "input")
		return nil, fmt.Errorf("未找到邮箱输入框")
	}
	br.saveDebugScreenshot(page, "04_email_entered")

	// 步骤2: 点击Continue按钮
	fmt.Println("\n步骤2: 点击Continue...")
	time.Sleep(1 * time.Second)

	continueClicked := br.clickButtonWithWait(page, []string{
		"button[type='submit']",
		"button[data-testid='continue-button']",
		"button[name='continue']",
		"input[type='submit']",
	}, 10*time.Second)

	if !continueClicked {
		// 尝试通过文本查找
		if !br.clickElementByText(page, "button", "Continue") {
			fmt.Println("⚠️ 未找到Continue按钮")
			br.saveDebugScreenshot(page, "05_continue_not_found")
			br.debugPageElements(page, "button")
		}
	}

	// 等待页面跳转/加载
	fmt.Println("等待页面响应...")
	time.Sleep(3 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "06_after_continue")

	// 步骤3: 输入密码（可能在同一页面或新页面）
	fmt.Println("\n步骤3: 输入密码...")
	time.Sleep(2 * time.Second)

	passwordSelectors := []string{
		"input[name='password']",
		"input[type='password']",
		"input[autocomplete='new-password']",
		"input[autocomplete='password']",
		"input[id*='password' i]",
		"input[placeholder*='password' i]",
		"input[data-testid='password-input']",
	}

	passwordFound := br.inputTextWithWait(page, passwordSelectors, password, "Password", 15*time.Second)

	if !passwordFound {
		fmt.Println("⚠️ 未找到密码输入框，可能已使用OAuth或其他方式")
		br.saveDebugScreenshot(page, "07_password_not_found")
		br.debugPageElements(page, "input")
		// 不返回错误，继续尝试
	} else {
		br.saveDebugScreenshot(page, "08_password_entered")

		// 步骤3: 点击Continue完成注册
		fmt.Println("\n步骤3: 提交注册...")
		time.Sleep(1 * time.Second)

		// 尝试多个可能的按钮文本
		for _, btnText := range []string{"Continue", "Sign up", "Create account", "Create"} {
			if br.clickElementByText(page, "button", btnText) {
				break
			}
		}
		br.clickButtonWithWait(page, []string{"button[type='submit']"}, 5*time.Second)
	}

	// 等待注册完成
	fmt.Println("\n等待注册处理...")
	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "09_after_submit")

	// 处理可能的验证码
	br.handleCaptcha(page)

	// 步骤4: 等待验证邮件
	fmt.Println("\n步骤4: 等待验证邮件...")
	verifyLink, err := br.httpClient.CheckEmail(email)
	if err != nil {
		fmt.Printf("获取验证邮件失败: %v\n", err)
		fmt.Println("等待页面跳转或手动验证...")
		// 等待更长时间让用户手动验证或页面自动跳转
		time.Sleep(30 * time.Second)
	} else {
		fmt.Printf("获取到验证内容: %s\n", verifyLink)

		// 检查是OTP码还是验证链接
		if strings.HasPrefix(verifyLink, "OTP:") {
			// 处理OTP验证码
			otpCode := strings.TrimPrefix(verifyLink, "OTP:")
			fmt.Printf("检测到OTP验证码: %s\n", otpCode)

			// 输入OTP码
			br.handleOTPInput(page, otpCode)
		} else {
			// 访问验证链接
			page.MustNavigate(verifyLink)
			time.Sleep(5 * time.Second)
			br.handleCloudflare(page)
		}
		br.saveDebugScreenshot(page, "09_after_verification")

		// 等待页面完全加载
		fmt.Println("\n等待页面加载...")
		time.Sleep(5 * time.Second)
		br.handleCloudflare(page)

		// 处理可能的后续步骤（姓名输入等）
		fmt.Println("\n处理后续步骤...")
		if err := br.handlePostVerification(page); err != nil {
			if err.Error() == "unsupported_email" {
				return nil, fmt.Errorf("邮箱域名不被支持，请使用其他邮箱")
			}
			fmt.Printf("后续步骤处理错误: %v\n", err)
		}
	}

	// 步骤5: 获取access token
	fmt.Println("\n步骤5: 获取Access Token...")
	accessToken, userID := br.getAccessToken(page)

	if accessToken == "" {
		br.saveDebugScreenshot(page, "10_no_token")
		return nil, fmt.Errorf("获取Access Token失败")
	}

	credentials := &AccountCredentials{
		Email:       email,
		Password:    password,
		AccessToken: accessToken,
		UserID:      userID,
		CreatedAt:   time.Now(),
	}

	// 步骤6: 尝试通过 iOS App OAuth 流程获取 refresh_token (无需 Plus 订阅)
	refreshToken, idToken, err := br.getRefreshTokenViaIOSAppFlow(page, email, password)
	if err != nil {
		fmt.Printf("iOS App OAuth 流程失败: %v\n", err)
		fmt.Println("尝试设备码流程...")
		// 回退到设备码流程
		refreshToken, idToken, err = br.getRefreshTokenViaDeviceFlow(page, email, password)
		if err != nil {
			fmt.Printf("设备码流程也失败（不影响使用）: %v\n", err)
		}
	}
	if refreshToken != "" {
		credentials.RefreshToken = refreshToken
		credentials.IDToken = idToken
		fmt.Println("成功获取 refresh_token!")
	}


	br.saveDebugScreenshot(page, "11_success")
	return credentials, nil
}

// handleCloudflare 处理Cloudflare挑战
func (br *BrowserRegister) handleCloudflare(page *rod.Page) {
	fmt.Println("检查Cloudflare挑战...")

	// 等待页面完全加载
	time.Sleep(2 * time.Second)

	// 检查是否有Cloudflare挑战
	for i := 0; i < 30; i++ {
		title := page.MustEval(`() => document.title || ""`).String()
		bodyText := page.MustEval(`() => document.body ? document.body.innerText : ""`).String()

		if strings.Contains(title, "Just a moment") ||
			strings.Contains(title, "Cloudflare") ||
			strings.Contains(bodyText, "Checking your browser") ||
			strings.Contains(bodyText, "Please Wait") ||
			strings.Contains(bodyText, "DDoS protection") {

			fmt.Printf("检测到Cloudflare挑战，等待自动解决... (%d/30)\n", i+1)

			// 尝试点击Cloudflare验证框
			cfCheckbox, _ := page.Timeout(2 * time.Second).Element("input[type='checkbox']")
			if cfCheckbox != nil {
				fmt.Println("尝试点击Cloudflare复选框...")
				cfCheckbox.MustClick()
				time.Sleep(5 * time.Second)
			}

			// 尝试点击验证按钮
			cfButton, _ := page.Timeout(1 * time.Second).Element("input[type='button'], button")
			if cfButton != nil {
				cfButton.MustClick()
				time.Sleep(3 * time.Second)
			}

			// 模拟人类行为
			page.MustEval(`() => {
				// 随机移动鼠标
				const moveEvent = new MouseEvent('mousemove', {
					bubbles: true,
					cancelable: true,
					clientX: Math.random() * window.innerWidth,
					clientY: Math.random() * window.innerHeight
				});
				document.dispatchEvent(moveEvent);
			}`)

			time.Sleep(3 * time.Second)
		} else {
			// 检查是否已通过
			currentURL := page.MustEval(`() => window.location.href`).String()
			if !strings.Contains(currentURL, "challenge") && !strings.Contains(currentURL, "cdn-cgi") {
				fmt.Println("Cloudflare挑战已通过!")
				return
			}
			break
		}
	}

	// 最终检查
	time.Sleep(2 * time.Second)
}

// handleCaptcha 处理验证码
func (br *BrowserRegister) handleCaptcha(page *rod.Page) {
	fmt.Println("检查验证码...")

	// 检查是否有reCAPTCHA
	recaptcha, _ := page.Timeout(3 * time.Second).Element("iframe[src*='recaptcha']")
	if recaptcha != nil {
		fmt.Println("⚠️ 检测到reCAPTCHA - 等待手动完成或使用验证码服务")
		// 等待更长时间让用户手动完成
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			// 检查是否已完成
			check, _ := page.Timeout(500 * time.Millisecond).Element("iframe[src*='recaptcha']")
			if check == nil {
				fmt.Println("reCAPTCHA已通过!")
				return
			}
		}
	}

	// 检查是否有hCaptcha
	hcaptcha, _ := page.Timeout(3 * time.Second).Element("iframe[src*='hcaptcha']")
	if hcaptcha != nil {
		fmt.Println("⚠️ 检测到hCaptcha - 等待手动完成或使用验证码服务")
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			check, _ := page.Timeout(500 * time.Millisecond).Element("iframe[src*='hcaptcha']")
			if check == nil {
				fmt.Println("hCaptcha已通过!")
				return
			}
		}
	}

	// 检查Cloudflare Turnstile
	turnstile, _ := page.Timeout(2 * time.Second).Element("iframe[src*='challenges.cloudflare.com']")
	if turnstile != nil {
		fmt.Println("检测到Cloudflare Turnstile验证...")
		time.Sleep(5 * time.Second)
	}
}

// handleOTPInput 处理OTP验证码输入
func (br *BrowserRegister) handleOTPInput(page *rod.Page, otpCode string) {
	fmt.Println("\n步骤5: 输入OTP验证码...")
	time.Sleep(2 * time.Second)

	// OTP输入框可能的选择器
	otpSelectors := []string{
		"input[name='otp']",
		"input[type='text']",
		"input[autocomplete='one-time-code']",
		"input[inputmode='numeric']",
		"input[pattern*='[0-9]']",
		"input[placeholder*='code' i]",
		"input[maxlength='6']",
		"input[maxlength='7']",
	}

	// 尝试查找OTP输入框
	for i := 0; i < 10; i++ {
		for _, sel := range otpSelectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err == nil && el != nil {
				// 检查是否可见
				isVisible, _ := el.Eval(`() => {
					const rect = this.getBoundingClientRect();
					return rect.width > 0 && rect.height > 0;
				}`)
				if isVisible.Value.Bool() {
					fmt.Printf("找到OTP输入框: %s\n", sel)
					el.MustClick()
					time.Sleep(200 * time.Millisecond)
					el.MustSelectAllText().MustInput(otpCode)
					fmt.Printf("已输入OTP码: %s\n", otpCode)
					time.Sleep(1 * time.Second)

					// 尝试点击提交按钮
					br.clickButtonWithWait(page, []string{"button[type='submit']"}, 3*time.Second)
					br.clickElementByText(page, "button", "Verify")
					br.clickElementByText(page, "button", "Continue")
					br.saveDebugScreenshot(page, "10_otp_entered")
					return
				}
			}
		}

		// 检查是否有多个单字符输入框（常见OTP UI）
		singleInputs, err := page.Elements("input[maxlength='1']")
		if err == nil && len(singleInputs) >= 6 {
			fmt.Printf("找到%d个单字符输入框，逐个输入OTP\n", len(singleInputs))
			for i, char := range otpCode {
				if i < len(singleInputs) {
					singleInputs[i].MustClick()
					singleInputs[i].MustInput(string(char))
					time.Sleep(100 * time.Millisecond)
				}
			}
			fmt.Printf("已输入OTP码: %s\n", otpCode)
			time.Sleep(1 * time.Second)
			br.clickButtonWithWait(page, []string{"button[type='submit']"}, 3*time.Second)
			br.saveDebugScreenshot(page, "10_otp_entered")
			return
		}

		fmt.Printf("等待OTP输入框... (%d/10)\n", i+1)
		time.Sleep(1 * time.Second)
	}

	fmt.Println("⚠️ 未找到OTP输入框")
	br.saveDebugScreenshot(page, "10_otp_not_found")
	br.debugPageElements(page, "input")
}

// handlePostVerification 处理验证后的步骤
func (br *BrowserRegister) handlePostVerification(page *rod.Page) error {
	step := 0 // 0: 初始, 1: 已提交
	name := ""

	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		currentURL := page.MustEval(`() => window.location.href`).String()
		fmt.Printf("当前URL: %s (步骤: %d)\n", currentURL, step)

		// 已跳转到主页面
		if strings.Contains(currentURL, "chatgpt.com") && !strings.Contains(currentURL, "auth") && !strings.Contains(currentURL, "log-in") && !strings.Contains(currentURL, "about-you") {
			fmt.Println("已跳转到主页面!")
			return nil
		}

		// 处理 about-you 页面
		if strings.Contains(currentURL, "about-you") {

			if step == 0 {
				// 输入姓名
				nameInput, _ := page.Timeout(2 * time.Second).Element("input[name='name']")
				if nameInput != nil {
					val, _ := nameInput.Eval(`() => this.value`)
					if len(val.Value.String()) == 0 {
						// 使用真实的英文名，不含数字
						names := []string{"James", "John", "Robert", "Michael", "David", "William", "Richard", "Joseph", "Thomas", "Charles", "Daniel", "Matthew", "Anthony", "Mark", "Steven", "Paul", "Andrew", "Joshua", "Kenneth", "Kevin", "Brian", "George", "Edward", "Ronald", "Timothy", "Jason", "Jeffrey", "Ryan", "Jacob", "Gary", "Nicholas", "Eric", "Jonathan", "Stephen", "Larry", "Justin", "Scott", "Brandon", "Raymond", "Samuel", "Benjamin", "Gregory", "Frank", "Alexander", "Patrick", "Jack", "Dennis", "Jerry", "Tyler", "Aaron", "Jose", "Adam", "Henry", "Nathan", "Douglas", "Zachary", "Peter", "Kyle"}
						firstName := names[rand.Intn(len(names))]
						lastName := names[rand.Intn(len(names))]
						name = firstName + " " + lastName
						fmt.Println("输入姓名: " + name)
						nameInput.MustClick()
						nameInput.MustInput(name)
						time.Sleep(300 * time.Millisecond)
					} else {
						name = val.Value.String()
					}
				}

				// 使用 React 原生方法设置隐藏的 birthday 字段
				year := 1990 + rand.Intn(15)
				birthdate := fmt.Sprintf("%d-01-15", year)
				fmt.Printf("设置生日: %s\n", birthdate)

				// 使用 React 兼容的方式设置隐藏字段
				result := page.MustEval(fmt.Sprintf(`() => {
					const input = document.querySelector('input[name="birthday"]');
					if (input) {
						const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
						nativeInputValueSetter.call(input, '%s');
						input.dispatchEvent(new Event('input', {bubbles: true}));
						input.dispatchEvent(new Event('change', {bubbles: true}));
						return {success: true, value: input.value};
					}
					return {success: false, error: 'birthday input not found'};
				}`, birthdate))
				fmt.Printf("设置生日结果: %v\n", result)

				time.Sleep(500 * time.Millisecond)

				// 尝试通过 API 提交
				fmt.Println("尝试通过 API 提交...")
				apiResult := page.MustEval(fmt.Sprintf(`() => {
					return fetch('https://auth.openai.com/api/accounts/create_account', {
						method: 'POST',
						headers: {
							'Content-Type': 'application/json',
							'Accept': 'application/json'
						},
						body: JSON.stringify({name: "%s", birthdate: "%s"})
					})
					.then(async r => {
						const text = await r.text();
						return {status: r.status, ok: r.ok, body: text};
					})
					.catch(e => ({error: e.toString()}));
				}`, name, birthdate))
				fmt.Printf("API 结果: %v\n", apiResult)

				// 检查是否是不支持的邮箱
				bodyStr := apiResult.Get("body").String()
				if strings.Contains(bodyStr, "unsupported_email") {
					fmt.Println("❌ 邮箱域名不被 OpenAI 支持")
					return fmt.Errorf("unsupported_email")
				}

				// 解析 API 响应，提取 continue_url
				if apiResult.Get("ok").Bool() {
					var apiResp struct {
						ContinueURL string `json:"continue_url"`
					}
					if err := json.Unmarshal([]byte(bodyStr), &apiResp); err == nil && apiResp.ContinueURL != "" {
						fmt.Printf("获取到 continue_url: %s\n", apiResp.ContinueURL)
						// 导航到 continue_url 完成认证
						fmt.Println("导航到 continue_url...")
						page.MustNavigate(apiResp.ContinueURL)
						time.Sleep(3 * time.Second)
						step = 2 // 跳过等待，直接进入下一步
						continue
					}
				}

				time.Sleep(2 * time.Second)

				// 也尝试点击 Submit 按钮
				submitBtn, _ := page.Timeout(1 * time.Second).Element("button[type='submit']")
				if submitBtn != nil {
					disabled, _ := submitBtn.Eval(`() => this.disabled`)
					if !disabled.Value.Bool() {
						fmt.Println("点击 Submit 按钮...")
						submitBtn.MustClick()
						time.Sleep(2 * time.Second)
					}
				}

				step = 1
			} else if step == 1 {
				// 已提交，等待跳转
				fmt.Println("等待页面跳转...")
			}

			br.saveDebugScreenshot(page, fmt.Sprintf("11_about_you_step%d", step))
			continue
		}

		// 点击同意按钮
		br.clickElementByText(page, "button", "Agree")
		br.clickElementByText(page, "button", "Accept")
	}

	return nil
}

// getAccessToken 获取Access Token
func (br *BrowserRegister) getAccessToken(page *rod.Page) (string, string) {
	fmt.Println("尝试获取session...")

	// 尝试多次获取session
	for attempt := 0; attempt < 5; attempt++ {
		// 导航到session API
		sessionPage, err := br.browser.Page(proto.TargetCreateTarget{URL: "https://chat.openai.com/api/auth/session"})
		if err != nil {
			fmt.Printf("打开session页面失败: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		time.Sleep(3 * time.Second)

		// 获取页面内容
		content, err := sessionPage.Eval(`() => document.body.innerText`)
		if err != nil {
			fmt.Printf("获取session内容失败: %v\n", err)
			sessionPage.MustClose()
			time.Sleep(2 * time.Second)
			continue
		}

		contentStr := content.Value.String()
		sessionPage.MustClose()

		fmt.Printf("Session响应: %s\n", contentStr[:min(200, len(contentStr))])

		// 检查是否为空或错误
		if strings.Contains(contentStr, "error") || strings.Contains(contentStr, "unauthorized") || contentStr == "" {
			fmt.Printf("Session无效，等待重试... (%d/5)\n", attempt+1)
			time.Sleep(3 * time.Second)
			continue
		}

		// 解析JSON
		var session struct {
			AccessToken string `json:"accessToken"`
			User        struct {
				ID string `json:"id"`
			} `json:"user"`
		}

		if err := json.Unmarshal([]byte(contentStr), &session); err != nil {
			fmt.Printf("解析session失败: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if session.AccessToken != "" {
			fmt.Println("成功获取Access Token!")
			return session.AccessToken, session.User.ID
		}

		fmt.Printf("Token为空，等待重试... (%d/5)\n", attempt+1)
		time.Sleep(3 * time.Second)
	}

	return "", ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// clickElementByText 通过文本点击元素
func (br *BrowserRegister) clickElementByText(page *rod.Page, tag, text string) bool {
	elements, err := page.Elements(tag)
	if err != nil || len(elements) == 0 {
		return false
	}

	for _, el := range elements {
		elText, err := el.Eval(`() => this.innerText || this.textContent || ""`)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(elText.Value.String()), strings.ToLower(text)) {
			if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
				fmt.Printf("  已点击包含 '%s' 的元素\n", text)
				return true
			}
		}
	}
	return false
}

// findElementByText 通过文本查找元素
func (br *BrowserRegister) findElementByText(page *rod.Page, tag, text string) *rod.Element {
	elements, err := page.Elements(tag)
	if err != nil || len(elements) == 0 {
		return nil
	}

	for _, el := range elements {
		elText, err := el.Eval(`() => this.innerText || this.textContent || ""`)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(elText.Value.String()), strings.ToLower(text)) {
			return el
		}
	}
	return nil
}

// clickElement 点击元素
func (br *BrowserRegister) clickElement(page *rod.Page, selectors []string, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err != nil || el == nil {
			continue
		}

		if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
			fmt.Printf("  %s: 已点击 (%s)\n", desc, sel)
			return true
		}
	}
	return false
}

// inputText 输入文本
func (br *BrowserRegister) inputText(page *rod.Page, selectors []string, text, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err != nil || el == nil {
			continue
		}

		// 使用非Must方法避免panic
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			continue
		}
		time.Sleep(100 * time.Millisecond)

		// 选择全部文本
		if err := el.SelectAllText(); err != nil {
			// 如果SelectAllText失败，手动清空
			el.Eval(`() => this.value = ''`)
		}

		// 输入文本
		if err := el.Input(text); err != nil {
			continue
		}

		fmt.Printf("  %s: 已输入\n", desc)
		return true
	}
	fmt.Printf("  %s: 未找到输入框\n", desc)
	return false
}

// waitForReactComponents 等待React组件渲染
func (br *BrowserRegister) waitForReactComponents(page *rod.Page) {
	// 等待至少一个input元素出现
	for i := 0; i < 30; i++ {
		inputs, err := page.Elements("input")
		if err == nil && len(inputs) > 0 {
			fmt.Printf("  检测到 %d 个input元素\n", len(inputs))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("  警告: 未检测到input元素")
}

// inputTextWithWait 带等待的文本输入
func (br *BrowserRegister) inputTextWithWait(page *rod.Page, selectors []string, text, desc string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for _, sel := range selectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err != nil || el == nil {
				continue
			}

			// 检查元素是否可见
			isVisible, _ := el.Eval(`() => {
				const rect = this.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			}`)
			if !isVisible.Value.Bool() {
				continue
			}

			// 点击聚焦
			if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
				continue
			}
			time.Sleep(200 * time.Millisecond)

			// 清空并输入
			el.Eval(`() => this.value = ''`)
			time.Sleep(100 * time.Millisecond)

			if err := el.Input(text); err != nil {
				continue
			}

			fmt.Printf("  %s: 已输入 (%s)\n", desc, sel)
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("  %s: 超时未找到\n", desc)
	return false
}

// clickButtonWithWait 带等待的按钮点击
func (br *BrowserRegister) clickButtonWithWait(page *rod.Page, selectors []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for _, sel := range selectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err != nil || el == nil {
				continue
			}

			// 检查是否可见和可点击
			isClickable, _ := el.Eval(`() => {
				const rect = this.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0 && !this.disabled;
			}`)
			if !isClickable.Value.Bool() {
				continue
			}

			if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
				fmt.Printf("  已点击按钮: %s\n", sel)
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return false
}

// saveDebugScreenshot 保存调试截图
func (br *BrowserRegister) saveDebugScreenshot(page *rod.Page, name string) {
	screenshot, err := page.Screenshot(false, nil)
	if err != nil {
		return
	}

	filename := fmt.Sprintf("/openai_register/debug_%s.png", name)
	if err := os.WriteFile(filename, screenshot, 0644); err == nil {
		fmt.Printf("  [调试] 截图已保存: %s\n", filename)
	}
}

// debugPageElements 打印页面元素用于调试
func (br *BrowserRegister) debugPageElements(page *rod.Page, tag string) {
	fmt.Printf("\n[调试] 页面 %s 元素:\n", tag)

	elements, err := page.Elements(tag)
	if err != nil {
		fmt.Printf("  获取元素失败: %v\n", err)
		return
	}

	fmt.Printf("  找到 %d 个 %s 元素\n", len(elements), tag)

	for i, el := range elements {
		if i >= 10 {
			fmt.Println("  ... (更多元素已省略)")
			break
		}

		// 直接获取属性
		name, _ := el.Eval(`() => this.name || this.id || ''`)
		typ, _ := el.Eval(`() => this.type || ''`)
		placeholder, _ := el.Eval(`() => this.placeholder || ''`)

		fmt.Printf("  [%d] name=%s type=%s placeholder=%s\n",
			i,
			name.Value.String(),
			typ.Value.String(),
			placeholder.Value.String())
	}
}

// SaveCredentialsWithDir 保存凭证到指定目录
func SaveCredentialsWithDir(credentials *AccountCredentials, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	// 保存为JSON格式
	credFile := filepath.Join(dataDir, "openai_credentials.json")
	existing := []AccountCredentials{}

	// 读取现有数据
	if data, err := os.ReadFile(credFile); err == nil {
		json.Unmarshal(data, &existing)
	}

	// 检查邮箱是否已存在，如果存在则更新
	found := false
	for i, cred := range existing {
		if cred.Email == credentials.Email {
			existing[i] = *credentials
			found = true
			fmt.Printf("更新已存在的凭证: %s\n", credentials.Email)
			break
		}
	}
	if !found {
		existing = append(existing, *credentials)
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(credFile, data, 0644); err != nil {
		return err
	}

	// 同时保存为CodeX格式
	codexAuth := map[string]interface{}{
		"access_token":  credentials.AccessToken,
		"refresh_token": credentials.RefreshToken,
		"id_token":      credentials.IDToken,
		"email":         credentials.Email,
		"user_id":       credentials.UserID,
		"created_at":    credentials.CreatedAt,
		"type":          "codex",
	}
	if credentials.RefreshToken != "" {
		codexAuth["last_refresh"] = time.Now().Format(time.RFC3339)
	}
	codexData, _ := json.MarshalIndent(codexAuth, "", "  ")
	codexFile := filepath.Join(dataDir, fmt.Sprintf("auth_%s.json", credentials.Email[:strings.Index(credentials.Email, "@")]))
	os.WriteFile(codexFile, codexData, 0600)
	fmt.Printf("CodeX凭证已保存到: %s\n", codexFile)

	// 保存为简单文本格式（供其他工具使用）
	// 先读取现有内容，检查是否已有该邮箱
	tokenFile := filepath.Join(dataDir, "openai_tokens.txt")
	existingTokens := ""
	if tokenData, err := os.ReadFile(tokenFile); err == nil {
		existingTokens = string(tokenData)
	}

	// 如果该邮箱已存在，先删除旧记录
	if strings.Contains(existingTokens, fmt.Sprintf("OPENAI_EMAIL=%s\n", credentials.Email)) {
		// 移除该邮箱的旧记录块
		lines := strings.Split(existingTokens, "\n")
		var newLines []string
		i := 0
		for i < len(lines) {
			if strings.HasPrefix(lines[i], "# Account: ") && i+4 < len(lines) {
				// 检查是否是目标邮箱
				if i+2 < len(lines) && strings.Contains(lines[i+2], fmt.Sprintf("OPENAI_EMAIL=%s", credentials.Email)) {
					// 跳过这个账号块（5行：# Account, ACCESS_TOKEN, EMAIL, PASSWORD, 空行）
					i += 5
					continue
				}
			}
			newLines = append(newLines, lines[i])
			i++
		}
		existingTokens = strings.Join(newLines, "\n")
	}

	// 追加新记录
	newRecord := fmt.Sprintf("# Account: %s\nOPENAI_ACCESS_TOKEN=%s\nOPENAI_REFRESH_TOKEN=%s\nOPENAI_EMAIL=%s\nOPENAI_PASSWORD=%s\n\n",
		credentials.Email, credentials.AccessToken, credentials.RefreshToken, credentials.Email, credentials.Password)
	existingTokens += newRecord

	if err := os.WriteFile(tokenFile, []byte(existingTokens), 0644); err != nil {
		return err
	}

	return nil
}

// SaveCredentials 保存凭证到文件 (使用默认目录)
func SaveCredentials(credentials *AccountCredentials) error {
	return SaveCredentialsWithDir(credentials, "/openai_register/creds")
}

func loadLastCredential(path string) (*AccountCredentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var all []AccountCredentials
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no credentials")
	}
	last := all[len(all)-1]
	return &last, nil
}

func runDeviceFlowOnly(config *Config) error {
	cred, err := loadLastCredential(filepath.Join(config.OutputDir, "openai_credentials.json"))
	if err != nil {
		return fmt.Errorf("加载最近凭证失败: %w", err)
	}
	if cred.Email == "" || cred.Password == "" {
		return fmt.Errorf("最近凭证缺少 email/password")
	}
	br := NewBrowserRegister(config)
	cleanup, err := br.openBrowser()
	if err != nil {
		return err
	}
	defer cleanup()
	page, err := br.browser.Page(proto.TargetCreateTarget{URL: codexDeviceVerificationURL})
	if err != nil {
		return fmt.Errorf("打开验证页面失败: %w", err)
	}
	defer page.Close()
	refreshToken, idToken, err := br.getRefreshTokenViaDeviceFlow(page, cred.Email, cred.Password)
	if err != nil {
		return fmt.Errorf("device flow 失败: %w", err)
	}
	if refreshToken == "" {
		return fmt.Errorf("device flow 未返回 refresh_token")
	}
	cred.RefreshToken = refreshToken
	cred.IDToken = idToken
	if err := SaveCredentialsWithDir(cred, config.OutputDir); err != nil {
		return fmt.Errorf("保存 refresh_token 失败: %w", err)
	}
	fmt.Println("device flow 成功并已写入 refresh_token")
	return nil
}

func main() {
	// Go 1.20+ rand 自动初始化，无需手动 seed

	fmt.Println("====================================")
	fmt.Println("   OpenAI 账号注册工具")
	fmt.Println("   用于 CodeX 认证")
	fmt.Println("====================================")
	fmt.Println()

	// 加载配置
	configPath := "/openai_register/config.json"
	config, err := LoadConfig(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v，使用默认配置\n", err)
		config = DefaultConfig()
	}

	// 检查命令行参数
	simMode := false
	count := config.Count // 默认使用配置文件中的数量
	deviceOnlyMode := false
	for _, arg := range os.Args[1:] {
		if arg == "--sim" || arg == "-sim" {
			simMode = true
		} else if arg == "--device-only" || arg == "-device-only" {
			deviceOnlyMode = true
		} else if arg == "--head" || arg == "-head" {
			config.Headless = false
		} else if arg == "--debug" || arg == "-debug" {
			config.Debug = true
		} else {
			fmt.Sscanf(arg, "%d", &count) // 命令行覆盖配置
		}
	}
	// 显示配置信息
	if config.Proxy != "" {
		fmt.Printf("代理: %s\n", config.Proxy)
	}
	fmt.Printf("无头模式: %v\n", config.Headless)
	fmt.Printf("输出目录: %s\n", config.OutputDir)
	fmt.Printf("注册数量: %d\n\n", count)
	if deviceOnlyMode {
		if err := runDeviceFlowOnly(config); err != nil {
			fmt.Printf("device-only 模式失败: %v\n", err)
		}
	} else if simMode {
		fmt.Println("[模拟模式] 生成测试凭证...")
		for i := 0; i < count; i++ {
			fmt.Printf("\n========== 生成第 %d/%d 个凭证 ==========\n", i+1, count)

			// 生成模拟凭证
			ts := time.Now().Unix()
			randStr := randomString(16)
			credentials := &AccountCredentials{
				Email:       fmt.Sprintf("test%d@openai-register.test", i+1),
				Password:    GeneratePassword(),
				AccessToken: fmt.Sprintf("eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLWR1bW15IiwiZW1haWwiOiJ0ZXN0JWRAb3BlbmFpLXJlZ2lzdGVyLnRlc3QiLCJpYXQiOjE3MDk1Njc2MDAsImV4cCI6MTc0MDgxMTM5OH0.sim_sig_%d_%s", ts, randStr),
				UserID:      fmt.Sprintf("user-test-%d-%d", i+1, ts),
				CreatedAt:   time.Now(),
			}

			// 保存凭证
			if err := SaveCredentialsWithDir(credentials, config.OutputDir); err != nil {
				fmt.Printf("保存凭证失败: %v\n", err)
				continue
			}

			fmt.Println("\n=== 凭证生成成功 ===")
			fmt.Printf("邮箱: %s\n", credentials.Email)
			fmt.Printf("密码: %s\n", credentials.Password)
			if len(credentials.AccessToken) > 50 {
				fmt.Printf("Access Token: %s...\n", credentials.AccessToken[:50])
			} else {
				fmt.Printf("Access Token: %s\n", credentials.AccessToken)
			}
		}
	} else {
		fmt.Printf("将注册 %d 个账号\n\n", count)

		httpClient := NewHTTPClientWithProxy(config.Proxy)
		br := NewBrowserRegister(config)
		for i := 0; i < count; i++ {
			fmt.Printf("\n========== 注册第 %d/%d 个账号 ==========\n", i+1, count)

			// 获取临时邮箱
			email, err := httpClient.GetTempEmail()
			if err != nil {
				fmt.Printf("获取临时邮箱失败: %v\n", err)
				continue
			}
			fmt.Printf("临时邮箱: %s\n", email)

			// 生成密码
			password := GeneratePassword()
			fmt.Printf("生成密码: %s\n", password)

			// 执行注册
			credentials, err := br.Register(email, password)
			if err != nil {
				fmt.Printf("注册失败: %v\n", err)
				continue
			}

			// 保存凭证
			if err := SaveCredentialsWithDir(credentials, config.OutputDir); err != nil {
				fmt.Printf("保存凭证失败: %v\n", err)
			}

			fmt.Println("\n=== 注册成功 ===")
			fmt.Printf("邮箱: %s\n", credentials.Email)
			// 安全打印 token 前缀
			if len(credentials.AccessToken) > 50 {
				fmt.Printf("Access Token: %s...\n", credentials.AccessToken[:50])
			} else {
				fmt.Printf("Access Token: %s\n", credentials.AccessToken)
			}
			fmt.Printf("凭证已保存到 %s/openai_credentials.json\n", config.OutputDir)

			// 等待一段时间再注册下一个
			if i < count-1 {
				waitTime := 30 + rand.Intn(30)
				fmt.Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
				time.Sleep(time.Duration(waitTime) * time.Second)
			}
		}
	}

	fmt.Println("\n====================================")
	fmt.Println("   所有账号处理完成")
	fmt.Println("====================================")
}

// ========================================
// 设备码流程 - 获取 refresh_token
// ========================================

const (
	codexClientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexDeviceUserCodeURL     = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL        = "https://auth.openai.com/api/accounts/deviceauth/token"
	codexDeviceVerificationURL = "https://auth.openai.com/codex/device"
	codexDeviceCallbackURI     = "https://auth.openai.com/deviceauth/callback"
	codexOAuthTokenURL         = "https://auth.openai.com/oauth/token"
	codexDeviceTimeout         = 10 * time.Minute
	codexDevicePollInterval    = 5 * time.Second
)

// ========================================
// iOS App OAuth 流程 - 获取 refresh_token (无需 Plus)
// ========================================

const (
	iosAppClientID       = "pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh"
	iosAppAuthorizeURL   = "https://auth0.openai.com/authorize"
	iosAppTokenURL       = "https://auth0.openai.com/oauth/token"
	iosAppRedirectURI    = "com.openai.chat://auth0.openai.com/ios/com.openai.chat/callback"
	iosAppScope          = "openid email profile offline_access model.request model.read organization.read offline"
	iosAppAudience       = "https://api.openai.com/v1"
)

// iOSAppOAuthResponse iOS App OAuth token 响应
type iOSAppOAuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// generatePKCECodes 生成 PKCE code_verifier 和 code_challenge
func generatePKCECodes() (codeVerifier, codeChallenge string, err error) {
	// 生成 32 字节的随机数作为 code_verifier
	verifierBytes := make([]byte, 32)
	if _, err := cryptorand.Read(verifierBytes); err != nil {
		return "", "", fmt.Errorf("生成 code_verifier 失败: %w", err)
	}
	codeVerifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	// 使用 SHA256 生成 code_challenge
	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return codeVerifier, codeChallenge, nil
}


// getRefreshTokenViaIOSAppFlow 通过 iOS App OAuth 流程获取 refresh_token
// 这个流程不需要 ChatGPT Plus 订阅
func (br *BrowserRegister) getRefreshTokenViaIOSAppFlow(page *rod.Page, email, password string) (string, string, error) {
	fmt.Println("\n尝试 iOS App OAuth 流程获取 Refresh Token...")

	// 1. 生成 PKCE 代码
	codeVerifier, codeChallenge, err := generatePKCECodes()
	if err != nil {
		return "", "", fmt.Errorf("生成 PKCE 失败: %w", err)
	}
	fmt.Printf("[ios-oauth] code_verifier=%s...\n", codeVerifier[:20])
	fmt.Printf("[ios-oauth] code_challenge=%s\n", codeChallenge)

	// 2. 构建授权 URL
	authURL := fmt.Sprintf("%s?client_id=%s&audience=%s&redirect_uri=%s&scope=%s&response_type=code&code_challenge=%s&code_challenge_method=S256&prompt=login",
		iosAppAuthorizeURL,
		iosAppClientID,
		url.QueryEscape(iosAppAudience),
		url.QueryEscape(iosAppRedirectURI),
		url.QueryEscape(iosAppScope),
		codeChallenge,
	)
	fmt.Printf("[ios-oauth] 授权 URL: %s\n", authURL)

	// 3. 在新页面中打开授权 URL
	authPage, err := br.browser.Page(proto.TargetCreateTarget{URL: authURL})
	if err != nil {
		return "", "", fmt.Errorf("打开授权页面失败: %w", err)
	}
	defer authPage.Close()

	// 监听回调 URL
	authPage.MustEval(`() => {
		window.__iosCallback = null;
		const origOpen = XMLHttpRequest.prototype.open;
		XMLHttpRequest.prototype.open = function(method, url) {
			if (url && url.includes('com.openai.chat://')) {
				window.__iosCallback = url;
			}
			return origOpen.apply(this, arguments);
		};
	}`)

	time.Sleep(3 * time.Second)

	// 4. 检查是否需要登录
	fmt.Println("[ios-oauth] 检查登录状态...")
	currentURL := authPage.MustEval(`() => window.location.href`).String()
	fmt.Printf("[ios-oauth] 当前 URL: %s\n", currentURL)

	// 检查是否已经在登录页面
	if strings.Contains(currentURL, "auth0.openai.com") || strings.Contains(currentURL, "login") {
		fmt.Println("[ios-oauth] 需要登录，尝试自动填写...")

		// 等待页面加载
		time.Sleep(2 * time.Second)

		// 尝试填写邮箱
		emailInput, err := authPage.Timeout(10 * time.Second).Element("input[name='username'], input[type='email'], input[name='email']")
		if err == nil {
			emailInput.MustInput(email)
			fmt.Printf("[ios-oauth] 已填写邮箱: %s\n", email)
			time.Sleep(500 * time.Millisecond)

			// 点击继续按钮
			continueBtn, _ := authPage.Timeout(5 * time.Second).Element("button[type='submit'], button[name='action'], input[type='submit']")
			if continueBtn != nil {
				continueBtn.MustClick()
				time.Sleep(2 * time.Second)
			}

			// 填写密码
			passwordInput, _ := authPage.Timeout(10 * time.Second).Element("input[name='password'], input[type='password']")
			if passwordInput != nil {
				passwordInput.MustInput(password)
				fmt.Println("[ios-oauth] 已填写密码")
				time.Sleep(500 * time.Millisecond)

				// 点击登录按钮
				loginBtn, _ := authPage.Timeout(5 * time.Second).Element("button[type='submit'], button[name='action']")
				if loginBtn != nil {
					loginBtn.MustClick()
					fmt.Println("[ios-oauth] 已点击登录")
				}
			}
		}
	}

	// 5. 等待授权完成 (监听回调 URL)
	fmt.Println("[ios-oauth] 等待授权回调...")
	var callbackURL string
	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {
		// 检查是否有回调 URL
		callbackURL = authPage.MustEval(`() => window.__iosCallback || ""`).String()
		if callbackURL != "" {
			break
		}

		// 也检查当前 URL
		currentURL = authPage.MustEval(`() => window.location.href`).String()
		if strings.Contains(currentURL, "com.openai.chat://") {
			callbackURL = currentURL
			break
		}

		// 检查是否有错误
		errorText := authPage.MustEval(`() => {
			const errorEl = document.querySelector('.alert-error, .error, [class*="error"]');
			return errorEl ? errorEl.innerText : "";
		}`).String()
		if errorText != "" {
			return "", "", fmt.Errorf("授权失败: %s", errorText)
		}

		time.Sleep(1 * time.Second)
	}

	if callbackURL == "" {
		return "", "", fmt.Errorf("等待授权回调超时")
	}

	fmt.Printf("[ios-oauth] 回调 URL: %s\n", callbackURL)

	// 6. 解析回调 URL 获取授权码
	parsedURL, err := url.Parse(callbackURL)
	if err != nil {
		return "", "", fmt.Errorf("解析回调 URL 失败: %w", err)
	}

	code := parsedURL.Query().Get("code")
	if code == "" {
		// 尝试从 fragment 中获取
		fragment := parsedURL.Fragment
		if fragment != "" {
			vals, _ := url.ParseQuery(fragment)
			code = vals.Get("code")
		}
	}

	if code == "" {
		return "", "", fmt.Errorf("未找到授权码")
	}
	fmt.Printf("[ios-oauth] 获取到授权码: %s...\n", code[:min(20, len(code))])

	// 7. 用授权码换取 tokens
	tokenReq := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     iosAppClientID,
		"code":          code,
		"redirect_uri":  iosAppRedirectURI,
		"code_verifier": codeVerifier,
	}
	tokenReqBody, _ := json.Marshal(tokenReq)

	req, err := http.NewRequest("POST", iosAppTokenURL, bytes.NewReader(tokenReqBody))
	if err != nil {
		return "", "", fmt.Errorf("创建 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	if br.config.Proxy != "" {
		proxyURL, err := url.Parse(br.config.Proxy)
		if err == nil {
			httpClient.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("[ios-oauth] token 响应状态: %d\n", resp.StatusCode)
	fmt.Printf("[ios-oauth] token 响应: %s\n", string(respBody))

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("token 交换失败: %s", string(respBody))
	}

	var tokenResp iOSAppOAuthResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", "", fmt.Errorf("解析 token 响应失败: %w", err)
	}

	if tokenResp.RefreshToken == "" {
		return "", "", fmt.Errorf("响应中未包含 refresh_token")
	}

	fmt.Println("[ios-oauth] 成功获取 refresh_token!")
	return tokenResp.RefreshToken, tokenResp.IDToken, nil
}



type deviceUserCodeRequest struct {
	ClientID string `json:"client_id"`
}

type deviceUserCodeResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	UserCodeAlt  string `json:"usercode"`
	Interval     string `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	CodeChallenge     string `json:"code_challenge"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func requestDeviceUserCodeViaPage(page *rod.Page) (*deviceUserCodeResponse, error) {
	result := page.MustEval(`() => {
		return fetch('https://auth.openai.com/api/accounts/deviceauth/usercode', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
				'Accept': 'application/json'
			},
			body: JSON.stringify({ client_id: 'app_EMoamEEZ73f0CkXaXp7hrann' })
		}).then(async (r) => ({ status: r.status, body: await r.text() }))
		  .catch((e) => ({ status: 0, body: String(e) }));
	}`)

	status := int(result.Get("status").Int())
	body := result.Get("body").String()
	fmt.Printf("[device-usercode-page] status=%d body=%s\n", status, body)
	if status != 200 {
		return nil, fmt.Errorf("页面请求设备码失败: %s", body)
	}

	var resp deviceUserCodeResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("解析页面设备码响应失败: %w", err)
	}
	return &resp, nil
}

// requestDeviceUserCode 请求设备码
func requestDeviceUserCode(httpClient *http.Client) (*deviceUserCodeResponse, error) {
	body, err := json.Marshal(deviceUserCodeRequest{ClientID: codexClientID})
	if err != nil {
		return nil, fmt.Errorf("编码设备码请求失败: %w", err)
	}

	req, err := http.NewRequest("POST", codexDeviceUserCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("创建设备码请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求设备码失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取设备码响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("设备码请求失败: %s", string(respBody))
	}

	var result deviceUserCodeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析设备码响应失败: %w", err)
	}

	return &result, nil
}

// pollDeviceToken 轮询获取授权码
func pollDeviceToken(httpClient *http.Client, deviceAuthID, userCode string, interval time.Duration) (*deviceTokenResponse, error) {
	deadline := time.Now().Add(codexDeviceTimeout)
	attempt := 0

	for {
		attempt++
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("设备码认证超时")
		}

		body := fmt.Sprintf(`{"device_auth_id":"%s","user_code":"%s"}`, deviceAuthID, userCode)
		req, err := http.NewRequest("POST", codexDeviceTokenURL, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			time.Sleep(codexDevicePollInterval)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(respBody) > 0 {
			fmt.Printf("[device-poll] attempt=%d status=%d body=%s\n", attempt, resp.StatusCode, string(respBody))
		} else {
			fmt.Printf("[device-poll] attempt=%d status=%d body=<empty>\n", attempt, resp.StatusCode)
		}

		if resp.StatusCode == 200 {
			var result deviceTokenResponse
			if err := json.Unmarshal(respBody, &result); err != nil {
				return nil, fmt.Errorf("解析授权码响应失败: %w", err)
			}
			return &result, nil
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			time.Sleep(interval)
			continue
		}

		time.Sleep(interval)
	}
}

func pollDeviceTokenViaPage(page *rod.Page, deviceAuthID, userCode string, interval time.Duration) (*deviceTokenResponse, error) {
	deadline := time.Now().Add(codexDeviceTimeout)
	attempt := 0
	for {
		attempt++
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("设备码认证超时")
		}

		result := page.MustEval(`(deviceAuthID, userCode) => {
			return fetch('https://auth.openai.com/api/accounts/deviceauth/token', {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json',
					'Accept': 'application/json'
				},
				body: JSON.stringify({ device_auth_id: deviceAuthID, user_code: userCode })
			}).then(async (r) => ({ status: r.status, body: await r.text() }))
			  .catch((e) => ({ status: 0, body: String(e) }));
		}`, deviceAuthID, userCode)

		status := int(result.Get("status").Int())
		body := result.Get("body").String()
		if body != "" {
			fmt.Printf("[device-poll-page] attempt=%d status=%d body=%s\n", attempt, status, body)
		} else {
			fmt.Printf("[device-poll-page] attempt=%d status=%d body=<empty>\n", attempt, status)
		}

		if status == 200 {
			var tokenResp deviceTokenResponse
			if err := json.Unmarshal([]byte(body), &tokenResp); err != nil {
				return nil, fmt.Errorf("解析授权码响应失败: %w", err)
			}
			return &tokenResp, nil
		}

		if status == http.StatusForbidden || status == http.StatusNotFound {
			time.Sleep(interval)
			continue
		}

		time.Sleep(interval)
	}
}

// exchangeCodeForTokens 用授权码换取 tokens
func exchangeCodeForTokens(httpClient *http.Client, authCode, codeVerifier string) (*oauthTokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {authCode},
		"redirect_uri":  {codexDeviceCallbackURI},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequest("POST", codexOAuthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 token 响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token 交换失败: %s", string(respBody))
	}

	var result oauthTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}

	return &result, nil
}

// getRefreshTokenViaDeviceFlow 通过设备码流程获取 refresh_token
func (br *BrowserRegister) getRefreshTokenViaDeviceFlow(page *rod.Page, email, password string) (string, string, error) {
	fmt.Println("\n步骤6: 通过设备码流程获取 Refresh Token...")

	if page == nil {
		return "", "", fmt.Errorf("设备码流程缺少浏览器页面上下文")
	}
	if err := page.Navigate(codexDeviceVerificationURL); err != nil {
		return "", "", fmt.Errorf("打开验证页面失败: %w", err)
	}
	time.Sleep(3 * time.Second)

	fmt.Println("请求设备码...")
	deviceResp, err := requestDeviceUserCodeViaPage(page)
	if err != nil {
		return "", "", fmt.Errorf("请求设备码失败: %w", err)
	}

	fmt.Printf("设备码: %s\n", deviceResp.UserCode)
	fmt.Printf("请访问: %s 并输入设备码\n", codexDeviceVerificationURL)
	pollInterval := codexDevicePollInterval
	if strings.TrimSpace(deviceResp.Interval) != "" {
		if sec, err := strconv.Atoi(strings.TrimSpace(deviceResp.Interval)); err == nil && sec > 0 {
			pollInterval = time.Duration(sec) * time.Second
		}
	}

	// 等待页面加载
	time.Sleep(2 * time.Second)

	// 3. 输入设备码
	codeInput, err := page.Timeout(10 * time.Second).Element("input[name='user_code'], input[type='text']")
	if err != nil {
		// 尝试其他选择器
		codeInput, err = page.Timeout(5 * time.Second).Element("input")
		if err != nil {
			fmt.Println("无法找到设备码输入框，跳过 refresh_token 获取")
			return "", "", nil
		}
	}

	// 分段输入设备码
	for i, ch := range deviceResp.UserCode {
		if i > 0 && (ch == '-' || i == 4) {
			// 等待输入框切换
			time.Sleep(200 * time.Millisecond)
		}
		activeInput, _ := page.Element("input:focus")
		if activeInput != nil {
			activeInput.MustInput(string(ch))
		} else {
			codeInput.MustInput(string(ch))
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Println("已输入设备码")
	time.Sleep(2 * time.Second)

	// 4. 点击确认按钮
	confirmBtn, err := page.Timeout(5 * time.Second).Element("button[type='submit']")
	if err == nil {
		confirmBtn.MustClick()
		fmt.Println("已点击确认按钮")
		time.Sleep(3 * time.Second)
	}

	// 5. 处理登录/授权页面（可能需要重新登录）
	currentURL := page.MustInfo().URL
	fmt.Printf("当前URL: %s\n", currentURL)

	// 如果需要登录，使用已注册的账号登录
	if strings.Contains(currentURL, "log-in") || strings.Contains(currentURL, "login") {
		fmt.Println("需要登录...")

		// 等待页面加载
		time.Sleep(3 * time.Second)

		// 尝试多种选择器
		emailInput, err := page.Timeout(10 * time.Second).Element("input[name='email']")
		if err != nil {
			emailInput, err = page.Timeout(5 * time.Second).Element("input[type='email']")
		}
		if err != nil {
			emailInput, err = page.Timeout(5 * time.Second).Element("input")
		}

		if err != nil {
			fmt.Printf("未找到邮箱输入框: %v\n", err)
			// 打印页面上的所有 input
			inputs, _ := page.Elements("input")
			fmt.Printf("页面上的 input 元素数量: %d\n", len(inputs))
			return "", "", nil
		}

		fmt.Printf("找到邮箱输入框，输入邮箱: %s\n", email)
		emailInput.MustInput(email)
		time.Sleep(500 * time.Millisecond)

		// 点击继续
		continueBtn, err := page.Timeout(5 * time.Second).Element("button[type='submit']")
		if err != nil {
			continueBtn, err = page.Timeout(3 * time.Second).Element("button:contains('Continue')")
		}
		if err != nil {
			continueBtn, err = page.Timeout(3 * time.Second).Element("button")
		}

		if continueBtn != nil {
			fmt.Println("点击继续按钮...")
			continueBtn.MustClick()
			time.Sleep(3 * time.Second)
		}

		passSelectors := []string{"input[type='password']", "input[name='password']", "input[autocomplete='current-password']"}
		var passInput *rod.Element
		for s := 0; s < 3 && passInput == nil; s++ {
			for _, sel := range passSelectors {
				p, e := page.Timeout(5 * time.Second).Element(sel)
				if e == nil && p != nil {
					passInput = p
					break
				}
			}
			if passInput != nil {
				break
			}
			continueBtn2, _ := page.Timeout(2 * time.Second).Element("button[type='submit']")
			if continueBtn2 != nil {
				continueBtn2.MustEval("() => this.click()")
				time.Sleep(2 * time.Second)
			}
		}
		if passInput == nil {
			fmt.Printf("未找到密码输入框: context deadline exceeded\n")
			return "", "", nil
		}

		fmt.Printf("找到密码输入框，输入密码\n")
		passInput.MustClick()
		time.Sleep(200 * time.Millisecond)

		// 清空并重新输入密码
		passInput.MustSelectAllText()
		passInput.MustInput("")
		time.Sleep(100 * time.Millisecond)
		passInput.MustInput(password)
		time.Sleep(500 * time.Millisecond)

		// 验证密码已输入
		val, _ := passInput.Eval("() => this.value")
		fmt.Printf("密码输入值长度: %d\n", len(val.Value.String()))

		submitBtn, err := page.Timeout(5 * time.Second).Element("button[type='submit']")
		if err != nil {
			submitBtn, _ = page.Timeout(3 * time.Second).Element("button")
		}

		// 打印按钮信息
		if submitBtn != nil {
			btnText, _ := submitBtn.Eval("() => this.innerText || this.textContent || ''")
			disabled, _ := submitBtn.Eval("() => this.disabled")
			fmt.Printf("按钮文本: %s, 禁用: %v\n", btnText.Value.String(), disabled.Value.Bool())
		}

		if submitBtn != nil {
			fmt.Println("点击登录按钮...")

			// 使用JS点击，更可靠
			submitBtn.MustEval("() => this.click()")

			time.Sleep(2 * time.Second)

			// 检查是否有错误消息
			errorMsg, _ := page.Eval("() => { const el = document.querySelector('[role=alert], .error, .Error'); return el ? el.innerText : ''; }")
			if errorMsg.Value.String() != "" {
				fmt.Printf("检测到错误消息: %s\n", errorMsg.Value.String())
			}

			// 等待页面跳转
			for j := 0; j < 10; j++ {
				time.Sleep(1 * time.Second)
				newURL := page.MustInfo().URL
				fmt.Printf("  [%ds] URL: %s\n", j+1, newURL)
				if !strings.Contains(newURL, "log-in") && !strings.Contains(newURL, "login") {
					fmt.Println("登录成功，已跳转!")
					break
				}
			}
			br.saveDebugScreenshot(page, "device_flow_after_login_submit")
		} else {
			fmt.Println("未找到提交按钮，尝试按Enter键")
			page.MustEval("() => { const e = new KeyboardEvent('keydown', {key: 'Enter', code: 'Enter'}); document.dispatchEvent(e); }")
			time.Sleep(3 * time.Second)
		}

		fmt.Println("登录完成，等待页面跳转...")
		time.Sleep(3 * time.Second)
		postLoginURL := page.MustInfo().URL
		if strings.Contains(postLoginURL, "log-in/password") {
			fmt.Println("登录仍停留在密码页，继续轮询设备授权（可能需要额外验证/人工完成）")
		}
	}

	// 6. 点击授权按钮（如果出现）
	// 等待更长时间让页面加载
	time.Sleep(3 * time.Second)

	// 检查当前 URL
	currentURL = page.MustInfo().URL
	fmt.Printf("登录后URL: %s\n", currentURL)

	// 尝试点击授权按钮
	for i := 0; i < 5; i++ {
		// 检查URL是否已经完成授权
		currentURL = page.MustInfo().URL
		if strings.Contains(currentURL, "codex") || strings.Contains(currentURL, "device") {
			fmt.Println("检测到设备码页面，可能已完成授权")
			break
		}

		authorizeBtn, err := page.Timeout(3 * time.Second).Element("button[type='submit']")
		if err == nil {
			btnText := authorizeBtn.MustText()
			fmt.Printf("找到按钮: %s\n", btnText)
			if strings.Contains(strings.ToLower(btnText), "allow") ||
				strings.Contains(strings.ToLower(btnText), "authorize") ||
				strings.Contains(strings.ToLower(btnText), "continue") ||
				strings.Contains(strings.ToLower(btnText), "confirm") {
				authorizeBtn.MustClick()
				fmt.Println("已点击授权按钮")
				time.Sleep(3 * time.Second)
			}
		}

		// 检查是否有其他按钮
		buttons, _ := page.Elements("button")
		for _, btn := range buttons {
			text, _ := btn.Eval("() => this.innerText || this.textContent || ''")
			btnText := strings.ToLower(text.Value.String())
			if strings.Contains(btnText, "allow") ||
				strings.Contains(btnText, "authorize") ||
				strings.Contains(btnText, "continue") ||
				strings.Contains(btnText, "confirm") {
				btn.Eval("() => this.click()")
				fmt.Printf("点击按钮: %s\n", btnText)
				time.Sleep(2 * time.Second)
			}
		}

		time.Sleep(1 * time.Second)
	}
	fmt.Println("轮询获取授权码...")
	tokenResp, err := pollDeviceTokenViaPage(page, deviceResp.DeviceAuthID, deviceResp.UserCode, pollInterval)
	if err != nil {
		fmt.Printf("页面上下文轮询失败: %v\n", err)
		fmt.Println("切换到 HTTP 轮询...")
		tokenResp, err = pollDeviceToken(br.httpClient.client, deviceResp.DeviceAuthID, deviceResp.UserCode, pollInterval)
		if err != nil {
			fmt.Printf("HTTP 轮询失败: %v\n", err)
			return "", "", nil
		}
	}

	fmt.Println("获取到授权码，交换 tokens...")

	// 8. 用授权码换取 tokens
	oauthResp, err := exchangeCodeForTokens(br.httpClient.client, tokenResp.AuthorizationCode, tokenResp.CodeVerifier)
	if err != nil {
		fmt.Printf("交换 tokens 失败: %v\n", err)
		return "", "", nil
	}

	fmt.Println("成功获取 refresh_token!")
	return oauthResp.RefreshToken, oauthResp.IDToken, nil
}

// ========================================
// 设备指纹随机化
// ========================================

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
}

var screenResolutions = []struct {
	width, height int
}{
	{1920, 1080},
	{2560, 1440},
	{1366, 768},
	{1536, 864},
	{1440, 900},
	{1680, 1050},
}

var languages = []string{
	"en-US",
	"en-GB",
	"en",
}

var timezones = []string{
	"America/New_York",
	"America/Los_Angeles",
	"Europe/London",
	"Europe/Paris",
	"Asia/Tokyo",
	"Australia/Sydney",
}

// DeviceFingerprint 设备指纹
type DeviceFingerprint struct {
	UserAgent     string
	ScreenWidth   int
	ScreenHeight  int
	Language      string
	Timezone      string
	DeviceID      string
	Platform      string
	WebGLRenderer string
}

// generateDeviceFingerprint 生成随机设备指纹
func generateDeviceFingerprint() *DeviceFingerprint {
	fp := &DeviceFingerprint{
		UserAgent: userAgents[rand.Intn(len(userAgents))],
		Language:  languages[rand.Intn(len(languages))],
		Timezone:  timezones[rand.Intn(len(timezones))],
		Platform:  "Win32",
		DeviceID:  fmt.Sprintf("%x", rand.Int63()),
	}

	// 随机屏幕分辨率
	res := screenResolutions[rand.Intn(len(screenResolutions))]
	fp.ScreenWidth = res.width
	fp.ScreenHeight = res.height

	// 随机 WebGL 渲染器
	webglRenderers := []string{
		"ANGLE (Intel, Intel(R) UHD Graphics 630, OpenGL 4.1)",
		"ANGLE (NVIDIA, NVIDIA GeForce GTX 1060, OpenGL 4.6)",
		"ANGLE (NVIDIA, NVIDIA GeForce RTX 2070, OpenGL 4.6)",
		"ANGLE (AMD, AMD Radeon RX 580, OpenGL 4.5)",
		"ANGLE (Intel, Intel(R) Iris(R) Xe Graphics, OpenGL 4.1)",
	}
	fp.WebGLRenderer = webglRenderers[rand.Intn(len(webglRenderers))]

	// 根据 User-Agent 设置平台
	if strings.Contains(fp.UserAgent, "Windows") {
		fp.Platform = "Win32"
	} else if strings.Contains(fp.UserAgent, "Mac") {
		fp.Platform = "MacIntel"
	} else {
		fp.Platform = "Linux x86_64"
	}

	return fp
}

// getStealthJS 生成隐蔽脚本（带设备指纹）
func getStealthJS(fp *DeviceFingerprint) string {
	return fmt.Sprintf(`() => {
// 设备指纹伪装
Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
Object.defineProperty(navigator, 'languages', {get: () => ['%s', '%s']});
Object.defineProperty(navigator, 'platform', {get: () => '%s'});
Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => %d});
Object.defineProperty(screen, 'width', {get: () => %d});
Object.defineProperty(screen, 'height', {get: () => %d});
Object.defineProperty(screen, 'availWidth', {get: () => %d});
Object.defineProperty(screen, 'availHeight', {get: () => %d - 40});
Object.defineProperty(screen, 'colorDepth', {get: () => 24});
Object.defineProperty(screen, 'pixelDepth', {get: () => 24});

// WebGL 指纹伪装
const getParameter = WebGLRenderingContext.prototype.getParameter;
WebGLRenderingContext.prototype.getParameter = function(parameter) {
    if (parameter === 37445) return '%s';
    if (parameter === 37446) return 'Google Inc. (NVIDIA)';
    return getParameter.call(this, parameter);
};

// 时区伪装
const originalDateTimeFormat = Intl.DateTimeFormat;
Intl.DateTimeFormat = function(...args) {
    if (args[1] && typeof args[1] === 'object') {
        args[1].timeZone = args[1].timeZone || '%s';
    }
    return new originalDateTimeFormat(...args);
};

// 隐藏自动化特征
window.chrome = {runtime: {}};
Object.defineProperty(navigator, 'permissions', {
    get: () => ({
        query: () => Promise.resolve({state: 'granted'})
    })
});

console.log('[Stealth] Device fingerprint applied');
}`, fp.Language, fp.Language[:2], fp.Platform, 4+rand.Intn(5), fp.ScreenWidth, fp.ScreenHeight, fp.ScreenWidth, fp.ScreenHeight, fp.WebGLRenderer, fp.Timezone)
}
