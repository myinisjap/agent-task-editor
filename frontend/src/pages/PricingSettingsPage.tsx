import { useCallback, useEffect, useState } from 'react'
import { PageFrame } from '../components/ui'
import { api, type ModelPricing } from '../api/client'
import HelpModal from '../components/shared/HelpModal'
import { PricingHelp } from '../components/shared/pageHelp'

// PricingRow is the editable form-state shape for one row of the table —
// prices are kept as strings while editing so an in-progress/invalid number
// doesn't get silently clamped, mirroring the pattern used by the log
// retention / backup settings forms on HealthPage.
interface PricingRow {
  model: string
  inputPer1M: string
  outputPer1M: string
  // Empty means "no cached rate": cached input tokens bill at the input price.
  cachedInputPer1M: string
  // Empty means "no cache-write rate": cache-write tokens (claude/qwen_code's
  // cache_creation_input_tokens) bill at the input price.
  cacheWriteInputPer1M: string
}

function toFormRow(r: ModelPricing): PricingRow {
  return {
    model: r.model,
    inputPer1M: String(r.input_per_1m),
    outputPer1M: String(r.output_per_1m),
    cachedInputPer1M: r.cached_input_per_1m == null ? '' : String(r.cached_input_per_1m),
    cacheWriteInputPer1M: r.cache_write_input_per_1m == null ? '' : String(r.cache_write_input_per_1m),
  }
}

