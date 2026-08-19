# API 设计文档

## 概述

Memory Gateway 提供三类 API：
1. **管理 API** (`/api/*`)：团队/API Key/健康看板等管理功能
2. **网关 API** (`/v1/*`)：LLM 请求代理，支持多协议
3. **MCP 内部 API** (`/api/mcp/*`)：供 MCP Server 调用的内部接口

所有 API 均使用 JSON 格式，遵循 RESTful 规范。

## 认证机制

### Bearer Token 认证

所有需认证的端点 (除 `/health` 外) 均使用 Bearer Token：

```http
Authorization: Bearer sk_abc123def456...
```

**Token 生成**：
- 创建 API Key 时自动生成 (32 字节随机字符串)
- 仅创建时返回明文，后续只存 SHA-256 哈希

**Token 校验流程**：
1. 提取 `Authorization` 头
2. 计算 `key_hash = SHA256(token)`
3. 查询 `api_keys` 表
4. 校验 `enabled=1`, `expires_at`, `revoked_at`
5. 解析 `team_id` 和 `scopes`

**错误响应**：
```json
{
  "error": {
    "type": "auth_error",
    "message": "invalid or expired API key"
  }
}
```

### 权限范围 (Scopes)

API Key 支持多权限范围：

| Scope | 说明 | 允许访问 |
|-------|------|---------|
| `gateway` | 网关权限 | `/v1/*` 所有 LLM 端点 |
| `mcp` | MCP 权限 | `/api/mcp/*` 所有内部端点 |
| `admin` | 管理权限 | `/api/teams`, `/api/api-keys` (未来) |

**Scope 校验**：
- `/api/mcp/*` 端点额外校验 `scopes` 包含 `"mcp"`
- 其他端点暂不强制校验，默认只需有效 Token

## 幂等性保障

### Idempotency-Key 请求头

网关 API (`/v1/*`) 支持幂等性保障，防止重复请求：

```http
POST /v1/messages
Authorization: Bearer sk_...
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json
```

**规则**：
- `Idempotency-Key` 为 UUID 或任意字符串 (推荐 UUID)
- 24 小时内相同 Key 返回缓存响应 (不重复调用上游 LLM)
- 缓存命中返回 `200 OK` + 原始响应体

**实现**：
- 存储表：`idempotency_cache (key, response_body, expires_at)`
- 中间件：`IdempotencyMiddleware` 在请求前检查缓存
- 过期清理：定期删除 `expires_at < NOW()` 的记录

## 统一响应格式

### 成功响应

**单个资源**：
```json
{
  "id": "team_abc123",
  "name": "Demo Team",
  "slug": "demo-team",
  "created_at": "2026-08-19T10:00:00Z"
}
```

**资源列表**：
```json
{
  "items": [
    { "id": "team_1", "name": "Team A" },
    { "id": "team_2", "name": "Team B" }
  ],
  "total": 50,
  "limit": 20,
  "offset": 0
}
```

### 错误响应

**格式**：
```json
{
  "error": {
    "type": "error_type",
    "message": "human-readable error description"
  }
}
```

**错误类型**：

| Type | HTTP Status | 说明 |
|------|-------------|------|
| `invalid_request` | 400 | 参数错误、格式错误 |
| `auth_error` | 401 | 认证失败、Token 无效 |
| `forbidden` | 403 | 权限不足 (Scope 不匹配) |
| `not_found` | 404 | 资源不存在 |
| `conflict` | 409 | 资源冲突 (如 slug 重复) |
| `internal_error` | 500 | 服务器内部错误 |

**示例**：
```json
{
  "error": {
    "type": "conflict",
    "message": "slug already exists"
  }
}
```

## 管理 API

### 健康检查

#### `GET /health`

获取服务健康状态（无需认证）。

