# 架构设计文档

## 系统概览

Memory Gateway 是一个多租户 LLM 请求网关，提供：
- **API 网关**：统一的 LLM 请求入口，支持 Anthropic Messages、OpenAI Chat Completions、Codex Responses 三种协议
- **录入保障**：完整录入每个 turn 到 L0 层，补偿机制保障零丢失
- **MCP 服务**：提供 Memory/Wiki/CodeGraph/Skill 等工具供 Claude Desktop 使用
- **管理面板**：团队/身份卡/API Key/上游通道/健康看板的 Web UI

## 整体架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                          客户端层                                     │
│  Claude Desktop  CodeBuddy  Codex  DSH  OpenClaw  Hermes  ...      │
└─────────────────────────────────────────────────────────────────────┘
                            │
                            ↓ (Authorization: Bearer <api-key>)
┌─────────────────────────────────────────────────────────────────────┐
│                      Gateway HTTP Server :8096                       │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  Middleware: Auth → Idempotency → Logging                   │   │
│  └─────────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  Router:                                                     │   │
│  │  - /health                    (Health Check)                │   │
│  │  - /api/teams                 (Team CRUD)                   │   │
│  │  - /api/api-keys              (API Key CRUD)                │   │
│  │  - /api/recording-health      (Dashboard Data)              │   │
│  │  - /api/mcp/*                 (Internal MCP API)            │   │
│  │  - /v1/messages               (Anthropic Messages)          │   │
│  │  - /v1/chat/completions       (OpenAI Chat Completions)     │   │
│  │  - /v1/responses              (Codex Responses)             │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        ↓                   ↓                   ↓
┌──────────────┐  ┌──────────────────┐  ┌──────────────┐
│ SQLite 全局库│  │ SQLite 团队库    │  │ 文件系统     │
│ memory-      │  │ teams/{id}/      │  │ L0/L1/L2/L3  │
│ gateway.db   │  │ memory.db        │  │ 资产正文     │
└──────────────┘  └──────────────────┘  └──────────────┘
                            │
                            ↓
┌─────────────────────────────────────────────────────────────────────┐
│                      Worker 异步任务                                 │
│  - Outbox 重试 (网络失败时补发录入)                                  │
│  - Buffer 补偿 (降级模式写入文件，定期回写数据库)                     │
│  - Task Queue 处理                                                   │
└─────────────────────────────────────────────────────────────────────┘
                            │
                            ↓
┌─────────────────────────────────────────────────────────────────────┐
│                   MCP Server :8097                                   │
│  Transport: stdio (Claude Desktop) / HTTP (其他客户端)              │
│  Tools: memory_search / wiki_search / codegraph_impact / ...       │
│  ↓ 调用 Gateway :8096 的 /api/mcp/* 内部 API                        │
└─────────────────────────────────────────────────────────────────────┘
                            │
                            ↓
┌─────────────────────────────────────────────────────────────────────┐
│                   Web Panel :5173 (开发) / dist (生产)              │
│  Vue 3 SPA: Teams / Identities / API Keys / Channels / Health      │
│  ↓ 调用 Gateway :8096 的 /api/* RESTful API                         │
└─────────────────────────────────────────────────────────────────────┘
```

## 分层架构

### 1. 表现层 (Presentation Layer)

**职责**：HTTP 请求/响应处理、中间件、路由

**组件**：
- `internal/httpx/router.go`：路由配置，定义所有 HTTP 端点
- `internal/httpx/middleware.go`：认证、幂等性、日志中间件
- `internal/httpx/*_handler.go`：各类 Handler (admin/gateway/health/mcp)

**设计原则**：
- Handler 只做 HTTP 层事务：参数校验、JSON 序列化、状态码映射
- 不包含业务逻辑，复杂逻辑下沉到 Service 层
- 统一错误响应格式：
  ```json
  {
    "error": {
      "type": "invalid_request|auth_error|internal_error",
      "message": "human-readable error message"
    }
  }
  ```

**单文件行数限制**：< 300 行 (目前 router.go 82 行，health_handler.go 185 行，符合规范)

### 2. 业务层 (Business Layer)

**职责**：核心业务逻辑、外部调用、异步任务

**组件**：
- `internal/httpx/gateway_handler.go`：LLM 请求转发、录入编排
- `internal/httpx/admin_handler.go`：团队/API Key 管理
- `internal/worker/`：异步任务处理 (outbox/buffer 补偿)
- `internal/secrets/`：密钥管理 (DPAPI/Stub)
- `internal/auth/`：认证授权
- `internal/idgen/`：ID 生成 (UUID)

**设计原则**：
- 每个 Handler 对应一个业务领域 (team/api-key/gateway/health)
- 复杂业务拆分为多个函数，保持单一职责
- 幂等性保障：`idempotency_cache` 表 + 中间件
- 录入保障：Outbox 模式 + Buffer 降级机制

**关键流程**：
```
LLM 请求 → AuthMiddleware → IdempotencyMiddleware → GatewayHandler
  ↓
1. 查询上游通道 (upstream_channels)
2. 转换协议格式 (Anthropic/OpenAI/Codex)
3. 调用上游 LLM API
4. 写入 turn_ledger (ACID 事务)
5. 尝试录入 L0 文件
6. 失败则写 outbox/buffer (异步补偿)
7. 返回响应给客户端
```

### 3. 数据层 (Data Layer)

**职责**：数据持久化、事务管理、查询优化

**组件**：
- `internal/db/db.go`：数据库连接、事务封装
- `internal/db/migrate.go`：Schema 迁移
- `internal/db/recover.go`：启动恢复 (outbox/buffer 重放)
- `schema/schema.sql`：数据库 Schema 定义

**数据库设计**：

#### 全局库 (memory-gateway.db)
```sql
-- 核心表
teams                   -- 团队
team_members            -- 团队成员
agents                  -- 代理 (Claude/CodeBuddy/Codex...)
identity_cards          -- 身份卡 (L3 资产)
api_keys                -- API Key (只存哈希+引用)
upstream_channels       -- 上游通道 (models.json 导入)
channel_aliases         -- 通道别名 (协议感知映射)

-- 会话与账本
sessions                -- 会话 (conversation_id 绑定)
turn_ledger             -- Turn 账本 (完整录入证明)
turn_events             -- Turn 事件流
idempotency_cache       -- 幂等性缓存

-- 可靠性保障
outbox                  -- 外发队列 (重试机制)
buffer_台账             -- 降级缓冲 (离线持久化)
task_queue              -- 异步任务队列
recording_health        -- 录入健康看板
```

#### 团队库 (teams/{team_id}/memory.db)
```sql
assets                  -- 资产清单 (L0/L1/L2/L3)
asset_citations         -- 正文引用
wikis                   -- Wiki 条目
codegraph_nodes         -- 代码图谱节点
codegraph_edges         -- 代码图谱边
skills                  -- 技能库
bindings                -- 绑定配置
asset_acl               -- 资产权限控制
```

**WAL 模式配置**：
```sql
PRAGMA journal_mode = WAL;        -- 并发读写
PRAGMA foreign_keys = ON;         -- 外键约束
PRAGMA busy_timeout = 5000;       -- 写锁等待 5s
PRAGMA synchronous = NORMAL;      -- WAL 下安全且高效
```

**索引策略**：
- 主键：自动建立 B-Tree 索引
- 外键：`api_keys(team_id)`, `turn_ledger(session_id)` 等
- 查询热点：`upstream_channels(team_id, protocol)`, `sessions(conversation_id)`

## API 设计

### RESTful 规范

**资源命名**：复数名词，使用 kebab-case
- `/api/teams` (团队)
- `/api/api-keys` (API Key)
- `/api/recording-health` (录入健康)

**HTTP 方法语义**：
- `GET`：查询资源 (幂等)
- `POST`：创建资源 (非幂等，但通过 idempotency-key 保障)
- `PUT`：全量更新 (幂等)
- `PATCH`：部分更新 (幂等)
- `DELETE`：删除资源 (幂等)

**状态码约定**：
- `200 OK`：成功返回数据
- `201 Created`：资源创建成功
- `400 Bad Request`：参数错误
- `401 Unauthorized`：未认证
- `403 Forbidden`：无权限
- `404 Not Found`：资源不存在
- `409 Conflict`：资源冲突 (如 slug 重复)
- `500 Internal Server Error`：服务器错误

### 统一响应格式

**成功响应**：
```json
{
  "id": "team_abc123",
  "name": "Demo Team",
  "slug": "demo-team",
  "created_at": "2026-08-19T10:00:00Z"
}
```

**错误响应**：
```json
{
  "error": {
    "type": "invalid_request",
    "message": "slug already exists"
  }
}
```

**分页响应**：
```json
{
  "items": [...],
  "total": 100,
  "limit": 20,
  "offset": 0
}
```

### 核心端点清单

#### 管理 API (需认证)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/teams` | GET | 列出团队 |
| `/api/teams` | POST | 创建团队 |
| `/api/teams/:team_id` | GET | 获取团队详情 |
| `/api/api-keys` | GET | 列出 API Key |
| `/api/api-keys` | POST | 创建 API Key |
| `/api/recording-health` | GET | 获取录入健康数据 |

#### 网关 API (需认证 + 幂等性)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/messages` | POST | Anthropic Messages API |
| `/claude-code/:channel/v1/messages` | POST | Claude Code 专用 |
| `/codebuddy/:channel/v1/chat/completions` | POST | CodeBuddy 专用 |
| `/codex/:channel/v1/responses` | POST | Codex 专用 |
| `/dsh/:channel/v1/chat/completions` | POST | DSH 专用 |

#### MCP 内部 API (需认证 + MCP scope)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/mcp/memory/search` | POST | Memory 搜索 |
| `/api/mcp/memory/get` | POST | Memory 获取 |
| `/api/mcp/memory/append` | POST | Memory 追加 |
| `/api/mcp/wiki/search` | POST | Wiki 搜索 |
| `/api/mcp/codegraph/impact` | POST | 代码影响分析 |
| `/api/mcp/skill/search` | POST | 技能搜索 |
| `/api/mcp/binding/get` | POST | 绑定配置获取 |
| `/api/mcp/assets/list` | POST | 资产列表 |

#### 健康检查 (无需认证)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 服务健康状态 |

### OpenAPI 规范

完整 API 规范见 `docs/openapi.yaml` (由 ADR-003 产出)。

关键特性：
- Bearer Token 认证：`Authorization: Bearer <api-key>`
- 幂等性保障：`Idempotency-Key` 请求头 (24 小时内有效)
- 速率限制：未来扩展 (暂无)
- 错误码：标准化错误类型 + 人类可读消息

## 数据流设计

### LLM 请求完整流程

```
1. 客户端发送请求
   POST /v1/messages
   Authorization: Bearer sk_abc123...
   Idempotency-Key: uuid-xxx-yyy
   Content-Type: application/json

2. AuthMiddleware 认证
   - 查询 api_keys 表，校验 key_hash
   - 解析 team_id、scopes
   - 写入上下文 c.Set("team_id", teamID)

3. IdempotencyMiddleware 幂等性检查
   - 查询 idempotency_cache 表
   - 命中缓存 → 直接返回缓存响应 (200 OK)
   - 未命中 → 继续处理

4. GatewayHandler 处理请求
   a. 解析通道名 (从 URL 或 X-Channel 头)
   b. 查询 upstream_channels (支持 channel_aliases 协议映射)
   c. 获取上游密钥 (secretsManager.Get(api_key_ref))
   d. 转换协议格式 (Anthropic ↔ OpenAI ↔ Codex)
   e. 调用上游 LLM API (HTTP POST)
   f. 流式返回 or 全量返回

5. 录入编排 (ACID 事务)
   BEGIN TRANSACTION
     - 写入 turn_ledger (request_id/conversation_id/session_id/final_status/content_hash)
     - 写入 turn_events (event_type=inbound/outbound)
     - 写入 L0 文件 (F:\memory_plus\L0_每一轮记录\{date}\{request_id}.jsonl)
   COMMIT

6. 录入保障
   - L0 写入成功 → 更新 turn_ledger.recording_state = 'complete'
   - L0 写入失败 → 写 outbox (worker 异步重试)
   - SQLite 写失败 → 写 buffer_台账 (降级文件，启动时回写)

7. 缓存幂等性结果
   INSERT INTO idempotency_cache (key, response_body, expires_at)

8. 返回响应给客户端
   HTTP 200 OK
   Content-Type: application/json
   { "id": "msg_xxx", "content": [...], ... }
```

### 录入保障机制

**三层保障**：
1. **同步录入 (99% 情况)**：turn_ledger + L0 文件在同一事务后立即写入
2. **Outbox 重试 (网络抖动)**：L0 写失败 → outbox 表 → worker 每 10s 重试
3. **Buffer 降级 (SQLite 繁忙)**：SQLite 写超时 → 文件系统缓冲 → 启动时回写

**状态流转**：
```
turn_ledger.recording_state:
  pending → complete (正常)
  pending → compensating → complete (outbox 补偿)
  pending → degraded → complete (buffer 降级后恢复)
```

## 部署架构

### 单机部署 (MVP)

```
F:\memory_plus\
├── .runtime\
│   ├── memory-gateway.db        # 全局库
│   ├── secrets\                 # 密钥存储 (DPAPI/Stub)
│   └── gateway.log              # 服务日志
├── 90_运行数据\
│   └── teams\
│       └── {team_id}\
│           └── memory.db        # 团队库
├── L0_每一轮记录\               # L0 资产
├── L1_长久记忆\                 # L1 资产
├── L2_全域知识\                 # L2 资产
├── L3_团队身份\                 # L3 资产
└── gateway\
    ├── gateway.exe              # 主服务
    ├── worker.exe               # 异步 Worker (可选独立进程)
    ├── web-panel\dist\          # 静态文件 (生产)
    └── mcp-server\dist\         # MCP 服务

进程：
1. gateway.exe :8096             # 主 HTTP 服务
2. node mcp-server/dist/index.js # MCP 服务 (stdio/HTTP :8097)
3. web-panel 静态托管 (可选 Caddy/nginx，或 gateway 内嵌)
```

### 进程管理

**开发环境**：
```bash
# Terminal 1: Gateway
cd gateway && go run cmd/gateway/main.go

# Terminal 2: MCP Server (stdio)
cd mcp-server && npm run dev:stdio

# Terminal 3: Web Panel
cd web-panel && npm run dev
```

**生产环境**：
- Windows Service：NSSM 注册 gateway.exe 为服务
- Linux：systemd unit 文件
- Docker：Dockerfile 多阶段构建 (可选)

## 可扩展性设计

### 水平扩展 (未来)

当单机瓶颈时 (写 QPS > 500 或数据 > 100GB)：

1. **数据库迁移**：SQLite → PostgreSQL
   - 修改 `internal/db/db.go` 驱动
   - Schema 迁移工具 (goose/migrate)

2. **分布式锁**：幂等性缓存改用 Redis
   - 替换 `idempotency_cache` 表逻辑

3. **消息队列**：Outbox 改用 RabbitMQ/Kafka
   - Worker 订阅队列而非轮询表

4. **无状态网关**：多实例 + 负载均衡
   - Nginx/HAProxy 前置
   - Session 存储共享 (Redis)

### 监控与告警

**指标采集**：
- Prometheus `/metrics` 端点 (使用 `github.com/prometheus/client_golang`)
- 关键指标：
  - `gateway_requests_total{status, channel}`：请求总数
  - `gateway_request_duration_seconds{quantile}`：请求延迟
  - `gateway_recording_failures_total`：录入失败数
  - `gateway_outbox_queue_size`：Outbox 队列长度

**日志**：
- 结构化日志 (使用 `log/slog`)
- 级别：DEBUG/INFO/WARN/ERROR
- 输出：stdout + 文件轮转

**告警规则**：
- 录入失败率 > 1% → P1 告警
- Outbox 队列堆积 > 100 → P2 告警
- Database 连接失败 → P0 告警

## 安全设计

### 密钥管理

**密钥纪律**：
- 数据库表绝不存明文密钥
- API Key 只存 `key_hash` (SHA-256) 和 `key_ref` (引用)
- 真实密钥由 `secrets.Manager` 从 DPAPI/文件读取

**DPAPI 实现** (Windows)：
```go
// 加密
encrypted := windows.CryptProtectData(plaintext, entropy, flags)
os.WriteFile(secretsDir + "/" + keyRef, encrypted)

// 解密
plaintext := windows.CryptUnprotectData(encrypted, entropy, flags)
```

**Stub 实现** (开发/跨平台)：
```go
// 明文文件存储，仅开发使用
os.WriteFile(secretsDir + "/" + keyRef, plaintext)
```

### 认证授权

**API Key 校验**：
1. 提取 `Authorization: Bearer <key>`
2. 计算 `key_hash = SHA256(key)`
3. 查询 `api_keys` 表，匹配 `key_hash`
4. 校验 `enabled=1`, `expires_at`, `scopes`
5. 解析 `team_id`，写入上下文

**权限范围 (Scopes)**：
- `gateway`：允许调用 LLM 网关 API
- `mcp`：允许调用 MCP 内部 API
- `admin`：允许调用管理 API (未来扩展)

### 防护措施

- **SQL 注入**：使用参数化查询 (`db.Query(query, args...)`)
- **XSS**：前端使用 Vue 自动转义
- **CSRF**：SPA 使用 Bearer Token，无需 CSRF Token
- **速率限制**：未来扩展 (当前未实现)

## 测试策略

### 单元测试
- 覆盖率目标：> 60%
- 框架：`testing` (Go 标准库) + `vitest` (前端)
- Mock：`sql.DB` 使用 in-memory SQLite

### 集成测试
- E2E 流程测试：完整 LLM 请求 + 录入验证
- 数据库测试：真实 SQLite 文件
- MCP 测试：调用真实 Gateway API

### 性能测试
- 工具：`wrk` / `ab` (Apache Bench)
- 场景：
  - 并发请求 100 QPS，持续 1 分钟
  - 验证：P99 延迟 < 500ms，错误率 < 0.1%

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**维护者**: 首席架构师 高见远
