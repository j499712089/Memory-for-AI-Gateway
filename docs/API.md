# Memory Gateway API 使用文档

**面向用户的 API 使用指南** - 快速集成、认证、端点说明与示例。

---

## 目录

- [认证方式](#认证方式)
- [基础 URL](#基础-url)
- [请求格式](#请求格式)
- [响应格式](#响应格式)
- [错误处理](#错误处理)
- [管理 API](#管理-api)
- [网关 API](#网关-api)
- [MCP 内部 API](#mcp-内部-api)
- [完整示例](#完整示例)

---

## 认证方式

所有需认证的端点（除 `/health` 外）均使用 **Bearer Token** 认证。

### 获取 API Key

1. **通过 Web 面板创建**:
   - 访问 [http://localhost:5173/api-keys](http://localhost:5173/api-keys)
   - 点击「创建 API Key」
   - 选择团队和权限范围
   - 复制生成的密钥（仅此次可见）

2. **通过 API 创建**:

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

**响应示例**:

```json
{
  "id": "key_xyz789",
  "team_id": "team_abc123",
  "key": "sk_abc123def456...",
  "scopes": ["gateway", "mcp"],
  "enabled": true,
  "expires_at": "2027-12-31T00:00:00Z",
  "created_at": "2026-08-19T10:00:00Z"
}
```

> ⚠️ **重要**: `key` 字段仅在创建时返回，请妥善保存。

### 使用 API Key

在所有请求头中添加：

```http
Authorization: Bearer sk_abc123def456...
```

### 权限范围 (Scopes)

| Scope | 说明 | 允许访问 |
|-------|------|---------|
| `gateway` | 网关权限 | `/v1/*` 所有 LLM 端点 |
| `mcp` | MCP 权限 | `/api/mcp/*` 所有内部端点 |
| `admin` | 管理权限 | `/api/teams`, `/api/api-keys` |

---

## 基础 URL

**开发环境**: `http://127.0.0.1:8096`

**生产环境**: 根据你的部署配置（如 `https://gateway.your-domain.com`）

---

## 请求格式

### HTTP 方法

- **GET**: 查询资源
- **POST**: 创建资源或执行操作
- **PUT**: 更新资源（全量）
- **DELETE**: 删除/禁用资源

### Content-Type

所有 POST/PUT 请求均使用 JSON 格式：

```http
Content-Type: application/json
```

### 查询参数

分页参数（适用于列表端点）：

- `limit`: 每页数量（默认 20，最大 100）
- `offset`: 偏移量（默认 0）

示例：

```bash
curl "http://127.0.0.1:8096/api/teams?limit=10&offset=20" \
  -H "Authorization: Bearer sk_..."
```

---

## 响应格式

### 成功响应

**单个资源**:

```json
{
  "id": "team_abc123",
  "name": "Demo Team",
  "slug": "demo-team",
  "created_at": "2026-08-19T10:00:00Z"
}
```

**资源列表**:

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

### HTTP 状态码

| 状态码 | 说明 |
|--------|------|
| 200 OK | 请求成功 |
| 201 Created | 资源创建成功 |
| 204 No Content | 删除成功（无返回内容） |
| 400 Bad Request | 参数错误 |
| 401 Unauthorized | 认证失败 |
| 403 Forbidden | 权限不足 |
| 404 Not Found | 资源不存在 |
| 409 Conflict | 资源冲突（如 slug 重复） |
| 500 Internal Server Error | 服务器内部错误 |
| 503 Service Unavailable | 服务暂时不可用 |

---

## 错误处理

### 错误响应格式

```json
{
  "error": {
    "type": "error_type",
    "message": "human-readable error description",
    "request_id": "req_xyz789"
  }
}
```

### 错误类型

| Type | HTTP Status | 说明 | 处理建议 |
|------|-------------|------|---------|
| `invalid_request` | 400 | 参数错误、格式错误 | 检查请求参数 |
| `auth_error` | 401 | 认证失败、Token 无效 | 检查 API Key 是否有效 |
| `forbidden` | 403 | 权限不足 | 检查 Scope 是否包含所需权限 |
| `not_found` | 404 | 资源不存在 | 检查 ID 是否正确 |
| `conflict` | 409 | 资源冲突 | 修改冲突字段（如 slug） |
| `internal_error` | 500 | 服务器内部错误 | 联系管理员 |

### 错误示例

```json
{
  "error": {
    "type": "auth_error",
    "message": "invalid or expired API key",
    "request_id": "req_abc123"
  }
}
```

---

## 管理 API

### 健康检查

#### `GET /health`

获取服务健康状态（**无需认证**）。

**请求示例**:

```bash
curl http://127.0.0.1:8096/health
```

**响应示例**:

```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

**状态码**:
- `200 OK`: 服务正常
- `503 Service Unavailable`: 数据库连接失败

---

### 团队管理

#### `POST /api/teams`

创建新团队。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/teams \
  -H "Authorization: Bearer sk_your_admin_key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Demo Team",
    "slug": "demo-team",
    "description": "A demo team for testing",
    "visibility": "private"
  }'
```

**请求字段**:
- `name` (必填): 团队名称
- `slug` (必填): 团队唯一标识（URL 友好）
- `description` (可选): 团队描述
- `visibility` (可选): 可见性（`private` | `team` | `restricted`，默认 `private`）

**响应示例** (`201 Created`):

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

**错误响应**:
- `400 Bad Request`: 缺少必填字段
- `409 Conflict`: slug 已存在

---

#### `GET /api/teams`

列出所有团队（分页）。

**请求示例**:

```bash
curl "http://127.0.0.1:8096/api/teams?limit=20&offset=0" \
  -H "Authorization: Bearer sk_your_admin_key"
```

**查询参数**:
- `limit` (可选): 每页数量，默认 20，最大 100
- `offset` (可选): 偏移量，默认 0

**响应示例**:

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

**请求示例**:

```bash
curl http://127.0.0.1:8096/api/teams/team_abc123 \
  -H "Authorization: Bearer sk_your_admin_key"
```

**响应示例** (`200 OK`):

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

**错误响应**:
- `404 Not Found`: 团队不存在

---

### API Key 管理

#### `POST /api/api-keys`

创建新 API Key。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/api-keys \
  -H "Authorization: Bearer sk_your_admin_key" \
  -H "Content-Type: application/json" \
  -d '{
    "team_id": "team_abc123",
    "scopes": ["gateway", "mcp"],
    "expires_at": "2027-08-19T00:00:00Z"
  }'
```

**请求字段**:
- `team_id` (必填): 所属团队 ID
- `scopes` (可选): 权限范围数组，默认 `["gateway", "mcp"]`
- `expires_at` (可选): 过期时间（RFC3339 格式），不填则永不过期

**响应示例** (`201 Created`):

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

> ⚠️ **重要提示**: `key` 字段仅在创建时返回，后续无法再获取，请妥善保存。

---

#### `GET /api/api-keys`

列出 API Keys（分页）。

**请求示例**:

```bash
curl "http://127.0.0.1:8096/api/api-keys?team_id=team_abc123&limit=20" \
  -H "Authorization: Bearer sk_your_admin_key"
```

**查询参数**:
- `team_id` (可选): 按团队过滤
- `limit` (可选): 每页数量，默认 20
- `offset` (可选): 偏移量，默认 0

**响应示例**:

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

**注意**: 列表接口不返回完整 `key`，仅返回前缀 `key_prefix`（前 8 字符）。

---

### 录入健康看板

#### `GET /api/recording-health`

获取录入健康数据（Dashboard 用）。

**请求示例**:

```bash
curl "http://127.0.0.1:8096/api/recording-health?limit=50&terminal_status=complete" \
  -H "Authorization: Bearer sk_your_admin_key"
```

**查询参数**:
- `limit` (可选): 每页数量，默认 50，最大 100
- `offset` (可选): 偏移量，默认 0
- `terminal_status` (可选): 按终止状态过滤（`complete` | `failed` | `timeout`）

**响应示例**:

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

**字段说明**:
- `terminal_status`: Turn 终止状态（`complete` | `failed` | `timeout`）
- `compensation_status`: 补偿状态（`ok` | `pending`）
- `l0_path`: L0 文件路径
- `binding_version`: 绑定版本号

---

## 网关 API

### Anthropic Messages API

#### `POST /v1/messages`

代理 Anthropic Messages API。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer sk_your_gateway_key" \
  -H "Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000" \
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

**请求头**:
- `Authorization`: Bearer Token（必需）
- `Idempotency-Key`: UUID（可选，防止重复请求）
- `Content-Type`: `application/json`

**请求体**（与 Anthropic API 一致）:
- `model` (必填): 模型名称（如 `claude-opus-5`）
- `max_tokens` (必填): 最大生成 token 数
- `messages` (必填): 对话消息数组
  - `role`: `user` 或 `assistant`
  - `content`: 消息内容

**响应示例**:

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

**通道选择**:
- 默认使用 `default` 通道
- 可通过 URL 路径指定：`/claude-code/:channel/v1/messages`

示例：

```bash
curl -X POST http://127.0.0.1:8096/claude-code/my-channel/v1/messages \
  -H "Authorization: Bearer sk_..." \
  -H "Content-Type: application/json" \
  -d '{ ... }'
```

**流式响应**:

支持 SSE (Server-Sent Events) 流式响应，请求头添加：

```http
Accept: text/event-stream
```

示例：

```bash
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer sk_..." \
  -H "Accept: text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

---

### OpenAI Chat Completions API

#### `POST /codebuddy/:channel/v1/chat/completions`
#### `POST /dsh/:channel/v1/chat/completions`

代理 OpenAI Chat Completions API。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/codebuddy/default/v1/chat/completions \
  -H "Authorization: Bearer sk_your_gateway_key" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {
        "role": "user",
        "content": "Hello, GPT!"
      }
    ],
    "temperature": 0.7
  }'
```

**请求体**:
- `model` (必填): 模型名称（如 `gpt-4`）
- `messages` (必填): 对话消息数组
- `temperature` (可选): 温度参数（0-2，默认 1）
- `max_tokens` (可选): 最大生成 token 数
- `stream` (可选): 是否流式响应（默认 false）

**响应示例**:

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

**通道路径**:
- CodeBuddy: `/codebuddy/:channel/v1/chat/completions`
- DSH: `/dsh/:channel/v1/chat/completions`

---

### Codex Responses API

#### `POST /codex/:channel/v1/responses`
#### `POST /codex/:channel/responses`

代理 Codex Responses API（OpenClaw 专用）。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/codex/default/v1/responses \
  -H "Authorization: Bearer sk_your_gateway_key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex-model",
    "prompt": "Write a Python function to reverse a string",
    "max_tokens": 256
  }'
```

**请求体**:
- `model` (必填): 模型名称
- `prompt` (必填): 提示词
- `max_tokens` (可选): 最大生成 token 数

**响应示例**:

```json
{
  "id": "resp_abc123",
  "model": "codex-model",
  "response": "def reverse_string(s):\\n    return s[::-1]",
  "usage": {
    "prompt_tokens": 15,
    "completion_tokens": 20
  }
}
```

**通道路径**:
- `/codex/:channel/v1/responses`（推荐）
- `/codex/:channel/responses`（兼容旧版无 `/v1` 前缀）

---

## MCP 内部 API

以下 API 供 MCP Server (`:8097`) 调用，需 `mcp` scope。

### Memory 搜索

#### `POST /api/mcp/memory/search`

搜索 Memory 资产。

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/search \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "authentication implementation",
    "limit": 10
  }'
```

**请求体**:
- `query` (必填): 搜索关键词
- `limit` (可选): 返回数量（默认 10）

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/get \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "mem_abc123"
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/append \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "mem_abc123",
    "content": "Additional notes: Remember to invalidate cache on logout."
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/wiki/search \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "deployment guide",
    "limit": 5
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/codegraph/impact \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "file_path": "internal/auth/middleware.go",
    "function_name": "AuthMiddleware"
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/skill/search \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "database migration",
    "limit": 5
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/binding/get \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "conversation_id": "conv_xyz789"
  }'
```

**响应示例**:

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

**请求示例**:

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/assets/list \
  -H "Authorization: Bearer sk_your_mcp_key" \
  -H "Content-Type: application/json" \
  -d '{
    "asset_type": "L1",
    "limit": 20,
    "offset": 0
  }'
```

**响应示例**:

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

## 完整示例

### 示例 1: 创建团队并调用 LLM

```bash
# 1. 创建团队
TEAM_RESPONSE=$(curl -s -X POST http://127.0.0.1:8096/api/teams \
  -H "Authorization: Bearer sk_admin_key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Project",
    "slug": "my-project",
    "visibility": "private"
  }')

TEAM_ID=$(echo $TEAM_RESPONSE | jq -r '.id')
echo "Created team: $TEAM_ID"

# 2. 创建 API Key
KEY_RESPONSE=$(curl -s -X POST http://127.0.0.1:8096/api/api-keys \
  -H "Authorization: Bearer sk_admin_key" \
  -H "Content-Type: application/json" \
  -d "{
    \"team_id\": \"$TEAM_ID\",
    \"scopes\": [\"gateway\"]
  }")

API_KEY=$(echo $KEY_RESPONSE | jq -r '.key')
echo "Created API Key: $API_KEY"

# 3. 调用 LLM
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Explain quantum computing in simple terms"}
    ]
  }'
```

### 示例 2: 幂等性请求

```bash
# 生成幂等性 Key
IDEMPOTENCY_KEY=$(uuidgen)

# 第一次请求（调用上游 LLM）
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer sk_your_key" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# 第二次请求（返回缓存，不调用上游）
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer sk_your_key" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### 示例 3: 查询录入健康

```bash
# 查询最近 10 条失败的录入记录
curl "http://127.0.0.1:8096/api/recording-health?limit=10&terminal_status=failed" \
  -H "Authorization: Bearer sk_admin_key" \
  | jq '.items[] | {request_id, terminal_status, l0_path}'
```

---

## 最佳实践

### 1. 重试逻辑

```javascript
async function callGateway(payload, maxRetries = 3) {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch('http://127.0.0.1:8096/v1/messages', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${API_KEY}`,
          'Content-Type': 'application/json',
          'Idempotency-Key': generateUUID()
        },
        body: JSON.stringify(payload)
      });
      
      if (response.ok) {
        return await response.json();
      }
      
      // 5xx 错误重试
      if (response.status >= 500 && i < maxRetries - 1) {
        await sleep(2 ** i * 1000); // 指数退避
        continue;
      }
      
      // 其他错误直接抛出
      const error = await response.json();
      throw new Error(error.error.message);
    } catch (err) {
      if (i === maxRetries - 1) throw err;
    }
  }
}
```

### 2. 超时设置

```javascript
const controller = new AbortController();
const timeout = setTimeout(() => controller.abort(), 120000); // 120 秒

