<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import type { UpstreamKeyTestResponse, UpstreamProvider } from '@/api/types'

type UpstreamKeyTestPayload = Omit<UpstreamKeyTestResponse, 'error'> & { error?: string | { message?: string } | null; message?: string }
const props = defineProps<{
  apiBase: string
  apiKey: string
  teamId: string
}>()
const emit = defineEmits<{
  (event: 'back'): void
  (event: 'skip'): void
  (event: 'complete', provider: UpstreamProvider): void
}>()
const providers: Array<{
  value: UpstreamProvider
  name: string
  detail: string
  description: string
  docs: string
  recommended?: boolean
}> = [
  {
    value: 'anthropic',
    name: 'Anthropic',
    detail: 'Claude',
    description: '适合 Claude 模型与 Anthropic Messages API',
    docs: 'https://console.anthropic.com',
    recommended: true,
  },
  {
    value: 'openai',
    name: 'OpenAI',
    detail: 'GPT',
    description: '适合 GPT 模型与 OpenAI 兼容接口',
    docs: 'https://platform.openai.com',
  },
  {
    value: 'codex',
    name: 'Codex',
    detail: 'Responses',
    description: '适合 Codex Responses API',
    docs: 'https://platform.openai.com',
  },
]
const selectedProvider = ref<UpstreamProvider>('anthropic')
const upstreamKey = ref('')
const savedProvider = ref<UpstreamProvider | null>(null)
const savedKey = ref('')
const submitting = ref(false)
const testing = ref(false)
const errorMessage = ref('')
const successMessage = ref('')
const availableModels = ref<string[]>([])
const canSubmit = computed(() => Boolean(props.apiKey.trim() && props.teamId.trim() && upstreamKey.value.trim()))
const currentProvider = computed(() => providers.find((provider) => provider.value === selectedProvider.value))
function apiUrl(path: string): string {
  return `${props.apiBase.replace(/\/$/, '')}${path}`
}

function clearFeedback() {
  errorMessage.value = ''
  successMessage.value = ''
  availableModels.value = []
}

function selectProvider(provider: UpstreamProvider) {
  selectedProvider.value = provider
  clearFeedback()
}

function setUpstreamKey(value: string) {
  upstreamKey.value = value
  savedProvider.value = null
  savedKey.value = ''
  clearFeedback()
}

async function responseMessage(response: Response): Promise<string> {
  const text = await response.text()
  if (!text) return `请求失败（HTTP ${response.status}）`

  try {
    const data = JSON.parse(text) as {
      error?: { message?: string } | string
      message?: string
      detail?: string
    }
    if (typeof data.error === 'string') return data.error
    if (data.error && typeof data.error === 'object' && data.error.message) return data.error.message
    if (data.message) return data.message
    if (data.detail) return data.detail
  } catch {
    return text.slice(0, 200)
  }

  return `请求失败（HTTP ${response.status}）`
}

async function readJson(response: Response): Promise<UpstreamKeyTestPayload> {
  const text = await response.text()
  if (!text) return {} as UpstreamKeyTestResponse
  try {
    return JSON.parse(text) as UpstreamKeyTestResponse
  } catch {
    return {} as UpstreamKeyTestResponse
  }
}

function payloadError(result: UpstreamKeyTestPayload, status: number): string {
  return typeof result.error === 'string' ? result.error : result.error?.message || result.message || `请求失败（HTTP ${status}）`
}

async function saveUpstreamKey(): Promise<boolean> {
  if (!canSubmit.value) {
    errorMessage.value = '请先填写 API Key，且确认当前 Gateway API Key 和 Team 已就绪。'
    return false
  }

  clearFeedback()
  submitting.value = true
  try {
    const response = await fetch(apiUrl('/api/upstreams/keys'), {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        Authorization: `Bearer ${props.apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        team_id: props.teamId,
        provider: selectedProvider.value,
        api_key: upstreamKey.value.trim(),
      }),
    })

    if (!response.ok) throw new Error(await responseMessage(response))
    savedProvider.value = selectedProvider.value
    savedKey.value = upstreamKey.value.trim()
    successMessage.value = `${currentProvider.value?.name || '上游'} API Key 已保存。`
    return true
  } catch (error) {
    errorMessage.value = `保存失败：${error instanceof Error ? error.message : String(error)}`
    return false
  } finally {
    submitting.value = false
  }
}

async function testConnection() {
  if (!canSubmit.value) {
    errorMessage.value = '请先填写 API Key，再测试连接。'
    return
  }

  if (savedProvider.value !== selectedProvider.value || savedKey.value !== upstreamKey.value.trim()) {
    const saved = await saveUpstreamKey()
    if (!saved) return
  }

  errorMessage.value = ''
  successMessage.value = ''
  availableModels.value = []
  testing.value = true
  try {
    const response = await fetch(apiUrl('/api/upstreams/keys/test'), {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        Authorization: `Bearer ${props.apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ team_id: props.teamId, provider: selectedProvider.value }),
    })
    const result = await readJson(response)
    if (!response.ok || result.success === false) {
      throw new Error(payloadError(result, response.status))
    }

    availableModels.value = result.available_models || result.models || []
    successMessage.value = availableModels.value.length
      ? `连接成功，可用模型：${availableModels.value.join('、')}`
      : '连接成功，API Key 可用。'
  } catch (error) {
    errorMessage.value = `连接失败：${error instanceof Error ? error.message : String(error)}`
  } finally {
    testing.value = false
  }
}

