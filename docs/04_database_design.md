# 数据库设计文档

## 概述

Memory Gateway 采用 **SQLite 3 (WAL 模式)** 作为数据库，分为两类：
1. **全局库** (`memory-gateway.db`)：存储跨团队数据（团队/API Key/会话/账本）
2. **团队库** (`teams/{team_id}/memory.db`)：存储团队私有数据（资产/Wiki/CodeGraph/Skill）

## 数据库配置

### PRAGMA 设置

```sql
-- 所有数据库统一配置
PRAGMA journal_mode = WAL;           -- 并发读写
PRAGMA foreign_keys = ON;            -- 外键约束
PRAGMA busy_timeout = 5000;          -- 写锁等待 5s
PRAGMA synchronous = NORMAL;         -- WAL 下安全且高效
PRAGMA wal_autocheckpoint = 1000;    -- 每 1000 页触发 checkpoint
```

**WAL 模式优势**：
- 并发读写：多个读者不阻塞写者
- 性能优异：写入不触发全库 checkpoint
- 崩溃恢复：WAL 文件自动回放

### 连接池配置

```go
db.SetMaxOpenConns(25)      // 最大连接数
db.SetMaxIdleConns(5)       // 最大空闲连接
db.SetConnMaxLifetime(5m)   // 连接最大生命周期
```

## 全局库 Schema

### 1. 团队与成员

#### teams 表

存储团队基本信息。

```sql
CREATE TABLE IF NOT EXISTS teams (
    id           TEXT PRIMARY KEY,              -- UUID
    name         TEXT NOT NULL,
    slug         TEXT NOT NULL UNIQUE,          -- URL 友好标识
    description  TEXT DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'private' 
                   CHECK (visibility IN ('private','team','restricted')),
    status       TEXT NOT NULL DEFAULT 'active' 
                   CHECK (status IN ('active','disabled','archived')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
```

**字段说明**：
- `id`：稳定 UUID，用于文件系统路径 `teams/{team_id}/`
- `slug`：唯一标识，用于 URL（如 `/teams/demo-team`）
- `visibility`：可见性级别
  - `private`：仅团队成员可见
  - `team`：团队内可见
  - `restricted`：受限访问
- `status`：团队状态
  - `active`：正常
  - `disabled`：已禁用
  - `archived`：已归档

**索引**：
- 主键：`id` (自动索引)
- 唯一索引：`slug`

---

#### team_members 表

存储团队成员关系。

```sql
CREATE TABLE IF NOT EXISTS team_members (
    team_id      TEXT NOT NULL REFERENCES teams(id),
    user_id      TEXT NOT NULL,                 -- 用户/代理全局标识
    member_type  TEXT NOT NULL CHECK (member_type IN ('user','agent')),
    role         TEXT NOT NULL DEFAULT 'member' 
                   CHECK (role IN ('owner','admin','member')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (team_id, user_id)
);
```

**字段说明**：
- `member_type`：成员类型
  - `user`：人类用户
  - `agent`：AI 代理
- `role`：角色权限
  - `owner`：所有者（创建者）
  - `admin`：管理员
  - `member`：普通成员

**索引**：
- 复合主键：`(team_id, user_id)`
- 外键：`team_id → teams(id)`

---

### 2. 代理与身份卡

#### agents 表

存储 AI 代理信息。

```sql
CREATE TABLE IF NOT EXISTS agents (
    id                TEXT PRIMARY KEY,         -- agent_id
    team_id           TEXT REFERENCES teams(id),
    name              TEXT NOT NULL,
    runtime           TEXT,                     -- claude-code/codebuddy/codex/dsh/hermes/openclaw
    model_ref         TEXT,                     -- models.json 中的模型 id
    identity_card_id  TEXT,                     -- 绑定身份卡
    status            TEXT NOT NULL DEFAULT 'active' 
                        CHECK (status IN ('active','disabled')),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
```

**字段说明**：
- `runtime`：运行时类型
  - `claude-code`：Claude Code
  - `codebuddy`：CodeBuddy
  - `codex`：Codex
  - `dsh`：DSH
  - `hermes`：Hermes
  - `openclaw`：OpenClaw
- `model_ref`：模型引用（不存密钥，仅存 ID）
- `identity_card_id`：绑定的身份卡 ID

