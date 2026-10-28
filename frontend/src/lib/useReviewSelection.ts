import { useCallback, useEffect, useRef } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { entryKey, entryTaskId, isCountedEntry, resolveSelection, type InboxEntry } from './inbox'

/**
 * URL-backed state of the Needs-you review popup.
 *  - `?task=<entryKey|taskId>` : popup open on that entry.
 *  - `?review=1`               : open on the first entry (once `loaded`).
 * Row clicks push a history entry (so Back closes); moves replace it.
 */
export function useReviewSelection(flat: InboxEntry[], loaded: boolean) {
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const param = params.get('task')
  const review = params.has('review')
  const locState = location.state
  // The last param value that matched an entry, and where it sat in the queue.
  const lastSel = useRef<{ param?: string; index: number }>({ index: 0 })

  const found = param ? flat.find((e) => entryKey(e) === param) ?? flat.find((e) => entryTaskId(e) === param) : undefined
  const selectedKey = found ? resolveSelection(flat, param) : undefined
  // Still "open" when the entry vanished (approved elsewhere) after having been shown.
  const vanished = !!param && !found && lastSel.current.param === param
  const isOpen = !!found || vanished

  const move = useCallback((key: string | undefined) => {
    if (!key) return
    setParams({ task: key }, { replace: true, state: locState })
  }, [setParams, locState])

  const openKey = useCallback((key: string) => {
    setParams({ task: key }, { state: { review: true } })
  }, [setParams])

  const close = useCallback(() => {
    if ((locState as { review?: boolean } | null)?.review) {
      navigate(-1)
      return
    }
    setParams((p) => {
      p.delete('task')
      p.delete('review')
      return p
    }, { replace: true })
  }, [locState, navigate, setParams])

  useEffect(() => {
    if (found) {
      lastSel.current = { param: param ?? undefined, index: flat.indexOf(found) }
      return
    }
    if (!loaded) return
    if (review && !param) {
      const first = flat.find(isCountedEntry)
      if (first) setParams({ task: entryKey(first) }, { replace: true })
      else setParams((p) => { p.delete('review'); return p }, { replace: true })
      return
    }
    if (!param) return
    if (lastSel.current.param === param) {
      // Resolved earlier and now gone: move to the neighbour at its old index (empty queue: the popup shows All clear).
      if (flat.length > 0) {
        move(entryKey(flat[Math.min(lastSel.current.index, flat.length - 1)]))
      }
      return
    }
    // Never matched an entry: drop the param and don't open.
    setParams((p) => { p.delete('task'); return p }, { replace: true })
  }, [found, flat, loaded, param, review, move, setParams])

  return { selectedKey, isOpen, openKey, move, close }
}
