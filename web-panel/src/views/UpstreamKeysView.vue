<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useTeamsStore } from '@/stores/teams'
import { useUpstreamKeysStore } from '@/stores/upstreamKeys'
import UpstreamKeyDialog from '@/components/UpstreamKeyDialog.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import UpstreamKeyList from '@/components/UpstreamKeyList.vue'
import type { UpstreamProvider } from '@/api/types'

type ProviderDefinition = {
  value: UpstreamProvider
  label: string
  capability: string
}

const providers: ProviderDefinition[] = [
  { value: 'anthropic', label: 'Anthropic', capability: 'Claude Messages API' },
  { value: 'openai', label: 'OpenAI', capability: 'GPT and compatible APIs' },
  { value: 'codex', label: 'Codex', capability: 'Responses API' },
]

const teamsStore = useTeamsStore()
const keysStore = useUpstreamKeysStore()
const { activeTeams } = storeToRefs(teamsStore)
const { keys, loading, error } = storeToRefs(keysStore)

const selectedTeamId = ref('')
const dialogOpen = ref(false)
const deleteTarget = ref<UpstreamProvider | null>(null)
const selectedProvider = ref<UpstreamProvider>('anthropic')
const apiKey = ref('')
const submitting = ref(false)
const testing = ref(false)
const deleting = ref(false)
const feedback = ref('')
const feedbackKind = ref<'success' | 'error'>('success')
const availableModels = ref<string[]>([])

const configuredKeys = computed(() => new Map(keys.value.map((key) => [key.provider, key])))
const rows = computed(() => providers.map((provider) => ({
  ...provider,
  summary: configuredKeys.value.get(provider.value),
})))
const canSave = computed(() => Boolean(selectedTeamId.value && apiKey.value.trim()))
const canTest = computed(() => Boolean(
  selectedTeamId.value && (apiKey.value.trim() || configuredKeys.value.has(selectedProvider.value)),
))
const selectedProviderLabel = computed(() => providerLabel(selectedProvider.value))

function providerLabel(provider: UpstreamProvider): string {
  return providers.find((item) => item.value === provider)?.label || provider
}

function setFeedback(message: string, kind: 'success' | 'error') {
  feedback.value = message
  feedbackKind.value = kind
}

function resetDialog() {
  apiKey.value = ''
  availableModels.value = []
}

function openDialog(provider: UpstreamProvider = 'anthropic') {
  selectedProvider.value = provider
  feedback.value = ''
  resetDialog()
  dialogOpen.value = true
}

function closeDialog() {
  if (submitting.value || testing.value) return
  dialogOpen.value = false
  resetDialog()
}

async function loadKeys() {
  if (selectedTeamId.value) await keysStore.fetchKeys(selectedTeamId.value)
}

async function saveKey(): Promise<boolean> {
  if (!canSave.value) {
    setFeedback('请输入 API Key 后再保存。', 'error')
    return false
  }

  submitting.value = true
  try {
    await keysStore.saveKey({
      team_id: selectedTeamId.value,
      provider: selectedProvider.value,
      api_key: apiKey.value.trim(),
    })
    apiKey.value = ''
    await loadKeys()
    setFeedback(`${selectedProviderLabel.value} API Key 已保存。`, 'success')
    return true
  } catch (saveError) {
    setFeedback(`保存失败：${saveError instanceof Error ? saveError.message : String(saveError)}`, 'error')
    return false
  } finally {
    submitting.value = false
  }
}

async function testConnection() {
  if (!canTest.value) {
    setFeedback('请先输入 API Key，或选择已配置的服务商。', 'error')
    return
  }

  if (apiKey.value.trim() && !(await saveKey())) return

  testing.value = true
  try {
    const result = await keysStore.testKey({
      team_id: selectedTeamId.value,
      provider: selectedProvider.value,
    })
    if (!result.success) throw new Error(result.error || '服务商未通过连接测试')

    availableModels.value = result.available_models || result.models || []
    const modelHint = availableModels.value.length ? ` 可用模型：${availableModels.value.join('、')}` : ''
    setFeedback(`连接成功。${modelHint}`, 'success')
    await loadKeys()
  } catch (testError) {
    setFeedback(`连接失败：${testError instanceof Error ? testError.message : String(testError)}`, 'error')
  } finally {
    testing.value = false
  }
}

async function confirmDelete() {
  if (!deleteTarget.value || !selectedTeamId.value) return

  deleting.value = true
  try {
    const provider = deleteTarget.value
    await keysStore.removeKey({ team_id: selectedTeamId.value, provider })
    deleteTarget.value = null
    setFeedback(`${providerLabel(provider)} API Key 已删除。`, 'success')
  } catch (deleteError) {
    setFeedback(`删除失败：${deleteError instanceof Error ? deleteError.message : String(deleteError)}`, 'error')
  } finally {
    deleting.value = false
  }
}

watch(selectedTeamId, () => {
  feedback.value = ''
  void loadKeys()
})

onMounted(async () => {
  if (!activeTeams.value.length) await teamsStore.fetchTeams()
  selectedTeamId.value = activeTeams.value[0]?.id || ''
})
</script>

