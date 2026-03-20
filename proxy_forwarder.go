package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type LocalProxyForwarder struct {
	listener   net.Listener
	targetURL  *url.URL
	authHeader string
}

func NewLocalProxyForwarder(proxyURL string) (*LocalProxyForwarder, error) {
	targetURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理URL失败: %v", err)
	}

	var authHeader string
	if targetURL.User != nil {
		password, _ := targetURL.User.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(targetURL.User.Username() + ":" + password))
		authHeader = "Basic " + auth
	}

	return &LocalProxyForwarder{
		targetURL:  targetURL,
		authHeader: authHeader,
	}, nil
}

func (lpf *LocalProxyForwarder) Start() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("启动本地代理监听失败: %v", err)
	}
	lpf.listener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go lpf.handleConnection(conn)
		}
	}()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return "", err
	}

	return "127.0.0.1:" + port, nil
}

func (lpf *LocalProxyForwarder) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	buf := make([]byte, 4096)
	n, err := clientConn.Read(buf)
	if err != nil {
		return
	}

	request := string(buf[:n])
	lines := strings.Split(request, "\r\n")
	if len(lines) == 0 {
		return
	}

	if strings.HasPrefix(lines[0], "CONNECT") {
		parts := strings.Fields(lines[0])
		if len(parts) < 2 {
			return
		}
		targetAddr := parts[1]

		proxyConn, err := net.Dial("tcp", lpf.targetURL.Host)
		if err != nil {
			return
		}
		defer proxyConn.Close()

		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)
		if lpf.authHeader != "" {
			connectReq += fmt.Sprintf("Proxy-Authorization: %s\r\n", lpf.authHeader)
		}
		connectReq += "\r\n"

		proxyConn.Write([]byte(connectReq))

		respBuf := make([]byte, 1024)
		proxyN, err := proxyConn.Read(respBuf)
		if err != nil {
			return
		}

		resp := string(respBuf[:proxyN])
		if !strings.Contains(resp, "200") {
			Printf("代理连接失败: %s\n", resp)
			return
		}

		clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := proxyConn.Read(buf)
				if err != nil {
					return
				}
				clientConn.Write(buf[:n])
			}
		}()

		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			proxyConn.Write(buf[:n])
		}
	} else {
		proxyConn, err := net.Dial("tcp", lpf.targetURL.Host)
		if err != nil {
			return
		}
		defer proxyConn.Close()

		if lpf.authHeader != "" && !strings.Contains(request, "Proxy-Authorization") {
			newRequest := lines[0] + "\r\nProxy-Authorization: " + lpf.authHeader + "\r\n"
			for i := 1; i < len(lines); i++ {
				newRequest += lines[i] + "\r\n"
			}
			proxyConn.Write([]byte(newRequest))
		} else {
			proxyConn.Write(buf[:n])
		}

		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := proxyConn.Read(buf)
				if err != nil {
					return
				}
				clientConn.Write(buf[:n])
			}
		}()

		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			proxyConn.Write(buf[:n])
		}
	}
}

func (lpf *LocalProxyForwarder) Stop() {
	if lpf.listener != nil {
		lpf.listener.Close()
	}
}