**响应示例**：
```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

**状态码**：
- `200 OK`：服务正常
- `503 Service Unavailable`：数据库连接失败

**注意**：前端调用的 `/api/health` 路由未注册，实际注册的端点为 `/health`（见 `internal/httpx/router.go:27`）。

---

#### `GET /api/upstream-channels` **(Phase 2 规划中)**

列出上游通道配置（分页）。

**当前状态**：此端点在 `router.go` 中尚未注册，Web 面板中已有调用代码，计划在 Phase 2 实现。

**规划的查询参数**：
- `team_id` (可选)：按团队过滤
- `protocol` (可选)：按协议过滤 (`anthropic` | `openai` | `codex`)
- `limit` (可选)：每页数量
- `offset` (可选)：偏移量

**规划的响应示例**：
```json
{
  "items": [
    {
      "id": "chan_1",
      "team_id": "team_abc123",
      "name": "Claude Production",
      "protocol": "anthropic",
      "priority": 100,
      "enabled": true
    }
  ],
  "total": 5,
  "limit": 20,
  "offset": 0
}
```

---

#### `POST /api/upstream-channels` **(Phase 2 规划中)**

创建上游通道。

**当前状态**：此端点在 `router.go` 中尚未注册，计划在 Phase 2 实现。

**规划的请求体**：
```json
{
  "team_id": "team_abc123",
  "name": "Claude Production",
  "protocol": "anthropic",
  "base_url": "https://api.anthropic.com",
  "priority": 100
}
```

---

#### `PUT /api/upstream-channels/:id` **(Phase 2 规划中)**

更新上游通道配置。

**当前状态**：此端点在 `router.go` 中尚未注册，计划在 Phase 2 实现。

---

#### `DELETE /api/upstream-channels/:id` **(Phase 2 规划中)**

禁用上游通道。

**当前状态**：此端点在 `router.go` 中尚未注册，计划在 Phase 2 实现。

---

#### `POST /api/upstream-channels/import-models` **(Phase 2 规划中)**

从 `models.json` 导入模型配置。

**当前状态**：此端点在 `router.go` 中尚未注册，计划在 Phase 2 实现。

---

### 健康检查

#### `GET /health`

获取服务健康状态（无需认证）。

**响应示例**：
```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

**状态码**：
- `200 OK`：服务正常
- `503 Service Unavailable`：数据库连接失败

**注意**：前端调用的 `/api/health` 路由未注册，实际注册的端点为 `/health`（见 `internal/httpx/router.go:27`）。

---

#### `GET /api/upstream-channels` **(Phase 2 规划中)**

列出上游通道配置（分页）。

**当前状态**：此端点在 `router.go` 中尚未注册，Web 面板中已有调用代码，计划在 Phase 2 实现。

**规划的查询参数**：
- `team_id` (可选)：按团队过滤
- `protocol` (可选)：按协议过滤 (`anthropic` | `openai` | `codex`)
- `limit` (可选)：每页数量
- `offset` (可选)：偏移量

**规划的响应示例**：
```json
{
  "items": [
    {
      "id": "chan_1",
      "team_id": "team_abc123",
      "name": "Claude Production",
      "protocol": "anthropic",
      "priority": 100,
      "enabled": true
    }
  ],
  "total": 5,
  "limit": 20,
  "offset": 0
}
```

---

### 团队管理

#### `POST /api/teams`

创建新团队。

**请求体**：
```json
{
  "name": "Demo Team",
  "slug": "demo-team",
  "description": "A demo team for testing",
  "visibility": "private"
}
```

**字段说明**：
- `name` (必填)：团队名称
- `slug` (必填)：团队唯一标识 (URL 友好)
- `description` (可选)：团队描述
- `visibility` (可选)：可见性 (`private` | `team` | `restricted`，默认 `private`)

**响应示例** (`201 Created`)：
```json
{
  "id": "team_abc123",
  "name": "Demo Team",
  "slug": "demo-team",
  "description": "A demo team for testing",
  "visibility": "private",
  "status": "active",
  "created_at": "2026-08-19T10:00:00Z",
  "updated_at": "2026-08-19T10:00:00Z"
}
```

**错误响应**：
- `400 Bad Request`：缺少必填字段
- `409 Conflict`：slug 已存在

---

#### `GET /api/teams`

列出所有团队（分页）。

**查询参数**：
- `limit` (可选)：每页数量，默认 20，最大 100
- `offset` (可选)：偏移量，默认 0

**响应示例**：
```json
{
  "items": [
    {
      "id": "team_1",
      "name": "Team A",
      "slug": "team-a",
      "visibility": "private",
      "status": "active",
      "created_at": "2026-08-19T09:00:00Z"
    },
    {
      "id": "team_2",
      "name": "Team B",
      "slug": "team-b",
      "visibility": "team",
      "status": "active",
      "created_at": "2026-08-19T09:30:00Z"
    }
  ],
  "total": 50,
  "limit": 20,
  "offset": 0
}
```

