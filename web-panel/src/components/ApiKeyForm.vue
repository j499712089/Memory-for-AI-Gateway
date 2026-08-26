<script setup lang="ts">
// API Key 创建表单（api-contract.md §3.3）
import { reactive } from 'vue'
import type { ApiKeyCreateInput } from '@/api/types'

const props = defineProps<{
  teams: { id: string; name: string }[]
  submitting?: boolean
  error?: string
}>()

const emit = defineEmits<{
  (e: 'submit', input: ApiKeyCreateInput): void
  (e: 'cancel'): void
}>()

const form = reactive<ApiKeyCreateInput>({
  team_id: props.teams[0]?.id ?? '',
  owner_id: '',
  name: '',
  scopes: ['gateway'],
  expires_at: null,
})

function toggleScope(scope: string) {
  const idx = form.scopes.indexOf(scope)
  if (idx >= 0) form.scopes.splice(idx, 1)
  else form.scopes.push(scope)
}

function onSubmit() {
  if (!form.team_id || !form.owner_id) return
  emit('submit', {
    team_id: form.team_id,
    owner_id: form.owner_id,
    name: form.name || undefined,
    scopes: form.scopes,
    expires_at: form.expires_at,
  })
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="onSubmit">
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">所属团队 *</label>
      <select
        v-model="form.team_id"
        required
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      >
        <option v-for="t in teams" :key="t.id" :value="t.id">{{ t.name }}</option>
      </select>
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">Owner ID *（用户/代理 id）</label>
      <input
        v-model="form.owner_id"
        required
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="UUID"
      />
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">名称（可选）</label>
      <input
        v-model="form.name"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="如 CI 部署 key"
      />
    </div>
    <div>
      <span class="mb-1 block text-sm font-medium text-gray-700">Scopes</span>
      <div class="flex gap-4">
        <label v-for="s in ['gateway', 'mcp', 'admin']" :key="s" class="flex items-center gap-2 text-sm">
          <input type="checkbox" :checked="form.scopes.includes(s)" @change="toggleScope(s)" />
          {{ s }}
        </label>
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
        :disabled="submitting || !form.team_id || !form.owner_id"
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
      >
        {{ submitting ? '提交中…' : '创建 Key' }}
      </button>
    </div>
  </form>
</template>
