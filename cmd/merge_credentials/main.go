package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credential 凭证结构（使用 map 保留所有字段）
type Credential map[string]interface{}

func main() {
	args := os.Args[1:]

	if len(args) < 2 {
		fmt.Println("用法: ./merge_credentials <目标目录> <源目录1> [源目录2] ...")
		fmt.Println("示例: ./merge_credentials creds_merged creds creds_refresh")
		fmt.Println("")
		fmt.Println("说明:")
		fmt.Println("  - 读取所有源目录的凭证（auth_*.json 和 openai_credentials.json）")
		fmt.Println("  - 根据 email 去重")
		fmt.Println("  - 输出到目标目录的 openai_credentials.json 和 auth_*.json")
		os.Exit(1)
	}

	targetDir := args[0]
	sourceDirs := args[1:]

	fmt.Println("==================================================")
	fmt.Println("  凭证合并工具")
	fmt.Println("==================================================")
	fmt.Println()
	fmt.Printf("目标目录: %s\n", targetDir)
	fmt.Printf("源目录: %s\n", strings.Join(sourceDirs, ", "))
	fmt.Println()

	// 用于去重的 map
	credentialsMap := make(map[string]Credential)
	duplicateCount := 0
	errorCount := 0

	// getEmail 从凭证中获取 email
	getEmail := func(cred Credential) string {
		if v, ok := cred["email"].(string); ok {
			return v
		}
		return ""
	}

	// 遍历所有源目录
	for _, dir := range sourceDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			fmt.Printf("\033[1;33m警告: 目录不存在 %s\033[0m\n", dir)
			continue
		}

		fmt.Printf("扫描目录: %s\n", dir)

		// 1. 读取 openai_credentials.json
		credFile := filepath.Join(dir, "openai_credentials.json")
		if _, err := os.Stat(credFile); err == nil {
			data, err := os.ReadFile(credFile)
			if err != nil {
				fmt.Printf("  \033[1;31m读取失败: %s - %v\033[0m\n", credFile, err)
				errorCount++
			} else {
				var creds []Credential
				if err := json.Unmarshal(data, &creds); err != nil {
					fmt.Printf("  \033[1;31m解析失败: %s - %v\033[0m\n", credFile, err)
					errorCount++
				} else {
					for _, cred := range creds {
						email := getEmail(cred)
						if email == "" {
							continue
						}
						if _, exists := credentialsMap[email]; exists {
							duplicateCount++
						} else {
							credentialsMap[email] = cred
						}
					}
					fmt.Printf("  从 openai_credentials.json 读取 %d 个凭证\n", len(creds))
				}
			}
		}

		// 2. 读取 auth_*.json 单个文件
		pattern := filepath.Join(dir, "auth_*.json")
		files, _ := filepath.Glob(pattern)

		authCount := 0
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				fmt.Printf("  \033[1;31m读取失败: %s\033[0m\n", file)
				errorCount++
				continue
			}

			var cred Credential
			if err := json.Unmarshal(data, &cred); err != nil {
				fmt.Printf("  \033[1;31m解析失败: %s\033[0m\n", file)
				errorCount++
				continue
			}

			email := getEmail(cred)
			if email == "" {
				continue
			}

			if _, exists := credentialsMap[email]; exists {
				duplicateCount++
			} else {
				credentialsMap[email] = cred
				authCount++
			}
		}
		if authCount > 0 {
			fmt.Printf("  从 auth_*.json 读取 %d 个凭证\n", authCount)
		}
	}

	// 转换为切片
	var credentials []Credential
	for _, cred := range credentialsMap {
		credentials = append(credentials, cred)
	}

	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("  统计结果")
	fmt.Println("==================================================")
	fmt.Println()
	fmt.Printf("读取凭证总数: %d\n", len(credentials)+duplicateCount)
	fmt.Printf("去重后数量: %d\n", len(credentials))
	if duplicateCount > 0 {
		fmt.Printf("\033[1;33m重复凭证: %d\033[0m\n", duplicateCount)
	}
	if errorCount > 0 {
		fmt.Printf("\033[1;31m读取错误: %d\033[0m\n", errorCount)
	}
	fmt.Println()

	if len(credentials) == 0 {
		fmt.Println("没有凭证需要合并")
		return
	}

	// 创建目标目录
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		fmt.Printf("\033[1;31m创建目录失败: %s - %v\033[0m\n", targetDir, err)
		os.Exit(1)
	}

	// 写入 openai_credentials.json
	credFile := filepath.Join(targetDir, "openai_credentials.json")
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		fmt.Printf("\033[1;31m序列化失败: %v\033[0m\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(credFile, data, 0644); err != nil {
		fmt.Printf("\033[1;31m写入失败: %s - %v\033[0m\n", credFile, err)
		os.Exit(1)
	}
	fmt.Printf("✅ 已写入: %s (%d 个凭证)\n", credFile, len(credentials))

	// 写入 auth_*.json 单个文件
	for _, cred := range credentials {
		email := getEmail(cred)
		if email == "" {
			continue
		}

		// 从 email 提取文件名前缀
		parts := strings.Split(email, "@")
		if len(parts) < 2 {
			continue
		}
		prefix := parts[0]

		authFile := filepath.Join(targetDir, fmt.Sprintf("auth_%s.json", prefix))

		data, err := json.MarshalIndent(cred, "", "  ")
		if err != nil {
			fmt.Printf("\033[1;31m序列化失败: %s\033[0m\n", email)
			continue
		}

		if err := os.WriteFile(authFile, data, 0644); err != nil {
			fmt.Printf("\033[1;31m写入失败: %s\033[0m\n", authFile)
			continue
		}
	}
	fmt.Printf("✅ 已写入 %d 个 auth_*.json 文件\n", len(credentials))

	fmt.Println()
	fmt.Println("完成！")
}
