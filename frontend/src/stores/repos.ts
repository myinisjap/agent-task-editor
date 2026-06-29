import { create } from 'zustand'
import { api, type Repo } from '../api/client'

type ReposState = {
  repos: Repo[]
  loading: boolean
  error: string | null
  fetch: () => Promise<void>
  byId: (id: string) => Repo | undefined
}

export const useReposStore = create<ReposState>((set, get) => ({
  repos: [],
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true, error: null })
    try {
      const repos = await api.repos.list()
      set({ repos: repos ?? [], loading: false })
    } catch (e) {
      set({ error: String(e), loading: false })
    }
  },
  byId: (id) => get().repos.find((r) => r.id === id),
}))
