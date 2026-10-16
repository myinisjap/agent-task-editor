import { useInstallPrompt } from '../../lib/installPrompt'

export default function InstallAppBar() {
  const { canInstall, install } = useInstallPrompt()
  if (!canInstall) return null
  return (
    <div className="flex shrink-0 items-center justify-between gap-3 border-b border-slate-700 bg-slate-900 px-4 py-2 md:px-6">
      <span className="text-sm text-slate-300">Install Agent Task Editor on this device.</span>
      <button
        type="button"
        onClick={() => void install()}
        className="rounded bg-indigo-600 px-3 py-1 text-sm font-medium text-white hover:bg-indigo-500"
      >
        Install app
      </button>
    </div>
  )
}
