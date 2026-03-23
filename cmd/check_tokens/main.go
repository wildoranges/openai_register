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

	"github.com/google/uuid"
)

const (
	tokenURL       = "https://auth.openai.com/oauth/token"
	codexModelsURL = "https://chatgpt.com/backend-api/codex/models"
	clientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexClientVer = "0.101.0"
	codexUserAgent = "codex_cli_rs/0.101.0"
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
	File       string
	Email      string
	Status     string
	Message    string
	CanRefresh bool
	CodexCode  int
	Cred       Credential
}

type ValidateResult struct {
	Valid   bool
	Message string
	Code    int
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

	// 分组：不能刷新的 vs Codex 验证失败的
	cannotRefresh := append(append(reused, expired...), errors[:0]...)
	var codexFailed map[int][]CheckResult
	codexFailed = make(map[int][]CheckResult)

	for _, r := range errors {
		if r.CanRefresh {
			codexFailed[r.CodexCode] = append(codexFailed[r.CodexCode], r)
		} else {
			cannotRefresh = append(cannotRefresh, r)
		}
	}

	// 按 Codex HTTP 状态码排序
	codexCodes := make([]int, 0, len(codexFailed))
	for code := range codexFailed {
		codexCodes = append(codexCodes, code)
	}
	for i := 0; i < len(codexCodes); i++ {
		for j := i + 1; j < len(codexCodes); j++ {
			if codexCodes[i] > codexCodes[j] {
				codexCodes[i], codexCodes[j] = codexCodes[j], codexCodes[i]
			}
		}
	}

	fmt.Println("\n====================================")
	fmt.Println("  按 HTTP 状态码统计")
	fmt.Println("====================================")
	if len(cannotRefresh) > 0 {
		fmt.Printf("  不能刷新的: %d 个\n", len(cannotRefresh))
		for _, r := range cannotRefresh {
			fmt.Printf("         - %s (%s)\n", filepath.Base(r.File), r.Message)
		}
	}
	for _, code := range codexCodes {
		results := codexFailed[code]
		fmt.Printf("  HTTP %d: %d 个\n", code, len(results))
		for _, r := range results {
			fmt.Printf("         - %s\n", filepath.Base(r.File))
		}
	}
	if len(valid) > 0 {
		fmt.Printf("  有效凭证: %d 个\n", len(valid))
	}
	fmt.Println("====================================")

	codexFailedCount := 0
	for _, results := range codexFailed {
		codexFailedCount += len(results)
	}
	invalidCount := len(cannotRefresh) + codexFailedCount
	if invalidCount == 0 {
		fmt.Println("\n✨ 所有凭证均有效!")
		return
	}

	fmt.Printf("\n发现 %d 个无效凭证 (不能刷新: %d + Codex验证失败: %d)\n",
		invalidCount, len(cannotRefresh), codexFailedCount)

	fmt.Printf("\n是否删除这些无效凭证? (不能刷新: %d + Codex验证失败: %d. 错误凭证共 %d 个. 凭证总数共 %d 个) (y/N): ", len(cannotRefresh), codexFailedCount, invalidCount, len(files))

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "y" || input == "yes" {
		deleted := 0
		for _, r := range cannotRefresh {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s\n", filepath.Base(r.File))
				deleted++
			}
		}
		for _, results := range codexFailed {
			for _, r := range results {
				if err := os.Remove(r.File); err != nil {
					fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
				} else {
					fmt.Printf("  ✓ 已删除: %s\n", filepath.Base(r.File))
					deleted++
				}
			}
		}
		fmt.Printf("\n🗑️  已删除 %d 个无效凭证\n", deleted)
		fmt.Printf("剩余有效凭证: %d 个\n", len(valid))
		return
	}

	// 按 HTTP code 删除功能
	if len(codexCodes) == 0 {
		fmt.Println("\n所有无效凭证均为不能刷新的，请使用上面的删除选项。")
		return
	}

	fmt.Println("\n====================================")
	fmt.Println("  按 HTTP 状态码删除")
	fmt.Println("====================================")
	if len(cannotRefresh) > 0 {
		fmt.Printf("  refresh: 删除所有不能刷新的凭证 (%d 个)\n", len(cannotRefresh))
	}
	for _, code := range codexCodes {
		fmt.Printf("  %d: 删除 HTTP %d 的凭证 (%d 个)\n", code, code, len(codexFailed[code]))
	}
	fmt.Println("  all: 删除所有无效凭证")
	fmt.Println("  q: 退出不删除")
	fmt.Print("\n请输入要删除的选项 (多个用逗号分隔, 如 401,403 或 refresh,401): ")

	input2, _ := reader.ReadString('\n')
	input2 = strings.TrimSpace(strings.ToLower(input2))

	if input2 == "q" || input2 == "" {
		fmt.Println("\n取消删除")
		return
	}

	var toDeleteCannotRefresh bool
	var codesToDelete []int

	if input2 == "all" {
		toDeleteCannotRefresh = true
		codesToDelete = codexCodes
	} else {
		opts := strings.Split(input2, ",")
		for _, opt := range opts {
			opt = strings.TrimSpace(opt)
			if opt == "refresh" {
				toDeleteCannotRefresh = true
			} else {
				var code int
				if _, err := fmt.Sscanf(opt, "%d", &code); err == nil {
					for _, existingCode := range codexCodes {
						if existingCode == code {
							codesToDelete = append(codesToDelete, code)
							break
						}
					}
				}
			}
		}
	}

	if !toDeleteCannotRefresh && len(codesToDelete) == 0 {
		fmt.Println("\n未选择有效的删除选项")
		return
	}

	// 去重
	uniqueCodes := make(map[int]bool)
	for _, code := range codesToDelete {
		uniqueCodes[code] = true
	}

	totalToDelete := 0
	if toDeleteCannotRefresh {
		totalToDelete += len(cannotRefresh)
	}
	for code := range uniqueCodes {
		totalToDelete += len(codexFailed[code])
	}

	var deleteDesc []string
	if toDeleteCannotRefresh {
		deleteDesc = append(deleteDesc, fmt.Sprintf("不能刷新: %d", len(cannotRefresh)))
	}
	for _, code := range codexCodes {
		if uniqueCodes[code] {
			deleteDesc = append(deleteDesc, fmt.Sprintf("HTTP %d: %d", code, len(codexFailed[code])))
		}
	}

	fmt.Printf("\n将删除 %d 个凭证 (%s)\n确认删除? (y/N): ", totalToDelete, strings.Join(deleteDesc, ", "))

	confirm, _ := reader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))

	if confirm != "y" && confirm != "yes" {
		fmt.Println("\n取消删除")
		return
	}

	deleted := 0
	if toDeleteCannotRefresh {
		for _, r := range cannotRefresh {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s (不能刷新)\n", filepath.Base(r.File))
				deleted++
			}
		}
	}
	for code := range uniqueCodes {
		for _, r := range codexFailed[code] {
			if err := os.Remove(r.File); err != nil {
				fmt.Printf("  删除失败 %s: %v\n", filepath.Base(r.File), err)
			} else {
				fmt.Printf("  ✓ 已删除: %s (HTTP %d)\n", filepath.Base(r.File), code)
				deleted++
			}
		}
	}
	fmt.Printf("\n🗑️  已删除 %d 个凭证\n", deleted)
}

