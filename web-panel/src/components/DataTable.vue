<script setup lang="ts">
// 通用表格：接收列定义与行数据
export interface Column {
  key: string
  label: string
  slot?: string
  width?: string
}

defineProps<{
  columns: Column[]
  rows: unknown[]
  loading?: boolean
  emptyText?: string
}>()

defineEmits<{
  (e: 'row-click', row: unknown): void
}>()
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm">
    <table class="min-w-full divide-y divide-gray-200 text-sm">
      <thead class="bg-gray-50">
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            class="px-4 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-gray-500"
            :style="col.width ? { width: col.width } : undefined"
          >
            {{ col.label }}
          </th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 bg-white">
        <tr
          v-for="(row, i) in rows"
          :key="i"
          class="transition-colors hover:bg-gray-50"
          :class="{ 'cursor-pointer': $attrs.onRowClick }"
          @click="$emit('row-click', row)"
        >
          <td
            v-for="col in columns"
            :key="col.key"
            class="whitespace-nowrap px-4 py-2.5 text-gray-700"
          >
            <slot :name="col.slot || col.key" :row="row">{{ (row as Record<string, unknown>)[col.key] }}</slot>
          </td>
        </tr>
        <tr v-if="loading">
          <td :colspan="columns.length" class="px-4 py-8 text-center text-gray-400">加载中…</td>
        </tr>
        <tr v-else-if="rows.length === 0">
          <td :colspan="columns.length" class="px-4 py-8 text-center text-gray-400">
            {{ emptyText || '暂无数据' }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
