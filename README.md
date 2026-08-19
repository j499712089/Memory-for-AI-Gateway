# Memory Gateway

**统一 LLM 网关与记忆管理系统** — 支持多团队隔离、API Key 管理、请求录入与幂等性保障。

[![Go Version](https://img.shields.io/badge/Go-1.23-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## 项目简介

Memory Gateway 是一个企业级 LLM 网关，提供：

- **统一 API 网关** - 支持 Anthropic/OpenAI/Codex 多协议代理
- **多租户隔离** - 团队级数据隔离与权限控制
- **请求录入** - 自动记录所有 LLM 对话到 L0 层（可选）
- **幂等性保障** - 防止重复请求，24 小时内返回缓存
- **Web 管理面板** - 团队管理、API Key 生成、健康监控

---

## 快速启动

### 环境要求

- **Go 1.23+** (必需)
- **Node.js 18+** (仅 Web 面板需要)
- **SQLite 3.35+** (内置，无需额外安装)
- **操作系统**: Windows 10/11, Linux, macOS

### 开发环境 3 步启动

#### 1. 克隆仓库

```bash
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway
```

#### 2. 配置环境变量

复制 `.env.example` 为 `.env` 并修改：

```bash
# Windows
copy .env.example .env

# macOS / Linux
cp .env.example .env
```

编辑 `.env` 文件，设置数据目录：

```env
MEMORY_PLUS_DIR=F:\memory_plus          # Windows 示例
# MEMORY_PLUS_DIR=/home/user/memory_plus  # Linux/macOS 示例
GATEWAY_PORT=8096
```

#### 3. 启动服务

**后端 (Gateway)**:

```bash
# 编译
go build -o gateway.exe cmd/gateway/main.go   # Windows
# go build -o gateway cmd/gateway/main.go     # Linux/macOS

# 运行
./gateway.exe   # Windows
# ./gateway     # Linux/macOS
```

**前端 (Web Panel)**:

```bash
cd web-panel
npm install
npm run dev
```

访问 [http://localhost:5173](http://localhost:5173) 查看管理面板。

### 健康检查

```bash
curl http://127.0.0.1:8096/health
```

预期输出：

```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

---

## 构建步骤

### 后端构建

```bash
# 开发模式（带热重载）
go run cmd/gateway/main.go

# 生产构建
go build -ldflags="-s -w" -o gateway cmd/gateway/main.go

# 跨平台构建
GOOS=linux GOARCH=amd64 go build -o gateway-linux cmd/gateway/main.go
GOOS=windows GOARCH=amd64 go build -o gateway.exe cmd/gateway/main.go
GOOS=darwin GOARCH=arm64 go build -o gateway-mac cmd/gateway/main.go
```

### 前端构建

```bash
cd web-panel

# 开发模式
npm run dev

# 生产构建
npm run build

# 预览生产构建
npm run preview
```

构建产物位于 `web-panel/dist/`，可部署到静态文件服务器或通过 Gateway 内嵌服务。

### MCP Server 构建（可选）

```bash
cd mcp-server
npm install
npm run build
```

---

## 目录结构

```
gateway/
├── cmd/
│   └── gateway/           # 主服务入口
│       └── main.go
├── internal/
│   ├── adapter/           # LLM 协议适配器（Anthropic/OpenAI/Codex）
│   ├── auth/              # Bearer Token 认证中间件
│   ├── db/                # SQLite 数据库层
│   ├── httpx/             # HTTP 路由与中间件
│   │   ├── router.go      # 路由注册
│   │   ├── admin.go       # 管理 API Handler
│   │   ├── gateway.go     # 网关 API Handler
│   │   ├── mcp.go         # MCP 内部 API Handler
│   │   └── health.go      # 健康检查 Handler
│   ├── models/            # 数据模型定义
│   ├── recorder/          # L0 录入引擎
│   └── secrets/           # 密钥管理
├── web-panel/             # Vue 3 管理面板
│   ├── src/
│   │   ├── api/           # API 客户端封装
│   │   ├── views/         # 页面组件
│   │   ├── router/        # 路由配置
│   │   └── App.vue
│   └── package.json
├── mcp-server/            # MCP 协议服务端（TypeScript）
├── schema/                # 数据库 Schema（SQLite）
│   ├── global.sql         # 全局数据库表
│   └── teams.sql          # 团队数据库表
├── docs/                  # 项目文档
│   ├── API.md             # API 使用文档（面向用户）
│   └── 03_api_design.md   # API 设计文档（面向开发者）
├── test/                  # 集成测试
├── .env.example           # 环境变量示例
├── README.md              # 本文件
├── Makefile               # 构建脚本（可选）
└── go.mod                 # Go 模块定义
```

### 核心模块说明

- **cmd/gateway**: 服务启动入口，初始化数据库、路由、中间件
- **internal/httpx**: HTTP 层实现，包含所有端点 Handler 和中间件
- **internal/auth**: 认证中间件，基于 Bearer Token 校验 API Key
- **internal/adapter**: LLM 协议适配器，将不同厂商 API 转换为统一格式
- **internal/recorder**: L0 录入引擎，将 LLM 对话记录到本地文件系统
- **internal/secrets**: 密钥管理，从 `.runtime/secrets` 读取上游 API Key
- **web-panel**: Vue 3 单页应用，提供可视化管理界面

---

## 核心功能

### 1. 多团队隔离

每个团队拥有独立的：
- API Key 管理
- 上游通道配置
- 会话历史
- 数据库实例（位于 `90_运行数据/teams/<team_id>/`）

### 2. API Key 管理

- **创建 API Key** - 支持自定义权限范围（gateway/mcp/admin）
- **密钥安全** - 仅创建时返回明文，后续只存 SHA-256 哈希
- **过期控制** - 可设置过期时间，自动禁用
- **吊销机制** - 支持手动禁用/吊销

### 3. LLM 网关代理

支持多协议：
- **Anthropic Messages API** - `/v1/messages` 和 `/claude-code/:channel/v1/messages`
- **OpenAI Chat Completions** - `/codebuddy/:channel/v1/chat/completions`
- **Codex Responses** - `/codex/:channel/v1/responses`
- **DSH** - `/dsh/:channel/v1/chat/completions`

通道隔离：
- 每个团队可配置多个上游通道
- 支持通道优先级与故障切换
- 支持 API Key 轮换与负载均衡

### 4. 请求幂等性

防止重复请求造成的重复扣费：
- 客户端传递 `Idempotency-Key` 请求头
- 24 小时内相同 Key 返回缓存响应
- 缓存存储在 SQLite `idempotency_cache` 表

### 5. 录入健康看板

监控所有 LLM 请求的录入状态：
- **terminal_status** - Turn 终止状态（complete/failed/timeout）
- **compensation_status** - 补偿状态（ok/pending）
- **L0 路径** - 录入文件存储位置

---

## 使用示例

### 创建团队

```bash
curl -X POST http://127.0.0.1:8096/api/teams \
  -H "Authorization: Bearer sk_your_admin_key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Demo Team",
    "slug": "demo-team",
    "description": "演示团队",
    "visibility": "private"
  }'
```

### 创建 API Key

```bash
curl -X POST http://127.0.0.1:8096/api/api-keys \
  -H "Authorization: Bearer sk_your_admin_key" \
  -H "Content-Type: application/json" \
  -d '{
    "team_id": "team_abc123",
    "scopes": ["gateway", "mcp"],
    "expires_at": "2027-12-31T23:59:59Z"
  }'
```

**重要**: 返回的 `key` 字段仅此次可见，请妥善保存。

### 调用 LLM 网关

```bash
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer sk_your_gateway_key" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [
      {
        "role": "user",
        "content": "Hello, Claude!"
      }
    ]
  }'