try {
  const response = await fetch('http://127.0.0.1:8096/v1/messages', {
    method: 'POST',
    headers: { /* ... */ },
    body: JSON.stringify(payload),
    signal: controller.signal
  });
  clearTimeout(timeout);
  return await response.json();
} catch (err) {
  clearTimeout(timeout);
  throw err;
}
```

### 3. 错误日志记录

```javascript
try {
  const response = await fetch(/* ... */);
  const data = await response.json();
  
  if (!response.ok) {
    console.error('API Error:', {
      status: response.status,
      type: data.error.type,
      message: data.error.message,
      request_id: data.error.request_id // 用于排查问题
    });
  }
} catch (err) {
  console.error('Network Error:', err);
}
```

---

## 常见问题

### Q1: 如何获取初始的 Admin API Key？

**答**: 首次启动 Gateway 时，会自动生成一个 Admin API Key 并输出到日志：

```
INFO: Created initial admin API key: sk_abc123def456...
```

请妥善保存此密钥，用于后续创建团队和用户 API Key。

### Q2: API Key 泄露怎么办？

**答**: 立即禁用该 API Key：

```bash
curl -X PUT http://127.0.0.1:8096/api/api-keys/<key_id> \
  -H "Authorization: Bearer sk_admin_key" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

然后创建新的 API Key 并更新客户端配置。