**索引**：
- 主键：`id`
- 外键：`team_id → teams(id)`

---

#### identity_cards 表

存储身份卡（L3 资产）。

```sql
CREATE TABLE IF NOT EXISTS identity_cards (
    id                TEXT PRIMARY KEY,
    team_id           TEXT REFERENCES teams(id),
    agent_id          TEXT REFERENCES agents(id),
    name              TEXT NOT NULL,
    role              TEXT NOT NULL,
    responsibilities  TEXT DEFAULT '',
    boundaries        TEXT DEFAULT '',
    allowed_tools     TEXT DEFAULT '',          -- JSON 数组
    style             TEXT DEFAULT '',
    visibility        TEXT NOT NULL DEFAULT 'agent'
                        CHECK (visibility IN ('private','team','restricted','agent')),
    version           INTEGER NOT NULL DEFAULT 1,   -- 乐观锁
    status            TEXT NOT NULL DEFAULT 'active' 
                        CHECK (status IN ('draft','active','archived')),
    source_event_ids  TEXT DEFAULT '[]',        -- JSON 数组
    body_path         TEXT,                     -- L3 下的 Markdown 路径
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
```

**字段说明**：
- `allowed_tools`：允许使用的工具列表（JSON 数组）
- `version`：版本号，用于乐观锁（并发控制）
- `body_path`：Markdown 正文路径（实际内容存文件系统）
- `source_event_ids`：来源事件 ID（追溯性）

**索引**：
- 主键：`id`
- 外键：`team_id → teams(id)`, `agent_id → agents(id)`

---

### 3. API Key 管理

#### api_keys 表

存储 API Key 元数据（不存明文密钥）。

```sql
CREATE TABLE IF NOT EXISTS api_keys (
    id            TEXT PRIMARY KEY,
    team_id       TEXT REFERENCES teams(id),
    owner_id      TEXT,                         -- 创建者 agent/user
    key_hash      TEXT NOT NULL UNIQUE,         -- sha256(bearer key)
    key_ref       TEXT NOT NULL,                -- DPAPI/secret 服务引用名
    scopes        TEXT NOT NULL DEFAULT '["gateway","mcp"]', -- JSON 数组
    enabled       INTEGER NOT NULL DEFAULT 1,
    expires_at    TEXT,
    revoked_at    TEXT,
    last_used_at  TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_api_keys_team ON api_keys(team_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_owner ON api_keys(owner_id);
```

**字段说明**：
- `key_hash`：密钥哈希（SHA-256），用于校验
- `key_ref`：密钥引用（DPAPI 文件名或密钥服务 ID），**不存明文**
- `scopes`：权限范围（JSON 数组，如 `["gateway","mcp"]`）
- `enabled`：是否启用（0=禁用，1=启用）
- `revoked_at`：吊销时间（非空表示已吊销）

**索引**：
- 主键：`id`
- 唯一索引：`key_hash`
- 外键索引：`team_id`, `owner_id`

---

### 4. 上游通道

#### upstream_channels 表

存储上游 LLM 通道配置。

```sql
CREATE TABLE IF NOT EXISTS upstream_channels (
    id                  TEXT PRIMARY KEY,
    team_id             TEXT REFERENCES teams(id),
    name                TEXT NOT NULL,
    protocol            TEXT NOT NULL CHECK (protocol IN
                          ('anthropic_messages','chat_completions','responses')),
    base_url            TEXT NOT NULL,          -- 如 https://api.deepseek.com/v1
    model               TEXT NOT NULL,          -- 模型名
    api_key_ref         TEXT NOT NULL,          -- 凭据引用（不存明文）
    capabilities_json   TEXT NOT NULL DEFAULT '{}',  -- supportsToolCall/supportsImages/maxInputTokens...
    priority            INTEGER NOT NULL DEFAULT 100,  -- 数字越小越优先
    enabled             INTEGER NOT NULL DEFAULT 1,
    failover_channel_id TEXT REFERENCES upstream_channels(id),
    source              TEXT NOT NULL DEFAULT 'models.json',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_upstream_team_proto ON upstream_channels(team_id, protocol);
CREATE INDEX IF NOT EXISTS idx_upstream_enabled ON upstream_channels(enabled, priority);
```

