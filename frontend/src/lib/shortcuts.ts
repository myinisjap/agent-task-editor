import { useEffect, useRef } from 'react'
import { isTypingTarget } from './inboxKeys'

export { isTypingTarget }

export type GlobalAction =
  | 'new-task' | 'go-inbox' | 'go-board' | 'go-pipeline' | 'go-settings' | 'focus-search' | 'help'

export interface Shortcut {
  id: string
  /** 'Mod' renders as ⌘ on Apple platforms and Ctrl elsewhere. */
  keys: string[]
  label: string
}

/** Single source of truth for the shortcut sheet, palette hints and tooltips. */
export const SHORTCUTS: Shortcut[] = [
  { id: 'palette', keys: ['Mod', 'K'], label: 'Open command palette' },
  { id: 'new-task', keys: ['c'], label: 'New task' },
  { id: 'go-inbox', keys: ['g', 'i'], label: 'Go to Inbox' },
  { id: 'go-board', keys: ['g', 'b'], label: 'Go to Board' },
  { id: 'go-pipeline', keys: ['g', 'p'], label: 'Go to Pipeline' },
  { id: 'go-settings', keys: ['g', 's'], label: 'Go to Settings' },
  { id: 'focus-search', keys: ['/'], label: 'Search the board' },
  { id: 'help', keys: ['?'], label: 'Show keyboard shortcuts' },
]

export const INBOX_SHORTCUTS: Shortcut[] = [
  { id: 'inbox-move', keys: ['j', '/', 'k'], label: 'Move down / up (also ↓ / ↑)' },
  { id: 'inbox-approve', keys: ['a'], label: 'Approve' },
  { id: 'inbox-reject', keys: ['r'], label: 'Request changes' },
  { id: 'inbox-open', keys: ['Enter'], label: 'Open full page (also o)' },
  { id: 'inbox-esc', keys: ['Esc'], label: 'Close note' },
]

export interface PageEntry { label: string; to: string; group: string; shortcutId?: string; keywords?: string }

/** Every navigable page; mirrors the routes in App.tsx and NavSidebar. */
export const PAGES: PageEntry[] = [
  { label: 'Inbox', to: '/inbox', group: 'Go to', shortcutId: 'go-inbox' },
  { label: 'Board', to: '/board', group: 'Go to', shortcutId: 'go-board' },
  { label: 'Chat', to: '/chat', group: 'Go to' },
  { label: 'Pipeline: Overview', to: '/pipeline/overview', group: 'Go to', shortcutId: 'go-pipeline' },
  { label: 'Pipeline: Workflow', to: '/pipeline/workflow', group: 'Go to' },
  { label: 'Pipeline: Agents', to: '/pipeline/agents', group: 'Go to' },
  { label: 'Repos', to: '/repos', group: 'Go to' },
  { label: 'Settings: Providers', to: '/settings/providers', group: 'Go to', shortcutId: 'go-settings' },
  { label: 'Settings: Pricing', to: '/settings/pricing', group: 'Go to' },
  { label: 'Settings: Intake rules', to: '/settings/intake-rules', group: 'Go to' },
  { label: 'Settings: Templates', to: '/settings/templates', group: 'Go to' },
  { label: 'Settings: Lessons', to: '/settings/lessons', group: 'Go to' },
  { label: 'Settings: Skills', to: '/settings/skills', group: 'Go to' },
  { label: 'Settings: Health', to: '/settings/health', group: 'Go to' },
  { label: 'Insights: Overview', to: '/dashboard', group: 'Go to' },
  { label: 'Insights: Cost & Usage', to: '/dashboard/usage', group: 'Go to' },
  { label: 'Insights: Performance', to: '/dashboard/performance', group: 'Go to' },
]

export function isMacPlatform(): boolean {
  return typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent || '')
}

export function formatKeys(keys: string[], isMac: boolean): string {
  if (keys.length === 2 && keys[0] === 'g') return `G then ${keys[1].toUpperCase()}`
  const mapped = keys.map((k) => (k === 'Mod' ? (isMac ? '⌘' : 'Ctrl') : k.length === 1 ? k.toUpperCase() : k))
  if (keys[0] === 'Mod') return isMac ? mapped.join('') : mapped.join('+')
  return mapped.join(' ')
}

export function shortcutKeys(id: string): string[] | undefined {
  return SHORTCUTS.find((s) => s.id === id)?.keys
}

/** Tooltip text, e.g. "New task (C)". */
export function shortcutHint(id: string, label?: string): string {
  const s = SHORTCUTS.find((x) => x.id === id)
  if (!s) return label ?? ''
  return `${label ?? s.label} (${formatKeys(s.keys, isMacPlatform())})`
}

type KeyLike = Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'defaultPrevented'> &
  Partial<Pick<KeyboardEvent, 'repeat' | 'shiftKey'>> & { target: EventTarget | null }

/** Cmd/Ctrl+K — fires even while typing in an input. */
export function isPaletteToggle(e: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey'>): boolean {
  return (e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k'
}

const GO: Record<string, GlobalAction> = {
  i: 'go-inbox', b: 'go-board', p: 'go-pipeline', s: 'go-settings',
}

/** Pure matcher for the single-key / "g then x" shortcuts. */
export function globalShortcutAction(
  e: KeyLike,
  pending: 'g' | null,
): { action: GlobalAction | null; pending: 'g' | null } {
  if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented || e.repeat) return { action: null, pending }
  if (isTypingTarget(e.target)) return { action: null, pending: null }
  if (e.key === 'Shift') return { action: null, pending }
  if (pending === 'g') {
    const a = GO[e.key.toLowerCase()]
    return { action: a ?? null, pending: null }
  }
  switch (e.key) {
    case 'g': return { action: null, pending: 'g' }
    case 'c': return { action: 'new-task', pending: null }
    case '/': return { action: 'focus-search', pending: null }
    case '?': return { action: 'help', pending: null }
    default: return { action: null, pending: null }
  }
}

export interface GlobalShortcutHandlers {
  onTogglePalette: () => void
  onAction: (a: GlobalAction) => void
}

export function useGlobalShortcuts(handlers: GlobalShortcutHandlers, opts: { singleKeyEnabled: boolean }) {
  const ref = useRef(handlers)
  useEffect(() => { ref.current = handlers })
  const { singleKeyEnabled } = opts
  useEffect(() => {
    let pending: 'g' | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    const clear = () => { pending = null; if (timer) clearTimeout(timer) }
    const onKey = (e: KeyboardEvent) => {
      if (isPaletteToggle(e)) {
        if (otherDialogOpen()) return
        e.preventDefault()
        ref.current.onTogglePalette()
        return
      }
      if (!singleKeyEnabled) return
      if (document.querySelector('[role="dialog"]')) { clear(); return }
      const res = globalShortcutAction(e, pending)
      if (res.pending === 'g' && pending !== 'g') {
        timer = setTimeout(clear, 1000)
      } else if (res.pending === null) {
        clear()
      }
      pending = res.pending
      if (res.action) {
        e.preventDefault()
        ref.current.onAction(res.action)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => { window.removeEventListener('keydown', onKey); clear() }
  }, [singleKeyEnabled])
}

/** True when a modal other than the command palette is open. */
function otherDialogOpen(): boolean {
  return Array.from(document.querySelectorAll('[role="dialog"]')).some((d) => !d.querySelector('[cmdk-root]'))
}
