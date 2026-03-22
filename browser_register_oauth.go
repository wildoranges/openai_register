package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	usedOTPs    map[string]bool
}

func NewBrowserRegisterOAuth(config *Config) *BrowserRegisterOAuth {
	return &BrowserRegisterOAuth{
		BrowserRegister: NewBrowserRegister(config),
		oauthServer:     NewOAuthCallbackServer(),
		usedOTPs:        make(map[string]bool),
	}
}

func NewBrowserRegisterOAuthWithProxy(config *Config, proxyURL string) *BrowserRegisterOAuth {
	return &BrowserRegisterOAuth{
		BrowserRegister: NewBrowserRegisterWithProxy(config, proxyURL),
		oauthServer:     NewOAuthCallbackServer(),
		usedOTPs:        make(map[string]bool),
	}
}

// RegisterWithOAuth 使用 OAuth PKCE 流程注册并获取 refresh_token
func (br *BrowserRegisterOAuth) RegisterWithOAuth(email, password, otp string) (*AccountCredentials, error) {
	// 生成 PKCE 代码
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("生成 PKCE 代码失败: %v", err)
	}

	// 启动 OAuth 回调服务器
	redirectURI, err := br.oauthServer.Start()
	if err != nil {
		return nil, fmt.Errorf("启动 OAuth 回调服务器失败: %v", err)
	}
	defer br.oauthServer.Stop()

	// 重置回调结果
	br.oauthServer.Reset()

	// 构建授权 URL
	authURL := BuildOAuthAuthURL(pkce, redirectURI)

	Println(strings.Repeat("=", 60))
	Println("开始 OAuth PKCE 注册流程")
	Println(strings.Repeat("=", 60))
	Printf("邮箱: %s\n", email)

	// 启动浏览器
	fingerprint := br.randomFingerprintProfile()
	path, found := launcher.LookPath()
	if !found {
		return nil, fmt.Errorf("未找到系统浏览器")
	}
	Printf("使用浏览器: %s\n", path)

	if br.config.Proxy != "" {
		Printf("使用代理: %s\n", br.config.Proxy)
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
				Println("代理需要认证，启动本地转发器...")
				localProxy, err = NewLocalProxyForwarder(br.config.Proxy)
				if err != nil {
					return nil, fmt.Errorf("创建本地代理失败: %v", err)
				}
				localAddr, err := localProxy.Start()
				if err != nil {
					return nil, fmt.Errorf("启动本地代理失败: %v", err)
				}
				Printf("本地代理已启动: %s\n", localAddr)
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

	br.browser = browser

	page, err := browser.Page(proto.TargetCreateTarget{URL: authURL})
	if err != nil {
		return nil, fmt.Errorf("打开授权页面失败: %v", err)
	}
	defer page.MustClose()

	_ = page.Timeout(60 * time.Second).WaitLoad()
	Println("注入隐蔽脚本...")

	Println("等待页面加载...")
	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "oauth_01_initial")

	if err := br.handleOAuthRegistration(page, email, password, otp); err != nil {
		return nil, err
	}

	currentURL := page.MustEval(`() => window.location.href`).String()
	isOnMainPage := strings.Contains(currentURL, "chatgpt.com") && !strings.Contains(currentURL, "auth")

	if isOnMainPage {
		Println("已到达主页面，尝试直接获取 token...")
		accessToken, userID := br.getAccessToken(page)
		if accessToken != "" {
			Println("\n" + strings.Repeat("=", 60))
			Println("直接获取 Token 成功!")
			Println(strings.Repeat("=", 60))
			Printf("邮箱:          %s\n", email)
			if len(accessToken) > 40 {
				Printf("Access Token:  %s...\n", accessToken[:40])
			}

			credentials := &AccountCredentials{
				Email:       email,
				Password:    password,
				AccessToken: accessToken,
				UserID:      userID,
				ExpiresIn:   86400,
				CreatedAt:   time.Now(),
			}
			return credentials, nil
		}
	}

	Println(">>> 等待 OAuth 回调 (最长 60 秒)...")
	result := br.oauthServer.WaitForResult(60 * time.Second)

	if result.Error != "" {
		return nil, fmt.Errorf("OAuth 回调失败: %s", result.Error)
	}

	if result.State != pkce.State {
		return nil, fmt.Errorf("State 不匹配，可能存在 CSRF 攻击")
	}

	Println("Authorization code 已获取")
	Println("State 校验通过")

	// 用 authorization code 兑换 token
	tokenResp, err := br.exchangeCodeForTokens(result.Code, pkce.CodeVerifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("Token 兑换失败: %v", err)
	}

	Println("\n" + strings.Repeat("=", 60))
	Println("OAuth 注册 + Token 获取成功!")
	Println(strings.Repeat("=", 60))
	Printf("邮箱:          %s\n", email)
	if len(tokenResp.AccessToken) > 40 {
		Printf("Access Token:  %s...\n", tokenResp.AccessToken[:40])
	}
	if tokenResp.RefreshToken != "" && len(tokenResp.RefreshToken) > 40 {
		Printf("Refresh Token: %s...\n", tokenResp.RefreshToken[:40])
	}
	Println(strings.Repeat("=", 60))

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

