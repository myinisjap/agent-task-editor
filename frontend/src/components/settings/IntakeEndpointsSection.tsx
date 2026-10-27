import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  BASE,
  type IntakeEndpoint,
  type IntakeEndpointBody,
  type IntakeEndpointTestResponse,
  type Repo,
  type Workflow,
} from '../../api/client'

const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

type Form = {
  name: string
  repo_id: string
  workflow_id: string
  target_label: string
  title_template: string
  description_template: string
  dedupe_key_template: string
}

const emptyForm: Form = {
  name: '',
  repo_id: '',
  workflow_id: '',
  target_label: '',
  title_template: '{{alert.title}}',
  description_template: '',
  dedupe_key_template: '',
}

function toBody(f: Form, enabled?: boolean): IntakeEndpointBody {
  return {
    name: f.name.trim(),
    repo_id: f.repo_id,
    workflow_id: f.workflow_id || null,
    target_label: f.target_label,
    title_template: f.title_template,
    description_template: f.description_template,
    dedupe_key_template: f.dedupe_key_template,
    ...(enabled === undefined ? {} : { enabled }),
  }
}

function fullUrl(ep: IntakeEndpoint) {
  return `${window.location.origin}${BASE}/intake/${ep.id}`
}

function curlExample(url: string, secret: string) {
  return [
    `BODY='{"alert":{"title":"CI failed on main"}}'`,
    `SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac '${secret}' | sed 's/^.* //')`,
    `curl -X POST '${url}' \\`,
    `  -H 'Content-Type: application/json' -H "X-ATE-Signature-256: sha256=$SIG" -d "$BODY"`,
  ].join('\n')
}

