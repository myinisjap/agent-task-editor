import { create } from 'zustand'
import { api, type NeedsMeItem, type PolicyApprovalItem } from '../api/client'

type Snapshot = Pick<NeedsMeState, 'items' | 'byTask' | 'autoApproving' | 'autoApprovingByTask' | 'approvedByPolicy'>

type NeedsMeState = {
  /** Items waiting on the human (no auto_approving rows). Drives the badge, board cards and notifications. */
  items: NeedsMeItem[]
  byTask: Record<string, NeedsMeItem>
  /** Pending auto-approvals (reason auto_approving). Never counted as needing the user. */
  autoApproving: NeedsMeItem[]
  autoApprovingByTask: Record<string, NeedsMeItem>
  /** Policy approvals from the last 24h, newest first. */
  approvedByPolicy: PolicyApprovalItem[]
  /** True once the first refresh has settled (success or failure). */
  loaded: boolean
  refresh: () => Promise<void>
  /** Optimistically moves a pending auto-approval into the human set. Returns a revert function. */
  markHeld: (taskId: string) => () => void
  /** Optimistically drops a policy approval. Returns a revert function. */
  markUndone: (taskId: string) => () => void
}

function split(all: NeedsMeItem[]) {
  const items = all.filter((i) => i.reason !== 'auto_approving')
  const autoApproving = all.filter((i) => i.reason === 'auto_approving')
  const byTask: Record<string, NeedsMeItem> = {}
  for (const it of items) byTask[it.task_id] = it
  const autoApprovingByTask: Record<string, NeedsMeItem> = {}
  for (const it of autoApproving) autoApprovingByTask[it.task_id] = it
  return { items, byTask, autoApproving, autoApprovingByTask }
}

/** Server-defined "needs me" set (GET /needs-me), shared by the nav badge, Home queue and board cards. */
export const useNeedsMeStore = create<NeedsMeState>((set, get) => {
  const snapshot = (): Snapshot => {
    const s = get()
    return { items: s.items, byTask: s.byTask, autoApproving: s.autoApproving, autoApprovingByTask: s.autoApprovingByTask, approvedByPolicy: s.approvedByPolicy }
  }
  return {
    items: [],
    byTask: {},
    autoApproving: [],
    autoApprovingByTask: {},
    approvedByPolicy: [],
    loaded: false,
    refresh: async () => {
      try {
        const r = await api.needsMe.get()
        set({ ...split(r.items), approvedByPolicy: r.approved_by_policy ?? [], loaded: true })
      } catch {
        /* keep the previous set on transient failures */
        set({ loaded: true })
      }
    },
    markHeld: (taskId) => {
      const prev = snapshot()
      const pending = prev.autoApprovingByTask[taskId]
      if (pending) {
        const held: NeedsMeItem = { ...pending, reason: 'human_gate', due_at: undefined, decision_id: undefined }
        const { [taskId]: _gone, ...restBy } = prev.autoApprovingByTask
        void _gone
        set({
          autoApproving: prev.autoApproving.filter((i) => i.task_id !== taskId),
          autoApprovingByTask: restBy,
          items: [...prev.items, held],
          byTask: { ...prev.byTask, [taskId]: held },
        })
      }
      return () => set(prev)
    },
    markUndone: (taskId) => {
      const prev = snapshot()
      set({ approvedByPolicy: prev.approvedByPolicy.filter((a) => a.task_id !== taskId) })
      return () => set({ approvedByPolicy: prev.approvedByPolicy })
    },
  }
})
