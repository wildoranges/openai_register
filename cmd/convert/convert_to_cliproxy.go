package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credential 注册机输出的凭证格式
type Credential struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	SessionID    string `json:"session_id"`
	UserID       string `json:"user_id"`
	CreatedAt    string `json:"created_at"`
}

// CLIProxyCredential CLIProxyAPI 要求的凭证格式
type CLIProxyCredential struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	LastRefresh  string `json:"last_refresh"`
	Email        string `json:"email"`
	Type         string `json:"type"`
	Expired      string `json:"expired"`
}

// decodeJWTPayload 解码 JWT payload（不验证签名）
func decodeJWTPayload(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload := parts[1]
	if l := len(payload) % 4; l > 0 {
		payload += strings.Repeat("=", 4-l)
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("base64 decode failed: %v", err)
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(decoded, &result); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %v", err)
	}

	return result, nil
}

// convertCredentials 转换凭证格式
func convertCredentials(inputFile, outputDir string) (int, error) {
	data, err := os.ReadFile(inputFile)
	if err != nil {
		return 0, fmt.Errorf("read file failed: %v", err)
	}

	var creds []Credential
	if err := json.Unmarshal(data, &creds); err != nil {
		return 0, fmt.Errorf("parse JSON failed: %v", err)
	}

	if len(creds) == 0 {
		return 0, fmt.Errorf("no credentials found")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return 0, fmt.Errorf("create directory failed: %v", err)
	}

	fmt.Printf("输入文件: %s\n", inputFile)
	fmt.Printf("输出目录: %s\n", outputDir)
	fmt.Printf("凭证数量: %d\n", len(creds))
	fmt.Println(strings.Repeat("-", 50))

	converted := 0
	for i, cred := range creds {
		email := cred.Email
		if email == "" {
			email = fmt.Sprintf("unknown_%d", i+1)
		}

		if cred.AccessToken == "" {
			fmt.Printf("[%d] 跳过 %s: 缺少 access_token\n", i+1, email)
			continue
		}

		payload, err := decodeJWTPayload(cred.AccessToken)
		if err != nil {
			fmt.Printf("[%d] 警告 %s: JWT 解码失败: %v\n", i+1, email, err)
		}

		cliproxyCred := CLIProxyCredential{
			IDToken:      cred.IDToken,
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			AccountID:    "",
			LastRefresh:  time.Now().Format(time.RFC3339),
			Email:        email,
			Type:         "codex",
			Expired:      "",
		}

		var ttlInfo string
		var refreshInfo string
		if cred.RefreshToken != "" {
			refreshInfo = "✓有refresh"
		} else {
			refreshInfo = "✗无refresh"
		}
		if payload != nil {
			if auth, ok := payload["https://api.openai.com/auth"].(map[string]interface{}); ok {
				if accountID, ok := auth["chatgpt_account_id"].(string); ok {
					cliproxyCred.AccountID = accountID
				}
			}
			if exp, ok := payload["exp"].(float64); ok && exp > 0 {
				cliproxyCred.Expired = time.Unix(int64(exp), 0).Format(time.RFC3339)
				ttlDays := float64(int64(exp)-time.Now().Unix()) / 86400
				ttlInfo = fmt.Sprintf("(剩余%.1f天)", ttlDays)
			}
		}

		filename := fmt.Sprintf("codex-%s.json", email)
		filePath := filepath.Join(outputDir, filename)

		jsonData, err := json.MarshalIndent(cliproxyCred, "", "  ")
		if err != nil {
			fmt.Printf("[%d] 保存失败: %s - %v\n", i+1, email, err)
			continue
		}

		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			fmt.Printf("[%d] 保存失败: %s - %v\n", i+1, email, err)
			continue
		}

		fmt.Printf("[%d] 转换成功: %s %s %s\n", i+1, email, refreshInfo, ttlInfo)
		converted++
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("总计转换: %d/%d 个凭证\n", converted, len(creds))
	fmt.Printf("输出目录: %s\n", outputDir)

	return converted, nil
}

func main() {
	scriptDir, _ := os.Getwd()
	defaultInput := filepath.Join(scriptDir, "creds", "openai_credentials.json")
	home, _ := os.UserHomeDir()
	defaultOutput := filepath.Join(home, ".cli-proxy-api")

	inputFile := defaultInput
	outputDir := defaultOutput

	if len(os.Args) >= 2 {
		inputFile = os.Args[1]
	}
	if len(os.Args) >= 3 {
		outputDir = os.Args[2]
	}

	convertCredentials(inputFile, outputDir)
}
