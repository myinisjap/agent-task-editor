import { useCallback, useState } from 'react'
import { useReposStore } from '../stores/repos'
import { useTasksStore } from '../stores/tasks'
import { useProviderConfigsStore } from '../stores/providerConfigs'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'
import { computeSetup, type SetupState } from './setupStatus'

/**
 * Store-backed setup status shared by OnboardingChecklist and HomeRoute.
 * `refresh()` loads repos/providers/agents/workflows (and tasks when
 * `includeTasks`; BoardPage owns the task fetch for the checklist, see #341).
 */
export function useSetupStatus(): SetupState & {
  refresh: (opts?: { includeTasks?: boolean }) => Promise<void>
  loaded: boolean
} {
  const repos = useReposStore((s) => s.repos)
  const fetchRepos = useReposStore((s) => s.fetch)
  const tasks = useTasksStore((s) => s.tasks)
  const fetchTasks = useTasksStore((s) => s.fetch)
  const providers = useProviderConfigsStore((s) => s.configs)
  const fetchProviders = useProviderConfigsStore((s) => s.fetch)
  const agents = useAgentsStore((s) => s.configs)
  const fetchAgents = useAgentsStore((s) => s.fetch)
  const workflows = useWorkflowStore((s) => s.workflows)
  const fetchWorkflows = useWorkflowStore((s) => s.fetch)
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(
    async (opts?: { includeTasks?: boolean }) => {
      await Promise.allSettled([
        fetchRepos(),
        fetchProviders(),
        fetchAgents(),
        fetchWorkflows(),
        ...(opts?.includeTasks ? [fetchTasks()] : []),
      ])
      setLoaded(true)
    },
    [fetchRepos, fetchProviders, fetchAgents, fetchWorkflows, fetchTasks],
  )

  const state = computeSetup({
    repoCount: repos.length,
    providerCount: providers.length,
    taskCount: tasks.length,
    workflows,
    agents,
  })
  return { ...state, refresh, loaded }
}
