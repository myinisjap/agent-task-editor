import { create } from 'zustand'
import { api, type Task, type TaskFilters } from '../api/client'

type TasksState = {
  tasks: Task[]
  loading: boolean
  error: string | null
  fetch: (filters?: TaskFilters) => Promise<void>
  upsert: (task: Task) => void
  remove: (id: string) => void
}

export const useTasksStore = create<TasksState>((set) => ({
  tasks: [],
  loading: false,
  error: null,

  fetch: async (filters?: TaskFilters) => {
    set({ loading: true, error: null })
    try {
      // The board shows every matching task grouped by column, so page through
      // all results (the endpoint caps each response) rather than showing only
      // the first page. Each request is bounded; a modest board resolves in one.
      const all: Task[] = []
      let after: string | undefined
      for (let guard = 0; guard < 100; guard++) {
        const page = await api.tasks.list(filters, { after, limit: 200 })
        all.push(...page.items)
        if (!page.nextCursor) break
        after = page.nextCursor
      }
      set({ tasks: all, loading: false })
    } catch (e) {
      set({ error: String(e), loading: false })
    }
  },

  upsert: (task) => {
    set((s) => {
      const idx = s.tasks.findIndex((t) => t.id === task.id)
      if (idx >= 0) {
        const next = [...s.tasks]
        next[idx] = task
        return { tasks: next }
      }
      return { tasks: [task, ...s.tasks] }
    })
  },

  remove: (id) => set((s) => ({ tasks: s.tasks.filter((t) => t.id !== id) })),
}))
