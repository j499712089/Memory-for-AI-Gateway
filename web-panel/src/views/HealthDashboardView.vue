<script setup lang="ts">
// 录入健康看板（api-contract.md §3.6）：服务级健康 + 录入健康明细
// 依赖 IMP-03 Phase 3a /api/recording-health 数据；未就绪时展示占位
import { onMounted, ref } from 'vue'
import { healthApi } from '@/api/client'
import type { HealthStatus, RecordingHealthItem } from '@/api/types'
import DataTable, { type Column } from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'

const health = ref<HealthStatus | null>(null)
const items = ref<RecordingHealthItem[]>([])
const total = ref(0)
const loading = ref(false)
const healthError = ref('')
const recordingError = ref('')

const columns: Column[] = [
  { key: 'request_id', label: 'Request ID', slot: 'request_id' },
  { key: 'conversation_id', label: '会话' },
  { key: 'inbound_at', label: '入站时间' },
  { key: 'terminal_at', label: '终态时间' },
  { key: 'terminal_status', label: '终态', slot: 'terminal_status' },
  { key: 'compensation_status', label: '补偿', slot: 'compensation_status' },
  { key: 'binding_version', label: '绑定版本' },
  { key: 'l0_path', label: 'L0 路径' },
]

function fmt(v: unknown): string {
  return v ? new Date(v as string).toLocaleString('zh-CN') : '—'
}

function shortId(v: unknown): string {
  const s = String(v ?? '—')
  return s.length > 12 ? s.slice(0, 12) + '…' : s
}

async function load() {
  loading.value = true
  healthError.value = ''
  recordingError.value = ''
  try {
    const h = await healthApi.get()
    health.value = h
  } catch (e) {
    healthError.value = e instanceof Error ? e.message : String(e)
  }
  try {
    const r = await healthApi.recording({ limit: 100 })
    items.value = Array.isArray(r) ? r : r.items
    total.value = Array.isArray(r) ? r.length : r.total
  } catch (e) {
    recordingError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold">录入健康看板</h1>
      <p class="text-sm text-gray-500">服务健康 + L0 录入明细（数据来自 Phase 3 的 /api/recording-health）</p>
      <button class="mt-2 text-sm text-blue-600 hover:underline" @click="load">刷新</button>
    </div>

    <section v-if="health" class="grid grid-cols-2 gap-4 sm:grid-cols-4">
      <div class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
        <div class="text-xs uppercase text-gray-500">服务状态</div>
        <div class="mt-1 text-lg font-semibold">
          <StatusBadge :status="health.status" />
        </div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
        <div class="text-xs uppercase text-gray-500">版本</div>
        <div class="mt-1 font-mono text-lg font-semibold">{{ health.version }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
        <div class="text-xs uppercase text-gray-500">DB</div>
        <div class="mt-1"><StatusBadge :status="health.checks.db" /></div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
        <div class="text-xs uppercase text-gray-500">Secrets</div>
        <div class="mt-1"><StatusBadge :status="health.checks.secrets" /></div>
      </div>
    </section>
    <p v-else-if="healthError" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      /api/health 不可用：{{ healthError }}
    </p>

    <section class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <div class="mb-2 flex items-center justify-between">
        <h2 class="text-base font-medium">录入健康明细</h2>
        <span v-if="!loading" class="text-sm text-gray-500">共 {{ total }} 条</span>
      </div>
      <p v-if="recordingError" class="mb-2 text-sm text-red-600">
        /api/recording-health 未就绪（等待 IMP-03）：{{ recordingError }}
      </p>
      <DataTable
        :columns="columns"
        :rows="items"
        :loading="loading"
        empty-text="暂无录入数据"
      >
        <template #request_id="{ row }">
          <span class="font-mono text-xs">{{ shortId((row as RecordingHealthItem).request_id) }}</span>
        </template>
        <template #terminal_status="{ row }">
          <StatusBadge :status="(row as RecordingHealthItem).terminal_status" />
        </template>
        <template #compensation_status="{ row }">
          <StatusBadge :status="(row as RecordingHealthItem).compensation_status" />
        </template>
        <template #inbound_at="{ row }">
          {{ fmt((row as RecordingHealthItem).inbound_at) }}
        </template>
        <template #terminal_at="{ row }">
          {{ fmt((row as RecordingHealthItem).terminal_at) }}
        </template>
        <template #l0_path="{ row }">
          <span class="font-mono text-xs">{{ (row as RecordingHealthItem).l0_path }}</span>
        </template>
      </DataTable>
    </section>
  </div>
</template>
