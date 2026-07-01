import { useState } from 'react'
import { NavLink } from 'react-router-dom'

const links = [
  { to: '/',          label: 'Dashboard' },
  { to: '/board',     label: 'Board' },
  { to: '/workflow',  label: 'Workflow' },
  { to: '/agents',    label: 'Agents' },
  { to: '/repos',     label: 'Repos' },
]

export default function NavSidebar() {
  const [isOpen, setIsOpen] = useState(false)

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

        {links.map(({ to, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            onClick={() => setIsOpen(false)}
            className={({ isActive }) =>
              `px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                isActive
                  ? 'bg-slate-700 text-white'
                  : 'text-slate-400 hover:text-slate-100 hover:bg-slate-800'
              }`
            }
          >
            {label}
          </NavLink>
        ))}
      </aside>
    </>
  )
}
