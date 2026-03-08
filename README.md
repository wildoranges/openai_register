# OpenAI 注册机

批量注册 OpenAI/ChatGPT 账号并部署 API 代理服务的自动化工具。

## 功能特性

- 🔄 **批量注册** - 自动批量注册 OpenAI/ChatGPT 账号
- 📧 **临时邮箱** - 自动获取临时邮箱（chatgpt.org.uk API）
- 🛡️ **Cloudflare 绕过** - 自动处理 Cloudflare 验证（代理 + 隐蔽脚本）
- 🔐 **OTP 自动处理** - 自动解析验证码邮件
- 📝 **多格式输出** - 导出 JSON、TXT、CodeX CLI 等多种凭证格式
- 🔗 **CLIProxyAPI 集成** - 一键转换凭证格式，对接 API 代理服务

## 目录

- [环境准备](#环境准备)
- [分支说明](#分支说明)
- [快速开始](#快速开始)
- [第一部分：OpenAI 账号注册机](#第一部分openai-账号注册机)
- [第二部分：凭证格式转换](#第二部分凭证格式转换)
- [第三部分：CLIProxyAPI 部署](#第三部分cliproxyapi-部署)
- [常见问题](#常见问题)

---

## 环境准备

### 安装 Go

本项目需要 Go 1.24+，推荐使用 Go 1.26。

#### 方法一：手动安装官方 tar.gz（推荐）

```bash
# 下载 Go 1.26 (amd64)
wget https://go.dev/dl/go1.26.0.linux-amd64.tar.gz

# 解压到 /usr/local或你指定的其他目录
# 解压前请确保/usr/local目录下没有旧版本的 go 文件夹
tar -C /usr/local -xzf go1.26.0.linux-amd64.tar.gz

# 添加到 PATH (添加到 ~/.bashrc 或 ~/.zshrc)
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# 验证安装
go version
```

#### 方法二：Go 官方多版本管理

```bash
# 先安装一个 Go 版本（作为引导）
apt install -y golang-go  # 通常为较旧版本

# 安装特定版本
go install golang.org/dl/go1.26.0@latest
go1.26.0 download

# 使用
go1.26.0 version

# 设置为默认（可选）
alias go=go1.26.0
```

> **提示**：方法一安装的是最新版本，方法二适合需要管理多个 Go 版本的开发者。

### 安装 Chrome

```bash
# Debian/Ubuntu
wget https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
dpkg -i google-chrome-stable_current_amd64.deb
apt --fix-broken install
```

### 安装 xvfb（无头模式运行需要）

```bash
# Debian/Ubuntu
apt install -y xvfb

# CentOS/RHEL
yum install -y xorg-x11-server-Xvfb
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

# 构建凭证格式转换工具
go build -o convert_to_cliproxy ./cmd/convert
```

### 3. 配置文件

```bash
# 复制配置模板
cp config.json.example config.json

# 编辑配置文件（填写代理地址，凭证输出路径等）
# 推荐配置代理来使用，避免region限制和Cloudflare验证失败
vim config.json
```

### 4. 注册账号

```bash
# 运行注册（5 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5
```

### 5. 转换凭证

```bash
# 转换为CLIProxyAPI兼容格式
./convert_to_cliproxy
```

### 6. 启动 API 代理（使用[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)）

```bash
# 下载并编译 CLIProxyAPI
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

### 7. 测试 API

```bash
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-5.2", "messages": [{"role": "user", "content": "Hello!"}]}'
```

---

## 第一部分：OpenAI 账号注册机

### 关于 Refresh Token

> ⚠️ **重要说明**
>
> 当前 `refresh_token` 暂时无法获取，因此获取的凭证只有 `access_token`。
>
> - `access_token` 有效期约为 **10 天**，过期后需要重新注册账号
> - 建议定期批量注册新账号，保持可用凭证池
> - 本项目默认分支为 `no_refresh`（稳定分支）
>

### 1.1 系统要求

- Go 1.24+
- Chrome/Chromium 浏览器
- 网络连接（建议使用代理并在 config.json 中配置）
- Linux 环境（推荐使用 xvfb-run 运行无头模式）

### 1.2 配置文件

首先复制配置模板：

```bash
cp config.json.example config.json
```

配置文件 `config.json`：

```json
{
  "proxy": "http://proxy-host:port",
  "headless": true,
  "timeout": 600,
  "debug": false,
  "output_dir": "creds",
  "count": 1
}
```

| 参数 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `proxy` | string | 代理地址（支持认证） | 空 |
| `headless` | bool | 无头模式 | true |
| `timeout` | int | 超时时间（秒） | 600 |
| `debug` | bool | 调试模式 | false |
| `output_dir` | string | 输出目录 | creds |
| `count` | int | 注册账号数量 | 1 |

```bash
# 构建
go build -o openai-register .

# 运行（注册 N 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register N

# 示例：注册 5 个账号
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# OAuth 模式（获取 refresh_token）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register --oauth 5

# 显示浏览器窗口（调试用）
./openai-register --head 1

# 模拟模式（生成测试数据）
./openai-register --sim 5

# 使用配置文件中的数量
./openai-register
```

**命令行参数：**

| 参数 | 说明 |
|------|------|
| `--oauth` | OAuth 模式，获取 access_token + refresh_token |
| `--head` | 显示浏览器窗口（调试用） |
| `--sim` | 模拟模式，生成测试数据 |
| `--debug` | 调试模式，保存截图 |
注册完成后，凭证保存在 `creds/` 目录：

| 文件 | 格式 | 说明 |
|------|------|------|
| `openai_credentials.json` | JSON | 完整凭证数组，包含 email、password、access_token、refresh_token |
| `openai_tokens.txt` | TEXT | 环境变量格式，每行一个 `OPENAI_ACCESS_TOKEN=xxx` |
| `auth_*.json` | JSON | CodeX CLI 格式，每个账号一个文件 |

### 1.5 临时邮箱服务

本项目使用 GPTMail (chatgpt.org.uk) 作为临时邮箱服务：

| 项目 | 说明 |
|------|------|
| **API Key** | `YOUR_API_KEY`（公共测试 Key） |
| **每日配额** | 20 万次（全球共享） |
| **配额重置** | UTC 0:00（北京时间上午 8:00） |

**注意事项：**
- 公共 API Key `YOUR_API_KEY` 配额全球共享，可能用完
- 配额用完时返回 `Daily quota exceeded`
- 可购买专属 API Key：[LDC Store](https://shop.chatgpt.org.uk/buy/prod_1768420938389)

### 1.6 工作流程

**普通模式：**
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
7. 处理 "about-you" 页面
        ↓
8. 完成注册，提取 access_token
        ↓
9. 保存凭证到文件
```

**OAuth 模式（`--oauth`）：**
```
1. 获取临时邮箱地址（chatgpt.org.uk API）
        ↓
2. 启动本地 OAuth 回调服务器（端口 1455）
        ↓
3. 生成 PKCE 代码（code_verifier + code_challenge）
        ↓
4. 访问 OpenAI OAuth 授权页面
        ↓
5. 通过 Cloudflare 验证
        ↓
6. 填写邮箱、密码，输入 OTP
        ↓
7. 同意 OAuth 授权（consent 页面）
        ↓
8. 本地回调服务器收到 authorization code
        ↓
9. 用 code 兑换 access_token + refresh_token
        ↓
10. 保存凭证到文件
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

构建：

```bash
go build -o convert_to_cliproxy ./cmd/convert
```

运行：

```bash
# 运行转换工具（使用默认路径）
./convert_to_cliproxy

# 指定输入输出路径
./convert_to_cliproxy creds/openai_credentials.json ~/.cli-proxy-api
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

---

## 第三部分：CLIProxyAPI 部署

CLIProxyAPI 是一个功能强大的 API 代理服务，提供 OpenAI 兼容的 API 接口。

**GitHub**: https://github.com/router-for-me/CLIProxyAPI

**文档**: https://help.router-for.me/

### 快速部署

```bash
# 克隆项目
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI

# 构建
go build -o cliproxyapi ./cmd/server

# 配置
cat > config.yaml << 'EOF'
host: ""
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-your-api-key"
debug: false
EOF

# 启动
./cliproxyapi -config config.yaml
```

> 详细配置、API 使用方法、模型列表等请参考 [CLIProxyAPI 文档](https://help.router-for.me/)

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

### Q2: 临时邮箱获取失败或502 Bad Gateway

**原因：** chatgpt.org.uk API 不可用

**解决方案：**
1. 检查网络连接
2. 尝试使用/更换代理
3. 检查 API 是否返回错误信息
4. 等待一段时间后重试

### Q3: access_token 过期

**原因：** JWT 令牌有过期时间

**解决方案：**
1. 定期重新注册新账号

### Q4: CLIProxyAPI 无法加载凭证

**原因：** 凭证文件格式错误或位置不对

**解决方案：**
1. 检查文件位置：`ls ~/.cli-proxy-api/`
2. 验证 JSON 格式：`python -m json.tool ~/.cli-proxy-api/codex-*.json`
3. 确保 `type` 字段为 `codex`

---

## 相关链接

- CLIProxyAPI GitHub: https://github.com/router-for-me/CLIProxyAPI
- CLIProxyAPI 文档: https://help.router-for.me/
- OpenAI API 文档: https://platform.openai.com/docs/api-reference
