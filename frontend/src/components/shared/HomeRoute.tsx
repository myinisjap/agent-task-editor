import { useEffect, type ReactNode, useState } from 'react'
import { Navigate } from 'react-router-dom'
import { useSetupStatus } from '../../lib/useSetupStatus'
import { readOnboardingDismissed } from '../../lib/setupStatus'
import { useTasksStore } from '../../stores/tasks'

// Index route: while setup is incomplete (and the checklist has not been
// dismissed) land on the Board, where the Get Started checklist lives;
// otherwise show the Dashboard as before.
export default function HomeRoute({ children }: { children: ReactNode }) {
  const { complete, refresh } = useSetupStatus()
  const tasksLoaded = useTasksStore((s) => s.loaded)
  const [ready, setReady] = useState(false)

  useEffect(() => {
    refresh({ includeTasks: true }).finally(() => setReady(true))
  }, [refresh])

  if (!ready) return <div className="p-6 text-slate-400 text-sm">Loading…</div>
  // If the task list failed to load we can't tell; don't trap the user.
  if (!tasksLoaded || complete || readOnboardingDismissed()) return <>{children}</>
  return <Navigate to="/board" replace />
}
