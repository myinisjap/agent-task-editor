import InstallAppBar from '../components/shared/InstallAppBar'
import TabbedPage from '../components/shared/TabbedPage'
import { useHealthStore } from '../stores/health'
import { healthSummary } from '../lib/healthTiers'

export default function SettingsPage() {
  const checks = useHealthStore((s) => s.checks)
  const snoozed = useHealthStore((s) => s.snoozed)
  const paused = useHealthStore((s) => s.paused)
  const { severity, count } = healthSummary(checks, snoozed, paused)
  const tabs = [
    { to: '/settings/providers', label: 'Providers' },
    { to: '/settings/pricing', label: 'Pricing' },
    { to: '/settings/intake-rules', label: 'Intake Rules' },
    { to: '/settings/templates', label: 'Templates' },
    { to: '/settings/lessons', label: 'Lessons' },
    { to: '/settings/skills', label: 'Skills' },
    {
      to: '/settings/health',
      label: 'Health',
      badge: severity
        ? { severity, label: severity === 'error' ? 'Health check failing' : 'Health check warning', count }
        : null,
    },
  ]
  return (
    <div className="flex flex-col h-full">
      <InstallAppBar />
      <div className="flex-1 min-h-0">
        <TabbedPage testId="settings-page" tabs={tabs} />
      </div>
    </div>
  )
}
