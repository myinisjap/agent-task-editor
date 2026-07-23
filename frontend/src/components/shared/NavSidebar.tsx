import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { useThemeStore } from '../../stores/theme'
import { useNotificationsStore, notificationsSupported } from '../../stores/notifications'

interface NavGroup {
  key: string
  label: string
  links: { to: string; label: string }[]
}

// Dashboard is the home route and stays reachable as a standalone top-level
// link above the collapsible groups; the remaining 10 destinations are
// organized into logical categories below.
const topLevelLink = { to: '/', label: 'Dashboard' }

const groups: NavGroup[] = [
  {
    key: 'insights',
    label: 'Insights',
    links: [
      { to: '/dashboard/usage', label: 'Cost & Usage' },
      { to: '/dashboard/performance', label: 'Performance' },
    ],
  },
  {
    key: 'work',
    label: 'Work',
    links: [
      { to: '/board', label: 'Board' },
      { to: '/chat', label: 'Chat' },
    ],
  },
  {
    key: 'configuration',
    label: 'Configuration',
    links: [
      { to: '/workflow', label: 'Workflow' },
      { to: '/agents', label: 'Agents' },
      { to: '/providers', label: 'Providers' },
      { to: '/repos', label: 'Repos' },
      { to: '/templates', label: 'Templates' },
    ],
  },
  {
    key: 'system',
    label: 'System',
    links: [{ to: '/health', label: 'Health' }],
  },
]

const STORAGE_KEY = 'nav-open-groups'

/** Which group (if any) contains the given pathname. */
function groupForPath(pathname: string): string | null {
  const group = groups.find((g) => g.links.some((l) => l.to !== '/' && pathname.startsWith(l.to)))
  return group?.key ?? null
}

function loadOpenGroups(activeKey: string | null): Record<string, boolean> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const parsed = JSON.parse(stored) as Record<string, boolean>
      if (activeKey) parsed[activeKey] = true
      return parsed
    }
  } catch { /* ignore */ }
  const initial: Record<string, boolean> = {}
  if (activeKey) initial[activeKey] = true
  return initial
}

export default function NavSidebar() {
  const [isOpen, setIsOpen] = useState(false)
  const theme = useThemeStore((s) => s.theme)
  const toggleTheme = useThemeStore((s) => s.toggle)
  const notificationsEnabled = useNotificationsStore((s) => s.enabled)
  const notificationsPermission = useNotificationsStore((s) => s.permission)
  const toggleNotifications = useNotificationsStore((s) => s.toggle)
  const location = useLocation()
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>(() =>
    loadOpenGroups(groupForPath(location.pathname)),
  )

  const toggleGroup = (key: string) => {
    setOpenGroups((prev) => {
      const next = { ...prev, [key]: !prev[key] }
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) } catch { /* ignore */ }
      return next
    })
  }

  const linkClassName = ({ isActive }: { isActive: boolean }) =>
    `px-3 py-2 rounded-md text-sm font-medium transition-colors ${
      isActive
        ? 'bg-slate-700 text-white'
        : 'text-slate-400 hover:text-slate-100 hover:bg-slate-800'
    }`

  return (
    <>
      {/* Hamburger button — only visible on mobile, fixed top-left */}
      <button
        onClick={() => setIsOpen(true)}
        aria-label="Open menu"
        className="fixed top-3 left-3 z-50 md:hidden p-2 rounded bg-slate-800 text-slate-200 shadow-lg"
      >
        ☰
      </button>

      {/* Backdrop — only on mobile when sidebar is open */}
      {isOpen && (
        <div
          className="fixed inset-0 bg-black/50 z-30 md:hidden"
          onClick={() => setIsOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside
        className={`
          flex flex-col bg-slate-900 border-r border-slate-700 p-4 gap-1 z-40
          fixed inset-y-0 left-0 w-52 transition-transform duration-200 ease-in-out
          md:relative md:translate-x-0 md:shrink-0
          ${isOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}
        `}
      >
        {/* Header row with title and mobile close button */}
        <div className="flex items-center justify-between mb-6 px-2">
          <div className="text-slate-100 font-bold text-lg">AgentEditor</div>
          <button
            onClick={() => setIsOpen(false)}
            aria-label="Close menu"
            className="md:hidden text-slate-400 hover:text-slate-100 p-1 rounded"
          >
            ✕
          </button>
        </div>

        {/* Scrollable nav-links region — keeps the theme toggle pinned at the
            bottom via mt-auto even when expanded groups overflow a short
            viewport. */}
        <div className="flex flex-col gap-1 overflow-y-auto min-h-0">
          <NavLink
            to={topLevelLink.to}
            end
            onClick={() => setIsOpen(false)}
            className={linkClassName}
          >
            {topLevelLink.label}
          </NavLink>

          {groups.map((group) => {
            const isGroupOpen = !!openGroups[group.key]
            const panelId = `nav-group-${group.key}`
            return (
              <div key={group.key} className="flex flex-col">
                <button
                  type="button"
                  onClick={() => toggleGroup(group.key)}
                  aria-expanded={isGroupOpen}
                  aria-controls={panelId}
                  className="flex items-center justify-between px-3 py-2 rounded-md text-xs font-semibold uppercase tracking-wide text-slate-500 hover:text-slate-200 hover:bg-slate-800 transition-colors"
                >
                  <span>{group.label}</span>
                  <span
                    aria-hidden="true"
                    className={`transition-transform duration-150 ${isGroupOpen ? 'rotate-90' : ''}`}
                  >
                    ▸
                  </span>
                </button>
                {isGroupOpen && (
                  <div id={panelId} className="flex flex-col gap-1 pl-1">
                    {group.links.map(({ to, label }) => (
                      <NavLink
                        key={to}
                        to={to}
                        onClick={() => setIsOpen(false)}
                        className={linkClassName}
                      >
                        {label}
                      </NavLink>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </div>

        {notificationsSupported() && (
          <button
            onClick={toggleNotifications}
            disabled={notificationsPermission === 'denied'}
            aria-label={notificationsEnabled ? 'Disable notifications' : 'Enable notifications'}
            title={
              notificationsPermission === 'denied'
                ? 'Notifications are blocked for this site — re-enable them in your browser settings'
                : notificationsEnabled
                  ? 'Disable notifications'
                  : 'Get notified in your browser when a task needs a human'
            }
            className="mt-auto flex items-center gap-2 px-3 py-2 rounded-md text-sm font-medium text-slate-400 hover:text-slate-100 hover:bg-slate-800 transition-colors shrink-0 disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-transparent disabled:hover:text-slate-400"
          >
            <span aria-hidden="true">{notificationsEnabled ? '🔔' : '🔕'}</span>
            <span>
              {notificationsPermission === 'denied'
                ? 'Notifications blocked'
                : notificationsEnabled
                  ? 'Notifications on'
                  : 'Enable notifications'}
            </span>
          </button>
        )}

        <button
          onClick={toggleTheme}
          aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          title={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          className={`${notificationsSupported() ? '' : 'mt-auto'} flex items-center gap-2 px-3 py-2 rounded-md text-sm font-medium text-slate-400 hover:text-slate-100 hover:bg-slate-800 transition-colors shrink-0`}
        >
          <span aria-hidden="true">{theme === 'dark' ? '☀️' : '🌙'}</span>
          <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>
        </button>
      </aside>
    </>
  )
}
