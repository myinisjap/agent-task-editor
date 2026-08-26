import { useState } from 'react'

// Runtime environment picker for a repo's agent-CLI execution environment.
// Three modes, mirroring the backend's resolution order (see
// backend/internal/agent/devcontainer.go): none (in-process, today's
// behavior) / an explicit image ref (escape hatch, wins over everything) /
// a UI-authored devcontainer.json (built via @devcontainers/cli).
//
// Round-trip rule: `rawJson` is the source of truth. The language picker
// only ever reads/writes the `features` object inside it — any other key
// (mounts, postCreateCommand, unlisted features, comments-via-extra-keys,
// whatever) must survive untouched. See LANGUAGES' featureRef as the only
// keys the picker owns within `features`.

export type RuntimeMode = 'none' | 'image' | 'devcontainer'

export type RuntimeEnvironmentValue = {
  mode: RuntimeMode
  imageRef: string
  rawJson: string
}

export const BLANK_RUNTIME_ENV: RuntimeEnvironmentValue = {
  mode: 'none',
  imageRef: '',
  rawJson: '',
}

type Language = { key: string; label: string; featureRef: string }

const LANGUAGES: Language[] = [
  { key: 'go', label: 'Go', featureRef: 'ghcr.io/devcontainers/features/go:1' },
  { key: 'node', label: 'Node', featureRef: 'ghcr.io/devcontainers/features/node:2' },
  { key: 'python', label: 'Python', featureRef: 'ghcr.io/devcontainers/features/python:1' },
  { key: 'rust', label: 'Rust', featureRef: 'ghcr.io/devcontainers/features/rust:1' },
  { key: 'java', label: 'Java', featureRef: 'ghcr.io/devcontainers/features/java:1' },
  { key: 'ruby', label: 'Ruby', featureRef: 'ghcr.io/devcontainers/features/ruby:2' },
]

/** Parses rawJson into an object; '' is treated as an empty (valid) config. Throws on malformed JSON. */
function parseDevcontainer(rawJson: string): Record<string, unknown> {
  const trimmed = rawJson.trim()
  if (trimmed === '') return {}
  const parsed = JSON.parse(trimmed)
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new Error('devcontainer.json must be a JSON object')
  }
  return parsed as Record<string, unknown>
}

/** Rows the picker renders: one per language feature ref present in `features`, in LANGUAGES order. */
function pickerRows(rawJson: string): { language: Language; version: string }[] {
  let parsed: Record<string, unknown>
  try {
    parsed = parseDevcontainer(rawJson)
  } catch {
    return []
  }
  const features = (parsed.features ?? {}) as Record<string, unknown>
  const rows: { language: Language; version: string }[] = []
  for (const language of LANGUAGES) {
    if (!(language.featureRef in features)) continue
    const featureVal = features[language.featureRef]
    const version =
      featureVal && typeof featureVal === 'object' && 'version' in (featureVal as Record<string, unknown>)
        ? String((featureVal as Record<string, unknown>).version ?? '')
        : ''
    rows.push({ language, version })
  }
  return rows
}

/** Rewrites only the given language's entry in `features`, preserving every other key untouched. */
function upsertFeature(rawJson: string, featureRef: string, version: string): string {
  const parsed = parseDevcontainer(rawJson)
  const features = { ...(parsed.features as Record<string, unknown> | undefined) }
  features[featureRef] = version.trim() ? { version: version.trim() } : {}
  return JSON.stringify({ ...parsed, features }, null, 2)
}

function removeFeature(rawJson: string, featureRef: string): string {
  const parsed = parseDevcontainer(rawJson)
  const features = { ...(parsed.features as Record<string, unknown> | undefined) }
  delete features[featureRef]
  const next: Record<string, unknown> = { ...parsed }
  if (Object.keys(features).length > 0) {
    next.features = features
  } else {
    delete next.features
  }
  return JSON.stringify(next, null, 2)
}

