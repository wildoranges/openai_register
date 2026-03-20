package main

import (
	"context"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// OAuthCallbackServer OAuth 回调服务器
type OAuthCallbackServer struct {
	server   *http.Server
	resultCh chan *OAuthResult
	mu       sync.Mutex
	started  bool
	port     int
}

// NewOAuthCallbackServer 创建新的 OAuth 回调服务器
func NewOAuthCallbackServer() *OAuthCallbackServer {
	return &OAuthCallbackServer{
		resultCh: make(chan *OAuthResult, 1),
	}
}

// Start 启动 OAuth 回调服务器
func (s *OAuthCallbackServer) Start() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return BuildOAuthRedirectURI(s.port), nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", s.handleCallback)
	mux.HandleFunc("/success", s.handleSuccess)

	preferredAddr := fmt.Sprintf("127.0.0.1:%d", OAuthCallbackPort)
	listener, err := net.Listen("tcp", preferredAddr)
	if err != nil {
		Printf("⚠️ OAuth 回调端口 %d 不可用: %v，自动尝试可用端口...\n", OAuthCallbackPort, err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("启动 OAuth 回调服务器失败: %v", err)
		}
	}

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		return "", fmt.Errorf("解析 OAuth 回调端口失败: %v", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		listener.Close()
		return "", fmt.Errorf("解析 OAuth 回调端口失败: %v", err)
	}

	s.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			Printf("OAuth 回调服务器错误: %v\n", err)
		}
	}()

	s.port = port
	s.started = true
	redirectURI := BuildOAuthRedirectURI(s.port)
	Printf("🌐 OAuth 回调服务器已启动: %s\n", redirectURI)

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)
	return redirectURI, nil
}

// Stop 停止 OAuth 回调服务器
func (s *OAuthCallbackServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("关闭 OAuth 回调服务器失败: %v", err)
	}

	s.port = 0
	s.started = false
	Println("🔒 OAuth 回调服务器已关闭")
	return nil
}

// handleCallback 处理 OAuth 回调
func (s *OAuthCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// 检查是否有错误
	if errParam := query.Get("error"); errParam != "" {
		Printf("❌ OAuth 回调收到错误: %s\n", errParam)
		s.resultCh <- &OAuthResult{Error: errParam}
		http.Error(w, fmt.Sprintf("授权失败: %s", errParam), http.StatusBadRequest)
		return
	}

	// 获取授权码
	code := query.Get("code")
	state := query.Get("state")

	if code == "" {
		Println("❌ OAuth 回调缺少 code 参数")
		s.resultCh <- &OAuthResult{Error: "no_code"}
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	Printf("✅ OAuth 回调收到 code (前8位: %s...)\n", truncateString(code, 8))
	s.resultCh <- &OAuthResult{Code: code, State: state}

	// 重定向到成功页面
	http.Redirect(w, r, "/success", http.StatusFound)
}

// handleSuccess 处理成功页面
func (s *OAuthCallbackServer) handleSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	tmpl := template.Must(template.New("success").Parse(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>授权成功</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background: linear-gradient(135deg, #28a745 0%, #20c997 100%);
        }
        .container {
            text-align: center;
            color: white;
        }
        h1 {
            font-size: 2.5rem;
            margin-bottom: 1rem;
        }
        p {
            font-size: 1.2rem;
            opacity: 0.9;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>✅ 授权成功</h1>
        <p>您可以关闭此窗口并返回应用</p>
    </div>
</body>
</html>`))

	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// WaitForResult 等待 OAuth 回调结果
func (s *OAuthCallbackServer) WaitForResult(timeout time.Duration) *OAuthResult {
	select {
	case result := <-s.resultCh:
		return result
	case <-time.After(timeout):
		return &OAuthResult{Error: "timeout"}
	}
}

// Reset 重置结果通道
func (s *OAuthCallbackServer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 清空通道中的旧结果
	select {
	case <-s.resultCh:
	default:
	}
}

// truncateString 截断字符串
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