```

### 查询录入健康

```bash
curl "http://127.0.0.1:8096/api/recording-health?limit=10&terminal_status=complete" \
  -H "Authorization: Bearer sk_your_admin_key"
```

---

## 配置说明

### 环境变量

详见 `.env.example` 和 `docs/API.md#环境变量配置`。

### 数据库

Gateway 使用 SQLite（WAL 模式）存储：
- **全局数据库**: `.runtime/memory-gateway.db` - 存储团队、API Key、幂等性缓存
- **团队数据库**: `90_运行数据/teams/<team_id>/team.db` - 存储上游通道、会话、录入记录

### 日志

日志输出到标准输出（stdout），生产环境建议重定向到文件：

```bash
./gateway 2>&1 | tee gateway.log
```

---

## 测试

### 运行单元测试

```bash
go test ./...
```

### 运行集成测试

```bash
go test ./test/... -v
```

### Web 面板测试

```bash
cd web-panel
npm run test
```

---

## 部署指南

### Docker 部署

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -ldflags="-s -w" -o gateway cmd/gateway/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/gateway .
EXPOSE 8096
CMD ["./gateway"]
```

构建并运行：

```bash
docker build -t memory-gateway .
docker run -d \
  -p 8096:8096 \
  -v /path/to/data:/data \
  -e MEMORY_PLUS_DIR=/data \
  --name memory-gateway \
  memory-gateway
```

### Systemd 服务（Linux）

创建 `/etc/systemd/system/memory-gateway.service`:

```ini
[Unit]
Description=Memory Gateway Service
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/memory-gateway
Environment="MEMORY_PLUS_DIR=/opt/memory_plus"
Environment="GATEWAY_PORT=8096"
ExecStart=/opt/memory-gateway/gateway
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

启用并启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable memory-gateway
sudo systemctl start memory-gateway
```

---

## 常见问题

### Q1: 数据库锁定错误 `database is locked`

**原因**: SQLite 在高并发下可能遇到锁冲突。

**解决方案**:
1. 确保使用 WAL 模式（已默认启用）
2. 检查 `busy_timeout` 设置（已设为 5000ms）
3. 避免长时间持有事务

### Q2: API Key 认证失败

**排查步骤**:
1. 检查 `Authorization` 头格式：`Bearer sk_...`
2. 确认 API Key 未过期：查询 `api_keys` 表的 `expires_at` 字段
3. 确认 API Key 未被禁用：`enabled = 1`, `revoked_at IS NULL`

### Q3: Web 面板无法连接 Gateway

**排查步骤**:
1. 确认 Gateway 正在运行：`curl http://127.0.0.1:8096/health`
2. 检查 CORS 配置（开发环境已默认允许）
3. 检查 Vite 代理配置：`web-panel/vite.config.ts`

---

## 参与贡献

我们欢迎所有形式的贡献：

- 🐛 提交 Bug 报告
- ✨ 提出新功能建议
- 📝 完善文档
- 🔧 提交代码补丁

### 贡献步骤

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交改动 (`git commit -m 'feat: Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 提交 Pull Request

---

## 许可协议

本项目采用 [MIT License](LICENSE) 开源协议。

---

## 联系方式

- **Issue 反馈**: [GitHub Issues](https://github.com/j499712089/Memory-for-AI/issues)
- **邮箱**: 861892722@qq.com
- **文档**: [API 使用文档](docs/API.md) | [架构设计文档](docs/03_api_design.md)

---

**Built with ❤️ by Memory-for-AI Team**
