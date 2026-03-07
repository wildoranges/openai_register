package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"crypto/rand"
)

// OAuth PKCE 配置常量
const (
	OAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	OAuthAuthURL      = "https://auth.openai.com/oauth/authorize"
	OAuthTokenURL     = "https://auth.openai.com/oauth/token"
	OAuthCallbackPort = 1455
	OAuthRedirectURI  = "http://localhost:1455/auth/callback"
)

// PKCECodes 存储 PKCE 相关的代码
type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
	State         string
}

// GeneratePKCECodes 生成 PKCE 所需的 code_verifier 和 code_challenge
func GeneratePKCECodes() (*PKCECodes, error) {
	// 生成 32 字节的随机 verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("生成 code_verifier 失败: %v", err)
	}

	// Base64 URL 编码 (无填充)
	codeVerifier := base64URLEncode(verifierBytes)

	// SHA256 哈希 verifier
	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64URLEncode(hash[:])

	// 生成 state
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("生成 state 失败: %v", err)
	}
	state := base64URLEncode(stateBytes)

	return &PKCECodes{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
		State:         state,
	}, nil
}

// base64URLEncode 执行 Base64 URL 安全编码（无填充）
func base64URLEncode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// BuildOAuthAuthURL 构建 OAuth 授权 URL
func BuildOAuthAuthURL(pkce *PKCECodes) string {
	return fmt.Sprintf(
		"%s?client_id=%s&response_type=code&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256&prompt=login&id_token_add_organizations=true&codex_cli_simplified_flow=true",
		OAuthAuthURL,
		OAuthClientID,
		OAuthRedirectURI,
		"openid email profile offline_access",
		pkce.State,
		pkce.CodeChallenge,
	)
}

// TokenResponse 存储 OAuth token 响应
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// OAuthResult 存储 OAuth 回调结果
type OAuthResult struct {
	Code  string
	State string
	Error string
}
