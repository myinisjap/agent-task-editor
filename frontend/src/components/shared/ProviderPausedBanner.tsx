import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { wsClient } from '../../api/ws'
import { useHealthStore } from '../../stores/health'

const PROVIDER_LABELS: Record<string, string> = {
  claude: 'Claude',
  codex_cli: 'Codex',
  qwen_code: 'Qwen',
  opencode: 'opencode',
}

/** Friendly provider name for the banner, falling back to the raw kind. */
export function providerLabel(provider: string): string {
  return PROVIDER_LABELS[provider] ?? provider
}

// App-wide banner shown while the provider circuit breaker has paused dispatch
// for a provider (a run was classified as an auth failure). Reads the shared
// health store (HealthWarningBanner owns the polling) and refetches on the
// provider.pause_changed WS event so it appears and clears without a reload.
export default function ProviderPausedBanner() {
  const paused = useHealthStore((s) => s.paused)
  const fetchHealth = useHealthStore((s) => s.fetch)
  const resume = useHealthStore((s) => s.resume)
  const [resuming, setResuming] = useState<string | null>(null)

  useEffect(() => {
    return wsClient.on((event) => {
      if (event.type === 'provider.pause_changed') fetchHealth()
    })
  }, [fetchHealth])

  if (paused.length === 0) return null

  const onResume = async (provider: string) => {
    setResuming(provider)
    try {
      await resume(provider)
    } finally {
      setResuming(null)
    }
  }

  return (
    <div role="alert" data-testid="provider-paused-banner" className="border-b px-4 py-2 text-xs bg-red-900/40 border-red-700 text-red-200">
      {paused.map((p) => (
        <div key={p.provider} className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="font-medium">
            {providerLabel(p.provider)} isn&apos;t logged in — dispatch paused.
          </span>
          <span>Queued tasks will wait and resume automatically once the health check passes.</span>
          <Link to="/health" className="underline font-medium">
            View health →
          </Link>
          <button
            type="button"
            onClick={() => onResume(p.provider)}
            disabled={resuming === p.provider}
            className="rounded border border-red-500 px-2 py-0.5 font-medium hover:bg-red-800/50 disabled:opacity-50"
          >
            Resume
          </button>
        </div>
      ))}
    </div>
  )
}