async function saveAndComplete() {
  if (submitting.value || testing.value) return
  const saved = await saveUpstreamKey()
  if (saved) emit('complete', selectedProvider.value)
}
</script>

<template>
  <section class="space-y-5 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
    <div>
      <h2 class="text-lg font-medium">步骤 6：配置上游通道</h2>
      <p class="mt-1 text-sm text-gray-500">Memory Gateway 是代理服务器，需要配置上游 LLM 服务商的 API Key。</p>
    </div>

    <fieldset class="space-y-2">
      <legend class="text-sm font-medium text-gray-700">选择服务商</legend>
      <div class="grid gap-2 md:grid-cols-3">
        <label
          v-for="provider in providers"
          :key="provider.value"
          class="relative flex min-h-24 cursor-pointer flex-col rounded border p-3 transition-colors hover:bg-gray-50"
          :class="selectedProvider === provider.value ? 'border-blue-600 bg-blue-50' : 'border-gray-200'"
        >
          <input
            class="sr-only"
            type="radio"
            name="upstream-provider"
            :value="provider.value"
            :checked="selectedProvider === provider.value"
            @change="selectProvider(provider.value)"
          />
          <span class="flex items-center justify-between text-sm font-medium text-gray-900">
            <span>{{ provider.name }} <span class="font-normal text-gray-500">({{ provider.detail }})</span></span>
            <span v-if="provider.recommended" class="text-xs font-normal text-blue-700">推荐</span>
          </span>
          <span class="mt-1 text-xs leading-5 text-gray-500">{{ provider.description }}</span>
        </label>
      </div>
    </fieldset>

    <div class="space-y-2">
      <label class="block text-sm font-medium text-gray-700" for="upstream-api-key">API Key</label>
      <input
        id="upstream-api-key"
        :value="upstreamKey"
        autocomplete="off"
        class="w-full rounded border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="粘贴服务商 API Key"
        type="password"
        @input="setUpstreamKey(($event.target as HTMLInputElement).value)"
      />
      <p class="text-xs text-gray-500">
        在哪里获取 API Key？
        <a
          class="text-blue-700 underline underline-offset-2"
          :href="currentProvider?.docs"
          rel="noopener noreferrer"
          target="_blank"
        >打开 {{ currentProvider?.name }} 控制台</a>
      </p>
    </div>

    <div v-if="errorMessage" aria-live="polite" class="flex items-start gap-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">
      <AppIcon class="mt-0.5 h-4 w-4 shrink-0" name="circle-x" />
      <span>{{ errorMessage }}</span>
    </div>
    <div v-if="successMessage" aria-live="polite" class="flex items-start gap-2 rounded border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-700" role="status">
      <AppIcon class="mt-0.5 h-4 w-4 shrink-0" name="circle-check" />
      <span>{{ successMessage }}</span>
    </div>

    <div class="flex flex-col-reverse gap-2 border-t border-gray-100 pt-4 sm:flex-row sm:items-center sm:justify-between">
      <button
        class="min-h-11 rounded border border-gray-300 px-4 py-2 text-sm transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
        type="button"
        @click="emit('back')"
      >
        上一步
      </button>
      <div class="flex flex-col gap-2 sm:flex-row">
        <button
          class="min-h-11 rounded border border-gray-300 px-4 py-2 text-sm transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
          type="button"
          @click="emit('skip')"
        >
          跳过此步
        </button>
        <button
          :disabled="submitting || testing"
          class="min-h-11 rounded border border-blue-600 px-4 py-2 text-sm text-blue-700 transition-colors hover:bg-blue-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2 disabled:cursor-wait disabled:opacity-60"
          type="button"
          @click="testConnection"
        >
          <AppIcon v-if="testing" class="mr-1 inline-block h-4 w-4 animate-spin" name="loader" />
          {{ testing ? '测试中...' : '测试连接' }}
        </button>
        <button
          :disabled="submitting || testing"
          class="min-h-11 rounded bg-blue-600 px-4 py-2 text-sm text-white transition-colors hover:bg-blue-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2 disabled:cursor-wait disabled:opacity-60"
          type="button"
          @click="saveAndComplete"
        >
          {{ submitting ? '保存中...' : '保存并完成' }}
        </button>
      </div>
    </div>
  </section>
</template>
