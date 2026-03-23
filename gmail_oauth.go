package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailOAuthClient struct {
	service *gmail.Service
	email   string
}

func NewGmailOAuthClientWithCredential(cred *GmailCredential) (*GmailOAuthClient, error) {
	if cred == nil {
		return nil, fmt.Errorf("Gmail credential is nil")
	}

	ctx := context.Background()

	config := &oauth2.Config{
		ClientID:     cred.ClientID,
		ClientSecret: cred.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailReadonlyScope},
		RedirectURL:  "http://localhost",
	}

	token := &oauth2.Token{
		RefreshToken: cred.RefreshToken,
		AccessToken:  cred.AccessToken,
	}

	if cred.TokenExpiry != "" {
		token.Expiry, _ = time.Parse(time.RFC3339, cred.TokenExpiry)
	}

	service, err := gmail.NewService(ctx, option.WithTokenSource(config.TokenSource(ctx, token)))
	if err != nil {
		return nil, fmt.Errorf("创建 Gmail 服务失败: %v", err)
	}

	return &GmailOAuthClient{
		service: service,
		email:   cred.Email,
	}, nil
}

func NewGmailOAuthClient(email string) (*GmailOAuthClient, error) {
	tokenPath := getGmailTokenPath()

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("未找到 Gmail token: %v", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("解析 token 失败: %v", err)
	}

	config := &oauth2.Config{
		ClientID:     "645377022671-4co60k0qdlgoj9i3tng13e1usb0q90t0.apps.googleusercontent.com",
		ClientSecret: "REDACTED",
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailReadonlyScope},
		RedirectURL:  "http://localhost",
	}

	ctx := context.Background()
	service, err := gmail.NewService(ctx, option.WithTokenSource(config.TokenSource(ctx, &token)))
	if err != nil {
		return nil, fmt.Errorf("创建 Gmail 服务失败: %v", err)
	}

	return &GmailOAuthClient{
		service: service,
		email:   email,
	}, nil
}

func getGmailTokenPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".config", "openai-register", "gmail_token.json")
}

func (g *GmailOAuthClient) GetOpenAIOTP(timeout time.Duration) (string, error) {
	return g.GetOpenAIOTPSkipUsed(timeout, nil)
}

func (g *GmailOAuthClient) GetOpenAIOTPSkipUsed(timeout time.Duration, usedOTPs map[string]bool) (string, error) {
	return g.GetOpenAIOTPAfterTime(timeout, usedOTPs, time.Now().Add(-10*time.Minute))
}

func (g *GmailOAuthClient) GetOpenAIOTPAfterTime(timeout time.Duration, usedOTPs map[string]bool, afterTime time.Time) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		otp, err := g.searchOTPInGmailAfterTime(usedOTPs, afterTime)
		if err == nil && otp != "" {
			return otp, nil
		}

		remaining := time.Until(deadline).Seconds()
		Printf("等待 OpenAI 验证码邮件... (剩余 %.0f 秒)\n", remaining)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证码超时")
}

func (g *GmailOAuthClient) searchOTPInGmailSkipUsed(usedOTPs map[string]bool) (string, error) {
	return g.searchOTPInGmailAfterTime(usedOTPs, time.Now().Add(-10*time.Minute))
}

func (g *GmailOAuthClient) searchOTPInGmailAfterTime(usedOTPs map[string]bool, afterTime time.Time) (string, error) {
	afterTimestamp := afterTime.Unix()
	query := fmt.Sprintf("from:(noreply@tm.openai.com OR no-reply@openai.com OR noreply@openai.com OR otp@tm1.openai.com) after:%d", afterTimestamp)

	Printf("Gmail 搜索查询: %s\n", query)
	resp, err := g.service.Users.Messages.List("me").Q(query).MaxResults(5).Do()
	if err != nil {
		return "", err
	}

	Printf("Gmail 找到 %d 封邮件\n", len(resp.Messages))

	if len(resp.Messages) == 0 {
		return "", fmt.Errorf("未找到验证邮件")
	}

	for _, msg := range resp.Messages {
		fullMsg, err := g.service.Users.Messages.Get("me", msg.Id).Format("full").Do()
		if err != nil {
			continue
		}

		var subject string
		for _, h := range fullMsg.Payload.Headers {
			if h.Name == "Subject" {
				subject = h.Value
			}
		}

		body := extractBodyFromMessage(fullMsg)
		Printf("邮件主题: %s, 正文长度: %d\n", subject, len(body))

		otp := extractOTPFromGmail(body)
		if otp != "" {
			if usedOTPs != nil && usedOTPs[otp] {
				Printf("跳过已使用的验证码: %s\n", otp)
				continue
			}
			Printf("找到验证码: %s\n", otp)
			return otp, nil
		}
	}

	return "", fmt.Errorf("未找到验证码")
}

func extractBodyFromMessage(msg *gmail.Message) string {
	if msg.Payload == nil {
		return ""
	}

	if msg.Payload.Body != nil && msg.Payload.Body.Data != "" {
		decoded, err := base64.URLEncoding.DecodeString(msg.Payload.Body.Data)
		if err == nil {
			return string(decoded)
		}
	}

	var result string
	extractBodyFromParts(msg.Payload.Parts, &result)
	return result
}

func extractBodyFromParts(parts []*gmail.MessagePart, result *string) {
	for _, part := range parts {
		if part.Body != nil && part.Body.Data != "" {
			decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil && len(decoded) > 0 {
				*result += string(decoded)
			}
		}
		if len(part.Parts) > 0 {
			extractBodyFromParts(part.Parts, result)
		}
	}
}

func extractOTPFromGmail(body string) string {
	patterns := []string{
		`(?i)ChatGPT code is (\d{6})`,
		`(?i)OpenAI code is (\d{6})`,
		`(?i)your code is (\d{6})`,
		`(?i)verification\s*code[:\s]*(\d{6})`,
		`(?i)code[:\s]*(\d{6})`,
		`(?i)enter[:\s]*(\d{6})`,
		`>(\d{6})<`,
		`(\d{6})`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(body)
		if len(matches) >= 2 {
			code := matches[1]
			if len(code) == 6 {
				return code
			}
		}
	}

	return ""
}

func CheckGmailToken() bool {
	tokenPath := getGmailTokenPath()
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return false
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return false
	}
	return token.Valid()
}

func PrintGmailSetupInstructions() {
	fmt.Println("========================================")
	fmt.Println("设置 Gmail API 访问")
	fmt.Println("========================================")
	fmt.Println("在 config.json 中配置 gmail_oauth.credential:")
	fmt.Println(`{
  "gmail_oauth": {
    "enabled": true,
    "credential": {
      "email": "your-email@gmail.com",
      "client_id": "...",
      "client_secret": "...",
      "refresh_token": "..."
    }
  }
}`)
	fmt.Println("========================================")
}
