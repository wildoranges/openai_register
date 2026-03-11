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

// CredentialsArray 凭证数组
type CredentialsArray []Credential

// getEmail 从凭证中获取 email
func getEmail(cred Credential) string {
	if v, ok := cred["email"].(string); ok {
		return v
	}
	return ""
}

// getRefreshToken 从凭证中获取 refresh_token
func getRefreshToken(cred Credential) string {
	if v, ok := cred["refresh_token"].(string); ok {
		return v
	}
	return ""
}

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		fmt.Println("用法: ./cleanup_no_refresh <目录1> [目录2] ...")
		fmt.Println("示例: ./cleanup_no_refresh creds creds_refresh")
		fmt.Println("")
		fmt.Println("选项:")
		fmt.Println("  --execute, -e    执行删除（默认只预览）")
		os.Exit(1)
	}

	// 解析参数
	dryRun := true
	var directories []string

	for _, arg := range args {
		if arg == "--execute" || arg == "-e" {
			dryRun = false
		} else {
			directories = append(directories, arg)
		}
	}

	if len(directories) == 0 {
		fmt.Println("错误: 未指定目录")
		os.Exit(1)
	}

	// 统计
	totalFiles := 0
	withRefresh := 0
	noRefresh := 0
	var filesToDelete []struct {
		path  string
		email string
	}
	var jsonFilesToUpdate []struct {
		path       string
		removed    int
		kept       int
		removedMsg []string
	}

	fmt.Println("==================================================")
	fmt.Println("  清理无 refresh_token 的凭证")
	fmt.Println("==================================================")
	fmt.Println()
	if dryRun {
		fmt.Println("模式: 预览模式（不删除）")
	} else {
		fmt.Println("模式: 执行模式（将删除）")
	}
	fmt.Printf("目录: %s\n", strings.Join(directories, ", "))
	fmt.Println()

	// 遍历所有目录
	for _, dir := range directories {
		// 检查目录是否存在
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			fmt.Printf("\033[1;33m警告: 目录不存在 %s\033[0m\n", dir)
			continue
		}

		// 1. 处理 auth_*.json 单个文件
		pattern := filepath.Join(dir, "auth_*.json")
		files, _ := filepath.Glob(pattern)

		for _, file := range files {
			totalFiles++

			data, err := os.ReadFile(file)
			if err != nil {
				fmt.Printf("\033[1;33m? 读取失败\033[0m %s: %v\n", file, err)
				continue
			}

			var cred Credential
			if err := json.Unmarshal(data, &cred); err != nil {
				fmt.Printf("\033[1;33m? 解析失败\033[0m %s: %v\n", file, err)
				continue
			}

			email := getEmail(cred)
			if email == "" {
				email = "unknown"
			}

			if getRefreshToken(cred) == "" {
				noRefresh++
				filesToDelete = append(filesToDelete, struct {
					path  string
					email string
				}{file, email})
				fmt.Printf("  \033[0;31m✗ 无刷新令牌\033[0m %s (auth_*.json)\n", email)
			} else {
				withRefresh++
			}
		}

		// 2. 处理 openai_credentials.json 汇总文件
		credFile := filepath.Join(dir, "openai_credentials.json")
		if _, err := os.Stat(credFile); err == nil {
			data, err := os.ReadFile(credFile)
			if err != nil {
				fmt.Printf("\033[1;33m? 读取失败\033[0m %s: %v\n", credFile, err)
			} else {
				var creds CredentialsArray
				if err := json.Unmarshal(data, &creds); err != nil {
					fmt.Printf("\033[1;33m? 解析失败\033[0m %s: %v\n", credFile, err)
				} else {
					var keptCreds CredentialsArray
					var removedMsg []string
					removed := 0

					for _, cred := range creds {
						if getRefreshToken(cred) == "" {
							removed++
							email := getEmail(cred)
							if email == "" {
								email = "unknown"
							}
							removedMsg = append(removedMsg, email)
							fmt.Printf("  \033[0;31m✗ 无刷新令牌\033[0m %s (openai_credentials.json)\n", email)
						} else {
							keptCreds = append(keptCreds, cred)
						}
					}

					if removed > 0 {
						noRefresh += removed
						withRefresh += len(keptCreds)
						jsonFilesToUpdate = append(jsonFilesToUpdate, struct {
							path       string
							removed    int
							kept       int
							removedMsg []string
						}{credFile, removed, len(keptCreds), removedMsg})
					} else {
						withRefresh += len(creds)
					}
				}
			}
		}
	}

	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("  统计结果")
	fmt.Println("==================================================")
	fmt.Println()
	fmt.Printf("总计检查: %d 个凭证\n", withRefresh+noRefresh)
	fmt.Printf("\033[0;32m有刷新令牌: %d\033[0m\n", withRefresh)
	fmt.Printf("\033[0;31m无刷新令牌: %d\033[0m\n", noRefresh)
	fmt.Println()

	// 显示将删除/更新的文件
	if len(filesToDelete) > 0 || len(jsonFilesToUpdate) > 0 {
		fmt.Println("==================================================")
		if dryRun {
			fmt.Println("  将处理的文件")
		} else {
			fmt.Println("  已处理的文件")
		}
		fmt.Println("==================================================")

		if len(filesToDelete) > 0 {
			fmt.Printf("\n删除 auth_*.json 文件 (%d 个):\n", len(filesToDelete))
			for i, f := range filesToDelete {
				if i < 10 {
					fmt.Printf("  %s\n", f.path)
				}
			}
			if len(filesToDelete) > 10 {
				fmt.Printf("  ... 还有 %d 个\n", len(filesToDelete)-10)
			}
		}

		if len(jsonFilesToUpdate) > 0 {
			fmt.Printf("\n更新 openai_credentials.json 文件 (%d 个):\n", len(jsonFilesToUpdate))
			for _, j := range jsonFilesToUpdate {
				fmt.Printf("  %s (移除 %d, 保留 %d)\n", j.path, j.removed, j.kept)
			}
		}
		fmt.Println()
	}

	// 执行删除或提示
	if dryRun {
		fmt.Println("==================================================")
		fmt.Println("  预览模式 - 未删除任何文件")
		fmt.Println("==================================================")
		fmt.Println()
		fmt.Println("要执行删除，运行:")
		fmt.Printf("  ./cleanup_no_refresh %s --execute\n", strings.Join(directories, " "))
	} else {
		// 实际执行删除
		deleted := 0

		// 删除单个 auth_*.json 文件
		for _, f := range filesToDelete {
			if err := os.Remove(f.path); err != nil {
				fmt.Printf("  删除失败: %s - %v\n", f.path, err)
			} else {
				deleted++
				fmt.Printf("  已删除: %s\n", f.path)
			}
		}

		// 更新 openai_credentials.json 文件
		for _, j := range jsonFilesToUpdate {
			// 重新读取并过滤
			data, _ := os.ReadFile(j.path)
			var creds CredentialsArray
			json.Unmarshal(data, &creds)

			var keptCreds CredentialsArray
			for _, cred := range creds {
				if getRefreshToken(cred) != "" {
					keptCreds = append(keptCreds, cred)
				}
			}

			// 写回文件
			newData, err := json.MarshalIndent(keptCreds, "", "  ")
			if err != nil {
				fmt.Printf("  序列化失败: %s - %v\n", j.path, err)
				continue
			}

			if err := os.WriteFile(j.path, newData, 0644); err != nil {
				fmt.Printf("  写入失败: %s - %v\n", j.path, err)
			} else {
				deleted += j.removed
				fmt.Printf("  已更新: %s (移除 %d 个凭证)\n", j.path, j.removed)
			}
		}

		fmt.Println()
		fmt.Printf("已处理 %d 个凭证\n", deleted)
	}

	fmt.Println()
	fmt.Println("完成！")
}
