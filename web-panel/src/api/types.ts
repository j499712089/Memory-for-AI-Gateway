// 与 api-contract.md §3 对齐的类型定义
// 时间统一 RFC3339 UTC 字符串

// ---------- 通用 ----------

export interface ApiErrorBody {
  error: {
    type: string
    message: string
    request_id?: string
  }
}

export interface Paginated<T> {
  items: T[]
  total: number
  limit: number
  offset: number
}

export type Visibility = 'private' | 'team' | 'org'
export type Status = 'active' | 'archived' | 'disabled'

// ---------- 团队与成员 (§3.1) ----------

export interface Team {
  id: string
  name: string
  slug: string
  description: string
  visibility: Visibility
  status: Status
  created_at: string
  updated_at?: string
}

export interface TeamCreateInput {
  name: string
  slug: string
  description?: string
  visibility?: Visibility
}

export interface TeamUpdateInput {
  name?: string
  description?: string
  visibility?: Visibility
  status?: Status
}

export interface Member {
  user_id: string
  name: string
  role: string
  joined_at: string
}

export interface MemberCreateInput {
  user_id: string
  name: string
  role?: string
}

// ---------- 代理与身份卡 (§3.2) ----------

export interface Agent {
  id: string
  team_id: string
  name: string
  runtime: string
  model_ref: string
  identity_card_id: string | null
  status: Status
  created_at: string
}

export interface AgentCreateInput {
  team_id: string
  name: string
  runtime?: string
  model_ref?: string
  identity_card_id?: string | null
}

export interface IdentityCard {
  id: string
  team_id: string
  agent_id: string | null
  name: string
  role: string
  responsibilities: string
  boundaries: string
  allowed_tools: string[]
  style: string
  visibility: Visibility
  version: number
  status: Status
  source_event_ids: string[]
  created_at: string
  updated_at?: string
}

export interface IdentityCardCreateInput {
  name: string
  role: string
  responsibilities: string
  boundaries: string
  allowed_tools?: string[]
  style?: string
  visibility?: Visibility
  agent_id?: string | null
}

export interface IdentityCardUpdateInput extends IdentityCardCreateInput {
  version: number
}

// ---------- API Key (§3.3) ----------

export interface ApiKey {
  id: string
  team_id: string
  owner_id: string
  name?: string
  key_hash_prefix: string
  scopes: string[]
  enabled: boolean
  expires_at: string | null
  created_at: string
  revoked_at: string | null
}

// 创建响应：明文 key 一次性返回
export interface ApiKeyCreated extends ApiKey {
  key: string
}

export interface ApiKeyCreateInput {
  team_id: string
  owner_id: string
  name?: string
  scopes: string[]
  expires_at?: string | null
}

export interface ApiKeyUpdateInput {
  scopes?: string[]
  enabled?: boolean
  expires_at?: string | null
}

// ---------- 上游通道 (§3.4) ----------

export interface UpstreamChannel {
  id: string
  team_id: string | null
  name: string
  protocol: 'anthropic' | 'chat_completions' | 'responses'
  base_url: string
  model: string
  api_key_ref: string
  capabilities_json: {
    supportsToolCall?: boolean
    supportsImages?: boolean
    supportsReasoning?: boolean
    maxInputTokens?: number
    maxOutputTokens?: number
  }
  priority: number
  failover_channel_id: string | null
  enabled: boolean
  created_at: string
}

export interface UpstreamCreateInput {
  name: string
  protocol: UpstreamChannel['protocol']
  base_url: string
  model: string
  api_key_ref?: string
  capabilities_json?: Record<string, unknown>
  priority?: number
  failover_channel_id?: string | null
  enabled?: boolean
}

export interface ImportModelsInput {
  path: string
}

export interface ImportModelsResult {
  imported: number
  skipped: number
  channels: UpstreamChannel[]
}

// ---------- 会话与绑定 (§3.5) ----------

export interface Session {
  session_id: string
  conversation_id: string
  team_id: string | null
  agent_id: string | null
  status: string
  created_at: string
  updated_at?: string
}

export interface SessionBinding {
  session_id: string
  conversation_id: string
  team_id: string | null
  agent_id: string | null
  identity_card_id: string | null
  upstream_channel_id: string | null
  binding_version: number
  binding_state: string
  binding_source: string
  last_resolved_at: string
}

// ---------- 健康与录入看板 (§3.6) ----------

export interface HealthStatus {
  status: 'ok' | 'degraded'
  version: string
  checks: {
    db: 'ok' | 'wal' | 'error'
    secrets: 'ok' | 'env_fallback' | 'error'
    obsidian: 'ok' | 'unavailable' | 'error'
    gateway?: 'ok' | 'error'
  }
}

export type TerminalStatus = 'complete' | 'partial' | 'error' | 'cancelled'

export interface RecordingHealthItem {
  request_id: string
  conversation_id: string | null
  session_id: string | null
  inbound_at: string
  terminal_at: string | null
  terminal_status: TerminalStatus | null
  content_hash: string
  l0_path: string
  binding_version: number | null
  compensation_status: 'ok' | 'pending' | 'failed' | 'none'
  last_success_at: string | null
}

// ---------- 团队资产 (§3.7) ----------

export interface TeamAsset {
  asset_id: string
  asset_type: 'l1' | 'l2' | 'l3' | 'l4' | 'wiki' | 'codegraph' | 'skill' | 'team_asset'
  name: string
  summary: string
  visibility: Visibility
  status: Status
  version: number
  updated_at: string
}
