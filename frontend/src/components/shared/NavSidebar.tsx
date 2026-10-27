import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { useThemeStore } from '../../stores/theme'
import { useNotificationsStore, notificationsUnavailableReason, type NotificationsUnavailableReason } from '../../stores/notifications'
import { useWsStatus } from '../../lib/useWsStatus'
import { useHealthStore, worstHealth } from '../../stores/health'
import type { WSStatus } from '../../api/ws'
import { useNeedsMeSync } from '../../lib/useNeedsMeSync'
import { useInboxSync } from '../../lib/useInboxSync'
import { useInboxCount } from '../../stores/inbox'

interface NavGroup {
  key: string
  label: string
  links: { to: string; label: string; end?: boolean }[]
}

// Primary destinations, always visible. Workflow + Agents live under Pipeline;
// Providers, Pricing, Intake Rules, Templates and Health live under Settings
// (as URL-backed tabs). The Overview dashboard stays reachable but is
// deliberately demoted into the collapsible Insights group.
const inboxLink = { to: '/inbox', label: 'Inbox' }
const primaryLinks = [
  { to: '/board', label: 'Board' },
  { to: '/chat', label: 'Chat' },
  { to: '/pipeline', label: 'Pipeline' },
  { to: '/repos', label: 'Repos' },
  { to: '/settings', label: 'Settings' },
]

const groups: NavGroup[] = [
  {
    key: 'insights',
    label: 'Insights',
    links: [
      { to: '/dashboard', label: 'Overview', end: true },
      { to: '/dashboard/usage', label: 'Cost & Usage' },
      { to: '/dashboard/performance', label: 'Performance' },
    ],
  },
]

const STORAGE_KEY = 'nav-open-groups'

const WS_STATUS_CONFIG: Record<WSStatus, { label: string; dotClassName: string }> = {
  open: { label: 'Live', dotClassName: 'bg-emerald-400' },
  connecting: { label: 'Reconnecting…', dotClassName: 'bg-amber-400 animate-pulse' },
  closed: { label: 'Offline', dotClassName: 'bg-red-400' },
}

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

const NOTIFICATIONS_UNAVAILABLE: Record<NotificationsUnavailableReason, { label: string; title: string }> = {
  insecure: {
    label: 'Notifications need HTTPS',
    title: 'Browsers only allow notifications on pages served over HTTPS (or localhost). Open the app through an HTTPS address to enable them.',
  },
  'ios-install': {
    label: 'Add to Home Screen for notifications',
    title: 'On iPhone and iPad, notifications only work once the app is added to the Home Screen (Share → Add to Home Screen) and opened from there.',
  },
  unsupported: {
    label: 'Notifications unsupported',
    title: 'This browser does not support notifications.',
  },
}

interface NavSidebarProps {
  /** Mobile drawer state, owned by App so the bottom tab bar's More tab can open it. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

export default function NavSidebar({ open = false, onOpenChange }: NavSidebarProps = {}) {
  const isOpen = open
  const setIsOpen = (v: boolean) => onOpenChange?.(v)
  const theme = useThemeStore((s) => s.theme)
  const toggleTheme = useThemeStore((s) => s.toggle)
  const notificationsEnabled = useNotificationsStore((s) => s.enabled)
  const notificationsPermission = useNotificationsStore((s) => s.permission)
  const toggleNotifications = useNotificationsStore((s) => s.toggle)
  const notificationsUnavailable = notificationsUnavailableReason()
  const wsStatus = useWsStatus()
  useNeedsMeSync()
  useInboxSync()
  const inboxCount = useInboxCount()
  const healthSeverity = worstHealth(useHealthStore((s) => s.checks))
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
        ? 'bg-slate-700 text-slate-50'
        : 'text-slate-400 hover:text-slate-100 hover:bg-slate-800'
    }`

  return (
    <>
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
            to={inboxLink.to}
            onClick={() => setIsOpen(false)}
            className={linkClassName}
          >
            {inboxLink.label}
            {inboxCount > 0 && (
              <span
                className="ml-2 inline-flex items-center justify-center min-w-[1.25rem] px-1.5 py-0.5 rounded-full bg-pink-600 text-white text-[10px] font-semibold"
                title={`${inboxCount} item${inboxCount === 1 ? '' : 's'} waiting on you`}
                aria-label={`${inboxCount} items waiting on you`}
              >
                {inboxCount}
              </span>
            )}
          </NavLink>

          {primaryLinks.map(({ to, label }) => (
            <NavLink
              key={to}
              to={to}
              onClick={() => setIsOpen(false)}
              className={linkClassName}
            >
              {label}
              {to === '/settings' && healthSeverity && (
                <span
                  role="img"
                  aria-label={healthSeverity === 'error' ? 'Health check failing' : 'Health check warning'}
                  className={`ml-2 inline-block w-2 h-2 rounded-full align-middle ${healthSeverity === 'error' ? 'bg-red-500' : 'bg-amber-400'}`}
                />
              )}
            </NavLink>
          ))}

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
                  <span>
                    {group.label}
                  </span>
                  <span
                    aria-hidden="true"
                    className={`transition-transform duration-150 ${isGroupOpen ? 'rotate-90' : ''}`}
                  >
                    ▸
                  </span>
                </button>
                {isGroupOpen && (
                  <div id={panelId} className="flex flex-col gap-1 pl-1">
                    {group.links.map(({ to, label, end }) => (
                      <NavLink
                        key={to}
                        to={to}
                        end={end}
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

        <div
          className="flex items-center gap-2 px-3 py-2 text-xs font-medium text-slate-400 shrink-0"
          title={`WebSocket: ${WS_STATUS_CONFIG[wsStatus].label}`}
        >
          <span
            aria-hidden="true"
            className={`inline-block w-2 h-2 rounded-full ${WS_STATUS_CONFIG[wsStatus].dotClassName}`}
          />
          <span>{WS_STATUS_CONFIG[wsStatus].label}</span>
        </div>

        {notificationsUnavailable ? (
          <div
            title={NOTIFICATIONS_UNAVAILABLE[notificationsUnavailable].title}
            className="mt-auto flex items-center gap-2 px-3 py-2 text-sm font-medium text-slate-500 shrink-0"
          >
            <span aria-hidden="true">🔕</span>
            <span>{NOTIFICATIONS_UNAVAILABLE[notificationsUnavailable].label}</span>
          </div>
        ) : (
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
          className="flex items-center gap-2 px-3 py-2 rounded-md text-sm font-medium text-slate-400 hover:text-slate-100 hover:bg-slate-800 transition-colors shrink-0"
        >
          <span aria-hidden="true">{theme === 'dark' ? '☀️' : '🌙'}</span>
          <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>
        </button>
      </aside>
    </>
  )
}
