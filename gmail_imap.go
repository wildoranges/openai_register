package main

import (
	"crypto/tls"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type GmailIMAPClient struct {
	Email    string
	Password string
}

func NewGmailIMAPClient(email, password string) *GmailIMAPClient {
	return &GmailIMAPClient{
		Email:    email,
		Password: password,
	}
}

func (g *GmailIMAPClient) GetOpenAIOTP(timeout time.Duration) (string, error) {
	imapClient, err := client.DialTLS("imap.gmail.com:993", &tls.Config{
		InsecureSkipVerify: false,
	})
	if err != nil {
		return "", fmt.Errorf("连接 IMAP 服务器失败: %v", err)
	}
	defer imapClient.Logout()

	if err := imapClient.Login(g.Email, g.Password); err != nil {
		return "", fmt.Errorf("IMAP 登录失败: %v (请检查应用专用密码是否正确)", err)
	}

	_, err = imapClient.Select("INBOX", false)
	if err != nil {
		return "", fmt.Errorf("选择收件箱失败: %v", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		otp, err := g.searchOTPInInbox(imapClient)
		if err == nil && otp != "" {
			return otp, nil
		}

		remaining := time.Until(deadline).Seconds()
		Printf("等待 OpenAI 验证码邮件... (剩余 %.0f 秒)\n", remaining)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证码超时")
}

func (g *GmailIMAPClient) searchOTPInInbox(imapClient *client.Client) (string, error) {
	criteria := imap.NewSearchCriteria()
	criteria.Since = time.Now().Add(-10 * time.Minute)

	senders := []string{
		"noreply@tm.openai.com",
		"no-reply@openai.com",
		"noreply@openai.com",
		"team@openai.com",
	}

	for _, sender := range senders {
		criteria.Header.Add("From", sender)
		uids, err := imapClient.Search(criteria)
		if err != nil {
			continue
		}

		if len(uids) == 0 {
			continue
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uids[len(uids)-1])

		messages := make(chan *imap.Message, 1)
		section := &imap.BodySectionName{}

		err = imapClient.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages)
		if err != nil {
			continue
		}

		msg := <-messages
		if msg == nil {
			continue
		}

		for _, literal := range msg.Body {
			if literal == nil {
				continue
			}

			body := make([]byte, literal.Len())
			literal.Read(body)

			otp := extractOTPFromEmail(string(body))
			if otp != "" {
				return otp, nil
			}
		}
	}

	return "", fmt.Errorf("未找到验证码邮件")
}

func extractOTPFromEmail(body string) string {
	patterns := []string{
		`(?i)verification\s*code[:\s]*(\d{6})`,
		`(?i)code[:\s]*(\d{6})`,
		`(?i)enter[:\s]*(\d{6})`,
		`(?i)your\s+code\s+is[:\s]*(\d{6})`,
		`(?i)(\d{6})\s*(?:is\s+your|as\s+your)\s+verification`,
		`>(\d{6})<`,
		`\b(\d{6})\b`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(body)
		if len(matches) >= 2 {
			code := matches[1]
			if len(code) == 6 && strings.HasPrefix(code, "0") ||
				len(code) == 6 && code != "000000" {
				return code
			}
		}
	}

	return ""
}

func (g *GmailIMAPClient) GetOpenAIVerificationLink(timeout time.Duration) (string, error) {
	imapClient, err := client.DialTLS("imap.gmail.com:993", &tls.Config{
		InsecureSkipVerify: false,
	})
	if err != nil {
		return "", fmt.Errorf("连接 IMAP 服务器失败: %v", err)
	}
	defer imapClient.Logout()

	if err := imapClient.Login(g.Email, g.Password); err != nil {
		return "", fmt.Errorf("IMAP 登录失败: %v", err)
	}

	_, err = imapClient.Select("INBOX", false)
	if err != nil {
		return "", fmt.Errorf("选择收件箱失败: %v", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		link, err := g.searchLinkInInbox(imapClient)
		if err == nil && link != "" {
			return link, nil
		}

		remaining := time.Until(deadline).Seconds()
		Printf("等待 OpenAI 验证邮件链接... (剩余 %.0f 秒)\n", remaining)
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("等待验证链接超时")
}

func (g *GmailIMAPClient) searchLinkInInbox(imapClient *client.Client) (string, error) {
	criteria := imap.NewSearchCriteria()
	criteria.Since = time.Now().Add(-10 * time.Minute)

	senders := []string{
		"noreply@tm.openai.com",
		"no-reply@openai.com",
		"noreply@openai.com",
	}

	for _, sender := range senders {
		criteria.Header.Add("From", sender)
		uids, err := imapClient.Search(criteria)
		if err != nil {
			continue
		}

		if len(uids) == 0 {
			continue
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uids[len(uids)-1])

		messages := make(chan *imap.Message, 1)
		section := &imap.BodySectionName{}

		err = imapClient.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages)
		if err != nil {
			continue
		}

		msg := <-messages
		if msg == nil {
			continue
		}

		for _, literal := range msg.Body {
			if literal == nil {
				continue
			}

			body := make([]byte, literal.Len())
			literal.Read(body)

			link := extractVerificationLink(string(body))
			if link != "" {
				return link, nil
			}
		}
	}

	return "", fmt.Errorf("未找到验证链接")
}

func extractVerificationLink(body string) string {
	patterns := []string{
		`https://auth\.openai\.com/authorize/callback\?[^"\s<>]+`,
		`https://auth\.openai\.com/u/email-verification\?[^"\s<>]+`,
		`https://chatgpt\.com/api/auth/callback/email\?[^"\s<>]+`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindString(body)
		if match != "" {
			return match
		}
	}

	return ""
}
