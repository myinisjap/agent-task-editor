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
  testId?: string
  children: ReactNode
}

const WIDTH = { default: 'max-w-5xl', wide: 'max-w-[1600px]', full: '' }

export default function PageFrame({ title, titleAdornment, help, actions, tabs, width = 'default', testId, children }: PageFrameProps) {
  return (
    <div className={cx('p-4 md:p-6 mx-auto w-full', WIDTH[width])} data-testid={testId}>
      <div className="flex flex-wrap items-center gap-2 mb-6">
        <h1 className="text-xl font-semibold text-fg">{title}</h1>
        {titleAdornment}
        {help && <HelpButton onClick={help.onClick} title={help.title} />}
        {actions && <div className="ml-auto flex gap-2">{actions}</div>}
      </div>
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
      {children}
    </div>
  )
}
