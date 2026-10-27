import { useEffect, useState } from 'react'
import { api, type ReviewBrief } from '../../api/client'
import RiskFlagChips from '../shared/RiskFlagChips'
import PreviewStrip from './PreviewStrip'
import { STATUS_STYLE, VERIFICATION_TONE } from './VerificationPanel'
import { Badge, Card } from '../ui'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col sm:flex-row sm:gap-3 min-w-0">
      <div className="sm:w-24 shrink-0 text-xs font-semibold uppercase tracking-wide text-fg-subtle">{label}</div>
      <div className="min-w-0 flex-1 break-words text-sm text-fg">{children}</div>
    </div>
  )
}

// ReviewBriefCard is the compact "what changed / risks / verified / agent
// summary" card shown while a task waits on a human (docs/workflows.md#review-brief).
// It renders nothing while loading, on error, or when the task is not at a
// human gate. `refreshKey` changes when the label or verification changes.
export default function ReviewBriefCard({ taskId, refreshKey = '', hidePreview = false, compact = false, filesOpen = false }: { taskId: string | undefined; refreshKey?: string | number; hidePreview?: boolean; compact?: boolean; filesOpen?: boolean }) {
  const [brief, setBrief] = useState<ReviewBrief | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [showFull, setShowFull] = useState(false)

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
  if (compact && !showFull) {
    return (
      <Card padding="sm" className="flex flex-col gap-1.5" data-testid="review-brief">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-xs font-semibold text-fg-muted">Review brief</h3>
          <button type="button" className="text-xs text-fg-muted hover:text-fg" onClick={() => setShowFull(true)}>
            Full brief
          </button>
        </div>
        <Row label="Changed">
          {brief.diff_available ? (
            <span>
              {changed.files} file{changed.files === 1 ? '' : 's'}, +{changed.additions} −{changed.deletions}
            </span>
          ) : (
            <span className="text-fg-subtle">No branch diff yet</span>
          )}
        </Row>
        <Row label="Risks">
          {brief.risk_flags.length === 0 ? (
            <span className="text-fg-subtle">No risk flags</span>
          ) : (
            <RiskFlagChips codes={brief.risk_flags.map((f) => f.code)} reasons={reasons} />
          )}
        </Row>
        <Row label="Verified">
          {verification ? (
            <Badge tone={VERIFICATION_TONE[verification.status as keyof typeof VERIFICATION_TONE] ?? 'neutral'} data-testid="brief-verification-badge">
                {STATUS_STYLE[verification.status as keyof typeof STATUS_STYLE]?.label ?? verification.status}
              </Badge>
          ) : (
            <span className="text-fg-subtle">No verify command ran</span>
          )}
        </Row>
      </Card>
    )
  }
  const clamp = !!summary && summary.text.length > 300 && !expanded

  return (
    <Card padding="sm" className="flex flex-col gap-2" data-testid="review-brief">
      <h3 className="text-xs font-semibold text-fg-muted">Review brief</h3>

      <Row label="Changed">
        {brief.diff_available ? (
          <>
            <span>
              {changed.files} file{changed.files === 1 ? '' : 's'}, +{changed.additions} −{changed.deletions}
            </span>
            {brief.truncated && <span className="ml-2 text-xs text-fg-subtle">(large diff; approximate)</span>}
            {changed.top_files.length > 0 && (
              <details className="mt-1" open={filesOpen || undefined}>
                <summary className="cursor-pointer text-xs text-fg-muted hover:text-fg">Top files</summary>
                <ul className="mt-1 text-xs font-mono">
                  {changed.top_files.map((f) => (
                    <li key={f.path} className="break-all">
                      {f.path} <span className="text-success">+{f.additions}</span> <span className="text-danger">−{f.deletions}</span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </>
        ) : (
          <span className="text-fg-subtle">No branch diff yet</span>
        )}
      </Row>

      <Row label="Risks">
        {brief.risk_flags.length === 0 ? (
          <span className="text-fg-subtle">No risk flags</span>
        ) : (
          <>
            <RiskFlagChips codes={brief.risk_flags.map((f) => f.code)} reasons={reasons} />
            {brief.risk_flags.map((f) => (
              <details key={f.code} className="mt-1">
                <summary className="cursor-pointer text-xs text-fg-muted hover:text-fg break-words">{f.reason}</summary>
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

      {!hidePreview && <PreviewStrip taskId={taskId} refreshKey={refreshKey} label="Preview" />}

      <Row label="Verified">
        <div className="flex flex-wrap items-center gap-2">
          {verification ? (
            <>
              <Badge tone={VERIFICATION_TONE[verification.status as keyof typeof VERIFICATION_TONE] ?? 'neutral'} data-testid="brief-verification-badge">
                {STATUS_STYLE[verification.status as keyof typeof STATUS_STYLE]?.label ?? verification.status}
              </Badge>
              <span className="text-xs text-fg-subtle">{formatDuration(verification.duration_ms)}</span>
            </>
          ) : (
            <span className="text-fg-subtle">No verify command ran</span>
          )}
        </div>
        <div className="text-xs text-fg-muted mt-1">
          runs {runs.total} ({runs.failed} failed) · rework loops {runs.rework_loops} · ${runs.cost_usd.toFixed(2)}
          {runs.cost_unknown ? ' + unknown' : ''}
        </div>
        {verification && verification.output_tail && (
          <details className="mt-1">
            <summary className="cursor-pointer text-xs text-fg-muted hover:text-fg">Output tail</summary>
            <pre className="mt-1 max-h-48 overflow-auto rounded bg-surface p-2 text-xs text-fg whitespace-pre-wrap break-words">
              {verification.output_tail}
            </pre>
          </details>
        )}
      </Row>

      {summary && (
        <Row label="Agent summary">
          <div className="text-[10px] text-fg-subtle mb-1">
            {summary.agent_name || (summary.source === 'stored_info' ? 'agent' : 'task notes')} · {summary.source === 'stored_info' ? 'store_info' : 'notes (latest)'}
          </div>
          <div className={`whitespace-pre-wrap break-words ${clamp ? 'max-h-24 overflow-hidden' : ''}`} data-testid="brief-summary-text">
            {summary.text}
          </div>
          {summary.text.length > 300 && (
            <button className="mt-1 text-xs text-fg-muted hover:text-fg" onClick={() => setExpanded((e) => !e)}>
              {expanded ? 'Show less' : 'Show more'}
            </button>
          )}
        </Row>
      )}
    </Card>
  )
}
