<script setup lang="ts">
import AppIcon from '@/components/AppIcon.vue'
import type { UpstreamProvider } from '@/api/types'

type ProviderOption = { value: UpstreamProvider; label: string }

defineProps<{
  providers: ProviderOption[]
  selectedProvider: UpstreamProvider
  apiKey: string
  submitting: boolean
  testing: boolean
  canSave: boolean
  canTest: boolean
  feedback: string
  feedbackKind: 'success' | 'error'
  availableModels: string[]
}>()

const emit = defineEmits<{
  (event: 'update:selectedProvider', value: UpstreamProvider): void
  (event: 'update:apiKey', value: string): void
  (event: 'close'): void
  (event: 'save'): void
  (event: 'test'): void
}>()
</script>

<template>
  <div class="modal-backdrop" role="presentation" @click.self="emit('close')">
    <section class="dialog" aria-labelledby="key-dialog-title" aria-modal="true" role="dialog">
      <header class="dialog-header">
        <div>
          <h2 id="key-dialog-title">配置上游 Key</h2>
          <p>测试连接会先保存当前输入的 Key，再调用对应服务商。</p>
        </div>
        <button class="icon-button" aria-label="关闭配置对话框" type="button" @click="emit('close')">
          <AppIcon name="x" />
        </button>
      </header>

      <div class="dialog-body">
        <label>
          <span>服务商</span>
          <select
            :value="selectedProvider"
            @change="emit('update:selectedProvider', ($event.target as HTMLSelectElement).value as UpstreamProvider)"
          >
            <option v-for="provider in providers" :key="provider.value" :value="provider.value">{{ provider.label }}</option>
          </select>
        </label>
        <label>
          <span>API Key</span>
          <input
            :value="apiKey"
            autocomplete="off"
            placeholder="粘贴服务商 API Key"
            type="password"
            @input="emit('update:apiKey', ($event.target as HTMLInputElement).value)"
          />
        </label>
        <p class="field-hint">保存成功后，输入框会立即清空，页面仅保留后端返回的掩码前缀。</p>

        <div v-if="feedback" class="notice" :class="feedbackKind === 'error' ? 'notice-error' : 'notice-success'" aria-live="polite">
          {{ feedback }}
        </div>
        <p v-if="availableModels.length" class="model-list">可用模型：{{ availableModels.join('、') }}</p>
      </div>

      <footer class="dialog-actions">
        <button class="button button-secondary" type="button" :disabled="submitting || testing" @click="emit('close')">取消</button>
        <button class="button button-secondary" type="button" :disabled="submitting || testing || !canTest" @click="emit('test')">
          {{ testing ? '测试中...' : '测试连接' }}
        </button>
        <button class="button button-primary" type="button" :disabled="submitting || testing || !canSave" @click="emit('save')">
          {{ submitting ? '保存中...' : '保存 Key' }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop { align-items: center; background: var(--panel-overlay); display: flex; inset: 0; justify-content: center; padding: 1rem; position: fixed; z-index: 30; }
.dialog { background: var(--panel-surface); border: 1px solid var(--panel-border); border-radius: var(--panel-radius); max-width: 34rem; width: 100%; }
.dialog-header, .dialog-actions { align-items: flex-start; display: flex; gap: 1rem; justify-content: space-between; padding: 1rem 1.25rem; }
.dialog-header { border-bottom: 1px solid var(--panel-border); }
.dialog h2 { color: var(--panel-text-primary); font-size: 1.125rem; font-weight: 590; margin: 0; }
.dialog p { color: var(--panel-text-secondary); font-size: 0.75rem; line-height: 1.5; margin: 0.25rem 0 0; }
.dialog-body { display: grid; gap: 1rem; padding: 1.25rem; }
.dialog-body label { color: var(--panel-text-primary); display: grid; font-size: 0.875rem; font-weight: 510; gap: 0.5rem; }
select, input { background: var(--panel-surface); border: 1px solid var(--panel-border-strong); border-radius: var(--panel-radius-sm); color: var(--panel-text-primary); font: inherit; padding: 0.625rem 0.75rem; width: 100%; }
select:focus-visible, input:focus-visible, button:focus-visible { outline: 2px solid var(--panel-focus); outline-offset: 2px; }
.field-hint, .model-list { color: var(--panel-text-secondary); font-size: 0.75rem; line-height: 1.5; margin: 0; }
.notice { border: 1px solid; border-radius: var(--panel-radius-sm); font-size: 0.875rem; margin: 0; padding: 0.75rem 1rem; }
.notice-success { background: var(--panel-status-success-bg); border-color: var(--panel-status-success-border); color: var(--panel-status-success-text); }
.notice-error { background: var(--panel-status-error-bg); border-color: var(--panel-status-error-border); color: var(--panel-status-error-text); }
.dialog-actions { align-items: center; border-top: 1px solid var(--panel-border); justify-content: flex-end; }
.button, .icon-button { cursor: pointer; font: inherit; transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease, transform 150ms ease; }
.button { min-height: 2.75rem; border: 1px solid transparent; border-radius: var(--panel-radius-sm); font-size: 0.875rem; font-weight: 510; padding: 0.5rem 0.875rem; }
.button:hover:not(:disabled), .icon-button:hover:not(:disabled) { transform: translateY(-1px); }
.button:disabled { cursor: wait; opacity: 0.6; }
.button-primary { background: var(--panel-action-primary); color: var(--panel-action-primary-text); }
.button-primary:hover:not(:disabled) { background: var(--panel-action-primary-hover); }
.button-secondary { background: var(--panel-surface); border-color: var(--panel-border-strong); color: var(--panel-text-primary); }
.button-secondary:hover:not(:disabled) { background: var(--panel-surface-muted); }
.icon-button { align-items: center; background: transparent; border: 0; color: var(--panel-text-secondary); display: inline-flex; height: 2.75rem; justify-content: center; width: 2.75rem; }
.icon-button :deep(svg) { height: 1.25rem; width: 1.25rem; }
@media (max-width: 640px) { .dialog-actions { flex-wrap: wrap; } .dialog-actions .button { flex: 1 1 8rem; } }
@media (prefers-reduced-motion: reduce) { .button, .icon-button { transition: none; } .button:hover:not(:disabled), .icon-button:hover:not(:disabled) { transform: none; } }
</style>
