# OpenAI 注册机

批量注册 OpenAI/ChatGPT 账号并部署 API 代理服务的自动化工具。

## 功能特性

- 🔄 **批量注册** - 自动批量注册 OpenAI/ChatGPT 账号
- 📧 **临时邮箱** - 自动获取临时邮箱（支持 API 和 WebMail 两种模式）
- 🛡️ **Cloudflare 绕过** - 自动处理 Cloudflare 验证（代理 + 隐蔽脚本）
- 🔐 **OTP 自动处理** - 自动解析验证码邮件
- 📝 **多格式输出** - 导出 JSON、TXT、CodeX CLI 等多种凭证格式
- 🔗 **CLIProxyAPI 集成** - 一键转换凭证格式，对接 API 代理服务

## 目录

- [环境准备](#环境准备)
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

# 构建凭证清理工具
go build -o cleanup_no_refresh ./cmd/cleanup_no_refresh

# 构建凭证合并工具
go build -o merge_credentials ./cmd/merge_credentials
```
### 3. 配置文件

```bash
# 复制配置模板
cp config.json.example config.json

# 编辑配置文件（填写代理地址、输出目录等）
# 推荐配置代理来使用，避免region限制和Cloudflare验证失败
vim config.json
```

### 4. 注册账号

```bash
# 运行注册（5 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# 使用 WebMail 模式
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register --webmail 5
```

### 5. 转换凭证

```bash
# 手动转换为 CLIProxyAPI 兼容格式
./convert_to_cliproxy
```

> 如果 `config.json` 中设置了 `convert_dir`，注册成功后会自动额外输出一份 CLIProxyAPI 格式凭证到该目录。

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

> ✅ **已支持**
>
> 本项目使用 OAuth PKCE 流程，可获取完整的 token set：
>
> - `access_token` - 访问令牌，有效期约 10 天
> - `refresh_token` - 刷新令牌，可用于刷新 access_token
> - `id_token` - 身份令牌
>
> **建议：** 定期批量注册新账号，保持可用凭证池
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
  "timeout": 60,
  "debug": false,
  "output_dir": "creds",
  "convert_dir": "~/.cli-proxy-api",
  "count": 1
}
```

| 参数 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `proxy` | string | 代理地址（支持认证） | 空 |
| `proxies` | []string | 代理地址列表（多代理轮换） | 空 |
| `headless` | bool | 无头模式 | true |
| `timeout` | int | 超时时间（秒） | 60 |
| `debug` | bool | 调试模式 | false |
| `output_dir` | string | 输出目录 | creds |
| `convert_dir` | string | 额外输出 CLIProxyAPI 格式凭证的目录 | ~/.cli-proxy-api |
| `count` | int | 注册账号数量 | 1 |
| `clash` | object | Clash / Mihomo 代理池配置（可选） | 空 |

#### 代理配置详解

**为什么需要代理？**
- 绕过地区限制（OpenAI 在某些地区不可用）
- 避免 Cloudflare 验证失败（IP 被 Cloudflare 标记为可疑时会导致注册失败）
- 提高注册成功率

**代理格式：**

```
协议://用户名:密码@代理服务器地址:端口
```

**支持的协议：**
- `http://` - HTTP 代理
- `https://` - HTTPS 代理（推荐）
- `socks5://` - SOCKS5 代理

**单代理配置：**

```json
{
  "proxy": "http://proxy-host:port"
}
```

**多代理轮换配置：**

当配置多个静态代理时，系统会先启动探活，随后在可用代理之间按顺序轮换，适用于需要大量注册的场景：

```json
{
  "proxies": [
    "http://proxy-host:port",
    "http://proxy-host:port",
    "socks5://user3:pass3@proxy3.example.com:1080"
  ]
}
```

**Clash 代理池配置：**

如果你本机或远端已经运行 Clash / Mihomo，并暴露了 External Controller，可以直接读取指定代理组中的节点构建代理池。程序会：

1. 通过 `external_controller` 读取 `proxy_group` 下的节点列表
2. 按 `include` / `exclude` 对节点名做关键字过滤
3. 启动时逐个切换并探活，只保留可用节点
4. 注册时在可用节点之间轮换；某个节点在注册过程中出现网络类错误时，会自动标记为失败并跳过

```json
{
  "headless": true,
  "timeout": 600,
  "debug": false,
  "output_dir": "creds",
  "convert_dir": "~/.cli-proxy-api",
  "count": 5,
  "clash": {
    "external_controller": "http://127.0.0.1:9090",
    "secret": "",
    "proxy_group": "US",
    "mixed_proxy": "http://127.0.0.1:7890",
    "include": ["美国"],
    "exclude": ["DIRECT", "REJECT"]
  }
}
```

**Clash 参数说明：**
- `external_controller`：Clash / Mihomo API 地址，支持 `http://host:port`
- `secret`：External Controller 的访问密钥，没有可留空
- `proxy_group`：要轮换的代理组名称，例如 `US`、`Proxy`、`GLOBAL`
- `mixed_proxy`：真正承载流量的统一代理入口，必须是完整 URL；若你的入口带认证，也直接写完整认证 URL
- `include`：仅保留节点名中包含任一关键字的节点，例如 `美国`、`HK`、`住宅`
- `exclude`：排除节点名中包含关键字的节点，通常建议排掉 `DIRECT`、`REJECT`

> **注意：** `mixed_proxy` 用于实际网络流量，`external_controller` 只负责切换节点；两者不是同一个概念。
>
> **补充说明：** 如果运行过程中所有已分配过的节点都暂时失败，程序会自动把“运行期失败”的节点重新激活一轮再继续尝试；但启动探活阶段就失败的节点不会被自动复活。

**无需认证的代理：**

```json
{
  "proxy": "http://proxy.example.com:8080"
}
```

**代理选择优先级：**
1. 如果配置了 `proxies`（数组），系统会优先使用静态代理池
2. 如果未配置 `proxies`，则使用 `proxy`（单个代理）
3. 如果前两者都未配置，但配置了 `clash`，则使用 Clash 代理池
4. 如果以上都未配置，则直连（不推荐）

> **注意：** `proxies` 和 `proxy` 的优先级都高于 `clash`。如果它们同时存在，程序会忽略 `clash` 配置。

**推荐配置：**
- 使用干净的住宅代理 IP，避免数据中心 IP 被 Cloudflare 标记
- 确保代理支持 HTTPS
- 如果使用多代理，建议配置 3-5 个代理进行轮换
- 如果使用 Clash，先确认 `proxy_group` 在 `/proxies` API 中真实存在，再配置 `include` / `exclude` 过滤

```bash
# 构建
go build -o openai-register .

# Linux 服务器（无 GUI）：需要 xvfb-run
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# Linux 桌面版：直接运行
./openai-register 5

# 显示浏览器窗口（调试用）
./openai-register --head 1

# 模拟模式（生成测试数据）
./openai-register --sim 5

# 使用配置文件中的数量
./openai-register

# 指定配置文件路径
./openai-register --config /path/to/config.json 5
```

> **说明：** `xvfb-run` 为无 GUI 的 Linux 服务器提供虚拟 X server。Linux 桌面版可直接运行。

### 1.4 定时任务

项目提供了定时注册脚本 `register_cron.sh`，可配合 crontab 实现每日自动注册。

**配置：**

1. 修改 `config_cron.json` 设置注册数量等参数
2. 安装 crontab：

```bash
# 编辑 crontab
crontab -e

# 添加以下内容（每天北京时间 8:05 和 20:05 执行）
# 系统时区: Asia/Shanghai (UTC+8)
5 8,20 * * * /path/to/register_cron.sh
```

**说明：**
- `config_cron.json` 是定时任务专用配置，不会覆盖 `config.json`
- 日志保存在 `logs/` 目录，自动清理 7 天前的日志
- 如果配置了 `convert_dir`，注册完成后会自动转换为 CLIProxyAPI 格式并输出到该目录
- 自动清理无 `refresh_token` 的凭证

**命令行参数：**

| 参数 | 说明 |
|------|------|
| `--config` | 指定配置文件路径，默认 `./config.json` |
| `--head` | 显示浏览器窗口（调试用） |
| `--sim` | 模拟模式，生成测试数据 |
| `--debug` | 调试模式，保存截图 |
| `--webmail` | 使用 WebMail 模式获取临时邮箱（绕过 API 配额限制） |

> **说明：** 默认使用 OAuth PKCE 模式，获取 `access_token` + `refresh_token`

注册完成后，凭证默认保存在 `output_dir`（默认 `creds/`）目录：

| 文件 | 格式 | 说明 |
|------|------|------|
| `openai_credentials.json` | JSON | 完整凭证数组，按 email 去重更新 |
| `openai_tokens.txt` | TEXT | 环境变量格式文本输出 |
| `auth_*.json` | JSON | 本项目的单账号输出格式，文件名使用邮箱前缀 |

如果配置了 `convert_dir`（默认 `~/.cli-proxy-api`），每次注册成功后还会额外输出：

| 文件 | 格式 | 说明 |
|------|------|------|
| `codex-<email>.json` | JSON | CLIProxyAPI 使用的单账号凭证格式 |

### 1.5 临时邮箱服务

本项目使用 GPTMail (chatgpt.org.uk) 作为临时邮箱服务，支持两种模式：

#### API 模式（默认）

| 项目 | 说明 |
|------|------|
| **API Key** | `YOUR_API_KEY`（公共测试 Key） |
| **每日配额** | 20 万次（全球共享） |
| **配额重置** | UTC 0:00（北京时间上午 8:00） |

**注意事项：**
- 公共 API Key `YOUR_API_KEY` 配额全球共享，可能用完
- 配额用完时返回 `Daily quota exceeded`
- 可购买专属 API Key：[LDC Store](https://shop.chatgpt.org.uk/buy/prod_1768420938389)

#### WebMail 模式（推荐）

当 API 配额用完、公共配额返回 `Daily quota exceeded`，或者你希望邮箱获取与 OTP 检查都走浏览器时，可使用 WebMail 模式：

```bash
# 使用 WebMail 模式（通过浏览器获取临时邮箱）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register --webmail 5

# 配合自定义配置文件（例如 Clash 代理池）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register --webmail --config ./config.json 5
```

**WebMail 模式优势：**
- 绕过 API 配额限制
- 同时支持获取邮箱和检查验证码
- 更稳定可靠

**已验证的运行特性：**
- WebMail 模式可以与单代理、静态代理池、Clash 代理池一起使用
- 如果某次尝试分配到了显式代理，该代理会同时用于浏览器启动和 OAuth token 兑换
- 当注册阶段返回 `registration_disallowed` 但账号已存在时，程序会自动回退到登录流程并继续获取凭证

### 1.6 工作流程

```
1. 获取临时邮箱地址（API 或 WebMail 模式）
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
10. 保存凭证到 `output_dir`
        ↓
11. 如配置了 `convert_dir`，额外转换并输出到 CLIProxyAPI 目录
```
---

## 第二部分：凭证格式转换

### 2.1 自动转换与手动转换

项目现在支持两种方式输出 CLIProxyAPI 凭证：

1. **自动转换**：在 `config.json` 中设置 `convert_dir`
2. **手动转换**：运行 `./convert_to_cliproxy`

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



### 2.3 转换后的凭证文件格式

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

### 2.4 清理无刷新令牌的凭证

可使用清理工具删除没有 `refresh_token` 的凭证：

```bash
# 预览模式（不删除，只显示）
./cleanup_no_refresh creds creds_refresh

# 执行删除
./cleanup_no_refresh creds creds_refresh --execute
```

### 2.5 合并多个目录的凭证

将多个目录的凭证合并到一个目录，按 email 去重：

```bash
# 合并 creds 和 creds_refresh 到 creds_merged
./merge_credentials creds_merged creds creds_refresh

# 查看帮助
./merge_credentials
```

**说明：**
- 读取所有源目录的 `auth_*.json` 和 `openai_credentials.json`
- 根据 `email` 字段去重，相同 email 时保留先读到的那份
- 输出到目标目录的 `openai_credentials.json` 和 `auth_*.json`

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

**原因：** chatgpt.org.uk API 不可用或配额用完

**解决方案：**
1. 使用 WebMail 模式：`./openai-register --webmail 5`
2. 检查网络连接
3. 尝试使用/更换代理
4. 等待一段时间后重试

### Q3: CLIProxyAPI 无法加载凭证

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
