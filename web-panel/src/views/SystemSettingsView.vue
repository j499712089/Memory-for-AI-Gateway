<script setup lang="ts">
import { ref, onMounted } from 'vue'
import StatusBadge from '@/components/StatusBadge.vue'

interface ServiceStatus {
  running: boolean
  pid?: number
  uptime?: string
  version: string
}

interface SystemConfig {
  autostart_enabled: boolean
  db_path: string
  obsidian_path?: string
  vector_path?: string
}

interface LogEntry {
  timestamp: string
  level: 'info' | 'warn' | 'error'
  message: string
}

const serviceStatus = ref<ServiceStatus>({ running: false, version: '1.0.0' })
const systemConfig = ref<SystemConfig>({ autostart_enabled: false, db_path: '' })
const logs = ref<LogEntry[]>([])
const loading = ref(false)
const actionLoading = ref('')
const error = ref('')
const logFilter = ref<'all' | 'error' | 'warn'>('all')

const apiBase = import.meta.env.VITE_API_BASE_URL || 'http://127.0.0.1:8096'

async function loadServiceStatus() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch(`${apiBase}/api/system/service/status`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    serviceStatus.value = await res.json()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

async function loadSystemConfig() {
  try {
    const res = await fetch(`${apiBase}/api/system/config`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    systemConfig.value = await res.json()
  } catch (e) {
    console.error('Failed to load system config:', e)
  }
}

async function loadLogs() {
  try {
    const res = await fetch(`${apiBase}/api/system/logs?limit=100`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    logs.value = await res.json()
  } catch (e) {
    console.error('Failed to load logs:', e)
  }
}

async function serviceAction(action: 'start' | 'stop' | 'restart') {
  actionLoading.value = action
  error.value = ''
  try {
    const res = await fetch(`${apiBase}/api/system/service/${action}`, { method: 'POST' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    await loadServiceStatus()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    actionLoading.value = ''
  }
}

async function toggleAutostart() {
  const newValue = !systemConfig.value.autostart_enabled
  try {
    const res = await fetch(`${apiBase}/api/system/autostart`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: newValue })
    })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    systemConfig.value.autostart_enabled = newValue
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

async function exportConfig() {
  try {
    const res = await fetch(`${apiBase}/api/system/config/export`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `gateway-config-${new Date().toISOString().split('T')[0]}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

function handleImportConfig(event: Event) {
  const input = event.target as HTMLInputElement
  if (!input.files?.length) return
  const file = input.files[0]
  const reader = new FileReader()
  reader.onload = async (e) => {
    try {
      const config = JSON.parse(e.target?.result as string)
      const res = await fetch(`${apiBase}/api/system/config/import`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config)
      })
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      await loadSystemConfig()
      alert('配置导入成功')
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }
  reader.readAsText(file)
}

const filteredLogs = ref<LogEntry[]>([])
function applyLogFilter() {
  if (logFilter.value === 'all') {
    filteredLogs.value = logs.value
  } else {
    filteredLogs.value = logs.value.filter(log => log.level === logFilter.value)
  }
}

function formatTimestamp(ts: string): string {
  return new Date(ts).toLocaleString('zh-CN')
}

onMounted(async () => {
  await Promise.all([loadServiceStatus(), loadSystemConfig(), loadLogs()])
  applyLogFilter()
})
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold">系统设置</h1>
      <p class="text-sm text-gray-500">Gateway 服务控制、配置管理、日志查看</p>
    </div>

    <p v-if="error" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ error }}
    </p>

    <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div class="flex items-center justify-between">
        <h2 class="text-base font-medium">服务控制</h2>
        <button class="text-sm text-blue-600 hover:underline" @click="loadServiceStatus">刷新</button>
      </div>

      <div class="grid grid-cols-3 gap-4">
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-xs uppercase text-gray-500">运行状态</div>
          <div class="mt-2">
            <StatusBadge :status="serviceStatus.running ? 'active' : 'inactive'" />
          </div>
        </div>
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-xs uppercase text-gray-500">进程 PID</div>
          <div class="mt-2 font-mono text-lg font-semibold">{{ serviceStatus.pid || '—' }}</div>
        </div>
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-xs uppercase text-gray-500">运行时长</div>
          <div class="mt-2 text-lg font-semibold">{{ serviceStatus.uptime || '—' }}</div>
        </div>
      </div>

      <div class="flex gap-2">
        <button
          :disabled="serviceStatus.running || actionLoading === 'start'"
          class="rounded bg-green-600 px-4 py-2 text-sm text-white hover:bg-green-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="serviceAction('start')"
        >
          {{ actionLoading === 'start' ? '启动中...' : '启动服务' }}
        </button>
        <button
          :disabled="!serviceStatus.running || actionLoading === 'stop'"
          class="rounded bg-red-600 px-4 py-2 text-sm text-white hover:bg-red-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="serviceAction('stop')"
        >
          {{ actionLoading === 'stop' ? '停止中...' : '停止服务' }}
        </button>
        <button
          :disabled="!serviceStatus.running || actionLoading === 'restart'"
          class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="serviceAction('restart')"
        >
          {{ actionLoading === 'restart' ? '重启中...' : '重启服务' }}
        </button>
      </div>
    </section>

    <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <h2 class="text-base font-medium">系统配置</h2>

      <div class="space-y-3">
        <div class="flex items-center justify-between rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div>
            <div class="text-sm font-medium">开机自启动</div>
            <div class="text-xs text-gray-500">系统启动时自动运行 Gateway 服务</div>
          </div>
          <label class="relative inline-block h-6 w-11 cursor-pointer">
            <input
              type="checkbox"
              :checked="systemConfig.autostart_enabled"
              class="peer sr-only"
              @change="toggleAutostart"
            />
            <span class="absolute inset-0 rounded-full bg-gray-300 transition peer-checked:bg-blue-600"></span>
            <span class="absolute left-1 top-1 h-4 w-4 rounded-full bg-white transition peer-checked:translate-x-5"></span>
          </label>
        </div>

        <div class="space-y-2 rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-sm font-medium">数据库路径</div>
          <div class="overflow-x-auto rounded bg-white p-2 font-mono text-xs text-gray-700">
            {{ systemConfig.db_path || '未配置' }}
          </div>
        </div>

        <div v-if="systemConfig.obsidian_path" class="space-y-2 rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-sm font-medium">Obsidian 存储路径</div>
          <div class="overflow-x-auto rounded bg-white p-2 font-mono text-xs text-gray-700">
            {{ systemConfig.obsidian_path }}
          </div>
        </div>

        <div v-if="systemConfig.vector_path" class="space-y-2 rounded-lg border border-gray-200 bg-gray-50 p-4">
          <div class="text-sm font-medium">向量存储路径</div>
          <div class="overflow-x-auto rounded bg-white p-2 font-mono text-xs text-gray-700">
            {{ systemConfig.vector_path }}
          </div>
        </div>
      </div>
    </section>

    <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <h2 class="text-base font-medium">备份与恢复</h2>
      <div class="flex gap-2">
        <button
          class="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
          @click="exportConfig"
        >
          导出配置
        </button>
        <label class="cursor-pointer rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50">
          导入配置
          <input type="file" accept=".json" class="hidden" @change="handleImportConfig" />
        </label>
      </div>
    </section>

    <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div class="flex items-center justify-between">
        <h2 class="text-base font-medium">系统日志</h2>
        <div class="flex gap-2">
          <select
            v-model="logFilter"
            class="rounded border border-gray-300 px-3 py-1 text-sm"
            @change="applyLogFilter"
          >
            <option value="all">全部</option>
            <option value="error">仅错误</option>
            <option value="warn">仅警告</option>
          </select>
          <button class="text-sm text-blue-600 hover:underline" @click="loadLogs">刷新</button>
        </div>
      </div>

      <div class="max-h-96 space-y-1 overflow-y-auto rounded border border-gray-200 bg-gray-50 p-3 font-mono text-xs">
        <div
          v-for="(log, i) in filteredLogs.slice(0, 100)"
          :key="i"
          class="flex gap-2"
          :class="{
            'text-red-600': log.level === 'error',
            'text-yellow-600': log.level === 'warn',
            'text-gray-700': log.level === 'info'
          }"
        >
          <span class="text-gray-400">{{ formatTimestamp(log.timestamp) }}</span>
          <span class="font-medium uppercase">{{ log.level }}</span>
          <span>{{ log.message }}</span>
        </div>
        <div v-if="filteredLogs.length === 0" class="text-center text-gray-400">暂无日志</div>
      </div>
    </section>
  </div>
</template>
