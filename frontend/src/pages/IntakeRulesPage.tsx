import { useEffect, useMemo, useState } from 'react'
import {
  api,
  type IntakeRule,
  type IntakeRuleBody,
  type IntakeRulePreviewMatch,
  type Repo,
  type TaskTemplate,
  type Workflow,
} from '../api/client'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { IntakeRulesHelp } from '../components/shared/pageHelp'

export const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

// TRUSTED_ASSOCIATIONS mirrors internal/intake.TrustedAssociations — the
// only author associations that let a rule target a non-agent_ignore
// (agent-triggerable) label. Kept in sync manually since this is a small,
// stable enum shared across the stack.
const TRUSTED_ASSOCIATIONS = ['OWNER', 'MEMBER', 'COLLABORATOR'] as const
const ALL_ASSOCIATIONS = ['OWNER', 'MEMBER', 'COLLABORATOR', 'CONTRIBUTOR', 'NONE'] as const

const MATCH_SOURCES = [
  { value: '', label: 'Any source' },
  { value: 'issue', label: 'Issue import' },
  { value: 'schedule', label: 'Schedule' },
] as const

type RuleForm = {
  name: string
  enabled: boolean
  sort_order: number
  match_source: '' | 'manual' | 'issue' | 'schedule' | 'subtask'
  match_repo_id: string
  match_labels: string
  match_title_pattern: string
  match_body_pattern: string
  match_author_assoc: ('OWNER' | 'MEMBER' | 'COLLABORATOR' | 'CONTRIBUTOR' | 'NONE')[]
  apply_template_id: string
  apply_priority: string
  apply_target_label: string
  apply_workflow_id: string
  apply_max_cost_usd: string
}

const emptyForm: RuleForm = {
  name: '',
  enabled: true,
  sort_order: 0,
  match_source: 'issue',
  match_repo_id: '',
  match_labels: '',
  match_title_pattern: '',
  match_body_pattern: '',
  match_author_assoc: [],
  apply_template_id: '',
  apply_priority: '',
  apply_target_label: '',
  apply_workflow_id: '',
  apply_max_cost_usd: '',
}

function ruleToForm(r: IntakeRule): RuleForm {
  return {
    name: r.name,
    enabled: r.enabled,
    sort_order: r.sort_order,
    match_source: (r.match_source ?? '') as RuleForm['match_source'],
    match_repo_id: r.match_repo_id ?? '',
    match_labels: (r.match_labels ?? []).join(', '),
    match_title_pattern: r.match_title_pattern ?? '',
    match_body_pattern: r.match_body_pattern ?? '',
    match_author_assoc: r.match_author_assoc ?? [],
    apply_template_id: r.apply_template_id ?? '',
    apply_priority: r.apply_priority == null ? '' : String(r.apply_priority),
    apply_target_label: r.apply_target_label ?? '',
    apply_workflow_id: r.apply_workflow_id ?? '',
    apply_max_cost_usd: r.apply_max_cost_usd == null ? '' : String(r.apply_max_cost_usd),
  }
}

function formToBody(f: RuleForm): IntakeRuleBody {
  return {
    name: f.name.trim(),
    enabled: f.enabled,
    sort_order: f.sort_order,
    match_source: f.match_source,
    match_repo_id: f.match_repo_id || null,
    match_labels: f.match_labels
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
    match_title_pattern: f.match_title_pattern.trim(),
    match_body_pattern: f.match_body_pattern.trim(),
    match_author_assoc: f.match_author_assoc,
    apply_template_id: f.apply_template_id || null,
    apply_priority: f.apply_priority === '' ? null : (Number(f.apply_priority) as -1 | 0 | 1 | 2),
    apply_target_label: f.apply_target_label.trim(),
    apply_workflow_id: f.apply_workflow_id || null,
    apply_max_cost_usd: f.apply_max_cost_usd === '' ? null : Number(f.apply_max_cost_usd),
  }
}

const PRIORITY_LABELS: Record<string, string> = {
  '-1': 'Low',
  '0': 'Normal',
  '1': 'High',
  '2': 'Urgent',
}

/**
 * IntakeRulesPage manages intake_rules: the match->apply table evaluated at
 * task-creation time for the 'issue' and 'schedule' sources (see
 * internal/intake, migration 051, docs/task-sources.md).
 *
 * The auto-start warning below is a hard requirement, not a nicety: a rule
 * whose apply_target_label is not the effective workflow's agent_ignore
 * (human-gate) label bypasses the human-review step that protects against
 * untrusted imported issue content (see #331). The form blocks submission
 * of such a rule unless match_author_assoc is restricted to trusted
 * associations — mirroring the same gate the backend enforces
 * (intake.AutoStartAllowed) so the UI never lets a request through the
 * server would reject anyway.
 */