export default function PricingSettingsPage() {
  const [rows, setRows] = useState<PricingRow[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [showHelp, setShowHelp] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    setSaved(false)
    api.modelPricing
      .list()
      .then((data) => setRows((data ?? []).map(toFormRow)))
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
    setRows((rs) => [
      ...rs,
      { model: '', inputPer1M: '0', outputPer1M: '0', cachedInputPer1M: '', cacheWriteInputPer1M: '' },
    ])
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
        let cachedInputPer1M: number | null = null
        if (r.cachedInputPer1M.trim() !== '') {
          cachedInputPer1M = Number(r.cachedInputPer1M)
          if (!Number.isFinite(cachedInputPer1M) || cachedInputPer1M < 0) {
            throw new Error(`Cached input price for "${model}" must be a number >= 0, or empty`)
          }
        }
        let cacheWriteInputPer1M: number | null = null
        if (r.cacheWriteInputPer1M.trim() !== '') {
          cacheWriteInputPer1M = Number(r.cacheWriteInputPer1M)
          if (!Number.isFinite(cacheWriteInputPer1M) || cacheWriteInputPer1M < 0) {
            throw new Error(`Cache-write price for "${model}" must be a number >= 0, or empty`)
          }
        }
        return {
          model,
          input_per_1m: inputPer1M,
          output_per_1m: outputPer1M,
          cached_input_per_1m: cachedInputPer1M,
          cache_write_input_per_1m: cacheWriteInputPer1M,
        }
      })
      const updated = await api.modelPricing.update(payload)
      setRows(updated.map(toFormRow))
      setSaved(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <PageFrame title="Model Pricing" headerClassName="mb-2" help={{ onClick: () => setShowHelp(true), title: 'About model pricing' }}>
      <p className="text-sm text-fg-muted mb-6">
        USD price per 1M tokens used to estimate run cost for the <code className="text-fg-muted">codex_cli</code>{' '}
        provider, plus the mid-run cost watchdog on{' '}
        <code className="text-fg-muted">claude</code>/<code className="text-fg-muted">qwen_code</code> — the{' '}
        <code className="text-fg-muted">claude</code> and <code className="text-fg-muted">opencode</code> CLIs'
        own final reported cost is authoritative and unaffected. A model with no row here falls back to an internal,
        approximate hardcoded table; a run whose model matches neither is flagged "cost unknown" in its run history
        instead of silently showing $0. Cached $/1M prices the prompt-cache hits{' '}
        <code className="text-fg-muted">codex_cli</code> reports (and <code className="text-fg-muted">claude</code>/
        <code className="text-fg-muted">qwen_code</code>'s cache-read tokens); leave it empty to bill them at the
        input price. Cache-write $/1M prices <code className="text-fg-muted">claude</code>/
        <code className="text-fg-muted">qwen_code</code>'s cache-creation tokens (writing a prompt prefix into the
        cache for the first time); leave it empty to bill them at the input price too. Changes take effect on the
        very next run — no restart needed.
      </p>

      {showHelp && (
        <HelpModal title="About Model Pricing" onClose={() => setShowHelp(false)}>
          <PricingHelp />
        </HelpModal>
      )}

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
        <div className="text-sm text-fg-subtle">Loading…</div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="grid grid-cols-[1fr_72px_72px_72px_72px_auto] sm:grid-cols-[1fr_120px_120px_120px_120px_auto] gap-2 text-xs text-fg-subtle px-1">
            <span>Model</span>
            <span>Input $/1M</span>
            <span>Output $/1M</span>
            <span>Cached $/1M</span>
            <span>Cache write $/1M</span>
            <span />
          </div>

          {rows.map((row, i) => (
            <div key={i} className="grid grid-cols-[1fr_72px_72px_72px_72px_auto] sm:grid-cols-[1fr_120px_120px_120px_120px_auto] gap-2 items-center">
              <input
                type="text"
                value={row.model}
                onChange={(e) => updateRow(i, { model: e.target.value })}
                placeholder="e.g. claude-sonnet-4-5"
                disabled={saving}
                className="px-2 py-1.5 bg-surface-sunken border border-border rounded-lg text-fg disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.inputPer1M}
                onChange={(e) => updateRow(i, { inputPer1M: e.target.value })}
                disabled={saving}
                className="px-2 py-1.5 bg-surface-sunken border border-border rounded-lg text-fg disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.outputPer1M}
                onChange={(e) => updateRow(i, { outputPer1M: e.target.value })}
                disabled={saving}
                className="px-2 py-1.5 bg-surface-sunken border border-border rounded-lg text-fg disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.cachedInputPer1M}
                onChange={(e) => updateRow(i, { cachedInputPer1M: e.target.value })}
                placeholder="= input"
                aria-label={`Cached input price for ${row.model || 'row'}`}
                disabled={saving}
                className="px-2 py-1.5 bg-surface-sunken border border-border rounded-lg text-fg disabled:opacity-50"
              />
              <input
                type="number"
                min={0}
                step="any"
                value={row.cacheWriteInputPer1M}
                onChange={(e) => updateRow(i, { cacheWriteInputPer1M: e.target.value })}
                placeholder="= input"
                aria-label={`Cache-write price for ${row.model || 'row'}`}
                disabled={saving}
                className="px-2 py-1.5 bg-surface-sunken border border-border rounded-lg text-fg disabled:opacity-50"
              />
              <button
                onClick={() => removeRow(i)}
                disabled={saving}
                aria-label={`Remove ${row.model || 'row'}`}
                className="px-2 py-1.5 text-sm text-fg-muted hover:text-danger disabled:opacity-50"
              >
                ✕
              </button>
            </div>
          ))}

          {rows.length === 0 && <p className="text-xs text-fg-subtle px-1 py-2">No pricing rows yet.</p>}

          <div className="flex items-center gap-3 mt-2">
            <button
              onClick={addRow}
              disabled={saving}
              className="px-3 py-1.5 text-sm bg-surface-sunken hover:bg-border disabled:opacity-50 text-fg rounded-lg transition-colors"
            >
              + Add row
            </button>
            <button
              onClick={save}
              disabled={saving}
              className="px-3 py-1.5 text-sm bg-accent hover:bg-accent/90 disabled:opacity-50 text-on-accent rounded-lg transition-colors"
            >
              {saving ? 'Saving…' : 'Save pricing table'}
            </button>
          </div>
        </div>
      )}
    </PageFrame>
  )
}
