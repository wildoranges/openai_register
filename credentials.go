package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AccountCredentials struct {
	Email        string    `json:"email"`
	Password     string    `json:"password"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	SessionID    string    `json:"session_id"`
	UserID       string    `json:"user_id"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func SaveCredentialsWithDir(credentials *AccountCredentials, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	credFile := filepath.Join(dataDir, "openai_credentials.json")
	existing := []AccountCredentials{}

	if data, err := os.ReadFile(credFile); err == nil {
		json.Unmarshal(data, &existing)
	}

	found := false
	for i, cred := range existing {
		if cred.Email == credentials.Email {
			existing[i] = *credentials
			found = true
			Printf("更新已存在的凭证: %s\n", credentials.Email)
			break
		}
	}
	if !found {
		existing = append(existing, *credentials)
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(credFile, data, 0644); err != nil {
		return err
	}

codexAuth := map[string]interface{}{
		"type":          "codex",
		"access_token":  credentials.AccessToken,
		"refresh_token": credentials.RefreshToken,
		"id_token":       credentials.IDToken,
		"email":         credentials.Email,
		"user_id":       credentials.UserID,
		"expired":       time.Now().Add(time.Duration(credentials.ExpiresIn) * time.Second).Format(time.RFC3339),
		"created_at":    credentials.CreatedAt,
	}
	codexData, _ := json.MarshalIndent(codexAuth, "", "  ")
	codexFile := filepath.Join(dataDir, fmt.Sprintf("auth_%s.json", credentials.Email[:strings.Index(credentials.Email, "@")]))
	os.WriteFile(codexFile, codexData, 0600)
	Printf("CodeX凭证已保存到: %s\n", codexFile)

	tokenFile := filepath.Join(dataDir, "openai_tokens.txt")
	existingTokens := ""
	if tokenData, err := os.ReadFile(tokenFile); err == nil {
		existingTokens = string(tokenData)
	}

	if strings.Contains(existingTokens, fmt.Sprintf("OPENAI_EMAIL=%s\n", credentials.Email)) {
		lines := strings.Split(existingTokens, "\n")
		var newLines []string
		i := 0
		for i < len(lines) {
			if strings.HasPrefix(lines[i], "# Account: ") && i+4 < len(lines) {
				if i+2 < len(lines) && strings.Contains(lines[i+2], fmt.Sprintf("OPENAI_EMAIL=%s", credentials.Email)) {
					i += 5
					continue
				}
			}
			newLines = append(newLines, lines[i])
			i++
		}
		existingTokens = strings.Join(newLines, "\n")
	}

newRecord := fmt.Sprintf("# Account: %s\nOPENAI_ACCESS_TOKEN=%s\nOPENAI_REFRESH_TOKEN=%s\nOPENAI_EMAIL=%s\nOPENAI_PASSWORD=%s\n\n",
		credentials.Email, credentials.AccessToken, credentials.RefreshToken, credentials.Email, credentials.Password)
	existingTokens += newRecord

	if err := os.WriteFile(tokenFile, []byte(existingTokens), 0644); err != nil {
		return err
	}

	return nil
}

func SaveCredentials(credentials *AccountCredentials) error {
	return SaveCredentialsWithDir(credentials, "./creds")
}
