import { NavLink } from 'react-router-dom'
import { House, Menu, MessageSquare, SquareKanban } from 'lucide-react'
import { useInboxCount } from '../../stores/inbox'
import { Badge, Icon } from '../ui'

interface BottomTabBarProps {
  onMore: () => void
}

const tabClassName = ({ isActive }: { isActive: boolean }) =>
  `flex-1 min-h-[3.5rem] min-w-[44px] flex flex-col items-center justify-center gap-0.5 text-xs font-medium transition-colors ${
    isActive ? 'text-fg bg-surface-sunken' : 'text-fg-muted hover:text-fg'
  }`

/** Phone-only fixed bottom navigation. Hidden at md and up (the sidebar takes over). */
export default function BottomTabBar({ onMore }: BottomTabBarProps) {
  const inboxCount = useInboxCount()
  return (
    <nav
      aria-label="Primary"
      className="fixed bottom-0 inset-x-0 z-30 md:hidden flex bg-surface-raised border-t border-border pb-[env(safe-area-inset-bottom)]"
    >
      <NavLink to="/" end className={tabClassName}>
        <Icon icon={House} size="lg" />
        <span className="relative">
          Home
          {inboxCount > 0 && (
            <Badge
              tone="accent"
              variant="solid"
              size="xs"
              className="absolute -top-2 left-full ml-1 justify-center min-w-[1.25rem] rounded-full font-semibold"
              aria-label={`${inboxCount} items waiting on you`}
            >
              {inboxCount}
            </Badge>
          )}
        </span>
      </NavLink>
      <NavLink to="/board" className={tabClassName}>
        <Icon icon={SquareKanban} size="lg" />
        Board
      </NavLink>
      <NavLink to="/chat" className={tabClassName}>
        <Icon icon={MessageSquare} size="lg" />
        Chat
      </NavLink>
      <button type="button" onClick={onMore} aria-label="More" className={tabClassName({ isActive: false })}>
        <Icon icon={Menu} size="lg" />
        More
      </button>
    </nav>
  )
}
