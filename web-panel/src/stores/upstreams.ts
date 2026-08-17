// 上游通道状态：列表 / models.json 导入（api-contract.md §3.4）
import { defineStore } from 'pinia'
import { upstreamsApi } from '@/api/client'
import type { ImportModelsInput, ImportModelsResult, UpstreamChannel } from '@/api/types'

export const useUpstreamsStore = defineStore('upstreams', {
  state: () => ({
    channels: [] as UpstreamChannel[],
    loading: false,
    error: '' as string,
    lastImport: null as ImportModelsResult | null,
  }),
  actions: {
    async fetchChannels() {
      this.loading = true
      this.error = ''
      try {
        this.channels = await upstreamsApi.list()
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async importModels(input: ImportModelsInput): Promise<ImportModelsResult> {
      this.error = ''
      try {
        const result = await upstreamsApi.importModels(input)
        this.lastImport = result
        this.channels = result.channels
        return result
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
        throw e
      }
    },
    async disable(id: string) {
      const ch = await upstreamsApi.disable(id)
      const idx = this.channels.findIndex((c) => c.id === id)
      if (idx >= 0) this.channels[idx] = ch
    },
  },
})
