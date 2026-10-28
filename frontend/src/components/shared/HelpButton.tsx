import { Info } from 'lucide-react'
import Icon from '../ui/Icon'

/**
 * Small circular "i" trigger button that opens a HelpModal. Visually
 * consistent across every page — keep this the single source for the icon.
 */
export default function HelpButton({
  onClick,
  title = 'Help',
}: {
  onClick: () => void
  title?: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      aria-label={title}
      className="w-7 h-7 flex items-center justify-center rounded-full bg-surface-sunken hover:bg-border text-fg-muted hover:text-fg transition-colors flex-shrink-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <Icon icon={Info} size="md" />
    </button>
  )
}
