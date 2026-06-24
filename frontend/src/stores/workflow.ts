import { create } from 'zustand'
import { api, type Workflow } from '../api/client'

type WorkflowState = {
  workflows: Workflow[]
  loading: boolean
  fetch: () => Promise<void>
  active: () => Workflow | undefined
}

export const useWorkflowStore = create<WorkflowState>((set, get) => ({
  workflows: [],
  loading: false,

  fetch: async () => {
    set({ loading: true })
    try {
      const workflows = await api.workflows.list()
      set({ workflows: workflows ?? [], loading: false })
    } catch {
      set({ loading: false })
    }
  },

  active: () => get().workflows[0],
}))
