<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'

const props = defineProps<{
  apiBase: string
  apiKey: string
}>()

const emit = defineEmits<{
  (event: 'back'): void
  (event: 'next'): void
}>()

type ConnectionState = 'idle' | 'loading' | 'success' | 'error'

const connectionState = ref<ConnectionState>('idle')
const resultMessage = ref('')

const resultClasses = computed(() => {
  if (connectionState.value === 'success') {
    return 'border-green-200 bg-green-50 text-green-700'
  }
  if (connectionState.value === 'error') {
    return 'border-red-200 bg-red-50 text-red-700'
  }
  return 'border-gray-200 bg-gray-50 text-gray-700'
})

async function testConnection() {
  connectionState.value = 'loading'
  resultMessage.value = '测试中...'

  try {
    const response = await fetch(`${props.apiBase}/api/health`, {
      headers: { Authorization: `Bearer ${props.apiKey}` },
    })

    if (response.ok) {
      connectionState.value = 'success'
      resultMessage.value = '连接成功！API Key 工作正常'
      return
    }

    connectionState.value = 'error'
    resultMessage.value = `连接失败：${response.status} ${response.statusText}`.trim()
  } catch (error) {
    connectionState.value = 'error'
    resultMessage.value = `连接失败：${error instanceof Error ? error.message : String(error)}`
  }
}
</script>

<template>
  <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
    <div>
      <h2 class="text-lg font-medium">步骤 4：测试 API 调用</h2>
      <p class="text-sm text-gray-500">验证 API Key 是否正常工作</p>
    </div>
    <div class="rounded bg-gray-50 p-4">
      <div class="text-sm font-medium text-gray-700">使用您的 API Key 测试连接：</div>
      <pre class="mt-2 overflow-x-auto rounded bg-white p-3 font-mono text-xs">curl -H "Authorization: Bearer {{ apiKey }}" \
  {{ apiBase }}/api/health</pre>
    </div>
    <button
      class="flex min-h-11 w-full items-center justify-center gap-2 rounded bg-green-600 px-4 py-2 text-sm text-white transition-colors hover:bg-green-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-green-600 focus-visible:ring-offset-2 disabled:cursor-wait disabled:opacity-60"
      :disabled="connectionState === 'loading'"
      type="button"
      @click="testConnection"
    >
      <AppIcon
        :class="['h-4 w-4', { 'animate-spin': connectionState === 'loading' }]"
        :name="connectionState === 'loading' ? 'loader' : 'plug'"
      />
      {{ connectionState === 'loading' ? '测试中...' : '测试连接' }}
    </button>
    <div
      v-if="connectionState !== 'idle'"
      aria-live="polite"
      class="flex items-start gap-2 rounded border px-3 py-2 text-sm"
      :class="resultClasses"
      role="status"
    >
      <AppIcon
        v-if="connectionState !== 'loading'"
        class="mt-0.5 h-4 w-4 shrink-0"
        :name="connectionState === 'success' ? 'circle-check' : 'circle-x'"
      />
      <span>{{ resultMessage }}</span>
    </div>
    <div class="flex justify-between">
      <button
        class="min-h-11 rounded border border-gray-300 px-4 py-2 text-sm transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
        type="button"
        @click="emit('back')"
      >
        上一步
      </button>
      <button
        class="min-h-11 rounded bg-blue-600 px-4 py-2 text-sm text-white transition-colors hover:bg-blue-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
        type="button"
        @click="emit('next')"
      >
        下一步
      </button>
    </div>
  </section>
</template>
