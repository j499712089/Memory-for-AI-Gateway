<script setup lang="ts">
// 团队列表 + 创建（api-contract.md §3.1）
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useTeamsStore } from '@/stores/teams'
import type { TeamCreateInput } from '@/api/types'
import TeamForm from '@/components/TeamForm.vue'
import DataTable, { type Column } from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'

const router = useRouter()
const store = useTeamsStore()
const { teams, loading, error } = storeToRefs(store)

const showCreate = ref(false)
const submitting = ref(false)

const columns: Column[] = [
  { key: 'name', label: '团队名称' },
  { key: 'slug', label: 'Slug' },
  { key: 'visibility', label: '可见性' },
  { key: 'status', label: '状态', slot: 'status' },
  { key: 'created_at', label: '创建时间' },
]

function fmtTime(v: unknown): string {
  return v ? new Date(v as string).toLocaleString('zh-CN') : '—'
}

async function onSubmit(input: TeamCreateInput) {
  submitting.value = true
  try {
    await store.createTeam(input)
    showCreate.value = false
  } finally {
    submitting.value = false
  }
}

onMounted(() => store.fetchTeams())
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold">团队管理</h1>
        <p class="text-sm text-gray-500">创建团队、查看成员；slug 冲突会返回 409</p>
      </div>
      <button
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500"
        @click="showCreate = !showCreate"
      >
        {{ showCreate ? '收起' : '+ 创建团队' }}
      </button>
    </div>

    <p v-if="error" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ error }}
    </p>

    <div v-if="showCreate" class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <TeamForm :submitting="submitting" :error="error" @submit="onSubmit" @cancel="showCreate = false" />
    </div>

    <DataTable
      :columns="columns"
      :rows="teams"
      :loading="loading"
      empty-text="暂无团队，点击右上角创建"
      @row-click="(r) => router.push(`/teams/${(r as { id: string }).id}`)"
    >
      <template #status="{ row }">
        <StatusBadge :status="(row as { status: string }).status" />
      </template>
      <template #created_at="{ row }">
        {{ fmtTime((row as Record<string, unknown>).created_at) }}
      </template>
    </DataTable>
  </div>
</template>
