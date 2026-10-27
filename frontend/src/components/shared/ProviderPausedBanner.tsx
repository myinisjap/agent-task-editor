import { X } from 'lucide-react'
import Icon from '../ui/Icon'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { wsClient } from '../../api/ws'
import { useHealthStore } from '../../stores/health'
import { pausedSignature, readBannerDismissed, writeBannerDismissed } from '../../lib/healthTiers'

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

// The app's only banner (tier 1, "blocking"): shown while the provider circuit
// breaker has paused dispatch for a provider. One slim line with the fix
// action; dismissible for the session, and it reappears when the paused set
// changes. Other health problems are a counted dot, never a banner. Health
// polling lives in useHealthPolling(); this refetches on provider.pause_changed
// so it appears and clears without a reload.
export default function ProviderPausedBanner() {
  const paused = useHealthStore((s) => s.paused)
  const fetchHealth = useHealthStore((s) => s.fetch)
  const resume = useHealthStore((s) => s.resume)
  const [resuming, setResuming] = useState(false)
  const [dismissedSig, setDismissedSig] = useState<string | null>(readBannerDismissed)
  const [detailsOpen, setDetailsOpen] = useState(false)

  useEffect(() => {
    return wsClient.on((event) => {
      if (event.type === 'provider.pause_changed') fetchHealth()
    })
  }, [fetchHealth])

  if (paused.length === 0) return null
  const sig = pausedSignature(paused)
  if (dismissedSig === sig) return null

  const names = paused.map((p) => providerLabel(p.provider)).join(', ')
  const title =
    paused.length === 1
      ? `${names} isn't logged in — dispatch paused.`
      : `${names} aren't logged in — dispatch paused.`

  const onResume = async () => {
    setResuming(true)
    try {
      for (const p of paused) await resume(p.provider)
    } finally {
      setResuming(false)
    }
  }

  const dismiss = () => {
    writeBannerDismissed(sig)
    setDismissedSig(sig)
  }

  return (
    <div role="alert" data-testid="provider-paused-banner" className="border-b px-3 py-1.5 text-xs bg-red-900/40 border-red-700 text-red-200">
      <div className="flex items-center gap-x-2">
        <span className="min-w-0 truncate font-medium">{title}</span>
        <span className="hidden md:inline">
          Queued tasks will wait and resume automatically once the health check passes.
          {paused.map((p) => (p.reason ? ` ${providerLabel(p.provider)}: ${p.reason}.` : ''))}
        </span>
        <button
          type="button"
          onClick={() => setDetailsOpen((v) => !v)}
          aria-expanded={detailsOpen}
          className="md:hidden shrink-0 underline"
        >
          Details
        </button>
        <span className="ml-auto flex shrink-0 items-center gap-2">
          <button
            type="button"
            onClick={onResume}
            disabled={resuming}
            className="rounded border border-red-500 px-2 py-0.5 font-medium hover:bg-red-800/50 disabled:opacity-50"
          >
            {paused.length === 1 ? 'Resume' : 'Resume all'}
          </button>
          <Link to="/settings/health" className="underline font-medium whitespace-nowrap">
            View health →
          </Link>
          <button type="button" onClick={dismiss} aria-label="Dismiss for this session" className="px-1 hover:text-white">
            <Icon icon={X} size="md" />
          </button>
        </span>
      </div>
      {detailsOpen && (
        <div className="md:hidden mt-1">
          Queued tasks will wait and resume automatically once the health check passes.
          {paused.map((p) => (
            <div key={p.provider}>
              {providerLabel(p.provider)}: {p.reason}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
