import { create } from 'zustand'
import { api, type Workflow } from '../api/client'

const SELECTED_ID_KEY = 'workflow.selectedId'

type WorkflowState = {
  workflows: Workflow[]
  loading: boolean
  selectedId: string | null
  fetch: () => Promise<void>
  setSelectedId: (id: string) => void
  active: () => Workflow | undefined
}

export const useWorkflowStore = create<WorkflowState>((set, get) => ({
  workflows: [],
  loading: false,
  selectedId: (() => {
    try { return localStorage.getItem(SELECTED_ID_KEY) } catch { return null }
  })(),

  fetch: async () => {
    set({ loading: true })
    try {
      const workflows = await api.workflows.list()
      const list = workflows ?? []
      // Validate stored selectedId still exists in the fetched list
      const storedId = get().selectedId
      const validId = storedId && list.some((w) => w.id === storedId)
        ? storedId
        : (list[0]?.id ?? null)
      set({ workflows: list, loading: false, selectedId: validId })
      try { if (validId) localStorage.setItem(SELECTED_ID_KEY, validId) } catch { /* ignore */ }
    } catch {
      set({ loading: false })
    }
  },

  setSelectedId: (id: string) => {
    set({ selectedId: id })
    try { localStorage.setItem(SELECTED_ID_KEY, id) } catch { /* ignore */ }
  },

  active: () => {
    const { workflows, selectedId } = get()
    return workflows.find((w) => w.id === selectedId) ?? workflows[0]
  },
}))
