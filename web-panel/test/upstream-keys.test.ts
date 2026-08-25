import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { upstreamKeysApi, setGatewayKey } from '@/api/client'
import UpstreamKeyDialog from '@/components/UpstreamKeyDialog.vue'
import UpstreamKeyList from '@/components/UpstreamKeyList.vue'

function mockFetchOnce(status: number, body: unknown) {
  return vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))
}

describe('upstreamKeysApi', () => {
  beforeEach(() => {
    setGatewayKey('')
    vi.restoreAllMocks()
  })

  it('按 Team 查询并将数组响应归一为 keys 字段', async () => {
    setGatewayKey('gateway-key')
    const fetchMock = mockFetchOnce(200, [{
      provider: 'anthropic',
      key_prefix: 'sk-ant-****',
      configured_at: '2026-08-20T00:00:00Z',
      last_tested_at: null,
      test_status: 'unknown',
    }])

    const result = await upstreamKeysApi.list('team-1')

    expect(result.keys).toHaveLength(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/upstreams/keys?team_id=team-1', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer gateway-key' }),
    }))
  })

  it('保存、测试和删除使用同一 Team/Provider 契约', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ provider: 'openai', key_prefix: 'sk-****' }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ success: true, provider: 'openai', available_models: ['gpt-4.1-mini'] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))

    await upstreamKeysApi.save({ team_id: 'team-1', provider: 'openai', api_key: 'sk-test-value' })
    await upstreamKeysApi.test({ team_id: 'team-1', provider: 'openai' })
    await upstreamKeysApi.remove({ team_id: 'team-1', provider: 'openai' })

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/upstreams/keys', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ team_id: 'team-1', provider: 'openai', api_key: 'sk-test-value' }),
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/upstreams/keys/test', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ team_id: 'team-1', provider: 'openai' }),
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/upstreams/keys', expect.objectContaining({
      method: 'DELETE',
      body: JSON.stringify({ team_id: 'team-1', provider: 'openai' }),
    }))
  })
})

describe('upstream key components', () => {
  const providers = [
    { value: 'anthropic' as const, label: 'Anthropic' },
    { value: 'openai' as const, label: 'OpenAI' },
    { value: 'codex' as const, label: 'Codex' },
  ]

  it('keeps the secret masked and exposes disabled test/save states', async () => {
    const wrapper = mount(UpstreamKeyDialog, {
      props: {
        providers,
        selectedProvider: 'anthropic',
        apiKey: 'sk-ant-secret-value',
        submitting: false,
        testing: false,
        canSave: false,
        canTest: false,
        feedback: '',
        feedbackKind: 'success',
        availableModels: [],
      },
    })

    expect(wrapper.get('input').attributes('type')).toBe('password')
    const actionButtons = wrapper.findAll('button')
    expect(actionButtons.find((button) => button.text() === '测试连接')?.attributes('disabled')).toBeDefined()
    expect(actionButtons.find((button) => button.text() === '保存 Key')?.attributes('disabled')).toBeDefined()

    await wrapper.get('select').setValue('openai')
    expect(wrapper.emitted('update:selectedProvider')?.[0]).toEqual(['openai'])
  })

  it('renders configured status without exposing a full key and emits deletion', async () => {
    const wrapper = mount(UpstreamKeyList, {
      props: {
        loading: false,
        selectedTeamId: 'team-1',
        rows: [{
          value: 'anthropic',
          label: 'Anthropic',
          capability: 'Claude Messages API',
          summary: {
            provider: 'anthropic',
            key_prefix: 'sk-ant-api****',
            configured_at: '2026-08-20T00:00:00Z',
            last_tested_at: '2026-08-20T01:00:00Z',
            test_status: 'success',
          },
        }],
      },
    })

    expect(wrapper.text()).toContain('连接正常')
    expect(wrapper.text()).toContain('sk-ant-api****')
    expect(wrapper.text()).not.toContain('sk-ant-secret-value')

    await wrapper.findAll('button').find((button) => button.text() === '删除')!.trigger('click')
    expect(wrapper.emitted('remove')?.[0]).toEqual(['anthropic'])
  })
})