<template>
  <div class="upstream-keys-view">
    <header class="page-header">
      <div>
        <h1>上游通道配置</h1>
        <p>为当前 Team 管理服务商凭据。完整 API Key 不会在此页面再次显示。</p>
      </div>
      <button class="button button-primary" type="button" @click="openDialog()">添加上游 Key</button>
    </header>

    <section class="team-selector" aria-labelledby="team-selector-label">
      <div>
        <h2 id="team-selector-label">当前 Team</h2>
        <p>切换 Team 后将加载对应的上游通道凭据状态。</p>
      </div>
      <select v-model="selectedTeamId" aria-label="选择 Team">
        <option value="" disabled>选择一个 Team</option>
        <option v-for="team in activeTeams" :key="team.id" :value="team.id">{{ team.name }}</option>
      </select>
    </section>

    <p v-if="error" class="notice notice-error" role="alert">{{ error }}</p>
    <p v-else-if="feedback" class="notice" :class="feedbackKind === 'error' ? 'notice-error' : 'notice-success'" aria-live="polite">
      {{ feedback }}
    </p>

    <UpstreamKeyList
      :loading="loading"
      :rows="rows"
      :selected-team-id="selectedTeamId"
      @configure="openDialog"
      @refresh="loadKeys"
      @remove="deleteTarget = $event"
    />

    <UpstreamKeyDialog
      v-if="dialogOpen"
      :api-key="apiKey"
      :available-models="availableModels"
      :can-save="canSave"
      :can-test="canTest"
      :feedback="feedback"
      :feedback-kind="feedbackKind"
      :providers="providers"
      :selected-provider="selectedProvider"
      :submitting="submitting"
      :testing="testing"
      @close="closeDialog"
      @save="saveKey"
      @test="testConnection"
      @update:api-key="apiKey = $event"
      @update:selected-provider="selectedProvider = $event"
    />

    <ConfirmDialog
      v-if="deleteTarget"
      :loading="deleting"
      :message="'Gateway 将删除该 Team 对应的 secrets 文件，此操作无法撤销。'"
      :title="`删除 ${providerLabel(deleteTarget)} Key？`"
      @cancel="deleteTarget = null"
      @confirm="confirmDelete"
    />
  </div>
</template>

<style scoped>
.upstream-keys-view { display: grid; gap: 1.5rem; color: var(--panel-text-primary); }
.page-header, .team-selector { display: flex; gap: 1rem; }
.page-header, .team-selector { align-items: center; justify-content: space-between; }
.page-header h1, .team-selector h2, .panel-heading h2 { margin: 0; color: var(--panel-text-primary); font-weight: 590; letter-spacing: 0; }
.page-header h1 { font-size: 1.5rem; }
.page-header p, .team-selector p, .panel-heading p { margin: 0.25rem 0 0; color: var(--panel-text-secondary); font-size: 0.875rem; line-height: 1.5; }
.team-selector, .configuration-panel { border: 1px solid var(--panel-border); background: var(--panel-surface); border-radius: var(--panel-radius); }
.team-selector { padding: 1rem; }
.team-selector h2, .panel-heading h2 { font-size: 1rem; }
select, input { width: 100%; border: 1px solid var(--panel-border-strong); border-radius: var(--panel-radius-sm); background: var(--panel-surface); color: var(--panel-text-primary); font: inherit; padding: 0.625rem 0.75rem; }
select { max-width: 20rem; }
select:focus-visible, input:focus-visible, button:focus-visible { outline: 2px solid var(--panel-focus); outline-offset: 2px; }
.button, .icon-button { cursor: pointer; font: inherit; transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease, transform 150ms ease; }
.button { min-height: 2.75rem; border: 1px solid transparent; border-radius: var(--panel-radius-sm); font-size: 0.875rem; font-weight: 510; padding: 0.5rem 0.875rem; }
.button:hover:not(:disabled), .icon-button:hover:not(:disabled) { transform: translateY(-1px); }
.button:disabled { cursor: wait; opacity: 0.6; }
.button-primary { background: var(--panel-action-primary); color: var(--panel-action-primary-text); }
.button-primary:hover:not(:disabled) { background: var(--panel-action-primary-hover); }
.button-secondary { border-color: var(--panel-border-strong); background: var(--panel-surface); color: var(--panel-text-primary); }
.button-secondary:hover:not(:disabled) { background: var(--panel-surface-muted); }
.button-danger { background: var(--panel-action-danger); color: var(--panel-action-danger-text); }
.button-danger:hover:not(:disabled) { background: var(--panel-action-danger-hover); }
.text-action { border: 0; background: transparent; color: var(--panel-action-primary); font-size: 0.875rem; padding: 0.25rem; }
.text-action-danger { color: var(--panel-action-danger); }
.notice { border: 1px solid; border-radius: var(--panel-radius-sm); font-size: 0.875rem; margin: 0; padding: 0.75rem 1rem; }
.notice-success { border-color: var(--panel-status-success-border); background: var(--panel-status-success-bg); color: var(--panel-status-success-text); }
.notice-error { border-color: var(--panel-status-error-border); background: var(--panel-status-error-bg); color: var(--panel-status-error-text); }
@media (max-width: 900px) { .provider-row { grid-template-columns: minmax(9rem, 1fr) repeat(2, minmax(7rem, 1fr)); } .row-actions { grid-column: 1 / -1; justify-content: flex-start; } }
@media (max-width: 640px) { .page-header, .team-selector { align-items: stretch; flex-direction: column; } .page-header .button, .team-selector select { max-width: none; width: 100%; } }
@media (prefers-reduced-motion: reduce) { .button, .icon-button { transition: none; } .button:hover:not(:disabled), .icon-button:hover:not(:disabled) { transform: none; } }
</style>
