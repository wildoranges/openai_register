package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TempMailProvider struct {
	Name        string
	GenerateURL string
	CheckURL    string
	Headers     map[string]string
}

var tempMailProviders = []TempMailProvider{
	{
		Name:        "chatgpt.org.uk",
		GenerateURL: "https://mail.chatgpt.org.uk/api/generate-email",
		CheckURL:    "https://mail.chatgpt.org.uk/api/emails?email=%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
		},
	},
	{
		Name:        "tempmail.plus",
		GenerateURL: "https://tempmail.plus/api/v1/mail",
		CheckURL:    "https://tempmail.plus/api/v1/mail/%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
		},
	},
}

type HTTPClient struct {
	client *http.Client
}

func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func NewHTTPClientWithProxy(proxyURL string) *HTTPClient {
	client := &http.Client{Timeout: 60 * time.Second}

	if proxyURL != "" {
		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyParsed)}
		}
	}

	return &HTTPClient{client: client}
}

func (c *HTTPClient) SetDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("DNT", "1")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("sec-ch-ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Linux"`)
}

func (c *HTTPClient) GetTempEmail() (string, error) {
	for _, provider := range tempMailProviders {
		req, err := http.NewRequest("GET", provider.GenerateURL, nil)
		if err != nil {
			continue
		}

		for k, v := range provider.Headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			fmt.Printf("[%s] 请求失败: %v\n", provider.Name, err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("[%s] API响应: %s\n", provider.Name, string(body))

		var result1 struct {
			Email string `json:"email"`
		}
		var result2 struct {
			Success bool `json:"success"`
			Data    struct {
				Email string `json:"email"`
			} `json:"data"`
		}
		var result3 struct {
			Mail string `json:"mail"`
		}

		if err := json.Unmarshal(body, &result2); err == nil && result2.Data.Email != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result2.Data.Email)
			return result2.Data.Email, nil
		}
		if err := json.Unmarshal(body, &result1); err == nil && result1.Email != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result1.Email)
			return result1.Email, nil
		}
		if err := json.Unmarshal(body, &result3); err == nil && result3.Mail != "" {
			fmt.Printf("[%s] 获取邮箱成功: %s\n", provider.Name, result3.Mail)
			return result3.Mail, nil
		}
	}

	if email := c.get1secmailEmail(); email != "" {
		fmt.Printf("[1secmail] 获取邮箱: %s\n", email)
		return email, nil
	}

	if email := c.getMailTmEmail(); email != "" {
		fmt.Printf("[Mail.tm] 获取邮箱: %s\n", email)
		return email, nil
	}

	return "", fmt.Errorf("所有临时邮箱服务都不可用")
}

func (c *HTTPClient) get1secmailEmail() string {
	resp, err := c.client.Get("https://www.1secmail.com/api/v1/?action=genRandomMailbox&count=1")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var emails []string
	if err := json.Unmarshal(body, &emails); err != nil || len(emails) == 0 {
		return ""
	}

	return emails[0]
}

func (c *HTTPClient) getMailTmEmail() string {
	resp, err := c.client.Get("https://api.mail.tm/domains")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var domainsResp struct {
		Data []struct {
			Domain string `json:"domain"`
		} `json:"hydra:member"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&domainsResp); err != nil || len(domainsResp.Data) == 0 {
		body, _ := io.ReadAll(resp.Body)
		var altResp struct {
			Data []struct {
				Domain string `json:"domain"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &altResp); len(altResp.Data) == 0 {
			return ""
		}
		domainsResp.Data = altResp.Data
	}

	domain := domainsResp.Data[0].Domain
	address := fmt.Sprintf("%s@%s", randomString(10), domain)

	createReq := map[string]string{"address": address, "password": "TempPass123!"}
	createBody, _ := json.Marshal(createReq)

	req, _ := http.NewRequest("POST", "https://api.mail.tm/accounts", strings.NewReader(string(createBody)))
	req.Header.Set("Content-Type", "application/json")

	resp2, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 && resp2.StatusCode != 201 {
		return ""
	}

	return address
}

type MailService struct {
	Name   string
	Email  string
	Token  string
	Domain string
}

var currentMailService *MailService

func (c *HTTPClient) CheckEmail(email string) (string, error) {
	maxRetries := 60

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("无效的邮箱格式")
	}
	login, domain := parts[0], parts[1]
	fmt.Printf("检查邮箱: %s (login=%s, domain=%s)\n", email, login, domain)

	for i := 0; i < maxRetries; i++ {
		for _, provider := range tempMailProviders {
			apiURL := fmt.Sprintf(provider.CheckURL, url.QueryEscape(email))
			req, err := http.NewRequest("GET", apiURL, nil)
			if err != nil {
				continue
			}

			for k, v := range provider.Headers {
				req.Header.Set(k, v)
			}

			resp, err := c.client.Do(req)
			if err != nil {
				fmt.Printf("[%s] 检查邮件失败: %v\n", provider.Name, err)
				continue
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if i%10 == 0 {
				fmt.Printf("[%s] 检查结果: %s\n", provider.Name, string(body)[:min(200, len(body))])
			}

			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Emails []struct {
						Subject     string `json:"subject"`
						Content     string `json:"content"`
						HtmlContent string `json:"html_content"`
						Body        string `json:"body"`
					} `json:"emails"`
				} `json:"data"`
			}

			if err := json.Unmarshal(body, &response); err != nil {
				var directEmails []struct {
					Subject     string `json:"subject"`
					Content     string `json:"content"`
					HtmlContent string `json:"html_content"`
					Body        string `json:"body"`
				}
				if json.Unmarshal(body, &directEmails) == nil {
					for _, mail := range directEmails {
						if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
							return link, nil
						}
					}
				}
				continue
			}

			for _, mail := range response.Data.Emails {
				if link := c.checkMailContent(mail.Subject, mail.HtmlContent, mail.Content, mail.Body); link != "" {
					return link, nil
				}
			}
		}

		if link := c.check1secmail(login, domain); link != "" {
			return link, nil
		}

		fmt.Printf("  等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证邮件超时")
}

func (c *HTTPClient) checkMailContent(subject, htmlContent, content, body string) string {
	subjectLower := strings.ToLower(subject)
	if strings.Contains(subjectLower, "verify") ||
		strings.Contains(subjectLower, "openai") ||
		strings.Contains(subjectLower, "chatgpt") ||
		strings.Contains(subjectLower, "验证") ||
		strings.Contains(subjectLower, "confirm") {

		fullContent := htmlContent
		if fullContent == "" {
			fullContent = content
		}
		if fullContent == "" {
			fullContent = body
		}

		fmt.Printf("找到验证邮件: %s\n", subject)
		if link := extractVerifyLink(fullContent); link != "" {
			fmt.Printf("提取到验证链接: %s\n", link)
			return link
		}
	}
	return ""
}

func (c *HTTPClient) check1secmail(login, domain string) string {
	apiURL := fmt.Sprintf("https://www.1secmail.com/api/v1/?action=getMessages&login=%s&domain=%s", login, domain)

	resp, err := c.client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var messages []struct {
		ID      int    `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Date    string `json:"date"`
	}

	if err := json.Unmarshal(body, &messages); err != nil || len(messages) == 0 {
		return ""
	}

	for _, msg := range messages {
		subject := strings.ToLower(msg.Subject)
		if strings.Contains(subject, "verify") ||
			strings.Contains(subject, "openai") ||
			strings.Contains(subject, "chatgpt") ||
			strings.Contains(subject, "验证") {

			detailURL := fmt.Sprintf("https://www.1secmail.com/api/v1/?action=readMessage&login=%s&domain=%s&id=%d", login, domain, msg.ID)
			detailResp, err := c.client.Get(detailURL)
			if err != nil {
				continue
			}
			detailBody, _ := io.ReadAll(detailResp.Body)
			detailResp.Body.Close()

			var detail struct {
				Body string `json:"body"`
				Text string `json:"textBody"`
				HTML string `json:"htmlBody"`
			}

			if json.Unmarshal(detailBody, &detail) == nil {
				content := detail.HTML
				if content == "" {
					content = detail.Body
				}
				if content == "" {
					content = detail.Text
				}
				if link := extractVerifyLink(content); link != "" {
					return link
				}
			}
		}
	}

	return ""
}

func extractVerifyLink(content string) string {
	if otp := extractOTPCode(content); otp != "" {
		return "OTP:" + otp
	}

	patterns := []string{
		`https://auth.openai.com/authorize?`,
		`https://chat.openai.com/auth/`,
		`https://platform.openai.com/`,
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			end := idx
			for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
				end++
			}
			if end > idx {
				link := content[idx:end]
				link = strings.ReplaceAll(link, "&amp;", "&")
				return link
			}
		}
	}

	if idx := strings.Index(content, "token="); idx != -1 {
		start := idx
		for start > 0 && content[start-1] != '"' && content[start-1] != '\'' && content[start-1] != ' ' {
			start--
		}
		end := idx
		for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '<' {
			end++
		}
		return content[start:end]
	}

	return ""
}

func extractOTPCode(content string) string {
	patterns := []string{
		"Your ChatGPT code is ",
		"Your verification code is ",
		"verification code: ",
		"code: ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx != -1 {
			start := idx + len(pattern)
			code := ""
			for i := 0; i < 20 && start+i < len(content); i++ {
				c := content[start+i]
				if c >= '0' && c <= '9' {
					code += string(c)
					if len(code) == 6 {
						return code
					}
				} else if c == ' ' || c == '\n' || c == '\r' || c == '<' {
					if len(code) >= 4 {
						return code
					}
					continue
				} else if len(code) > 0 {
					break
				}
			}
			if len(code) >= 4 {
				return code
			}
		}
	}

	for i := 0; i < len(content)-5; i++ {
		if content[i] >= '0' && content[i] <= '9' {
			code := ""
			for j := 0; j < 6 && i+j < len(content); j++ {
				if content[i+j] >= '0' && content[i+j] <= '9' {
					code += string(content[i+j])
				}
			}
			if len(code) == 6 {
				return code
			}
		}
	}

	return ""
}
