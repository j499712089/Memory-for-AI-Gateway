<script setup lang="ts">
// 身份卡创建/编辑表单（api-contract.md §3.2，版本乐观锁）
import { reactive } from 'vue'
import type { IdentityCard, IdentityCardCreateInput } from '@/api/types'

const props = defineProps<{
  initial?: IdentityCard | null
  submitting?: boolean
  error?: string
}>()

const emit = defineEmits<{
  (e: 'submit', input: IdentityCardCreateInput & { version?: number }): void
  (e: 'cancel'): void
}>()

const toolsText = reactive({ value: '' })

const form = reactive<IdentityCardCreateInput>({
  name: props.initial?.name ?? '',
  role: props.initial?.role ?? '',
  responsibilities: props.initial?.responsibilities ?? '',
  boundaries: props.initial?.boundaries ?? '',
  allowed_tools: props.initial?.allowed_tools ?? [],
  style: props.initial?.style ?? '',
  visibility: props.initial?.visibility ?? 'team',
  agent_id: props.initial?.agent_id ?? null,
})

if (props.initial?.allowed_tools?.length) {
  toolsText.value = props.initial.allowed_tools.join(', ')
}

function onSubmit() {
  const allowed_tools = toolsText.value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
  emit('submit', {
    ...form,
    allowed_tools,
    ...(props.initial ? { version: props.initial.version } : {}),
  })
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="onSubmit">
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">名称 *</label>
        <input
          v-model="form.name"
          required
          class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="如 Memory Gateway 后端工程师"
        />
      </div>
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">角色（role）*</label>
        <input
          v-model="form.role"
          required
          class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="如 backend-desktop"
        />
      </div>
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">职责</label>
      <textarea
        v-model="form.responsibilities"
        rows="2"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      />
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">边界（不改动范围）</label>
      <textarea
        v-model="form.boundaries"
        rows="2"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      />
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">允许工具（逗号分隔）</label>
      <input
        v-model="toolsText.value"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="bash, edit, read"
      />
    </div>
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">风格</label>
        <input
          v-model="form.style"
          class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="如 简洁"
        />
      </div>
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">可见性</label>
        <select
          v-model="form.visibility"
          class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="team">team</option>
          <option value="private">private</option>
          <option value="org">org</option>
        </select>
      </div>
    </div>
    <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
    <div class="flex justify-end gap-2 pt-2">
      <button
        type="button"
        class="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
        @click="emit('cancel')"
      >
        取消
      </button>
      <button
        type="submit"
        :disabled="submitting"
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
      >
        {{ submitting ? '提交中…' : '保存' }}
      </button>
    </div>
  </form>
</template>
