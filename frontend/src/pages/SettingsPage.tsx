import TabbedPage from '../components/shared/TabbedPage'
import { useHealthStore, worstHealth } from '../stores/health'

export default function SettingsPage() {
  const severity = worstHealth(useHealthStore((s) => s.checks))
  const tabs = [
    { to: '/settings/providers', label: 'Providers' },
    { to: '/settings/pricing', label: 'Pricing' },
    { to: '/settings/intake-rules', label: 'Intake Rules' },
    { to: '/settings/templates', label: 'Templates' },
    {
      to: '/settings/health',
      label: 'Health',
      badge: severity
        ? { severity, label: severity === 'error' ? 'Health check failing' : 'Health check warning' }
        : null,
    },
  ]
  return <TabbedPage testId="settings-page" tabs={tabs} />
}