export default function RuntimeEnvironmentEditor({
  value,
  onChange,
  repoFilePresent,
  inputCls,
}: {
  value: RuntimeEnvironmentValue
  onChange: (next: RuntimeEnvironmentValue) => void
  repoFilePresent?: boolean
  inputCls: string
}) {
  const [showRaw, setShowRaw] = useState(false)
  const [rawError, setRawError] = useState('')
  const [addLanguage, setAddLanguage] = useState('')

  function setMode(mode: RuntimeMode) {
    // Switching modes never clears imageRef/rawJson — only the active mode
    // changes which one gets sent to the backend. This means toggling
    // None -> Dev container -> None -> Dev container within one editing
    // session keeps whatever the user typed.
    onChange({ ...value, mode })
  }

  function updateRawJson(next: string) {
    setRawError('')
    try {
      if (next.trim() !== '') parseDevcontainer(next)
    } catch (e) {
      setRawError(e instanceof Error ? e.message : String(e))
    }
    onChange({ ...value, rawJson: next })
  }

  function handleFeatureVersionChange(featureRef: string, version: string) {
    try {
      const next = upsertFeature(value.rawJson, featureRef, version)
      setRawError('')
      onChange({ ...value, rawJson: next })
    } catch (e) {
      setRawError(e instanceof Error ? e.message : String(e))
    }
  }

  function handleRemoveLanguage(featureRef: string) {
    try {
      const next = removeFeature(value.rawJson, featureRef)
      setRawError('')
      onChange({ ...value, rawJson: next })
    } catch (e) {
      setRawError(e instanceof Error ? e.message : String(e))
    }
  }

  function handleAddLanguage(key: string) {
    setAddLanguage('')
    const language = LANGUAGES.find((l) => l.key === key)
    if (!language) return
    try {
      const next = upsertFeature(value.rawJson, language.featureRef, '')
      setRawError('')
      onChange({ ...value, rawJson: next })
    } catch (e) {
      setRawError(e instanceof Error ? e.message : String(e))
    }
  }

  const rows = pickerRows(value.rawJson)
  const availableToAdd = LANGUAGES.filter((l) => !rows.some((r) => r.language.key === l.key))

  return (
    <div className="flex flex-col gap-3">
      <label className="text-xs font-medium text-slate-400">Runtime environment</label>

      <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
        <input
          type="radio"
          checked={value.mode === 'none'}
          onChange={() => setMode('none')}
          className="accent-indigo-500"
        />
        None — run in the backend container
      </label>

      <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
        <input
          type="radio"
          checked={value.mode === 'image'}
          onChange={() => setMode('image')}
          className="accent-indigo-500"
        />
        Image ref:
        <input
          value={value.imageRef}
          onChange={(e) => onChange({ ...value, imageRef: e.target.value, mode: 'image' })}
          onFocus={() => setMode('image')}
          placeholder="ghcr.io/me/img:1"
          className={`${inputCls} flex-1 min-w-0`}
        />
      </label>

      <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
        <input
          type="radio"
          checked={value.mode === 'devcontainer'}
          onChange={() => setMode('devcontainer')}
          className="accent-indigo-500"
        />
        Dev container
      </label>

      {value.mode === 'devcontainer' && (
        <div className="pl-6 flex flex-col gap-3">
          {repoFilePresent && (
            <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
              This repo ships .devcontainer/devcontainer.json — those settings win; these are ignored.
            </p>
          )}

          <div className="flex flex-col gap-2">
            <span className="text-xs font-medium text-slate-400">Languages</span>
            {rows.map(({ language, version }) => (
              <div key={language.key} className="flex items-center gap-2">
                <select value={language.key} disabled className={`${inputCls} w-32`}>
                  <option value={language.key}>{language.label}</option>
                </select>
                <input
                  value={version}
                  onChange={(e) => handleFeatureVersionChange(language.featureRef, e.target.value)}
                  placeholder="version (e.g. 1.26)"
                  aria-label={`${language.label} version`}
                  className={`${inputCls} w-32`}
                />
                <button
                  type="button"
                  onClick={() => handleRemoveLanguage(language.featureRef)}
                  aria-label={`Remove ${language.label}`}
                  className="text-slate-500 hover:text-red-400 transition-colors"
                >
                  ✕
                </button>
              </div>
            ))}

            {availableToAdd.length > 0 && (
              <select
                value={addLanguage}
                onChange={(e) => handleAddLanguage(e.target.value)}
                className={`${inputCls} w-40`}
              >
                <option value="">+ add language</option>
                {availableToAdd.map((l) => (
                  <option key={l.key} value={l.key}>{l.label}</option>
                ))}
              </select>
            )}
          </div>

          <button
            type="button"
            onClick={() => setShowRaw((v) => !v)}
            className="text-xs text-slate-500 hover:text-indigo-400 transition-colors text-left"
          >
            {showRaw ? '▾' : '▸'} Advanced: edit raw devcontainer.json
          </button>

          {showRaw && (
            <div className="flex flex-col gap-1.5">
              <textarea
                value={value.rawJson}
                onChange={(e) => updateRawJson(e.target.value)}
                rows={10}
                aria-label="Raw devcontainer.json"
                className={`${inputCls} font-mono text-xs`}
              />
              {rawError && <p className="text-xs text-red-400">{rawError}</p>}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/** Validates the mode-appropriate value for save; returns an error string or null. */
export function validateRuntimeEnvironment(value: RuntimeEnvironmentValue): string | null {
  if (value.mode !== 'devcontainer') return null
  if (value.rawJson.trim() === '') return null
  try {
    parseDevcontainer(value.rawJson)
    return null
  } catch (e) {
    return e instanceof Error ? e.message : String(e)
  }
}

/** Derives {runtime_image, devcontainer_json} to send to the API from the editor's mode + fields. */
export function toApiFields(value: RuntimeEnvironmentValue): { runtime_image: string; devcontainer_json: string } {
  if (value.mode === 'image') return { runtime_image: value.imageRef.trim(), devcontainer_json: '' }
  if (value.mode === 'devcontainer') return { runtime_image: '', devcontainer_json: value.rawJson.trim() }
  return { runtime_image: '', devcontainer_json: '' }
}

/** Builds editor state from a saved repo's runtime_image/devcontainer_json. */
export function fromRepo(repo: { runtime_image?: string; devcontainer_json?: string }): RuntimeEnvironmentValue {
  if (repo.runtime_image) return { mode: 'image', imageRef: repo.runtime_image, rawJson: repo.devcontainer_json ?? '' }
  if (repo.devcontainer_json) return { mode: 'devcontainer', imageRef: '', rawJson: repo.devcontainer_json }
  return { ...BLANK_RUNTIME_ENV }
}
