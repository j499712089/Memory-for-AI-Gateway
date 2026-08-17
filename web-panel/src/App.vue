<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getGatewayKey, setGatewayKey } from '@/api/client'

const route = useRoute()
const keyInput = ref(getGatewayKey())

watch(
  () => route.meta.title,
  (t) => {
    document.title = t ? `${t} · Memory Gateway` : 'Memory Gateway 管理面板'
  },
  { immediate: true },
)

function saveKey() {
  setGatewayKey(keyInput.value.trim())
}
</script>

<template>
  <div class="min-h-screen bg-gray-50">
    <header class="bg-gray-900 text-white">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-4 py-3">
        <div class="flex items-center gap-6">
          <span class="text-lg font-semibold tracking-wide">Memory Gateway</span>
          <nav class="flex items-center gap-1 text-sm">
            <RouterLink to="/teams" class="rounded px-3 py-1.5 hover:bg-gray-700">团队</RouterLink>
            <RouterLink to="/identity-cards" class="rounded px-3 py-1.5 hover:bg-gray-700">身份卡</RouterLink>
            <RouterLink to="/api-keys" class="rounded px-3 py-1.5 hover:bg-gray-700">API Key</RouterLink>
            <RouterLink to="/upstreams" class="rounded px-3 py-1.5 hover:bg-gray-700">上游通道</RouterLink>
            <RouterLink to="/health" class="rounded px-3 py-1.5 hover:bg-gray-700">健康看板</RouterLink>
          </nav>
        </div>
        <form class="flex items-center gap-2" @submit.prevent="saveKey">
          <input
            v-model="keyInput"
            type="password"
            placeholder="Gateway API Key"
            class="w-56 rounded border border-gray-600 bg-gray-800 px-2 py-1 text-xs text-gray-100 placeholder-gray-400 focus:outline-none focus:ring-1 focus:ring-blue-400"
          />
          <button type="submit" class="rounded bg-blue-600 px-3 py-1 text-xs hover:bg-blue-500">
            保存
          </button>
        </form>
      </div>
    </header>
    <main class="mx-auto max-w-7xl px-4 py-6">
      <RouterView />
    </main>
  </div>
</template>
