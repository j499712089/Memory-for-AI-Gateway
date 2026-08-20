<script setup lang="ts">
import type { UpstreamKeySummary, UpstreamProvider } from '@/api/types'

type ProviderRow = {
  value: UpstreamProvider
  label: string
  capability: string
  summary?: UpstreamKeySummary
}

defineProps<{
  rows: ProviderRow[]
  loading: boolean
  selectedTeamId: string
}>()

const emit = defineEmits<{
  (event: 'refresh'): void
  (event: 'configure', provider: UpstreamProvider): void
  (event: 'remove', provider: UpstreamProvider): void
}>()

function formatTime(value: string | null | undefined): string {
  if (!value) return '未测试'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

function statusLabel(summary: UpstreamKeySummary | undefined): string {
  if (!summary) return '未配置'
  if (summary.test_status === 'success') return '连接正常'
  if (summary.test_status === 'failed' || summary.test_status === 'failure') return '连接失败'
  return '待测试'
}

function statusClass(summary: UpstreamKeySummary | undefined): string {
  if (!summary) return 'status-muted'
  if (summary.test_status === 'success') return 'status-success'
  if (summary.test_status === 'failed' || summary.test_status === 'failure') return 'status-error'
  return 'status-pending'
}
</script>

<template>
  <section class="configuration-panel" aria-labelledby="configured-providers-heading">
    <div class="panel-heading">
      <div>
        <h2 id="configured-providers-heading">服务商状态</h2>
        <p>仅显示 Key 前缀；删除后由 Gateway 移除对应的 secrets 文件。</p>
      </div>
      <button class="button button-secondary" type="button" :disabled="loading || !selectedTeamId" @click="emit('refresh')">
        {{ loading ? '刷新中...' : '刷新' }}
      </button>
    </div>

    <div v-if="!selectedTeamId" class="empty-state">请先创建或选择一个 Team。</div>
    <div v-else class="provider-list">
      <article v-for="row in rows" :key="row.value" class="provider-row">
        <div class="provider-identity">
          <strong>{{ row.label }}</strong>
          <span>{{ row.capability }}</span>
        </div>
        <div class="provider-detail">
          <span class="detail-label">配置状态</span>
          <span class="status-badge" :class="statusClass(row.summary)">{{ statusLabel(row.summary) }}</span>
        </div>
        <div class="provider-detail">
          <span class="detail-label">Key 前缀</span>
          <code>{{ row.summary?.key_prefix || '未配置' }}</code>
        </div>
        <div class="provider-detail">
          <span class="detail-label">最后测试</span>
          <span>{{ formatTime(row.summary?.last_tested_at) }}</span>
        </div>
        <div class="row-actions">
          <button class="text-action" type="button" @click="emit('configure', row.value)">
            {{ row.summary ? '更新 Key' : '配置 Key' }}
          </button>
          <button v-if="row.summary" class="text-action text-action-danger" type="button" @click="emit('remove', row.value)">删除</button>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.configuration-panel { background: var(--panel-surface); border: 1px solid var(--panel-border); border-radius: var(--panel-radius); overflow: hidden; }
.panel-heading { align-items: center; border-bottom: 1px solid var(--panel-border); display: flex; gap: 1rem; justify-content: space-between; padding: 1rem; }
.panel-heading h2 { color: var(--panel-text-primary); font-size: 1rem; font-weight: 590; margin: 0; }
.panel-heading p { color: var(--panel-text-secondary); font-size: 0.875rem; line-height: 1.5; margin: 0.25rem 0 0; }
.provider-list { display: grid; }
.provider-row { align-items: center; border-bottom: 1px solid var(--panel-border); display: grid; gap: 1rem; grid-template-columns: minmax(10rem, 1.3fr) repeat(3, minmax(7rem, 1fr)) auto; padding: 1rem; }
.provider-row:last-child { border-bottom: 0; }
.provider-identity, .provider-detail { display: grid; gap: 0.25rem; }
.provider-identity strong { color: var(--panel-text-primary); font-weight: 590; }
.provider-identity span, .detail-label { color: var(--panel-text-secondary); font-size: 0.75rem; }
.provider-detail > span:last-child, .provider-detail code { color: var(--panel-text-primary); font-size: 0.875rem; }
.provider-detail code { font-family: var(--panel-font-mono); }
.status-badge { border-radius: var(--panel-radius-pill); font-size: 0.75rem; font-weight: 510; padding: 0.25rem 0.5rem; width: fit-content; }
.status-muted { background: var(--panel-status-muted-bg); color: var(--panel-status-muted-text); }
.status-success { background: var(--panel-status-success-bg); color: var(--panel-status-success-text); }
.status-pending { background: var(--panel-status-pending-bg); color: var(--panel-status-pending-text); }
.status-error { background: var(--panel-status-error-bg); color: var(--panel-status-error-text); }
.row-actions { display: flex; gap: 0.75rem; justify-content: flex-end; }
.button, .text-action { cursor: pointer; font: inherit; transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease, transform 150ms ease; }
.button { border: 1px solid transparent; border-radius: var(--panel-radius-sm); font-size: 0.875rem; font-weight: 510; min-height: 2.75rem; padding: 0.5rem 0.875rem; }
.button:hover:not(:disabled), .text-action:hover:not(:disabled) { transform: translateY(-1px); }
.button:disabled { cursor: wait; opacity: 0.6; }
.button-secondary { background: var(--panel-surface); border-color: var(--panel-border-strong); color: var(--panel-text-primary); }
.button-secondary:hover:not(:disabled) { background: var(--panel-surface-muted); }
.text-action { background: transparent; border: 0; color: var(--panel-action-primary); font-size: 0.875rem; padding: 0.25rem; }
.text-action-danger { color: var(--panel-action-danger); }
.empty-state { color: var(--panel-text-secondary); padding: 2.5rem 1rem; text-align: center; }
@media (max-width: 900px) { .provider-row { grid-template-columns: minmax(9rem, 1fr) repeat(2, minmax(7rem, 1fr)); } .row-actions { grid-column: 1 / -1; justify-content: flex-start; } }
@media (max-width: 640px) { .panel-heading { align-items: stretch; flex-direction: column; } .provider-row { grid-template-columns: repeat(2, minmax(0, 1fr)); } .provider-identity, .row-actions { grid-column: 1 / -1; } }
@media (prefers-reduced-motion: reduce) { .button, .text-action { transition: none; } .button:hover:not(:disabled), .text-action:hover:not(:disabled) { transform: none; } }
</style>
