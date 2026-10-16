import { NavLink } from 'react-router-dom'
import { useInboxCount } from '../../stores/inbox'

interface BottomTabBarProps {
  onMore: () => void
}

const tabClassName = ({ isActive }: { isActive: boolean }) =>
  `flex-1 min-h-[3.5rem] min-w-[44px] flex flex-col items-center justify-center gap-0.5 text-xs font-medium transition-colors ${
    isActive ? 'text-white bg-slate-800' : 'text-slate-400 hover:text-slate-100'
  }`

/** Phone-only fixed bottom navigation. Hidden at md and up (the sidebar takes over). */
export default function BottomTabBar({ onMore }: BottomTabBarProps) {
  const inboxCount = useInboxCount()
  return (
    <nav
      aria-label="Primary"
      className="fixed bottom-0 inset-x-0 z-30 md:hidden flex bg-slate-900 border-t border-slate-700 pb-[env(safe-area-inset-bottom)]"
    >
      <NavLink to="/inbox" className={tabClassName}>
        <span className="relative">
          Inbox
          {inboxCount > 0 && (
            <span
              className="absolute -top-2 left-full ml-1 inline-flex items-center justify-center min-w-[1.25rem] px-1.5 py-0.5 rounded-full bg-pink-600 text-white text-[10px] font-semibold"
              aria-label={`${inboxCount} items waiting on you`}
            >
              {inboxCount}
            </span>
          )}
        </span>
      </NavLink>
      <NavLink to="/board" className={tabClassName}>Board</NavLink>
      <NavLink to="/chat" className={tabClassName}>Chat</NavLink>
      <button type="button" onClick={onMore} aria-label="More" className={tabClassName({ isActive: false })}>
        More
      </button>
    </nav>
  )
}
