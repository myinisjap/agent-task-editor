import { create } from 'zustand'
import { api, type NeedsMeItem } from '../api/client'

type NeedsMeState = {
  items: NeedsMeItem[]
  byTask: Record<string, NeedsMeItem>
  refresh: () => Promise<void>
}

/** Server-defined "needs me" set (GET /needs-me), shared by the nav badge, Dashboard queue and board cards. */
export const useNeedsMeStore = create<NeedsMeState>((set) => ({
  items: [],
  byTask: {},
  refresh: async () => {
    try {
      const items = await api.needsMe.list()
      const byTask: Record<string, NeedsMeItem> = {}
      for (const it of items) byTask[it.task_id] = it
      set({ items, byTask })
    } catch {
      /* keep the previous set on transient failures */
    }
  },
}))
