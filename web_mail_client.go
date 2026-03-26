package main

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// WebMailClient 使用浏览器访问 chatgpt.org.uk 网页获取临时邮箱
type WebMailClient struct {
	browser      *rod.Browser
	page         *rod.Page
	currentEmail string
	proxyURL     string
	headless     bool
	usedOTPs     map[string]bool
	otpMutex     sync.RWMutex
	localProxy   *LocalProxyForwarder
}

// NewWebMailClient 创建 WebMailClient
func NewWebMailClient(proxyURL string, headless bool) *WebMailClient {
	return &WebMailClient{
		proxyURL: proxyURL,
		headless: headless,
		usedOTPs: make(map[string]bool),
	}
}

// Start 启动浏览器并导航到邮件页面
func (w *WebMailClient) Start() error {
	path, found := launcher.LookPath()
	if !found {
		return fmt.Errorf("未找到系统浏览器")
	}

	l := launcher.New().Bin(path).Headless(w.headless).
		Set("no-sandbox", "true").
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-infobars", "true").
		Set("excludeSwitches", "enable-automation").
		Set("useAutomationExtension", "false").
		Set("disable-gpu", "true").
		Set("disable-dev-shm-usage", "true").
		Set("window-size", "1920,1080")

	if w.proxyURL != "" {
		proxyURL, err := url.Parse(w.proxyURL)
		if err == nil {
			if proxyURL.User != nil {
				Println("WebMail: 代理需要认证，启动本地转发器...")
				localProxy, err := NewLocalProxyForwarder(w.proxyURL)
				if err != nil {
					return fmt.Errorf("创建本地代理失败: %v", err)
				}
				localAddr, err := localProxy.Start()
				if err != nil {
					return fmt.Errorf("启动本地代理失败: %v", err)
				}
				Printf("WebMail: 本地代理已启动: %s\n", localAddr)
				w.localProxy = localProxy
				l = l.Set("proxy-server", localAddr)
			} else {
				l = l.Set("proxy-server", proxyURL.Host)
			}
		}
	}

	u, err := l.Launch()
	if err != nil {
		return fmt.Errorf("启动浏览器失败: %v", err)
	}

	w.browser = rod.New().ControlURL(u).MustConnect()

	page, err := w.browser.Page(proto.TargetCreateTarget{URL: "https://mail.chatgpt.org.uk/"})
	if err != nil {
		return fmt.Errorf("打开邮件页面失败: %v", err)
	}
	w.page = page

	_ = page.Timeout(30 * time.Second).WaitLoad()
	time.Sleep(3 * time.Second)

	w.closeDialog()

	return nil
}

// closeDialog 关闭弹窗
func (w *WebMailClient) closeDialog() {
	dialogBtn, _ := w.page.Timeout(2 * time.Second).Element("button:has-text('Understood')")
	if dialogBtn != nil {
		dialogBtn.MustClick()
		time.Sleep(500 * time.Millisecond)
	}
}

// GetTempEmail 获取临时邮箱地址
func (w *WebMailClient) GetTempEmail() (string, error) {
	Println("\n📧 正在获取临时邮箱 (Web模式)...")

	if w.page == nil {
		if err := w.Start(); err != nil {
			return "", err
		}
	}

	w.closeDialog()

	randomBtn, _ := w.page.Timeout(3 * time.Second).Element("button:has-text('Random')")
	if randomBtn != nil {
		randomBtn.MustClick()
		time.Sleep(2 * time.Second)
		w.closeDialog()
	}

	emailEl, err := w.page.Timeout(5 * time.Second).Element("[class*='address'], [class*='email']")
	if err != nil || emailEl == nil {
		emailEl, _ = w.page.Timeout(2 * time.Second).Element("generic")
	}

	if emailEl != nil {
		text := emailEl.MustEval("() => this.innerText || this.textContent || ''").String()
		text = strings.TrimSpace(text)
		if strings.Contains(text, "@") && !strings.Contains(text, " ") {
			w.currentEmail = text
			Printf("[WebMail] ✅ 获取邮箱成功: %s\n", text)
			return text, nil
		}
	}

	emailFromURL := w.page.MustEval("() => window.location.pathname").String()
	emailFromURL = strings.TrimPrefix(emailFromURL, "/")
	if strings.Contains(emailFromURL, "@") {
		w.currentEmail = emailFromURL
		Printf("[WebMail] ✅ 从URL获取邮箱: %s\n", emailFromURL)
		return emailFromURL, nil
	}

	emailText := w.page.MustEval(`() => {
		const els = document.querySelectorAll('*');
		for (const el of els) {
			const text = el.innerText || el.textContent || '';
			if (text.match(/^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/)) {
				return text.trim();
			}
		}
		return '';
	}`).String()

	if emailText != "" {
		w.currentEmail = emailText
		Printf("[WebMail] ✅ 从页面提取邮箱: %s\n", emailText)
		return emailText, nil
	}

	return "", fmt.Errorf("无法从页面获取邮箱地址")
}

// CheckEmail 检查邮件
func (w *WebMailClient) CheckEmail(email string) (string, error) {
	return w.CheckEmailSkipUsed(email, nil)
}