// LoginWithOAuth 使用 OAuth PKCE 流程登录已存在的账号
func (br *BrowserRegisterOAuth) LoginWithOAuth(email, password, otp string) (*AccountCredentials, error) {
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("生成 PKCE 代码失败: %v", err)
	}

	redirectURI, err := br.oauthServer.Start()
	if err != nil {
		return nil, fmt.Errorf("启动 OAuth 回调服务器失败: %v", err)
	}
	defer br.oauthServer.Stop()

	br.oauthServer.Reset()

	authURL := BuildOAuthAuthURL(pkce, redirectURI)

	Println(strings.Repeat("=", 60))
	Println("开始 OAuth 登录流程")
	Println(strings.Repeat("=", 60))
	Printf("邮箱: %s\n", email)

	fingerprint := br.randomFingerprintProfile()
	path, found := launcher.LookPath()
	if !found {
		return nil, fmt.Errorf("未找到系统浏览器")
	}
	Printf("使用浏览器: %s\n", path)

	if br.config.Proxy != "" {
		Printf("使用代理: %s\n", br.config.Proxy)
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
				Println("代理需要认证，启动本地转发器...")
				localProxy, err = NewLocalProxyForwarder(br.config.Proxy)
				if err != nil {
					return nil, fmt.Errorf("启动本地代理转发器失败: %v", err)
				}
				localAddr, err := localProxy.Start()
				if err != nil {
					return nil, fmt.Errorf("启动本地代理失败: %v", err)
				}
				Printf("本地代理已启动: %s\n", localAddr)
				l = l.Set("proxy-server", localAddr)
				defer localProxy.Stop()
			} else {
				l = l.Set("proxy-server", proxyURL.Host)
			}
		}
	}

	uri, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %v", err)
	}

	browser := rod.New().ControlURL(uri).MustConnect()
	defer browser.MustClose()

	br.browser = browser

	page, err := browser.Page(proto.TargetCreateTarget{URL: authURL})
	if err != nil {
		return nil, fmt.Errorf("打开授权页面失败: %v", err)
	}
	defer page.MustClose()

	_ = page.Timeout(60 * time.Second).WaitLoad()
	Println("注入隐蔽脚本...")

	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "login_01_initial")

	if err := br.handleOAuthLogin(page, email, password, otp); err != nil {
		return nil, err
	}

	accessToken, userID := br.getAccessToken(page)
	if accessToken != "" {
		Println("直接获取 Token 成功!")
		Println(strings.Repeat("=", 60))
		Printf("邮箱:          %s\n", email)
		if len(accessToken) > 40 {
			Printf("Access Token:  %s...\n", accessToken[:40])
		}

		credentials := &AccountCredentials{
			Email:       email,
			Password:    password,
			AccessToken: accessToken,
			UserID:      userID,
			ExpiresIn:   86400,
			CreatedAt:   time.Now(),
		}
		return credentials, nil
	}

	Println(">>> 等待 OAuth 回调 (最长 60 秒)...")
	result := br.oauthServer.WaitForResult(60 * time.Second)

	if result.Error != "" {
		return nil, fmt.Errorf("OAuth 回调失败: %s", result.Error)
	}

	if result.State != pkce.State {
		return nil, fmt.Errorf("State 不匹配，可能存在 CSRF 攻击")
	}

	Println("Authorization code 已获取")
	Println("State 校验通过")

	tokenResp, err := br.exchangeCodeForTokens(result.Code, pkce.CodeVerifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("Token 兑换失败: %v", err)
	}

	Println("\n" + strings.Repeat("=", 60))
	Println("OAuth 登录 + Token 获取成功!")
	Println(strings.Repeat("=", 60))
	Printf("邮箱:          %s\n", email)
	if len(tokenResp.AccessToken) > 40 {
		Printf("Access Token:  %s...\n", tokenResp.AccessToken[:40])
	}
	if tokenResp.RefreshToken != "" && len(tokenResp.RefreshToken) > 40 {
		Printf("Refresh Token: %s...\n", tokenResp.RefreshToken[:40])
	}
	Println(strings.Repeat("=", 60))

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

