import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useProviderConfigsStore } from '../../stores/providerConfigs'
import { useAgentsStore } from '../../stores/agents'
import { planStaffing } from '../../lib/setupStatus'

// One-click staffing: creates an agent config from the built-in template for
// each agent-triggerable workflow label that has no enabled agent, against the
// chosen provider config. Templates only fill prompt/labels/retry policy; the
// provider config supplies provider/model/credentials.
export default function StaffAgentsButton({ missingLabels }: { missingLabels: string[] }) {
  const providers = useProviderConfigsStore((s) => s.configs)
  const createAgent = useAgentsStore((s) => s.create)
  const [providerId, setProviderId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const { templates, unmatched } = planStaffing(missingLabels)
  const selected = providers.find((p) => p.id === providerId) ?? providers[0]
  const noProvider = providers.length === 0

  const run = async () => {
    if (!selected) return
    setBusy(true)
    setError(null)
    // Create sequentially so a failure leaves a clear, resumable state: the
    // already-created agents cover their labels and the rest stay missing.
    for (const t of templates) {
      try {
        await createAgent({ ...t, provider_config_id: selected.id })
      } catch (e) {
        setError(`Could not create ${t.name}: ${e instanceof Error ? e.message : String(e)}`)
        break
      }
    }
    setBusy(false)
  }

  return (
    <div className="mt-1 ml-6 space-y-1 text-xs">
      {templates.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          {providers.length > 1 && (
            <select
              aria-label="Provider config"
              value={selected?.id ?? ''}
              onChange={(e) => setProviderId(e.target.value)}
              disabled={busy}
              className="bg-slate-900 border border-slate-600 rounded px-1 py-0.5 text-slate-200"
            >
              {providers.map((p) => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          )}
          <button
            type="button"
            onClick={run}
            disabled={noProvider || busy}
            className="px-2 py-1 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {busy ? 'Creating…' : `Create ${templates.map((t) => t.name).join(', ')}`}
          </button>
          {noProvider && (
            <span className="text-slate-400">
              Configure a provider first (see the step above) — <Link to="/settings/providers" className="text-indigo-400 hover:text-indigo-300">Providers →</Link>
            </span>
          )}
        </div>
      )}
      {unmatched.length > 0 && (
        <p className="text-slate-400">
          No built-in template for: {unmatched.join(', ')}. <Link to="/pipeline/agents" className="text-indigo-400 hover:text-indigo-300">Create an agent config →</Link>
        </p>
      )}
      {error && <p role="alert" className="text-red-300">{error}</p>}
    </div>
  )
}
