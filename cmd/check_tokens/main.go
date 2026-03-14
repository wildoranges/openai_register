package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	tokenURL = "https://auth.openai.com/oauth/token"
	clientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type Credential map[string]any

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type CheckResult struct {
	File    string
	Email   string
	Status  string
	Message string
	Cred    Credential
}

func main() {
	authDir := os.Getenv("AUTH_DIR")
	if authDir == "" {
		home, _ := os.UserHomeDir()
		authDir = filepath.Join(home, ".cli-proxy-api")
	}

	if len(os.Args) > 1 {
		authDir = os.Args[1]
	}

	fmt.Println("====================================")
	fmt.Println("  Refresh Token 检测工具")
	fmt.Println("====================================")
	fmt.Printf("凭证目录: %s\n\n", authDir)

	files, err := filepath.Glob(filepath.Join(authDir, "codex-*.json"))
	if err != nil {
		fmt.Printf("扫描目录失败: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("未找到凭证文件 (codex-*.json)")
		os.Exit(0)
	}

	fmt.Printf("找到 %d 个凭证文件，开始检测...\n\n", len(files))

	results := make(chan CheckResult, len(files))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5)

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results <- checkToken(f)
			fmt.Print(".")
		}(file)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var valid, reused, expired, errors []CheckResult
	for r := range results {
		switch r.Status {
		case "valid":
			valid = append(valid, r)
		case "reused":
			reused = append(reused, r)
		case "expired":
			expired = append(expired, r)
		default:
			errors = append(errors, r)
		}
	}

	fmt.Println("\n\n====================================")
	fmt.Println("  检测结果")
	fmt.Println("====================================")

	fmt.Printf("\n✅ 有效凭证: %d\n", len(valid))
	for _, r := range valid {
		fmt.Printf("   - %s (已更新token)\n", r.Email)
	}

	fmt.Printf("\n❌ 已重用 (refresh_token_reused): %d\n", len(reused))
	for _, r := range reused {
		fmt.Printf("   - %s (%s)\n", r.Email, filepath.Base(r.File))
	}

	fmt.Printf("\n⏰ 已过期: %d\n", len(expired))
	for _, r := range expired {
		fmt.Printf("   - %s (%s)\n", r.Email, r.Message)
	}

	fmt.Printf("\n⚠️ 其他错误: %d\n", len(errors))
	for _, r := range errors {
		fmt.Printf("   - %s: %s\n", r.Email, r.Message)
	}

	fmt.Println("\n====================================")
	fmt.Printf("  总计: %d 个凭证\n", len(files))
	fmt.Printf("  有效: %d | 已重用: %d | 已过期: %d | 错误: %d\n",
		len(valid), len(reused), len(expired), len(errors))
	fmt.Println("====================================")

	invalidCount := len(reused) + len(expired) + len(errors)
	if invalidCount == 0 {
		fmt.Println("\n✨ 所有凭证均有效!")
		return
	}

	fmt.Printf("\n发现 %d 个无效凭证 (已重用: %d + 已过期: %d + 其他错误: %d)\n",
		invalidCount, len(reused), len(expired), len(errors))
	fmt.Println("\n无效凭证列表:")
	for _, r := range reused {
		fmt.Printf("  [reused] %s\n", filepath.Base(r.File))
	}
	for _, r := range expired {
		fmt.Printf("  [expired] %s\n", filepath.Base(r.File))
	}
	for _, r := range errors {
		fmt.Printf("  [error] %s - %s\n", filepath.Base(r.File), r.Message)
	}

	fmt.Print("\n是否删除这些无效凭证? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "y" || input == "yes" {
		deleted := 0
		for _, r := range reused {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s\n", filepath.Base(r.File))
				deleted++
			}
		}
		for _, r := range expired {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s\n", filepath.Base(r.File))
				deleted++
			}
		}
		for _, r := range errors {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s\n", filepath.Base(r.File))
				deleted++
			}
		}
		fmt.Printf("\n🗑️  已删除 %d 个无效凭证\n", deleted)
		fmt.Printf("剩余有效凭证: %d 个\n", len(valid))
	} else {
		fmt.Println("\n取消删除")
	}
}

func checkToken(file string) CheckResult {
	data, err := os.ReadFile(file)
	if err != nil {
		return CheckResult{File: file, Status: "error", Message: fmt.Sprintf("读取文件失败: %v", err)}
	}

	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return CheckResult{File: file, Status: "error", Message: fmt.Sprintf("解析JSON失败: %v", err)}
	}

	email, _ := cred["email"].(string)
	refreshToken, _ := cred["refresh_token"].(string)

	if refreshToken == "" {
		return CheckResult{File: file, Email: email, Status: "error", Message: "无refresh_token"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	form := url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("创建请求失败: %v", err)}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("请求失败: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		var tokenResp TokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("解析token响应失败: %v", err)}
		}

		cred["access_token"] = tokenResp.AccessToken
		cred["refresh_token"] = tokenResp.RefreshToken
		cred["id_token"] = tokenResp.IDToken
		cred["last_refresh"] = time.Now().Format(time.RFC3339)
		cred["expired"] = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)

		updatedData, err := json.Marshal(cred)
		if err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("序列化凭证失败: %v", err)}
		}

		if err := os.WriteFile(file, updatedData, 0644); err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("写入文件失败: %v", err)}
		}

		return CheckResult{File: file, Email: email, Status: "valid", Message: "刷新成功", Cred: cred}
	}

	errStr := strings.ToLower(string(body))

	if strings.Contains(errStr, "refresh_token_reused") {
		return CheckResult{File: file, Email: email, Status: "reused", Message: "refresh_token已被重用"}
	}

	if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "invalid_token") {
		return CheckResult{File: file, Email: email, Status: "expired", Message: "token无效或已过期"}
	}

	return CheckResult{
		File:    file,
		Email:   email,
		Status:  "error",
		Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
	}
}
