// API client 单元测试：bearer 注入、错误统一处理、分页参数（api-contract.md §1/§6）
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiKeysApi, setGatewayKey, teamsApi } from '@/api/client'

function mockFetchOnce(status: number, body: unknown) {
  return vi
    .spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
}

describe('api client', () => {
  beforeEach(() => {
    setGatewayKey('')
    vi.restoreAllMocks()
  })

  it('注入 Bearer header', async () => {
    setGatewayKey('test-key-123')
    const fetchMock = mockFetchOnce(200, { items: [], total: 0, limit: 20, offset: 0 })
    await teamsApi.list()
    const [, init] = fetchMock.mock.calls[0]
    expect((init as RequestInit).headers).toMatchObject({ Authorization: 'Bearer test-key-123' })
  })

  it('携带分页查询参数', async () => {
    const fetchMock = mockFetchOnce(200, { items: [], total: 0, limit: 10, offset: 5 })
    await teamsApi.list({ limit: 10, offset: 5 })
    const url = fetchMock.mock.calls[0][0] as string
    expect(url).toContain('limit=10')
    expect(url).toContain('offset=5')
  })

  it('409 conflict 归一为 ApiError.type=conflict', async () => {
    mockFetchOnce(409, {
      error: { type: 'conflict', message: 'slug already exists', request_id: 'r-1' },
    })
    await expect(teamsApi.create({ name: 'X', slug: 'x' })).rejects.toMatchObject({
      type: 'conflict',
      status: 409,
      message: 'slug already exists',
    })
  })

  it('401 归一为 ApiError.type=unauthorized', async () => {
    mockFetchOnce(401, {
      error: { type: 'unauthorized', message: 'invalid key' },
    })
    await expect(teamsApi.list()).rejects.toMatchObject({ type: 'unauthorized', status: 401 })
  })

  it('非 JSON 错误体兜底为 internal_error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('Bad Gateway', { status: 502 }),
    )
    await expect(teamsApi.list()).rejects.toMatchObject({ type: 'internal_error', status: 502 })
  })

  it('明文 key 只在创建响应中出现（create 返回 key，list 不返回 key 字段）', async () => {
    setGatewayKey('k')
    mockFetchOnce(201, {
      id: 'key-1',
      team_id: 't-1',
      owner_id: 'o-1',
      key: 'mgw-secret-plaintext',
      key_hash_prefix: 'a1b2',
      scopes: ['gateway'],
      enabled: true,
      created_at: '2026-08-17T00:00:00Z',
    })
    const created = await apiKeysApi.create({ team_id: 't-1', owner_id: 'o-1', scopes: ['gateway'] })
    expect(created.key).toBe('mgw-secret-plaintext')

    mockFetchOnce(200, [
      {
        id: 'key-1',
        team_id: 't-1',
        owner_id: 'o-1',
        key_hash_prefix: 'a1b2',
        scopes: ['gateway'],
        enabled: true,
        created_at: '2026-08-17T00:00:00Z',
      },
    ])
    const listed = await apiKeysApi.list()
    expect('key' in listed[0]).toBe(false)
  })
})