export default function IntakeRulesPage() {
  const [rules, setRules] = useState<IntakeRule[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [templates, setTemplates] = useState<TaskTemplate[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showHelp, setShowHelp] = useState(false)

  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<RuleForm>(emptyForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')

  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState<RuleForm>(emptyForm)
  const [editSaving, setEditSaving] = useState(false)
  const [editError, setEditError] = useState('')

  const [previewFor, setPreviewFor] = useState<'new' | string | null>(null)
  const [previewResults, setPreviewResults] = useState<IntakeRulePreviewMatch[] | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')

  function reload() {
    setLoading(true)
    Promise.all([api.intakeRules.list(), api.repos.list(), api.templates.list(), api.workflows.list()])
      .then(([r, repo, t, w]) => {
        setRules([...(r ?? [])].sort((a, b) => a.sort_order - b.sort_order))
        setRepos(repo ?? [])
        setTemplates(t ?? [])
        setWorkflows(w ?? [])
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    reload()
  }, [])

  // effectiveWorkflowLabels resolves which workflow's labels a form's
  // apply_target_label should be validated/selected against: the explicit
  // apply_workflow_id override if set, else the selected repo's own
  // workflow. Mirrors the backend handler's validate() precedence.
  function effectiveWorkflowLabels(f: RuleForm) {
    const wfId = f.apply_workflow_id || repos.find((r) => r.id === f.match_repo_id)?.workflow_id
    const wf = workflows.find((w) => w.id === wfId)
    return wf?.labels ?? []
  }

  function isGateLabel(f: RuleForm) {
    if (!f.apply_target_label) return true // empty = leave default = the gate
    const label = effectiveWorkflowLabels(f).find((l) => l.name === f.apply_target_label)
    // Unknown label (workflow not resolvable client-side, e.g. repo-agnostic
    // rule): don't claim it's safe — treat as non-gate so the warning shows
    // and the author constraint is required, matching the server's
    // conservative fallback.
    return label ? label.agent_ignore !== 0 : false
  }

  function hasTrustedAuthorConstraint(f: RuleForm) {
    return (
      f.match_author_assoc.length > 0 &&
      f.match_author_assoc.every((a) => (TRUSTED_ASSOCIATIONS as readonly string[]).includes(a))
    )
  }

  function autoStartUnsafe(f: RuleForm) {
    // The auto-start gate only applies to rules that can match an 'issue'
    // (match_source is "issue" or "" for any). It does not apply to
    // match_source "schedule": a schedule's target_label is already
    // human-configured, validated content, not untrusted imported text, so
    // requiring an author-association constraint (which a schedule firing
    // has no equivalent of) would be nonsensical — mirrors the backend's
    // validate() in intake_rules.go.
    if (f.match_source === 'schedule') return false
    return !isGateLabel(f) && !hasTrustedAuthorConstraint(f)
  }

  // templateNoOpForSchedule reports whether apply_template_id is set on a
  // rule whose match_source is "schedule" — a combination the scheduler
  // silently ignores (scheduled tasks are always shaped from the
  // schedule's own template, never from a matched rule's), so the backend
  // rejects it at write time. Mirrored here so the form can warn/block
  // before submitting rather than surfacing only as a server 400.
  function templateNoOpForSchedule(f: RuleForm) {
    return f.match_source === 'schedule' && !!f.apply_template_id
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setFormError('')
    try {
      const rule = await api.intakeRules.create(formToBody(form))
      setRules((rs) => [...rs, rule].sort((a, b) => a.sort_order - b.sort_order))
      setShowForm(false)
      setForm(emptyForm)
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  function startEdit(rule: IntakeRule) {
    setEditingId(rule.id)
    setEditForm(ruleToForm(rule))
    setEditError('')
  }

  function cancelEdit() {
    setEditingId(null)
    setEditForm(emptyForm)
    setEditError('')
  }

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault()
    if (!editingId) return
    setEditSaving(true)
    setEditError('')
    try {
      const updated = await api.intakeRules.update(editingId, formToBody(editForm))
      setRules((rs) => rs.map((r) => (r.id === editingId ? updated : r)).sort((a, b) => a.sort_order - b.sort_order))
      cancelEdit()
    } catch (e) {
      setEditError(String(e))
    } finally {
      setEditSaving(false)
    }
  }

  async function handleDelete(rule: IntakeRule) {
    if (!confirm(`Delete intake rule "${rule.name}"?`)) return
    await api.intakeRules.delete(rule.id)
    setRules((rs) => rs.filter((r) => r.id !== rule.id))
  }

  // move reorders a rule up/down by swapping sort_order with its neighbour
  // and persisting both, mirroring the workflow label reorder UX.
  async function move(rule: IntakeRule, direction: -1 | 1) {
    const idx = rules.findIndex((r) => r.id === rule.id)
    const otherIdx = idx + direction
    if (idx < 0 || otherIdx < 0 || otherIdx >= rules.length) return
    const other = rules[otherIdx]
    const [a, b] = await Promise.all([
      api.intakeRules.update(rule.id, { ...formToBody(ruleToForm(rule)), sort_order: other.sort_order }),
      api.intakeRules.update(other.id, { ...formToBody(ruleToForm(other)), sort_order: rule.sort_order }),
    ])
    setRules((rs) =>
      rs.map((r) => (r.id === a.id ? a : r.id === b.id ? b : r)).sort((x, y) => x.sort_order - y.sort_order),
    )
  }

  async function runPreview(which: 'new' | string, f: RuleForm) {
    if (!f.match_repo_id) {
      setPreviewError('Select a repo to preview against.')
      return
    }
    setPreviewFor(which)
    setPreviewLoading(true)
    setPreviewError('')
    setPreviewResults(null)
    try {
      const res = await api.intakeRules.preview(f.match_repo_id, formToBody(f))
      setPreviewResults(res.matches ?? [])
    } catch (e) {
      setPreviewError(String(e))
    } finally {
      setPreviewLoading(false)
    }
  }

  const templateOptions = useMemo(() => templates, [templates])

  function renderAssocCheckboxes(f: RuleForm, setF: (fn: (f: RuleForm) => RuleForm) => void) {
    return (
      <div className="flex flex-wrap gap-3">
        {ALL_ASSOCIATIONS.map((a) => (
          <label key={a} className="flex items-center gap-1.5 text-xs text-slate-300">
            <input
              type="checkbox"
              checked={f.match_author_assoc.includes(a)}
              onChange={(e) =>
                setF((prev) => ({
                  ...prev,
                  match_author_assoc: e.target.checked
                    ? [...prev.match_author_assoc, a]
                    : prev.match_author_assoc.filter((x) => x !== a),
                }))
              }
            />
            {a}
            {!(TRUSTED_ASSOCIATIONS as readonly string[]).includes(a) && (
              <span className="text-slate-600">(untrusted)</span>
            )}
          </label>
        ))}
      </div>
    )
  }

  function renderFormFields(f: RuleForm, setF: (fn: (f: RuleForm) => RuleForm) => void, idPrefix: string) {
    const unsafe = autoStartUnsafe(f)
    const templateDisabledForSchedule = f.match_source === 'schedule'
    const templateWarn = templateNoOpForSchedule(f)
    return (
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Name</label>
            <input
              value={f.name}
              onChange={(e) => setF((p) => ({ ...p, name: e.target.value }))}
              placeholder="Bug triage"
              className={inputCls}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Source</label>
            <select
              value={f.match_source}
              onChange={(e) => setF((p) => ({ ...p, match_source: e.target.value as RuleForm['match_source'] }))}
              className={inputCls}
            >
              {MATCH_SOURCES.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Repo</label>
            <select
              value={f.match_repo_id}
              onChange={(e) => setF((p) => ({ ...p, match_repo_id: e.target.value }))}
              className={inputCls}
            >
              <option value="">Any repo</option>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Match labels (comma-separated, any-of)</label>
            <input
              value={f.match_labels}
              onChange={(e) => setF((p) => ({ ...p, match_labels: e.target.value }))}
              placeholder="bug, regression"
              className={inputCls}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Title pattern (Go regexp)</label>
            <input
              value={f.match_title_pattern}
              onChange={(e) => setF((p) => ({ ...p, match_title_pattern: e.target.value }))}
              placeholder="(?i)crash"
              className={inputCls}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Body pattern (Go regexp)</label>
            <input
              value={f.match_body_pattern}
              onChange={(e) => setF((p) => ({ ...p, match_body_pattern: e.target.value }))}
              className={inputCls}
            />
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <label className="text-xs font-medium text-slate-400">Author association (required to auto-start)</label>
          {renderAssocCheckboxes(f, setF)}
        </div>

        <div className="border-t border-slate-800 pt-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Apply template</label>
            <select
              value={f.apply_template_id}
              onChange={(e) => setF((p) => ({ ...p, apply_template_id: e.target.value }))}
              disabled={templateDisabledForSchedule}
              className={`${inputCls} disabled:opacity-50 disabled:cursor-not-allowed`}
            >
              <option value="">None</option>
              {templateOptions.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
            {templateDisabledForSchedule && (
              <p className="text-[11px] text-slate-500">
                Not available for schedule rules — scheduled tasks are always shaped from the schedule's own
                template.
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Priority</label>
            <select
              value={f.apply_priority}
              onChange={(e) => setF((p) => ({ ...p, apply_priority: e.target.value }))}
              className={inputCls}
            >
              <option value="">Leave default</option>
              {Object.entries(PRIORITY_LABELS).map(([v, label]) => (
                <option key={v} value={v}>
                  {label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Target label</label>
            <input
              value={f.apply_target_label}
              onChange={(e) => setF((p) => ({ ...p, apply_target_label: e.target.value }))}
              placeholder="Leave default (human-gate label)"
              className={inputCls}
              list={`${idPrefix}-labels`}
            />
            <datalist id={`${idPrefix}-labels`}>
              {effectiveWorkflowLabels(f).map((l) => (
                <option key={l.id} value={l.name} />
              ))}
            </datalist>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Workflow override</label>
            <select
              value={f.apply_workflow_id}
              onChange={(e) => setF((p) => ({ ...p, apply_workflow_id: e.target.value }))}
              className={inputCls}
            >
              <option value="">Use repo's workflow</option>
              {workflows.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Max cost (USD)</label>
            <input
              type="number"
              min="0"
              step="0.01"
              value={f.apply_max_cost_usd}
              onChange={(e) => setF((p) => ({ ...p, apply_max_cost_usd: e.target.value }))}
              placeholder="Leave default"
              className={inputCls}
            />
          </div>
          <div className="flex items-end">
            <label className="flex items-center gap-2 text-xs text-slate-300">
              <input
                type="checkbox"
                checked={f.enabled}
                onChange={(e) => setF((p) => ({ ...p, enabled: e.target.checked }))}
              />
              Enabled
            </label>
          </div>
        </div>

        {unsafe && (
          <div className="rounded-lg border border-amber-700/50 bg-amber-950/30 px-3 py-2.5 text-xs text-amber-300">
            <strong>Auto-start warning:</strong> this target label is not the workflow's human-review gate, so a
            matching task skips the human promotion step that protects against untrusted imported issue content.
            Restrict "Author association" above to OWNER / MEMBER / COLLABORATOR before this rule can be saved, or
            leave the target label empty to keep landing on the gate.
          </div>
        )}

        {templateWarn && (
          <div className="rounded-lg border border-amber-700/50 bg-amber-950/30 px-3 py-2.5 text-xs text-amber-300">
            <strong>No effect:</strong> "Apply template" has no effect for schedule rules — scheduled tasks are
            always shaped from the schedule's own template. Set "Apply template" to None to save this rule, or
            change Source to Issue import to shape imported issues with a template instead.
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="p-6 max-w-4xl">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-2">
          <h1 className="text-xl font-semibold text-slate-100">Intake Routing Rules</h1>
          <HelpButton onClick={() => setShowHelp(true)} title="About intake rules" />
        </div>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg transition-colors"
        >
          {showForm ? 'Cancel' : '+ Add Rule'}
        </button>
      </div>

      {showHelp && (
        <HelpModal title="About Intake Routing Rules" onClose={() => setShowHelp(false)}>
          <IntakeRulesHelp />
        </HelpModal>
      )}

      {showForm && (
        <form
          onSubmit={handleCreate}
          className="mb-6 bg-slate-900 border border-slate-700 rounded-xl p-5 flex flex-col gap-4"
        >
          <h2 className="text-sm font-semibold text-slate-200">New Rule</h2>
          {renderFormFields(form, setForm, 'new')}
          {formError && <p className="text-xs text-red-400">{formError}</p>}
          <div className="flex items-center justify-between">
            <button
              type="button"
              onClick={() => runPreview('new', form)}
              disabled={previewLoading}
              className="px-3 py-1.5 text-xs text-slate-300 hover:text-indigo-400 border border-slate-700 rounded-lg transition-colors disabled:opacity-50"
            >
              {previewLoading && previewFor === 'new' ? 'Previewing…' : 'Preview matches'}
            </button>
            <button
              type="submit"
              disabled={saving || !form.name.trim() || autoStartUnsafe(form) || templateNoOpForSchedule(form)}
              className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
            >
              {saving ? 'Adding…' : 'Add Rule'}
            </button>
          </div>
          {previewFor === 'new' && (
            <PreviewResultsPanel results={previewResults} error={previewError} />
          )}
        </form>
      )}

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : error && rules.length === 0 ? (
        <div className="text-red-400 text-sm">{error}</div>
      ) : rules.length === 0 ? (
        <div className="text-slate-500 text-sm">
          No intake rules yet. Issues and scheduled tasks land on their workflow's gate label by default.
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {rules.map((rule, idx) => (
            <div key={rule.id} className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden">
              <div className="px-5 py-4 flex items-center gap-3">
                <div className="flex flex-col shrink-0">
                  <button
                    onClick={() => move(rule, -1)}
                    disabled={idx === 0}
                    className="text-slate-500 hover:text-indigo-400 disabled:opacity-30 disabled:cursor-not-allowed text-xs leading-none"
                    title="Move up"
                  >
                    ▲
                  </button>
                  <button
                    onClick={() => move(rule, 1)}
                    disabled={idx === rules.length - 1}
                    className="text-slate-500 hover:text-indigo-400 disabled:opacity-30 disabled:cursor-not-allowed text-xs leading-none"
                    title="Move down"
                  >
                    ▼
                  </button>
                </div>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-slate-100 flex items-center gap-2">
                    {rule.name}
                    {!rule.enabled && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-slate-800 text-slate-500 border border-slate-700">
                        disabled
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-slate-500 mt-0.5 truncate">
                    {rule.match_source || 'any source'}
                    {rule.match_repo_id ? ` · ${repos.find((r) => r.id === rule.match_repo_id)?.name ?? rule.match_repo_id}` : ' · any repo'}
                    {rule.match_labels && rule.match_labels.length > 0 ? ` · labels: ${rule.match_labels.join(', ')}` : ''}
                  </div>
                </div>
                <button
                  onClick={() => runPreview(rule.id, ruleToForm(rule))}
                  className="text-xs text-slate-500 hover:text-indigo-400 transition-colors shrink-0"
                >
                  Preview
                </button>
                <button
                  onClick={() => (editingId === rule.id ? cancelEdit() : startEdit(rule))}
                  className="text-xs text-slate-500 hover:text-indigo-400 transition-colors shrink-0"
                >
                  {editingId === rule.id ? 'Cancel' : 'Edit'}
                </button>
                <button
                  onClick={() => handleDelete(rule)}
                  className="text-xs text-slate-600 hover:text-red-400 transition-colors shrink-0"
                >
                  Delete
                </button>
              </div>

              {previewFor === rule.id && (
                <div className="border-t border-slate-800 px-5 py-3">
                  <PreviewResultsPanel results={previewResults} error={previewError} />
                </div>
              )}

              {editingId === rule.id && (
                <form
                  onSubmit={handleUpdate}
                  className="border-t border-slate-700 bg-slate-900 px-5 py-4 flex flex-col gap-4"
                >
                  <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Edit Rule</h3>
                  {renderFormFields(editForm, setEditForm, `edit-${rule.id}`)}
                  {editError && <p className="text-xs text-red-400">{editError}</p>}
                  <div className="flex items-center justify-end gap-2">
                    <button
                      type="button"
                      onClick={cancelEdit}
                      className="px-3 py-1.5 text-sm text-slate-400 hover:text-slate-200 transition-colors"
                    >
                      Cancel
                    </button>
                    <button
                      type="submit"
                      disabled={editSaving || !editForm.name.trim() || autoStartUnsafe(editForm) || templateNoOpForSchedule(editForm)}
                      className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
                    >
                      {editSaving ? 'Saving…' : 'Save'}
                    </button>
                  </div>
                </form>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function PreviewResultsPanel({ results, error }: { results: IntakeRulePreviewMatch[] | null; error: string }) {
  if (error) return <p className="text-xs text-red-400">{error}</p>
  if (results === null) return null
  if (results.length === 0) {
    return <p className="text-xs text-slate-500">No recently-imported tasks for this repo to preview against.</p>
  }
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-xs text-slate-500">
        Checked against the most recently imported tasks for this repo:
      </p>
      <ul className="flex flex-col gap-1">
        {results.map((m) => (
          <li key={m.task_id} className="text-xs flex items-center gap-2">
            <span className={m.matched ? 'text-emerald-400' : 'text-slate-600'}>{m.matched ? '✓' : '·'}</span>
            <span className="text-slate-300 truncate">{m.title}</span>
            {m.matched && m.target_label && (
              <span className="text-slate-500 shrink-0">→ {m.target_label}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
