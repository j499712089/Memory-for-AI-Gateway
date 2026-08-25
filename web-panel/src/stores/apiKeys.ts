// API Key 状态：列表 / 创建（明文一次性）/ 吊销 / 启用（api-contract.md §3.3）
import { defineStore } from 'pinia'
import { apiKeysApi } from '@/api/client'
import type { ApiKey, ApiKeyCreated, ApiKeyCreateInput } from '@/api/types'

export const useApiKeysStore = defineStore('apiKeys', {
  state: () => ({
    keys: [] as ApiKey[],
    loading: false,
    error: '' as string,
  }),
  actions: {
    async fetchKeys(teamId?: string) {
      this.loading = true
      this.error = ''
      try {
        this.keys = await apiKeysApi.list(teamId ? { team_id: teamId } : undefined)
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async createKey(input: ApiKeyCreateInput): Promise<ApiKeyCreated> {
      this.error = ''
      try {
        const created = await apiKeysApi.create(input)
        // 明文不进入 state；列表里只放掩码形态
        const { key: _plain, ...masked } = created
        this.keys.unshift(masked)
        return created
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
        throw e
      }
    },
    async revokeKey(id: string) {
      await apiKeysApi.revoke(id)
      const k = this.keys.find((x) => x.id === id)
      if (k) {
        k.enabled = false
        k.revoked_at = new Date().toISOString()
      }
    },
    async setEnabled(id: string, enabled: boolean) {
      await apiKeysApi.update(id, { enabled })
      const k = this.keys.find((x) => x.id === id)
      if (k) k.enabled = enabled
    },
  },
})
