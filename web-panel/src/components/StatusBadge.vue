<script setup lang="ts">
// 通用状态徽章：按状态/终态映射配色
import { computed } from 'vue'

const props = defineProps<{
  status?: string | null
}>()

const config = computed<{ text: string; cls: string }>(() => {
  const s = props.status?.toLowerCase() ?? ''
  switch (s) {
    case 'active':
    case 'ok':
    case 'complete':
      return { text: s === 'active' ? '启用' : s === 'ok' ? '正常' : '完成', cls: 'bg-green-100 text-green-700' }
    case 'archived':
    case 'disabled':
      return { text: s === 'archived' ? '已归档' : '已禁用', cls: 'bg-gray-100 text-gray-500' }
    case 'partial':
      return { text: '部分', cls: 'bg-amber-100 text-amber-700' }
    case 'error':
    case 'failed':
      return { text: s === 'error' ? '错误' : '失败', cls: 'bg-red-100 text-red-700' }
    case 'cancelled':
      return { text: '已取消', cls: 'bg-gray-100 text-gray-500' }
    case 'pending':
    case 'processing':
      return { text: '处理中', cls: 'bg-blue-100 text-blue-700' }
    case 'degraded':
      return { text: '降级', cls: 'bg-amber-100 text-amber-700' }
    default:
      return { text: props.status || '—', cls: 'bg-gray-100 text-gray-600' }
  }
})
</script>

<template>
  <span
    class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
    :class="config.cls"
  >
    {{ config.text }}
  </span>
</template>
