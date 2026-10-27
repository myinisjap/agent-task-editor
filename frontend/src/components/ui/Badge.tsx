import type { HTMLAttributes } from 'react'
import { cx } from './cx'

export type BadgeTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'

const SOFT: Record<BadgeTone, string> = {
  neutral: 'bg-surface-sunken text-fg-muted border border-border',
  accent: 'bg-accent/15 text-accent-fg border border-accent/30',
  success: 'bg-success-soft text-success border border-success/30',
  warning: 'bg-warning-soft text-warning border border-warning/30',
  danger: 'bg-danger-soft text-danger border border-danger/30',
  info: 'bg-info-soft text-info border border-info/30',
}
// Solid fills exist only for tones with a token that meets AA with on-accent text.
// Other tones fall back to soft.
const SOLID: Partial<Record<BadgeTone, string>> = {
  accent: 'bg-accent text-on-accent border border-transparent',
  danger: 'bg-danger-solid text-on-accent border border-transparent',
}

export type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  tone?: BadgeTone
  variant?: 'soft' | 'solid'
  size?: 'sm' | 'xs'
}

export default function Badge({ tone = 'neutral', variant = 'soft', size = 'sm', className, children, ...rest }: BadgeProps) {
  const color = (variant === 'solid' && SOLID[tone]) || SOFT[tone]
  return (
    <span
      {...rest}
      className={cx('inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium', size === 'xs' ? 'text-[10px]' : 'text-xs', color, className)}
    >
      {children}
    </span>
  )
}
