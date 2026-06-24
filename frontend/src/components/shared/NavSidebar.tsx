import { NavLink } from 'react-router-dom'

const links = [
  { to: '/',          label: 'Dashboard' },
  { to: '/board',     label: 'Board' },
  { to: '/workflow',  label: 'Workflow' },
  { to: '/agents',    label: 'Agents' },
  { to: '/repos',     label: 'Repos' },
]

export default function NavSidebar() {
  return (
    <aside className="w-52 shrink-0 bg-slate-900 border-r border-slate-700 flex flex-col p-4 gap-1">
      <div className="text-slate-100 font-bold text-lg mb-6 px-2">AgentEditor</div>
      {links.map(({ to, label }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
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
  )
}
