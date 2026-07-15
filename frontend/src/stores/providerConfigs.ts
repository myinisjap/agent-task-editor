import { create } from 'zustand'
import { api, type ProviderConfig } from '../api/client'

type ProviderConfigsState = {
  configs: ProviderConfig[]
  loading: boolean
  fetch: () => Promise<void>
  create: (payload: Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>) => Promise<ProviderConfig>
  update: (id: string, payload: Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>) => Promise<ProviderConfig>
  delete: (id: string) => Promise<void>
}

export const useProviderConfigsStore = create<ProviderConfigsState>((set) => ({
  configs: [],
  loading: false,

  fetch: async () => {
    set({ loading: true })
    try {
      const configs = await api.providerConfigs.list()
      set({ configs: configs ?? [], loading: false })
    } catch {
      set({ loading: false })
    }
  },

  create: async (payload) => {
    const config = await api.providerConfigs.create(payload)
    await useProviderConfigsStore.getState().fetch()
    return config
  },

  update: async (id, payload) => {
    const config = await api.providerConfigs.update(id, payload)
    await useProviderConfigsStore.getState().fetch()
    return config
  },

  delete: async (id) => {
    await api.providerConfigs.delete(id)
    await useProviderConfigsStore.getState().fetch()
  },
}))
