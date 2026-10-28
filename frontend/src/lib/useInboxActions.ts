import { useRef, useState } from 'react'
import { api, type Workflow } from '../api/client'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { entryKey, entryTaskId, type InboxEntry } from './inbox'
import type { InboxPreviewProps } from '../components/inbox/InboxPreview'

/**
 * All action state and wiring for inbox entries (approve, request changes,
 * reply, retry, dismiss, apply/discard, resume). Busy/error state is keyed by
 * `entryKey`; notes and replies by task id.
 */
export function useInboxActions({ onSuccess }: { onSuccess?: (advance: string | undefined) => void } = {}) {
  const refreshNeedsMe = useNeedsMeStore((s) => s.refresh)
  const refreshEffects = useInboxStore((s) => s.refresh)
  const resume = useHealthStore((s) => s.resume)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [noteOpen, setNoteOpen] = useState<Record<string, boolean>>({})
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [replies, setReplies] = useState<Record<string, string>>({})
  const noteRef = useRef<HTMLTextAreaElement>(null)
  const replyRef = useRef<HTMLTextAreaElement>(null)

  const refreshAll = () => Promise.all([refreshNeedsMe(), refreshEffects()])

  // Resolves true on success; `advance` is handed to onSuccess so the caller can move on.
  const run = async (key: string, fn: () => Promise<unknown>, after: () => Promise<unknown> | void, advance?: string): Promise<boolean> => {
    setErrors((e) => ({ ...e, [key]: '' }))
    setBusy((b) => ({ ...b, [key]: true }))
    try {
      await fn()
      await after()
      onSuccess?.(advance)
      return true
    } catch (e) {
      setErrors((m) => ({ ...m, [key]: e instanceof Error ? e.message : String(e) }))
      return false
    } finally {
      setBusy((b) => ({ ...b, [key]: false }))
    }
  }

  const ek = entryKey
  const idOf = (e: InboxEntry) => entryTaskId(e) ?? ''

  const approve = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.approve(idOf(e)), refreshAll, adv)
  const sendChanges = (e: InboxEntry, adv?: string) => {
    const id = idOf(e)
    return run(ek(e), async () => {
      await api.tasks.reject(id, (notes[id] ?? '').trim())
      setNotes((n) => ({ ...n, [id]: '' }))
      setNoteOpen((o) => ({ ...o, [id]: false }))
    }, refreshAll, adv)
  }
  const sendReply = (e: InboxEntry, adv?: string) => {
    if (e.kind !== 'needs_me' || !e.item.run_id) return Promise.resolve(false)
    const { task_id: id, run_id } = e.item
    return run(ek(e), async () => {
      await api.tasks.replyRun(id, run_id, (replies[id] ?? '').trim())
      setReplies((r) => ({ ...r, [id]: '' }))
    }, refreshAll, adv)
  }
  const retry = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.rerun(idOf(e)), refreshAll, adv)
  const dismissEffects = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.dismissSideEffects(idOf(e)), refreshEffects, adv)
  const retryEffects = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.retrySideEffects(idOf(e)), refreshEffects, adv)
  const applySource = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.applySourceUpdate(idOf(e)), refreshEffects, adv)
  const discardSource = (e: InboxEntry, adv?: string) => run(ek(e), () => api.tasks.discardSourceUpdate(idOf(e)), refreshEffects, adv)
  const resumeProvider = (e: InboxEntry, adv?: string) =>
    e.kind === 'provider' ? run(ek(e), () => resume(e.provider.provider), () => undefined, adv) : Promise.resolve(false)

  const openNote = (taskId: string) => setNoteOpen((o) => ({ ...o, [taskId]: true }))
  const closeNote = (taskId: string) => setNoteOpen((o) => ({ ...o, [taskId]: false }))
  const isNoteOpen = (taskId: string) => !!noteOpen[taskId]
  const isBusy = (e: InboxEntry) => !!busy[ek(e)]

  /** Every InboxPreview prop for an entry. */
  const previewProps = (entry: InboxEntry | undefined, workflow: Workflow | undefined, advance: string | undefined): InboxPreviewProps => {
    const key = entry ? ek(entry) : ''
    const id = entry ? idOf(entry) : ''
    const e = entry as InboxEntry
    return {
      entry,
      workflow,
      noteRef,
      replyRef,
      busy: !!busy[key],
      error: errors[key],
      noteOpen: !!noteOpen[id],
      note: notes[id] ?? '',
      setNote: (v) => setNotes((n) => ({ ...n, [id]: v })),
      setNoteOpen: (v) => setNoteOpen((o) => ({ ...o, [id]: v })),
      reply: replies[id] ?? '',
      setReply: (v) => setReplies((r) => ({ ...r, [id]: v })),
      onApprove: () => void approve(e, advance),
      onReject: () => void sendChanges(e, advance),
      onReply: () => void sendReply(e, advance),
      onRetry: () => void retry(e, advance),
      onDismissEffects: () => void dismissEffects(e, advance),
      onRetryEffects: () => void retryEffects(e, advance),
      onApplySource: () => void applySource(e, advance),
      onDiscardSource: () => void discardSource(e, advance),
      onResume: () => void resumeProvider(e, advance),
    }
  }

  return {
    noteRef, replyRef, previewProps, isBusy,
    approve, sendChanges, sendReply, retry, dismissEffects, retryEffects, applySource, discardSource, resumeProvider,
    openNote, closeNote, isNoteOpen,
  }
}
