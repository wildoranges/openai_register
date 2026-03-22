package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type SMSActivateClient struct {
	APIKey  string
	BaseURL string
}

type SMSNumber struct {
	ActivationID string
	PhoneNumber  string
	Cost         float64
	Country      string
}

func NewSMSActivateClient(apiKey string) *SMSActivateClient {
	return &SMSActivateClient{
		APIKey:  apiKey,
		BaseURL: "https://hero-sms.com/stubs/handler_api.php",
	}
}

func NewSMSActivateClientWithBaseURL(apiKey string, baseURL string) *SMSActivateClient {
	if baseURL == "" {
		baseURL = "https://hero-sms.com/stubs/handler_api.php"
	}
	return &SMSActivateClient{
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
}

func (c *SMSActivateClient) GetBalance() (float64, error) {
	resp, err := http.Get(fmt.Sprintf("%s?api_key=YOUR_API_KEY&action=getBalance", c.BaseURL, c.APIKey))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := string(body)

	if strings.HasPrefix(result, "ACCESS_BALANCE") {
		parts := strings.Split(result, ":")
		if len(parts) >= 2 {
			balance, _ := strconv.ParseFloat(parts[1], 64)
			return balance, nil
		}
	}

	return 0, fmt.Errorf("failed to get balance: %s", result)
}

func (c *SMSActivateClient) GetNumber(service string, country int) (*SMSNumber, error) {
	apiURL := fmt.Sprintf("%s?api_key=YOUR_API_KEY&action=getNumber&service=%s&country=%d",
		c.BaseURL, c.APIKey, service, country)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := string(body)

	if strings.HasPrefix(result, "ACCESS_NUMBER") {
		parts := strings.Split(result, ":")
		if len(parts) >= 3 {
			return &SMSNumber{
				ActivationID: parts[1],
				PhoneNumber:  parts[2],
			}, nil
		}
	}

	return nil, fmt.Errorf("failed to get number: %s", result)
}

func (c *SMSActivateClient) SetStatus(activationID string, status int) error {
	apiURL := fmt.Sprintf("%s?api_key=YOUR_API_KEY&action=setStatus&id=%s&status=%d",
		c.BaseURL, c.APIKey, activationID, status)

	resp, err := http.Get(apiURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := string(body)

	if strings.Contains(result, "ACCESS") {
		return nil
	}

	return fmt.Errorf("failed to set status: %s", result)
}

func (c *SMSActivateClient) GetSMSCode(activationID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		apiURL := fmt.Sprintf("%s?api_key=YOUR_API_KEY&action=getStatus&id=%s",
			c.BaseURL, c.APIKey, activationID)

		resp, err := http.Get(apiURL)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		result := string(body)

		if strings.HasPrefix(result, "STATUS_OK") {
			parts := strings.Split(result, ":")
			if len(parts) >= 2 {
				return parts[1], nil
			}
		}

		if strings.HasPrefix(result, "STATUS_WAIT_CODE") {
			Printf("等待验证码... (剩余 %.0f 秒)\n", time.Until(deadline).Seconds())
			time.Sleep(5 * time.Second)
			continue
		}

		if strings.HasPrefix(result, "STATUS_WAIT_RETRY") {
			Println("等待重发验证码...")
			time.Sleep(10 * time.Second)
			continue
		}

		time.Sleep(3 * time.Second)
	}

	return "", fmt.Errorf("等待验证码超时")
}

func (c *SMSActivateClient) CancelActivation(activationID string) error {
	return c.SetStatus(activationID, 8)
}

func (c *SMSActivateClient) ConfirmActivation(activationID string) error {
	return c.SetStatus(activationID, 6)
}

func (c *SMSActivateClient) GetAvailableNumbers(service string, country int) (map[string]int, error) {
	apiURL := fmt.Sprintf("%s?api_key=YOUR_API_KEY&action=getNumbersStatus&service=%s&country=%d",
		c.BaseURL, c.APIKey, service, country)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]int
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *SMSActivateClient) GetPhoneNumberForOpenAI() (*SMSNumber, error) {
	Println("正在获取手机号...")

	balance, err := c.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %v", err)
	}
	Printf("余额: %.2f RUB\n", balance)

	if balance < 10 {
		return nil, fmt.Errorf("余额不足，请充值")
	}

	services := []string{"go", "ot", "op"}
	countries := []int{0, 1, 22, 57, 175}

	for _, service := range services {
		for _, country := range countries {
			num, err := c.GetNumber(service, country)
			if err == nil && num != nil {
				Printf("成功获取手机号: %s (国家: %d, 服务: %s)\n", num.PhoneNumber, country, service)
				return num, nil
			}
			Printf("尝试服务 %s 国家 %d 失败: %v\n", service, country, err)
			time.Sleep(1 * time.Second)
		}
	}

	return nil, fmt.Errorf("无法获取可用的手机号")
}

func (c *SMSActivateClient) WaitForOTPAndConfirm(activationID string, timeout time.Duration) (string, error) {
	err := c.SetStatus(activationID, 1)
	if err != nil {
		return "", fmt.Errorf("设置就绪状态失败: %v", err)
	}

	code, err := c.GetSMSCode(activationID, timeout)
	if err != nil {
		c.CancelActivation(activationID)
		return "", err
	}

	err = c.ConfirmActivation(activationID)
	if err != nil {
		Printf("确认激活失败: %v\n", err)
	}

	return code, nil
}

type SMSActivateConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key"`
	Service string `json:"service"`
	Country int    `json:"country"`
}

func LoadSMSActivateConfig(configPath string) (*SMSActivateConfig, error) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config struct {
		SMSActivate SMSActivateConfig `json:"sms_activate"`
	}

	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, err
	}

	return &config.SMSActivate, nil
}
