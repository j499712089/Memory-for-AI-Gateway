<script setup lang="ts">
// 上游通道列表 + models.json 导入（api-contract.md §3.4）
import { onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useUpstreamsStore } from '@/stores/upstreams'
import DataTable, { type Column } from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'

const store = useUpstreamsStore()
const { channels, loading, error } = storeToRefs(store)

const importPath = ref('C:\\Users\\Administrator\\.codebuddy\\models.json')
const importing = ref(false)
const importOk = ref('')

const columns: Column[] = [
  { key: 'name', label: '名称' },
  { key: 'protocol', label: '协议' },
  { key: 'model', label: '模型' },
  { key: 'base_url', label: 'Base URL' },
  { key: 'priority', label: '优先级' },
  { key: 'enabled', label: '状态', slot: 'status' },
]

async function doImport() {
  importing.value = true
  importOk.value = ''
  try {
    const r = await store.importModels({ path: importPath.value })
    importOk.value = `导入完成：新增 ${r.imported}，跳过 ${r.skipped}`
  } finally {
    importing.value = false
  }
}

onMounted(() => store.fetchChannels())
</script>

<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-xl font-semibold">上游通道</h1>
      <p class="text-sm text-gray-500">从 models.json 导入或手动维护；密钥只存 key_ref</p>
    </div>

    <section class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <h2 class="mb-2 text-base font-medium">从 models.json 导入</h2>
      <form class="flex items-center gap-2" @submit.prevent="doImport">
        <input
          v-model="importPath"
          class="w-full max-w-lg rounded border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="models.json 绝对路径"
        />
        <button
          type="submit"
          :disabled="importing"
          class="shrink-0 rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
        >
          {{ importing ? '导入中…' : '导入' }}
        </button>
      </form>
      <p v-if="importOk" class="mt-2 text-sm text-green-600">{{ importOk }}</p>
      <p v-if="error" class="mt-2 text-sm text-red-600">{{ error }}</p>
    </section>

    <DataTable :columns="columns" :rows="channels" :loading="loading" empty-text="暂无上游通道">
      <template #status="{ row }">
        <StatusBadge :status="(row as { enabled: boolean }).enabled ? 'active' : 'disabled'" />
      </template>
      <template #base_url="{ row }">
        <span class="font-mono text-xs">{{ (row as { base_url: string }).base_url }}</span>
      </template>
    </DataTable>
  </div>
</template>
