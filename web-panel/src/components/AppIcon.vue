<script setup lang="ts">
defineProps<{
  name:
    | 'book-open'
    | 'circle-check'
    | 'circle-x'
    | 'clipboard-check'
    | 'loader'
    | 'plug'
    | 'triangle-alert'
}>()
</script>

<template>
  <svg
    :data-icon="name"
    aria-hidden="true"
    focusable="false"
    fill="none"
    stroke="currentColor"
    stroke-linecap="round"
    stroke-linejoin="round"
    stroke-width="2"
    viewBox="0 0 24 24"
  >
    <g v-if="name === 'book-open'">
      <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2Z" />
      <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7Z" />
    </g>
    <g v-else-if="name === 'circle-check'">
      <circle cx="12" cy="12" r="10" />
      <path d="m9 12 2 2 4-4" />
    </g>
    <g v-else-if="name === 'circle-x'">
      <circle cx="12" cy="12" r="10" />
      <path d="m15 9-6 6" />
      <path d="m9 9 6 6" />
    </g>
    <g v-else-if="name === 'clipboard-check'">
      <rect width="14" height="18" x="5" y="3" rx="2" />
      <path d="M9 3V1h6v2" />
      <path d="m9 13 2 2 4-4" />
    </g>
    <g v-else-if="name === 'loader'">
      <path d="M21 12a9 9 0 1 1-6.2-8.6" />
    </g>
    <g v-else-if="name === 'plug'">
      <path d="M12 22v-5" />
      <path d="M9 8V2" />
      <path d="M15 8V2" />
      <path d="M18 8v5a6 6 0 0 1-12 0V8Z" />
    </g>
    <g v-else>
      <path d="m21.7 18-8-14a2 2 0 0 0-3.4 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3Z" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </g>
  </svg>
</template>