### Q3: 如何限制 API Key 的访问权限？

**答**: 通过 `scopes` 字段控制：

- 仅网关权限: `["gateway"]`
- 仅 MCP 权限: `["mcp"]`
- 管理权限: `["admin"]`
- 多权限组合: `["gateway", "mcp"]`

### Q4: 幂等性 Key 必须传吗？

**答**: 不是必须的，但强烈建议传递：

- **不传**: 每次请求都会调用上游 LLM（可能产生重复扣费）
- **传递**: 24 小时内相同 Key 返回缓存（避免重复扣费）

---

## 环境变量配置

详见项目根目录的 `.env.example` 文件。

**核心配置**:

```env
MEMORY_PLUS_DIR=F:\memory_plus    # 数据根目录
GATEWAY_PORT=8096                 # 服务端口
SECRETS_DIR=F:\memory_plus\.runtime\secrets  # 上游 API Key 存储目录
LOG_LEVEL=info                    # 日志级别（debug/info/warn/error）
```

**完整配置项**: 见 `.env.example` 文件注释。

---

## 联系支持

- **GitHub Issues**: [https://github.com/j499712089/Memory-for-AI/issues](https://github.com/j499712089/Memory-for-AI/issues)
- **邮箱**: 861892722@qq.com
- **架构文档**: [docs/03_api_design.md](03_api_design.md)（面向开发者）

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**最后更新**: 2026-08-19
