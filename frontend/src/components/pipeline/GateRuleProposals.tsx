import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  api,
  type AutonomySettings,
  type ProviderConfig,
  type RuleProposal,
  type RuleProposalRun,
} from '../../api/client'
import { formatRelativeAge } from '../../lib/format'
import { useReposStore } from '../../stores/repos'
import { Badge, Button, Field, controlClasses } from '../ui'

const POLL_MS = 4000
const MAX_EVIDENCE_LINKS = 5

function runLine(run: RuleProposalRun): string {
  const age = formatRelativeAge(run.finished_at ?? run.started_at)
  switch (run.status) {
    case 'running':
      return 'Looking for patterns in this gate’s escapes and rejections…'
    case 'failed':
      return `Last run failed ${age}: ${run.error}`
    case 'skipped':
      return `Last run ${age}: ${run.error}`
    default: {
      const n = run.proposal_count
      const cost = run.cost_usd > 0 ? ` · $${run.cost_usd.toFixed(2)}` : ''
      return `Last run ${age}: ${n} ${n === 1 ? 'suggestion' : 'suggestions'} from ${run.evidence_count} escapes and rejections${cost}`
    }
  }
}

function ProposalCard({
  p,
  busy,
  onAccept,
  onDismiss,
}: {
  p: RuleProposal
  busy: boolean
  onAccept: () => void
  onDismiss: () => void
}) {
  const repos = useReposStore((s) => s.repos)
  const repo = p.repo_id ? repos.find((r) => r.id === p.repo_id) : undefined
  const shown = p.evidence.slice(0, MAX_EVIDENCE_LINKS)
  return (
    <li className="rounded border border-border p-2 text-sm space-y-1.5">
      <div className="text-fg break-words">
        {p.path_globs.length > 0 && (
          <span className="inline-flex flex-wrap gap-1 mr-1">
            {p.path_globs.map((g) => (
              <code key={g} className="rounded bg-surface-sunken px-1 text-xs">{g}</code>
            ))}
          </span>
        )}
        <span>{p.summary}</span>
        {p.repo_id && <Badge size="xs" className="ml-1">{`One repo${repo ? `: ${repo.name}` : ''}`}</Badge>}
      </div>
      <p className="text-xs text-fg">
        Would have caught <strong>{p.caught}</strong> of {p.evidence_total} escapes and rejections, and also stopped{' '}
        <strong>{p.cost}</strong> of {p.clean_approvals} approvals nobody objected to.
        {p.unknown > 0 && <span className="text-fg-muted"> {p.unknown} could not be checked (diff gone).</span>}
      </p>
      {p.rationale && <p className="text-xs text-fg-muted italic break-words">Model: {p.rationale}</p>}
      {shown.length > 0 && (
        <p className="text-xs text-fg-muted flex flex-wrap gap-x-2 gap-y-0.5">
          <span>Caught:</span>
          {shown.map((e) => (
            <Link key={e.task_id} to={`/tasks/${e.task_id}`} className="text-accent-fg hover:underline truncate max-w-[14rem]">
              {e.title || e.task_id}
            </Link>
          ))}
          {p.evidence.length > shown.length && <span>+{p.evidence.length - shown.length} more</span>}
        </p>
      )}
      <div className="flex flex-wrap gap-2 pt-0.5">
        <Button size="sm" variant="primary" disabled={busy} onClick={onAccept} aria-label={`Add rule ${p.summary}`}>
          Add as ask rule
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onDismiss} aria-label={`Dismiss suggestion ${p.summary}`}>
          Dismiss
        </Button>
      </div>
    </li>
  )
}

/**
 * Model-proposed ask rules for a gate (ADR 0040). A suggestion only ever adds
 * asking, and nothing changes until a person adds it as a rule.
 */
