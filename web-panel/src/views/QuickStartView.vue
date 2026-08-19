<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useTeamsStore } from '@/stores/teams'
import { useApiKeysStore } from '@/stores/apiKeys'
import { useIdentityCardsStore } from '@/stores/identityCards'
import type { TeamCreateInput, ApiKeyCreateInput, IdentityCardCreateInput } from '@/api/types'

const router = useRouter()
const teamsStore = useTeamsStore()
const keysStore = useApiKeysStore()
const cardsStore = useIdentityCardsStore()

const currentStep = ref(1)
const teamName = ref('')
const teamSlug = ref('')
const teamDescription = ref('')
const keyName = ref('')
const keyScopes = ref(['memories:read', 'memories:write'])
const cardName = ref('')
const cardPersona = ref('')
const agentId = ref('')
const testResult = ref('')
const submitting = ref(false)
const createdTeamId = ref('')
const createdKeyValue = ref('')
const createdCardId = ref('')
const apiBase = import.meta.env.VITE_API_BASE_URL || 'http://127.0.0.1:8096'

const canProceed = computed(() => {
  if (currentStep.value === 1) return teamName.value && teamSlug.value
  if (currentStep.value === 2) return keyName.value
  if (currentStep.value === 3) return cardName.value
  return true
})

async function handleStep1() {
  submitting.value = true
  try {
    const input: TeamCreateInput = {
      name: teamName.value,
      slug: teamSlug.value,
      description: teamDescription.value || undefined,
    }
    const team = await teamsStore.createTeam(input)
    createdTeamId.value = team.id
    currentStep.value = 2
  } finally {
    submitting.value = false
  }
}

async function handleStep2() {
  submitting.value = true
  try {
    const input: ApiKeyCreateInput = {
      name: keyName.value,
      team_id: createdTeamId.value,
      owner_id: createdTeamId.value,
      scopes: keyScopes.value,
    }
    const result = await keysStore.createKey(input)
    createdKeyValue.value = result.key
    currentStep.value = 3
  } finally {
    submitting.value = false
  }
}

async function handleStep3() {
  submitting.value = true
  try {
    const input: IdentityCardCreateInput = {
      name: cardName.value,
      role: 'assistant',
      responsibilities: cardPersona.value || 'AI助手职责',
      boundaries: '遵守用户指令',
      agent_id: agentId.value || undefined,
    }
    const card = await cardsStore.createCard(createdTeamId.value, input)
    createdCardId.value = card.id
    currentStep.value = 4
  } finally {
    submitting.value = false
  }
}

async function testConnection() {
  testResult.value = '测试中...'
  try {
    const response = await fetch(`${apiBase}/api/health`, {
      headers: { 'Authorization': `Bearer ${createdKeyValue.value}` }
    })
    if (response.ok) {
      testResult.value = '✅ 连接成功！API Key 工作正常'
    } else {
      testResult.value = `❌ 连接失败：${response.status} ${response.statusText}`
    }
  } catch (e) {
    testResult.value = `❌ 连接失败：${e instanceof Error ? e.message : String(e)}`
  }
}

function goToDashboard() {
  router.push('/teams')
}

onMounted(async () => {
  await teamsStore.fetchTeams()
})
</script>

