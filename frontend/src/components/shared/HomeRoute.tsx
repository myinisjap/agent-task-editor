import { useEffect, type ReactNode, useState } from 'react'
import { Navigate, useSearchParams } from 'react-router-dom'
import { useSetupStatus } from '../../lib/useSetupStatus'
import { readOnboardingDismissed } from '../../lib/setupStatus'
import { useTasksStore } from '../../stores/tasks'

// Index route: on a first run (no repo, provider or task, and the checklist has not been
// dismissed) land on the Board, where the Get Started checklist lives;
// otherwise show the Home control room.
export default function HomeRoute({ children }: { children: ReactNode }) {
  const { hasRepo, hasProvider, hasTask, refresh } = useSetupStatus()
  const tasksLoaded = useTasksStore((s) => s.loaded)
  const [ready, setReady] = useState(false)
  const [params] = useSearchParams()

  useEffect(() => {
    refresh({ includeTasks: true }).finally(() => setReady(true))
  }, [refresh])

  if (!ready) return <div className="p-6 text-slate-400 text-sm">Loading…</div>
  // If the task list failed to load we can't tell; don't trap the user.
  // Deep links into the review popup (/?task=…, /?review=1, via the /inbox redirect) stay on Home.
  if (params.has('task') || params.has('review')) return <>{children}</>
  // A staffing gap alone must not hijack Home on an established install.
  const firstRun = !hasRepo || !hasProvider || !hasTask
  if (!tasksLoaded || !firstRun || readOnboardingDismissed()) return <>{children}</>
  return <Navigate to="/board" replace />
}
