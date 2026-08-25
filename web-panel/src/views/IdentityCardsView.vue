<script setup lang="ts">
// 身份卡列表 + 创建/编辑（api-contract.md §3.2，编辑携带 version 乐观锁）
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useTeamsStore } from '@/stores/teams'
import { useIdentityCardsStore } from '@/stores/identityCards'
import type { IdentityCard, IdentityCardCreateInput } from '@/api/types'
import IdentityCardForm from '@/components/IdentityCardForm.vue'
import DataTable, { type Column } from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'

const route = useRoute()
const teamsStore = useTeamsStore()
const cardsStore = useIdentityCardsStore()
const { cards, loading, error } = storeToRefs(cardsStore)

const selectedTeamId = ref(String(route.query.team_id || ''))
const showForm = ref(false)
const editing = ref<IdentityCard | null>(null)
const submitting = ref(false)

const columns: Column[] = [
  { key: 'name', label: '名称' },
  { key: 'role', label: '角色' },
  { key: 'version', label: '版本' },
  { key: 'visibility', label: '可见性' },
  { key: 'status', label: '状态', slot: 'status' },
]

function openCreate() {
  editing.value = null
  showForm.value = true
}

function openEdit(card: IdentityCard) {
  editing.value = card
  showForm.value = true
}

async function onSubmit(input: IdentityCardCreateInput & { version?: number }) {
  if (!selectedTeamId.value) return
  submitting.value = true
  try {
    if (editing.value && input.version !== undefined) {
      await cardsStore.updateCard(editing.value.id, { ...input, version: input.version })
    } else {
      await cardsStore.createCard(selectedTeamId.value, input)
    }
    showForm.value = false
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  if (teamsStore.teams.length === 0) await teamsStore.fetchTeams()
  if (selectedTeamId.value) await cardsStore.fetchCards(selectedTeamId.value)
})

function onTeamChange() {
  if (selectedTeamId.value) cardsStore.fetchCards(selectedTeamId.value)
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold">身份卡管理</h1>
        <p class="text-sm text-gray-500">L3 身份卡；编辑携带 version，冲突返回 409 生成新版本</p>
      </div>
      <button
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
        :disabled="!selectedTeamId"
        @click="openCreate"
      >
        + 创建身份卡
      </button>
    </div>

    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">选择团队</label>
      <select
        v-model="selectedTeamId"
        class="w-full max-w-md rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        @change="onTeamChange"
      >
        <option value="" disabled>请选择团队</option>
        <option v-for="t in teamsStore.teams" :key="t.id" :value="t.id">{{ t.name }}</option>
      </select>
    </div>

    <p v-if="error" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ error }}
    </p>

    <div v-if="showForm" class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <h2 class="mb-3 text-base font-medium">{{ editing ? `编辑：${editing.name} (v${editing.version})` : '创建身份卡' }}</h2>
      <IdentityCardForm
        :initial="editing"
        :submitting="submitting"
        :error="error"
        @submit="onSubmit"
        @cancel="showForm = false"
      />
    </div>

    <DataTable :columns="columns" :rows="cards" :loading="loading" empty-text="暂无身份卡">
      <template #status="{ row }">
        <StatusBadge :status="(row as { status: string }).status" />
      </template>
      <template #name="{ row }">
        <button class="text-blue-600 hover:underline" @click="openEdit(row as IdentityCard)">
          {{ (row as { name: string }).name }}
        </button>
      </template>
    </DataTable>
  </div>
</template>
