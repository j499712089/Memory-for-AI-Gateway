<script setup lang="ts">
import AppIcon from '@/components/AppIcon.vue'

defineProps<{
  teamName: string
  keyName: string
  cardName: string
}>()

const emit = defineEmits<{
  (event: 'finish'): void
}>()
</script>

<template>
  <section class="space-y-4 rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
    <div class="text-center">
      <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-green-100">
        <AppIcon class="h-8 w-8 text-green-600" name="circle-check" />
      </div>
      <h2 class="mt-4 text-lg font-medium">配置完成！</h2>
      <p class="mt-2 text-sm text-gray-500">您已成功完成 Memory Gateway 的基础配置</p>
    </div>
    <div class="space-y-2 rounded bg-gray-50 p-4 text-sm">
      <div class="flex items-center gap-2 font-medium text-gray-700">
        <AppIcon class="h-4 w-4" name="clipboard-check" />
        <span>完成的配置：</span>
      </div>
      <ul class="ml-4 list-disc space-y-1 text-gray-600">
        <li>创建了 Team：{{ teamName }}</li>
        <li>生成了 API Key：{{ keyName }}</li>
        <li>创建了身份卡片：{{ cardName }}</li>
      </ul>
    </div>
    <div class="space-y-2 rounded border border-blue-200 bg-blue-50 p-4 text-sm">
      <div class="flex items-center gap-2 font-medium text-blue-800">
        <AppIcon class="h-4 w-4" name="book-open" />
        <span>下一步推荐：</span>
      </div>
      <ul class="ml-4 list-disc space-y-1 text-blue-700">
        <li>查看 <a href="/api-keys" class="underline">API Keys 管理</a> 了解更多权限配置</li>
        <li>访问 <a href="/identity-cards" class="underline">身份卡片</a> 管理您的 AI 身份</li>
        <li>
          阅读
          <a
            class="underline"
            href="https://github.com/j499712089/Memory-for-AI"
            rel="noopener noreferrer"
            target="_blank"
          >使用文档</a>
          了解高级功能
        </li>
      </ul>
    </div>
    <button
      class="min-h-11 w-full rounded bg-blue-600 px-4 py-2 text-sm text-white transition-colors hover:bg-blue-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
      type="button"
      @click="emit('finish')"
    >
      进入管理面板
    </button>
  </section>
</template>
