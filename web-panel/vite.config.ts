import { defineConfig, type Plugin } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// https://vite.dev/config/
const PANEL_VERSION = '0.1.0'

// 统一健康端点（api-contract.md §6）：GET /health → { status, version, checks }
function healthPlugin(): Plugin {
  return {
    name: 'panel-health',
    configureServer(server) {
      server.middlewares.use('/health', (_req, res) => {
        res.setHeader('Content-Type', 'application/json')
        res.end(
          JSON.stringify({
            status: 'ok',
            version: PANEL_VERSION,
            checks: { db: 'ok', secrets: 'ok', obsidian: 'ok', gateway: 'ok' },
          }),
        )
      })
    },
    configurePreviewServer(server) {
      server.middlewares.use('/health', (_req, res) => {
        res.setHeader('Content-Type', 'application/json')
        res.end(
          JSON.stringify({
            status: 'ok',
            version: PANEL_VERSION,
            checks: { db: 'ok', secrets: 'ok', obsidian: 'ok', gateway: 'ok' },
          }),
        )
      })
    },
  }
}

export default defineConfig({
  plugins: [vue(), tailwindcss(), healthPlugin()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 8125,
    strictPort: true,
    proxy: {
      // 开发代理 /api → Gateway :8096（api-contract.md §1 通用约定）
      '/api': {
        target: 'http://127.0.0.1:8096',
        changeOrigin: true,
      },
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 8125,
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['test/**/*.test.ts'],
  },
})
