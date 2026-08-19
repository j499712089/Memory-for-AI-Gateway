// 表单组件测试：校验、提交载荷、明文 Key 一次性展示（Plan.md §4.3）
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TeamForm from '@/components/TeamForm.vue'
import IdentityCardForm from '@/components/IdentityCardForm.vue'
import ApiKeyReveal from '@/components/ApiKeyReveal.vue'

describe('TeamForm', () => {
  it('名称与 slug 输入框带 required 校验标记', () => {
    const wrapper = mount(TeamForm)
    const nameInput = wrapper.find('input[placeholder*="圣殿骑士团"]')
    const slugInput = wrapper.find('input[placeholder*="mhm-sk"]')
    expect(nameInput.attributes('required')).toBeDefined()
    expect(slugInput.attributes('required')).toBeDefined()
  })

  it('slug 留空时按名称自动生成', async () => {
    const wrapper = mount(TeamForm)
    await wrapper.find('input[placeholder*="圣殿骑士团"]').setValue('MHM-SK 圣殿骑士团')
    await wrapper.find('input[placeholder*="mhm-sk"]').trigger('blur')
    const slugInput = wrapper.find('input[placeholder*="mhm-sk"]')
    expect((slugInput.element as HTMLInputElement).value).toBe('mhm-sk')
  })

  it('提交时发出 TeamCreateInput 载荷', async () => {
    const wrapper = mount(TeamForm)
    await wrapper.find('input[placeholder*="圣殿骑士团"]').setValue('测试团队')
    await wrapper.find('input[placeholder*="mhm-sk"]').setValue('test-team')
    await wrapper.find('textarea').setValue('描述')
    await wrapper.find('form').trigger('submit')
    const emitted = wrapper.emitted('submit')
    expect(emitted).toBeTruthy()
    expect(emitted![0][0]).toMatchObject({
      name: '测试团队',
      slug: 'test-team',
      description: '描述',
      visibility: 'private',
    })
  })
})

describe('IdentityCardForm', () => {
  it('提交包含 allowed_tools 数组与编辑版本号', async () => {
    const wrapper = mount(IdentityCardForm, {
      props: { initial: { version: 3, allowed_tools: ['bash'] } as never },
    })
    await wrapper.find('input[placeholder*="后端工程师"]').setValue('测试卡')
    await wrapper.find('input[placeholder*="backend-desktop"]').setValue('tester')
    await wrapper.find('input[placeholder*="bash, edit"]').setValue('bash, read')
    await wrapper.find('form').trigger('submit')
    const emitted = wrapper.emitted('submit')
    expect(emitted).toBeTruthy()
    const payload = emitted![0][0] as { allowed_tools: string[]; version: number }
    expect(payload.allowed_tools).toEqual(['bash', 'read'])
    expect(payload.version).toBe(3)
  })

  it('新建（无 initial）时不携带 version', async () => {
    const wrapper = mount(IdentityCardForm)
    await wrapper.find('input[placeholder*="后端工程师"]').setValue('新卡')
    await wrapper.find('input[placeholder*="backend-desktop"]').setValue('newbie')
    await wrapper.find('form').trigger('submit')
    const payload = wrapper.emitted('submit')![0][0] as { version?: number }
    expect(payload.version).toBeUndefined()
  })
})

describe('ApiKeyReveal', () => {
  it('用 SVG 告警图标替代功能性 emoji', () => {
    const wrapper = mount(ApiKeyReveal, { props: { keyValue: 'mgw-top-secret' } })
    expect(wrapper.find('[data-icon="triangle-alert"]').exists()).toBe(true)
    expect(wrapper.text()).not.toMatch(/[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}]/u)
  })

  it('明文默认隐藏，点击显示按钮后才出现', async () => {
    const wrapper = mount(ApiKeyReveal, { props: { keyValue: 'mgw-top-secret' } })
    expect(wrapper.find('input[data-mgw-key]').exists()).toBe(false)
    const revealBtn = wrapper.findAll('button').find((b) => b.text().includes('显示明文'))
    expect(revealBtn).toBeTruthy()
    await revealBtn!.trigger('click')
    expect(wrapper.find('input[data-mgw-key]').exists()).toBe(true)
    expect((wrapper.find('input[data-mgw-key]').element as HTMLInputElement).value).toBe('mgw-top-secret')
  })

  it('复制按钮调用 clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    const wrapper = mount(ApiKeyReveal, { props: { keyValue: 'mgw-copy-me' } })
    const revealBtn = wrapper.findAll('button').find((b) => b.text().includes('显示明文'))
    await revealBtn!.trigger('click')
    const copyBtn = wrapper.findAll('button').find((b) => b.text() === '复制')
    expect(copyBtn).toBeTruthy()
    await copyBtn!.trigger('click')
    expect(writeText).toHaveBeenCalledWith('mgw-copy-me')
  })
})
