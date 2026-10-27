import { useEffect, useMemo, useState } from 'react'
import { api, type Skill } from '../api/client'
import { useAgentsStore } from '../stores/agents'
import { useReposStore } from '../stores/repos'

const MAX_ZIP_BYTES = 1024 * 1024
const btnCls = 'px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-200'
const chipCls = 'px-2 py-0.5 rounded-full text-xs bg-slate-800 border border-slate-700 text-slate-300 break-words'

const byName = (a: Skill, b: Skill) => a.name.localeCompare(b.name)

export default function SkillsSettingsPage() {
  const agents = useAgentsStore((s) => s.configs)
  const fetchAgents = useAgentsStore((s) => s.fetch)
  const repos = useReposStore((s) => s.repos)
  const fetchRepos = useReposStore((s) => s.fetch)

  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({})
  const [mode, setMode] = useState<'paste' | 'zip'>('paste')
  const [md, setMd] = useState('')
  const [zip, setZip] = useState<File | null>(null)
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [viewId, setViewId] = useState<string | null>(null)
  const [detail, setDetail] = useState<Record<string, Skill>>({})
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)

  useEffect(() => {
    api.skills
      .list()
      .then((l) => setSkills((l ?? []).slice().sort(byName)))
      .catch((e: any) => setError(e?.message ?? String(e)))
      .finally(() => setLoading(false))
    void fetchAgents()
    void fetchRepos()
  }, [fetchAgents, fetchRepos])

  const agentName = useMemo(() => new Map(agents.map((a) => [a.id, a.name])), [agents])
  const repoName = useMemo(() => new Map(repos.map((r) => [r.id, r.name])), [repos])

  const setRowError = (id: string, msg: string) => setRowErrors((r) => ({ ...r, [id]: msg }))

  const onFile = (f: File | null) => {
    setFormError('')
    if (f && f.size > MAX_ZIP_BYTES) {
      setZip(null)
      setFormError('Zip is larger than 1 MB.')
      return
    }
    setZip(f)
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setFormError('')
    if (mode === 'paste' && !md.trim()) return setFormError('Paste a SKILL.md first.')
    if (mode === 'zip' && !zip) return setFormError('Choose a zip file first.')
    setSaving(true)
    try {
      const created = mode === 'paste' ? await api.skills.createFromMarkdown(md) : await api.skills.createFromZip(zip!)
      setSkills((l) => [...l, created].sort(byName))
      setMd('')
      setZip(null)
    } catch (err: any) {
      setFormError(err?.message ?? String(err))
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (s: Skill, enabled: boolean) => {
    setRowError(s.id, '')
    try {
      const updated = await api.skills.update(s.id, { enabled })
      setSkills((l) => l.map((x) => (x.id === s.id ? { ...x, enabled: updated?.enabled ?? enabled } : x)))
    } catch (err: any) {
      setRowError(s.id, err?.message ?? String(err))
    }
  }

  const view = (s: Skill) => {
    if (viewId === s.id) return setViewId(null)
    setViewId(s.id)
    if (detail[s.id]) return
    api.skills
      .get(s.id)
      .then((d) => setDetail((m) => ({ ...m, [s.id]: d })))
      .catch((err: any) => setRowError(s.id, err?.message ?? String(err)))
  }

  const remove = async (s: Skill) => {
    try {
      await api.skills.delete(s.id)
      setSkills((l) => l.filter((x) => x.id !== s.id))
      setConfirmDeleteId(null)
      if (viewId === s.id) setViewId(null)
    } catch (err: any) {
      setRowError(s.id, err?.message ?? String(err))
    }
  }

  return (
    <div className="p-4 md:p-6 max-w-3xl">
      <h2 className="text-lg font-semibold text-slate-100 mb-1">Skills</h2>
      <p className="text-sm text-slate-400 mb-4">
        Agent Skills (a SKILL.md plus optional files). Enabled skills attached to an agent or its repo are copied into
        the worktree at run start. See docs/skills.md.
      </p>

      <form onSubmit={submit} className="mb-6 p-3 rounded border border-slate-700 bg-slate-900 space-y-2">
        <div className="flex gap-2" role="tablist">
          {(['paste', 'zip'] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="tab"
              aria-selected={mode === m}
              onClick={() => { setMode(m); setFormError('') }}
              className={`px-3 py-1 text-xs rounded ${mode === m ? 'bg-indigo-600 text-white' : 'bg-slate-800 text-slate-400'}`}
            >
              {m === 'paste' ? 'Paste SKILL.md' : 'Upload zip'}
            </button>
          ))}
        </div>
        {mode === 'paste' ? (
          <textarea
            aria-label="SKILL.md contents"
            value={md}
            onChange={(e) => setMd(e.target.value)}
            rows={8}
            placeholder={'---\nname: my-skill\ndescription: What it does\n---\n'}
            className="w-full bg-slate-800 border border-slate-700 rounded p-2 text-xs font-mono text-slate-100"
          />
        ) : (
          <input
            type="file"
            aria-label="Skill zip file"
            accept=".zip,application/zip"
            onChange={(e) => onFile(e.target.files?.[0] ?? null)}
            className="w-full text-xs text-slate-300"
          />
        )}
        {formError && <p className="text-xs text-red-400 break-words">{formError}</p>}
        <button type="submit" disabled={saving} className="px-3 py-1 text-xs rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50">
          {saving ? 'Adding…' : 'Add skill'}
        </button>
      </form>

      {error && <p className="text-sm text-red-400 mb-2">{error}</p>}
      {loading ? (
        <p className="text-sm text-slate-500">Loading…</p>
      ) : skills.length === 0 ? (
        <p className="text-sm text-slate-500">No skills yet.</p>
      ) : (
        <ul className="space-y-2">
          {skills.map((s) => {
            const aIds = s.agent_config_ids ?? []
            const rIds = s.repo_ids ?? []
            const d = detail[s.id]
            return (
              <li key={s.id} className="p-3 rounded border border-slate-700 bg-slate-900">
                <div className="flex flex-col sm:flex-row sm:items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="font-mono text-sm text-slate-100 break-words">{s.name}</div>
                    <div className="text-xs text-slate-400 break-words">{s.description}</div>
                    <div className="mt-1 flex flex-wrap items-center gap-1 text-xs text-slate-500">
                      <span>Attached to:</span>
                      {aIds.length + rIds.length === 0 && <span>Not attached</span>}
                      {aIds.map((id) => <span key={`a${id}`} className={chipCls}>Agent: {agentName.get(id) ?? 'Unknown'}</span>)}
                      {rIds.map((id) => <span key={`r${id}`} className={chipCls}>Repo: {repoName.get(id) ?? 'Unknown'}</span>)}
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <label className="flex items-center gap-1 text-xs text-slate-300">
                      <input
                        type="checkbox"
                        aria-label={`Enable ${s.name}`}
                        checked={s.enabled}
                        onChange={(e) => toggle(s, e.target.checked)}
                      />
                      Enabled
                    </label>
                    <button type="button" className={btnCls} onClick={() => view(s)}>
                      {viewId === s.id ? 'Hide' : 'View'}
                    </button>
                    {confirmDeleteId === s.id ? (
                      <>
                        <button type="button" className="px-2 py-1 text-xs rounded bg-red-600 hover:bg-red-500 text-white" onClick={() => remove(s)}>
                          Confirm delete
                        </button>
                        <button type="button" className={btnCls} onClick={() => setConfirmDeleteId(null)}>Cancel</button>
                      </>
                    ) : (
                      <button type="button" className={btnCls} onClick={() => setConfirmDeleteId(s.id)}>Delete</button>
                    )}
                  </div>
                </div>
                {confirmDeleteId === s.id && (
                  <p className="mt-1 text-xs text-amber-400">
                    Deletes this skill. Also detaches it from {aIds.length} agents / {rIds.length} repos.
                  </p>
                )}
                {rowErrors[s.id] && <p className="mt-1 text-xs text-red-400 break-words">{rowErrors[s.id]}</p>}
                {viewId === s.id && (
                  <div className="mt-2">
                    {!d ? (
                      <p className="text-xs text-slate-500">Loading…</p>
                    ) : (
                      <>
                        <pre className="whitespace-pre-wrap break-words text-xs max-h-96 overflow-auto bg-slate-950 p-2 rounded text-slate-300">{d.skill_md}</pre>
                        {(d.files ?? []).length > 0 && (
                          <ul className="mt-1 text-xs text-slate-400 font-mono break-words">
                            {d.files!.map((f) => <li key={f}>{f}</li>)}
                          </ul>
                        )}
                      </>
                    )}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
