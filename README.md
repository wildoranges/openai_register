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

- [分支说明](#分支说明)
- [快速开始](#快速开始)
- [第一部分：OpenAI 账号注册机](#第一部分openai-账号注册机)
- [第二部分：凭证格式转换](#第二部分凭证格式转换)
- [第三部分：CLIProxyAPI 部署](#第三部分cliproxyapi-部署)
- [常见问题](#常见问题)

---

## 分支说明

| 分支 | 说明 |
|------|------|
| `no_refresh` | **稳定分支** - 仅获取 access_token，无 refresh_token 功能 |
| `refresh` | 实验分支 - 包含 iOS OAuth refresh_token 代码（需要 preauth_cookie） |

**注意**: `refresh_token` 获取功能目前受限：
- Device Code Flow 需要 ChatGPT Plus 订阅
- iOS OAuth Flow 需要 preauth_cookie（外部服务已不可用）

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

### 1.1 系统要求

- Go 1.24+
- Chrome/Chromium 浏览器
- 网络连接（建议使用代理并在config.json中配置）
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

### 1.3 构建与运行

```bash
# 构建
go build -o openai-register .

# 运行（注册 N 个账号）
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register N

# 示例：注册 5 个账号
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 1500 ./openai-register 5

# 显示浏览器窗口（调试用）
./openai-register --head 1

# 使用配置文件中的数量
./openai-register
```

### 1.4 输出文件

注册完成后，凭证保存在 `creds/` 目录：

| 文件 | 格式 | 说明 |
|------|------|------|
| `openai_credentials.json` | JSON | 完整凭证数组，包含 email、password、access_token 等 |
| `openai_tokens.txt` | TEXT | 环境变量格式，每行一个 `OPENAI_ACCESS_TOKEN=xxx` |
| `auth_*.json` | JSON | CodeX CLI 格式，每个账号一个文件 |

### 1.5 工作流程

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
