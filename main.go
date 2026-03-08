package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"
)

func main() {
	fmt.Println("====================================")
	fmt.Println("   OpenAI 账号注册工具")
	fmt.Println("   用于 CodeX 认证")
	fmt.Println("====================================")
	fmt.Println()

	configPath := "./config.json"
	config, err := LoadConfig(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v，使用默认配置\n", err)
		config = DefaultConfig()
	}

	simMode := false
	oauthMode := false
	count := config.Count
	for _, arg := range os.Args[1:] {
		if arg == "--sim" || arg == "-sim" {
			simMode = true
		} else if arg == "--oauth" || arg == "-oauth" {
			oauthMode = true
		} else if arg == "--head" || arg == "-head" {
			config.Headless = false
		} else if arg == "--debug" || arg == "-debug" {
			config.Debug = true
		} else {
			fmt.Sscanf(arg, "%d", &count)
		}
	}

	if config.Proxy != "" {
		fmt.Printf("代理: %s\n", config.Proxy)
	}
	fmt.Printf("无头模式: %v\n", config.Headless)
	fmt.Printf("OAuth模式: %v\n", oauthMode)
	fmt.Printf("输出目录: %s\n", config.OutputDir)
	fmt.Printf("注册数量: %d\n\n", count)

	if simMode {
		fmt.Println("[模拟模式] 生成测试凭证...")
		successCount := 0
		for i := 0; i < count; i++ {
			fmt.Printf("\n========== 生成第 %d/%d 个凭证 ==========\n", i+1, count)

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
				fmt.Printf("保存凭证失败: %v\n", err)
				continue
			}

			fmt.Println("\n=== 凭证生成成功 ===")
			fmt.Printf("邮箱: %s\n", credentials.Email)
			fmt.Printf("密码: %s\n", credentials.Password)
			if len(credentials.AccessToken) > 50 {
				fmt.Printf("Access Token:  %s...\n", credentials.AccessToken[:50])
			} else {
				fmt.Printf("Access Token:  %s\n", credentials.AccessToken)
			}
			if credentials.RefreshToken != "" {
				maxLen := minInt(50, len(credentials.RefreshToken))
				fmt.Printf("Refresh Token: %s...\n", credentials.RefreshToken[:maxLen])
			}
			successCount++
		}
		fmt.Printf("\n模拟模式成功生成账号数: %d/%d\n", successCount, count)
	} else {
		fmt.Printf("将注册 %d 个账号\n\n", count)

		httpClient := NewHTTPClientWithProxy(config.Proxy)
		br := NewBrowserRegister(config)
		brOAuth := NewBrowserRegisterOAuth(config)
		successCount := 0
		for i := 0; i < count; i++ {
			fmt.Printf("\n========== 注册第 %d/%d 个账号 ==========\n", i+1, count)

			email, err := httpClient.GetTempEmail()
			if err != nil {
				fmt.Printf("获取临时邮箱失败: %v\n", err)
				continue
			}
			fmt.Printf("临时邮箱: %s\n", email)

			password := GeneratePassword()
			fmt.Printf("生成密码: %s\n", password)

			var credentials *AccountCredentials
			var regErr error

			if oauthMode {
				// 使用 OAuth PKCE 模式 (获取 refresh_token)
				credentials, regErr = brOAuth.RegisterWithOAuth(email, password)
			} else {
				// 使用传统模式 (仅 access_token)
				credentials, regErr = br.Register(email, password)
			}

if regErr != nil {
				if regErr == ErrUnsupportedEmail {
					fmt.Printf("❌ 邮箱不被 OpenAI 支持: %s，跳过该账号\n", email)
				} else {
					fmt.Printf("注册失败: %v\n", regErr)
				}
				continue
			}
			if err := SaveCredentialsWithDir(credentials, config.OutputDir); err != nil {
				fmt.Printf("保存凭证失败: %v\n", err)
			}

			fmt.Println("\n=== 注册成功 ===")
			fmt.Printf("邮箱: %s\n", credentials.Email)
			if len(credentials.AccessToken) > 50 {
				fmt.Printf("Access Token:  %s...\n", credentials.AccessToken[:50])
			} else {
				fmt.Printf("Access Token:  %s\n", credentials.AccessToken)
			}
			if credentials.RefreshToken != "" && len(credentials.RefreshToken) > 50 {
				fmt.Printf("Refresh Token: %s...\n", credentials.RefreshToken[:50])
			}
			fmt.Printf("凭证已保存到 %s/openai_credentials.json\n", config.OutputDir)
			successCount++

if i < count-1 {
				waitTime := 10 + rand.Intn(10)
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
