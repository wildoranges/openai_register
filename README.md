# OpenAI 账号注册与 API 代理完整指南

本指南涵盖从 OpenAI 账号注册到部署 API 代理服务的完整流程。

## 目录

- [项目概述](#项目概述)
- [快速开始](#快速开始)
- [第一部分：OpenAI 账号注册机](#第一部分openai-账号注册机)
- [第二部分：凭证格式转换](#第二部分凭证格式转换)
- [第三部分：CLIProxyAPI 部署](#第三部分cliproxyapi-部署)
- [第四部分：API 使用方法](#第四部分api-使用方法)
- [常见问题](#常见问题)

---

## 项目概述

### 架构图

```
┌─────────────────────┐     ┌─────────────────────┐     ┌─────────────────────┐
│   OpenAI 注册机      │────▶│   凭证格式转换        │────▶│   CLIProxyAPI       │
│   (openai-register) │     │   (convert_to_cliproxy) │   │   (API 代理服务)    │
└─────────────────────┘     └─────────────────────┘     └─────────────────────┘
         │                           │                           │
         ▼                           ▼                           ▼
   批量注册账号              格式化凭证文件              提供 OpenAI 兼容 API
   获取 access_token         导入代理服务               支持多账号负载均衡
```

### 文件结构

```
openai_register/             # 本项目根目录
├── main.go                 # 主程序源码
├── openai-register         # 编译后的二进制
├── config.json             # 配置文件
├── cmd/
│   └── convert/
│       └── main.go         # 转换工具源码
├── convert_to_cliproxy     # 转换工具二进制
├── README.md               # 本文档
└── creds/                  # 凭证输出目录
    ├── openai_credentials.json   # 完整凭证 JSON
    ├── openai_tokens.txt         # 环境变量格式
    └── auth_*.json               # CodeX CLI 格式

~/.cli-proxy-api/           # CLIProxyAPI 凭证存储目录
├── codex-user1@example.com.json
├── codex-user2@example.com.json
└── ...
```

---

## 快速开始

### 1. 克隆项目

```bash
git clone <project_url>
cd openai_register
```

### 2. 构建工具

```bash
# 构建注册机
go build -o openai-register .

# 构建转换工具
go build -o convert_to_cliproxy ./cmd/convert
```

### 3. 注册账号

```bash
# 编辑配置文件
vim config.json

# 运行注册（5 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5
```

### 4. 转换凭证

```bash
./convert_to_cliproxy
```

### 5. 启动 API 代理

```bash
# 下载并配置 CLIProxyAPI
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cliproxyapi ./cmd/server

# 创建配置文件
cat > config.yaml << 'EOF'
host: ""
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-your-api-key"
debug: false
EOF

# 启动服务
./cliproxyapi -config config.yaml
```

### 6. 测试 API

```bash
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-5", "messages": [{"role": "user", "content": "Hello!"}]}'
```

---

## 第一部分：OpenAI 账号注册机

### 1.1 功能特性

- 批量注册 OpenAI/ChatGPT 账号
- 自动获取临时邮箱（chatgpt.org.uk API）
- 自动处理 Cloudflare 验证（代理 + 隐蔽脚本）
- 自动处理 OTP 验证码
- 自动填写用户信息页面
- 导出多种格式的凭证

### 1.2 系统要求

- Go 1.21+
- Chrome/Chromium 浏览器
- 网络连接（建议使用代理）
- Linux 环境（推荐使用 xvfb-run 运行无头模式）

### 1.3 配置文件

配置文件 `config.json`：

```json
{
  "proxy": "http://proxy-host:port",
  "headless": true,
  "timeout": 60,
  "debug": false,
  "output_dir": "creds",
  "count": 1
}
```

| 参数 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `proxy` | string | 代理地址（支持认证） | 空 |
| `headless` | bool | 无头模式 | true |
| `timeout` | int | 超时时间（秒） | 60 |
| `debug` | bool | 调试模式 | false |
| `output_dir` | string | 输出目录 | creds |
| `count` | int | 注册账号数量 | 1 |

### 1.4 构建与运行

```bash
# 构建
go build -o openai-register .

# 运行（注册 N 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register N

# 示例：注册 5 个账号
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# 显示浏览器窗口（调试用）
./openai-register --head 1

# 模拟模式（生成测试数据）
./openai-register --sim 5

# 使用配置文件中的数量
./openai-register
```

### 1.5 输出文件

注册完成后，凭证保存在 `creds/` 目录：

| 文件 | 格式 | 说明 |
|------|------|------|
| `openai_credentials.json` | JSON | 完整凭证数组，包含 email、password、access_token 等 |
| `openai_tokens.txt` | TEXT | 环境变量格式，每行一个 `OPENAI_ACCESS_TOKEN=xxx` |
| `auth_*.json` | JSON | CodeX CLI 格式，每个账号一个文件 |

#### openai_credentials.json 格式

```json
[
  {
    "email": "user@example.com",
    "password": "GeneratedPassword123!",
    "access_token": "eyJhbGciOiJSUzI1NiIs...",
    "session_id": "",
    "user_id": "user-xxxxxxxx",
    "created_at": "2026-03-05T00:00:00Z"
  }
]
```

### 1.6 工作流程

```
1. 获取临时邮箱地址（chatgpt.org.uk API）
        ↓
2. 生成随机密码
        ↓
3. 访问 OpenAI 注册页面
        ↓
4. 通过 Cloudflare 验证（代理 + 隐蔽脚本）
        ↓
5. 填写邮箱和密码
        ↓
6. 等待并解析 OTP 验证码邮件
        ↓
7. 处理 "about-you" 页面（通过 API 调用）
        ↓
8. 完成注册，提取 access_token
        ↓
9. 保存凭证到文件
```

---

## 第二部分：凭证格式转换

### 2.1 为什么需要转换？

OpenAI 注册机输出的凭证格式与 CLIProxyAPI 要求的格式不同：

| 项目 | 注册机输出 | CLIProxyAPI 要求 |
|------|------------|------------------|
| 文件格式 | 单个 JSON 数组 | 每个账号一个 JSON 文件 |
| 字段名称 | `access_token` | `access_token` + `id_token` + `refresh_token` |
| 元数据 | 基础信息 | 需要 `type`, `account_id`, `expired` 等 |

### 2.2 使用转换工具

本目录包含 `convert_to_cliproxy` 工具，用于将凭证转换为 CLIProxyAPI 格式。

```bash
# 运行转换工具（使用默认路径）
./convert_to_cliproxy

# 指定输入输出路径
./convert_to_cliproxy creds/openai_credentials.json ~/.cli-proxy-api
```

**构建：**

```bash
go build -o convert_to_cliproxy ./cmd/convert
```

### 2.3 凭证文件格式

转换后的每个文件格式如下：

```json
{
  "id_token": "",
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "",
  "account_id": "b88078ca-b61d-4ced-bb5e-2fbaafd8e73b",
  "last_refresh": "2026-03-05T10:09:29.856156",
  "email": "user@example.com",
  "type": "codex",
  "expired": "2026-03-15T00:09:35"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `id_token` | string | JWT ID Token（可选） |
| `access_token` | string | 访问令牌（必需） |
| `refresh_token` | string | 刷新令牌（可选） |
| `account_id` | string | OpenAI 账户 ID |
| `last_refresh` | string | 上次刷新时间 |
| `email` | string | 账户邮箱 |
| `type` | string | 固定为 "codex" |
| `expired` | string | 令牌过期时间 |

---

## 第三部分：CLIProxyAPI 部署

### 3.1 项目介绍

CLIProxyAPI 是一个功能强大的 API 代理服务，可以将 ChatGPT/Claude/Gemini 等 CLI 工具转换为 OpenAI 兼容的 API 接口。

**GitHub**: https://github.com/router-for-me/CLIProxyAPI

**主要特性：**
- 支持 OpenAI Codex（GPT 模型）OAuth 认证
- 支持 Claude Code OAuth 认证
- 支持 Gemini CLI OAuth 认证
- 多账号负载均衡
- OpenAI 兼容 API 接口
- 流式和非流式响应

### 3.2 安装

#### 方式一：从源码编译

```bash
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cliproxyapi ./cmd/server
```

#### 方式二：下载预编译版本

```bash
# 从 GitHub Releases 下载
# https://github.com/router-for-me/CLIProxyAPI/releases

# Linux amd64
wget https://github.com/router-for-me/CLIProxyAPI/releases/download/v6.8.41/cliproxyapi-linux-amd64
chmod +x cliproxyapi-linux-amd64
mv cliproxyapi-linux-amd64 cliproxyapi
```

### 3.3 配置文件

在 CLIProxyAPI 目录下创建 `config.yaml`：

```yaml
# 服务器配置
host: ""
port: 8317

# 认证目录
auth-dir: "~/.cli-proxy-api"

# API Keys（访问此代理服务需要）
api-keys:
  - "sk-your-api-key"

# 调试模式
debug: false

# 请求重试次数
request-retry: 3

# 路由策略
routing:
  strategy: "round-robin"   # round-robin 或 fill-first

# 代理设置（可选）
# proxy-url: "http://proxy-host:port"
```

### 3.4 导入凭证

```bash
# 确保凭证目录存在
mkdir -p ~/.cli-proxy-api

# 运行转换工具（在本项目目录下）
cd openai_register
./convert_to_cliproxy

# 验证凭证文件
ls -la ~/.cli-proxy-api/
```

### 3.5 启动服务

```bash
# 在 CLIProxyAPI 目录下
cd CLIProxyAPI

# 前台运行
./cliproxyapi -config config.yaml

# 后台运行
nohup ./cliproxyapi -config config.yaml > cliproxyapi.log 2>&1 &
```

### 3.6 验证部署

```bash
# 检查模型列表
curl http://localhost:8317/v1/models \
  -H "Authorization: Bearer YOUR_API_KEY"

# 测试聊天
curl http://localhost:8317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"model": "gpt-5", "messages": [{"role": "user", "content": "Hello!"}]}'
```

---

## 第四部分：API 使用方法

### 4.1 支持的端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/models` | GET | 获取模型列表 |
| `/v1/chat/completions` | POST | 聊天完成 |
| `/v1/completions` | POST | 文本完成 |
| `/v1/responses` | POST | Codex Responses API |
| `/v1/messages` | POST | Claude Messages API |

### 4.2 Python SDK

```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-your-api-key",
    base_url="http://localhost:8317/v1"
)

# 发送请求
response = client.chat.completions.create(
    model="gpt-5",
    messages=[{"role": "user", "content": "Hello!"}]
)

print(response.choices[0].message.content)

# 流式响应
stream = client.chat.completions.create(
    model="gpt-5",
    messages=[{"role": "user", "content": "Tell me a story"}],
    stream=True
)

for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### 4.3 cURL 示例

```bash
# 聊天完成
curl http://localhost:8317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "gpt-5",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# 流式响应
curl http://localhost:8317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "gpt-5",
    "messages": [{"role": "user", "content": "Count to 10"}],
    "stream": true
  }'
```

### 4.4 JavaScript/TypeScript

```javascript
import OpenAI from 'openai';

const client = new OpenAI({
  apiKey: 'REDACTED',
  baseURL: 'http://localhost:8317/v1'
});

async function main() {
  const response = await client.chat.completions.create({
    model: 'gpt-5',
    messages: [{ role: 'user', content: 'Hello!' }]
  });
  
  console.log(response.choices[0].message.content);
}

main();
```

### 4.5 与其他工具集成

#### 配合 Cursor 使用

设置中配置：
- API Base URL: `http://localhost:8317/v1`
- API Key: `sk-your-api-key`

#### 配合 Continue 使用

`~/.continue/config.json`:

```json
{
  "models": [{
    "title": "CLIProxyAPI",
    "provider": "openai",
    "model": "gpt-5",
    "apiBase": "http://localhost:8317/v1",
    "apiKey": "sk-your-api-key"
  }]
}
```

### 4.6 可用模型

| 模型 ID | 说明 |
|---------|------|
| `gpt-5` | GPT-5 主模型 |
| `gpt-5-codex` | GPT-5 Codex 版本 |
| `gpt-5-codex-mini` | GPT-5 Mini 版本 |
| `gpt-5.1` | GPT-5.1 版本 |
| `gpt-5.2` | GPT-5.2 版本 |
| `gpt-5.3-codex` | GPT-5.3 Codex 版本 |

---

## 常见问题

### Q1: 注册时 Cloudflare 验证失败

**原因：** IP 被 Cloudflare 标记为可疑

**解决方案：**
1. 使用干净的代理 IP
2. 确保代理支持 HTTPS
3. 尝试使用 `--head` 参数查看浏览器状态

```bash
./openai-register --head 1
```

### Q2: 临时邮箱获取失败

**原因：** chatgpt.org.uk API 不可用

**解决方案：**
1. 检查网络连接
2. 尝试使用代理
3. 检查 API 是否返回错误信息

### Q3: access_token 过期

**原因：** JWT 令牌有过期时间（通常 7 天）

**解决方案：**
1. 使用 refresh_token 刷新（如果有的话）
2. 重新登录获取新的 access_token
3. 定期重新注册新账号

### Q4: CLIProxyAPI 无法加载凭证

**原因：** 凭证文件格式错误或位置不对

**解决方案：**
1. 检查文件位置：`ls ~/.cli-proxy-api/`
2. 验证 JSON 格式：`python -m json.tool ~/.cli-proxy-api/codex-*.json`
3. 确保 `type` 字段为 `codex`

### Q5: API 返回 401 错误

**原因：** API Key 不正确

**解决方案：**
1. 检查 config.yaml 中的 `api-keys` 配置
2. 确保请求头包含正确的 Authorization

### Q6: 如何实现多账号负载均衡

CLIProxyAPI 自动支持多账号负载均衡：

1. 将多个凭证文件放入 `~/.cli-proxy-api/`
2. 配置 `routing.strategy`:
   - `round-robin`: 轮询（默认）
   - `fill-first`: 优先使用第一个可用账号

---

## 附录：完整部署脚本

```bash
#!/bin/bash
set -e

echo "=== OpenAI 账号注册与 API 代理部署脚本 ==="

# 1. 克隆项目
echo "[1/5] 克隆项目..."
git clone gitlab@222.195.92.204:wildoranges/openai_register.git
cd openai_register

# 2. 构建工具
echo "[2/5] 构建工具..."
go build -o openai-register .
go build -o convert_to_cliproxy ./cmd/convert

# 3. 注册账号
echo "[3/5] 注册 OpenAI 账号..."
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# 4. 转换凭证
echo "[4/5] 转换凭证格式..."
./convert_to_cliproxy

# 5. 启动 CLIProxyAPI
echo "[5/5] 启动 API 代理服务..."
cd ..
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cliproxyapi ./cmd/server

cat > config.yaml << 'EOF'
host: ""
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-your-api-key"
debug: false
EOF

nohup ./cliproxyapi -config config.yaml > /tmp/cliproxyapi.log 2>&1 &
sleep 3

# 验证
curl -s http://localhost:8317/v1/models -H "Authorization: Bearer YOUR_API_KEY" | python3 -m json.tool | head -20

echo ""
echo "=== 部署完成 ==="
echo "API 地址: http://localhost:8317"
echo "API Key: sk-your-api-key"
echo "凭证数量: $(ls ~/.cli-proxy-api/*.json 2>/dev/null | wc -l)"
```

---

## 相关链接

- CLIProxyAPI GitHub: https://github.com/router-for-me/CLIProxyAPI
- CLIProxyAPI 文档: https://help.router-for.me/
- OpenAI API 文档: https://platform.openai.com/docs/api-reference
