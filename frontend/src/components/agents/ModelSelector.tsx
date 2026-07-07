import type { ModelList } from '../../api/client'
import { CLAUDE_MODELS } from './constants'

export default function ModelSelector({ provider, model, onChange, modelList, fetchingModels }: {
  provider: string
  model: string
  onChange: (model: string) => void
  modelList: ModelList | null
  fetchingModels: boolean
}) {
  if (provider === 'claude') {
    return (
      <select
        value={model}
        onChange={(e) => onChange(e.target.value)}
        className="input"
      >
        {CLAUDE_MODELS.map((m) => (
          <option key={m.value} value={m.value}>{m.label}</option>
        ))}
      </select>
    )
  }

  if (modelList && modelList.models.length > 0) {
    return (
      <select
        value={model}
        onChange={(e) => onChange(e.target.value)}
        className="input"
      >
        <option value="">Use $MODEL env var</option>
        {modelList.models.map((m) => (
          <option key={m} value={m}>{m}</option>
        ))}
      </select>
    )
  }

  return (
    <input
      value={model}
      onChange={(e) => onChange(e.target.value)}
      className="input"
      placeholder={fetchingModels ? 'Loading models...' : 'e.g. sonnet (empty = use env var)'}
    />
  )
}