---

#### `GET /api/teams/:team_id`

获取团队详情。

**路径参数**：
- `team_id`：团队 ID

**响应示例** (`200 OK`)：
```json
{
  "id": "team_abc123",
  "name": "Demo Team",
  "slug": "demo-team",
  "description": "A demo team for testing",
  "visibility": "private",
  "status": "active",
  "created_at": "2026-08-19T10:00:00Z",
  "updated_at": "2026-08-19T10:00:00Z"
}
```

**错误响应**：
- `404 Not Found`：团队不存在

---

### API Key 管理

#### `POST /api/api-keys`

创建新 API Key。

**请求体**：
```json
{
  "team_id": "team_abc123",
  "scopes": ["gateway", "mcp"],
  "expires_at": "2027-08-19T00:00:00Z"
}
```

**字段说明**：
- `team_id` (必填)：所属团队 ID
- `scopes` (可选)：权限范围数组，默认 `["gateway", "mcp"]`
- `expires_at` (可选)：过期时间 (RFC3339 格式)，不填则永不过期

**响应示例** (`201 Created`)：
```json
{
  "id": "key_xyz789",
  "team_id": "team_abc123",
  "key": "sk_abc123def456...",
  "scopes": ["gateway", "mcp"],
  "enabled": true,
  "expires_at": "2027-08-19T00:00:00Z",
  "created_at": "2026-08-19T10:00:00Z"
}
```

**重要提示**：
- `key` 字段仅在创建时返回，后续无法再获取
- 请妥善保存 API Key

---

#### `GET /api/api-keys`

列出 API Keys（分页）。

**查询参数**：
- `team_id` (可选)：按团队过滤
- `limit` (可选)：每页数量，默认 20
- `offset` (可选)：偏移量，默认 0

**响应示例**：
```json
{
  "items": [
    {
      "id": "key_1",
      "team_id": "team_abc123",
      "key_prefix": "sk_abc...",
      "scopes": ["gateway", "mcp"],
      "enabled": true,
      "expires_at": null,
      "last_used_at": "2026-08-19T09:45:00Z",
      "created_at": "2026-08-19T08:00:00Z"
    }
  ],
  "total": 10,
  "limit": 20,
  "offset": 0
}
```

**注意**：
- 列表接口不返回完整 `key`，仅返回前缀 `key_prefix` (前 8 字符)

---

#### `DELETE /api/api-keys/:id` **(Phase 2 规划中)**

删除/吊销 API Key。

**当前状态**：此端点在 `router.go` 中尚未注册，计划在 Phase 2 实现。当前可通过 `PUT /api/api-keys/:id` 更新 `enabled: false` 来禁用密钥。

**路径参数**：
- `id`：API Key ID

**规划的响应示例** (`200 OK`)：
```json
{
  "id": "key_1",
  "team_id": "team_abc123",
  "enabled": false,
  "revoked_at": "2026-08-19T10:30:00Z"
}
```

---

### 录入健康看板

#### `GET /api/recording-health`

获取录入健康数据（Dashboard 用）。

**查询参数**：
- `limit` (可选)：每页数量，默认 50，最大 100
- `offset` (可选)：偏移量，默认 0
- `terminal_status` (可选)：按终止状态过滤 (`complete` | `failed` | `timeout`)

**响应示例**：
```json
{
  "items": [
    {
      "request_id": "req_abc123",
      "conversation_id": "conv_xyz789",
      "session_id": "sess_aaa111",
      "inbound_at": "2026-08-19T10:00:00Z",
      "terminal_at": "2026-08-19T10:00:05Z",
      "terminal_status": "complete",
      "content_hash": "sha256:abc...",
      "l0_path": "F:\\memory_plus\\L0_每一轮记录\\2026-08-19\\req_abc123.jsonl",
      "binding_version": 1,
      "compensation_status": "ok",
      "last_success_at": "2026-08-19T10:00:05Z"
    }
  ],
  "total": 1234,
  "limit": 50,
  "offset": 0
}
```

