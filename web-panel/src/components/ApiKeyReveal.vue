<script setup lang="ts">
// 明文 API Key 一次性展示 + 复制（安全纪律：明文只出现这一次，刷新即失）
import { ref } from 'vue'

const props = defineProps<{
  keyValue: string
}>()

const emit = defineEmits<{
  (e: 'dismiss'): void
}>()

const copied = ref(false)
const revealed = ref(false)

async function copy() {
  try {
    await navigator.clipboard.writeText(props.keyValue)
  } catch {
    // 剪贴板不可用时降级：选中文本由用户手动复制
    const el = document.querySelector<HTMLInputElement>('[data-mgw-key]')
    el?.select()
  }
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}
</script>

<template>
  <div class="rounded-lg border border-amber-300 bg-amber-50 p-4">
    <div class="mb-2 flex items-center justify-between">
      <span class="text-sm font-medium text-amber-800">
        ⚠️ 明文 Key 仅此一次展示，关闭后无法再次查看
      </span>
      <button class="text-sm text-amber-700 hover:underline" @click="emit('dismiss')">关闭</button>
    </div>
    <div class="flex items-center gap-2">
      <input
        v-if="revealed"
        :value="keyValue"
        data-mgw-key
        readonly
        class="w-full rounded border border-amber-300 bg-white px-3 py-2 font-mono text-sm"
      />
      <button
        v-else
        class="w-full rounded border border-amber-300 bg-white px-3 py-2 text-left font-mono text-sm"
        @click="revealed = true"
      >
        点击显示明文 Key
      </button>
      <button
        class="shrink-0 rounded bg-amber-600 px-3 py-2 text-sm text-white hover:bg-amber-500 disabled:opacity-50"
        :disabled="!revealed"
        @click="copy"
      >
        {{ copied ? '已复制' : '复制' }}
      </button>
    </div>
  </div>
</template>
