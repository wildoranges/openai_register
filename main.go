package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

func shouldMarkProxyFailed(err error) bool {
	if err == nil {
		return false
	}

	if err == ErrUnsupportedEmail || err == ErrUserAlreadyExists || err == ErrLoginFailedAliasUsed {
		return false
	}

	message := strings.ToLower(err.Error())
	networkSignals := []string{
		"proxy",
		"代理",
		"timeout",
		"deadline exceeded",
		"connection reset",
		"connection refused",
		"no such host",
		"network is unreachable",
		"tls handshake timeout",
		"i/o timeout",
		"请求失败",
		"连接",
		"oauth 回调失败",
		"token 兑换失败",
		"当前ip/地区不支持openai注册",
	}

	for _, signal := range networkSignals {
		if strings.Contains(message, signal) {
			return true
		}
	}

	return false
}

func init() {
	_ = fmt.Sprintf
}

func main() {
	fmt.Println("====================================")
	fmt.Println("   OpenAI 账号注册工具")
	fmt.Println("   用于 CodeX 认证")
	fmt.Println("====================================")
	fmt.Println()

	configPath := "./config.json"
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == "--config" || os.Args[i] == "-config" {
			if i+1 < len(os.Args) {
				configPath = os.Args[i+1]
			}
		} else if len(os.Args[i]) > 8 && os.Args[i][:8] == "--config=" {
			configPath = os.Args[i][8:]
		}
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		Printf("加载配置失败: %v，使用默认配置\n", err)
		config = DefaultConfig()
	}

	simMode := false
	webmailMode := false
	count := config.Count
	skipNext := false
	specifiedEmail := ""
	specifiedPassword := ""
	specifiedOTP := ""
	for i := 0; i < len(os.Args); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		arg := os.Args[i]
		if arg == "--sim" || arg == "-sim" {
			simMode = true
		} else if arg == "--webmail" || arg == "-webmail" {
			webmailMode = true
		} else if arg == "--head" || arg == "-head" {
			config.Headless = false
		} else if arg == "--debug" || arg == "-debug" {
			config.Debug = true
		} else if arg == "--config" || arg == "-config" {
			skipNext = true
			continue
		} else if len(arg) > 8 && arg[:8] == "--config=" {
			continue
		} else if arg == "--email" || arg == "-email" {
			if i+1 < len(os.Args) {
				specifiedEmail = os.Args[i+1]
				i++
			}
		} else if len(arg) > 7 && arg[:7] == "--email=" {
			specifiedEmail = arg[7:]
		} else if arg == "--password" || arg == "-password" {
			if i+1 < len(os.Args) {
				specifiedPassword = os.Args[i+1]
				i++
			}
		} else if len(arg) > 10 && arg[:10] == "--password=" {
			specifiedPassword = arg[10:]
		} else if arg == "--otp" || arg == "-otp" {
			if i+1 < len(os.Args) {
				specifiedOTP = os.Args[i+1]
				i++
			}
		} else if len(arg) > 5 && arg[:5] == "--otp=" {
			specifiedOTP = arg[5:]
		} else {
			fmt.Sscanf(arg, "%d", &count)
		}
	}

	if specifiedEmail != "" {
		count = 1
	}

	Printf("无头模式: %v\n", config.Headless)
	Printf("输出目录: %s\n", config.OutputDir)
	if config.ConvertDir != "" {
		Printf("转换目录: %s\n", config.ConvertDir)
	}
	if webmailMode {
		Println("邮箱模式: WebMail (浏览器获取临时邮箱)")
	}
	if specifiedEmail != "" {
		Printf("指定邮箱: %s\n", specifiedEmail)
		Printf("注册数量: 1 (指定邮箱模式)\n\n")
	} else {
		Printf("注册数量: %d\n\n", count)
	}

	var proxyPool *ProxyPool
	if len(config.Proxies) > 0 {
		if config.Clash != nil {
			Println("检测到 clash 配置，但已配置静态 proxies，忽略 clash 代理池")
		}
		proxyPool = NewProxyPool(config.Proxies)
		proxyPool.TestAll()
		if proxyPool.GetAvailableCount() == 0 {
			Println("\n❌ 没有可用的代理，退出")
			return
		}
	} else if config.Proxy != "" {
		if config.Clash != nil {
			Println("检测到 clash 配置，但已配置静态 proxy，忽略 clash 代理池")
		}
		Printf("代理: %s\n", maskProxyURL(config.Proxy))
		Println("\n🔍 测试代理...")
		result := TestSingleProxy(config.Proxy)
		if !result.Available {
			Printf("\n❌ 代理测试失败: %s\n", result.Error)
			Println("代理测试失败，后续注册可能会遇到问题")
		}
		if result.IP != "" {
			location := result.IP
			if result.Country != "" {
				location += " (" + result.Country
				if result.City != "" {
					location += ", " + result.City
				}
				location += ")"
			}
			Printf("✅ 代理可用 → %s\n", location)
		} else {
			Println("✅ 代理可用")
		}
	} else if config.Clash != nil {
		var clashErr error
		proxyPool, clashErr = NewClashProxyPool(config.Clash)
		if clashErr != nil {
			Printf("\n❌ Clash 代理池初始化失败: %v\n", clashErr)
			return
		}
		proxyPool.TestAll()
		if proxyPool.GetAvailableCount() == 0 {
			Println("\n❌ Clash 代理池没有可用节点，退出")
			return
		}
	}

	if simMode {
		Println("[模拟模式] 生成测试凭证...")
		successCount := 0
		for i := 0; i < count; i++ {
			Printf("\n========== 生成第 %d/%d 个凭证 ==========\n", i+1, count)

			ts := time.Now().Unix()
			randStr := randomString(16)
			credentials := &AccountCredentials{
				Email:        fmt.Sprintf("test%d@openai-register.test", i+1),
				Password:     GeneratePassword(),
				AccessToken:  fmt.Sprintf("eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLWR1bW15IiwiZW1haWwiOiJ0ZXN0JWRAb3BlbmFpLXJlZ2lzdGVyLnRlc3QiLCJpYXQiOjE3MDk1Njc2MDAsImV4cCI6MTc0MDgxMTM5OH0.sim_sig_%d_%s", ts, randStr),
				RefreshToken: fmt.Sprintf("v1|test_refresh_%d_%s", ts, randStr),
				UserID:       fmt.Sprintf("user-test-%d-%d", i+1, ts),
				ExpiresIn:    86400,
				CreatedAt:    time.Now(),
			}

			if err := SaveCredentialsWithDir(credentials, config.OutputDir); err != nil {
				Printf("保存凭证失败: %v\n", err)
				continue
			}

			Println("\n=== 凭证生成成功 ===")
			Printf("邮箱: %s\n", credentials.Email)
			Printf("密码: %s\n", credentials.Password)
			if len(credentials.AccessToken) > 50 {
				Printf("Access Token:  %s...\n", credentials.AccessToken[:50])
			} else {
				Printf("Access Token:  %s\n", credentials.AccessToken)
			}
			if credentials.RefreshToken != "" {
				maxLen := minInt(50, len(credentials.RefreshToken))
				Printf("Refresh Token: %s...\n", credentials.RefreshToken[:maxLen])
			}
			successCount++
		}
		Printf("\n模拟模式成功生成账号数: %d/%d\n", successCount, count)
	} else {
		Printf("将注册 %d 个账号\n\n", count)

		successCount := 0
		for i := 0; i < count; i++ {
			Printf("\n========== 注册第 %d/%d 个账号 ==========\n", i+1, count)

			currentProxy := config.Proxy
			var currentAssignment *ProxyAssignment
			if proxyPool != nil {
				currentAssignment = proxyPool.GetNextAssignment()
				if currentAssignment == nil {
					Println("当前没有可用代理节点，停止注册")
					break
				}
				currentProxy = currentAssignment.ProxyURL
				Printf("使用代理: %s\n", currentAssignment.Label())
			}

			var httpClient *HTTPClient
			var webMailClient *WebMailClient
			var brOAuth *BrowserRegisterOAuth

			if webmailMode {
				// Use WebMail for both getting temp email AND checking verification emails
				brOAuth = NewBrowserRegisterOAuthWithWebMail(config, currentProxy, config.Headless)
				webMailClient = brOAuth.webMailClient
				if err := webMailClient.Start(); err != nil {
					Printf("启动 WebMail 失败: %v\n", err)
					if i < count-1 {
						waitTime := 10 + rand.Intn(10)
						Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
						time.Sleep(time.Duration(waitTime) * time.Second)
					}
					continue
				}
				defer webMailClient.Close()
				// Also initialize httpClient as fallback for email checking
				httpClient = NewHTTPClientWithProxy(currentProxy)
				brOAuth.httpClient = httpClient
			} else {
				httpClient = NewHTTPClientWithProxy(currentProxy)
				brOAuth = NewBrowserRegisterOAuth(config, currentProxy)
			}

			var email, password string
			var err error

			if specifiedEmail != "" {
				email = specifiedEmail
				if specifiedPassword != "" {
					password = specifiedPassword
				} else {
					password = GeneratePassword()
				}
				Printf("使用指定邮箱: %s\n", email)
				Printf("密码: %s\n", password)
			} else {
				if webmailMode {
					email, err = webMailClient.GetTempEmail()
				} else {
					email, err = httpClient.GetTempEmail()
				}
				if err != nil {
					Printf("获取临时邮箱失败: %v\n", err)
					if i < count-1 {
						waitTime := 10 + rand.Intn(10)
						Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
						time.Sleep(time.Duration(waitTime) * time.Second)
					}
					continue
				}
				Printf("临时邮箱: %s\n", email)

				password = GeneratePassword()
				Printf("生成密码: %s\n", password)
			}

			var credentials *AccountCredentials
			var regErr error
			maxAttempts := 5

		RETRY_LOOP:
			for attempt := range maxAttempts {
				if attempt > 0 {
					Printf("\n=== 重试第 %d 次 (共 %d 次) ===\n", attempt, maxAttempts-1)
				}

				credentials, regErr = brOAuth.RegisterWithOAuth(email, password, specifiedOTP)

				if regErr == nil {
					break RETRY_LOOP
				}

				if regErr == ErrUnsupportedEmail {
					Printf("❌ 邮箱不被 OpenAI 支持: %s，跳过该账号\n", email)
					break RETRY_LOOP
				}

				if regErr == ErrUserAlreadyExists {
					Printf("⚠️ 账号已存在 (注册阶段)，尝试登录获取凭证...\n")
					credentials, regErr = brOAuth.LoginWithOAuth(email, password, specifiedOTP)
					if regErr == nil {
						break RETRY_LOOP
					}
					Printf("登录失败: %v\n", regErr)
					break RETRY_LOOP
				}

				Printf("注册失败: %v\n", regErr)
				break RETRY_LOOP
			}

			if regErr != nil || credentials == nil {
				if proxyPool != nil && shouldMarkProxyFailed(regErr) {
					if currentAssignment != nil {
						proxyPool.MarkFailedAssignment(currentAssignment)
					} else {
						proxyPool.MarkFailed(currentProxy)
					}
				}
				if i < count-1 {
					waitTime := 10 + rand.Intn(10)
					Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
					time.Sleep(time.Duration(waitTime) * time.Second)
				}
				continue
			}
			if err := SaveCredentialsWithDir(credentials, config.OutputDir); err != nil {
				Printf("保存凭证失败: %v\n", err)
			}

			if config.ConvertDir != "" {
				if err := ConvertCredentialToCLIProxy(credentials, config.ConvertDir); err != nil {
					Printf("转换 CLIProxyAPI 格式失败: %v\n", err)
				}
			}

			Println("\n=== 注册成功 ===")
			Printf("邮箱: %s\n", credentials.Email)
			if len(credentials.AccessToken) > 50 {
				Printf("Access Token:  %s...\n", credentials.AccessToken[:50])
			} else {
				Printf("Access Token:  %s\n", credentials.AccessToken)
			}
			if credentials.RefreshToken != "" && len(credentials.RefreshToken) > 50 {
				Printf("Refresh Token: %s...\n", credentials.RefreshToken[:50])
			}
			Printf("凭证已保存到 %s/openai_credentials.json\n", config.OutputDir)
			successCount++

			if i < count-1 {
				waitTime := 10 + rand.Intn(10)
				Printf("\n等待 %d 秒后继续注册下一个账号...\n", waitTime)
				time.Sleep(time.Duration(waitTime) * time.Second)
			}
		}
		Printf("\n成功注册账号数: %d/%d\n", successCount, count)
	}

	fmt.Println("\n====================================")
	fmt.Println("   所有账号处理完成")
	fmt.Println("====================================")
}
