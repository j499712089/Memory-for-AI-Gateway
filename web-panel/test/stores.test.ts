// Pinia store 测试：API Key 明文一次性（安全纪律）+ 团队 slug 冲突
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useApiKeysStore } from '@/stores/apiKeys'
import { useTeamsStore } from '@/stores/teams'
import { ApiError, apiKeysApi, teamsApi } from '@/api/client'

vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>()
  return {
    ...actual,
    apiKeysApi: {
      list: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      revoke: vi.fn(),
    },
    teamsApi: {
      list: vi.fn(),
      create: vi.fn(),
      archive: vi.fn(),
      members: vi.fn(),
      addMember: vi.fn(),
      removeMember: vi.fn(),
    },
  }
})

describe('apiKeys store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('创建后明文不进入列表 state（只返回给调用方一次）', async () => {
    vi.mocked(apiKeysApi.create).mockResolvedValue({
      id: 'k1',
      team_id: 't1',
      owner_id: 'o1',
      key: 'mgw-plaintext-secret',
      key_hash_prefix: 'abc123',
      scopes: ['gateway'],
      enabled: true,
      created_at: '2026-08-17T00:00:00Z',
      revoked_at: null,
      expires_at: null,
    })
    const store = useApiKeysStore()
    const created = await store.createKey({ team_id: 't1', owner_id: 'o1', scopes: ['gateway'] })
    expect(created.key).toBe('mgw-plaintext-secret')
    // state 中的条目不含明文 key
    expect(store.keys[0]).not.toHaveProperty('key')
    expect(store.keys[0].key_hash_prefix).toBe('abc123')
  })

  it('吊销后 enabled=false 且 revoked_at 置位', async () => {
    const store = useApiKeysStore()
    store.keys = [
      {
        id: 'k1',
        team_id: 't1',
        owner_id: 'o1',
        key_hash_prefix: 'abc',
        scopes: ['gateway'],
        enabled: true,
        created_at: '2026-08-17T00:00:00Z',
        revoked_at: null,
        expires_at: null,
      },
    ]
    await store.revokeKey('k1')
    expect(store.keys[0].enabled).toBe(false)
    expect(store.keys[0].revoked_at).toBeTruthy()
  })
})

describe('teams store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('slug 冲突 (409 conflict) 时记录中文错误', async () => {
    vi.mocked(teamsApi.create).mockRejectedValue(
      new ApiError(409, { error: { type: 'conflict', message: 'slug already exists' } }),
    )
    const store = useTeamsStore()
    await expect(store.createTeam({ name: 'X', slug: 'x' })).rejects.toThrow()
    expect(store.error).toContain('slug 冲突')
  })
})
