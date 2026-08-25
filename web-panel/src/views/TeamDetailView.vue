<script setup lang="ts">
// 团队详情：成员管理 + 身份卡入口（api-contract.md §3.1 / §3.2）
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useTeamsStore } from '@/stores/teams'
import { useIdentityCardsStore } from '@/stores/identityCards'
import { useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const teamId = computed(() => String(route.params.id))

const teamsStore = useTeamsStore()
const cardsStore = useIdentityCardsStore()
const { members, error } = storeToRefs(teamsStore)
const { cards, loading: cardsLoading } = storeToRefs(cardsStore)

const team = computed(() => teamsStore.teams.find((t) => t.id === teamId.value))

const newMember = ref({ user_id: '', name: '', role: 'member' })

async function addMember() {
  if (!newMember.value.user_id || !newMember.value.name) return
  await teamsStore.addMember(teamId.value, newMember.value.user_id, newMember.value.name)
  newMember.value = { user_id: '', name: '', role: 'member' }
}

onMounted(async () => {
  if (teamsStore.teams.length === 0) await teamsStore.fetchTeams()
  await teamsStore.fetchMembers(teamId.value)
  await cardsStore.fetchCards(teamId.value)
})
</script>

<template>
  <div class="space-y-6">
    <div>
      <button class="mb-2 text-sm text-blue-600 hover:underline" @click="router.push('/teams')">
        ← 返回团队列表
      </button>
      <h1 class="text-xl font-semibold">{{ team?.name || '团队详情' }}</h1>
      <p class="text-sm text-gray-500">
        slug: {{ team?.slug }} · 可见性: {{ team?.visibility }} · 状态: {{ team?.status }}
      </p>
    </div>

    <p v-if="error" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ error }}
    </p>

    <section class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <h2 class="mb-3 text-base font-medium">成员管理</h2>
      <form class="mb-3 flex flex-wrap items-center gap-2" @submit.prevent="addMember">
        <input
          v-model="newMember.user_id"
          required
          class="w-64 rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          placeholder="user_id (UUID)"
        />
        <input
          v-model="newMember.name"
          required
          class="w-40 rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          placeholder="成员名称"
        />
        <button type="submit" class="rounded bg-blue-600 px-3 py-2 text-sm text-white hover:bg-blue-500">
          添加成员
        </button>
      </form>
      <table class="w-full text-sm">
        <thead class="border-b text-left text-xs uppercase text-gray-500">
          <tr>
            <th class="py-2">成员</th>
            <th>角色</th>
            <th>加入时间</th>
            <th></th>
          </tr>
        </thead>
        <tbody class="divide-y">
          <tr v-for="m in members" :key="m.user_id">
            <td class="py-2">{{ m.name }}</td>
            <td>{{ m.role }}</td>
            <td>{{ new Date(m.joined_at).toLocaleString('zh-CN') }}</td>
            <td class="text-right">
              <button
                class="text-sm text-red-600 hover:underline"
                @click="teamsStore.removeMember(teamId, m.user_id)"
              >
                移除
              </button>
            </td>
          </tr>
          <tr v-if="members.length === 0">
            <td colspan="4" class="py-4 text-center text-gray-400">暂无成员</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-base font-medium">身份卡（L3）</h2>
        <button
          class="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-500"
          @click="router.push(`/identity-cards?team_id=${teamId}`)"
        >
          管理身份卡
        </button>
      </div>
      <p v-if="cardsLoading" class="text-sm text-gray-400">加载中…</p>
      <table v-else class="w-full text-sm">
        <thead class="border-b text-left text-xs uppercase text-gray-500">
          <tr>
            <th class="py-2">名称</th>
            <th>角色</th>
            <th>版本</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          <tr v-for="c in cards" :key="c.id">
            <td class="py-2">{{ c.name }}</td>
            <td>{{ c.role }}</td>
            <td>v{{ c.version }}</td>
          </tr>
          <tr v-if="cards.length === 0">
            <td colspan="3" class="py-4 text-center text-gray-400">暂无身份卡</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>
