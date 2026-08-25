// 身份卡状态（L3）：列表 / 创建 / 更新（版本乐观锁，api-contract.md §3.2）
import { defineStore } from 'pinia'
import { identityCardsApi } from '@/api/client'
import type { IdentityCard, IdentityCardCreateInput, IdentityCardUpdateInput } from '@/api/types'
import { ApiError } from '@/api/client'

export const useIdentityCardsStore = defineStore('identityCards', {
  state: () => ({
    cards: [] as IdentityCard[],
    loading: false,
    error: '' as string,
  }),
  getters: {
    byId: (state) => (id: string) => state.cards.find((c) => c.id === id),
  },
  actions: {
    async fetchCards(teamId: string) {
      this.loading = true
      this.error = ''
      try {
        this.cards = await identityCardsApi.list(teamId)
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async createCard(teamId: string, input: IdentityCardCreateInput): Promise<IdentityCard> {
      this.error = ''
      try {
        const card = await identityCardsApi.create(teamId, input)
        this.cards.unshift(card)
        return card
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
        throw e
      }
    },
    async updateCard(cardId: string, input: IdentityCardUpdateInput): Promise<IdentityCard> {
      this.error = ''
      try {
        const card = await identityCardsApi.update(cardId, input)
        const idx = this.cards.findIndex((c) => c.id === cardId)
        if (idx >= 0) this.cards[idx] = card
        else this.cards.unshift(card)
        return card
      } catch (e) {
        if (e instanceof ApiError && e.type === 'version_conflict') {
          this.error = `版本冲突：${e.message}（请刷新后重试）`
        } else {
          this.error = e instanceof Error ? e.message : String(e)
        }
        throw e
      }
    },
  },
})
