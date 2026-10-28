import { useEffect, useMemo } from 'react'
import { Link } from 'react-router-dom'
import type { Workflow, WorkflowLabel } from '../../api/client'
import { reposWithoutVerify, successTarget } from '../../lib/autonomyDial'
import { useReposStore } from '../../stores/repos'
import { useWorkflowStore } from '../../stores/workflow'

/** Warnings shared by the panel (and mirrored in the sheet). */
export function useGateWarnings(workflow: Workflow, label: WorkflowLabel, mode: string) {
  const repos = useReposStore((s) => s.repos)
  const fetchRepos = useReposStore((s) => s.fetch)
  const workflows = useWorkflowStore((s) => s.workflows)
  useEffect(() => {
    if (repos.length === 0) void fetchRepos()
  }, [repos.length, fetchRepos])
  return useMemo(() => {
    const out: { key: string; node: React.ReactNode }[] = []
    if (mode === 'auto' && successTarget(workflow, label.name)?.create_pr) {
      out.push({ key: 'pr', node: 'PRs will open without anyone reading the diff.' })
    }
    const missing = mode === 'ask' ? [] : reposWithoutVerify(workflow, repos, workflows)
    if (missing.length > 0) {
      out.push({
        key: 'verify',
        node: (
          <>
            No verify command for: {missing.map((r) => r.name).join(', ')}, so &lsquo;verify must pass&rsquo; can&rsquo;t
            stop anything there. <Link to="/repos" className="underline">Set one on Repos</Link>
          </>
        ),
      })
    }
    return out
  }, [mode, workflow, label.name, repos, workflows])
}

