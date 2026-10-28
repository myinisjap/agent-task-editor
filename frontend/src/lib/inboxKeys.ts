import { useEffect, useRef } from 'react'

export type InboxKeyAction = 'next' | 'prev' | 'approve' | 'reject' | 'open' | 'escape'

type KeyLike = Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'defaultPrevented'> & { target: EventTarget | null }

export function isTypingTarget(el: EventTarget | null): boolean {
  const t = el as HTMLElement | null
  if (!t || !t.tagName) return false
  const tag = t.tagName.toLowerCase()
  if (tag === 'input' || tag === 'textarea' || tag === 'select' || !!t.isContentEditable) return true
  return !!t.closest?.('.xterm')
}

/** Maps a keydown to an Inbox action, or null when it must be left alone. */
export function inboxKeyAction(e: KeyLike): InboxKeyAction | null {
  if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return null
  if (isTypingTarget(e.target)) return null
  switch (e.key) {
    case 'j': case 'ArrowDown': return 'next'
    case 'k': case 'ArrowUp': return 'prev'
    case 'a': return 'approve'
    case 'r': return 'reject'
    case 'o': return 'open'
    case 'Enter': {
      const tag = (e.target as HTMLElement | null)?.tagName?.toLowerCase()
      return tag === 'a' || tag === 'button' ? null : 'open'
    }
    case 'Escape': return 'escape'
    default: return null
  }
}

export function useInboxKeyboard(enabled: boolean, handler: (action: InboxKeyAction) => void) {
  const ref = useRef(handler)
  useEffect(() => { ref.current = handler })
  useEffect(() => {
    if (!enabled) return
    const onKey = (e: KeyboardEvent) => {
      const action = inboxKeyAction(e)
      if (!action) return
      // A modal or lightbox owns the keyboard while open.
      if (document.querySelector('[role="dialog"]')) return
      e.preventDefault()
      ref.current(action)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [enabled])
}