**字段说明**：
- `protocol`：协议类型
  - `anthropic_messages`：Anthropic Messages API
  - `chat_completions`：OpenAI Chat Completions API
  - `responses`：Codex Responses API
- `capabilities_json`：能力描述（JSON 对象）
  ```json
  {
    "supportsToolCall": true,
    "supportsImages": true,
    "supportsReasoning": true,
    "maxInputTokens": 200000,
    "maxOutputTokens": 8192
  }
  ```
- `priority`：优先级（数字越小越优先，用于多通道选择）
- `failover_channel_id`：故障转移通道 ID

**索引**：
- 主键：`id`
- 复合索引：`(team_id, protocol)` （查询团队的某协议通道）
- 复合索引：`(enabled, priority)` （选择可用通道）

---

#### channel_aliases 表

协议感知的通道别名映射。

```sql
CREATE TABLE IF NOT EXISTS channel_aliases (
    id                TEXT PRIMARY KEY,
    team_id           TEXT REFERENCES teams(id),
    alias_name        TEXT NOT NULL,        -- 客户端使用的通道名（如 default）
    protocol          TEXT NOT NULL CHECK (protocol IN
                          ('anthropic_messages','chat_completions','responses')),
    target_channel_id TEXT NOT NULL REFERENCES upstream_channels(id),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE(team_id, alias_name, protocol)
);

CREATE INDEX IF NOT EXISTS idx_channel_aliases_team_alias_proto 
  ON channel_aliases(team_id, alias_name, protocol);
```

**用途**：
解决运行时固定通道名（如 Codex Runtime 固定使用 `"default"`）与数据库通道协议不匹配的问题。

**查询逻辑**：
```sql
-- 客户端请求：protocol=responses, channel_name="default"
SELECT target_channel_id 
FROM channel_aliases 
WHERE team_id = ? AND alias_name = 'default' AND protocol = 'responses';

-- 若未找到别名，则直接查 upstream_channels
SELECT id FROM upstream_channels 
WHERE team_id = ? AND name = 'default' AND protocol = 'responses';
```

---

### 5. 会话与账本

#### sessions 表

存储会话元数据。

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id               TEXT PRIMARY KEY,
    team_id          TEXT REFERENCES teams(id),
    agent_id         TEXT REFERENCES agents(id),
    conversation_id  TEXT NOT NULL,
    runtime          TEXT,
    model_ref        TEXT,
    binding_version  INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'active' 
                       CHECK (status IN ('active','completed','failed','timeout')),
    started_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    completed_at     TEXT,
    metadata_json    TEXT DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_sessions_conversation ON sessions(conversation_id);
