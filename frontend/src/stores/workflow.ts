import { create } from 'zustand'
import { api, type Workflow } from '../api/client'
import { defaultWorkflow, resolveSelectedWorkflow } from '../lib/defaultWorkflow'

const SELECTED_ID_KEY = 'workflow.selectedId'

type WorkflowState = {
  workflows: Workflow[]
  loading: boolean
  selectedId: string | null
  fetch: () => Promise<void>
  setSelectedId: (id: string) => void
  active: () => Workflow | undefined
}

// A transient failure (backend still warming up, a locked sqlite write) must not
// leave the board permanently without columns, so retry a few times.
async function listWithRetry(attempts = 4, delayMs = 400) {
  for (let i = 1; ; i++) {
    try {
      return await api.workflows.list()
    } catch (err) {
      if (i >= attempts) throw err
      await new Promise((r) => setTimeout(r, delayMs * i))
    }
  }
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
      const workflows = await listWithRetry()
      const list = workflows ?? []
      // Validate stored selectedId still exists in the fetched list
      const storedId = get().selectedId
      const storedValid = !!storedId && list.some((w) => w.id === storedId)
      const validId = storedValid ? storedId : (defaultWorkflow(list)?.id ?? null)
      set({ workflows: list, loading: false, selectedId: validId })
      // Only explicit selections (setSelectedId) are persisted; drop a stale one.
      if (!storedValid) { try { localStorage.removeItem(SELECTED_ID_KEY) } catch { /* ignore */ } }
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
    return resolveSelectedWorkflow(workflows, selectedId)
  },
}))
