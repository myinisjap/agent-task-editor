import type { LucideIcon } from 'lucide-react'
import { cx } from './cx'

export type IconSize = 'xs' | 'sm' | 'md' | 'lg'

const SIZE: Record<IconSize, string> = {
  xs: 'h-3 w-3',
  sm: 'h-3.5 w-3.5',
  md: 'h-4 w-4',
  lg: 'h-5 w-5',
}

/** Decorative Lucide icon. The text label (or the parent's aria-label) carries the meaning. */
export default function Icon({ icon: Glyph, size = 'md', className }: { icon: LucideIcon; size?: IconSize; className?: string }) {
  return <Glyph aria-hidden="true" focusable="false" strokeWidth={2} className={cx('shrink-0', SIZE[size], className)} />
}
