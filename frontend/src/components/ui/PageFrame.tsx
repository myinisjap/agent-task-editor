import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import HelpButton from '../shared/HelpButton'
import { cx } from './cx'

export type PageTab = { to: string; label: string; badge?: ReactNode }

export type PageFrameProps = {
  title: ReactNode
  titleAdornment?: ReactNode
  help?: { onClick: () => void; title?: string }
  actions?: ReactNode
  tabs?: PageTab[]
  width?: 'default' | 'wide' | 'full'
  description?: ReactNode
  /** Fill the available height (graphs, boards); children get a flex column. */
  fill?: boolean
  headerClassName?: string
  testId?: string
  children: ReactNode
}

const WIDTH = { default: 'max-w-5xl', wide: 'max-w-[1600px]', full: '' }

export default function PageFrame({ title, titleAdornment, help, actions, tabs, width = 'default', description, fill, headerClassName, testId, children }: PageFrameProps) {
  return (
    <div className={cx('p-4 md:p-6 mx-auto w-full', fill && 'h-full min-h-0 flex flex-col', WIDTH[width])} data-testid={testId}>
      <div className={cx('flex flex-wrap items-center gap-2', headerClassName ?? 'mb-6', fill && 'shrink-0')}>
        <h1 className="min-w-0 text-xl font-semibold text-fg">{title}</h1>
        {titleAdornment}
        {help && <HelpButton onClick={help.onClick} title={help.title} />}
        {actions && <div className="ml-auto flex gap-2 min-w-0">{actions}</div>}
      </div>
      {description && <p className="text-sm text-fg-muted -mt-4 mb-6">{description}</p>}
      {tabs && tabs.length > 0 && (
        <nav aria-label="Page tabs" className="flex gap-4 border-b border-border mb-4">
          {tabs.map((t) => (
            <NavLink
              key={t.to}
              to={t.to}
              className={({ isActive }) =>
                cx('pb-2 -mb-px text-sm border-b-2 inline-flex items-center gap-1.5', isActive ? 'border-accent text-accent-fg' : 'border-transparent text-fg-muted hover:text-fg')
              }
            >
              {t.label}
              {t.badge}
            </NavLink>
          ))}
        </nav>
      )}
      {fill ? <div className="flex-1 min-h-0 flex flex-col">{children}</div> : children}
    </div>
  )
}
