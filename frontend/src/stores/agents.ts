import { create } from 'zustand'
import { api, type AgentConfig, type ModelList, type ClaudeOptions } from '../api/client'

type AgentsState = {
  configs: AgentConfig[]
  loading: boolean
  modelList: ModelList | null
  fetchingModels: boolean
  claudeOptions: ClaudeOptions | null
  fetch: () => Promise<void>
  fetchModels: (provider: string) => Promise<ModelList | null>
  fetchClaudeOptions: () => Promise<void>
  create: (payload: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled'>) => Promise<{ config: AgentConfig; labelConflict?: string }>
  update: (id: string, payload: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at'> & { enabled?: boolean }) => Promise<AgentConfig>
  delete: (id: string) => Promise<void>
}

export const useAgentsStore = create<AgentsState>((set) => ({
  configs: [],
  loading: false,
  modelList: null,
  fetchingModels: false,
  claudeOptions: null,

  fetch: async () => {
    set({ loading: true })
    try {
      const configs = await api.agents.list()
      set({ configs: configs ?? [], loading: false })
    } catch {
      set({ loading: false })
    }
  },

  // fetchModels wraps the /agents/models lookup for non-claude providers.
  // Returns the fetched ModelList (or null on failure) so the caller can
  // decide whether to default a form field — that's page-local form state,
  // not something the store should mutate.
  fetchModels: async (provider: string) => {
    set({ fetchingModels: true })
    try {
      const data = await api.agents.models(provider)
      set({ modelList: data, fetchingModels: false })
      return data
    } catch {
      set({ modelList: null, fetchingModels: false })
      return null
    }
  },

  fetchClaudeOptions: async () => {
    try {
      const opts = await api.agents.claudeOptions()
      set({ claudeOptions: opts })
    } catch {
      set({ claudeOptions: null })
    }
  },

  create: async (payload) => {
    const result = await api.agents.create(payload)
    await useAgentsStore.getState().fetch()
    return result
  },

  update: async (id, payload) => {
    const updated = await api.agents.update(id, payload)
    await useAgentsStore.getState().fetch()
    return updated
  },

  delete: async (id) => {
    await api.agents.delete(id)
    await useAgentsStore.getState().fetch()
  },
}))
