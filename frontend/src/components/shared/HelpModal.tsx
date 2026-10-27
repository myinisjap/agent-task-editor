import { X } from 'lucide-react'
import Icon from '../ui/Icon'
import type { ReactNode } from 'react'
import ModalShell from './ModalShell'

/**
 * Generic "help" overlay modal used to explain a page's purpose and
 * configuration to new users. Presentational only — callers own the
 * open/close state and supply their own content as children (typically a
 * series of <section> blocks; see components/shared/pageHelp.tsx).
 *
 * Built on the shared ModalShell (dialog role/aria, Escape, backdrop-click,
 * focus trap + restore) — see components/board/NewTaskModal.tsx and
 * pages/WorkflowPage.tsx's NewWorkflowModal for the other two consumers.
 */
export default function HelpModal({
  title,
  onClose,
  children,
}: {
  title: string
  onClose: () => void
  children: ReactNode
}) {
  return (
    <ModalShell
      onClose={onClose}
      ariaLabel={title}
      className="bg-surface-raised border border-border rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] flex flex-col"
    >
      <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
        <h2 className="text-lg font-semibold text-fg">{title}</h2>
        <button
          onClick={onClose}
          aria-label="Close"
          className="text-fg-subtle hover:text-fg transition-colors text-sm leading-none"
        >
          <Icon icon={X} size="md" />
        </button>
      </div>

      <div className="px-6 py-4 overflow-y-auto flex flex-col gap-5 text-sm text-fg-muted">
        {children}
      </div>
    </ModalShell>
  )
}
