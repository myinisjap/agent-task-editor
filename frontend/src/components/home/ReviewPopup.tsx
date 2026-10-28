import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import type { Workflow } from '../../api/client'
import ModalShell from '../shared/ModalShell'
import InboxPreview from '../inbox/InboxPreview'
import ShouldHaveAskedDialog from '../shared/ShouldHaveAskedDialog'
import { Icon } from '../ui'
import { useNeedsMeStore } from '../../stores/needsMe'
import { useInboxActions } from '../../lib/useInboxActions'
import { useInboxKeyboard, type InboxKeyAction } from '../../lib/inboxKeys'
import { entryKey, entryTaskId, isCountedEntry, itemActions, neighborKey, nextAfterRemoval, type InboxEntry } from '../../lib/inbox'

const ICON_BTN = 'p-1.5 rounded text-fg-subtle hover:text-fg disabled:opacity-40 disabled:hover:text-fg-subtle'
const ALL_CLEAR_MS = 1500

type Props = {
  flat: InboxEntry[]
  selectedKey: string | undefined
  /** Move to another entry (replaces the history entry). */
  onMove: (key: string) => void
  onClose: () => void
  workflows: Workflow[]
}

/** Needs-you review popup: a centred dialog (bottom sheet on phones) over Home. */
export default function ReviewPopup({ flat, selectedKey, onMove, onClose, workflows }: Props) {
  const navigate = useNavigate()
  const [allClear, setAllClear] = useState(false)
  const [focusTick, setFocusTick] = useState(0)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const actions = useInboxActions({ onSuccess: (adv) => (adv ? onMove(adv) : setAllClear(true)) })

  const index = flat.findIndex((e) => entryKey(e) === selectedKey)
  const entry = index >= 0 ? flat[index] : undefined
  const clear = allClear || flat.length === 0

  useEffect(() => {
    if (focusTick > 0) actions.noteRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusTick])

  useEffect(() => {
    if (!clear) return
    const t = setTimeout(onClose, ALL_CLEAR_MS)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clear])

  const workflowOf = (e: InboxEntry | undefined) =>
    e?.kind === 'needs_me' ? workflows.find((w) => w.id === e.item.workflow_id)
      : e?.kind === 'policy_approval' ? workflows.find((w) => w.id === e.approval.workflow_id)
      : undefined

  const onKey = (action: InboxKeyAction) => {
    switch (action) {
      case 'next':
      case 'prev': {
        const k = neighborKey(flat, selectedKey, action === 'next' ? 1 : -1)
        if (k) onMove(k)
        return
      }
      case 'open': {
        if (!entry) return
        const id = entryTaskId(entry)
        return navigate(id ? `/tasks/${id}` : '/settings/health')
      }
      case 'hold': {
        if (entry?.kind === 'needs_me' && entry.item.reason === 'auto_approving' && !actions.isBusy(entry)) void actions.hold(entry)
        return
      }
      case 'escape': {
        if (entry?.kind === 'policy_approval' && actions.isUndoConfirmOpen(entry)) return actions.closeUndoConfirm(entry)
        if (entry?.kind === 'needs_me' && actions.isNoteOpen(entry.item.task_id)) {
          return actions.closeNote(entry.item.task_id)
        }
        return onClose()
      }
      case 'approve':
      case 'reject': {
        if (entry?.kind !== 'needs_me') return
        const item = entry.item
        const a = itemActions(item, workflowOf(entry))
        if (action === 'approve') {
          if (a.approve && !actions.isBusy(entry)) void actions.approve(entry, nextAfterRemoval(flat, selectedKey))
        } else if (item.reason === 'human_gate' && a.requestChanges) {
          actions.openNote(item.task_id)
          setFocusTick((t) => t + 1)
        } else if (a.reply && item.run_id) {
          actions.replyRef.current?.focus()
        }
      }
    }
  }
  useInboxKeyboard(true, onKey, { ownDialogTestId: 'review-popup' })

  const shaEntry = entry?.kind === 'policy_approval' && actions.shouldAskKey === entryKey(entry) ? entry.approval : undefined
  const shaWorkflow = workflowOf(entry)

  const prev = neighborKey(flat, selectedKey, -1)
  const next = neighborKey(flat, selectedKey, 1)

  return (
    <ModalShell
      onClose={onClose}
      ariaLabel="Needs you review"
      testId="review-popup"
      closeOnEscape={false}
      initialFocusRef={headingRef}
      overlayClassName="fixed inset-0 z-50 flex items-end md:items-center justify-center bg-black/60"
      className="w-full md:max-w-3xl max-h-[90vh] md:max-h-[85vh] overflow-y-auto bg-surface-raised border border-border rounded-t-xl md:rounded-xl shadow-xl"
    >
      <div className="sticky top-0 z-10 flex items-center justify-between gap-2 px-4 py-3 bg-surface-raised border-b border-border">
        <h2 ref={headingRef} tabIndex={-1} className="text-sm font-semibold text-fg outline-none">
          {clear || index < 0 ? 'Needs you' : `${entry && !isCountedEntry(entry) ? (entry.kind === 'policy_approval' ? 'Approved for you' : 'Auto-approving') : 'Needs you'} · ${index + 1} of ${flat.length}`}
        </h2>
        <div className="flex items-center gap-1">
          <button type="button" aria-label="Previous item" className={ICON_BTN} disabled={clear || index <= 0} onClick={() => prev && onMove(prev)}>
            <Icon icon={ChevronUp} size="md" />
          </button>
          <button type="button" aria-label="Next item" className={ICON_BTN} disabled={clear || index < 0 || index >= flat.length - 1} onClick={() => next && onMove(next)}>
            <Icon icon={ChevronDown} size="md" />
          </button>
          <button type="button" aria-label="Close" className={ICON_BTN} onClick={onClose}>
            <Icon icon={X} size="md" />
          </button>
        </div>
      </div>
      <div className="p-4">
        {clear ? (
          <div data-testid="review-all-clear" className="text-center py-12">
            <p className="text-base font-semibold text-fg">All clear</p>
            <p className="text-xs text-fg-subtle mt-1">Nothing is waiting on you.</p>
          </div>
        ) : (
          <InboxPreview {...actions.previewProps(entry, workflowOf(entry), nextAfterRemoval(flat, selectedKey))} />
        )}
      </div>
      {shaEntry && (
        <ShouldHaveAskedDialog
          taskId={shaEntry.task_id}
          taskTitle={shaEntry.task_title}
          done={!!shaWorkflow?.labels.find((l) => l.name === shaEntry.label)?.is_terminal}
          workflow={shaWorkflow}
          onClose={actions.closeShouldHaveAsked}
          onDone={() => void useNeedsMeStore.getState().refresh()}
        />
      )}
    </ModalShell>
  )
}
