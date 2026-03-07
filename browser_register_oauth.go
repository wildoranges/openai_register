package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// BrowserRegisterOAuth 基于 OAuth PKCE 的浏览器注册
type BrowserRegisterOAuth struct {
	*BrowserRegister
	oauthServer *OAuthCallbackServer
}

// NewBrowserRegisterOAuth 创建新的 OAuth 注册器
func NewBrowserRegisterOAuth(config *Config) *BrowserRegisterOAuth {
	return &BrowserRegisterOAuth{
		BrowserRegister: NewBrowserRegister(config),
		oauthServer:     NewOAuthCallbackServer(),
	}
}

// RegisterWithOAuth 使用 OAuth PKCE 流程注册并获取 refresh_token
func (br *BrowserRegisterOAuth) RegisterWithOAuth(email, password string) (*AccountCredentials, error) {
	// 生成 PKCE 代码
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("生成 PKCE 代码失败: %v", err)
	}

	// 启动 OAuth 回调服务器
	if err := br.oauthServer.Start(); err != nil {
		return nil, fmt.Errorf("启动 OAuth 回调服务器失败: %v", err)
	}
	defer br.oauthServer.Stop()

	// 重置回调结果
	br.oauthServer.Reset()

	// 构建授权 URL
	authURL := BuildOAuthAuthURL(pkce)

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("开始 OAuth PKCE 注册流程")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("邮箱: %s\n", email)

	// 启动浏览器
	fingerprint := br.randomFingerprintProfile()
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
		Set("window-size", fmt.Sprintf("%d,%d", fingerprint.WindowWidth, fingerprint.WindowHeight)).
		Set("user-agent", fingerprint.UserAgent).
		Set("lang", strings.Split(fingerprint.AcceptLanguage, ",")[0])

	var localProxy *LocalProxyForwarder
	if br.config.Proxy != "" {
		proxyURL, err := url.Parse(br.config.Proxy)
		if err == nil {
			if proxyURL.User != nil {
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
				l = l.Set("proxy-server", proxyURL.Host)
			}
		}
	}

	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %v", err)
	}

	browser := rod.New().ControlURL(u).MustConnect()
	defer browser.MustClose()

	// 打开授权页面
	page, err := browser.Page(proto.TargetCreateTarget{URL: authURL})
	if err != nil {
		return nil, fmt.Errorf("打开授权页面失败: %v", err)
	}
	defer page.MustClose()

	_ = page.Timeout(60 * time.Second).WaitLoad()
	fmt.Println("注入隐蔽脚本...")

	fmt.Println("等待页面加载...")
	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "oauth_01_initial")

	// 处理注册流程
	if err := br.handleOAuthRegistration(page, email, password); err != nil {
		return nil, err
	}

	// 等待 OAuth 回调
	fmt.Println(">>> 等待 OAuth 回调 (最长 60 秒)...")
	result := br.oauthServer.WaitForResult(60 * time.Second)

	if result.Error != "" {
		return nil, fmt.Errorf("OAuth 回调失败: %s", result.Error)
	}

	if result.State != pkce.State {
		return nil, fmt.Errorf("State 不匹配，可能存在 CSRF 攻击")
	}

	fmt.Println("Authorization code 已获取")
	fmt.Println("State 校验通过")

	// 用 authorization code 兑换 token
	tokenResp, err := br.exchangeCodeForTokens(result.Code, pkce.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("Token 兑换失败: %v", err)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("OAuth 注册 + Token 获取成功!")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("邮箱:          %s\n", email)
	if len(tokenResp.AccessToken) > 40 {
		fmt.Printf("Access Token:  %s...\n", tokenResp.AccessToken[:40])
	}
	if tokenResp.RefreshToken != "" && len(tokenResp.RefreshToken) > 40 {
		fmt.Printf("Refresh Token: %s...\n", tokenResp.RefreshToken[:40])
	}
	fmt.Println(strings.Repeat("=", 60))

	credentials := &AccountCredentials{
		Email:        email,
		Password:     password,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		CreatedAt:    time.Now(),
	}

	return credentials, nil
}

