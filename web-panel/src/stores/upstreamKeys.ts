import { defineStore } from 'pinia'
import { upstreamKeysApi } from '@/api/client'
import type { UpstreamKeySummary, UpstreamProvider } from '@/api/types'

export const useUpstreamKeysStore = defineStore('upstreamKeys', {
  state: () => ({
    keys: [] as UpstreamKeySummary[],
    loading: false,
    error: '' as string,
  }),
  actions: {
    async fetchKeys(teamId: string) {
      this.loading = true
      this.error = ''
      try {
        this.keys = (await upstreamKeysApi.list(teamId)).keys
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error)
      } finally {
        this.loading = false
      }
    },
    async saveKey(input: { team_id: string; provider: UpstreamProvider; api_key: string }) {
      this.error = ''
      try {
        return await upstreamKeysApi.save(input)
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error)
        throw error
      }
    },
    async testKey(input: { team_id: string; provider: UpstreamProvider }) {
      this.error = ''
      try {
        return await upstreamKeysApi.test(input)
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error)
        throw error
      }
    },
    async removeKey(input: { team_id: string; provider: UpstreamProvider }) {
      this.error = ''
      try {
        await upstreamKeysApi.remove(input)
        this.keys = this.keys.filter((key) => key.provider !== input.provider)
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error)
        throw error
      }
    },
  },
})