export default function IntakeEndpointsSection() {
  const [endpoints, setEndpoints] = useState<IntakeEndpoint[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loadError, setLoadError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<Form>(emptyForm)
  const [formError, setFormError] = useState('')
  const [secretInfo, setSecretInfo] = useState<{ ep: IntakeEndpoint; secret: string } | null>(null)
  const [testPayload, setTestPayload] = useState('{\n  "alert": { "title": "CI failed on main", "id": "123" }\n}')
  const [testResult, setTestResult] = useState<IntakeEndpointTestResponse | null>(null)
  const [testError, setTestError] = useState('')

  const load = useCallback(async () => {
    try {
      const [eps, rs, wfs] = await Promise.all([api.intakeEndpoints.list(), api.repos.list(), api.workflows.list()])
      setEndpoints(eps)
      setRepos(rs)
      setWorkflows(wfs)
      setLoadError('')
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : 'Failed to load webhook endpoints')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const effectiveLabels = useMemo(() => {
    const repo = repos.find((r) => r.id === form.repo_id)
    const wfId = form.workflow_id || repo?.workflow_id || ''
    return workflows.find((w) => w.id === wfId)?.labels ?? []
  }, [repos, workflows, form.repo_id, form.workflow_id])

  const targetIsAgentTriggerable = useMemo(
    () => !!form.target_label && effectiveLabels.some((l) => l.name === form.target_label && !l.agent_ignore),
    [effectiveLabels, form.target_label],
  )

  function openNew() {
    setEditingId(null)
    setForm({ ...emptyForm, repo_id: repos[0]?.id ?? '' })
    setFormError('')
    setTestResult(null)
    setTestError('')
    setShowForm(true)
  }

  function openEdit(ep: IntakeEndpoint) {
    setEditingId(ep.id)
    setForm({
      name: ep.name,
      repo_id: ep.repo_id,
      workflow_id: ep.workflow_id ?? '',
      target_label: ep.target_label,
      title_template: ep.title_template,
      description_template: ep.description_template,
      dedupe_key_template: ep.dedupe_key_template,
    })
    setFormError('')
    setTestResult(null)
    setTestError('')
    setShowForm(true)
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault()
    setFormError('')
    try {
      if (editingId) {
        const cur = endpoints.find((x) => x.id === editingId)
        const updated = await api.intakeEndpoints.update(editingId, toBody(form, cur?.enabled))
        setEndpoints((eps) => eps.map((x) => (x.id === updated.id ? updated : x)))
      } else {
        const created = await api.intakeEndpoints.create(toBody(form))
        const { secret, ...ep } = created
        setEndpoints((eps) => [ep, ...eps])
        setSecretInfo({ ep, secret })
      }
      setShowForm(false)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save endpoint')
    }
  }

  async function toggleEnabled(ep: IntakeEndpoint) {
    try {
      const updated = await api.intakeEndpoints.update(ep.id, {
        name: ep.name,
        repo_id: ep.repo_id,
        workflow_id: ep.workflow_id,
        target_label: ep.target_label,
        title_template: ep.title_template,
        description_template: ep.description_template,
        dedupe_key_template: ep.dedupe_key_template,
        enabled: !ep.enabled,
      })
      setEndpoints((eps) => eps.map((x) => (x.id === updated.id ? updated : x)))
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to update endpoint')
    }
  }

  async function handleDelete(ep: IntakeEndpoint) {
    if (!window.confirm(`Delete webhook endpoint "${ep.name}"?`)) return
    try {
      await api.intakeEndpoints.delete(ep.id)
      setEndpoints((eps) => eps.filter((x) => x.id !== ep.id))
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to delete endpoint')
    }
  }

  async function handleRotate(ep: IntakeEndpoint) {
    if (!window.confirm('Rotate the secret? The old secret stops working immediately.')) return
    try {
      const { secret } = await api.intakeEndpoints.rotateSecret(ep.id)
      setSecretInfo({ ep, secret })
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to rotate secret')
    }
  }

  async function handleTest() {
    setTestError('')
    setTestResult(null)
    if (!editingId) {
      setTestError('Save the endpoint first, then edit it to test with a sample payload.')
      return
    }
    let payload: Record<string, unknown>
    try {
      payload = JSON.parse(testPayload)
    } catch {
      setTestError('Sample payload is not valid JSON')
      return
    }
    try {
      setTestResult(
        await api.intakeEndpoints.test(editingId, {
          payload,
          title_template: form.title_template,
          description_template: form.description_template,
          dedupe_key_template: form.dedupe_key_template,
        }),
      )
    } catch (err) {
      setTestError(err instanceof Error ? err.message : 'Test failed')
    }
  }

  const repoName = (id: string) => repos.find((r) => r.id === id)?.name ?? id

  return (
    <section className="mt-10" aria-label="Webhook endpoints">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold text-slate-100">Webhook endpoints</h2>
        <button
          onClick={() => (showForm ? setShowForm(false) : openNew())}
          className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg transition-colors"
        >
          {showForm ? 'Cancel' : '+ Add Endpoint'}
        </button>
      </div>
      <p className="text-xs text-slate-500 mb-4">
        Signed inbound events (CI, error trackers, scanners) create tasks. Payload text is untrusted: tasks land on the
        workflow&apos;s human-review label unless you pick a target label.
      </p>
      {loadError && <p className="text-xs text-red-400 mb-3">{loadError}</p>}

      {secretInfo && (
        <div className="mb-4 bg-slate-900 border border-amber-700 rounded-xl p-4 flex flex-col gap-2" role="status">
          <p className="text-sm text-amber-300">
            Secret for &quot;{secretInfo.ep.name}&quot; — copy it now, it won&apos;t be shown again.
          </p>
          <code className="text-xs text-slate-100 break-all">{secretInfo.secret}</code>
          <div className="flex gap-2">
            <button
              onClick={() => void navigator.clipboard?.writeText(secretInfo.secret)}
              className="px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 text-slate-100 rounded"
            >
              Copy secret
            </button>
            <button onClick={() => setSecretInfo(null)} className="px-2 py-1 text-xs text-slate-400 hover:text-slate-200">
              Dismiss
            </button>
          </div>
          <pre className="text-xs text-slate-400 bg-slate-950 rounded p-2 overflow-x-auto">
            {curlExample(fullUrl(secretInfo.ep), secretInfo.secret)}
          </pre>
        </div>
      )}

      {showForm && (
        <form onSubmit={handleSave} className="mb-5 bg-slate-900 border border-slate-700 rounded-xl p-5 flex flex-col gap-3">
          <h3 className="text-sm font-semibold text-slate-200">{editingId ? 'Edit endpoint' : 'New endpoint'}</h3>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Name
            <input className={inputCls} value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} placeholder="CI alerts" />
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Repo
            <select className={inputCls} value={form.repo_id} onChange={(e) => setForm((f) => ({ ...f, repo_id: e.target.value, target_label: '' }))}>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Workflow
            <select className={inputCls} value={form.workflow_id} onChange={(e) => setForm((f) => ({ ...f, workflow_id: e.target.value, target_label: '' }))}>
              <option value="">Repo&apos;s workflow</option>
              {workflows.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Target label
            <select className={inputCls} value={form.target_label} onChange={(e) => setForm((f) => ({ ...f, target_label: e.target.value }))}>
              <option value="">Gate (human review)</option>
              {effectiveLabels.map((l) => (
                <option key={l.id} value={l.name}>
                  {l.name}
                </option>
              ))}
            </select>
          </label>
          {targetIsAgentTriggerable && (
            <p className="text-xs text-amber-400">
              Payload text is untrusted; tasks will start without human review.
            </p>
          )}
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Title template
            <input className={inputCls} value={form.title_template} onChange={(e) => setForm((f) => ({ ...f, title_template: e.target.value }))} placeholder="{{alert.title}}" />
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Description template (empty = the payload as JSON)
            <textarea rows={3} className={inputCls} value={form.description_template} onChange={(e) => setForm((f) => ({ ...f, description_template: e.target.value }))} placeholder="{{alert.url}}" />
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Dedupe key template (empty = never dedupe)
            <input className={inputCls} value={form.dedupe_key_template} onChange={(e) => setForm((f) => ({ ...f, dedupe_key_template: e.target.value }))} placeholder="{{alert.id}}" />
          </label>

          <div className="flex flex-col gap-2 border-t border-slate-800 pt-3">
            <label className="flex flex-col gap-1 text-xs text-slate-400">
              Sample payload
              <textarea rows={4} className={`${inputCls} font-mono`} value={testPayload} onChange={(e) => setTestPayload(e.target.value)} />
            </label>
            <div>
              <button type="button" onClick={() => void handleTest()} className="px-3 py-1.5 text-xs bg-slate-700 hover:bg-slate-600 text-slate-100 rounded-lg">
                Test payload
              </button>
            </div>
            {testError && <p className="text-xs text-red-400">{testError}</p>}
            {testResult && (
              <dl className="text-xs text-slate-300 grid grid-cols-[8rem_1fr] gap-1" aria-label="Test result">
                <dt className="text-slate-500">Title</dt>
                <dd>{testResult.title}</dd>
                <dt className="text-slate-500">Description</dt>
                <dd className="whitespace-pre-wrap">{testResult.description}</dd>
                <dt className="text-slate-500">Dedupe key</dt>
                <dd>{testResult.dedupe_key || '—'}</dd>
                <dt className="text-slate-500">Label</dt>
                <dd>{testResult.label}</dd>
                <dt className="text-slate-500">Matched rule</dt>
                <dd>{testResult.matched_rule_id ?? 'none'}</dd>
                <dt className="text-slate-500">Existing task</dt>
                <dd>{testResult.existing_task_id ?? 'none (a new task would be created)'}</dd>
              </dl>
            )}
          </div>

          {formError && <p className="text-xs text-red-400">{formError}</p>}
          <div>
            <button type="submit" className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg">
              {editingId ? 'Save' : 'Create endpoint'}
            </button>
          </div>
        </form>
      )}

      {endpoints.length === 0 ? (
        <p className="text-sm text-slate-500">No webhook endpoints yet.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {endpoints.map((ep) => (
            <li key={ep.id} className="bg-slate-900 border border-slate-800 rounded-xl p-3 flex flex-col gap-1.5">
              <div className="flex items-center gap-3">
                <span className="text-sm font-medium text-slate-100">{ep.name}</span>
                <span className="text-xs text-slate-500">{repoName(ep.repo_id)}</span>
                <span className="text-xs text-slate-500">→ {ep.target_label || 'gate (human review)'}</span>
                <label className="ml-auto flex items-center gap-1.5 text-xs text-slate-400">
                  <input type="checkbox" checked={ep.enabled} onChange={() => void toggleEnabled(ep)} aria-label={`Enable ${ep.name}`} />
                  Enabled
                </label>
              </div>
              <div className="flex items-center gap-2 text-xs text-slate-500">
                <code className="truncate">{fullUrl(ep)}</code>
                <span>· {ep.last_received_at ? `last received ${new Date(ep.last_received_at).toLocaleString()}` : 'never received'}</span>
              </div>
              <div className="flex gap-2 text-xs">
                <button onClick={() => void navigator.clipboard?.writeText(fullUrl(ep))} className="text-slate-400 hover:text-slate-200">Copy URL</button>
                <button onClick={() => openEdit(ep)} className="text-slate-400 hover:text-slate-200">Edit</button>
                <button onClick={() => void handleRotate(ep)} className="text-slate-400 hover:text-slate-200">Rotate secret</button>
                <button onClick={() => void handleDelete(ep)} className="text-red-400 hover:text-red-300">Delete</button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