// handleOAuthRegistration 处理 OAuth 注册流程
func (br *BrowserRegisterOAuth) handleOAuthRegistration(page *rod.Page, email, password string) error {
	time.Sleep(2 * time.Second)

	// 检查是否需要点击 Sign up
	signupClicked := false
	for i := 0; i < 5; i++ {
		links, err := page.Elements("a")
		if err == nil {
			for _, link := range links {
				text, err := link.Eval("() => this.innerText || this.textContent || ''")
				if err != nil {
					continue
				}
				linkText := strings.ToLower(text.Value.String())
				if strings.Contains(linkText, "sign up") || strings.Contains(linkText, "注册") {
					link.Eval("() => this.click()")
					fmt.Println("已点击 Sign up 链接")
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

	time.Sleep(2 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "oauth_02_after_signup")

	// 输入邮箱
	fmt.Println("\n步骤1: 输入邮箱...")
	emailSelectors := []string{
		"input[name='email']",
		"input[type='email']",
		"input[id*='email']",
		"input[autocomplete='email']",
		"input[autocomplete='username']",
		"input[placeholder*='email' i]",
	}

	if !br.inputTextWithWait(page, emailSelectors, email, "Email", 15*time.Second) {
		br.saveDebugScreenshot(page, "oauth_03_email_not_found")
		return fmt.Errorf("未找到邮箱输入框")
	}
	br.saveDebugScreenshot(page, "oauth_03_email_entered")

	// 点击 Continue
	fmt.Println("\n步骤2: 点击 Continue...")
	time.Sleep(1 * time.Second)
	continueSelectors := []string{
		"button[type='submit']",
		"button[data-testid='continue-button']",
		"button[name='continue']",
	}
	br.clickButtonWithWait(page, continueSelectors, 10*time.Second)
	br.clickElementByText(page, "button", "Continue")
	br.clickElementByText(page, "button", "继续")

	time.Sleep(3 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "oauth_04_after_continue")

	// 输入密码
	fmt.Println("\n步骤3: 输入密码...")
	time.Sleep(2 * time.Second)
	passwordSelectors := []string{
		"input[name='password']",
		"input[type='password']",
		"input[autocomplete='new-password']",
		"input[autocomplete='password']",
	}

	passwordFound := br.inputTextWithWait(page, passwordSelectors, password, "Password", 15*time.Second)
	if passwordFound {
		br.saveDebugScreenshot(page, "oauth_05_password_entered")

		fmt.Println("\n步骤4: 提交注册...")
		time.Sleep(1 * time.Second)
		br.clickButtonWithWait(page, []string{"button[type='submit']"}, 5*time.Second)
		for _, btnText := range []string{"Continue", "Sign up", "Create account", "继续"} {
			if br.clickElementByText(page, "button", btnText) {
				break
			}
		}
	}

	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "oauth_06_after_submit")

	// 等待验证邮件
	fmt.Println("\n步骤5: 等待验证邮件...")
	verifyLink, err := br.httpClient.CheckEmail(email)
	if err != nil {
		fmt.Printf("获取验证邮件失败: %v\n", err)
		fmt.Println("等待页面跳转...")
		time.Sleep(30 * time.Second)
	} else {
		fmt.Printf("获取到验证内容: %s\n", verifyLink)

		if strings.HasPrefix(verifyLink, "OTP:") {
			otpCode := strings.TrimPrefix(verifyLink, "OTP:")
			fmt.Printf("检测到 OTP 验证码: %s\n", otpCode)
			br.handleOTPInput(page, otpCode)
		} else {
			page.MustNavigate(verifyLink)
			time.Sleep(5 * time.Second)
			br.handleCloudflare(page)
		}
		br.saveDebugScreenshot(page, "oauth_07_after_verification")

		fmt.Println("\n步骤6: 处理个人信息...")
		time.Sleep(5 * time.Second)
		br.handlePostVerification(page)
	}

	// 处理 consent 页面
	br.handleConsentPage(page)

	return nil
}

// handleConsentPage 处理 OAuth consent 页面
func (br *BrowserRegisterOAuth) handleConsentPage(page *rod.Page) {
	for i := 0; i < 10; i++ {
		currentURL := page.MustEval("() => window.location.href").String()

		if strings.Contains(currentURL, "consent") {
			fmt.Println("检测到 consent 页面，点击同意...")
			time.Sleep(1 * time.Second)

			for _, btnText := range []string{"Continue", "Accept", "Agree", "继续", "同意", "接受"} {
				if br.clickElementByText(page, "button", btnText) {
					fmt.Printf("已点击 %s 按钮\n", btnText)
					break
				}
			}

			time.Sleep(2 * time.Second)
		}

		if !strings.Contains(currentURL, "consent") && !strings.Contains(currentURL, "auth") {
			fmt.Println("已离开 consent 页面")
			break
		}

		time.Sleep(1 * time.Second)
	}
}

// exchangeCodeForTokens 用 authorization code 兑换 token
func (br *BrowserRegisterOAuth) exchangeCodeForTokens(code, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {OAuthClientID},
		"code":          {code},
		"redirect_uri":  {OAuthRedirectURI},
		"code_verifier": {codeVerifier},
	}

	fmt.Println("正在用 authorization code 兑换 Token...")

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", OAuthTokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Token 兑换失败: HTTP %d, 响应: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	fmt.Println("Token 兑换成功!")
	return &tokenResp, nil
}
