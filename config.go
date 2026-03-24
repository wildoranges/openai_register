package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClashConfig holds optional Clash proxy configuration.
type ClashConfig struct {
	ExternalController string   `json:"external_controller"`
	Secret             string   `json:"secret"`
	ProxyGroup         string   `json:"proxy_group"`
	MixedProxy         string   `json:"mixed_proxy"`
	Include            []string `json:"include"`
	Exclude            []string `json:"exclude"`
}

type Config struct {
	Proxy      string       `json:"proxy"`
	Proxies    []string     `json:"proxies"`
	Headless   bool         `json:"headless"`
	Timeout    int          `json:"timeout"`
	Debug      bool         `json:"debug"`
	OutputDir  string       `json:"output_dir"`
	ConvertDir string       `json:"convert_dir"`
	Count      int          `json:"count"`
	Clash      *ClashConfig `json:"clash,omitempty"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Headless:   true,
		Timeout:    60,
		Debug:      false,
		OutputDir:  "./creds",
		ConvertDir: filepath.Join(home, ".cli-proxy-api"),
		Count:      1,
	}
}

func LoadConfig(path string) (*Config, error) {
	config := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return config, nil
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	config.OutputDir, err = normalizeDirPath(config.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("解析 output_dir 失败: %v", err)
	}
	config.ConvertDir, err = normalizeDirPath(config.ConvertDir)
	if err != nil {
		return nil, fmt.Errorf("解析 convert_dir 失败: %v", err)
	}

	return config, nil
}

func normalizeDirPath(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}

	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}

	absPath, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

func SaveConfig(config *Config, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