// CheckEmailSkipUsed 检查邮件，跳过已使用的OTP
func (w *WebMailClient) CheckEmailSkipUsed(email string, usedOTPs map[string]bool) (string, error) {
	maxRetries := 30
	Printf("📬 WebMail 检查邮箱: %s\n", email)

	// Navigate to the specific email inbox
	if err := w.NavigateToEmail(email); err != nil {
		Printf("  导航到邮箱页面失败: %v\n", err)
	}

	for i := 0; i < maxRetries; i++ {
		refreshBtn, _ := w.page.Timeout(2 * time.Second).Element("button:has-text('Refresh')")
		if refreshBtn != nil {
			refreshBtn.MustClick()
			time.Sleep(2 * time.Second)
		}

		// Look for email items from OpenAI
		emailContent := w.page.MustEval(`() => {
			const results = [];
			// Look for email list items
			const items = document.querySelectorAll('li, [class*="email"], [class*="message"], tr, .inbox-item');
			for (const item of items) {
				const text = (item.innerText || item.textContent || '').toLowerCase();
				const subject = item.querySelector('[class*="subject"]')?.innerText || '';
				// Check if this looks like an OpenAI verification email
				if (text.includes('verify') || text.includes('verification') || 
					subject.toLowerCase().includes('verify') || subject.toLowerCase().includes('openai')) {
					results.push({ type: 'item', subject: subject, content: item.innerText, html: item.outerHTML });
				}
			}
			// Also check the full page content for verification links
			const bodyText = document.body.innerText || '';
			const bodyHtml = document.body.innerHTML || '';
			if (bodyText.includes('Your ChatGPT code') || bodyText.includes('verification code') ||
				bodyHtml.includes('auth.openai.com')) {
				results.push({ type: 'page', content: bodyText, html: bodyHtml });
			}
			return results;
		}`)

		contentStr := emailContent.String()

		// Try to extract OTP code first
		if otp := extractOTPCode(contentStr); otp != "" {
			if usedOTPs != nil && usedOTPs[otp] {
				Printf("  跳过已使用的OTP: %s\n", otp)
			} else {
				Printf("✅ WebMail 提取到验证码: %s\n", otp)
				return "OTP:" + otp, nil
			}
		}

		// Try to extract verification link
		if link := w.extractVerifyLinkSkipUsed(contentStr, usedOTPs); link != "" {
			Printf("✅ WebMail 提取到验证链接: %s\n", link)
			return link, nil
		}

		// Try clicking on email items to open them
		if link := w.openEmailAndGetContent(usedOTPs); link != "" {
			Printf("✅ WebMail 从邮件内容提取到验证信息: %s\n", link)
			return link, nil
		}

		Printf("  ⏳ WebMail 等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("WebMail 等待验证邮件超时")
}

// openEmailAndGetContent 点击邮件获取完整内容
func (w *WebMailClient) openEmailAndGetContent(usedOTPs map[string]bool) string {
	defer func() {
		if r := recover(); r != nil {
			Printf("  WebMail openEmailAndGetContent panic recovered: %v\n", r)
		}
	}()

	emailLinks, err := w.page.Timeout(3 * time.Second).Elements("a[href*='mail'], li[class*='email'], [class*='message']")
	if err != nil || len(emailLinks) == 0 {
		return ""
	}
	for _, link := range emailLinks {
		text := link.MustEval("() => this.innerText || this.textContent || ''").String()
		textLower := strings.ToLower(text)
		if strings.Contains(textLower, "verify") ||
			strings.Contains(textLower, "openai") ||
			strings.Contains(textLower, "chatgpt") {

			link.MustClick()
			time.Sleep(2 * time.Second)

			content := w.page.MustEval("() => document.body.innerText || document.body.textContent || ''").String()
			if verifyLink := w.extractVerifyLinkSkipUsed(content, usedOTPs); verifyLink != "" {
				return verifyLink
			}

			w.page.MustEval("() => history.back()")
			time.Sleep(1 * time.Second)
		}
	}
	return ""
}

// extractVerifyLinkSkipUsed 从内容中提取验证链接，跳过已使用的OTP
func (w *WebMailClient) extractVerifyLinkSkipUsed(content string, usedOTPs map[string]bool) string {
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

// MarkOTPUsed 标记OTP为已使用
func (w *WebMailClient) MarkOTPUsed(otp string) {
	w.otpMutex.Lock()
	defer w.otpMutex.Unlock()
	w.usedOTPs[otp] = true
}

// Close 关闭浏览器
func (w *WebMailClient) Close() {
	if w.page != nil {
		w.page.MustClose()
	}
	if w.browser != nil {
		w.browser.MustClose()
	}
	if w.localProxy != nil {
		w.localProxy.Stop()
	}
}

// NavigateToEmail 导航到特定邮箱的页面
func (w *WebMailClient) NavigateToEmail(email string) error {
	url := fmt.Sprintf("https://mail.chatgpt.org.uk/%s", email)
	if w.page == nil {
		return fmt.Errorf("浏览器未启动")
	}
	if err := w.page.Timeout(30 * time.Second).Navigate(url); err != nil {
		return err
	}
	_ = w.page.Timeout(10 * time.Second).WaitLoad()
	time.Sleep(2 * time.Second)
	w.closeDialog()
	return nil
}