**字段说明**：
- `terminal_status`：Turn 终止状态 (`complete` | `failed` | `timeout`)
- `compensation_status`：补偿状态 (`ok` | `pending`)
- `l0_path`：L0 文件路径
- `binding_version`：绑定版本号

---

## 网关 API

### Anthropic Messages API

#### `POST /v1/messages`

代理 Anthropic Messages API。

**请求头**：
```http
Authorization: Bearer sk_...
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json
```

**请求体** (与 Anthropic API 一致)：
```json
{
  "model": "claude-opus-5",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "Hello, Claude!"
    }
  ]
}
```

**响应示例**：
```json
{
  "id": "msg_abc123",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "Hello! How can I help you today?"
    }
  ],
  "model": "claude-opus-5",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 10,
    "output_tokens": 15
  }
}
```

**通道选择**：
- 默认使用 `default` 通道
- 可通过 URL 路径指定：`/claude-code/:channel/v1/messages`（已注册，见 `router.go:65`）

**流式响应**：
支持 SSE (Server-Sent Events) 流式响应，请求头添加：
```http
Accept: text/event-stream
```

**注意**：
- 不支持通过请求头 `X-Channel: my-channel` 指定通道（Phase 2 规划）
- 通用网关别名路径（如 `/anthropic/:channel/v1/messages`）未在当前路由注册，计划通过 `channel_aliases` 表实现

---

### OpenAI Chat Completions API

#### `POST /codebuddy/:channel/v1/chat/completions`
#### `POST /dsh/:channel/v1/chat/completions`

代理 OpenAI Chat Completions API。

**请求体**：
```json
{
  "model": "gpt-4",
  "messages": [
    {
      "role": "user",
      "content": "Hello, GPT!"
    }
  ],
  "temperature": 0.7
}
```

**响应示例**：
```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1692345678,
  "model": "gpt-4",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I assist you?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 12,
    "total_tokens": 22
  }
}
```

**通道路径**：
- CodeBuddy：`/codebuddy/:channel/v1/chat/completions`（已注册，见 `router.go:69`）
- DSH：`/dsh/:channel/v1/chat/completions`（已注册，见 `router.go:77`）

**注意**：
- 不支持通用路径 `/v1/chat/completions`（无 `:channel` 参数），必须通过带通道名的路径调用
- 通用网关别名路径（如 `/openai/:channel/v1/chat/completions`）未注册（Phase 2 规划）

---

### Codex Responses API

#### `POST /codex/:channel/v1/responses`
#### `POST /codex/:channel/responses`

代理 Codex Responses API (OpenClaw 专用)。

**请求体**：
```json
{
  "model": "codex-model",
  "prompt": "Write a Python function to reverse a string",
  "max_tokens": 256
}
```

**响应示例**：
```json
{
  "id": "resp_abc123",
  "model": "codex-model",
  "response": "def reverse_string(s):\n    return s[::-1]",
  "usage": {
    "prompt_tokens": 15,
    "completion_tokens": 20
  }
}
```

**通道路径**：
- `/codex/:channel/v1/responses`（已注册，见 `router.go:72`）
- `/codex/:channel/responses`（已注册，见 `router.go:74`，兼容旧版无 `/v1` 前缀）

**注意**：
- 不支持通用路径 `/v1/responses`（无 `:channel` 参数），必须通过带通道名的路径调用

---

## MCP 内部 API

以下 API 供 MCP Server (`:8097`) 调用，需 `mcp` scope。

### Memory 搜索

#### `POST /api/mcp/memory/search`

搜索 Memory 资产。

**请求体**：
```json
{
  "query": "authentication implementation",
  "limit": 10
}
```

**响应示例**：
```json
{
  "results": [
    {
      "id": "mem_abc123",
      "title": "User Authentication Flow",
      "content": "The authentication system uses JWT tokens...",
      "relevance_score": 0.95,
      "created_at": "2026-08-15T10:00:00Z"
    }
  ]
}
```

---

### Memory 获取

#### `POST /api/mcp/memory/get`

获取单个 Memory 资产。

**请求体**：
```json
{
  "id": "mem_abc123"
}
```

**响应示例**：
```json
{
  "id": "mem_abc123",
  "title": "User Authentication Flow",
  "content": "The authentication system uses JWT tokens...",
  "created_at": "2026-08-15T10:00:00Z",
  "updated_at": "2026-08-18T14:30:00Z"
}
```