export default function GateRuleProposals({
  workflowId,
  label,
  onRuleAdded,
}: {
  workflowId: string
  label: string
  onRuleAdded?: () => void
}) {
  const [settings, setSettings] = useState<AutonomySettings | null>(null)
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [proposals, setProposals] = useState<RuleProposal[]>([])
  const [run, setRun] = useState<RuleProposalRun | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [picking, setPicking] = useState(false)
  const [choice, setChoice] = useState('')

  const loadProposals = useCallback(async () => {
    const [ps, runs] = await Promise.all([
      api.autonomy.proposals.list({ workflow_id: workflowId, label }),
      api.autonomy.proposals.runs(workflowId, label, 1),
    ])
    setProposals(ps)
    setRun(runs[0] ?? null)
  }, [workflowId, label])

  const load = useCallback(async () => {
    try {
      const [s, pcs] = await Promise.all([api.autonomy.settings.get(), api.providerConfigs.list()])
      setSettings(s)
      setProviders(pcs)
      setChoice(s.proposal_provider_config_id ?? pcs[0]?.id ?? '')
      await loadProposals()
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load suggestions')
    }
  }, [loadProposals])
  // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch on mount
  useEffect(() => { void load() }, [load])

  const running = run?.status === 'running'
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(() => { void loadProposals().catch(() => {}) }, POLL_MS)
    return () => window.clearInterval(id)
  }, [running, loadProposals])

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await f()
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  const saveProvider = (id: string | null) =>
    act(async () => {
      if (!settings) return
      const next = await api.autonomy.settings.update({ ...settings, proposal_provider_config_id: id })
      setSettings(next)
      setPicking(false)
    })

  const configured = !!settings?.proposal_provider_config_id
  const provider = providers.find((p) => p.id === settings?.proposal_provider_config_id)
  const ideas = run?.status === 'succeeded' ? run.flag_ideas : []

  return (
    <section aria-label="Suggested ask rules" className="space-y-2">
      <h3 className="text-sm font-semibold text-fg">Suggested ask rules</h3>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      {settings && (!configured || picking) && (
        <div className="space-y-2">
          <p className="text-xs text-fg-muted">
            A model can read this gate’s escapes and rejections and suggest ask rules that would have caught them.
            Suggestions only ever add asking, and nothing changes until you add one.
          </p>
          {providers.length === 0 ? (
            <p className="text-xs text-fg-muted">
              Add a provider config first (<Link to="/settings/providers" className="text-accent-fg hover:underline">Settings</Link>).
            </p>
          ) : (
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Model for suggestions" className="w-56">
                <select className={controlClasses} value={choice} onChange={(e) => setChoice(e.target.value)}>
                  {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
              </Field>
              <Button size="sm" variant="primary" disabled={busy || !choice} onClick={() => void saveProvider(choice)}>
                Use this model
              </Button>
              {configured && (
                <>
                  <Button size="sm" variant="ghost" disabled={busy} onClick={() => void saveProvider(null)}>Turn off</Button>
                  <Button size="sm" variant="ghost" disabled={busy} onClick={() => setPicking(false)}>Cancel</Button>
                </>
              )}
            </div>
          )}
        </div>
      )}
      {settings && configured && !picking && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
          <Button
            size="sm"
            disabled={busy || running}
            onClick={() => void act(async () => setRun(await api.autonomy.proposals.run(workflowId, label)))}
          >
            {running ? 'Looking…' : 'Find rules'}
          </Button>
          <span>
            Using {provider?.name ?? 'a removed provider config'}
            {(settings.proposal_interval_hours ?? 0) > 0
              ? `, every ${settings.proposal_interval_hours}h while the gate is not Ask`
              : ', on demand only'}.
          </span>
          <button type="button" className="text-accent-fg hover:underline" onClick={() => setPicking(true)}>Change</button>
        </div>
      )}
      {run && <p role="status" className={run.status === 'failed' ? 'text-xs text-danger' : 'text-xs text-fg-muted'}>{runLine(run)}</p>}
      {proposals.length > 0 && (
        <ul className="space-y-2">
          {proposals.map((p) => (
            <ProposalCard
              key={p.id}
              p={p}
              busy={busy}
              onAccept={() => void act(async () => {
                await api.autonomy.proposals.accept(p.id)
                await loadProposals()
                onRuleAdded?.()
              })}
              onDismiss={() => void act(async () => {
                await api.autonomy.proposals.dismiss(p.id)
                await loadProposals()
              })}
            />
          ))}
        </ul>
      )}
      {ideas.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-xs font-semibold text-fg">Ideas for new checks</h4>
          <ul className="list-disc pl-4 text-xs text-fg-muted space-y-0.5">
            {ideas.map((f) => (
              <li key={f.idea}>{f.idea}{f.rationale ? ` (${f.rationale})` : ''}</li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}