<template>
  <div class="mx-auto max-w-3xl space-y-6">
    <div>
      <h1 class="text-xl font-semibold">快速开始向导</h1>
      <p class="text-sm text-gray-500">5 分钟完成 Memory Gateway 基础配置</p>
    </div>

    <div class="flex items-center justify-between rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <div
        v-for="step in 5"
        :key="step"
        class="flex items-center"
        :class="{ 'opacity-40': step > currentStep }"
      >
        <div
          class="flex h-8 w-8 items-center justify-center rounded-full text-sm font-medium"
          :class="step <= currentStep ? 'bg-blue-600 text-white' : 'border border-gray-300 text-gray-400'"
        >
          {{ step }}
        </div>
        <div v-if="step < 5" class="mx-2 h-px w-12 bg-gray-300"></div>
      </div>
    </div>

    <div v-if="currentStep === 1" class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div>
        <h2 class="text-lg font-medium">步骤 1：创建第一个 Team</h2>
        <p class="text-sm text-gray-500">Team 用于组织身份卡片和 API Key 权限</p>
      </div>
      <div class="space-y-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">Team 名称*</label>
          <input
            v-model="teamName"
            type="text"
            placeholder="例如：我的 AI 助手团队"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Slug*</label>
          <input
            v-model="teamSlug"
            type="text"
            placeholder="例如：my-team"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none"
          />
          <p class="mt-1 text-xs text-gray-500">仅限小写字母、数字、连字符</p>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">描述（可选）</label>
          <textarea
            v-model="teamDescription"
            rows="2"
            placeholder="简要描述这个 Team 的用途"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          ></textarea>
        </div>
      </div>
      <div class="flex justify-end">
        <button
          :disabled="!canProceed || submitting"
          class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="handleStep1"
        >
          {{ submitting ? '创建中...' : '下一步' }}
        </button>
      </div>
    </div>

    <div v-if="currentStep === 2" class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div>
        <h2 class="text-lg font-medium">步骤 2：生成第一个 API Key</h2>
        <p class="text-sm text-gray-500">API Key 用于客户端调用 Gateway 服务</p>
      </div>
      <div class="space-y-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">Key 名称*</label>
          <input
            v-model="keyName"
            type="text"
            placeholder="例如：Production Key"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">权限范围</label>
          <div class="mt-2 space-y-2">
            <label class="flex items-center">
              <input v-model="keyScopes" type="checkbox" value="memories:read" class="mr-2" />
              <span class="text-sm">memories:read（读取记忆）</span>
            </label>
            <label class="flex items-center">
              <input v-model="keyScopes" type="checkbox" value="memories:write" class="mr-2" />
              <span class="text-sm">memories:write（写入记忆）</span>
            </label>
          </div>
        </div>
      </div>
      <div class="flex justify-between">
        <button
          class="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
          @click="currentStep = 1"
        >
          上一步
        </button>
        <button
          :disabled="!canProceed || submitting"
          class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="handleStep2"
        >
          {{ submitting ? '生成中...' : '下一步' }}
        </button>
      </div>
    </div>

    <div v-if="currentStep === 3" class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div>
        <h2 class="text-lg font-medium">步骤 3：创建身份卡片并绑定 Agent</h2>
        <p class="text-sm text-gray-500">身份卡片定义 AI 助手的身份和记忆存储位置</p>
      </div>
      <div v-if="createdKeyValue" class="rounded bg-yellow-50 p-3 text-sm">
        <div class="font-medium text-yellow-800">⚠️ 请保存您的 API Key（仅展示一次）</div>
        <div class="mt-2 overflow-x-auto rounded bg-white p-2 font-mono text-xs">{{ createdKeyValue }}</div>
      </div>
      <div class="space-y-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">身份卡片名称*</label>
          <input
            v-model="cardName"
            type="text"
            placeholder="例如：我的 AI 助手"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Persona（可选）</label>
          <textarea
            v-model="cardPersona"
            rows="2"
            placeholder="例如：你是一个专业的技术顾问..."
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          ></textarea>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Agent ID（可选）</label>
          <input
            v-model="agentId"
            type="text"
            placeholder="例如：agent-123"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none"
          />
          <p class="mt-1 text-xs text-gray-500">用于绑定客户端 Agent</p>
        </div>
      </div>
      <div class="flex justify-between">
        <button
          class="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
          @click="currentStep = 2"
        >
          上一步
        </button>
        <button
          :disabled="!canProceed || submitting"
          class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
          @click="handleStep3"
        >
          {{ submitting ? '创建中...' : '下一步' }}
        </button>
      </div>
    </div>

    <div v-if="currentStep === 4" class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div>
        <h2 class="text-lg font-medium">步骤 4：测试 API 调用</h2>
        <p class="text-sm text-gray-500">验证 API Key 是否正常工作</p>
      </div>
      <div class="rounded bg-gray-50 p-4">
        <div class="text-sm font-medium text-gray-700">使用您的 API Key 测试连接：</div>
        <pre class="mt-2 overflow-x-auto rounded bg-white p-3 font-mono text-xs">curl -H "Authorization: Bearer {{ createdKeyValue }}" \
  {{ apiBase }}/api/health</pre>
      </div>
      <button
        class="w-full rounded bg-green-600 px-4 py-2 text-sm text-white hover:bg-green-500"
        @click="testConnection"
      >
        🔌 测试连接
      </button>
      <div v-if="testResult" class="rounded border px-3 py-2 text-sm" :class="testResult.startsWith('✅') ? 'border-green-200 bg-green-50 text-green-700' : 'border-red-200 bg-red-50 text-red-700'">
        {{ testResult }}
      </div>
      <div class="flex justify-between">
        <button
          class="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
          @click="currentStep = 3"
        >
          上一步
        </button>
        <button
          class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500"
          @click="currentStep = 5"
        >
          下一步
        </button>
      </div>
    </div>

    <div v-if="currentStep === 5" class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div class="text-center">
        <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-green-100">
          <svg class="h-8 w-8 text-green-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path>
          </svg>
        </div>
        <h2 class="mt-4 text-lg font-medium">配置完成！</h2>
        <p class="mt-2 text-sm text-gray-500">您已成功完成 Memory Gateway 的基础配置</p>
      </div>
      <div class="space-y-2 rounded bg-gray-50 p-4 text-sm">
        <div class="font-medium text-gray-700">✅ 完成的配置：</div>
        <ul class="ml-4 list-disc space-y-1 text-gray-600">
          <li>创建了 Team：{{ teamName }}</li>
          <li>生成了 API Key：{{ keyName }}</li>
          <li>创建了身份卡片：{{ cardName }}</li>
        </ul>
      </div>
      <div class="space-y-2 rounded border border-blue-200 bg-blue-50 p-4 text-sm">
        <div class="font-medium text-blue-800">📚 下一步推荐：</div>
        <ul class="ml-4 list-disc space-y-1 text-blue-700">
          <li>查看 <a href="/api-keys" class="underline">API Keys 管理</a> 了解更多权限配置</li>
          <li>访问 <a href="/identity-cards" class="underline">身份卡片</a> 管理您的 AI 身份</li>
          <li>阅读 <a href="https://github.com/j499712089/Memory-for-AI" target="_blank" class="underline">使用文档</a> 了解高级功能</li>
        </ul>
      </div>
      <button
        class="w-full rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500"
        @click="goToDashboard"
      >
        进入管理面板
      </button>
    </div>
  </div>
</template>
