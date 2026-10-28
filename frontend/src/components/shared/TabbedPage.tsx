import { NavLink, Outlet } from 'react-router-dom'

export interface TabDef {
  to: string
  label: string
  /** Optional status dot (e.g. failing health check). */
  badge?: { severity: 'error' | 'warn'; label: string; count?: number } | null
}

/**
 * Shared shell for sections that fold several existing pages into URL-backed
 * tabs (Settings, Pipeline). Each tab is a nested route rendered via <Outlet>,
 * so the page components themselves are unchanged.
 */
export default function TabbedPage({ testId, tabs }: { testId: string; tabs: TabDef[] }) {
  return (
    <div data-testid={testId} className="flex flex-col h-full">
      <nav
        aria-label="Section tabs"
        className="flex shrink-0 gap-1 overflow-x-auto border-b border-border bg-surface-raised px-4 pt-2 md:px-6"
      >
        {tabs.map(({ to, label, badge }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `shrink-0 whitespace-nowrap px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
                isActive
                  ? 'border-accent text-accent-fg'
                  : 'border-transparent text-fg-muted hover:text-fg'
              }`
            }
          >
            {label}
            {badge && (badge.count && badge.count > 0 ? (
              <span
                role="img"
                aria-label={badge.label}
                className={`ml-2 inline-block min-w-[1.25rem] rounded-full px-1 text-center text-[10px] font-semibold leading-4 align-middle ${badge.severity === 'error' ? 'bg-danger-solid text-on-accent' : 'bg-warning-soft text-warning'}`}
              >
                {badge.count}
              </span>
            ) : (
              <span
                role="img"
                aria-label={badge.label}
                className={`ml-2 inline-block w-2 h-2 rounded-full align-middle ${badge.severity === 'error' ? 'bg-danger' : 'bg-warning'}`}
              />
            ))}
          </NavLink>
        ))}
      </nav>
      <div className="flex-1 min-h-0 flex flex-col overflow-auto">
        <Outlet />
      </div>
    </div>
  )
}
