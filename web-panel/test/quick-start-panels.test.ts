import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ConnectionTestPanel from '@/components/ConnectionTestPanel.vue'
import SetupCompletePanel from '@/components/SetupCompletePanel.vue'

describe('ConnectionTestPanel', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('成功响应显示 SVG 成功图标并发送 Bearer 凭据', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(null, { status: 200 }))
    const wrapper = mount(ConnectionTestPanel, {
      props: { apiBase: 'http://127.0.0.1:8096', apiKey: 'mgw-test-key' },
    })

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith('http://127.0.0.1:8096/api/health', {
      headers: { Authorization: 'Bearer mgw-test-key' },
    })
    expect(wrapper.get('[role="status"]').text()).toContain('连接成功')
    expect(wrapper.find('[data-icon="circle-check"]').exists()).toBe(true)
  })

  it('失败响应显示 SVG 错误图标和状态码', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(null, { status: 503, statusText: 'Service Unavailable' }),
    )
    const wrapper = mount(ConnectionTestPanel, {
      props: { apiBase: 'http://127.0.0.1:8096', apiKey: 'mgw-test-key' },
    })

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="status"]').text()).toContain('连接失败：503 Service Unavailable')
    expect(wrapper.find('[data-icon="circle-x"]').exists()).toBe(true)
  })

  it('网络异常显示可读的错误信息', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('网络不可达'))
    const wrapper = mount(ConnectionTestPanel, {
      props: { apiBase: 'http://127.0.0.1:8096', apiKey: 'mgw-test-key' },
    })

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="status"]').text()).toContain('连接失败：网络不可达')
    expect(wrapper.find('[data-icon="circle-x"]').exists()).toBe(true)
  })
})

describe('SetupCompletePanel', () => {
  it('完成摘要和推荐区域使用统一 SVG 图标', () => {
    const wrapper = mount(SetupCompletePanel, {
      props: { teamName: '团队', keyName: '密钥', cardName: '身份卡' },
    })

    expect(wrapper.find('[data-icon="clipboard-check"]').exists()).toBe(true)
    expect(wrapper.find('[data-icon="book-open"]').exists()).toBe(true)
    expect(wrapper.text()).not.toMatch(/[\u{1F1E6}-\u{1FAFF}\u{2600}-\u{27BF}]/u)
  })
})
