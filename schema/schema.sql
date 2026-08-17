-- ============================================================================
-- Memory Gateway MVP — SQLite 3 Schema（Stage 2: Specify 交付物）
-- ============================================================================
-- 来源依据：
--   F:\memory_plus\00_系统\Constitution.md（Stage 1 宪章）
--   F:\memory_plus\00_系统\完整录入保障协议.md
--   F:\memory_plus\00_系统\并发写入协议.md
--   F:\memory_plus\00_系统\传话者每轮协议.md
--   F:\memory_plus\00_系统\资产权限说明.md
--   F:\memory_plus\00_系统\路径注入清单.md
--
-- 两类数据库：
--   1) 全局库  F:\memory_plus\.runtime\memory-gateway.db
--      —— 团队/代理/身份卡/API Key/上游通道/会话/会话绑定/turn 账本/
--         outbox/本地缓冲台账/任务队列(全局)/审计/幂等/注入快照/录入健康
--   2) 团队库  F:\memory_plus\teams\{team_id}\memory.db（每团队一个）
--      —— 该团队的资产清单与正文引用、Wiki、CodeGraph、Skill、ACL
--
-- 建库约定：
--   PRAGMA journal_mode = WAL;      -- 并发读写
--   PRAGMA foreign_keys = ON;       -- 外键约束
--   PRAGMA busy_timeout = 5000;     -- 写锁等待 5s
--   PRAGMA synchronous = NORMAL;    -- WAL 下 NORMAL 即可，兼顾安全与吞吐
--   PRAGMA wal_autocheckpoint = 1000;
--
-- 密钥纪律：本 schema 中任何表都不存明文密钥。API Key 只存
--   key_hash（sha256）与 key_ref（DPAPI/secret 服务引用）。真实密钥由
--   F:\memory_plus\.runtime\secrets 读取，绝不进入 Markdown/日志/Git/上下文。
-- ============================================================================


-- ============================================================================
-- 第一部分：全局库 memory-gateway.db
-- ============================================================================

-- ----------------------------------------------------------------------------
-- 1. 团队与成员
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS teams (
    id           TEXT PRIMARY KEY,              -- 稳定 UUID，用于 teams/{team_id}
    name         TEXT NOT NULL,
    slug         TEXT NOT NULL UNIQUE,
    description  TEXT DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','team','restricted')),
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','archived')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS team_members (
    team_id      TEXT NOT NULL REFERENCES teams(id),
    user_id      TEXT NOT NULL,                 -- 对应用户/代理全局标识
    member_type  TEXT NOT NULL CHECK (member_type IN ('user','agent')),
    role         TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','member')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (team_id, user_id)
);

