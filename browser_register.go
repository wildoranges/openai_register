package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// 定义错误类型
var (
	ErrUnsupportedEmail     = fmt.Errorf("邮箱不被支持，跳过该账号")
	ErrUserAlreadyExists    = fmt.Errorf("账号已存在，需要登录")
	ErrLoginFailedAliasUsed = fmt.Errorf("登录失败：别名已被其他账号使用，需要新别名")
)

type BrowserRegister struct {
	browser       *rod.Browser
	httpClient    *HTTPClient
	webMailClient *WebMailClient
	config        *Config
}

type BrowserFingerprintProfile struct {
	UserAgent           string
	AcceptLanguage      string
	NavigatorLanguages  []string
	Platform            string
	HardwareConcurrency int
	DeviceMemory        int
	WindowWidth         int
	WindowHeight        int
	ConnectionRTT       int
	ConnectionDownlink  int
}

func NewBrowserRegister(config *Config) *BrowserRegister {
	return &BrowserRegister{
		httpClient: NewHTTPClientWithProxy(config.Proxy),
		config:     config,
	}
}

func NewBrowserRegisterWithProxy(config *Config, proxyURL string) *BrowserRegister {
	return &BrowserRegister{
		httpClient: NewHTTPClientWithProxy(proxyURL),
		config:     config,
	}
}

func NewBrowserRegisterWithWebMail(config *Config, proxyURL string, headless bool) *BrowserRegister {
	return &BrowserRegister{
		webMailClient: NewWebMailClient(proxyURL, headless),
		config:        config,
	}
}

type Point struct {
	X, Y float64
}

func (br *BrowserRegister) randomFingerprintProfile() BrowserFingerprintProfile {
	profiles := []BrowserFingerprintProfile{
		{
			UserAgent:           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			AcceptLanguage:      "en-US,en;q=0.9",
			NavigatorLanguages:  []string{"en-US", "en"},
			Platform:            "Linux x86_64",
			HardwareConcurrency: 8,
			DeviceMemory:        8,
			WindowWidth:         1920,
			WindowHeight:        1080,
			ConnectionRTT:       40,
			ConnectionDownlink:  12,
		},
		{
			UserAgent:           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			AcceptLanguage:      "en-US,en;q=0.8",
			NavigatorLanguages:  []string{"en-US", "en"},
			Platform:            "Linux x86_64",
			HardwareConcurrency: 4,
			DeviceMemory:        4,
			WindowWidth:         1366,
			WindowHeight:        768,
			ConnectionRTT:       55,
			ConnectionDownlink:  8,
		},
		{
			UserAgent:           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
			AcceptLanguage:      "en-GB,en;q=0.9",
			NavigatorLanguages:  []string{"en-GB", "en"},
			Platform:            "Linux x86_64",
			HardwareConcurrency: 16,
			DeviceMemory:        16,
			WindowWidth:         2560,
			WindowHeight:        1440,
			ConnectionRTT:       30,
			ConnectionDownlink:  20,
		},
	}

	base := profiles[rand.Intn(len(profiles))]
	tinyJitter := rand.Intn(5) - 2
	base.ConnectionRTT = maxInt(10, base.ConnectionRTT+tinyJitter)
	base.ConnectionDownlink = maxInt(2, base.ConnectionDownlink+(rand.Intn(3)-1))
	return base
}

func toJSStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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

