<script setup lang="ts">
defineProps<{
  title: string
  message: string
  loading?: boolean
}>()

const emit = defineEmits<{
  (event: 'cancel'): void
  (event: 'confirm'): void
}>()
</script>

<template>
  <div class="modal-backdrop" role="presentation">
    <section class="dialog" aria-modal="true" role="dialog" :aria-labelledby="`${title}-title`">
      <header class="dialog-header">
        <h2 :id="`${title}-title`">{{ title }}</h2>
        <p>{{ message }}</p>
      </header>
      <footer class="dialog-actions">
        <button class="button button-secondary" type="button" :disabled="loading" @click="emit('cancel')">取消</button>
        <button class="button button-danger" type="button" :disabled="loading" @click="emit('confirm')">
          {{ loading ? '删除中...' : '确认删除' }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop { align-items: center; background: var(--panel-overlay); display: flex; inset: 0; justify-content: center; padding: 1rem; position: fixed; z-index: 30; }
.dialog { background: var(--panel-surface); border: 1px solid var(--panel-border); border-radius: var(--panel-radius); max-width: 28rem; width: 100%; }
.dialog-header, .dialog-actions { padding: 1rem 1.25rem; }
.dialog-header { border-bottom: 1px solid var(--panel-border); }
.dialog h2 { color: var(--panel-text-primary); font-size: 1.125rem; font-weight: 590; margin: 0; }
.dialog p { color: var(--panel-text-secondary); font-size: 0.875rem; line-height: 1.5; margin: 0.25rem 0 0; }
.dialog-actions { display: flex; gap: 0.5rem; justify-content: flex-end; }
.button { border: 1px solid transparent; border-radius: var(--panel-radius-sm); cursor: pointer; font: inherit; min-height: 2.75rem; padding: 0.5rem 0.875rem; transition: background-color 150ms ease, transform 150ms ease; }
.button:hover:not(:disabled) { transform: translateY(-1px); }
.button:disabled { cursor: wait; opacity: 0.6; }
.button-secondary { background: var(--panel-surface); border-color: var(--panel-border-strong); color: var(--panel-text-primary); }
.button-danger { background: var(--panel-action-danger); color: var(--panel-action-danger-text); }
@media (prefers-reduced-motion: reduce) { .button { transition: none; } .button:hover:not(:disabled) { transform: none; } }
</style>
