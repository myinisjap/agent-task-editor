import { useCallback, useEffect, useState } from 'react'
import { isStandalone } from '../stores/notifications'

export interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

/** Tracks the deferred `beforeinstallprompt` event; `canInstall` is false once installed. */
export function useInstallPrompt() {
  const [event, setEvent] = useState<BeforeInstallPromptEvent | null>(null)
  const [installed, setInstalled] = useState(() => isStandalone())

  useEffect(() => {
    const onPrompt = (e: Event) => {
      e.preventDefault()
      setEvent(e as BeforeInstallPromptEvent)
    }
    const onInstalled = () => {
      setInstalled(true)
      setEvent(null)
    }
    window.addEventListener('beforeinstallprompt', onPrompt)
    window.addEventListener('appinstalled', onInstalled)
    return () => {
      window.removeEventListener('beforeinstallprompt', onPrompt)
      window.removeEventListener('appinstalled', onInstalled)
    }
  }, [])

  const install = useCallback(async () => {
    if (!event) return
    await event.prompt()
    const { outcome } = await event.userChoice
    // The event can only be used once.
    setEvent(null)
    if (outcome === 'accepted') setInstalled(true)
  }, [event])

  return { canInstall: !installed && event !== null, install }
}
