import type { HTMLAttributes, Ref } from 'react'
import { cx } from './cx'

export type CardProps = HTMLAttributes<HTMLElement> & {
  ref?: Ref<HTMLDivElement>
  as?: 'div' | 'section' | 'article' | 'aside'
  padding?: 'none' | 'sm' | 'md'
  selected?: boolean
  interactive?: boolean
}

const PAD = { none: '', sm: 'p-3', md: 'p-4' }

export default function Card({ as: Tag = 'div', padding = 'none', selected, interactive, className, children, ...rest }: CardProps) {
  return (
    <Tag
      {...rest}
      className={cx(
        'bg-surface-raised border border-border rounded-lg min-w-0',
        PAD[padding],
        selected && 'border-accent ring-2 ring-accent',
        interactive && 'cursor-pointer hover:border-fg-subtle',
        className,
      )}
    >
      {children}
    </Tag>
  )
}