func (br *BrowserRegister) Register(email, password string) (*AccountCredentials, error) {
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
		Set("disable-software-rasterizer", "true").
		Set("disable-web-security", "true").
		Set("disable-features", "IsolateOrigins,site-per-process").
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

	br.browser = rod.New().ControlURL(u).MustConnect()
	defer br.browser.MustClose()

	signupURL := "https://auth.openai.com/authorize?client_id=TdJIcbe16WoTHtN95nyywh5E4yOo6ItG&audience=https%3A%2F%2Fapi.openai.com%2Fv1&redirect_uri=https%3A%2F%2Fchatgpt.com%2Fapi%2Fauth%2Fcallback%2Flogin-web&scope=openid+email+profile+offline_access+model.request+model.read+organization.read+organization.write&response_type=code&response_mode=query&state=state_is_immaterial&code_challenge=challenge_is_immaterial&code_challenge_method=S256&screen_hint=signup"

	page, err := br.browser.Page(proto.TargetCreateTarget{URL: signupURL})
	if err != nil {
		return nil, fmt.Errorf("打开注册页面失败: %v", err)
	}

	_ = page.Timeout(60 * time.Second).WaitLoad()
	Println("注入隐蔽脚本...")
	stealthScript := fmt.Sprintf(`() => {
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		Object.defineProperty(navigator, 'plugins', {
			get: () => {
				return [
					{description: 'Portable Document Format', filename: 'internal-pdf-viewer', name: 'Chrome PDF Plugin'},
					{description: '', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', name: 'Chrome PDF Viewer'},
					{description: '', filename: 'internal-nacl-plugin', name: 'Native Client'}
				];
			}
		});
		Object.defineProperty(navigator, 'languages', {get: () => %s});
		Object.defineProperty(navigator, 'platform', {get: () => %s});
		Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => %d});
		Object.defineProperty(navigator, 'deviceMemory', {get: () => %d});
		window.chrome = {
			runtime: {connect: function() {}, sendMessage: function() {}},
			loadTimes: function() {},
			csi: function() {},
			app: {}
		};
		const originalQuery = window.navigator.permissions.query;
		window.navigator.permissions.query = (parameters) => (
			parameters.name === 'notifications' ?
				Promise.resolve({ state: Notification.permission }) :
				originalQuery(parameters)
		);
		Object.defineProperty(navigator, 'connection', {
			get: () => ({
				effectiveType: '4g',
				rtt: %d,
				downlink: %d,
				saveData: false
			})
		});
		console.log('Stealth mode activated');
	}`,
		toJSStringArray(fingerprint.NavigatorLanguages),
		strconv.Quote(fingerprint.Platform),
		fingerprint.HardwareConcurrency,
		fingerprint.DeviceMemory,
		fingerprint.ConnectionRTT,
		fingerprint.ConnectionDownlink,
	)
	page.MustEval(stealthScript)

	Println("等待页面加载...")
	time.Sleep(5 * time.Second)

	br.handleCloudflare(page)
	time.Sleep(2 * time.Second)

	br.saveDebugScreenshot(page, "01_initial_load")

	Println("\n步骤0: 点击 Sign up for free...")

	signupClicked := false
	for i := 0; i < 5; i++ {
		buttons, err := page.Elements("button")
		if err == nil {
			for _, btn := range buttons {
				text, err := btn.Eval("() => this.innerText || this.textContent || ''")
				if err != nil {
					continue
				}
				btnText := strings.ToLower(text.Value.String())
				if strings.Contains(btnText, "sign up") {
					btn.Eval("() => this.click()")
					Println("  已点击 'Sign up' 按钮")
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
		Println("⚠️ 未找到 Sign up 按钮")
		br.saveDebugScreenshot(page, "02_signup_not_found")
		return nil, fmt.Errorf("未找到 Sign up 按钮")
	}

	Println("等待注册页面加载 (React SPA)...")
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)
		url, _ := page.Eval("() => window.location.href")
		inputs, _ := page.Elements("input")
		Printf("  [%ds] URL: %s | Inputs: %d\n", i+1, url.Value.String(), len(inputs))
		if len(inputs) > 0 {
			Println("  表单已加载!")
			break
		}
	}

	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "02_after_signup_click")

	if blocked, reason := br.detectUnsupportedRegionError(page); blocked {
		Println("❌ 检测到地区限制页面")
		br.saveDebugScreenshot(page, "03_region_not_supported")
		return nil, fmt.Errorf("当前IP/地区不支持OpenAI注册: %s", reason)
	}

	Println("\n步骤1: 输入邮箱...")
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
			Println("❌ 邮箱输入前检测到地区限制")
			br.saveDebugScreenshot(page, "03_region_not_supported")
			return nil, fmt.Errorf("当前IP/地区不支持OpenAI注册: %s", reason)
		}
		Println("⚠️ 未找到邮箱输入框")
		br.saveDebugScreenshot(page, "03_email_not_found")
		br.debugPageElements(page, "input")
		return nil, fmt.Errorf("未找到邮箱输入框")
	}
	br.saveDebugScreenshot(page, "04_email_entered")

	Println("\n步骤2: 点击Continue...")
	time.Sleep(1 * time.Second)

	continueClicked := br.clickButtonWithWait(page, []string{
		"button[type='submit']",
		"button[data-testid='continue-button']",
		"button[name='continue']",
		"input[type='submit']",
	}, 10*time.Second)

	if !continueClicked {
		if !br.clickElementByText(page, "button", "Continue") {
			Println("⚠️ 未找到Continue按钮")
			br.saveDebugScreenshot(page, "05_continue_not_found")
			br.debugPageElements(page, "button")
		}
	}

	Println("等待页面响应...")
	time.Sleep(3 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "06_after_continue")

	Println("\n步骤3: 输入密码...")
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
		Println("⚠️ 未找到密码输入框，可能已使用OAuth或其他方式")
		br.saveDebugScreenshot(page, "07_password_not_found")
		br.debugPageElements(page, "input")
	} else {
		br.saveDebugScreenshot(page, "08_password_entered")

		Println("\n步骤3: 提交注册...")
		time.Sleep(1 * time.Second)

		for _, btnText := range []string{"Continue", "Sign up", "Create account", "Create"} {
			if br.clickElementByText(page, "button", btnText) {
				break
			}
		}
		br.clickButtonWithWait(page, []string{"button[type='submit']"}, 5*time.Second)
	}

	Println("\n等待注册处理...")
	time.Sleep(5 * time.Second)
	br.handleCloudflare(page)
	br.saveDebugScreenshot(page, "09_after_submit")

	br.handleCaptcha(page)

	Println("\n步骤4: 等待验证邮件...")
	verifyLink, err := br.httpClient.CheckEmail(email)
	if err != nil {
		Printf("获取验证邮件失败: %v\n", err)
		Println("等待页面跳转或手动验证...")
		time.Sleep(30 * time.Second)
	} else {
		Printf("获取到验证内容: %s\n", verifyLink)

		if strings.HasPrefix(verifyLink, "OTP:") {
			otpCode := strings.TrimPrefix(verifyLink, "OTP:")
			Printf("检测到OTP验证码: %s\n", otpCode)

			br.handleOTPInput(page, otpCode)
		} else {
			page.MustNavigate(verifyLink)
			time.Sleep(5 * time.Second)
			br.handleCloudflare(page)
		}
		br.saveDebugScreenshot(page, "09_after_verification")

		Println("\n等待页面加载...")
		time.Sleep(5 * time.Second)
		br.handleCloudflare(page)

		Println("\n处理后续步骤...")
		if err := br.handlePostVerification(page); err != nil {
			return nil, err
		}

		Println("\n步骤5: 获取Access Token...")
	}

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