CREATE INDEX IF NOT EXISTS idx_sessions_team ON sessions(team_id);
```

**字段说明**：
- `conversation_id`：会话唯一标识（Claude Desktop 的 conversation ID）
- `binding_version`：绑定版本（注入信息版本号）
- `metadata_json`：会话元数据（JSON 对象）

---

#### turn_ledger 表

Turn 账本，记录每个 LLM 请求的完整信息。

```sql
CREATE TABLE IF NOT EXISTS turn_ledger (
    turn_id            TEXT PRIMARY KEY,
    request_id         TEXT NOT NULL UNIQUE,
    conversation_id    TEXT NOT NULL,
    session_id         TEXT REFERENCES sessions(id),
    team_id            TEXT REFERENCES teams(id),
    agent_id           TEXT REFERENCES agents(id),
    inbound_event_id   TEXT,
    outbound_event_id  TEXT,
    protocol           TEXT NOT NULL,
    upstream_channel_id TEXT REFERENCES upstream_channels(id),
    model              TEXT,
    content_hash       TEXT,                     -- SHA-256(request+response)
    l0_request_path    TEXT,                     -- L0 文件路径
    l0_response_path   TEXT,
    recording_state    TEXT NOT NULL DEFAULT 'pending'
                         CHECK (recording_state IN ('pending','complete','compensating','degraded')),
    final_status       TEXT CHECK (final_status IN ('complete','failed','timeout')),
    completed_at       TEXT,
    binding_version    INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_turn_ledger_request ON turn_ledger(request_id);
CREATE INDEX IF NOT EXISTS idx_turn_ledger_conversation ON turn_ledger(conversation_id);
CREATE INDEX IF NOT EXISTS idx_turn_ledger_session ON turn_ledger(session_id);
```

**字段说明**：
- `turn_id`：Turn 唯一 ID
- `request_id`：请求 ID（幂等性 Key）
- `recording_state`：录入状态
  - `pending`：等待录入
  - `complete`：已完成
  - `compensating`：补偿中（outbox 重试）
  - `degraded`：降级（buffer 文件）
- `final_status`：最终状态
  - `complete`：成功
  - `failed`：失败
  - `timeout`：超时

---

#### turn_events 表

Turn 事件流。

```sql
CREATE TABLE IF NOT EXISTS turn_events (
    id              TEXT PRIMARY KEY,
    turn_id         TEXT NOT NULL REFERENCES turn_ledger(turn_id),
    event_type      TEXT NOT NULL CHECK (event_type IN 
                      ('inbound','outbound','error','compensation')),
    payload_json    TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_turn_events_turn ON turn_events(turn_id);
```

**事件类型**：
- `inbound`：入站请求
- `outbound`：出站响应
- `error`：错误事件
- `compensation`：补偿事件

---

### 6. 可靠性保障

#### idempotency_cache 表

幂等性缓存。

```sql
CREATE TABLE IF NOT EXISTS idempotency_cache (
    key            TEXT PRIMARY KEY,
    response_body  TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_idempotency_expires ON idempotency_cache(expires_at);
```

**字段说明**：
- `key`：幂等性 Key（Idempotency-Key 请求头）
- `response_body`：缓存的响应体（JSON 字符串）
- `expires_at`：过期时间（24 小时）

**清理策略**：
```sql
-- 定期删除过期记录
DELETE FROM idempotency_cache WHERE expires_at < datetime('now');
```

---

#### outbox 表

外发队列（重试机制）。

```sql
CREATE TABLE IF NOT EXISTS outbox (
    id              TEXT PRIMARY KEY,
    event_type      TEXT NOT NULL CHECK (event_type IN ('recording','notification')),
    payload_json    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','processing','completed','failed')),
    retry_count     INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL DEFAULT 5,
    next_retry_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    completed_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_next_retry ON outbox(next_retry_at);
CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status);
```

**字段说明**：
- `event_type`：事件类型
  - `recording`：录入事件（L0 文件写入失败）
  - `notification`：通知事件（未来扩展）
- `retry_count`：已重试次数
- `max_retries`：最大重试次数（默认 5）
- `next_retry_at`：下次重试时间（指数退避：2^n 秒）

**Worker 查询**：
```sql
-- 查询待重试事件
SELECT * FROM outbox 
WHERE status = 'pending' 
  AND next_retry_at <= datetime('now')
ORDER BY created_at 
LIMIT 10;
```

---

#### buffer_台账 表

降级缓冲台账（离线持久化）。

```sql
CREATE TABLE IF NOT EXISTS buffer_台账 (
    id              TEXT PRIMARY KEY,
    buffer_file     TEXT NOT NULL,             -- 缓冲文件路径
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','replayed','failed')),
    replayed_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_buffer_status ON buffer_台账(status);
```

**用途**：
SQLite 写入超时时，降级到文件系统写 buffer 文件，启动时回写数据库。

**文件路径示例**：
```
F:\memory_plus\.runtime\buffers\2026-08-19\buffer_abc123.jsonl
```

---

#### recording_health 表

录入健康看板（Dashboard 用）。

```sql
CREATE TABLE IF NOT EXISTS recording_health (
    id                 TEXT PRIMARY KEY,
    request_id         TEXT NOT NULL UNIQUE,
    conversation_id    TEXT NOT NULL,
    session_id         TEXT,
    inbound_at         TEXT,
    terminal_at        TEXT,
    terminal_status    TEXT,
    content_hash       TEXT,
    l0_path            TEXT,
    binding_version    INTEGER,
    compensation_status TEXT NOT NULL DEFAULT 'ok'
                         CHECK (compensation_status IN ('ok','pending')),
    last_success_at    TEXT,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_recording_health_status ON recording_health(terminal_status);
```

**投影逻辑**：
从 `turn_ledger` 和 `turn_events` 聚合而来，定期更新（见 `health_handler.go:projectLedgerHealth`）。

---

### 7. 异步任务

#### task_queue 表

全局任务队列。

```sql
CREATE TABLE IF NOT EXISTS task_queue (
    id              TEXT PRIMARY KEY,
    task_type       TEXT NOT NULL,
    payload_json    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','processing','completed','failed')),
    priority        INTEGER NOT NULL DEFAULT 100,
    worker_id       TEXT,
    lease_expires_at TEXT,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL DEFAULT 3,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    completed_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_task_queue_status_priority ON task_queue(status, priority);
CREATE INDEX IF NOT EXISTS idx_task_queue_lease ON task_queue(lease_expires_at);
```

**字段说明**：
- `worker_id`：占用该任务的 Worker ID
- `lease_expires_at`：租约过期时间（Worker 崩溃时自动释放）
- `priority`：优先级（数字越小越优先）

**Worker 查询**：
```sql
-- 获取任务（带租约）
UPDATE task_queue 
SET status = 'processing', 
    worker_id = ?, 
    lease_expires_at = datetime('now', '+5 minutes')
WHERE id IN (
  SELECT id FROM task_queue 
  WHERE status = 'pending'
  ORDER BY priority, created_at 
  LIMIT 1
)
RETURNING *;
```

---

## 团队库 Schema

### 1. 资产管理

#### assets 表

资产清单。

```sql
CREATE TABLE IF NOT EXISTS assets (
    id              TEXT PRIMARY KEY,
    asset_type      TEXT NOT NULL CHECK (asset_type IN ('L0','L1','L2','L3')),
    name            TEXT NOT NULL,
    path            TEXT NOT NULL,             -- 文件系统路径
    content_hash    TEXT,
    size_bytes      INTEGER,
    visibility      TEXT NOT NULL DEFAULT 'team'
                      CHECK (visibility IN ('private','team','restricted')),
    status          TEXT NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','archived','deleted')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_assets_type ON assets(asset_type);
CREATE INDEX IF NOT EXISTS idx_assets_status ON assets(status);
```

---

#### asset_citations 表

正文引用。

```sql
CREATE TABLE IF NOT EXISTS asset_citations (
    id              TEXT PRIMARY KEY,
    asset_id        TEXT NOT NULL REFERENCES assets(id),
    cited_asset_id  TEXT NOT NULL REFERENCES assets(id),
    citation_type   TEXT NOT NULL CHECK (citation_type IN ('reference','dependency','related')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_citations_asset ON asset_citations(asset_id);
CREATE INDEX IF NOT EXISTS idx_citations_cited ON asset_citations(cited_asset_id);
```

---

### 2. Wiki

#### wikis 表

```sql
CREATE TABLE IF NOT EXISTS wikis (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    summary         TEXT,
    body_path       TEXT NOT NULL,
    category        TEXT,
    tags            TEXT DEFAULT '[]',         -- JSON 数组
    visibility      TEXT NOT NULL DEFAULT 'team'
                      CHECK (visibility IN ('private','team','restricted')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_wikis_slug ON wikis(slug);
CREATE INDEX IF NOT EXISTS idx_wikis_category ON wikis(category);
```

---

### 3. 代码图谱

#### codegraph_nodes 表

```sql
CREATE TABLE IF NOT EXISTS codegraph_nodes (
    id              TEXT PRIMARY KEY,
    node_type       TEXT NOT NULL CHECK (node_type IN ('file','function','class','module')),
    name            TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    line_start      INTEGER,
    line_end        INTEGER,
    signature       TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_codegraph_nodes_file ON codegraph_nodes(file_path);
```

---

#### codegraph_edges 表

```sql
CREATE TABLE IF NOT EXISTS codegraph_edges (
    id              TEXT PRIMARY KEY,
    from_node_id    TEXT NOT NULL REFERENCES codegraph_nodes(id),
    to_node_id      TEXT NOT NULL REFERENCES codegraph_nodes(id),
    edge_type       TEXT NOT NULL CHECK (edge_type IN ('calls','imports','extends','implements')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_codegraph_edges_from ON codegraph_edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_codegraph_edges_to ON codegraph_edges(to_node_id);
```

---

### 4. 技能库

#### skills 表

```sql
CREATE TABLE IF NOT EXISTS skills (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT,
    category        TEXT,
    body_path       TEXT NOT NULL,
    usage_count     INTEGER NOT NULL DEFAULT 0,
    visibility      TEXT NOT NULL DEFAULT 'team'
                      CHECK (visibility IN ('private','team','restricted')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_skills_name ON skills(name);
CREATE INDEX IF NOT EXISTS idx_skills_category ON skills(category);
```

---

### 5. 绑定配置

#### bindings 表

会话级注入绑定。

```sql
CREATE TABLE IF NOT EXISTS bindings (
    id                TEXT PRIMARY KEY,
    conversation_id   TEXT NOT NULL UNIQUE,
    team_id           TEXT NOT NULL,
    agent_id          TEXT,
    identity_card_id  TEXT,
    memory_paths      TEXT DEFAULT '[]',       -- JSON 数组
    wiki_refs         TEXT DEFAULT '[]',
    skill_ids         TEXT DEFAULT '[]',
    version           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_bindings_conversation ON bindings(conversation_id);
```

---

### 6. 权限控制

#### asset_acl 表

资产访问控制列表。

```sql
CREATE TABLE IF NOT EXISTS asset_acl (
    id              TEXT PRIMARY KEY,
    asset_id        TEXT NOT NULL REFERENCES assets(id),
    principal_id    TEXT NOT NULL,             -- user_id 或 agent_id
    principal_type  TEXT NOT NULL CHECK (principal_type IN ('user','agent','team')),
    permission      TEXT NOT NULL CHECK (permission IN ('read','write','admin')),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_asset_acl_asset ON asset_acl(asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_acl_principal ON asset_acl(principal_id);
```

---

## 迁移策略

### Schema 版本管理

使用内置迁移系统（`internal/db/migrate.go`）：

```go
// 执行迁移
func Migrate(db *sql.DB, schemaPath string) error {
    schema, _ := os.ReadFile(schemaPath)
    _, err := db.Exec(string(schema))
    return err
}
```

### 迁移记录表

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
```

### 迁移文件命名

```
schema/
├── schema.sql              # 当前完整 Schema
└── migrations/
    ├── 001_initial.sql     # 初始化
    ├── 002_add_aliases.sql # 新增别名表
    └── 003_add_acl.sql     # 新增权限表
```

---

## 查询优化

### 常见查询模式

1. **按 API Key 查询团队**：
   ```sql
   SELECT team_id FROM api_keys WHERE key_hash = ? AND enabled = 1;
   ```

2. **查询团队的上游通道**：
   ```sql
   SELECT * FROM upstream_channels 
   WHERE team_id = ? AND protocol = ? AND enabled = 1 
   ORDER BY priority;
   ```

3. **查询会话 Turn 列表**：
   ```sql
   SELECT * FROM turn_ledger 
   WHERE conversation_id = ? 
   ORDER BY created_at DESC 
   LIMIT 20;
   ```

### 慢查询监控

启用 SQLite 慢查询日志：

```go
db.Exec("PRAGMA query_only = OFF")
db.Exec("PRAGMA optimize")
```

---

## 备份与恢复

### 备份策略

**热备份** (WAL 模式下可安全备份)：

```bash
# 备份全局库
cp memory-gateway.db memory-gateway.db.backup
cp memory-gateway.db-wal memory-gateway.db-wal.backup
cp memory-gateway.db-shm memory-gateway.db-shm.backup

# 备份团队库
cp teams/{team_id}/memory.db teams/{team_id}/memory.db.backup
```

**冷备份** (停服务后备份)：

```bash
sqlite3 memory-gateway.db ".backup memory-gateway.db.backup"
```

### 恢复策略

```bash
# 恢复全局库
mv memory-gateway.db.backup memory-gateway.db

# 恢复团队库
mv teams/{team_id}/memory.db.backup teams/{team_id}/memory.db
```

---

## 监控指标

### 数据库健康指标

- **连接数**：`db.Stats().OpenConnections`
- **WAL 大小**：`PRAGMA wal_checkpoint` 返回值
- **表行数**：`SELECT COUNT(*) FROM table`
- **慢查询**：查询耗时 > 100ms

### 告警规则

- WAL 文件 > 100MB → 触发 checkpoint
- 连接数 > 20 → 连接泄露告警
- `outbox` 队列 > 100 → 重试堆积告警

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**维护者**: 首席架构师 高见远
