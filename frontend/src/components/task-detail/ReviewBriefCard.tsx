import { useEffect, useState } from 'react'
import { api, type ReviewBrief } from '../../api/client'
import RiskFlagChips from '../shared/RiskFlagChips'
import PreviewStrip from './PreviewStrip'
import { STATUS_STYLE } from './VerificationPanel'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col sm:flex-row sm:gap-3 min-w-0">
      <div className="sm:w-24 shrink-0 text-xs font-semibold uppercase tracking-wide text-slate-500">{label}</div>
      <div className="min-w-0 flex-1 break-words text-sm text-slate-300">{children}</div>
    </div>
  )
}

// ReviewBriefCard is the compact "what changed / risks / verified / agent
// summary" card shown while a task waits on a human (docs/workflows.md#review-brief).
// It renders nothing while loading, on error, or when the task is not at a
// human gate. `refreshKey` changes when the label or verification changes.
export default function ReviewBriefCard({ taskId, refreshKey = '' }: { taskId: string | undefined; refreshKey?: string | number }) {
  const [brief, setBrief] = useState<ReviewBrief | null>(null)
  const [expanded, setExpanded] = useState(false)

  useEffect(() => {
    if (!taskId) return
    let cancelled = false
    api.tasks
      .reviewBrief(taskId)
      .then((b) => {
        if (!cancelled) setBrief(b)
      })
      .catch(() => {
        if (!cancelled) setBrief(null)
      })
    return () => {
      cancelled = true
    }
  }, [taskId, refreshKey])

  if (!brief || !brief.human_gate) return null
  const { changed, runs, verification, agent_summary: summary } = brief
  const reasons = Object.fromEntries(brief.risk_flags.map((f) => [f.code, f.reason]))
  const clamp = !!summary && summary.text.length > 300 && !expanded

  return (
    <div className="rounded border border-slate-800 bg-slate-900/60 p-3 flex flex-col gap-2 min-w-0" data-testid="review-brief">
      <h3 className="text-xs font-semibold text-slate-400">Review brief</h3>

      <Row label="Changed">
        {brief.diff_available ? (
          <>
            <span>
              {changed.files} file{changed.files === 1 ? '' : 's'}, +{changed.additions} −{changed.deletions}
            </span>
            {brief.truncated && <span className="ml-2 text-xs text-slate-500">(large diff; approximate)</span>}
            {changed.top_files.length > 0 && (
              <details className="mt-1">
                <summary className="cursor-pointer text-xs text-slate-400 hover:text-slate-200">Top files</summary>
                <ul className="mt-1 text-xs font-mono">
                  {changed.top_files.map((f) => (
                    <li key={f.path} className="break-all">
                      {f.path} <span className="text-emerald-400">+{f.additions}</span> <span className="text-red-400">−{f.deletions}</span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </>
        ) : (
          <span className="text-slate-500">No branch diff yet</span>
        )}
      </Row>

      <Row label="Risks">
        {brief.risk_flags.length === 0 ? (
          <span className="text-slate-500">No risk flags</span>
        ) : (
          <>
            <RiskFlagChips codes={brief.risk_flags.map((f) => f.code)} reasons={reasons} />
            {brief.risk_flags.map((f) => (
              <details key={f.code} className="mt-1">
                <summary className="cursor-pointer text-xs text-slate-400 hover:text-slate-200 break-words">{f.reason}</summary>
                {f.files.length > 0 && (
                  <ul className="mt-1 text-xs font-mono">
                    {f.files.map((p) => (
                      <li key={p} className="break-all">{p}</li>
                    ))}
                  </ul>
                )}
              </details>
            ))}
          </>
        )}
      </Row>

      <PreviewStrip taskId={taskId} refreshKey={refreshKey} label="Preview" />

      <Row label="Verified">
        <div className="flex flex-wrap items-center gap-2">
          {verification ? (
            <>
              <span
                className={`px-2 py-0.5 text-xs font-medium rounded border ${STATUS_STYLE[verification.status as keyof typeof STATUS_STYLE]?.cls ?? ''}`}
                data-testid="brief-verification-badge"
              >
                {STATUS_STYLE[verification.status as keyof typeof STATUS_STYLE]?.label ?? verification.status}
              </span>
              <span className="text-xs text-slate-500">{formatDuration(verification.duration_ms)}</span>
            </>
          ) : (
            <span className="text-slate-500">No verify command ran</span>
          )}
        </div>
        <div className="text-xs text-slate-400 mt-1">
          runs {runs.total} ({runs.failed} failed) · rework loops {runs.rework_loops} · ${runs.cost_usd.toFixed(2)}
          {runs.cost_unknown ? ' + unknown' : ''}
        </div>
        {verification && verification.output_tail && (
          <details className="mt-1">
            <summary className="cursor-pointer text-xs text-slate-400 hover:text-slate-200">Output tail</summary>
            <pre className="mt-1 max-h-48 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-300 whitespace-pre-wrap break-words">
              {verification.output_tail}
            </pre>
          </details>
        )}
      </Row>

      {summary && (
        <Row label="Agent summary">
          <div className="text-[10px] text-slate-500 mb-1">
            {summary.agent_name || (summary.source === 'stored_info' ? 'agent' : 'task notes')} · {summary.source === 'stored_info' ? 'store_info' : 'notes (latest)'}
          </div>
          <div className={`whitespace-pre-wrap break-words ${clamp ? 'max-h-24 overflow-hidden' : ''}`} data-testid="brief-summary-text">
            {summary.text}
          </div>
          {summary.text.length > 300 && (
            <button className="mt-1 text-xs text-slate-400 hover:text-slate-200" onClick={() => setExpanded((e) => !e)}>
              {expanded ? 'Show less' : 'Show more'}
            </button>
          )}
        </Row>
      )}
    </div>
  )
}
