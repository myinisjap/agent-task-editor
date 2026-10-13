import { NavLink, Outlet } from 'react-router-dom'

export interface TabDef {
  to: string
  label: string
  /** Optional status dot (e.g. failing health check). */
  badge?: { severity: 'error' | 'warn'; label: string } | null
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
        className="flex shrink-0 gap-1 overflow-x-auto border-b border-slate-700 bg-slate-900 px-4 pt-2 md:px-6"
      >
        {tabs.map(({ to, label, badge }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `shrink-0 whitespace-nowrap px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
                isActive
                  ? 'border-indigo-400 text-indigo-400'
                  : 'border-transparent text-slate-400 hover:text-slate-100'
              }`
            }
          >
            {label}
            {badge && (
              <span
                role="img"
                aria-label={badge.label}
                className={`ml-2 inline-block w-2 h-2 rounded-full align-middle ${badge.severity === 'error' ? 'bg-red-500' : 'bg-amber-400'}`}
              />
            )}
          </NavLink>
        ))}
      </nav>
      <div className="flex-1 min-h-0 flex flex-col overflow-auto">
        <Outlet />
      </div>
    </div>
  )
}
