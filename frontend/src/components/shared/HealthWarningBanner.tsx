import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useHealthStore, worstHealth } from '../../stores/health'

const POLL_MS = 60_000

// Compact app-wide banner shown whenever /health/providers reports a failing
// check, regardless of whether the Get Started checklist is still visible.
export default function HealthWarningBanner() {
  const checks = useHealthStore((s) => s.checks)
  const fetchHealth = useHealthStore((s) => s.fetch)

  useEffect(() => {
    fetchHealth()
    const id = setInterval(fetchHealth, POLL_MS)
    const onFocus = () => fetchHealth()
    window.addEventListener('focus', onFocus)
    return () => {
      clearInterval(id)
      window.removeEventListener('focus', onFocus)
    }
  }, [fetchHealth])

  const failing = checks.filter((c) => c.status !== 'ok')
  const worst = worstHealth(checks)
  if (!worst) return null

  const style =
    worst === 'error'
      ? 'bg-red-900/40 border-red-700 text-red-200'
      : 'bg-yellow-900/40 border-yellow-700 text-yellow-200'

  return (
    <div role="alert" data-testid="health-warning-banner" className={`border-b px-4 py-2 text-xs ${style}`}>
      <span className="font-medium">
        {failing.length === 1 ? '1 health check needs attention: ' : `${failing.length} health checks need attention: `}
      </span>
      <span>{failing.map((c) => `${c.name}: ${c.detail}`).join(' · ')}</span>{' '}
      <Link to="/health" className="underline font-medium">
        View health →
      </Link>
    </div>
  )
}
