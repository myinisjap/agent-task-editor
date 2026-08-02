import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ProviderCheck } from '../../api/client'
import { useReposStore } from '../../stores/repos'
import { useTasksStore } from '../../stores/tasks'
import { useProviderConfigsStore } from '../../stores/providerConfigs'
import { useAgentsStore } from '../../stores/agents'

// DISMISSED_STORAGE_KEY follows the CONDENSED_STORAGE_KEY naming
// convention used elsewhere on the board (see BoardPage.tsx).
const DISMISSED_STORAGE_KEY = 'board.onboarding.dismissed'

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

function readDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISSED_STORAGE_KEY) === 'true'
  } catch {
    return false
  }
}

export default function OnboardingChecklist() {
  const { repos, fetch: fetchRepos } = useReposStore()
  const { tasks, fetch: fetchTasks } = useTasksStore()
  const { configs: providerConfigs, fetch: fetchProviderConfigs } = useProviderConfigsStore()
  const { configs: agentConfigs, fetch: fetchAgentConfigs } = useAgentsStore()
  const [checks, setChecks] = useState<ProviderCheck[]>([])
  const [dismissed, setDismissed] = useState<boolean>(readDismissed)
  // All store data starts out empty until the first fetch resolves, which
  // would otherwise make every step look incomplete for a frame on each
  // reload/login — showing the checklist in a flash even when onboarding
  // is already done. Gate rendering on the initial load finishing so we
  // only ever show real state, never the empty-store placeholder state.
  const [initialLoadDone, setInitialLoadDone] = useState(false)
  const [tasksLoadDone, setTasksLoadDone] = useState(false)
  // Guards against overlapping/duplicate refreshes when several `focus`
  // events fire in quick succession (e.g. alt-tabbing repeatedly).
  const refreshing = useRef(false)

  const refresh = () => {
    if (refreshing.current) return
    refreshing.current = true
    Promise.allSettled([
      fetchRepos(),
      fetchProviderConfigs(),
      fetchAgentConfigs(),
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

  useEffect(() => {
    // Tasks are already kept fresh by BoardPage's own fetch + WS
    // subscription; make sure at least one fetch has happened so a
    // checklist rendered before BoardPage's effect runs still has data.
    if (tasks.length === 0) fetchTasks().finally(() => setTasksLoadDone(true))
    else setTasksLoadDone(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const dismiss = () => {
    try {
      localStorage.setItem(DISMISSED_STORAGE_KEY, 'true')
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
      complete: repos.length > 0,
      to: '/repos',
      linkLabel: 'Add a repo →',
      checkIds: ['repo_base_dir', 'gh_auth'],
    },
    {
      key: 'provider',
      label: 'Configure a provider',
      complete: providerConfigs.length > 0,
      to: '/providers',
      linkLabel: 'Configure a provider →',
      checkIds: ['claude_cli', 'mcp_sidecar'],
    },
    {
      key: 'agent',
      label: 'Create an agent config',
      complete: agentConfigs.some((c) => c.enabled),
      to: '/agents',
      linkLabel: 'Create an agent config →',
      checkIds: [],
    },
    {
      key: 'task',
      label: 'Create your first task',
      complete: tasks.length > 0,
      to: '/board',
      linkLabel: 'Use "+ Add task" below →',
      checkIds: [],
    },
  ]

  const allComplete = steps.every((s) => s.complete)

  if (dismissed || !initialLoadDone || !tasksLoadDone || allComplete) return null

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
