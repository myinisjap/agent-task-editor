import { create } from 'zustand'
import { api } from '../api/client'
import { failedEffectTasks, sourceEditTasks, type FailedEffectTask, type SourceEditTask } from '../lib/inbox'
import { useNeedsMeStore } from './needsMe'
import { useHealthStore } from './health'

const PAGE_SIZE = 200
const MAX_PAGES = 5

type InboxState = {
  /** Tasks with a permanently failed side-effect (push / PR / write-back). */
  failedEffects: FailedEffectTask[]
  /** Tasks holding a source-issue edit made after approval. */
  sourceEdits: SourceEditTask[]
  loaded: boolean
  refresh: () => Promise<void>
}

/**
 * The part of the inbox that has no dedicated endpoint: failed outbox rows are
 * read off GET /tasks (each task carries its outstanding side_effects and any held source_pending_update). Needs-me
 * items and paused providers live in their own stores.
 */
export const useInboxStore = create<InboxState>((set) => ({
  failedEffects: [],
  sourceEdits: [],
  loaded: false,
  refresh: async () => {
    try {
      const tasks = []
      let after: string | undefined
      for (let i = 0; i < MAX_PAGES; i++) {
        const page = await api.tasks.list(undefined, { after, limit: PAGE_SIZE })
        tasks.push(...page.items)
        if (!page.nextCursor) break
        after = page.nextCursor
      }
      set({ failedEffects: failedEffectTasks(tasks), sourceEdits: sourceEditTasks(tasks), loaded: true })
    } catch {
      /* keep the previous set on transient failures */
      set({ loaded: true })
    }
  },
}))

/** Total number of things waiting on the human, for the nav badge. */
export function useInboxCount(): number {
  const needsMe = useNeedsMeStore((s) => s.items.length)
  const failed = useInboxStore((s) => s.failedEffects.length)
  const sourceEdits = useInboxStore((s) => s.sourceEdits.length)
  const paused = useHealthStore((s) => s.paused.length)
  return needsMe + failed + sourceEdits + paused
}