func (br *BrowserRegisterOAuth) handleOAuthLogin(page *rod.Page, email, password, otp string) error {
	time.Sleep(2 * time.Second)

	Println("\n步骤1: 输入邮箱...")
	emailSelectors := []string{
		"input[name='email']",
		"input[type='email']",
		"input[autocomplete='username']",
		"input[id*='email']",
	}
	for _, selector := range emailSelectors {
		emailInput, _ := page.Timeout(2 * time.Second).Element(selector)
		if emailInput != nil {
			emailInput.MustClick()
			emailInput.MustInput(email)
			Printf("  Email: 已输入 (%s)\n", selector)
			break
		}
	}
	br.saveDebugScreenshot(page, "login_02_email_entered")

	Println("\n步骤2: 点击 Continue...")
	time.Sleep(1 * time.Second)
	br.clickElementByText(page, "button", "Continue")
	br.clickElementByText(page, "button", "继续")
	submitBtn, _ := page.Timeout(2 * time.Second).Element("button[type='submit']")
	if submitBtn != nil {
		submitBtn.MustClick()
	}
	time.Sleep(3 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "login_03_after_continue")

	Println("\n步骤3: 输入密码...")
	passwordSelectors := []string{
		"input[name='password']",
		"input[type='password']",
		"input[autocomplete='current-password']",
	}
	for _, selector := range passwordSelectors {
		passwordInput, _ := page.Timeout(2 * time.Second).Element(selector)
		if passwordInput != nil {
			passwordInput.MustClick()
			passwordInput.MustInput(password)
			Printf("  Password: 已输入 (%s)\n", selector)
			break
		}
	}
	br.saveDebugScreenshot(page, "login_04_password_entered")

	Println("\n步骤4: 提交登录...")
	time.Sleep(1 * time.Second)
	br.clickElementByText(page, "button", "Continue")
	br.clickElementByText(page, "button", "继续")
	submitBtn, _ = page.Timeout(2 * time.Second).Element("button[type='submit']")
	if submitBtn != nil {
		submitBtn.MustClick()
	}
	time.Sleep(3 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "login_05_after_submit")

	loginTime := time.Now()

	currentURL := page.MustEval("() => window.location.href").String()
	Printf("当前URL: %s\n", currentURL)

	if strings.Contains(currentURL, "authorize") || strings.Contains(currentURL, "consent") {
		Println("\n步骤5: 无需验证码，继续处理...")
	} else if strings.Contains(currentURL, "about-you") {
		Println("\n步骤5: 检测到 about-you 页面，处理个人信息...")
		if err := br.handleAboutYouPageWithMode(page, true); err != nil {
			return err
		}
	} else {
		Println("\n步骤5: 检查是否需要验证码...")

		nameInput, _ := page.Timeout(1 * time.Second).Element("input[name='name']")
		birthdayInput, _ := page.Timeout(1 * time.Second).Element("input[name='birthday']")

		if nameInput != nil || birthdayInput != nil {
			Println("检测到 about-you 页面 (姓名/生日输入框)，处理个人信息...")
			if err := br.handleAboutYouPageWithMode(page, true); err != nil {
				return err
			}
		} else {
			otpInput, _ := page.Timeout(3 * time.Second).Element("input[type='text']")
			if otpInput != nil {
				Println("检测到 OTP 输入框，尝试触发发送新验证码...")

				for _, btnText := range []string{"Resend code", "Resend", "重新发送", "重发"} {
					if br.clickElementByText(page, "button", btnText) {
						Printf("已点击 %s 按钮，等待新验证码...\n", btnText)
						time.Sleep(3 * time.Second)
						break
					}
				}

				for _, linkText := range []string{"Resend code", "Resend", "重新发送", "重发"} {
					if br.clickElementByText(page, "a", linkText) {
						Printf("已点击 %s 链接，等待新验证码...\n", linkText)
						time.Sleep(3 * time.Second)
						break
					}
				}

				gmailClient, err := NewGmailOAuthClientWithCredential(br.config.GmailOAuth.Credential)
				if err == nil {
					Println("使用 Gmail API 等待新验证码...")
					otp, err = gmailClient.GetOpenAIOTPAfterTime(120*time.Second, br.usedOTPs, loginTime)
					if err != nil {
						Println("Gmail API 获取验证码失败，等待手动输入...")
						otp = br.readOTPFromStdin()
					} else {
						Printf("Gmail API 获取到验证码: %s\n", otp)
					}
				} else {
					Printf("Gmail OAuth 未配置: %v\n", err)
					otp = br.readOTPFromStdin()
				}

				if otp != "" {
					Printf("输入 OTP: %s\n", otp)
					otpInput.MustInput(otp)
					br.usedOTPs[otp] = true
					time.Sleep(1 * time.Second)
					br.clickElementByText(page, "button", "Continue")
					submitBtn, _ = page.Timeout(2 * time.Second).Element("button[type='submit']")
					if submitBtn != nil {
						submitBtn.MustClick()
					}
					time.Sleep(3 * time.Second)
				} else {
					Println("OTP 为空，跳过输入")
				}
			}
		}
	}

	br.saveDebugScreenshot(page, "login_06_after_verification")

	br.handleConsentPage(page)

	return nil
}

