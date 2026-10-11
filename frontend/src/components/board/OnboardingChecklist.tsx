import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ProviderCheck } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useSetupStatus } from '../../lib/useSetupStatus'
import { ONBOARDING_DISMISSED_KEY, readOnboardingDismissed } from '../../lib/setupStatus'
import StaffAgentsButton from './StaffAgentsButton'

type Step = {
  key: string
  label: string
  complete: boolean
  to: string
  linkLabel: string
  checkIds: string[]
}

const STATUS_STYLES: Record<ProviderCheck['status'], string> = {
  ok: '',
  warn: 'bg-yellow-900/40 border-yellow-700 text-yellow-200',
  error: 'bg-red-900/40 border-red-700 text-red-200',
}

export default function OnboardingChecklist() {
  // Tasks come from BoardPage's own fetch + WS subscription — this
  // component is always rendered as a BoardPage child, so it never needs
  // (and must not run) its own competing fetchTasks() sweep; doing so raced
  // BoardPage's own fetch on every mount (#341). Read via a selector rather
  // than destructuring the whole store so this component doesn't re-render
  // on unrelated store field changes (loading/reqId/etc.).
  const tasksLoaded = useTasksStore((s) => s.loaded)
  const setup = useSetupStatus()
  const [checks, setChecks] = useState<ProviderCheck[]>([])
  const [dismissed, setDismissed] = useState<boolean>(readOnboardingDismissed)
  // All store data starts out empty until the first fetch resolves, which
  // would otherwise make every step look incomplete for a frame on each
  // reload/login — showing the checklist in a flash even when onboarding
  // is already done. Gate rendering on the initial load finishing so we
  // only ever show real state, never the empty-store placeholder state.
  const [initialLoadDone, setInitialLoadDone] = useState(false)
  // Guards against overlapping/duplicate refreshes when several `focus`
  // events fire in quick succession (e.g. alt-tabbing repeatedly).
  const refreshing = useRef(false)

  const refresh = () => {
    if (refreshing.current) return
    refreshing.current = true
    Promise.allSettled([
      setup.refresh(),
      api.health.providers().then((r) => setChecks(r.checks ?? [])).catch(() => setChecks([])),
    ]).finally(() => {
      refreshing.current = false
      setInitialLoadDone(true)
    })
  }

  useEffect(() => {
    refresh()
    // There is no WS event for repo/provider-config/agent-config changes
    // (only task.* events exist), and completing those steps requires
    // navigating away from the board and back — so re-fetch on window
    // focus to reliably pick up changes made on another page without
    // requiring a manual refresh.
    const onFocus = () => refresh()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const dismiss = () => {
    try {
      localStorage.setItem(ONBOARDING_DISMISSED_KEY, 'true')
    } catch {
      // ignore storage errors
    }
    setDismissed(true)
  }

  const checksFor = (ids: string[]) => checks.filter((c) => ids.includes(c.id) && c.status !== 'ok')

  const steps: Step[] = [
    {
      key: 'repo',
      label: 'Add a repo',
      complete: setup.hasRepo,
      to: '/repos',
      linkLabel: 'Add a repo →',
      checkIds: ['repo_base_dir', 'gh_auth'],
    },
    {
      key: 'provider',
      label: 'Configure a provider',
      complete: setup.hasProvider,
      to: '/providers',
      linkLabel: 'Configure a provider →',
      checkIds: ['claude_cli', 'mcp_sidecar'],
    },
    {
      key: 'agent',
      label: 'Staff the default workflow',
      complete: setup.staffed,
      to: '/agents',
      linkLabel: 'Set up agents manually →',
      checkIds: [],
    },
    {
      key: 'task',
      label: 'Create your first task',
      complete: setup.hasTask,
      to: '/board',
      linkLabel: 'Use "+ Add task" below →',
      checkIds: [],
    },
  ]

  const allComplete = steps.every((s) => s.complete)

  // Health problems are surfaced app-wide by HealthWarningBanner, so hiding
  // the completed checklist no longer hides a failing check.
  if (dismissed || !initialLoadDone || !tasksLoaded || allComplete) return null

  const firstIncompleteKey = steps.find((s) => !s.complete)?.key

  return (
    <div className="mb-4 border border-slate-700 bg-slate-800/60 rounded-lg p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-slate-200">Get started</h2>
        <button
          onClick={dismiss}
          className="text-xs text-slate-500 hover:text-slate-300 transition-colors"
        >
          Dismiss
        </button>
      </div>
      <ul className="space-y-2">
        {steps.map((step) => {
          const relevantChecks = checksFor(step.checkIds)
          const isNext = step.key === firstIncompleteKey
          return (
            <li key={step.key} className="text-sm">
              <div className="flex items-center gap-2">
                <span className={step.complete ? 'text-green-400' : 'text-slate-600'}>
                  {step.complete ? '✓' : '○'}
                </span>
                <span className={step.complete ? 'text-slate-500 line-through' : isNext ? 'text-slate-100 font-medium' : 'text-slate-300'}>
                  {step.label}
                </span>
                {!step.complete && (
                  <Link to={step.to} className="text-xs text-indigo-400 hover:text-indigo-300">
                    {step.linkLabel}
                  </Link>
                )}
              </div>
              {step.key === 'agent' && !step.complete && (
                <StaffAgentsButton missingLabels={setup.missingAgentLabels} />
              )}
              {relevantChecks.length > 0 && (
                <div className="mt-1 ml-6 space-y-1">
                  {relevantChecks.map((check) => (
                    <div
                      key={check.id}
                      className={`text-xs border rounded px-2 py-1 ${STATUS_STYLES[check.status]}`}
                    >
                      <span className="font-medium">{check.name}: </span>
                      <span>{check.detail}</span>
                      {check.hint && <span className="opacity-80"> — {check.hint}</span>}
                    </div>
                  ))}
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
