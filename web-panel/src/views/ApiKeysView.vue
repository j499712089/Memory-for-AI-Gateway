<script setup lang="ts">
// API Key 列表 + 创建（明文一次性展示）+ 吊销/启用（api-contract.md §3.3）
import { onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useTeamsStore } from '@/stores/teams'
import { useApiKeysStore } from '@/stores/apiKeys'
import type { ApiKeyCreateInput, ApiKeyCreated } from '@/api/types'
import ApiKeyForm from '@/components/ApiKeyForm.vue'
import ApiKeyReveal from '@/components/ApiKeyReveal.vue'
import DataTable, { type Column } from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'

const teamsStore = useTeamsStore()
const keysStore = useApiKeysStore()
const { keys, loading, error } = storeToRefs(keysStore)

const showCreate = ref(false)
const submitting = ref(false)
const createdKey = ref<ApiKeyCreated | null>(null)

const columns: Column[] = [
  { key: 'name', label: '名称', slot: 'name' },
  { key: 'key_hash_prefix', label: 'Key 前缀' },
  { key: 'scopes', label: 'Scopes' },
  { key: 'enabled', label: '状态', slot: 'status' },
  { key: 'created_at', label: '创建时间' },
  { key: 'expires_at', label: '过期时间' },
  { key: 'actions', label: '操作', slot: 'actions' },
]

function fmt(v: unknown): string {
  return v ? new Date(v as string).toLocaleString('zh-CN') : '—'
}

async function onSubmit(input: ApiKeyCreateInput) {
  submitting.value = true
  try {
    createdKey.value = await keysStore.createKey(input)
    showCreate.value = false
  } finally {
    submitting.value = false
  }
}

async function toggleRevoke(row: unknown) {
  const k = row as { id: string; enabled: boolean }
  if (k.enabled) {
    if (confirm('吊销该 API Key？吊销后调用将返回 401/410')) await keysStore.revokeKey(k.id)
  } else {
    await keysStore.setEnabled(k.id, true)
  }
}

onMounted(async () => {
  if (teamsStore.teams.length === 0) await teamsStore.fetchTeams()
  await keysStore.fetchKeys()
})
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold">API Key 管理</h1>
        <p class="text-sm text-gray-500">明文 Key 仅在创建时展示一次；其余全部掩码</p>
      </div>
      <button
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500"
        @click="showCreate = !showCreate"
      >
        {{ showCreate ? '收起' : '+ 创建 API Key' }}
      </button>
    </div>

    <p v-if="error" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ error }}
    </p>

    <ApiKeyReveal v-if="createdKey" :key-value="createdKey.key" @dismiss="createdKey = null" />

    <div v-if="showCreate" class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <ApiKeyForm
        :teams="teamsStore.teams"
        :submitting="submitting"
        :error="error"
        @submit="onSubmit"
        @cancel="showCreate = false"
      />
    </div>

    <DataTable :columns="columns" :rows="keys" :loading="loading" empty-text="暂无 API Key">
      <template #name="{ row }">
        {{ (row as { name?: string }).name || '（未命名）' }}
      </template>
      <template #status="{ row }">
        <StatusBadge :status="(row as { enabled: boolean; revoked_at: string | null }).enabled ? 'active' : 'disabled'" />
      </template>
      <template #created_at="{ row }">
        {{ fmt((row as Record<string, unknown>).created_at) }}
      </template>
      <template #expires_at="{ row }">
        {{ fmt((row as Record<string, unknown>).expires_at) }}
      </template>
      <template #actions="{ row }">
        <button
          class="text-sm hover:underline"
          :class="(row as { enabled: boolean }).enabled ? 'text-red-600' : 'text-green-600'"
          @click="toggleRevoke(row)"
        >
          {{ (row as { enabled: boolean }).enabled ? '吊销' : '重新启用' }}
        </button>
      </template>
    </DataTable>
  </div>
</template>