func (br *BrowserRegisterOAuth) readOTPFromStdin() string {
	Println("请手动输入验证码:")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	otp := strings.TrimSpace(line)
	re := regexp.MustCompile(`\d{6}`)
	matches := re.FindStringSubmatch(otp)
	if len(matches) > 0 {
		return matches[0]
	}
	return otp
}

func (br *BrowserRegisterOAuth) handleAboutYouPage(page *rod.Page) error {
	return br.handleAboutYouPageWithMode(page, false)
}

func (br *BrowserRegisterOAuth) handleAboutYouPageWithMode(page *rod.Page, isLogin bool) error {
	names := []string{"James", "John", "Robert", "Michael", "David", "William", "Richard", "Joseph", "Thomas", "Charles", "Daniel", "Matthew", "Anthony", "Mark", "Steven", "Paul", "Andrew", "Joshua", "Kenneth", "Kevin", "Brian", "George", "Edward", "Ronald", "Timothy", "Jason", "Jeffrey", "Ryan", "Jacob", "Gary", "Nicholas", "Eric", "Jonathan", "Stephen", "Larry", "Justin", "Scott", "Brandon", "Raymond", "Samuel", "Benjamin", "Gregory", "Frank", "Alexander", "Patrick", "Jack", "Dennis", "Jerry", "Tyler", "Aaron"}
	firstName := names[rand.Intn(len(names))]
	lastName := names[rand.Intn(len(names))]
	name := firstName + " " + lastName

	nameInput, _ := page.Timeout(2 * time.Second).Element("input[name='name']")
	if nameInput != nil {
		nameInput.MustClick()
		nameInput.MustInput(name)
		Printf("输入姓名: %s\n", name)
	}

	year := 1990 + rand.Intn(15)
	age := 2026 - year

	htmlContent := page.MustEval(`() => document.documentElement.outerHTML`).String()
	os.WriteFile("debug_about_you_page.html", []byte(htmlContent), 0644)
	Printf("已保存页面 HTML 到 debug_about_you_page.html\n")

	visibleInfo := page.MustEval(`() => {
		const ageInput = document.querySelector('input[name="age"]');
		const birthdayInput = document.querySelector('input[name="birthday"]');
		const birthdayField = document.querySelector('[data-type="month"]');
		return {
			ageExists: !!ageInput,
			ageVisible: ageInput ? (ageInput.offsetParent !== null) : false,
			birthdayExists: !!birthdayInput,
			birthdayVisible: !!birthdayField,
			hasDateSegments: !!birthdayField
		};
	}`)
	Printf("输入框状态: %v\n", visibleInfo)

	hasDateSegments := visibleInfo.Get("hasDateSegments").Bool()

	if hasDateSegments {
		Printf("检测到 React Aria DateField，使用键盘模拟设置日期: %d-01-15\n", year)

		segments := []struct {
			segType string
			value   string
		}{
			{"month", "01"},
			{"day", "15"},
			{"year", fmt.Sprintf("%d", year)},
		}

		for _, seg := range segments {
			selector := fmt.Sprintf(`[data-type="%s"]`, seg.segType)
			el, err := page.Timeout(2 * time.Second).Element(selector)
			if err != nil || el == nil {
				Printf("  未找到 %s segment: %v\n", seg.segType, err)
				continue
			}

			el.MustClick()
			time.Sleep(100 * time.Millisecond)

			_ = page.MustEval(fmt.Sprintf(`() => {
				const el = document.querySelector('[data-type="%s"]');
				if (!el) return;
				
				el.focus();
				
				const range = document.createRange();
				range.selectNodeContents(el);
				const sel = window.getSelection();
				sel.removeAllRanges();
				sel.addRange(range);
				
				const value = '%s';
				
				for (let i = 0; i < value.length; i++) {
					const char = value[i];
					
					const beforeInputEvent = new InputEvent('beforeinput', {
						bubbles: true,
						cancelable: true,
						data: char,
						inputType: 'insertText'
					});
					el.dispatchEvent(beforeInputEvent);
					
					if (i === 0) {
						el.textContent = char;
					} else {
						el.textContent += char;
					}
					
					const inputEvent = new InputEvent('input', {
						bubbles: true,
						data: char,
						inputType: 'insertText'
					});
					el.dispatchEvent(inputEvent);
				}
				
				el.dispatchEvent(new Event('change', {bubbles: true}));
			}`, seg.segType, seg.value))

			time.Sleep(150 * time.Millisecond)
		}

		_ = page.MustEval(fmt.Sprintf(`() => {
			const hiddenInput = document.querySelector('input[name="birthday"]');
			if (hiddenInput) {
				const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
				setter.call(hiddenInput, '%d-01-15');
				hiddenInput.dispatchEvent(new Event('input', {bubbles: true}));
				hiddenInput.dispatchEvent(new Event('change', {bubbles: true}));
			}
		}`, year))
		time.Sleep(200 * time.Millisecond)

		result := page.MustEval(`() => {
			const monthSegment = document.querySelector('[data-type="month"]');
			const daySegment = document.querySelector('[data-type="day"]');
			const yearSegment = document.querySelector('[data-type="year"]');
			const hiddenInput = document.querySelector('input[name="birthday"]');
			return {
				month: monthSegment ? monthSegment.textContent : 'N/A',
				day: daySegment ? daySegment.textContent : 'N/A',
				year: yearSegment ? yearSegment.textContent : 'N/A',
				hiddenValue: hiddenInput ? hiddenInput.value : 'N/A'
			};
		}`)
		Printf("设置日期结果: %v\n", result)
	} else {
		ageInput, _ := page.Timeout(1 * time.Second).Element("input[name='age']")
		if ageInput != nil {
			Printf("设置年龄: %d\n", age)
			ageInput.MustClick()
			ageInput.MustInput(fmt.Sprintf("%d", age))
			time.Sleep(500 * time.Millisecond)
		}
	}

	br.saveDebugScreenshot(page, "about_you_before_submit")

	time.Sleep(1 * time.Second)

	br.clickElementByText(page, "button", "Finish creating account")
	br.clickElementByText(page, "button", "Continue")
	submitBtn, _ := page.Timeout(2 * time.Second).Element("button[type='submit']")
	if submitBtn != nil {
		submitBtn.MustClick()
	}

	time.Sleep(5 * time.Second)

	for i := 0; i < 10; i++ {
		currentURL := page.MustEval("() => window.location.href").String()
		Printf("提交后 URL: %s\n", currentURL)

		htmlContent := page.MustEval(`() => document.documentElement.outerHTML`).String()
		if strings.Contains(htmlContent, "user_already_exists") || strings.Contains(htmlContent, "already exists") {
			Printf("检测到 user_already_exists 错误\n")
			os.WriteFile("debug_user_exists_error.html", []byte(htmlContent), 0644)
			br.saveDebugScreenshot(page, "user_exists_error")
			if isLogin {
				Printf("登录流程中检测到 user_already_exists 错误，别名已被其他密码使用\n")
				return ErrLoginFailedAliasUsed
			}
		}
		if strings.Contains(htmlContent, "authentication") && strings.Contains(htmlContent, "error") {
			Printf("检测到认证错误\n")
			os.WriteFile("debug_auth_error.html", []byte(htmlContent), 0644)
			br.saveDebugScreenshot(page, "auth_error")
			if isLogin {
				Printf("登录流程中检测到认证错误\n")
				return ErrLoginFailedAliasUsed
			}
		}

		if strings.Contains(currentURL, "add-phone") {
			Println("检测到 add-phone 页面，尝试跳过...")
			for _, skipText := range []string{"Skip", "Do this later", "Not now", "稍后", "跳过"} {
				if br.clickElementByText(page, "button", skipText) {
					Printf("已点击 %s 按钮...\n", skipText)
					time.Sleep(3 * time.Second)
					break
				}
			}
		} else if strings.Contains(currentURL, "consent") {
			Println("检测到 consent 页面，点击同意...")
			for _, btnText := range []string{"Continue", "Accept", "Agree", "继续", "同意", "接受"} {
				if br.clickElementByText(page, "button", btnText) {
					Printf("已点击 %s 按钮\n", btnText)
					time.Sleep(3 * time.Second)
					break
				}
			}
		} else if strings.Contains(currentURL, "chatgpt.com") || strings.Contains(currentURL, "localhost:1455") {
			Println("已完成，等待回调...")
			break
		}

		time.Sleep(2 * time.Second)
	}
	return nil
}

