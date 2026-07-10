import { useCallback, useEffect, useState } from 'react'
import { api, type ProviderCheck, type ProviderCheckStatus } from '../api/client'

const STATUS_META: Record<ProviderCheckStatus, { dot: string; label: string; labelCls: string }> = {
  ok: { dot: 'bg-green-500', label: 'Ready', labelCls: 'text-green-400' },
  warn: { dot: 'bg-yellow-500', label: 'Warning', labelCls: 'text-yellow-400' },
  error: { dot: 'bg-red-500', label: 'Not ready', labelCls: 'text-red-400' },
}

// backupFilenameFromContentDisposition extracts the filename from a
// Content-Disposition: attachment; filename="..." header value, falling
// back to a client-side timestamped name if the header is missing/unparseable.
function backupFilenameFromContentDisposition(header: string | null): string {
  if (header) {
    const match = /filename="?([^";]+)"?/.exec(header)
    if (match?.[1]) return match[1]
  }
  const ts = new Date().toISOString().replace(/[:.]/g, '-')
  return `agent-task-editor-backup-${ts}.db`
}

export default function HealthPage() {
  const [checks, setChecks] = useState<ProviderCheck[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [backupLoading, setBackupLoading] = useState(false)
  const [backupError, setBackupError] = useState('')

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    api.health.providers()
      .then((r) => setChecks(r.checks ?? []))
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const downloadBackup = useCallback(async () => {
    setBackupLoading(true)
    setBackupError('')
    try {
      const res = await fetch(api.backup.url(), {
        headers: import.meta.env.VITE_API_TOKEN
          ? { Authorization: `Bearer ${import.meta.env.VITE_API_TOKEN}` }
          : {},
      })
      if (!res.ok) {
        const body = await res.json().catch(() => null) as { error?: string } | null
        throw new Error(body?.error ?? `${res.status} ${res.statusText}`)
      }
      const blob = await res.blob()
      const filename = backupFilenameFromContentDisposition(res.headers.get('Content-Disposition'))
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e) {
      setBackupError(`Backup failed: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setBackupLoading(false)
    }
  }, [])

  const problems = (checks ?? []).filter((c) => c.status !== 'ok').length

  return (
    <div className="p-6 max-w-3xl">
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-xl font-semibold text-slate-100">Provider Health</h1>
        <button
          onClick={load}
          disabled={loading}
          className="px-3 py-1.5 text-sm bg-slate-800 hover:bg-slate-700 disabled:opacity-50 text-slate-200 rounded-lg transition-colors"
        >
          {loading ? 'Checking…' : 'Refresh'}
        </button>
      </div>
      <p className="text-sm text-slate-400 mb-6">
        Readiness of the agent providers and supporting infrastructure. Fix any red or
        yellow row before running your first task to avoid a failed run.
      </p>

      {error && (
        <div className="mb-4 bg-red-900/40 border border-red-700 text-red-200 text-sm px-3 py-2 rounded-lg">
          {error}
        </div>
      )}

      {checks && !error && (
        <div className="mb-4 text-sm text-slate-400">
          {problems === 0
            ? <span className="text-green-400">All checks passing.</span>
            : <span>{problems} {problems === 1 ? 'item needs' : 'items need'} attention.</span>}
        </div>
      )}

      <div className="flex flex-col gap-2">
        {loading && !checks && (
          <div className="text-sm text-slate-500">Running checks…</div>
        )}

        {(checks ?? []).map((c) => {
          const meta = STATUS_META[c.status]
          return (
            <div
              key={c.id}
              className="bg-slate-900 border border-slate-700 rounded-xl p-4 flex items-start gap-3"
            >
              <span className={`mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full ${meta.dot}`} aria-hidden />
              <div className="flex-1 min-w-0">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-sm font-medium text-slate-100">{c.name}</span>
                  <span className={`text-xs font-medium ${meta.labelCls}`}>{meta.label}</span>
                </div>
                <div className="text-xs text-slate-400 mt-0.5 break-words">{c.detail}</div>
                {c.hint && c.status !== 'ok' && (
                  <div className="text-xs text-slate-500 mt-1.5 break-words">
                    <span className="text-slate-400">Fix:</span> {c.hint}
                  </div>
                )}
              </div>
            </div>
          )
        })}
      </div>

      <div className="mt-8 pt-6 border-t border-slate-800">
        <h2 className="text-base font-semibold text-slate-100 mb-1">Backup</h2>
        <p className="text-sm text-slate-400 mb-3">
          Download a consistent point-in-time snapshot of the database. Safe to run while
          the app is in use. See <code className="text-slate-300">docs/backup.md</code> for
          the restore procedure and automatic/scheduled backup options.
        </p>

        {backupError && (
          <div className="mb-3 bg-red-900/40 border border-red-700 text-red-200 text-sm px-3 py-2 rounded-lg">
            {backupError}
          </div>
        )}

        <button
          onClick={downloadBackup}
          disabled={backupLoading}
          className="px-3 py-1.5 text-sm bg-slate-800 hover:bg-slate-700 disabled:opacity-50 text-slate-200 rounded-lg transition-colors"
        >
          {backupLoading ? 'Preparing backup…' : 'Download backup'}
        </button>
      </div>
    </div>
  )
}
