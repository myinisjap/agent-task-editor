import { create } from 'zustand'
import { api, type AgentConfig } from '../api/client'

type AgentsState = {
  configs: AgentConfig[]
  loading: boolean
  fetch: () => Promise<void>
}

export const useAgentsStore = create<AgentsState>((set) => ({
  configs: [],
  loading: false,

  fetch: async () => {
    set({ loading: true })
    try {
      const configs = await api.agents.list()
      set({ configs: configs ?? [], loading: false })
    } catch {
      set({ loading: false })
    }
  },
}))