---

### Memory 追加

#### `POST /api/mcp/memory/append`

追加内容到 Memory 资产。

**请求体**：
```json
{
  "id": "mem_abc123",
  "content": "Additional notes: Remember to invalidate cache on logout."
}
```

**响应示例**：
```json
{
  "success": true,
  "updated_at": "2026-08-19T10:05:00Z"
}
```

---

### Wiki 搜索

#### `POST /api/mcp/wiki/search`

搜索 Wiki 条目。

**请求体**：
```json
{
  "query": "deployment guide",
  "limit": 5
}
```

**响应示例**：
```json
{
  "results": [
    {
      "id": "wiki_abc123",
      "title": "Deployment Guide",
      "summary": "Step-by-step guide for deploying to production...",
      "url": "/wiki/deployment-guide",
      "relevance_score": 0.92
    }
  ]
}
```

---

### 代码影响分析

#### `POST /api/mcp/codegraph/impact`

分析代码变更影响范围。

**请求体**：
```json
{
  "file_path": "internal/auth/middleware.go",
  "function_name": "AuthMiddleware"
}
```

**响应示例**：
```json
{
  "impacted_files": [
    "internal/httpx/router.go",
    "cmd/gateway/main.go"
  ],
  "impacted_functions": [
    "SetupRouter",
    "main"
  ],
  "dependency_chain": [
    "AuthMiddleware → SetupRouter → main"
  ]
}
```

---

### 技能搜索

#### `POST /api/mcp/skill/search`

搜索技能库。

**请求体**：
```json
{
  "query": "database migration",
  "limit": 5
}
```

**响应示例**：
```json
{
  "results": [
    {
      "id": "skill_abc123",
      "name": "db-migration",
      "description": "Database schema migration helper",
      "category": "database",
      "usage_count": 42
    }
  ]
}
```

---

### 绑定配置获取

#### `POST /api/mcp/binding/get`

获取绑定配置（会话级注入信息）。

**请求体**：
```json
{
  "conversation_id": "conv_xyz789"
}
```

**响应示例**：
```json
{
  "conversation_id": "conv_xyz789",
  "team_id": "team_abc123",
  "agent_id": "agent_def456",
  "identity_card_id": "id_card_ghi789",
  "injections": {
    "memory_paths": ["L1_长久记忆/project-context.md"],
    "wiki_refs": ["wiki_abc123"],
    "skill_ids": ["skill_def456"]
  }
}
```

---

### 资产列表

#### `POST /api/mcp/assets/list`

列出团队资产。

**请求体**：
```json
{
  "asset_type": "L1",
  "limit": 20,
  "offset": 0
}
```

**响应示例**：
```json
{
  "items": [
    {
      "id": "asset_abc123",
      "name": "project-context.md",
      "type": "L1",
      "path": "F:\\memory_plus\\L1_长久记忆\\project-context.md",
      "size": 1024,
      "created_at": "2026-08-10T08:00:00Z"
    }
  ],
  "total": 45,
  "limit": 20,
  "offset": 0
}
```

---

## 错误处理最佳实践

### 客户端处理建议

1. **重试逻辑**：
   - `500` / `503` 错误：指数退避重试 (最多 3 次)
   - `401` 错误：刷新 Token 或提示用户重新登录
   - `409` 错误：提示用户修改冲突字段 (如 slug)

2. **超时设置**：
   - 普通 API：30 秒
   - LLM 网关 API：120 秒 (流式响应可能较长)

3. **日志记录**：
   - 记录 `request_id` (响应头 `X-Request-ID`)
   - 便于问题排查和关联日志

### 服务端错误恢复

- **数据库连接失败**：自动重连 (最多 5 次，间隔 2s)
- **上游 LLM API 失败**：写入 outbox，异步重试
- **L0 写入失败**：降级到 buffer 文件，启动时回写

---

## 版本控制

**当前版本**：v1

**API 版本策略**：
- URL 路径版本：`/v1/messages`, `/v2/messages`
- 向后兼容：v1 API 保持稳定，新功能加入 v2
- 废弃策略：v1 废弃后保留 6 个月，提前通知用户

**变更日志**：见 `CHANGELOG.md`

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**维护者**: 首席架构师 高见远
