// fetch 封装：baseURL、bearer 注入、错误统一处理（api-contract.md §1 / §6）
import type {
  Agent,
  AgentCreateInput,
  ApiKey,
  ApiKeyCreateInput,
  ApiKeyCreated,
  ApiKeyUpdateInput,
  ApiErrorBody,
  HealthStatus,
  IdentityCard,
  IdentityCardCreateInput,
  IdentityCardUpdateInput,
  ImportModelsInput,
  ImportModelsResult,
  Member,
  MemberCreateInput,
  Paginated,
  RecordingHealthItem,
  Session,
  SessionBinding,
  Team,
  TeamCreateInput,
  TeamUpdateInput,
  TerminalStatus,
  UpstreamChannel,
  UpstreamCreateInput,
} from './types'

const GATEWAY_KEY_STORAGE = 'mgw.gatewayKey'

export class ApiError extends Error {
  type: string
  status: number
  requestId?: string

  constructor(status: number, body: ApiErrorBody) {
    super(body.error.message)
    this.name = 'ApiError'
    this.type = body.error.type
    this.status = status
    this.requestId = body.error.request_id
  }
}

function getBaseUrl(): string {
  // 开发环境由 Vite 代理 /api → 127.0.0.1:8096；生产可经 VITE_GATEWAY_BASE 覆盖
  return import.meta.env.VITE_GATEWAY_BASE || ''
}

export function getGatewayKey(): string {
  return localStorage.getItem(GATEWAY_KEY_STORAGE) || ''
}

export function setGatewayKey(key: string): void {
  if (key) localStorage.setItem(GATEWAY_KEY_STORAGE, key)
  else localStorage.removeItem(GATEWAY_KEY_STORAGE)
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  params?: Record<string, string | number | boolean | null | undefined>
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, params } = options

  let url = `${getBaseUrl()}${path}`
  if (params) {
    const qs = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
    }
    const s = qs.toString()
    if (s) url += `?${s}`
  }

  const headers: Record<string, string> = { Accept: 'application/json' }
  const key = getGatewayKey()
  if (key) headers.Authorization = `Bearer ${key}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let resp: Response
  try {
    resp = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch (e) {
    throw new Error(`网络错误：无法连接 Gateway (${url})`)
  }

  if (resp.status === 204) return undefined as T

  const text = await resp.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }

  if (!resp.ok) {
    const errBody = data as ApiErrorBody
    if (errBody && errBody.error) throw new ApiError(resp.status, errBody)
    throw new ApiError(resp.status, {
      error: { type: 'internal_error', message: `HTTP ${resp.status}`, request_id: undefined },
    })
  }
  return data as T
}

// ---------- 团队与成员 ----------

export const teamsApi = {
  list: (params?: { limit?: number; offset?: number; status?: string }) =>
    request<Paginated<Team>>('/api/teams', { params }),
  create: (input: TeamCreateInput) => request<Team>('/api/teams', { method: 'POST', body: input }),
  get: (id: string) => request<Team>(`/api/teams/${id}`),
  update: (id: string, input: TeamUpdateInput) =>
    request<Team>(`/api/teams/${id}`, { method: 'PUT', body: input }),
  archive: (id: string) => request<Team>(`/api/teams/${id}`, { method: 'DELETE' }),
  members: (id: string) => request<Member[]>(`/api/teams/${id}/members`),
  addMember: (id: string, input: MemberCreateInput) =>
    request<Member>(`/api/teams/${id}/members`, { method: 'POST', body: input }),
  removeMember: (teamId: string, userId: string) =>
    request<void>(`/api/teams/${teamId}/members/${userId}`, { method: 'DELETE' }),
}

// ---------- 代理与身份卡 ----------

export const agentsApi = {
  list: (params?: { team_id?: string }) => request<Agent[]>('/api/agents', { params }),
  create: (input: AgentCreateInput) => request<Agent>('/api/agents', { method: 'POST', body: input }),
  update: (id: string, input: Partial<AgentCreateInput>) =>
    request<Agent>(`/api/agents/${id}`, { method: 'PUT', body: input }),
}

export const identityCardsApi = {
  list: (teamId: string) => request<IdentityCard[]>(`/api/teams/${teamId}/identity-cards`),
  create: (teamId: string, input: IdentityCardCreateInput) =>
    request<IdentityCard>(`/api/teams/${teamId}/identity-cards`, { method: 'POST', body: input }),
  get: (cardId: string) => request<IdentityCard>(`/api/identity-cards/${cardId}`),
  update: (cardId: string, input: IdentityCardUpdateInput) =>
    request<IdentityCard>(`/api/identity-cards/${cardId}`, { method: 'PUT', body: input }),
}

// ---------- API Key ----------

export const apiKeysApi = {
  list: (params?: { team_id?: string }) => request<ApiKey[]>('/api/api-keys', { params }),
  create: (input: ApiKeyCreateInput) => request<ApiKeyCreated>('/api/api-keys', { method: 'POST', body: input }),
  update: (id: string, input: ApiKeyUpdateInput) =>
    request<ApiKey>(`/api/api-keys/${id}`, { method: 'PUT', body: input }),
  revoke: (id: string) => request<ApiKey>(`/api/api-keys/${id}`, { method: 'DELETE' }),
}

// ---------- 上游通道 ----------

export const upstreamsApi = {
  list: (params?: { team_id?: string; protocol?: string }) =>
    request<UpstreamChannel[]>('/api/upstream-channels', { params }),
  create: (input: UpstreamCreateInput) =>
    request<UpstreamChannel>('/api/upstream-channels', { method: 'POST', body: input }),
  importModels: (input: ImportModelsInput) =>
    request<ImportModelsResult>('/api/upstream-channels/import-models', { method: 'POST', body: input }),
  update: (id: string, input: Partial<UpstreamCreateInput>) =>
    request<UpstreamChannel>(`/api/upstream-channels/${id}`, { method: 'PUT', body: input }),
  disable: (id: string) => request<UpstreamChannel>(`/api/upstream-channels/${id}`, { method: 'DELETE' }),
}

// ---------- 会话与绑定 ----------

export const sessionsApi = {
  list: (params?: { conversation_id?: string; agent_id?: string; status?: string }) =>
    request<Session[]>('/api/sessions', { params }),
  bindings: (sessionId: string) => request<SessionBinding[]>(`/api/sessions/${sessionId}/bindings`),
  bindingByConversation: (conversationId: string) =>
    request<SessionBinding>(`/api/bindings/${conversationId}`),
}

// ---------- 健康与录入看板 ----------

export const healthApi = {
  get: () => request<HealthStatus>('/api/health'),
  recording: (params?: {
    limit?: number
    offset?: number
    status?: string
    terminal_status?: TerminalStatus
  }) => request<Paginated<RecordingHealthItem>>('/api/recording-health', { params }),
}
