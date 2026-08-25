// 团队状态：列表 / 创建 / 归档 / 成员（api-contract.md §3.1）
import { defineStore } from 'pinia'
import { teamsApi } from '@/api/client'
import type { Member, Team, TeamCreateInput } from '@/api/types'
import { ApiError } from '@/api/client'

export const useTeamsStore = defineStore('teams', {
  state: () => ({
    teams: [] as Team[],
    members: [] as Member[],
    loading: false,
    error: '' as string,
  }),
  getters: {
    activeTeams: (state) => state.teams.filter((t) => t.status !== 'archived'),
  },
  actions: {
    async fetchTeams() {
      this.loading = true
      this.error = ''
      try {
        const res = await teamsApi.list({ limit: 100 })
        this.teams = Array.isArray(res) ? res : res.items
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async createTeam(input: TeamCreateInput): Promise<Team> {
      this.error = ''
      try {
        const team = await teamsApi.create(input)
        this.teams.unshift(team)
        return team
      } catch (e) {
        if (e instanceof ApiError && e.type === 'conflict') {
          this.error = `slug 冲突：${e.message}`
        } else {
          this.error = e instanceof Error ? e.message : String(e)
        }
        throw e
      }
    },
    async archiveTeam(id: string) {
      await teamsApi.archive(id)
      const t = this.teams.find((x) => x.id === id)
      if (t) t.status = 'archived'
    },
    async fetchMembers(teamId: string) {
      this.members = await teamsApi.members(teamId)
    },
    async addMember(teamId: string, userId: string, name: string) {
      const m = await teamsApi.addMember(teamId, { user_id: userId, name })
      this.members.push(m)
      return m
    },
    async removeMember(teamId: string, userId: string) {
      await teamsApi.removeMember(teamId, userId)
      this.members = this.members.filter((m) => m.user_id !== userId)
    },
  },
})
