import { create } from 'zustand'
import { api, type Task, type TaskFilters } from '../api/client'

type TasksState = {
  tasks: Task[]
  loading: boolean
  // True once the first fetch() has completed successfully. Consumers (e.g.
  // OnboardingChecklist) use this instead of running their own fetch to know
  // whether `tasks` reflects a real load or just the empty initial state.
  // Left false on an error (see fetch's catch branch) so a failed load
  // doesn't make an empty board look like a legitimately-loaded empty one.
  loaded: boolean
  error: string | null
  // Monotonic counter used to ignore stale fetch() results: each call
  // captures the value at its start and checks it hasn't changed (i.e. no
  // newer fetch() has started) before committing state.
  reqId: number
  // Ids upserted/removed via WS while a fetch() sweep is in flight. A sweep
  // that finishes after such an update must not resurrect/overwrite it with
  // the page data it fetched before the update arrived.
  upsertedDuringFetch: Set<string>
  removedDuringFetch: Set<string>
  fetch: (filters?: TaskFilters) => Promise<void>
  upsert: (task: Task) => void
  remove: (id: string) => void
}

export const useTasksStore = create<TasksState>((set, get) => ({
  tasks: [],
  loading: false,
  loaded: false,
  error: null,
  reqId: 0,
  upsertedDuringFetch: new Set(),
  removedDuringFetch: new Set(),

  fetch: async (filters?: TaskFilters) => {
    const myReq = get().reqId + 1
    // Reset the mid-flight tracking sets for *this* sweep. If another fetch
    // is already in flight, its own stale-check below will make it a no-op
    // once it notices reqId has moved on, so clobbering the sets here is safe.
    set({ loading: true, error: null, reqId: myReq, upsertedDuringFetch: new Set(), removedDuringFetch: new Set() })
    try {
      // The board shows every matching task grouped by column, so page through
      // all results (the endpoint caps each response) rather than showing only
      // the first page. Each request is bounded; a modest board resolves in one.
      const all: Task[] = []
      let after: string | undefined
      for (let guard = 0; guard < 100; guard++) {
        // Bail if a newer fetch() has superseded this one, so a slow
        // multi-page sweep doesn't keep making requests (or eventually
        // commit stale results) after a fresher call has started.
        if (get().reqId !== myReq) return
        const page = await api.tasks.list(filters, { after, limit: 200 })
        all.push(...page.items)
        if (!page.nextCursor) break
        after = page.nextCursor
      }
      if (get().reqId !== myReq) return

      // Merge in any WS upsert/remove that landed while this sweep was in
      // flight — it is newer than the page data it would otherwise overwrite.
      const { upsertedDuringFetch, removedDuringFetch, tasks: liveTasks } = get()
      let merged = all
        .filter((t) => !removedDuringFetch.has(t.id))
        .map((t) => {
          if (!upsertedDuringFetch.has(t.id)) return t
          const live = liveTasks.find((lt) => lt.id === t.id)
          return live ?? t
        })
      // Tasks that were newly created (upserted) during the sweep and aren't
      // present in the page data at all — e.g. created after the page that
      // would have contained them was already fetched. Prepend them,
      // matching upsert()'s own prepend-on-create semantics.
      const mergedIds = new Set(merged.map((t) => t.id))
      const newlyCreated = liveTasks.filter((t) => upsertedDuringFetch.has(t.id) && !mergedIds.has(t.id))
      merged = [...newlyCreated, ...merged]

      set({ tasks: merged, loading: false, loaded: true })
    } catch (e) {
      if (get().reqId !== myReq) return
      set({ error: String(e), loading: false })
    }
  },

  upsert: (task) => {
    set((s) => {
      const idx = s.tasks.findIndex((t) => t.id === task.id)
      const nextUpsertedDuringFetch = s.loading
        ? new Set(s.upsertedDuringFetch).add(task.id)
        : s.upsertedDuringFetch
      if (idx >= 0) {
        const next = [...s.tasks]
        next[idx] = task
        return { tasks: next, upsertedDuringFetch: nextUpsertedDuringFetch }
      }
      return { tasks: [task, ...s.tasks], upsertedDuringFetch: nextUpsertedDuringFetch }
    })
  },

  remove: (id) =>
    set((s) => ({
      tasks: s.tasks.filter((t) => t.id !== id),
      removedDuringFetch: s.loading ? new Set(s.removedDuringFetch).add(id) : s.removedDuringFetch,
    })),
}))