func checkToken(file string) CheckResult {
	data, err := os.ReadFile(file)
	if err != nil {
		return CheckResult{File: file, Status: "error", Message: fmt.Sprintf("读取文件失败: %v", err), CanRefresh: false}
	}

	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return CheckResult{File: file, Status: "error", Message: fmt.Sprintf("解析JSON失败: %v", err), CanRefresh: false}
	}

	email, _ := cred["email"].(string)
	refreshToken, _ := cred["refresh_token"].(string)

	if refreshToken == "" {
		return CheckResult{File: file, Email: email, Status: "error", Message: "无refresh_token", CanRefresh: false}
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
		return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("创建请求失败: %v", err), CanRefresh: false}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("请求失败: %v", err), CanRefresh: false}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	refreshCode := resp.StatusCode

	if refreshCode == 200 {
		var tokenResp TokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("解析token响应失败: %v", err), CanRefresh: false}
		}

		cred["access_token"] = tokenResp.AccessToken
		cred["refresh_token"] = tokenResp.RefreshToken
		cred["id_token"] = tokenResp.IDToken
		cred["last_refresh"] = time.Now().Format(time.RFC3339)
		cred["expired"] = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)

		updatedData, err := json.Marshal(cred)
		if err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("序列化凭证失败: %v", err), CanRefresh: false}
		}

		if err := os.WriteFile(file, updatedData, 0644); err != nil {
			return CheckResult{File: file, Email: email, Status: "error", Message: fmt.Sprintf("写入文件失败: %v", err), CanRefresh: false}
		}

		validateResult := validateCodexAccess(ctx, tokenResp.AccessToken, cred)
		if !validateResult.Valid {
			return CheckResult{
				File:       file,
				Email:      email,
				Status:     "error",
				Message:    fmt.Sprintf("Codex验证失败: %s", validateResult.Message),
				CanRefresh: true,
				CodexCode:  validateResult.Code,
				Cred:       cred,
			}
		}

		return CheckResult{File: file, Email: email, Status: "valid", Message: "刷新成功并验证可用", CanRefresh: true, CodexCode: 200, Cred: cred}
	}

	errStr := strings.ToLower(string(body))

	if strings.Contains(errStr, "refresh_token_reused") {
		return CheckResult{File: file, Email: email, Status: "reused", Message: "refresh_token已被重用", CanRefresh: false}
	}

	if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "invalid_token") {
		return CheckResult{File: file, Email: email, Status: "expired", Message: "token无效或已过期", CanRefresh: false}
	}

	return CheckResult{
		File:       file,
		Email:      email,
		Status:     "error",
		Message:    fmt.Sprintf("刷新失败 HTTP %d: %s", refreshCode, truncateString(string(body), 100)),
		CanRefresh: false,
	}
}

