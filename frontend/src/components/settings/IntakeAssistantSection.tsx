import { useEffect, useState } from 'react'
import { api, type IntakeAssistantSettings, type ProviderConfig } from '../../api/client'
import { errMessage } from '../../lib/errMessage'

/**
 * Opt-in settings for the task intake assistant (advisory model check of new
 * tasks). Off by default; the deterministic duplicate hint needs no setup.
 */
export default function IntakeAssistantSection() {
  const [settings, setSettings] = useState<IntakeAssistantSettings | null>(null)
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([api.intakeAssistant.get(), api.providerConfigs.list()])
      .then(([s, p]) => {
        setSettings(s)
        setProviders(p)
      })
      .catch((e) => setError(errMessage(e)))
  }, [])

  async function save(next: IntakeAssistantSettings) {
    setError('')
    try {
      setSettings(await api.intakeAssistant.update(next))
    } catch (e) {
      setError(errMessage(e))
    }
  }

  if (!settings) return error ? <p className="text-xs text-danger">{error}</p> : null

  return (
    <section className="mt-8 flex flex-col gap-3" data-testid="intake-assistant-settings">
      <h2 className="text-sm font-semibold text-fg">Task intake assistant</h2>
      <p className="text-xs text-fg-muted">
        A model reads new tasks and suggests clarifying questions, a type, and which possible duplicates are real. It is
        advisory only and never moves, edits or archives a task. Off by default.
      </p>
      <label className="flex items-center gap-2 text-sm text-fg">
        <input
          type="checkbox"
          checked={settings.enabled}
          onChange={(e) => save({ ...settings, enabled: e.target.checked })}
        />
        Enable the assistant
      </label>
      <label className="flex items-center gap-2 text-sm text-fg">
        <input
          type="checkbox"
          checked={settings.auto_check}
          onChange={(e) => save({ ...settings, auto_check: e.target.checked })}
        />
        Check tasks automatically when created on a parked label (e.g. not_ready)
      </label>
      <label className="flex flex-col gap-1 text-xs text-fg-muted">
        Provider config
        <select
          value={settings.provider_config_id ?? ''}
          onChange={(e) => save({ ...settings, provider_config_id: e.target.value || null })}
          className="bg-surface-sunken border border-border rounded-lg px-3 py-2 text-sm text-fg max-w-xs"
        >
          <option value="">None</option>
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      {error && <p className="text-xs text-danger">{error}</p>}
    </section>
  )
}