func (br *BrowserRegister) handleCloudflare(page *rod.Page) {
	Println("检查Cloudflare挑战...")

	time.Sleep(2 * time.Second)

	for i := 0; i < 30; i++ {
		title := page.MustEval(`() => document.title || ""`).String()
		bodyText := page.MustEval(`() => document.body ? document.body.innerText : ""`).String()

		if strings.Contains(title, "Just a moment") ||
			strings.Contains(title, "Cloudflare") ||
			strings.Contains(bodyText, "Checking your browser") ||
			strings.Contains(bodyText, "Please Wait") ||
			strings.Contains(bodyText, "DDoS protection") {

			Printf("检测到Cloudflare挑战，等待自动解决... (%d/30)\n", i+1)

			cfCheckbox, _ := page.Timeout(2 * time.Second).Element("input[type='checkbox']")
			if cfCheckbox != nil {
				Println("尝试点击Cloudflare复选框...")
				cfCheckbox.MustClick()
				time.Sleep(5 * time.Second)
			}

			cfButton, _ := page.Timeout(1 * time.Second).Element("input[type='button'], button")
			if cfButton != nil {
				cfButton.MustClick()
				time.Sleep(3 * time.Second)
			}

			page.MustEval(`() => {
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
			currentURL := page.MustEval(`() => window.location.href`).String()
			if !strings.Contains(currentURL, "challenge") && !strings.Contains(currentURL, "cdn-cgi") {
				Println("Cloudflare挑战已通过!")
				return
			}
			break
		}
	}

	time.Sleep(2 * time.Second)
}

func (br *BrowserRegister) handleCaptcha(page *rod.Page) {
	Println("检查验证码...")

	recaptcha, _ := page.Timeout(3 * time.Second).Element("iframe[src*='recaptcha']")
	if recaptcha != nil {
		Println("⚠️ 检测到reCAPTCHA - 等待手动完成或使用验证码服务")
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			check, _ := page.Timeout(500 * time.Millisecond).Element("iframe[src*='recaptcha']")
			if check == nil {
				Println("reCAPTCHA已通过!")
				return
			}
		}
	}

	hcaptcha, _ := page.Timeout(3 * time.Second).Element("iframe[src*='hcaptcha']")
	if hcaptcha != nil {
		Println("⚠️ 检测到hCaptcha - 等待手动完成或使用验证码服务")
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			check, _ := page.Timeout(500 * time.Millisecond).Element("iframe[src*='hcaptcha']")
			if check == nil {
				Println("hCaptcha已通过!")
				return
			}
		}
	}

	turnstile, _ := page.Timeout(2 * time.Second).Element("iframe[src*='challenges.cloudflare.com']")
	if turnstile != nil {
		Println("检测到Cloudflare Turnstile验证...")
		time.Sleep(5 * time.Second)
	}
}

func (br *BrowserRegister) handleOTPInput(page *rod.Page, otpCode string) {
	Println("\n步骤5: 输入OTP验证码...")
	time.Sleep(2 * time.Second)

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

	for i := 0; i < 10; i++ {
		for _, sel := range otpSelectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err == nil && el != nil {
				isVisible, _ := el.Eval(`() => {
					const rect = this.getBoundingClientRect();
					return rect.width > 0 && rect.height > 0;
				}`)
				if isVisible.Value.Bool() {
					Printf("找到OTP输入框: %s\n", sel)
					el.MustClick()
					time.Sleep(200 * time.Millisecond)
					el.MustSelectAllText().MustInput(otpCode)
					Printf("已输入OTP码: %s\n", otpCode)
					time.Sleep(1 * time.Second)

					br.clickButtonWithWait(page, []string{"button[type='submit']"}, 3*time.Second)
					br.clickElementByText(page, "button", "Verify")
					br.clickElementByText(page, "button", "Continue")
					br.saveDebugScreenshot(page, "10_otp_entered")
					return
				}
			}
		}

		singleInputs, err := page.Elements("input[maxlength='1']")
		if err == nil && len(singleInputs) >= 6 {
			Printf("找到%d个单字符输入框，逐个输入OTP\n", len(singleInputs))
			for i, char := range otpCode {
				if i < len(singleInputs) {
					singleInputs[i].MustClick()
					singleInputs[i].MustInput(string(char))
					time.Sleep(100 * time.Millisecond)
				}
			}
			Printf("已输入OTP码: %s\n", otpCode)
			time.Sleep(1 * time.Second)
			br.clickButtonWithWait(page, []string{"button[type='submit']"}, 3*time.Second)
			br.saveDebugScreenshot(page, "10_otp_entered")
			return
		}

		Printf("等待OTP输入框... (%d/10)\n", i+1)
		time.Sleep(1 * time.Second)
	}

	Println("⚠️ 未找到OTP输入框")
	br.saveDebugScreenshot(page, "10_otp_not_found")
	br.debugPageElements(page, "input")
}

func (br *BrowserRegister) handlePostVerification(page *rod.Page) error {
	step := 0
	name := ""

	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		currentURL := page.MustEval(`() => window.location.href`).String()
		Printf("当前URL: %s (步骤: %d)\n", currentURL, step)

		if strings.Contains(currentURL, "chatgpt.com") && !strings.Contains(currentURL, "auth") && !strings.Contains(currentURL, "log-in") && !strings.Contains(currentURL, "about-you") {
			Println("已跳转到主页面!")
			return nil
		}

		if strings.Contains(currentURL, "add-phone") {
			Println("检测到 add-phone 页面，尝试跳过手机验证...")

			br.saveDebugScreenshot(page, "add_phone_page")

			// 尝试各种跳过按钮
			skipSelectors := []string{
				"button[data-testid='skip-button']",
				"button[aria-label*='skip' i]",
				"button[aria-label*='later' i]",
				"a[href*='skip']",
			}
			for _, sel := range skipSelectors {
				skipBtn, _ := page.Timeout(1 * time.Second).Element(sel)
				if skipBtn != nil {
					Println("点击跳过按钮: " + sel)
					skipBtn.MustClick()
					time.Sleep(2 * time.Second)
					continue
				}
			}

			skipTexts := []string{"Skip", "Do this later", "Not now", "Skip for now", "稍后", "跳过", "Maybe later", "Remind me later", "Not ready"}
			skipped := false
			for _, skipText := range skipTexts {
				if br.clickElementByText(page, "button", skipText) {
					Printf("点击 %s 按钮...\n", skipText)
					time.Sleep(2 * time.Second)
					skipped = true
					break
				}
			}

			// 尝试点击链接形式的跳过
			for _, skipText := range skipTexts {
				if br.clickElementByText(page, "a", skipText) {
					Printf("点击 %s 链接...\n", skipText)
					time.Sleep(2 * time.Second)
					skipped = true
					break
				}
			}

			if skipped {
				continue
			}

			// 无法跳过，尝试直接导航到 chatgpt.com 获取 session
			if i > 5 {
				Println("无法跳过手机验证，尝试直接获取 session token...")
				page.MustNavigate("https://chatgpt.com/")
				time.Sleep(3 * time.Second)
				return nil
			}

			// 尝试直接提交空手机号
			phoneInput, _ := page.Timeout(1 * time.Second).Element("input[type='tel']")
			if phoneInput != nil {
				Println("找到手机输入框，尝试直接提交...")
				submitBtn, _ := page.Timeout(1 * time.Second).Element("button[type='submit']")
				if submitBtn != nil {
					submitBtn.MustClick()
					time.Sleep(2 * time.Second)
				}
			}

			continue
		}

		if strings.Contains(currentURL, "about-you") {
			if step == 0 {
				nameInput, _ := page.Timeout(2 * time.Second).Element("input[name='name']")
				if nameInput != nil {
					val, _ := nameInput.Eval(`() => this.value`)
					if len(val.Value.String()) == 0 {
						names := []string{"James", "John", "Robert", "Michael", "David", "William", "Richard", "Joseph", "Thomas", "Charles", "Daniel", "Matthew", "Anthony", "Mark", "Steven", "Paul", "Andrew", "Joshua", "Kenneth", "Kevin", "Brian", "George", "Edward", "Ronald", "Timothy", "Jason", "Jeffrey", "Ryan", "Jacob", "Gary", "Nicholas", "Eric", "Jonathan", "Stephen", "Larry", "Justin", "Scott", "Brandon", "Raymond", "Samuel", "Benjamin", "Gregory", "Frank", "Alexander", "Patrick", "Jack", "Dennis", "Jerry", "Tyler", "Aaron", "Jose", "Adam", "Henry", "Nathan", "Douglas", "Zachary", "Peter", "Kyle"}
						firstName := names[rand.Intn(len(names))]
						lastName := names[rand.Intn(len(names))]
						name = firstName + " " + lastName
						Println("输入姓名: " + name)
						nameInput.MustClick()
						nameInput.MustInput(name)
						time.Sleep(300 * time.Millisecond)
					} else {
						name = val.Value.String()
					}
				}

				year := 1990 + rand.Intn(15)
				birthdate := fmt.Sprintf("%d-01-15", year)
				Printf("设置生日: %s\n", birthdate)

				// 检测是否有 React Aria DateField (有 data-type 属性的 segment)
				hasDateSegments := page.MustEval(`() => {
					return document.querySelector('[data-type="month"]') !== null ||
						   document.querySelector('[data-type="day"]') !== null ||
						   document.querySelector('[data-type="year"]') !== null;
				}`).Bool()

				if hasDateSegments {
					Printf("检测到 React Aria DateField，使用正确的事件序列设置日期\n")

					// 设置 month segment
					_ = page.MustEval(`() => {
						const el = document.querySelector('[data-type="month"]');
						if (!el) return;
						el.focus();
						const range = document.createRange();
						range.selectNodeContents(el);
						const sel = window.getSelection();
						sel.removeAllRanges();
						sel.addRange(range);
						const value = '01';
						for (let i = 0; i < value.length; i++) {
							const char = value[i];
							const beforeInputEvent = new InputEvent('beforeinput', {
								bubbles: true, cancelable: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(beforeInputEvent);
							if (i === 0) { el.textContent = char; } else { el.textContent += char; }
							const inputEvent = new InputEvent('input', {
								bubbles: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(inputEvent);
						}
						el.dispatchEvent(new Event('change', {bubbles: true}));
					}`)
					time.Sleep(150 * time.Millisecond)

					// 设置 day segment
					_ = page.MustEval(`() => {
						const el = document.querySelector('[data-type="day"]');
						if (!el) return;
						el.focus();
						const range = document.createRange();
						range.selectNodeContents(el);
						const sel = window.getSelection();
						sel.removeAllRanges();
						sel.addRange(range);
						const value = '15';
						for (let i = 0; i < value.length; i++) {
							const char = value[i];
							const beforeInputEvent = new InputEvent('beforeinput', {
								bubbles: true, cancelable: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(beforeInputEvent);
							if (i === 0) { el.textContent = char; } else { el.textContent += char; }
							const inputEvent = new InputEvent('input', {
								bubbles: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(inputEvent);
						}
						el.dispatchEvent(new Event('change', {bubbles: true}));
					}`)
					time.Sleep(150 * time.Millisecond)

					// 设置 year segment
					yearStr := fmt.Sprintf("%d", year)
					_ = page.MustEval(fmt.Sprintf(`() => {
						const el = document.querySelector('[data-type="year"]');
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
								bubbles: true, cancelable: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(beforeInputEvent);
							if (i === 0) { el.textContent = char; } else { el.textContent += char; }
							const inputEvent = new InputEvent('input', {
								bubbles: true, data: char, inputType: 'insertText'
							});
							el.dispatchEvent(inputEvent);
						}
						el.dispatchEvent(new Event('change', {bubbles: true}));
					}`, yearStr))
					time.Sleep(150 * time.Millisecond)

					// 同时设置 hidden input 以确保值同步
					_ = page.MustEval(fmt.Sprintf(`() => {
						const hiddenInput = document.querySelector('input[name="birthday"]');
						if (hiddenInput) {
							const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
							setter.call(hiddenInput, '%s');
							hiddenInput.dispatchEvent(new Event('input', {bubbles: true}));
							hiddenInput.dispatchEvent(new Event('change', {bubbles: true}));
						}
					}`, birthdate))

					// 验证日期是否正确设置
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
					// 回退到简单 input 方式
					Printf("使用简单 input 方式设置日期\n")
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
					Printf("设置生日结果: %v\n", result)
				}

				time.Sleep(500 * time.Millisecond)

				br.saveDebugScreenshot(page, "about_you_before_submit")

				Println("先尝试点击 Finish creating account 按钮...")
				clicked := false
				if br.clickElementByText(page, "button", "Finish creating account") {
					Println("已点击 Finish creating account")
					clicked = true
					time.Sleep(3 * time.Second)
				}

				if !clicked {
					submitBtn, _ := page.Timeout(1 * time.Second).Element("button[type='submit']")
					if submitBtn != nil {
						disabled, _ := submitBtn.Eval(`() => this.disabled`)
						if !disabled.Value.Bool() {
							Println("点击 Submit 按钮...")
							submitBtn.MustClick()
							clicked = true
							time.Sleep(3 * time.Second)
						}
					}
				}

				currentURL := page.MustEval("() => window.location.href").String()
				if clicked && !strings.Contains(currentURL, "about-you") {
					Printf("页面已跳转: %s\n", currentURL)
					step = 2
					continue
				}

				Println("按钮点击未跳转，尝试 API 提交...")
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
				Printf("API 结果: %v\n", apiResult)

				bodyStr := apiResult.Get("body").String()
				if !apiResult.Get("ok").Bool() {
					br.saveDebugScreenshot(page, "about_you_api_failed")
					htmlPath := fmt.Sprintf("debug_about_you_api_failed_%d.html", time.Now().Unix())
					if html, err := page.HTML(); err == nil {
						os.WriteFile(htmlPath, []byte(html), 0644)
						Printf("已保存页面 HTML: %s\n", htmlPath)
					}

					if strings.Contains(bodyStr, "unsupported_email") || strings.Contains(bodyStr, "not supported") {
						Println("❌ 邮箱不被 OpenAI 支持，跳过该账号")
						return ErrUnsupportedEmail
					}
					if strings.Contains(bodyStr, "user_already_exists") || strings.Contains(bodyStr, "already exists") {
						Println("⚠️ 账号已存在，需要登录")
						return ErrUserAlreadyExists
					}
					if strings.Contains(bodyStr, "registration_disallowed") {
						Println("⚠️ 注册被阻止，尝试登录...")
						return ErrUserAlreadyExists
					}
				}

				if apiResult.Get("ok").Bool() {
					bodyStr := apiResult.Get("body").String()
					var apiResp struct {
						ContinueURL string `json:"continue_url"`
					}
					if err := json.Unmarshal([]byte(bodyStr), &apiResp); err == nil && apiResp.ContinueURL != "" {
						Printf("获取到 continue_url: %s\n", apiResp.ContinueURL)
						Println("导航到 continue_url...")
						page.MustNavigate(apiResp.ContinueURL)
						time.Sleep(3 * time.Second)
						step = 2
						continue
					}
				}

				step = 1
			} else if step == 1 {
				Println("等待页面跳转...")
			}

			br.saveDebugScreenshot(page, fmt.Sprintf("11_about_you_step%d", step))
			continue
		}

		br.clickElementByText(page, "button", "Agree")
		br.clickElementByText(page, "button", "Accept")
	}

	return nil
}

func (br *BrowserRegister) getAccessToken(page *rod.Page) (string, string) {
	Println("尝试获取session...")

	for attempt := 0; attempt < 5; attempt++ {
		sessionPage, err := br.browser.Page(proto.TargetCreateTarget{URL: "https://chat.openai.com/api/auth/session"})
		if err != nil {
			Printf("打开session页面失败: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		time.Sleep(3 * time.Second)

		content, err := sessionPage.Eval(`() => document.body.innerText`)
		if err != nil {
			Printf("获取session内容失败: %v\n", err)
			sessionPage.MustClose()
			time.Sleep(2 * time.Second)
			continue
		}

		contentStr := content.Value.String()
		sessionPage.MustClose()

		Printf("Session响应: %s\n", contentStr[:min(200, len(contentStr))])

		if strings.Contains(contentStr, "error") || strings.Contains(contentStr, "unauthorized") || contentStr == "" {
			Printf("Session无效，等待重试... (%d/5)\n", attempt+1)
			time.Sleep(3 * time.Second)
			continue
		}

		var session struct {
			AccessToken string `json:"accessToken"`
			User        struct {
				ID string `json:"id"`
			} `json:"user"`
		}

		if err := json.Unmarshal([]byte(contentStr), &session); err != nil {
			Printf("解析session失败: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if session.AccessToken != "" {
			Println("成功获取Access Token!")
			return session.AccessToken, session.User.ID
		}

		Printf("Token为空，等待重试... (%d/5)\n", attempt+1)
		time.Sleep(3 * time.Second)
	}

	return "", ""
}

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
				Printf("  已点击包含 '%s' 的元素\n", text)
				return true
			}
		}
	}
	return false
}

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

func (br *BrowserRegister) clickElement(page *rod.Page, selectors []string, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err != nil || el == nil {
			continue
		}

		if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
			Printf("  %s: 已点击 (%s)\n", desc, sel)
			return true
		}
	}
	return false
}

func (br *BrowserRegister) inputText(page *rod.Page, selectors []string, text, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err != nil || el == nil {
			continue
		}

		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			continue
		}
		time.Sleep(100 * time.Millisecond)

		if err := el.SelectAllText(); err != nil {
			el.Eval(`() => this.value = ''`)
		}

		if err := el.Input(text); err != nil {
			continue
		}

		Printf("  %s: 已输入\n", desc)
		return true
	}
	Printf("  %s: 未找到输入框\n", desc)
	return false
}

func (br *BrowserRegister) waitForReactComponents(page *rod.Page) {
	for i := 0; i < 30; i++ {
		inputs, err := page.Elements("input")
		if err == nil && len(inputs) > 0 {
			Printf("  检测到 %d 个input元素\n", len(inputs))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	Println("  警告: 未检测到input元素")
}

func (br *BrowserRegister) inputTextWithWait(page *rod.Page, selectors []string, text, desc string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for _, sel := range selectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err != nil || el == nil {
				continue
			}

			isVisible, _ := el.Eval(`() => {
				const rect = this.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			}`)
			if !isVisible.Value.Bool() {
				continue
			}

			if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
				continue
			}
			time.Sleep(200 * time.Millisecond)

			el.Eval(`() => this.value = ''`)
			time.Sleep(100 * time.Millisecond)

			if err := el.Input(text); err != nil {
				continue
			}

			Printf("  %s: 已输入 (%s)\n", desc, sel)
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}

	Printf("  %s: 超时未找到\n", desc)
	return false
}

func (br *BrowserRegister) clickButtonWithWait(page *rod.Page, selectors []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for _, sel := range selectors {
			el, err := page.Timeout(2 * time.Second).Element(sel)
			if err != nil || el == nil {
				continue
			}

			isClickable, _ := el.Eval(`() => {
				const rect = this.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0 && !this.disabled;
			}`)
			if !isClickable.Value.Bool() {
				continue
			}

			if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
				Printf("  已点击按钮: %s\n", sel)
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return false
}

func (br *BrowserRegister) saveDebugScreenshot(page *rod.Page, name string) {
	screenshot, err := page.Screenshot(false, nil)
	if err != nil {
		return
	}

	filename := fmt.Sprintf("./debug_%s.png", name)
	if err := os.WriteFile(filename, screenshot, 0644); err == nil {
		Printf("  [调试] 截图已保存: %s\n", filename)
	}
}

func (br *BrowserRegister) debugPageElements(page *rod.Page, tag string) {
	Printf("\n[调试] 页面 %s 元素:\n", tag)

	elements, err := page.Elements(tag)
	if err != nil {
		Printf("  获取元素失败: %v\n", err)
		return
	}

	Printf("  找到 %d 个 %s 元素\n", len(elements), tag)

	for i, el := range elements {
		if i >= 10 {
			Println("  ... (更多元素已省略)")
			break
		}

		name, _ := el.Eval(`() => this.name || this.id || ''`)
		typ, _ := el.Eval(`() => this.type || ''`)
		placeholder, _ := el.Eval(`() => this.placeholder || ''`)

		Printf("  [%d] name=%s type=%s placeholder=%s\n",
			i,
			name.Value.String(),
			typ.Value.String(),
			placeholder.Value.String())
	}
}
