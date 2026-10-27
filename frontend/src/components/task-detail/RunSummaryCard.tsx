import { formatDuration } from '../../lib/format'
import type { RunSummary } from '../../lib/runTimeline'

const DOT: Record<RunSummary['outcome']['tone'], string> = {
  success: 'bg-green-500',
  failed: 'bg-red-500',
  waiting: 'bg-amber-500',
  running: 'bg-yellow-400 animate-pulse',
  cancelled: 'bg-slate-500',
  interrupted: 'bg-slate-500',
  pending: 'bg-slate-500',
}

const CHIP = 'px-2 py-0.5 rounded-full bg-slate-800 border border-slate-700 text-slate-300'

export default function RunSummaryCard({ summary, partial }: { summary: RunSummary; partial?: boolean }) {
  const { outcome: o } = summary
  const hint = partial ? ' (loaded events)' : ''
  return (
    <div className="mx-2 mb-2 rounded border border-slate-800 bg-slate-900/60 p-3 flex flex-col gap-2 shrink-0" data-testid="run-summary">
      <div className="flex items-start gap-2">
        <span className={`mt-1 inline-block w-2.5 h-2.5 rounded-full shrink-0 ${DOT[o.tone]}`} aria-hidden="true" />
        <div className="min-w-0">
          <div className={`text-sm font-semibold ${o.tone === 'failed' ? 'text-red-300' : 'text-slate-200'}`}>{o.text}</div>
          {o.why && <div className="text-xs text-slate-400 break-words">{o.why}</div>}
        </div>
      </div>
      <div className="flex flex-wrap gap-1.5 text-[11px]">
        {summary.agentName && <span className={CHIP}>{summary.agentName}</span>}
        {summary.providerModel && <span className={CHIP}>{summary.providerModel}</span>}
        <span className={CHIP} title="Duration">{summary.durationSecs != null ? formatDuration(summary.durationSecs) : '—'}</span>
        <span className={CHIP} title="Cost">
          {summary.costUnknown ? '—' : summary.costUsd != null ? `$${summary.costUsd.toFixed(2)}` : '—'}
        </span>
        <span className={CHIP} title={summary.filesChanged.join('\n') || undefined}>
          {summary.filesChanged.length} {summary.filesChanged.length === 1 ? 'file' : 'files'} changed{hint}
        </span>
        <span className={CHIP}>
          {summary.commands.total} {summary.commands.total === 1 ? 'command' : 'commands'}
          {summary.commands.failed > 0 && ` (${summary.commands.failed} failed)`}
        </span>
        {summary.tests.total > 0 && (
          <span className={CHIP}>
            Tests: {summary.tests.passed} passed / {summary.tests.failed} failed
            {summary.tests.running > 0 && ` / ${summary.tests.running} running`}
          </span>
        )}
        {summary.reworkLoop != null && <span className={CHIP}>Rework loop {summary.reworkLoop}</span>}
      </div>
      {summary.currentStep && (
        <div className="text-xs text-yellow-300 flex items-center gap-1.5" data-testid="current-step">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-yellow-400 animate-pulse" aria-hidden="true" />
          Now: {summary.currentStep}
        </div>
      )}
    </div>
  )
}
