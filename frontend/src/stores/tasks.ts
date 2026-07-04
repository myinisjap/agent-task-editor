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
      const tasks = await api.tasks.list(filters)
      set({ tasks: tasks ?? [], loading: false })
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