func validateCodexAccess(ctx context.Context, accessToken string, cred Credential) ValidateResult {
	apiURL := fmt.Sprintf("%s?client_version=%s", codexModelsURL, codexClientVer)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return ValidateResult{Valid: false, Message: fmt.Sprintf("创建验证请求失败: %v", err), Code: 0}
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Version", codexClientVer)
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Session-Id", uuid.NewString())
	req.Header.Set("Originator", "codex_cli_rs")

	if accountID, ok := cred["account_id"].(string); ok && accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return ValidateResult{Valid: false, Message: fmt.Sprintf("验证请求失败: %v", err), Code: 0}
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code == 200 {
		return ValidateResult{Valid: true, Message: "Codex可用", Code: 200}
	}

	body, _ := io.ReadAll(resp.Body)
	errStr := strings.ToLower(string(body))

	if code == 401 {
		return ValidateResult{Valid: false, Message: fmt.Sprintf("认证失败: %s", truncateString(errStr, 50)), Code: 401}
	}

	if code == 403 {
		if strings.Contains(errStr, "unsupported_country") || strings.Contains(errStr, "region") {
			return ValidateResult{Valid: false, Message: "地区不支持", Code: 403}
		}
		return ValidateResult{Valid: false, Message: fmt.Sprintf("访问被拒绝: %s", truncateString(errStr, 50)), Code: 403}
	}

	return ValidateResult{Valid: false, Message: fmt.Sprintf("验证失败: %s", truncateString(errStr, 50)), Code: code}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