-- ----------------------------------------------------------------------------
-- 2. 代理与身份卡（L3）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agents (
    id                TEXT PRIMARY KEY,         -- 稳定 agent_id
    team_id           TEXT REFERENCES teams(id),
    name              TEXT NOT NULL,
    runtime           TEXT,                     -- claude-code / codebuddy / codex / dsh / hermes / openclaw ...
    model_ref         TEXT,                     -- models.json 中的模型 id（引用，非密钥）
    identity_card_id  TEXT,                     -- 绑定身份卡
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- 身份卡（L3 资产，全局登记 + 正文存团队库/文件系统）
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
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('draft','active','archived')),
    source_event_ids  TEXT DEFAULT '[]',        -- JSON 数组，可追溯
    body_path         TEXT,                     -- L3_团队身份 下的 Markdown 路径
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- ----------------------------------------------------------------------------
-- 3. API Key（只存哈希与凭据引用）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_keys (
    id            TEXT PRIMARY KEY,
    team_id       TEXT REFERENCES teams(id),
    owner_id      TEXT,                         -- 创建者 agent/user
    key_hash      TEXT NOT NULL UNIQUE,         -- sha256(bearer key)，用于校验
    key_ref       TEXT NOT NULL,                -- DPAPI/secret 服务中的引用名，非明文
    scopes        TEXT NOT NULL DEFAULT '["gateway","mcp"]', -- JSON 数组
    enabled       INTEGER NOT NULL DEFAULT 1,
    expires_at    TEXT,
    revoked_at    TEXT,
    last_used_at  TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_api_keys_team ON api_keys(team_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_owner ON api_keys(owner_id);

-- ----------------------------------------------------------------------------
-- 4. 上游通道（由 models.json 导入，只保存引用）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS upstream_channels (
    id                  TEXT PRIMARY KEY,
    team_id             TEXT REFERENCES teams(id),
    name                TEXT NOT NULL,
    protocol            TEXT NOT NULL CHECK (protocol IN
                          ('anthropic_messages','chat_completions','responses')),
    base_url            TEXT NOT NULL,          -- 例如 https://api.deepseek.com/v1
    model               TEXT NOT NULL,          -- models.json 的模型名
    api_key_ref         TEXT NOT NULL,          -- 凭据引用，绝不存明文
    capabilities_json   TEXT NOT NULL DEFAULT '{}',  -- supportsToolCall/supportsImages/supportsReasoning/maxInputTokens...
    priority            INTEGER NOT NULL DEFAULT 100,  -- 数字越小越优先
    enabled             INTEGER NOT NULL DEFAULT 1,
    failover_channel_id TEXT REFERENCES upstream_channels(id),
    source              TEXT NOT NULL DEFAULT 'models.json',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_upstream_team_proto ON upstream_channels(team_id, protocol);
CREATE INDEX IF NOT EXISTS idx_upstream_enabled   ON upstream_channels(enabled, priority);

-- ----------------------------------------------------------------------------
-- 4b. 通道别名（协议感知的通道映射）
-- 同一客户端通道名在不同协议下可映射到不同的上游通道。用于解决运行时
-- 固定通道名（如 Codex Runtime 固定使用 "default"）与数据库通道协议不匹配
-- 的问题：当客户端以协议 P 请求通道名 N 时，若 N 的存储协议不是 P，则通过
-- (team_id, alias_name=N, protocol=P) 解析到真正支持 P 的目标通道。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS channel_aliases (
    id                TEXT PRIMARY KEY,
    team_id           TEXT REFERENCES teams(id),
    alias_name        TEXT NOT NULL,        -- 客户端使用的通道名（如 default）
    protocol          TEXT NOT NULL CHECK (protocol IN
                          ('anthropic_messages','chat_completions','responses')),
    target_channel_id TEXT NOT NULL REFERENCES upstream_channels(id),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (team_id, alias_name, protocol)
);
CREATE INDEX IF NOT EXISTS idx_channel_alias_lookup ON channel_aliases(team_id, alias_name, protocol);

-- ----------------------------------------------------------------------------
-- 5. 会话与会话绑定
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sessions (
    id                   TEXT PRIMARY KEY,      -- session_id，稳定 UUID
    team_id              TEXT REFERENCES teams(id),
    agent_id             TEXT REFERENCES agents(id),
    user_id              TEXT,
    upstream_channel_id  TEXT REFERENCES upstream_channels(id),
    parent_session_id    TEXT REFERENCES sessions(id),   -- 子代理/侧查询归属
    kind                 TEXT NOT NULL DEFAULT 'main'
                           CHECK (kind IN ('main','fork','sidequery','subagent','initialization')),
    conversation_id      TEXT NOT NULL,         -- 上游对话标识（或内部生成）
    binding_version      INTEGER NOT NULL DEFAULT 0,
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN
                           ('active','closed','archived','quarantined')),
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (conversation_id, agent_id, kind)
);

-- 会话绑定：每一轮按 conversation_id 重新解析，不依赖内存
CREATE TABLE IF NOT EXISTS session_bindings (
    id                    TEXT PRIMARY KEY,
    session_id            TEXT NOT NULL REFERENCES sessions(id),
    conversation_id       TEXT NOT NULL,
    team_id               TEXT REFERENCES teams(id),
    agent_id              TEXT REFERENCES agents(id),
    identity_card_id      TEXT REFERENCES identity_cards(id),
    upstream_channel_id   TEXT REFERENCES upstream_channels(id),
    binding_version       INTEGER NOT NULL DEFAULT 1,    -- 首轮固定并逐轮复核
    binding_state         TEXT NOT NULL DEFAULT 'bound'
                            CHECK (binding_state IN ('bound','binding_missing','quarantined')),
    binding_source        TEXT NOT NULL DEFAULT 'service',  -- ui / service / import
    last_resolved_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (conversation_id, binding_version)
);
CREATE INDEX IF NOT EXISTS idx_bindings_session   ON session_bindings(session_id);
CREATE INDEX IF NOT EXISTS idx_bindings_conv      ON session_bindings(conversation_id);
CREATE INDEX IF NOT EXISTS idx_bindings_state     ON session_bindings(binding_state);

-- ----------------------------------------------------------------------------
-- 6. turn 事件账本（核心：完整录入保障）
-- ----------------------------------------------------------------------------
-- 终态规则：每个 inbound_persisted 必须且只能有一个终态
--   complete | partial | error | cancelled；没有终态不得把 turn 标为 complete。
CREATE TABLE IF NOT EXISTS turn_events (
    id             TEXT PRIMARY KEY,            -- event_id，全局唯一
    request_id     TEXT NOT NULL,
    turn_id        TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    session_id     TEXT REFERENCES sessions(id),
    team_id        TEXT REFERENCES teams(id),
    direction      TEXT NOT NULL CHECK (direction IN ('inbound','outbound')),
    sequence       INTEGER NOT NULL,            -- turn 内事件序号
    event_type     TEXT NOT NULL CHECK (event_type IN
                     ('inbound_persisted','response_started','delta_checkpoint',
                      'complete','partial','error','cancelled')),
    status         TEXT NOT NULL,               -- 见下方约定
    content_hash   TEXT NOT NULL,               -- sha256，去重/完整性
    content_path   TEXT,                        -- L0 文件相对路径（原样 JSON/JSONL）
    body_json      TEXT,                        -- 事件载荷（正文大字段可落文件，此处存引用）
    metadata_json  TEXT DEFAULT '{}',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    -- 幂等：同一 request 内 event 唯一
    UNIQUE (request_id, id),
    -- 并发防重：同会话同 turn 同方向同序号唯一
    UNIQUE (conversation_id, turn_id, direction, sequence)
);
CREATE INDEX IF NOT EXISTS idx_turn_events_request   ON turn_events(request_id);
CREATE INDEX IF NOT EXISTS idx_turn_events_turn      ON turn_events(turn_id);
CREATE INDEX IF NOT EXISTS idx_turn_events_session   ON turn_events(session_id);
CREATE INDEX IF NOT EXISTS idx_turn_events_conv      ON turn_events(conversation_id);
CREATE INDEX IF NOT EXISTS idx_turn_events_created   ON turn_events(created_at);

-- turn 级账本：入站/终态配对、序号缺口检测
CREATE TABLE IF NOT EXISTS turn_ledger (
    turn_id          TEXT PRIMARY KEY,
    request_id       TEXT NOT NULL,
    conversation_id  TEXT NOT NULL,
    session_id       TEXT REFERENCES sessions(id),
    team_id          TEXT REFERENCES teams(id),
    turn_seq         INTEGER NOT NULL,           -- 会话内轮次序号，必须连续
    inbound_event_id TEXT,
    final_event_id   TEXT,
    final_status     TEXT CHECK (final_status IN
                      ('complete','partial','error','cancelled','missing')),
    gap_detected     INTEGER NOT NULL DEFAULT 0,
    binding_version  INTEGER,
    recording_state  TEXT NOT NULL DEFAULT 'open'
                       CHECK (recording_state IN ('open','complete','compensating','quarantined')),
    content_hash     TEXT,
    l0_request_path  TEXT,
    l0_reply_path    TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    completed_at     TEXT,
    UNIQUE (conversation_id, turn_seq),
    UNIQUE (conversation_id, turn_id)
);
CREATE INDEX IF NOT EXISTS idx_ledger_session ON turn_ledger(session_id);
CREATE INDEX IF NOT EXISTS idx_ledger_final   ON turn_ledger(final_status, recording_state);

-- 事件 → L0 文件去重标记（防止进程重启后重试重复写文件）
CREATE TABLE IF NOT EXISTS event_files (
    event_id      TEXT PRIMARY KEY REFERENCES turn_events(id),
    file_path     TEXT NOT NULL UNIQUE,
    file_hash     TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL,
    written_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- ----------------------------------------------------------------------------
-- 7. 本地持久化缓冲台账（数据库不可写时先落缓冲，恢复后补写）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS local_buffer (
    id           TEXT PRIMARY KEY,              -- 缓冲记录 id
    event_id     TEXT,
    request_id   TEXT,
    turn_id      TEXT,
    payload_json TEXT NOT NULL,                 -- 序列化事件
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                   ('pending','replayed','failed','dead')),
    file_path    TEXT NOT NULL UNIQUE,          -- 90_运行数据/本地持久化缓冲/...
    sha256       TEXT NOT NULL,
    retry_count  INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    replayed_at  TEXT,
    UNIQUE (request_id, event_id)
);
CREATE INDEX IF NOT EXISTS idx_buffer_status ON local_buffer(status, created_at);

-- ----------------------------------------------------------------------------
-- 8. outbox：事件与文件最终一致 + 失败重试
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS outbox (
    id             TEXT PRIMARY KEY,
    event_id       TEXT REFERENCES turn_events(id),
    request_id     TEXT NOT NULL,
    payload_json   TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                     ('pending','processing','done','failed','dead_letter')),
    retry_count    INTEGER NOT NULL DEFAULT 0,
    max_retries    INTEGER NOT NULL DEFAULT 10,
    next_retry_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_error     TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    processed_at   TEXT,
    UNIQUE (request_id, event_id)
);
CREATE INDEX IF NOT EXISTS idx_outbox_due ON outbox(status, next_retry_at);

-- ----------------------------------------------------------------------------
-- 9. Worker 队列（优先级 + 租约 + 死信）
-- ----------------------------------------------------------------------------
-- 优先级：L1 提炼 80 / Wiki 索引 60 / CodeGraph 增量 50 / L2L3L4 晋升 40 /
--          Skill 审核 40 / Git 批量提交 20；补偿/缺失响应/缓冲重放/绑定修复 = 90。
CREATE TABLE IF NOT EXISTS jobs (
    id                 TEXT PRIMARY KEY,
    queue              TEXT NOT NULL CHECK (queue IN
                          ('l1_refine','l2_promote','l3_promote','l4_promote',
                           'wiki_build','codegraph_incremental','skill_review',
                           'git_commit','compensation','missing_response',
                           'buffer_replay','binding_fix')),
    priority           INTEGER NOT NULL DEFAULT 50,   -- 100 最高
    team_id            TEXT REFERENCES teams(id),
    agent_id           TEXT REFERENCES agents(id),
    asset_id           TEXT,                          -- 目标资产 id（团队库）
    asset_type         TEXT,                          -- l1/l2/l3/l4/wiki/codegraph/skill/team_asset
    payload_json       TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                         ('pending','processing','done','failed','dead_letter')),
    -- 租约
    lease_token        TEXT,
    lease_until        TEXT,
    worker_id          TEXT,
    heartbeat_at       TEXT,
    -- 重试/死信
    retry_count        INTEGER NOT NULL DEFAULT 0,
    max_retries        INTEGER NOT NULL DEFAULT 5,
    dead_letter_reason TEXT,
    -- 分区串行键：同 (team_id, agent_id, asset_id) 只允许一个 Worker
    partition_key      TEXT NOT NULL,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    started_at         TEXT,
    finished_at        TEXT
);
CREATE INDEX IF NOT EXISTS idx_jobs_dispatch ON jobs(queue, status, priority, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_lease    ON jobs(status, lease_until);
CREATE INDEX IF NOT EXISTS idx_jobs_partition ON jobs(partition_key, status);
CREATE INDEX IF NOT EXISTS idx_jobs_dead     ON jobs(status, queue);

-- 死信明细（与 jobs.status='dead_letter' 关联，保留完整轨迹）
CREATE TABLE IF NOT EXISTS dead_letters (
    id               TEXT PRIMARY KEY,
    job_id           TEXT REFERENCES jobs(id),
    queue            TEXT NOT NULL,
    reason           TEXT NOT NULL,
    error_trace      TEXT,
    payload_json     TEXT NOT NULL,
    retries          INTEGER NOT NULL,
    enqueued_at      TEXT NOT NULL,
    dead_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- ----------------------------------------------------------------------------
-- 10. 幂等键
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id             TEXT PRIMARY KEY,
    key            TEXT NOT NULL UNIQUE,         -- 客户端幂等键（如 Idempotency-Key）
    request_id     TEXT NOT NULL,
    response_ref   TEXT,                          -- 复用已保存结果的引用
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_idem_expires ON idempotency_keys(expires_at);

-- ----------------------------------------------------------------------------
-- 11. 注入快照（每轮检索注入可审计）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS injection_snapshots (
    id                  TEXT PRIMARY KEY,
    request_id          TEXT NOT NULL,
    turn_id             TEXT NOT NULL,
    manifest_version    TEXT NOT NULL,           -- 路径清单版本
    source_ids_json     TEXT NOT NULL DEFAULT '[]',
    token_budget        INTEGER NOT NULL DEFAULT 0,
    used_tokens         INTEGER NOT NULL DEFAULT 0,
    truncated           INTEGER NOT NULL DEFAULT 0,   -- memory_truncated 标记
    injected_text_path  TEXT,                    -- 注入正文落盘路径（快照）
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_inj_request ON injection_snapshots(request_id);

-- ----------------------------------------------------------------------------
-- 12. 录入健康看板（看门狗）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS recording_health (
    id                 TEXT PRIMARY KEY,
    request_id         TEXT NOT NULL UNIQUE,
    conversation_id    TEXT NOT NULL,
    session_id         TEXT REFERENCES sessions(id),
    inbound_at         TEXT,
    terminal_at        TEXT,
    terminal_status    TEXT,
    content_hash       TEXT,
    l0_path            TEXT,
    binding_version    INTEGER,
    compensation_status TEXT NOT NULL DEFAULT 'ok' CHECK (compensation_status IN
                         ('ok','pending','replayed','failed')),
    last_success_at    TEXT,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_health_terminal ON recording_health(terminal_status, terminal_at);

-- ----------------------------------------------------------------------------
-- 13. 审计日志
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_log (
    id           TEXT PRIMARY KEY,
    request_id   TEXT,
    actor_type   TEXT CHECK (actor_type IN ('agent','user','system','worker')),
    actor_id     TEXT,
    action       TEXT NOT NULL,                  -- binding_change / key_rotate / job_state / status_change ...
    target_type  TEXT,
    target_id    TEXT,
    before_json  TEXT,
    after_json   TEXT,
    result       TEXT,
    failure      TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor_type, actor_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log(target_type, target_id);

-- ----------------------------------------------------------------------------
-- 14. Git 批量提交批次
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS git_batches (
    id           TEXT PRIMARY KEY,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                   ('pending','committing','committed','failed')),
    files_json   TEXT NOT NULL DEFAULT '[]',      -- 本批文件清单
    commit_sha   TEXT,
    message      TEXT,
    error        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    committed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_git_batches_status ON git_batches(status, created_at);

-- ============================================================================
-- 第二部分：团队库 teams/{team_id}/memory.db
-- ============================================================================
-- 每团队一个数据库文件，天然隔离；跨团队检索由全局库路由到对应团队库。
-- 建库后同样执行：
--   PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;

-- ----------------------------------------------------------------------------
-- 15. 资产基表（L1-L4 / Wiki / CodeGraph / Skill / Team Asset 的统一登记）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS assets (
    id                  TEXT PRIMARY KEY,
    team_id             TEXT NOT NULL,
    identity_card_id    TEXT,
    asset_type          TEXT NOT NULL CHECK (asset_type IN
                          ('l1','l2','l3','l4','wiki','codegraph','skill','team_asset')),
    name                TEXT NOT NULL,
    slug                TEXT NOT NULL,
    summary             TEXT DEFAULT '',
    body_path           TEXT,                    -- 资产 Markdown/JSON 原件路径
    -- 统一元数据（宪法 §5 要求）
    source_event_ids    TEXT NOT NULL DEFAULT '[]',   -- JSON 数组
    confidence          REAL NOT NULL DEFAULT 0,      -- 0..1
    status              TEXT NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('candidate','draft','approved','promoted',
                                            'ready','deprecated','archived')),
    visibility          TEXT NOT NULL DEFAULT 'private'
                          CHECK (visibility IN ('private','team','restricted','agent')),
    version             INTEGER NOT NULL DEFAULT 1,   -- 乐观锁
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (asset_type, slug, version),
    UNIQUE (id, version)
);
CREATE INDEX IF NOT EXISTS idx_assets_team_type ON assets(team_id, asset_type, status);
CREATE INDEX IF NOT EXISTS idx_assets_visibility ON assets(team_id, visibility);

-- 版本历史（L1-L4 冲突生成新版本，不静默覆盖旧结论）
CREATE TABLE IF NOT EXISTS asset_versions (
    id             TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL REFERENCES assets(id),
    version        INTEGER NOT NULL,
    body_path      TEXT,
    summary        TEXT,
    confidence     REAL,
    source_event_ids TEXT DEFAULT '[]',
    created_by     TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (asset_id, version)
);

-- ----------------------------------------------------------------------------
-- 16. Wiki 资产
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_pages (
    id              TEXT PRIMARY KEY,
    asset_id        TEXT NOT NULL UNIQUE REFERENCES assets(id),
    title           TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    content_md      TEXT NOT NULL DEFAULT '',
    frontmatter_json TEXT DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'ready' CHECK (status IN ('building','ready','failed')),
    built_at        TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- 页面链接（[[内部链接]] 解析结果）
CREATE TABLE IF NOT EXISTS wiki_edges (
    id           TEXT PRIMARY KEY,
    from_page_id TEXT NOT NULL REFERENCES wiki_pages(id),
    to_page_id   TEXT NOT NULL REFERENCES wiki_pages(id),
    link_type    TEXT NOT NULL DEFAULT 'internal',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (from_page_id, to_page_id, link_type)
);
CREATE INDEX IF NOT EXISTS idx_wiki_edges_to ON wiki_edges(to_page_id);

-- Wiki 全文索引（FTS5）
CREATE VIRTUAL TABLE IF NOT EXISTS wiki_fts USING fts5(
    page_id UNINDEXED,
    title,
    content,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- ----------------------------------------------------------------------------
-- 17. CodeGraph 资产
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS code_repos (
    id           TEXT PRIMARY KEY,
    asset_id     TEXT NOT NULL UNIQUE REFERENCES assets(id),
    repo_url     TEXT NOT NULL,
    local_path   TEXT NOT NULL,                  -- 源头仓库路径（唯一真相）
    branch       TEXT,
    head_commit  TEXT,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                   ('pending','indexing','ready','failed','stale')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS code_files (
    id           TEXT PRIMARY KEY,
    repo_id      TEXT NOT NULL REFERENCES code_repos(id),
    path         TEXT NOT NULL,
    file_hash    TEXT NOT NULL,                  -- hash 未变则跳过重建
    language     TEXT,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    indexed_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (repo_id, path)
);
CREATE INDEX IF NOT EXISTS idx_code_files_hash ON code_files(repo_id, file_hash);

CREATE TABLE IF NOT EXISTS code_symbols (
    id           TEXT PRIMARY KEY,
    file_id      TEXT NOT NULL REFERENCES code_files(id),
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL,                  -- function/method/class/struct/interface/import...
    signature    TEXT DEFAULT '',
    line_start   INTEGER,
    line_end     INTEGER,
    symbol_hash  TEXT NOT NULL,
    UNIQUE (file_id, name, kind, line_start)
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON code_symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON code_symbols(file_id);

CREATE TABLE IF NOT EXISTS code_edges (
    id                TEXT PRIMARY KEY,
    source_symbol_id  TEXT NOT NULL REFERENCES code_symbols(id),
    target_symbol_id  TEXT NOT NULL REFERENCES code_symbols(id),
    edge_type         TEXT NOT NULL CHECK (edge_type IN
                        ('import','call','export','inherit','reference')),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (source_symbol_id, target_symbol_id, edge_type)
);
CREATE INDEX IF NOT EXISTS idx_code_edges_target ON code_edges(target_symbol_id);
CREATE INDEX IF NOT EXISTS idx_code_edges_type   ON code_edges(edge_type);

-- CodeGraph 增量重建游标
CREATE TABLE IF NOT EXISTS codegraph_cursor (
    repo_id        TEXT PRIMARY KEY REFERENCES code_repos(id),
    last_indexed_commit TEXT,
    last_indexed_at TEXT,
    pending_diffs  TEXT DEFAULT '[]'             -- 待处理的 git diff 清单
);

-- ----------------------------------------------------------------------------
-- 18. Skill 资产
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS skills (
    id                TEXT PRIMARY KEY,
    asset_id          TEXT NOT NULL UNIQUE REFERENCES assets(id),
    name              TEXT NOT NULL UNIQUE,      -- 稳定英文 kebab-case id
    display_name      TEXT,                      -- 中文显示名
    version           TEXT NOT NULL,             -- SemVer
    status            TEXT NOT NULL DEFAULT 'candidate'
                        CHECK (status IN ('candidate','approved','deprecated')),
    scope             TEXT NOT NULL DEFAULT 'personal' CHECK (scope IN ('personal','team')),
    trigger_boundary  TEXT,                      -- 触发边界说明
    steps_json        TEXT NOT NULL DEFAULT '[]',  -- 执行步骤
    validation_json   TEXT NOT NULL DEFAULT '{}',  -- 验证规则
    source_ids        TEXT NOT NULL DEFAULT '[]',
    resource_refs     TEXT NOT NULL DEFAULT '[]',  -- 资源文件引用
    entrypoint        TEXT,
    manifest_path     TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Skill 版本（SemVer 不覆盖旧版）
CREATE TABLE IF NOT EXISTS skill_versions (
    id             TEXT PRIMARY KEY,
    skill_id       TEXT NOT NULL REFERENCES skills(id),
    version        TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'candidate',
    content_ref    TEXT NOT NULL,
    source_ids     TEXT DEFAULT '[]',
    created_by     TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (skill_id, version)
);

-- ----------------------------------------------------------------------------
-- 19. 团队资产 ACL（细粒度授权）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS acl_entries (
    id           TEXT PRIMARY KEY,
    asset_id     TEXT NOT NULL REFERENCES assets(id),
    grantee_type TEXT NOT NULL CHECK (grantee_type IN ('user','role','agent','team')),
    grantee_id   TEXT NOT NULL,
    permission   TEXT NOT NULL DEFAULT 'read' CHECK (permission IN ('read','write','manage')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (asset_id, grantee_type, grantee_id, permission)
);
CREATE INDEX IF NOT EXISTS idx_acl_grantee ON acl_entries(grantee_type, grantee_id);

-- ----------------------------------------------------------------------------
-- 20. 团队库 schema 迁移版本
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    script_hash TEXT NOT NULL
);
