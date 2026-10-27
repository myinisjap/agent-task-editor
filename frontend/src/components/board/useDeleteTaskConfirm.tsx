import { useCallback, useState, type ReactNode } from 'react'
import type { Task } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import ConfirmDialog from '../shared/ConfirmDialog'

/**
 * Shared by the board columns: `requestDelete` (stable identity, safe to pass
 * to memo(TaskCard)) opens an in-page confirm; `dialog` must be rendered by the
 * column *outside* the card list so clicks inside it don't bubble into a card.
 */
export function useDeleteTaskConfirm(): { requestDelete: (task: Task) => void; dialog: ReactNode } {
  const remove = useTasksStore((s) => s.remove)
  const [pending, setPending] = useState<Task | null>(null)
  const requestDelete = useCallback((task: Task) => setPending(task), [])

  const dialog = pending ? (
    <ConfirmDialog
      destructive
      title="Delete task?"
      confirmLabel="Delete task"
      onCancel={() => setPending(null)}
      onConfirm={async () => {
        await api.tasks.delete(pending.id)
        remove(pending.id)
        setPending(null)
      }}
    >
      <p>
        &quot;{pending.title}&quot; and its agent runs, logs and review comments will be permanently deleted. This can&apos;t
        be undone.
      </p>
    </ConfirmDialog>
  ) : null

  return { requestDelete, dialog }
}