// handleOAuthRegistration 处理 OAuth 注册流程
func (br *BrowserRegisterOAuth) handleOAuthRegistration(page *rod.Page, email, password, otp string) error {
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
					Println("已点击 Sign up 链接")
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
	Println("\n步骤1: 输入邮箱...")
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
	Println("\n步骤2: 点击 Continue...")
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
	Println("\n步骤3: 输入密码...")
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

		Println("\n步骤4: 提交注册...")
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

	Println("\n步骤5: 等待验证码...")
	Println("\n========================================")
	Println("请检查您的邮箱获取验证码")
	Printf("邮箱: %s\n", email)
	Println("========================================")

	var verifyLink string
	if otp != "" {
		Printf("使用命令行提供的验证码: %s\n", otp)
		if strings.HasPrefix(strings.ToLower(otp), "http") {
			verifyLink = otp
		} else {
			verifyLink = "OTP:" + otp
		}
	} else if br.config.GmailOAuth.Enabled {
		Printf("使用 Gmail API 自动获取验证码...\n")

		var gmailClient *GmailOAuthClient
		var err error

		if br.config.GmailOAuth.Credential != nil {
			gmailClient, err = NewGmailOAuthClientWithCredential(br.config.GmailOAuth.Credential)
		} else {
			gmailClient, err = NewGmailOAuthClient(email)
		}

		if err != nil {
			Printf("Gmail API 初始化失败: %v\n", err)
			PrintGmailSetupInstructions()
			Println("请手动输入验证码:")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if strings.HasPrefix(strings.ToLower(input), "http") {
				verifyLink = input
			} else {
				verifyLink = "OTP:" + input
			}
		} else {
			autoOTP, err := gmailClient.GetOpenAIOTPSkipUsed(120*time.Second, br.usedOTPs)
			if err != nil {
				Printf("Gmail API 获取验证码失败: %v\n", err)
				Println("请手动输入验证码:")
				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(input)
				if strings.HasPrefix(strings.ToLower(input), "http") {
					verifyLink = input
				} else {
					verifyLink = "OTP:" + input
				}
			} else {
				Printf("Gmail API 获取到验证码: %s\n", autoOTP)
				br.usedOTPs[autoOTP] = true
				verifyLink = "OTP:" + autoOTP
			}
		}
	} else {
		Println("尝试从临时邮箱获取验证码...")

		autoVerifyLink, err := br.httpClient.CheckEmail(email)
		if err == nil && autoVerifyLink != "" {
			Printf("从临时邮箱获取到验证内容: %s\n", autoVerifyLink)
			verifyLink = autoVerifyLink
		} else {
			Printf("临时邮箱获取验证码失败: %v\n", err)
			otpFile := "/tmp/openai_otp.txt"
			Printf("请在文件 %s 中写入验证码\n", otpFile)
			Println("格式: 6位数字 或 验证链接")
			Println("示例: echo '123456' > /tmp/openai_otp.txt")

			deadline := time.Now().Add(180 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(otpFile)
				if err == nil && len(data) > 0 {
					input := strings.TrimSpace(string(data))
					if len(input) >= 6 {
						if strings.HasPrefix(strings.ToLower(input), "http") {
							verifyLink = input
						} else {
							re := regexp.MustCompile(`\d{6}`)
							if match := re.FindString(input); match != "" {
								verifyLink = "OTP:" + match
							}
						}
						os.Remove(otpFile)
						break
					}
				}
				time.Sleep(2 * time.Second)
			}
			if verifyLink == "" {
				Println("等待验证码超时")
			}
		}
	}
	Printf("用户输入: %s\n", verifyLink)

	if strings.HasPrefix(verifyLink, "OTP:") {
		otpCode := strings.TrimPrefix(verifyLink, "OTP:")
		Printf("检测到 OTP 验证码: %s\n", otpCode)
		br.handleOTPInput(page, otpCode)
	} else {
		page.MustNavigate(verifyLink)
		time.Sleep(5 * time.Second)
		br.handleCloudflare(page)
	}
	br.saveDebugScreenshot(page, "oauth_07_after_verification")

	Println("\n步骤6: 处理个人信息...")
	time.Sleep(5 * time.Second)
	if err := br.handlePostVerification(page); err != nil {
		return err
	}

	br.handleConsentPage(page)

	return nil
}

// handleConsentPage 处理 OAuth consent 页面
func (br *BrowserRegisterOAuth) handleConsentPage(page *rod.Page) {
	for i := 0; i < 10; i++ {
		currentURL := page.MustEval("() => window.location.href").String()

		if strings.Contains(currentURL, "consent") {
			Println("检测到 consent 页面，点击同意...")
			time.Sleep(1 * time.Second)

			for _, btnText := range []string{"Continue", "Accept", "Agree", "继续", "同意", "接受"} {
				if br.clickElementByText(page, "button", btnText) {
					Printf("已点击 %s 按钮\n", btnText)
					break
				}
			}

			time.Sleep(2 * time.Second)
		}

		if !strings.Contains(currentURL, "consent") && !strings.Contains(currentURL, "auth") {
			Println("已离开 consent 页面")
			break
		}

		time.Sleep(1 * time.Second)
	}
}

// exchangeCodeForTokens 用 authorization code 兑换 token
func (br *BrowserRegisterOAuth) exchangeCodeForTokens(code, codeVerifier, redirectURI string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {OAuthClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	Println("正在用 authorization code 兑换 Token...")

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

	Println("Token 兑换成功!")
	return &tokenResp, nil
}
