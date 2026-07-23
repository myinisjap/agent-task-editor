import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'

// PricingRow is the editable form-state shape for one row of the table —
// prices are kept as strings while editing so an in-progress/invalid number
// doesn't get silently clamped, mirroring the pattern used by the log
// retention / backup settings forms on HealthPage.
interface PricingRow {
  model: string
  inputPer1M: string
  outputPer1M: string
}

export default function PricingSettingsPage() {
  const [rows, setRows] = useState<PricingRow[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    setSaved(false)
    api.modelPricing
      .list()
      .then((data) =>
        setRows(
          (data ?? []).map((r) => ({
            model: r.model,
            inputPer1M: String(r.input_per_1m),
            outputPer1M: String(r.output_per_1m),
          })),
        ),
      )
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  function updateRow(i: number, patch: Partial<PricingRow>) {
    setRows((rs) => rs.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }

  function addRow() {
    setRows((rs) => [...rs, { model: '', inputPer1M: '0', outputPer1M: '0' }])
  }

  function removeRow(i: number) {
    setRows((rs) => rs.filter((_, idx) => idx !== i))
  }

  async function save() {
    setSaving(true)
    setError('')
    setSaved(false)
    try {
      const seen = new Set<string>()
      const payload = rows.map((r) => {
        const model = r.model.trim()
        if (!model) throw new Error('Model must not be empty')
        if (seen.has(model)) throw new Error(`Duplicate model "${model}"`)
        seen.add(model)
        const inputPer1M = Number(r.inputPer1M)
        const outputPer1M = Number(r.outputPer1M)
        if (!Number.isFinite(inputPer1M) || inputPer1M < 0) {
          throw new Error(`Input price for "${model}" must be a number >= 0`)
        }
        if (!Number.isFinite(outputPer1M) || outputPer1M < 0) {
          throw new Error(`Output price for "${model}" must be a number >= 0`)
        }
        return { model, input_per_1m: inputPer1M, output_per_1m: outputPer1M }
      })
      const updated = await api.modelPricing.update(payload)
      setRows(
        updated.map((r) => ({
          model: r.model,
          inputPer1M: String(r.input_per_1m),
          outputPer1M: String(r.output_per_1m),
        })),
      )
      setSaved(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="p-6 max-w-3xl">
      <h1 className="text-xl font-semibold text-slate-100 mb-2">Model Pricing</h1>
      <p className="text-sm text-slate-400 mb-6">
        USD price per 1M tokens used to estimate run cost for the <code className="text-slate-300">anthropic</code>{' '}
        and <code className="text-slate-300">llm</code> providers (the <code className="text-slate-300">claude</code>{' '}
        and <code className="text-slate-300">qwen_code</code> CLIs report their own authoritative cost and aren't
        affected). A model with no row here falls back to an internal, approximate hardcoded table; a run whose model
        matches neither is flagged "cost unknown" in its run history instead of silently showing $0. Changes take
        effect on the very next run — no restart needed.
      </p>

      {error && (
        <div className="mb-4 bg-red-900/40 border border-red-700 text-red-200 text-sm px-3 py-2 rounded-lg">
          {error}
        </div>
      )}
      {saved && !error && (
        <div className="mb-4 bg-green-900/30 border border-green-700 text-green-300 text-sm px-3 py-2 rounded-lg">
          Pricing table saved.
        </div>
      )}

      {loading ? (
        <div className="text-sm text-slate-500">Loading…</div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="grid grid-cols-[1fr_140px_140px_auto] gap-2 text-xs text-slate-500 px-1">
            <span>Model</span>
            <span>Input $/1M</span>
            <span>Output $/1M</span>
            <span />
          </div>

          {rows.map((row, i) => (
            <div key={i} className="grid grid-cols-[1fr_140px_140px_auto] gap-2 items-center">
              <input
                type="text"
                value={row.model}
                onChange={(e) => updateRow(i, { model: e.target.value })}
                placeholder="e.g. claude-sonnet-4-5"
                disabled={saving}
                className="px-2 py-1.5 bg-slate-800 border border-slate-700 rounded-lg text-slate-100 disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.inputPer1M}
                onChange={(e) => updateRow(i, { inputPer1M: e.target.value })}
                disabled={saving}
                className="px-2 py-1.5 bg-slate-800 border border-slate-700 rounded-lg text-slate-100 disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.outputPer1M}
                onChange={(e) => updateRow(i, { outputPer1M: e.target.value })}
                disabled={saving}
                className="px-2 py-1.5 bg-slate-800 border border-slate-700 rounded-lg text-slate-100 disabled:opacity-50"
              />
              <button
                onClick={() => removeRow(i)}
                disabled={saving}
                aria-label={`Remove ${row.model || 'row'}`}
                className="px-2 py-1.5 text-sm text-slate-400 hover:text-red-300 disabled:opacity-50"
              >
                ✕
              </button>
            </div>
          ))}

          {rows.length === 0 && <p className="text-xs text-slate-600 px-1 py-2">No pricing rows yet.</p>}

          <div className="flex items-center gap-3 mt-2">
            <button
              onClick={addRow}
              disabled={saving}
              className="px-3 py-1.5 text-sm bg-slate-800 hover:bg-slate-700 disabled:opacity-50 text-slate-200 rounded-lg transition-colors"
            >
              + Add row
            </button>
            <button
              onClick={save}
              disabled={saving}
              className="px-3 py-1.5 text-sm bg-indigo-700 hover:bg-indigo-600 disabled:opacity-50 text-white rounded-lg transition-colors"
            >
              {saving ? 'Saving…' : 'Save pricing table'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
