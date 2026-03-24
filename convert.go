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

func decodeJWTPayloadForConvert(token string) (map[string]interface{}, error) {
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

func ConvertCredentialToCLIProxy(cred *AccountCredentials, outputDir string) error {
	if cred == nil || cred.AccessToken == "" {
		return fmt.Errorf("invalid credential")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create directory failed: %v", err)
	}

	payload, err := decodeJWTPayloadForConvert(cred.AccessToken)
	if err != nil {
		Printf("JWT decode warning: %v\n", err)
	}

	cliproxyCred := CLIProxyCredential{
		IDToken:      cred.IDToken,
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		AccountID:    "",
		LastRefresh:  time.Now().Format(time.RFC3339),
		Email:        cred.Email,
		Type:         "codex",
		Expired:      "",
	}

	if payload != nil {
		if auth, ok := payload["https://api.openai.com/auth"].(map[string]interface{}); ok {
			if accountID, ok := auth["chatgpt_account_id"].(string); ok {
				cliproxyCred.AccountID = accountID
			}
		}
		if exp, ok := payload["exp"].(float64); ok && exp > 0 {
			cliproxyCred.Expired = time.Unix(int64(exp), 0).Format(time.RFC3339)
		}
	}

	jsonData, err := json.MarshalIndent(cliproxyCred, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal failed: %v", err)
	}

	filename := fmt.Sprintf("codex-%s.json", cred.Email)
	filePath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("write file failed: %v", err)
	}

	Printf("CLIProxyAPI 凭证已保存到: %s\n", filePath)
	return nil
}

func ConvertAllCredentials(inputFile, outputDir string) (int, error) {
	data, err := os.ReadFile(inputFile)
	if err != nil {
		return 0, fmt.Errorf("read file failed: %v", err)
	}

	var creds []AccountCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return 0, fmt.Errorf("parse JSON failed: %v", err)
	}

	if len(creds) == 0 {
		return 0, fmt.Errorf("no credentials found")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return 0, fmt.Errorf("create directory failed: %v", err)
	}

	converted := 0
	for _, cred := range creds {
		if cred.AccessToken == "" {
			continue
		}
		if err := ConvertCredentialToCLIProxy(&cred, outputDir); err != nil {
			Printf("转换失败 %s: %v\n", cred.Email, err)
			continue
		}
		converted++
	}

	return converted, nil
}
