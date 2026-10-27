import { TriangleAlert, X } from 'lucide-react'
import Icon from '../ui/Icon'
import type { Dispatch, ReactNode, SetStateAction } from 'react'
import { Link } from 'react-router-dom'
import type { AgentConfig, ClaudeOptions, ProviderConfig, Skill } from '../../api/client'
import Field from './Field'
import PluginMcpPicker from './PluginMcpPicker'
import CommandFilterEditor from './CommandFilterEditor'
import LabelPicker from './LabelPicker'
import AgentHistoryPanel from './AgentHistoryPanel'
import AgentExperimentPanel from './AgentExperimentPanel'
import SkillPicker from '../skills/SkillPicker'
import { getCapability } from '../../lib/providerCapabilities'
import { providerListGaps } from '../../lib/providerListGaps'

export type FormState = Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled' | 'provider_config' | 'current_version' | 'running_experiment'>

export default function AgentConfigForm({
  selected,
  form,
  setForm,
  availableLabels,
  providerConfigs,
  claudeOptions,
  saving,
  deleting,
  onSave,
  onDelete,
  onToggleEnabled,
  helpButton,
  note = '',
  setNote,
  onRestored,
  historyReloadKey = 0,
  skills,
  skillIds,
  onSkillIdsChange,
}: {
  selected: AgentConfig | null
  form: FormState
  setForm: Dispatch<SetStateAction<FormState>>
  availableLabels: string[]
  providerConfigs: ProviderConfig[]
  claudeOptions: ClaudeOptions | null
  saving: boolean
  deleting: boolean
  onSave: () => void
  onDelete: () => void
  onToggleEnabled: () => void
  helpButton?: ReactNode
  note?: string
  setNote?: (n: string) => void
  onRestored?: (config: AgentConfig) => void
  historyReloadKey?: number
  skills?: Skill[]
  skillIds?: string[]
  onSkillIdsChange?: (ids: string[]) => void
}) {
  const isEnabled = selected ? selected.enabled : true
  // The claude-specific plugin/MCP picker and the command-filter hints are
  // both driven by the *selected provider config's* provider string, not a
  // form field — provider/model/env now live on the referenced ProviderConfig.
  const selectedProviderConfig = providerConfigs.find((pc) => pc.id === form.provider_config_id)
  const providerStr = selectedProviderConfig?.provider ?? ''
  const mcpCap = getCapability(providerStr, 'mcpServers')
  const skillsCap = getCapability(providerStr, 'skills')
  const subtasksCap = getCapability(providerStr, 'subtasks')
  const sessionResumeCap = getCapability(providerStr, 'sessionResume')
  const costTrackingCap = getCapability(providerStr, 'costTracking')
  const costWatchdogCap = getCapability(providerStr, 'costWatchdog')
  const labelTransitionsCap = getCapability(providerStr, 'labelTransitions')
  const maxTurnsCap = getCapability(providerStr, 'maxTurns')
  const effortCap = getCapability(providerStr, 'effort')
  const permissionModeCap = getCapability(providerStr, 'permissionMode')

  // Ordered fallback providers, tried after the primary when its account is
  // rate-limited. Ids no longer in providerConfigs are kept (the server
  // rejects them on save) but shown as unknown.
  const fallbackIds = form.fallback_provider_config_ids ?? []
  const setFallbacks = (ids: string[]) => setForm((f) => ({ ...f, fallback_provider_config_ids: ids }))
  const moveFallback = (i: number, delta: number) => {
    const ids = [...fallbackIds]
    const j = i + delta
    if (j < 0 || j >= ids.length) return
    ;[ids[i], ids[j]] = [ids[j], ids[i]]
    setFallbacks(ids)
  }
  const addableFallbacks = providerConfigs.filter(
    (pc) => pc.id !== form.provider_config_id && !fallbackIds.includes(pc.id),
  )
  // Escalation ladder: rung 0 is the provider/effort above; entry i is rung i+1.
  const ladder = form.escalation_ladder ?? []
  const setLadder = (l: NonNullable<FormState['escalation_ladder']>) => setForm((f) => ({ ...f, escalation_ladder: l }))
  const updateRung = (i: number, patch: Partial<NonNullable<FormState['escalation_ladder']>[number]>) =>
    setLadder(ladder.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  const moveRung = (i: number, delta: number) => {
    const l = [...ladder]
    const j = i + delta
    if (j < 0 || j >= l.length) return
    ;[l[i], l[j]] = [l[j], l[i]]
    setLadder(l)
  }
  const providerOf = (id: string) => providerConfigs.find((pc) => pc.id === id)?.provider ?? ''
  // The primary's gaps already show next to each field; this covers the
  // fallbacks, which a task only reaches when the primary is tapped out.
  const listGaps = providerListGaps(
    form,
    fallbackIds.map(providerOf).filter((p) => p !== providerStr),
  )

  return (
    <div className="flex-1 min-w-0">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-2">
          <h2 className="text-base font-semibold text-fg">
            {selected ? `Edit: ${selected.name}` : 'New Agent Config'}
          </h2>
          {helpButton}
        </div>
        {selected && (
          <div className="flex items-center gap-2">
            <span className={`text-xs ${isEnabled ? 'text-green-400' : 'text-fg-subtle'}`}>
              {isEnabled ? 'Active' : 'Disabled'}
            </span>
            <button
              onClick={onToggleEnabled}
              disabled={saving}
              className={`relative w-9 h-5 rounded-full transition-colors ${isEnabled ? 'bg-green-600' : 'bg-border'} disabled:opacity-50`}
            >
              <span className={`absolute left-0 top-1/2 w-4 h-4 rounded-full bg-white shadow transition-transform -translate-y-1/2 ${isEnabled ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </button>
          </div>
        )}
      </div>

      {providerStr && labelTransitionsCap.support !== 'full' && (
        <div className="max-w-2xl mb-5 px-3 py-2 rounded border border-amber-700/50 bg-amber-950/30 text-xs text-warning">
          <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />The <span className="font-mono">{providerStr}</span> provider cannot signal workflow transitions via
          MCP tools — tasks handled by this agent may not move to the next label automatically (it relies on a
          text <span className="font-mono">OUTCOME: success/failure</span> marker). See{' '}
          <a
            href="https://github.com/myinisjap/agent-task-editor/blob/main/docs/agents.md#capability-matrix"
            target="_blank"
            rel="noreferrer"
            className="underline hover:text-amber-200"
          >
            docs/agents.md
          </a>.
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 max-w-2xl">
        <Field label="Name">
          <input
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            className="input"
            placeholder="e.g. Code Agent"
          />
        </Field>

        <Field label="Provider config" className="sm:col-span-2" hint="Which provider/model/API key this agent runs against. Manage provider configs (and their env vars) on the Providers page — they can be shared across agent configs and chat sessions.">
          <select
            value={form.provider_config_id}
            onChange={(e) => setForm((f) => ({ ...f, provider_config_id: e.target.value }))}
            className="input"
          >
            <option value="">Select a provider config…</option>
            {providerConfigs.map((pc) => (
              <option key={pc.id} value={pc.id}>{pc.name} ({pc.provider}{pc.model ? `/${pc.model}` : ''})</option>
            ))}
          </select>
          <p className="mt-1 text-xs text-fg-subtle">
            <Link to="/settings/providers" className="text-accent-fg hover:text-accent-fg">Manage provider configs →</Link>
          </p>
        </Field>

        <Field
          label="Fallback providers"
          className="sm:col-span-2"
          hint="Tried in order when the provider above (or an earlier fallback) hits its usage limit. Tasks go back to the first one as soon as its limit resets."
        >
          {fallbackIds.length > 0 && (
            <ol className="mb-2 space-y-1" aria-label="Fallback providers">
              {fallbackIds.map((id, i) => {
                const pc = providerConfigs.find((p) => p.id === id)
                return (
                  <li key={id} className="flex items-center gap-2 text-xs text-fg-muted">
                    <span className="w-5 text-fg-subtle">{i + 1}.</span>
                    <span className="flex-1 truncate">
                      {pc ? `${pc.name} (${pc.provider}${pc.model ? `/${pc.model}` : ''})` : `Unknown provider config ${id}`}
                    </span>
                    <button type="button" onClick={() => moveFallback(i, -1)} disabled={i === 0} className="px-1 text-fg-muted hover:text-fg disabled:opacity-30" aria-label={`Move ${pc?.name ?? id} up`}>↑</button>
                    <button type="button" onClick={() => moveFallback(i, 1)} disabled={i === fallbackIds.length - 1} className="px-1 text-fg-muted hover:text-fg disabled:opacity-30" aria-label={`Move ${pc?.name ?? id} down`}>↓</button>
                    <button type="button" onClick={() => setFallbacks(fallbackIds.filter((x) => x !== id))} className="px-1 text-fg-muted hover:text-danger" aria-label={`Remove ${pc?.name ?? id}`}><Icon icon={X} size="sm" /></button>
                  </li>
                )
              })}
            </ol>
          )}
          <select
            value=""
            onChange={(e) => e.target.value && setFallbacks([...fallbackIds, e.target.value])}
            className="input"
            aria-label="Add fallback provider"
            disabled={addableFallbacks.length === 0}
          >
            <option value="">{addableFallbacks.length === 0 ? 'No other provider configs' : 'Add a fallback provider…'}</option>
            {addableFallbacks.map((pc) => (
              <option key={pc.id} value={pc.id}>{pc.name} ({pc.provider}{pc.model ? `/${pc.model}` : ''})</option>
            ))}
          </select>
        </Field>

        <details className="sm:col-span-2 rounded border border-border px-3 py-2" open={ladder.length > 0 || (skillIds?.length ?? 0) > 0}>
          <summary className="cursor-pointer text-sm text-fg-muted">Advanced</summary>
          <div className="mt-3">
            <p className="text-sm font-medium text-fg">Escalate on failure</p>
            <p className="mb-2 text-xs text-fg-subtle">
              Rung 0 is the provider and effort above. After N failed attempts or review bounce-backs on this stage, the task runs on rung N (capped at the last rung). Resets when the stage is freshly entered or a human acts.
            </p>
            {ladder.length > 0 && (
              <ol className="mb-2 space-y-1" aria-label="Escalation rungs">
                {ladder.map((r, i) => {
                  const pc = providerConfigs.find((p) => p.id === r.provider_config_id)
                  const rungProvider = pc?.provider ?? ''
                  const rungEffortCap = getCapability(rungProvider, 'effort')
                  return (
                    <li key={i} className="text-xs text-fg-muted">
                      <div className="flex items-center gap-2">
                        <span className="w-14 text-fg-subtle">Rung {i + 1}</span>
                        <select value={r.provider_config_id} onChange={(e) => updateRung(i, { provider_config_id: e.target.value })} className="input flex-1" aria-label={`Rung ${i + 1} provider`}>
                          {!pc && <option value={r.provider_config_id}>Unknown provider config {r.provider_config_id}</option>}
                          {providerConfigs.map((p) => (
                            <option key={p.id} value={p.id}>{p.name} ({p.provider}{p.model ? `/${p.model}` : ''})</option>
                          ))}
                        </select>
                        <select value={r.effort ?? ''} onChange={(e) => updateRung(i, { effort: e.target.value as typeof r.effort })} className="input w-32" aria-label={`Rung ${i + 1} effort`}>
                          <option value="">Default</option>
                          <option value="low">Low</option>
                          <option value="medium">Medium</option>
                          <option value="high">High</option>
                          <option value="xhigh">Extra high</option>
                          <option value="max">Max</option>
                        </select>
                        <button type="button" onClick={() => moveRung(i, -1)} disabled={i === 0} className="px-1 text-fg-muted hover:text-fg disabled:opacity-30" aria-label={`Move rung ${i + 1} up`}>↑</button>
                        <button type="button" onClick={() => moveRung(i, 1)} disabled={i === ladder.length - 1} className="px-1 text-fg-muted hover:text-fg disabled:opacity-30" aria-label={`Move rung ${i + 1} down`}>↓</button>
                        <button type="button" onClick={() => setLadder(ladder.filter((_, k) => k !== i))} className="px-1 text-fg-muted hover:text-danger" aria-label={`Remove rung ${i + 1}`}><Icon icon={X} size="sm" /></button>
                      </div>
                      {r.effort && rungProvider && rungEffortCap.support !== 'full' && (
                        <p className="mt-1 ml-16 text-xs text-warning"><Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{rungEffortCap.note ?? `Effort is not supported for the ${rungProvider} provider — this setting will be ignored.`}</p>
                      )}
                    </li>
                  )
                })}
              </ol>
            )}
            <button
              type="button"
              onClick={() => setLadder([...ladder, { provider_config_id: form.provider_config_id, effort: '' }])}
              disabled={!form.provider_config_id || ladder.length >= 10}
              className="text-xs text-accent-fg hover:text-accent-fg disabled:opacity-40"
            >
              Add rung
            </button>
          </div>

          <div className="mt-4 border-t border-border pt-3">
            <p className="text-sm font-medium text-fg">Skills</p>
            <p className="text-xs text-fg-subtle mb-2">
              Enabled skills attached here, plus the repo's skills, are copied into the worktree at run start.
            </p>
            <SkillPicker skills={skills ?? []} selected={skillIds ?? []} onChange={(ids) => onSkillIdsChange?.(ids)} />
            {providerStr && skillsCap.support !== 'full' && (
              <p className="mt-1 text-xs text-warning">
                <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{skillsCap.note ?? `The ${providerStr} provider has no skills support — attached skills won't be copied for this agent.`}
              </p>
            )}
          </div>
        </details>

        {listGaps.length > 0 && (
          <div role="alert" className="sm:col-span-2 px-3 py-2 rounded border border-amber-700/50 bg-amber-950/30 text-xs text-warning">
            <p className="mb-1"><Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />Some of this agent's settings won't fully apply when a task falls back to:</p>
            <ul className="list-disc pl-5 space-y-0.5">
              {listGaps.map((g) => (
                <li key={`${g.provider}:${g.setting}`}>
                  <span className="font-mono">{g.provider}</span>: {g.setting}{' '}
                  {g.support === 'none' ? 'is not supported' : 'is only partly supported'}
                  {g.note ? <span className="text-warning/80"> — {g.note}</span> : null}
                </li>
              ))}
            </ul>
          </div>
        )}

        <Field label="Labels" className="sm:col-span-2">
          <LabelPicker
            selected={(() => { try { return JSON.parse(form.labels) } catch { return [] } })()}
            available={availableLabels}
            onChange={(lbls) => setForm((f) => ({ ...f, labels: JSON.stringify(lbls) }))}
          />
        </Field>

        <Field label="System prompt" className="sm:col-span-2">
          <textarea
            value={form.system_prompt}
            onChange={(e) => setForm((f) => ({ ...f, system_prompt: e.target.value }))}
            rows={8}
            className="input resize-none font-mono text-xs"
            placeholder="You are an expert software engineer…"
          />
        </Field>

        <Field label="Timeout (secs)">
          <input
            type="number"
            value={form.timeout_secs}
            onChange={(e) => setForm((f) => ({ ...f, timeout_secs: Number(e.target.value) }))}
            className="input"
            min={30}
            max={3600}
          />
        </Field>

        <Field label="Max tokens">
          <input
            type="number"
            value={form.max_tokens}
            onChange={(e) => setForm((f) => ({ ...f, max_tokens: Number(e.target.value) }))}
            className="input"
            min={256}
            max={200000}
          />
        </Field>

        <Field label="Max turns">
          <input
            type="number"
            value={form.max_turns}
            onChange={(e) => setForm((f) => ({ ...f, max_turns: Number(e.target.value) }))}
            className="input"
            min={1}
            max={200}
          />
          {form.max_turns > 0 && providerStr && maxTurnsCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{maxTurnsCap.note ?? `Max turns is not enforced for the ${providerStr} provider.`}
            </p>
          )}
        </Field>

        <Field label="Effort" hint="Higher effort = more reasoning, higher cost and latency. Leave as Default to use the provider's own default.">
          <select
            value={form.effort ?? ''}
            onChange={(e) => setForm((f) => ({ ...f, effort: e.target.value as FormState['effort'] }))}
            className="input"
          >
            <option value="">Default</option>
            <option value="low">Low</option>
            <option value="medium">Medium</option>
            <option value="high">High</option>
            <option value="xhigh">Extra high</option>
            <option value="max">Max</option>
          </select>
          {form.effort && providerStr && effortCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{effortCap.note ?? `Effort is not supported for the ${providerStr} provider — this setting will be ignored.`}
            </p>
          )}
        </Field>

        {providerStr === 'claude' && (
          <Field label="Permission mode" hint="Claude only. Default (unset) matches today's behavior: no --permission-mode flag, so the CLI's own default ('auto') applies.">
            <select
              value={form.permission_mode ?? ''}
              onChange={(e) => setForm((f) => ({ ...f, permission_mode: e.target.value as FormState['permission_mode'] }))}
              className="input"
            >
              <option value="">Default (unset — CLI's own default)</option>
              <option value="default">default — approve every tool call interactively</option>
              <option value="auto">auto — cloud safety classifier (same as CLI default)</option>
              <option value="acceptEdits">acceptEdits — auto-approve file edits only</option>
              <option value="bypassPermissions">bypassPermissions — skip approval prompts entirely</option>
            </select>
            {form.permission_mode === 'auto' && (
              <p className="mt-1 text-xs text-warning">
                <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />This mode depends on a cloud safety classifier, which can transiently deny harmless commands
                when the classifier is unavailable.
              </p>
            )}
            {form.permission_mode === 'bypassPermissions' && (
              <p className="mt-1 text-xs text-warning">
                <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />This mode skips approval prompts entirely for this agent&apos;s runs.
                {(() => { try { return JSON.parse(form.command_denylist ?? '[]').length > 0 } catch { return false } })() && (
                  <> Your command_denylist is still enforced under this mode (verified empirically) — it is not bypassed.</>
                )}
              </p>
            )}
            {form.permission_mode && providerStr && permissionModeCap.support !== 'full' && (
              <p className="mt-1 text-xs text-warning">
                <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{permissionModeCap.note ?? `Permission mode is not supported for the ${providerStr} provider — this setting will be ignored.`}
              </p>
            )}
          </Field>
        )}

        <Field label="Max retries" hint="Auto-retries for transient errors (rate limits, network blips). 0 disables auto-retry.">
          <input
            type="number"
            value={form.max_retries}
            onChange={(e) => setForm((f) => ({ ...f, max_retries: Number(e.target.value) }))}
            className="input"
            min={0}
            max={10}
          />
        </Field>

        <Field label="Priority" hint="Lower runs first; higher-priority-number configs on the same label act as backups when the primary is rate-limited.">
          <input
            type="number"
            value={form.priority ?? 0}
            onChange={(e) => setForm((f) => ({ ...f, priority: Number(e.target.value) }))}
            className="input"
          />
        </Field>

        <Field label="Retry backoff (secs)" hint="Base backoff before a retry is re-dispatched; doubles each attempt, capped at 10 min.">
          <input
            type="number"
            value={form.retry_backoff_secs}
            onChange={(e) => setForm((f) => ({ ...f, retry_backoff_secs: Number(e.target.value) }))}
            className="input"
            min={1}
            max={600}
          />
        </Field>

        <Field label="Max cost per task (USD)" hint="Advisory budget cap in USD, cumulative across all runs of a task (not per run): checked by the dispatcher before each dispatch against the task's total run cost so far. 0 disables the cap (unlimited). On claude/qwen_code, a mid-run watchdog also projects cost from incremental token usage and cancels an in-flight run that crosses this cap (escalating to waiting_human) — an estimate, not the provider's own authoritative billed cost. Other providers only enforce the cap before the next dispatch.">
          <input
            type="number"
            step="0.01"
            value={form.max_cost_usd}
            onChange={(e) => setForm((f) => ({ ...f, max_cost_usd: Number(e.target.value) }))}
            className="input"
            min={0}
          />
          {form.max_cost_usd > 0 && providerStr && costTrackingCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{costTrackingCap.note ?? `Cost is not reliably tracked for the ${providerStr} provider — this budget cap may not fire.`}
            </p>
          )}
          {form.max_cost_usd > 0 && providerStr && costTrackingCap.support === 'full' && costWatchdogCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{costWatchdogCap.note ?? `No mid-run cost kill switch for the ${providerStr} provider — this budget cap only prevents the next dispatch, not an in-flight run.`}
            </p>
          )}
        </Field>

        <Field label="Resume sessions" hint="Claude provider only: re-runs on the same task continue the previous run's session (full prior context) instead of starting cold. Turn off for stages that should review with fresh eyes.">
          <label className="flex items-center gap-2 text-sm text-fg-muted cursor-pointer">
            <input
              type="checkbox"
              checked={form.resume_sessions ?? true}
              onChange={(e) => setForm((f) => ({ ...f, resume_sessions: e.target.checked }))}
            />
            Resume previous session on re-runs
          </label>
          {(form.resume_sessions ?? true) && providerStr && sessionResumeCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />{sessionResumeCap.note
                ? `Session resume is not fully supported for the ${providerStr} provider: ${sessionResumeCap.note}`
                : `Session resume is not supported for the ${providerStr} provider.`}
            </p>
          )}
        </Field>

        <Field label="Subtasks" hint="claude/qwen_code/codex_cli only: expose the create_subtask tool so this agent (typically the planner) can decompose its task into child tasks. Children branch off the parent's branch and merge back automatically. Off by default.">
          <label className="flex items-center gap-2 text-sm text-fg-muted cursor-pointer">
            <input
              type="checkbox"
              checked={form.subtasks_enabled ?? false}
              onChange={(e) => setForm((f) => ({ ...f, subtasks_enabled: e.target.checked }))}
            />
            Allow this agent to create subtasks
          </label>
          {form.subtasks_enabled && providerStr && subtasksCap.support !== 'full' && (
            <p className="mt-1 text-xs text-warning">
              <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />Subtasks require the create_subtask MCP tool, not available on the {providerStr} provider.
            </p>
          )}
          {form.subtasks_enabled && (
            <label className="flex items-center gap-2 text-xs text-fg-muted mt-2">
              Max subtasks per task
              <input
                type="number"
                min={1}
                value={form.max_subtasks ?? 10}
                onChange={(e) => setForm((f) => ({ ...f, max_subtasks: Number(e.target.value) }))}
                className="w-20 bg-surface-sunken border border-border rounded px-2 py-1 text-fg"
              />
            </label>
          )}
        </Field>

        {providerStr === 'claude' ? (
          <PluginMcpPicker
            claudeOptions={claudeOptions}
            enabledPlugins={form.enabled_plugins ?? '[]'}
            enabledMcpServers={form.enabled_mcp_servers ?? '[]'}
            onPluginsChange={(json) => setForm((f) => ({ ...f, enabled_plugins: json }))}
            onMcpServersChange={(json) => setForm((f) => ({ ...f, enabled_mcp_servers: json }))}
          />
        ) : providerStr && mcpCap.support !== 'full' ? (
          <Field label="Plugins / MCP servers" className="sm:col-span-2">
            <p className="text-xs text-fg-subtle">
              MCP servers / plugins are not supported by the {providerStr} provider.
            </p>
          </Field>
        ) : null}

        <CommandFilterEditor
          provider={providerStr}
          allowlist={form.command_allowlist ?? '[]'}
          denylist={form.command_denylist ?? '[]'}
          onAllowlistChange={(v) => setForm((f) => ({ ...f, command_allowlist: v }))}
          onDenylistChange={(v) => setForm((f) => ({ ...f, command_denylist: v }))}
        />
      </div>

      {selected && setNote && (
        <div className="mt-6">
          <label className="block text-xs text-fg-muted mb-1" htmlFor="agent-change-note">Change note (optional)</label>
          <input
            id="agent-change-note"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="What changed and why? Recorded in History"
            className="w-full rounded bg-surface-raised border border-border px-3 py-2 text-sm text-fg"
          />
        </div>
      )}

      <div className="flex gap-3 mt-6">
        <button
          onClick={onSave}
          disabled={saving || !form.name.trim() || !form.provider_config_id}
          className="px-5 py-2 text-sm font-medium rounded bg-accent hover:bg-accent/90 text-on-accent disabled:opacity-50"
        >
          {saving ? 'Saving…' : selected ? 'Update' : 'Create'}
        </button>
        {selected && (
          <button
            onClick={onDelete}
            disabled={deleting}
            className="px-5 py-2 text-sm font-medium rounded bg-danger-solid hover:bg-danger-solid/90 text-on-accent disabled:opacity-50"
          >
            {deleting ? 'Deleting…' : 'Delete'}
          </button>
        )}
      </div>

      {selected && onRestored && (
        <AgentHistoryPanel agentId={selected.id} onRestored={onRestored} reloadKey={historyReloadKey} />
      )}

      {selected && onRestored && (
        <AgentExperimentPanel
          agentId={selected.id}
          config={selected}
          providerConfigs={providerConfigs}
          onPromoted={onRestored}
          reloadKey={historyReloadKey}
        />
      )}
    </div>
  )
}
