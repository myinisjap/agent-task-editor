import { ChevronDown, ChevronRight, RefreshCw } from 'lucide-react'
import Icon from '../ui/Icon'
import { Fragment, useState } from 'react'
import { formatRunStatus } from '../../lib/format'
import type { AgentRun, AgentConfig, ProviderConfig } from '../../api/client'

// formatTokenCount renders a token count compactly (e.g. "1.2k") for the
// per-run cost/usage badge, falling back to the plain number for small counts.
function formatTokenCount(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

function NotesPanel({ notes }: { notes: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-border pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-fg-subtle hover:text-fg-muted w-full text-left py-0.5"
      >
        <Icon icon={open ? ChevronDown : ChevronRight} size="sm" />
        <span>agent notes</span>
      </button>
      {open && (
        <pre className="text-xs text-fg-muted bg-surface-sunken rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {notes}
        </pre>
      )}
    </div>
  )
}

function StoredInfoPanel({ runId, info }: { runId: string; info: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-border pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-fg-subtle hover:text-fg-muted w-full text-left py-0.5"
      >
        <Icon icon={open ? ChevronDown : ChevronRight} size="sm" />
        <span className="font-mono text-fg-subtle">{runId.slice(0, 8)}</span>
        <span>stored info</span>
      </button>
      {open && (
        <pre className="text-xs text-fg-muted bg-surface-sunken rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {info}
        </pre>
      )}
    </div>
  )
}

function ladderLabel(run: AgentRun, agentConfigs: AgentConfig[], providerConfigs: ProviderConfig[]): string {
  const rung = run.ladder_rung ?? 0
  const agent = agentConfigs.find((a) => a.id === run.agent_config_id)
  const pc = providerConfigs.find((p) => p.id === run.provider_config_id) ?? (run.provider_config_id === agent?.provider_config_id ? agent?.provider_config : undefined)
  const effort = rung === 0 ? agent?.effort : (agent?.escalation_ladder?.[rung - 1]?.effort || agent?.effort)
  const name = pc ? (pc.model || pc.name) : ''
  return `rung ${rung}` + (name ? `: ${name}` : '') + (effort ? ` / ${effort}` : '')
}

export default function RunHistoryList({
  runs,
  agentConfigs,
  providerConfigs = [],
  selectedRun,
  onSelectRun,
  isRunning,
  canRerun,
  onStop,
  onRerun,
  actionPending,
}: {
  runs: AgentRun[]
  agentConfigs: AgentConfig[]
  providerConfigs?: ProviderConfig[]
  selectedRun: string | null
  onSelectRun: (runId: string) => void
  isRunning: boolean
  canRerun: boolean
  onStop: () => void
  onRerun: () => void
  actionPending: boolean
}) {
  if (runs.length === 0 && !isRunning && !canRerun) return null

  return (
    <>
      {runs.length > 0 && (
        <div>
          <p className="text-xs text-fg-subtle mb-2">Agent runs</p>
          <div className="flex flex-col gap-1">
            {runs.map((run) => (
              <Fragment key={run.id}>
                <button
                  onClick={() => onSelectRun(run.id)}
                  className={`text-left text-xs px-2 py-1.5 rounded ${
                    selectedRun === run.id
                      ? 'bg-border text-fg'
                      : 'text-fg-muted hover:bg-surface-sunken'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono truncate">{run.id.slice(0, 8)}</span>
                    {agentConfigs.find((a) => a.id === run.agent_config_id)?.name && (
                      <span className="text-fg-muted truncate text-xs">
                        {agentConfigs.find((a) => a.id === run.agent_config_id)?.name}
                      </span>
                    )}
                    <span title={run.status} className={`shrink-0 ${
                      run.status === 'completed'     ? 'text-success' :
                      run.status === 'running'       ? 'text-yellow-400 animate-pulse' :
                      run.status === 'failed'        ? 'text-danger' :
                      run.status === 'waiting_human' ? 'text-pink-400' :
                      run.status === 'cancelled'     ? 'text-orange-400' :
                      // Interrupted by a server restart: not a failure, so neutral grey.
                      run.status === 'interrupted'   ? 'text-fg-muted' :
                      'text-fg-subtle'
                    }`}>{formatRunStatus(run.status)}</span>
                    {run.ladder_rung != null && (
                      <span className="text-[11px] text-accent-fg" title="Escalation ladder rung (effort from current agent config)">
                        {ladderLabel(run, agentConfigs, providerConfigs)}
                      </span>
                    )}
                  </div>
                  {((run.cost_usd ?? 0) > 0 || (run.input_tokens ?? 0) > 0 || (run.output_tokens ?? 0) > 0) && (
                    <div className="text-fg-subtle text-[11px] mt-0.5">
                      {run.cost_unknown
                        // Tokens were consumed but no pricing table row (user-edited
                        // or hardcoded fallback) matched the model — surface that
                        // distinctly from a genuine $0 (e.g. under a Claude Max
                        // subscription) so users know to add a row at /settings/pricing.
                        ? <span title="Model not found in the pricing table — add it at Settings → Pricing">cost unknown</span>
                        : <>${(run.cost_usd ?? 0).toFixed(4)}</>}
                      {' '}· {formatTokenCount(run.input_tokens ?? 0)}/{formatTokenCount(run.output_tokens ?? 0)} tok
                      {/* turns_used is 0 when a provider reports no turn count at all,
                          or when a run (codex_cli/opencode, which count completed tool
                          steps / step_finish events as a proxy for turns) ended before
                          completing a single step — omit rather than show a made-up 0. */}
                      {(run.turns_used ?? 0) > 0 && (
                        <span title="Internal agent turns this run used">
                          {' '}· {run.turns_used} turns
                        </span>
                      )}
                    </div>
                  )}
                </button>
                {run.stored_info && (
                  <StoredInfoPanel runId={run.id} info={run.stored_info} />
                )}
                {run.notes && (
                  <NotesPanel notes={run.notes} />
                )}
              </Fragment>
            ))}
          </div>
        </div>
      )}

      {isRunning && (
        <button
          onClick={onStop}
          disabled={actionPending}
          className="w-full px-3 py-1.5 text-xs font-medium rounded bg-red-900/60 hover:bg-red-800 text-red-200 disabled:opacity-50"
          title="Stop the running agent and pause the task"
        >
          ■ Stop run
        </button>
      )}

      {canRerun && (
        <button
          onClick={onRerun}
          disabled={actionPending}
          className="w-full px-3 py-1.5 text-xs font-medium rounded bg-border hover:bg-slate-600 text-fg disabled:opacity-50"
        >
          <Icon icon={RefreshCw} size="sm" className="inline -mt-0.5 mr-1" />Re-run
        </button>
      )}
    </>
  )
}
