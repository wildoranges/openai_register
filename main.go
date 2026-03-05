package main

import (
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
		OutputDir: "./creds",
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
	Email       string    `json:"email"`
	Password    string    `json:"password"`
	AccessToken string    `json:"access_token"`
	SessionID   string    `json:"session_id"`
	UserID      string    `json:"user_id"`
	CreatedAt   time.Time `json:"created_at"`
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
	browser    *rod.Browser
	httpClient *HTTPClient
	config     *Config
}

func NewBrowserRegister(config *Config) *BrowserRegister {
	return &BrowserRegister{
		httpClient: NewHTTPClientWithProxy(config.Proxy),
		config:     config,
	}
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
		Set("window-size", "1920,1080").
		Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

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

	// 完整的隐蔽模式脚本 - 绕过各种检测
	fmt.Println("注入隐蔽脚本...")
	page.MustEval(`() => {
		// 移除webdriver标记
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		
		// 伪造plugins
		Object.defineProperty(navigator, 'plugins', {
			get: () => {
				return [
					{description: 'Portable Document Format', filename: 'internal-pdf-viewer', name: 'Chrome PDF Plugin'},
					{description: '', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', name: 'Chrome PDF Viewer'},
					{description: '', filename: 'internal-nacl-plugin', name: 'Native Client'}
				];
			}
		});
		
		// 伪造languages
		Object.defineProperty(navigator, 'languages', {get: () => ['en-US', 'en', 'zh-CN']});
		
		// 伪造platform
		Object.defineProperty(navigator, 'platform', {get: () => 'Linux x86_64'});
		
		// 伪造hardwareConcurrency
		Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 8});
		
		// 伪造deviceMemory
		Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
		
		// 添加chrome对象
		window.chrome = {
			runtime: {connect: function() {}, sendMessage: function() {}},
			loadTimes: function() {},
			csi: function() {},
			app: {}
		};
		
		// 覆盖permissions查询
		const originalQuery = window.navigator.permissions.query;
		window.navigator.permissions.query = (parameters) => (
			parameters.name === 'notifications' ?
				Promise.resolve({ state: Notification.permission }) :
				originalQuery(parameters)
		);
		
		// 伪造connection信息
		Object.defineProperty(navigator, 'connection', {
			get: () => ({
				effectiveType: '4g',
				rtt: 50,
				downlink: 10,
				saveData: false
			})
		});
		
		console.log('Stealth mode activated');
	}`)

	// 等待页面加载和React渲染
	fmt.Println("等待页面加载...")
	time.Sleep(5 * time.Second)

	// 处理Cloudflare挑战
	br.handleCloudflare(page)
	time.Sleep(2 * time.Second)

	// 截图调试
	br.saveDebugScreenshot(page, "01_initial_load")

	// 步骤0: 点击 "Sign up for free" 按钮
	fmt.Println("\n步骤0: 点击 Sign up for free...")

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
			}
		}
		if signupClicked {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !signupClicked {
		fmt.Println("⚠️ 未找到 Sign up 按钮")
		br.saveDebugScreenshot(page, "02_signup_not_found")
		return nil, fmt.Errorf("未找到 Sign up 按钮")
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

	if blocked, reason := br.detectUnsupportedRegionError(page); blocked {
		fmt.Println("❌ 检测到地区限制页面")
		br.saveDebugScreenshot(page, "03_region_not_supported")
		return nil, fmt.Errorf("当前IP/地区不支持OpenAI注册: %s", reason)
	}

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
		if blocked, reason := br.detectUnsupportedRegionError(page); blocked {
			fmt.Println("❌ 邮箱输入前检测到地区限制")
			br.saveDebugScreenshot(page, "03_region_not_supported")
			return nil, fmt.Errorf("当前IP/地区不支持OpenAI注册: %s", reason)
		}
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
		br.handlePostVerification(page)
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

	br.saveDebugScreenshot(page, "11_success")
	return credentials, nil
}

func (br *BrowserRegister) detectUnsupportedRegionError(page *rod.Page) (bool, string) {
	titleObj, err := page.Eval(`() => document.title || ""`)
	if err != nil {
		return false, ""
	}
	bodyObj, err := page.Eval(`() => document.body ? (document.body.innerText || "") : ""`)
	if err != nil {
		return false, ""
	}
	urlObj, err := page.Eval(`() => window.location.href || ""`)
	if err != nil {
		return false, ""
	}

	title := strings.ToLower(titleObj.Value.String())
	bodyText := strings.ToLower(bodyObj.Value.String())
	currentURL := strings.ToLower(urlObj.Value.String())
	combined := title + "\n" + bodyText + "\n" + currentURL

	indicators := []string{
		"country, region, or territory not supported",
		"country, region, or teritory not suported",
		"unsuperted country region territory",
		"unsupported country region territory",
		"request forbidden",
		"request forbiden",
		"not supported in your country",
	}

	for _, indicator := range indicators {
		if strings.Contains(combined, indicator) {
			reason := strings.TrimSpace(bodyObj.Value.String())
			if reason == "" {
				reason = "Country/Region/Territory not supported"
			}
			if len(reason) > 240 {
				reason = reason[:240] + "..."
			}
			return true, reason
		}
	}

	return false, ""
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
func (br *BrowserRegister) handlePostVerification(page *rod.Page) {
	step := 0 // 0: 初始, 1: 已提交
	name := ""

	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		currentURL := page.MustEval(`() => window.location.href`).String()
		fmt.Printf("当前URL: %s (步骤: %d)\n", currentURL, step)

		// 已跳转到主页面
		if strings.Contains(currentURL, "chatgpt.com") && !strings.Contains(currentURL, "auth") && !strings.Contains(currentURL, "log-in") && !strings.Contains(currentURL, "about-you") {
			fmt.Println("已跳转到主页面!")
			return
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

				// 解析 API 响应，提取 continue_url
				if apiResult.Get("ok").Bool() {
					bodyStr := apiResult.Get("body").String()
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

	filename := fmt.Sprintf("./debug_%s.png", name)
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
		"access_token": credentials.AccessToken,
		"email":        credentials.Email,
		"user_id":      credentials.UserID,
		"created_at":   credentials.CreatedAt,
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
	newRecord := fmt.Sprintf("# Account: %s\nOPENAI_ACCESS_TOKEN=%s\nOPENAI_EMAIL=%s\nOPENAI_PASSWORD=%s\n\n",
		credentials.Email, credentials.AccessToken, credentials.Email, credentials.Password)
	existingTokens += newRecord

	if err := os.WriteFile(tokenFile, []byte(existingTokens), 0644); err != nil {
		return err
	}

	return nil
}

// SaveCredentials 保存凭证到文件 (使用默认目录)
func SaveCredentials(credentials *AccountCredentials) error {
	return SaveCredentialsWithDir(credentials, "./creds")
}

func main() {
	// Go 1.20+ rand 自动初始化，无需手动 seed

	fmt.Println("====================================")
	fmt.Println("   OpenAI 账号注册工具")
	fmt.Println("   用于 CodeX 认证")
	fmt.Println("====================================")
	fmt.Println()

	// 加载配置
	configPath := "./config.json"
	config, err := LoadConfig(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v，使用默认配置\n", err)
		config = DefaultConfig()
	}

	// 检查命令行参数
	simMode := false
	count := config.Count // 默认使用配置文件中的数量
	for _, arg := range os.Args[1:] {
		if arg == "--sim" || arg == "-sim" {
			simMode = true
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

	if simMode {
		fmt.Println("[模拟模式] 生成测试凭证...")
		successCount := 0
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
			successCount++
		}
		fmt.Printf("\n模拟模式成功生成账号数: %d/%d\n", successCount, count)
	} else {
		fmt.Printf("将注册 %d 个账号\n\n", count)

		httpClient := NewHTTPClientWithProxy(config.Proxy)
		br := NewBrowserRegister(config)
		successCount := 0
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
			successCount++

			// 等待一段时间再注册下一个
			if i < count-1 {
				waitTime := 30 + rand.Intn(30)
				fmt.Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
				time.Sleep(time.Duration(waitTime) * time.Second)
			}
		}
		fmt.Printf("\n成功注册账号数: %d/%d\n", successCount, count)
	}

	fmt.Println("\n====================================")
	fmt.Println("   所有账号处理完成")
	fmt.Println("====================================")
}
