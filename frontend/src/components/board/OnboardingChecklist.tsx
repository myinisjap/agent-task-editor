import { Check, Circle } from 'lucide-react'
import Icon from '../ui/Icon'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useHealthStore } from '../../stores/health'
import { visibleIssues } from '../../lib/healthTiers'
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
  const checks = useHealthStore((s) => s.checks)
  const snoozed = useHealthStore((s) => s.snoozed)
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
      useHealthStore.getState().fetch(),
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

  const steps: Step[] = [
    {
      key: 'repo',
      label: 'Add a repo',
      complete: setup.hasRepo,
      to: '/repos',
      linkLabel: 'Add a repo →',
    },
    {
      key: 'provider',
      label: 'Configure a provider',
      complete: setup.hasProvider,
      to: '/settings/providers',
      linkLabel: 'Configure a provider →',
    },
    {
      key: 'agent',
      label: 'Staff the default workflow',
      complete: setup.staffed,
      to: '/pipeline/agents',
      linkLabel: 'Set up agents manually →',
    },
    {
      key: 'task',
      label: 'Create your first task',
      complete: setup.hasTask,
      to: '/board',
      linkLabel: 'Use "+ Add task" below →',
    },
  ]

  const allComplete = steps.every((s) => s.complete)

  // Health problems are surfaced app-wide (StatusBanner / Settings dot), so
  // hiding the completed checklist no longer hides a failing check.
  if (dismissed || !initialLoadDone || !tasksLoaded || allComplete) return null

  const issueCount = visibleIssues(checks, snoozed).length
  const firstIncompleteKey = steps.find((s) => !s.complete)?.key

  return (
    <div className="mb-4 border border-border bg-surface-sunken/60 rounded-lg p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-fg">Get started</h2>
        <button
          onClick={dismiss}
          className="text-xs text-fg-subtle hover:text-fg-muted transition-colors"
        >
          Dismiss
        </button>
      </div>
      <ul className="space-y-2">
        {steps.map((step) => {
          const isNext = step.key === firstIncompleteKey
          return (
            <li key={step.key} className="text-sm">
              <div className="flex items-center gap-2">
                <span className={step.complete ? 'text-green-400' : 'text-fg-subtle'}>
                  <Icon icon={step.complete ? Check : Circle} size="sm" />
                </span>
                <span className={step.complete ? 'text-fg-subtle line-through' : isNext ? 'text-fg font-medium' : 'text-fg-muted'}>
                  {step.label}
                </span>
                {!step.complete && (
                  <Link to={step.to} className="text-xs text-accent-fg hover:text-accent-fg">
                    {step.linkLabel}
                  </Link>
                )}
              </div>
              {step.key === 'agent' && !step.complete && (
                <StaffAgentsButton missingLabels={setup.missingAgentLabels} />
              )}
            </li>
          )
        })}
      </ul>
      {issueCount > 0 && (
        <p data-testid="onboarding-health-summary" className="mt-3 text-xs text-fg-muted">
          {issueCount} setup {issueCount === 1 ? 'issue' : 'issues'} ·{' '}
          <Link to="/settings/health" className="text-accent-fg hover:text-accent-fg underline">
            View health
          </Link>
        </p>
      )}
    </div>
  )
}
