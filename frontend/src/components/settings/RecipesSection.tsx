import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  type AutomationRecipe,
  type AutomationRecipeInstallResult,
  type Repo,
  type Workflow,
} from '../../api/client'
import ConfirmDialog from '../shared/ConfirmDialog'

const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

/**
 * RecipesSection: a gallery of one-click automation presets. Installing a
 * recipe creates ordinary templates / schedules / intake endpoints + rules
 * tagged with the recipe id; "Remove recipe" deletes only those.
 */
export default function RecipesSection() {
  const [recipes, setRecipes] = useState<AutomationRecipe[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loadError, setLoadError] = useState('')
  const [installing, setInstalling] = useState<string | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [confirmAuto, setConfirmAuto] = useState(false)
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<AutomationRecipeInstallResult | null>(null)
  const [toRemove, setToRemove] = useState<AutomationRecipe | null>(null)

  const load = useCallback(async () => {
    try {
      const [rc, rs, wfs] = await Promise.all([api.automationRecipes.list(), api.repos.list(), api.workflows.list()])
      setRecipes(rc)
      setRepos(rs)
      setWorkflows(wfs)
      setLoadError('')
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : 'Failed to load recipes')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const active = recipes.find((r) => r.id === installing) ?? null

  const labels = useMemo(() => {
    const repo = repos.find((r) => r.id === values.repo_id)
    const wfId = values.workflow_id || repo?.workflow_id || ''
    return workflows.find((w) => w.id === wfId)?.labels ?? []
  }, [repos, workflows, values.repo_id, values.workflow_id])

  const autoStart = !!values.target_label && labels.some((l) => l.name === values.target_label && !l.agent_ignore)

  function openInstall(r: AutomationRecipe) {
    const v: Record<string, string> = {}
    for (const p of r.params) v[p.key] = p.default
    v.repo_id = repos[0]?.id ?? ''
    setValues(v)
    setConfirmAuto(false)
    setFormError('')
    setInstalling(r.id)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!active) return
    setBusy(true)
    setFormError('')
    try {
      const params: Record<string, string> = {}
      for (const p of active.params) if (values[p.key]) params[p.key] = values[p.key]
      const res = await api.automationRecipes.install(active.id, { params, confirm_auto_start: autoStart && confirmAuto })
      setResult(res)
      setInstalling(null)
      await load()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Install failed')
    } finally {
      setBusy(false)
    }
  }

  async function performRemove(r: AutomationRecipe) {
    await api.automationRecipes.remove(r.id)
    setToRemove(null)
    await load()
  }

  const set = (k: string, v: string) => setValues((cur) => ({ ...cur, [k]: v, ...(k === 'repo_id' || k === 'workflow_id' ? { target_label: '' } : {}) }))

  return (
    <section className="mb-10" aria-label="Recipes">
      <h2 className="text-lg font-semibold text-slate-100 mb-1">Recipes</h2>
      <p className="text-xs text-slate-500 mb-4">
        One-click presets that create a template plus a schedule and/or a webhook endpoint with a rule. Tasks land parked
        on the human-review label unless you pick a target label.
      </p>
      {loadError && <p className="text-xs text-red-400 mb-3">{loadError}</p>}

      {result && (
        <div className="mb-4 bg-slate-900 border border-amber-700 rounded-xl p-4 flex flex-col gap-2" role="status">
          <p className="text-sm text-amber-300">
            Installed recipe &quot;{result.recipe}&quot;.
            {result.secret ? ' Copy the secret now — it won’t be shown again.' : ''}
          </p>
          {result.warnings.map((w) => (
            <p key={w} className="text-xs text-amber-400">
              {w}
            </p>
          ))}
          {result.secret && (
            <>
              <p className="text-xs text-slate-400">URL</p>
              <code className="text-xs text-slate-100 break-all">{result.url}</code>
              <p className="text-xs text-slate-400">Secret</p>
              <code className="text-xs text-slate-100 break-all">{result.secret}</code>
              <p className="text-xs text-slate-400">Sender snippet (store the secret as ATE_INTAKE_SECRET)</p>
              <pre className="text-xs text-slate-300 bg-slate-950 rounded p-2 overflow-x-auto">{result.snippet}</pre>
              <div>
                <button
                  onClick={() => void navigator.clipboard?.writeText(result.secret ?? '')}
                  className="px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 text-slate-100 rounded mr-2"
                >
                  Copy secret
                </button>
                <button
                  onClick={() => void navigator.clipboard?.writeText(result.snippet ?? '')}
                  className="px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 text-slate-100 rounded"
                >
                  Copy snippet
                </button>
              </div>
            </>
          )}
          <div>
            <button onClick={() => setResult(null)} className="px-2 py-1 text-xs text-slate-400 hover:text-slate-200">
              Dismiss
            </button>
          </div>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        {recipes.map((r) => (
          <div key={r.id} className="bg-slate-900 border border-slate-800 rounded-xl p-4 flex flex-col gap-2" data-testid={`recipe-${r.id}`}>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-medium text-slate-100">{r.name}</h3>
              <span className="text-[10px] uppercase tracking-wide text-slate-500">{r.kind === 'webhook' ? 'webhook' : 'schedule'}</span>
              {r.installed && <span className="ml-auto text-xs text-emerald-400">Installed</span>}
            </div>
            <p className="text-xs text-slate-400">{r.description}</p>
            <div className="mt-auto flex gap-2">
              {r.installed ? (
                <button onClick={() => setToRemove(r)} className="px-3 py-1.5 text-xs text-red-400 hover:text-red-300">
                  Remove recipe
                </button>
              ) : (
                <button
                  onClick={() => (installing === r.id ? setInstalling(null) : openInstall(r))}
                  className="px-3 py-1.5 text-xs bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg"
                >
                  {installing === r.id ? 'Cancel' : 'Install'}
                </button>
              )}
            </div>
            {installing === r.id && (
              <form onSubmit={submit} className="flex flex-col gap-2 border-t border-slate-800 pt-3" aria-label={`Install ${r.name}`}>
                {r.params.map((p) => (
                  <label key={p.key} className="flex flex-col gap-1 text-xs text-slate-400">
                    {p.label}
                    {p.kind === 'repo' ? (
                      <select className={inputCls} value={values[p.key] ?? ''} onChange={(e) => set(p.key, e.target.value)}>
                        {repos.map((rp) => (
                          <option key={rp.id} value={rp.id}>
                            {rp.name}
                          </option>
                        ))}
                      </select>
                    ) : p.kind === 'workflow' ? (
                      <select className={inputCls} value={values[p.key] ?? ''} onChange={(e) => set(p.key, e.target.value)}>
                        <option value="">Repo&apos;s workflow</option>
                        {workflows.map((w) => (
                          <option key={w.id} value={w.id}>
                            {w.name}
                          </option>
                        ))}
                      </select>
                    ) : p.kind === 'label' ? (
                      <select className={inputCls} value={values[p.key] ?? ''} onChange={(e) => set(p.key, e.target.value)}>
                        <option value="">Parked (human review)</option>
                        {labels.map((l) => (
                          <option key={l.id} value={l.name}>
                            {l.name}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <input className={inputCls} value={values[p.key] ?? ''} onChange={(e) => set(p.key, e.target.value)} />
                    )}
                    {p.help && <span className="text-[11px] text-slate-500">{p.help}</span>}
                  </label>
                ))}
                {autoStart && (
                  <label className="flex items-start gap-2 text-xs text-amber-400">
                    <input type="checkbox" checked={confirmAuto} onChange={(e) => setConfirmAuto(e.target.checked)} className="mt-0.5" />
                    <span>
                      Tasks will start an agent without human review{r.kind === 'webhook' ? ' (webhook payload text is untrusted)' : ''}. I
                      understand.
                    </span>
                  </label>
                )}
                {formError && <p className="text-xs text-red-400">{formError}</p>}
                <div>
                  <button
                    type="submit"
                    disabled={busy || !values.repo_id || (autoStart && !confirmAuto)}
                    className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white rounded-lg"
                  >
                    {busy ? 'Installing…' : 'Install recipe'}
                  </button>
                </div>
              </form>
            )}
          </div>
        ))}
      </div>

      {toRemove && (
        <ConfirmDialog
          destructive
          title={`Remove recipe "${toRemove.name}"?`}
          confirmLabel="Remove recipe"
          onCancel={() => setToRemove(null)}
          onConfirm={() => performRemove(toRemove)}
        >
          <p>Deletes only the template, schedule, endpoint and rule this recipe created. Tasks already created are kept.</p>
        </ConfirmDialog>
      )}
    </section>
  )
}
