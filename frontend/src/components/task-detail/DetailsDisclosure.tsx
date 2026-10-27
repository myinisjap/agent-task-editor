import { ChevronDown, ChevronRight } from 'lucide-react'
import Icon from '../ui/Icon'
import { useId, useState, type ReactNode } from 'react'

export const DETAILS_OPEN_KEY = 'taskDetail.details.open'

function readOpen(): boolean {
  try {
    return localStorage.getItem(DETAILS_OPEN_KEY) === '1'
  } catch {
    return false
  }
}

// DetailsDisclosure hides rarely used Task Detail sections (dependencies, force
// move, label history, source comments). Open/closed is remembered per browser.
export default function DetailsDisclosure({
  children,
  hint = 'Dependencies, force move, label history, source comments',
}: {
  children: ReactNode
  hint?: string
}) {
  const [open, setOpen] = useState(readOpen)
  const panelId = useId()
  const toggle = () => {
    const next = !open
    setOpen(next)
    try {
      localStorage.setItem(DETAILS_OPEN_KEY, next ? '1' : '0')
    } catch {
      /* storage unavailable (e.g. Safari private mode) */
    }
  }
  return (
    <div data-testid="task-details-disclosure" className="flex flex-col gap-4 min-w-0">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={panelId}
        className="flex items-center gap-2 text-left text-xs min-w-0"
      >
        <span aria-hidden="true" className="text-fg-subtle"><Icon icon={open ? ChevronDown : ChevronRight} size="sm" /></span>
        <span className="font-semibold text-fg-muted">Details</span>
        <span className="text-fg-subtle truncate">{hint}</span>
      </button>
      <div id={panelId} className="flex flex-col gap-4 min-w-0">
        {open && children}
      </div>
    </div>
  )
}
