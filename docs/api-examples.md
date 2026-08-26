# Memory Gateway API 使用示例

本文档为每个端点提供一个 curl 示例（含认证头），并附一个端到端示例：
创建团队 -> 创建 API Key -> 调用 `/v1/messages` -> 验证幂等性。

> 基础 URL：`http://127.0.0.1:8096`（生产环境按部署配置替换）。
> 认证：`Authorization: Bearer <api_key>`。MCP 端点要求 API Key 的 scopes 包含 `mcp`。

## 1. 健康检查（无需认证）

```bash
curl http://127.0.0.1:8096/health
```

## 2. 团队管理

### 创建团队

```bash
curl -X POST http://127.0.0.1:8096/api/teams \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Demo Team",
    "slug": "demo-team",
    "description": "A demo team for testing",
    "visibility": "private"
  }'
```

### 列出团队

```bash
curl http://127.0.0.1:8096/api/teams \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 获取团队详情

```bash
curl http://127.0.0.1:8096/api/teams/team_abc123 \
  -H "Authorization: Bearer $ADMIN_KEY"
```

## 3. API Key 管理

### 创建 API Key（明文仅此一次返回）

```bash
curl -X POST http://127.0.0.1:8096/api/api-keys \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "team_id": "team_abc123",
    "scopes": ["gateway", "mcp"]
  }'
```

### 列出 API Keys

```bash
curl "http://127.0.0.1:8096/api/api-keys?team_id=team_abc123" \
  -H "Authorization: Bearer $ADMIN_KEY"
```

## 4. 录入健康看板

```bash
curl "http://127.0.0.1:8096/api/recording-health?limit=10&terminal_status=failed" \
  -H "Authorization: Bearer $ADMIN_KEY"
```

## 5. 系统管理

### 服务状态

```bash
curl http://127.0.0.1:8096/api/system/service/status \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 启动服务

```bash
curl -X POST http://127.0.0.1:8096/api/system/service/start \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 停止服务

```bash
curl -X POST http://127.0.0.1:8096/api/system/service/stop \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 重启服务

```bash
curl -X POST http://127.0.0.1:8096/api/system/service/restart \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 配置开机自启动

```bash
curl -X PUT http://127.0.0.1:8096/api/system/autostart \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

### 获取系统日志

```bash
curl "http://127.0.0.1:8096/api/system/logs?limit=50" \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### 导出配置备份

```bash
curl -X POST http://127.0.0.1:8096/api/system/backup \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -o gateway-config.json
```

### 导入配置备份

```bash
curl -X POST http://127.0.0.1:8096/api/system/restore \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  --data-binary @gateway-config.json
```

### 获取系统配置

```bash
curl http://127.0.0.1:8096/api/system/config \
  -H "Authorization: Bearer $ADMIN_KEY"
```

## 6. 身份卡 Agent 绑定

### 绑定 Agent

```bash
curl -X POST http://127.0.0.1:8096/api/identity-cards/id_card_ghi789/bind-agent \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "agent_def456"}'
```

### 测试 Agent 连接

```bash
curl -X POST http://127.0.0.1:8096/api/identity-cards/id_card_ghi789/test-connection \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{}'
```

## 7. MCP 内部 API（需 mcp scope）

### 记忆搜索

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/search \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "authentication implementation",
    "limit": 10
  }'
```

### 记忆获取

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/get \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"asset_id": "asset_abc123"}'
```

### 记忆追加（唯一写工具）

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/memory/append \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Additional notes: remember to invalidate cache on logout.",
    "source_ids": ["evt_xxx"],
    "confidence": 0.8,
    "visibility": "private"
  }'
```

### Wiki 搜索

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/wiki/search \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "deployment guide",
    "limit": 5
  }'
```

### 代码影响分析

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/codegraph/impact \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "repo_id": "repo_main",
    "symbol": "AuthMiddleware",
    "file_path": "internal/auth/middleware.go",
    "depth": 3
  }'
```

