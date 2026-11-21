import { useEffect, useState } from 'react'
import { api, type RunVerification } from '../../api/client'
import { wsClient } from '../../api/ws'
import type { BadgeTone } from '../ui'

export const STATUS_STYLE: Record<RunVerification['status'], { label: string; cls: string }> = {
  pass: { label: 'Verification passed', cls: 'bg-emerald-900/50 text-emerald-300 border-emerald-700' },
  fail: { label: 'Verification failed', cls: 'bg-red-900/50 text-red-300 border-red-700' },
  timeout: { label: 'Verification timed out', cls: 'bg-red-900/50 text-red-300 border-red-700' },
  error: { label: 'Verification could not run', cls: 'bg-amber-900/50 text-amber-300 border-amber-700' },
}

// Semantic Badge tone per verification status (used by the migrated ReviewBriefCard).
export const VERIFICATION_TONE: Record<RunVerification['status'], BadgeTone> = {
  pass: 'success',
  fail: 'danger',
  timeout: 'danger',
  error: 'warning',
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

// VerificationPanel shows the latest result of the repo's verify command for
// this task (see docs/workflows.md#verify-commands), beside the diff so
// Approve is an informed click. Renders nothing when no verify command has
// run. `refreshKey` bumps on the task.verification WS event.
export default function VerificationPanel({ taskId, refreshKey = 0 }: { taskId: string | undefined; refreshKey?: number }) {
  const [latest, setLatest] = useState<RunVerification | null>(null)
  // True between the running task.verification event and the final status.
  const [verifying, setVerifying] = useState(false)

  useEffect(() => {
    if (!taskId) return
    return wsClient.on((event) => {
      if (event.type === 'task.verification' && event.payload.task_id === taskId) {
        setVerifying(event.payload.status === 'running')
      } else if (event.type === 'task.agent_done' && event.payload.task_id === taskId) {
        setVerifying(false)
      }
    })
  }, [taskId])

  useEffect(() => {
    if (!taskId) return
    let cancelled = false
    api.tasks
      .verifications(taskId)
      .then((v) => {
        if (!cancelled) setLatest(v && v.length > 0 ? v[0] : null)
      })
      .catch(() => {
        if (!cancelled) setLatest(null)
      })
    return () => {
      cancelled = true
    }
  }, [taskId, refreshKey])

  const verifyingRow = verifying ? (
    <div className="mb-3 flex items-center gap-2 rounded border border-amber-700 bg-amber-900/30 p-3 text-xs text-amber-300" data-testid="verification-running">
      <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" />
      Verifying\u2026 running the repo verify command
    </div>
  ) : null
  if (!latest) return verifyingRow
  const style = STATUS_STYLE[latest.status]
  return (
    <>
      {verifyingRow}
    <div className="mb-3 rounded border border-slate-800 bg-slate-900/60 p-3" data-testid="verification-panel">
      <div className="flex flex-wrap items-center gap-2">
        <span className={`px-2 py-0.5 text-xs font-medium rounded border ${style.cls}`} data-testid="verification-badge">
          {style.label}
        </span>
        <code className="text-xs text-slate-300 font-mono break-all">{latest.command}</code>
        <span className="text-xs text-slate-500">
          {latest.exit_code != null ? `exit ${latest.exit_code} · ` : ''}
          {latest.reused_from_run_id ? `reused from run ${latest.reused_from_run_id.slice(0, 8)}` : formatDuration(latest.duration_ms)}
        </span>
      </div>
      <details className="mt-2">
        <summary className="cursor-pointer text-xs text-slate-400 hover:text-slate-200">Output</summary>
        {latest.truncated && <p className="mt-1 text-xs text-slate-500">Earlier output truncated; showing the tail.</p>}
        <pre className="mt-1 max-h-72 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-300 whitespace-pre-wrap break-words">
          {latest.output || '(no output)'}
        </pre>
      </details>
    </div>
    </>
  )
}
