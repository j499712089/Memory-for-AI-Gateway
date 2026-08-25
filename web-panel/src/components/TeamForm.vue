<script setup lang="ts">
// 团队创建/编辑表单（api-contract.md §3.1）
import { reactive } from 'vue'
import type { Team, TeamCreateInput } from '@/api/types'

const props = defineProps<{
  initial?: Team | null
  submitting?: boolean
  error?: string
}>()

const emit = defineEmits<{
  (e: 'submit', input: TeamCreateInput): void
  (e: 'cancel'): void
}>()

const form = reactive<TeamCreateInput>({
  name: props.initial?.name ?? '',
  slug: props.initial?.slug ?? '',
  description: props.initial?.description ?? '',
  visibility: props.initial?.visibility ?? 'private',
})

function onSlugBlur() {
  if (!form.slug && form.name) {
    form.slug = form.name
      .trim()
      .toLowerCase()
      // 仅保留 ASCII 字母/数字，其余替换为连字符
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 40)
  }
}

function onSubmit() {
  emit('submit', { ...form })
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="onSubmit">
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">团队名称 *</label>
      <input
        v-model="form.name"
        required
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="如 MHM-SK 圣殿骑士团"
      />
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">Slug（唯一标识）*</label>
      <input
        v-model="form.slug"
        required
        pattern="[a-z0-9-]+"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        placeholder="如 mhm-sk（小写字母/数字/连字符，冲突会 409）"
        @blur="onSlugBlur"
      />
      <p v-if="error" class="mt-1 text-sm text-red-600">{{ error }}</p>
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">描述</label>
      <textarea
        v-model="form.description"
        rows="2"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      />
    </div>
    <div>
      <label class="mb-1 block text-sm font-medium text-gray-700">可见性</label>
      <select
        v-model="form.visibility"
        class="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      >
        <option value="private">private</option>
        <option value="team">team</option>
        <option value="org">org</option>
      </select>
    </div>
    <div class="flex justify-end gap-2 pt-2">
      <button
        type="button"
        class="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
        @click="emit('cancel')"
      >
        取消
      </button>
      <button
        type="submit"
        :disabled="submitting"
        class="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
      >
        {{ submitting ? '提交中…' : '保存' }}
      </button>
    </div>
  </form>
</template>