### 技能搜索

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/skill/search \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"query": "database migration"}'
```

### 绑定信息获取

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/binding/get \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"conversation_id": "conv_xyz789"}'
```

### 资产列表

```bash
curl -X POST http://127.0.0.1:8096/api/mcp/assets/list \
  -H "Authorization: Bearer $MCP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "asset_type": "l1",
    "limit": 20,
    "offset": 0
  }'
```

## 8. 网关 LLM 端点

### Anthropic Messages（默认通道）

```bash
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Explain quantum computing in simple terms"}
    ]
  }'
```

### Anthropic Messages（指定通道）

```bash
curl -X POST http://127.0.0.1:8096/claude-code/my-channel/v1/messages \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### Chat Completions（CodeBuddy / DSH）

```bash
curl -X POST http://127.0.0.1:8096/codebuddy/default/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {"role": "user", "content": "Hello, GPT!"}
    ],
    "temperature": 0.7
  }'
```

### Responses（Codex，兼容无 /v1 前缀）

```bash
curl -X POST http://127.0.0.1:8096/codex/default/v1/responses \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex-model",
    "instructions": "You are a helpful assistant.",
    "input": "Write a Python function to reverse a string"
  }'
```

### 流式响应（SSE）

```bash
curl -N -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Accept: text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

## 9. 端到端示例：创建 API Key -> 调用 /v1/messages -> 验证幂等性

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE="http://127.0.0.1:8096"
ADMIN_KEY="sk_your_admin_key"   # 首次启动时由 Gateway 输出到日志

# 1. 创建团队
TEAM_ID=$(curl -s -X POST "$BASE/api/teams" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "My Project", "slug": "my-project", "visibility": "private"}' \
  | python -c "import json,sys; print(json.load(sys.stdin)['id'])")
echo "Team created: $TEAM_ID"

# 2. 创建 API Key（明文仅此一次返回）
KEY_RESPONSE=$(curl -s -X POST "$BASE/api/api-keys" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"team_id\": \"$TEAM_ID\", \"scopes\": [\"gateway\"]}")
GATEWAY_KEY=$(echo "$KEY_RESPONSE" | python -c "import json,sys; print(json.load(sys.stdin)['key'])")
echo "API Key created: $GATEWAY_KEY"

# 3. 生成幂等性 Key
IDEMPOTENCY_KEY=$(python -c "import uuid; print(uuid.uuid4())")

# 4. 第一次请求（调用上游 LLM）
echo "--- Request 1 (upstream) ---"
curl -s -X POST "$BASE/v1/messages" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-opus-5", "max_tokens": 100, "messages": [{"role": "user", "content": "Hello"}]}' \
  -D - -o /tmp/req1.json
echo "Response headers saved; body saved to /tmp/req1.json"

# 5. 第二次请求（相同 Key + 相同请求体，返回缓存，不调用上游）
echo "--- Request 2 (cached) ---"
curl -s -X POST "$BASE/v1/messages" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-opus-5", "max_tokens": 100, "messages": [{"role": "user", "content": "Hello"}]}' \
  -D - -o /tmp/req2.json

# 6. 幂等性冲突示例：相同 Key 但请求体不同 -> 409
echo "--- Request 3 (conflict, expect 409) ---"
curl -s -X POST "$BASE/v1/messages" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-opus-5", "max_tokens": 100, "messages": [{"role": "user", "content": "Different content"}]}' \
  -w "\nHTTP %{http_code}\n"
```

幂等性语义：

- 相同 `Idempotency-Key` + 相同请求体：24 小时内第二次请求直接返回第一次的缓存响应（HTTP 200），不重复调用上游 LLM。
- 相同 `Idempotency-Key` + 不同请求体：返回 `409 idempotency_conflict`。
- 请求正在处理中时复用同一 Key：返回 `409 idempotency_conflict`。
- 不传 `Idempotency-Key`：每次请求都会调用上游 LLM。
