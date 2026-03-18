package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Proxy     string   `json:"proxy"`
	Proxies   []string `json:"proxies"`
	Headless  bool     `json:"headless"`
	Timeout   int      `json:"timeout"`
	Debug     bool     `json:"debug"`
	OutputDir string   `json:"output_dir"`
	Count     int      `json:"count"`
}

func DefaultConfig() *Config {
	return &Config{
		Headless:  true,
		Timeout:   60,
		Debug:     false,
		OutputDir: "./creds",
		Count:     1,
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

	return config, nil
}

func SaveConfig(config *Config, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
