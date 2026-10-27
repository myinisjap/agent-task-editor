import { X } from 'lucide-react'
import { Button, Icon, controlClasses } from '../components/ui'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, type ChatSession, type Repo, type ProviderConfig } from '../api/client'
import { wsTicketParam } from '../api/ws'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { ChatHelp } from '../components/shared/pageHelp'
import TerminalKeyBar from '../components/chat/TerminalKeyBar'
import { toControlChar } from '../lib/termKeys'
import MessageComposer from '../components/chat/MessageComposer'
import { isTap, pixelsToLines } from '../lib/chatInput'
import { isNarrowScreen, loadFontSize, saveFontSize, stepFont, MIN_FONT, MAX_FONT, looksLikePrompt } from '../lib/termFont'
import { useKeyboardInset } from '../lib/useKeyboardInset'

export default function ChatPage() {
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [providerConfigs, setProviderConfigs] = useState<ProviderConfig[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [showHelp, setShowHelp] = useState(false)
  // New-session form
  const [newRepo, setNewRepo] = useState('')
  const [newProviderConfigId, setNewProviderConfigId] = useState('')
  // Mobile: the on-screen keyboard overlays the bottom of the layout viewport,
  // so pad the page by its height to keep the terminal key bar above it.
  const keyboardInset = useKeyboardInset()
  // Terminal text size, remembered per device class (narrow vs wide).
  const [fontSize, setFontSize] = useState(() => loadFontSize(isNarrowScreen()))
  function changeFont(dir: 1 | -1) {
    const next = stepFont(fontSize, dir)
    saveFontSize(isNarrowScreen(), next)
    setFontSize(next)
  }

  // Initial load: sessions + repos (repos needed for the new-session picker)
  // + provider configs (needed to create a session and to resolve each
  // session's provider_config_id to a human-readable provider/model).
  // Coerce to [] — the API marshals an empty list as JSON null (Go nil slice).
  useEffect(() => {
    api.chat.list().then((s) => setSessions(s ?? [])).catch(() => {})
    api.repos.list().then((r) => {
      r = r ?? []
      setRepos(r)
      if (r.length > 0) setNewRepo(r[0].id)
    }).catch(() => {})
    api.providerConfigs.list().then((pcs) => {
      pcs = pcs ?? []
      setProviderConfigs(pcs)
      if (pcs.length > 0) setNewProviderConfigId(pcs[0].id)
    }).catch(() => {})
  }, [])

  async function createSession() {
    if (!newRepo || !newProviderConfigId) return
    const sess = await api.chat.create({ repo_id: newRepo, provider_config_id: newProviderConfigId })
    setSessions((prev) => [sess, ...prev])
    setActiveId(sess.id)
  }

  async function deleteSession(id: string) {
    await api.chat.delete(id).catch(() => {})
    setSessions((prev) => prev.filter((s) => s.id !== id))
    if (activeId === id) setActiveId(null)
  }

  const active = sessions.find((s) => s.id === activeId)

  return (
    <div
      className="h-full min-h-0 flex"
      style={keyboardInset ? { paddingBottom: keyboardInset } : undefined}
      data-testid="chat-page"
    >
      {/* Left: session list + new-session form.
          Mobile: full width, hidden once a chat is open. Desktop: fixed rail. */}
      <div
        data-testid="chat-sidebar"
        className={`${active ? 'hidden md:flex' : 'flex'} w-full md:w-64 shrink-0 border-r border-border flex-col min-h-0 bg-surface-raised`}
      >
        <div className="p-3 border-b border-border space-y-2">
          <div className="flex items-center justify-between">
            <div className="text-fg font-semibold text-sm">New terminal</div>
            <HelpButton onClick={() => setShowHelp(true)} title="About Chat" />
          </div>
          <select
            value={newRepo}
            onChange={(e) => setNewRepo(e.target.value)}
            className={`${controlClasses} !text-base md:!text-xs !px-2 !py-2 md:!py-1`}
          >
            {repos.length === 0 && <option value="">No repos configured</option>}
            {repos.map((r) => (
              <option key={r.id} value={r.id}>{r.name}</option>
            ))}
          </select>
          <select
            value={newProviderConfigId}
            onChange={(e) => setNewProviderConfigId(e.target.value)}
            className={`${controlClasses} !text-base md:!text-xs !px-2 !py-2 md:!py-1`}
          >
            {providerConfigs.length === 0 && <option value="">No provider configs</option>}
            {providerConfigs.map((pc) => (
              <option key={pc.id} value={pc.id}>{pc.name} ({pc.provider})</option>
            ))}
          </select>
          {providerConfigs.length === 0 && (
            <p className="text-[11px] text-fg-subtle">
              <Link to="/settings/providers" className="text-accent-fg hover:underline">Create a provider config →</Link> before starting a terminal.
            </p>
          )}
          <Button
            variant="primary"
            size="sm"
            className="w-full"
            onClick={createSession}
            disabled={!newRepo || !newProviderConfigId}
          >
            Start terminal
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {sessions.map((s) => {
            const repo = repos.find((r) => r.id === s.repo_id)
            const pc = providerConfigs.find((p) => p.id === s.provider_config_id)
            return (
              <div
                key={s.id}
                onClick={() => setActiveId(s.id)}
                className={`group px-3 py-3 md:py-2 cursor-pointer border-b border-border/50 flex items-start justify-between gap-2 ${
                  s.id === activeId ? 'bg-surface-sunken' : 'hover:bg-surface-sunken/50'
                }`}
              >
                <div className="min-w-0">
                  <div className="text-fg text-xs truncate">{s.title || repo?.name || 'Chat'}</div>
                  <div className="text-fg-subtle text-[11px] truncate">{pc?.provider ?? ''}{repo ? ` · ${repo.name}` : ''}</div>
                </div>
                <button
                  onClick={(e) => { e.stopPropagation(); deleteSession(s.id) }}
                  aria-label="Delete chat"
                  className="opacity-100 md:opacity-0 md:group-hover:opacity-100 text-fg-subtle hover:text-danger text-sm px-1"
                >
                  <Icon icon={X} size="md" />
                </button>
              </div>
            )
          })}
          {sessions.length === 0 && (
            <p className="text-fg-subtle text-xs px-3 py-3">No terminals yet</p>
          )}
        </div>
      </div>

      {/* Right: the live terminal. Inverse of the sidebar on mobile. */}
      <div className={`${active ? 'flex' : 'hidden md:flex'} flex-1 flex-col min-w-0 min-h-0`}>
        {active ? (
          <>
            <div className="px-4 py-2 border-b border-border text-fg-muted text-xs flex items-center gap-2">
              <button
                onClick={() => setActiveId(null)}
                aria-label="Back to chats"
                className="md:hidden text-fg-muted hover:text-fg -ml-1 pr-1"
              >
                ‹ Chats
              </button>
              <span className="flex-1 min-w-0 truncate">
                {providerConfigs.find((p) => p.id === active.provider_config_id)?.provider ?? ''}
                {repos.find((r) => r.id === active.repo_id) && ` · ${repos.find((r) => r.id === active.repo_id)!.name}`}
              </span>
              <span className="ml-auto flex items-center gap-1 shrink-0">
                <button
                  type="button"
                  onClick={() => changeFont(-1)}
                  disabled={fontSize <= MIN_FONT}
                  aria-label="Smaller text"
                  className="px-2 py-0.5 rounded border border-border text-fg-muted disabled:opacity-40"
                >
                  A−
                </button>
                <button
                  type="button"
                  onClick={() => changeFont(1)}
                  disabled={fontSize >= MAX_FONT}
                  aria-label="Larger text"
                  className="px-2 py-0.5 rounded border border-border text-fg-muted disabled:opacity-40"
                >
                  A+
                </button>
              </span>
            </div>
            {/* keyed by session id so switching sessions remounts a fresh terminal */}
            <TerminalView key={active.id} sessionId={active.id} fontSize={fontSize} />
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center text-fg-subtle text-sm">
            Select a terminal or start a new one.
          </div>
        )}
      </div>

      {showHelp && (
        <HelpModal title="About Chat" onClose={() => setShowHelp(false)}>
          <ChatHelp />
        </HelpModal>
      )}
    </div>
  )
}

// TerminalView mounts an xterm.js terminal bound to one chat session's PTY over
// a dedicated WebSocket. The backend keeps the process alive across disconnects
// and replays scrollback on reconnect, so remounting (session switch, refresh)
// reattaches to the same live CLI.
function TerminalView({ sessionId, fontSize }: { sessionId: string; fontSize: number }) {
  const containerRef = useRef<HTMLDivElement>(null)
  // Set inside the effect; the key bar reaches the live terminal/socket through
  // these rather than forcing a remount when the connection is replaced.
  const sendRef = useRef<(data: string) => void>(() => {})
  const focusRef = useRef<() => void>(() => {})
  // Sticky Ctrl: the ref is what onData reads (it must see the current value
  // without re-registering the handler); the state only drives the button style.
  const ctrlRef = useRef(false)
  const [ctrlArmed, setCtrlArmed] = useState(false)
  // Phones: false = type in the message box, true = type straight into xterm.
  const [directMode, setDirectMode] = useState(false)
  const directRef = useRef(false)
  const [showJump, setShowJump] = useState(false)
  const scrollToBottomRef = useRef<() => void>(() => {})
  const applyFontRef = useRef<(n: number) => void>(() => {})
  const fontSizeRef = useRef(fontSize)
  const [promptLike, setPromptLike] = useState(false)

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    // Smaller font on phones so a usable number of columns fits the width.
    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: fontSizeRef.current,
      theme: { background: '#0f172a' }, // slate-900, matches the app chrome
      cursorBlink: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(container)

    let ws: WebSocket | null = null
    let closedByUs = false
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let keepAliveTimer: ReturnType<typeof setInterval> | null = null
    const encoder = new TextEncoder()

    function sendResize() {
      if (ws?.readyState === WebSocket.OPEN) {
        // Resize goes as a TEXT frame (a string, not a Uint8Array) so the
        // backend can tell it apart from keystrokes, which are binary frames.
        // Sending it as binary would fall through to the PTY as literal stdin —
        // the CLI would print "\x00resize:..." and never actually resize.
        ws.send(`\x00resize:${term.cols},${term.rows}`)
      }
    }

    // Refit to the current container box and tell the PTY. Guarded because
    // fit() throws if the container isn't laid out yet (0x0), which happens on
    // the first mobile paint before flex resolves.
    function refit() {
      try {
        fit.fit()
        sendResize()
      } catch { /* container not laid out yet; a later call will succeed */ }
    }

    applyFontRef.current = (n: number) => {
      term.options.fontSize = n
      refit()
    }

    // Initial fit after layout settles. On mobile the container often measures
    // 0x0 synchronously on mount, so defer to the next frame (and once more, in
    // case fonts/flex settle a beat later) rather than fitting to a stale size.
    requestAnimationFrame(() => {
      refit()
      requestAnimationFrame(refit)
    })

    // (Re)connect to the session's PTY. The backend keeps the process alive and
    // replays scrollback on attach, so reconnecting after a drop just redraws the
    // current screen. Any drop that isn't our own teardown retries in 2s.
    async function connect() {
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const base = import.meta.env.BASE_URL.replace(/\/$/, '')
      const ticket = await wsTicketParam()
      if (closedByUs) return
      const url = `${proto}//${window.location.host}${base}/api/v1/chat/sessions/${sessionId}/terminal${ticket}`
      ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'

      // Send the size once connected — by now the deferred fit has run, so the
      // PTY starts at the real terminal dimensions rather than the 80x24 default.
      ws.onopen = () => {
        sendResize()
        // Keepalive: idle terminals send no frames, so a proxy/LB idle timeout
        // silently reaps the connection. A periodic resize is a frame the backend
        // already understands and a no-op when the size is unchanged.
        keepAliveTimer = setInterval(sendResize, 30000)
      }
      ws.onmessage = (e) => {
        if (typeof e.data === 'string') term.write(e.data)
        else term.write(new Uint8Array(e.data))
      }
      ws.onclose = () => {
        if (keepAliveTimer) { clearInterval(keepAliveTimer); keepAliveTimer = null }
        if (closedByUs) return
        reconnectTimer = setTimeout(connect, 2000)
      }
    }
    connect()

    // Keystrokes -> PTY. Registered once; reads the current ws each time so it
    // keeps working across reconnects (don't re-register inside connect()).
    function send(data: string) {
      if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
    }
    sendRef.current = send
    focusRef.current = () => term.focus()

    term.onData((data) => {
      // An armed Ctrl (from the key bar) folds into the next character typed on
      // the device keyboard, giving phones a working Ctrl+R / Ctrl+A / etc.
      if (ctrlRef.current) {
        ctrlRef.current = false
        setCtrlArmed(false)
        const ctrl = toControlChar(data)
        if (ctrl !== null) {
          send(ctrl)
          return
        }
      }
      send(data)
    })

    // Refit on container resize (desktop pane drag, orientation change).
    const ro = new ResizeObserver(refit)
    ro.observe(container)

    // Mobile: tapping the terminal must focus xterm's hidden textarea, which is
    // what summons the on-screen keyboard — without this, touch users can't type.
    // On phones the message box owns typing unless direct mode is on; on desktop
    // (composer hidden) always focus.
    const desktopMq = window.matchMedia?.('(min-width: 768px)')
    const focus = () => {
      if (directRef.current || !desktopMq || desktopMq.matches) term.focus()
    }
    container.addEventListener('click', focus)

    // Touch: tap focuses; a vertical drag scrolls the scrollback with momentum.
    let t0: { x: number; y: number; time: number } | null = null
    let lastY = 0
    let lastT = 0
    let velocity = 0 // px/ms
    let remainder = 0
    let raf = 0
    const lineHeight = () => {
      const h = container.querySelector('.xterm-screen')?.clientHeight ?? 0
      return h > 0 && term.rows > 0 ? h / term.rows : 16
    }
    const scrollBy = (px: number) => {
      const r = pixelsToLines(px, remainder, lineHeight())
      remainder = r.remainder
      if (r.lines) term.scrollLines(r.lines)
    }
    const onTouchStart = (e: TouchEvent) => {
      cancelAnimationFrame(raf)
      const t = e.touches[0]
      t0 = { x: t.clientX, y: t.clientY, time: Date.now() }
      lastY = t.clientY
      lastT = e.timeStamp
      velocity = 0
      remainder = 0
    }
    const onTouchMove = (e: TouchEvent) => {
      if (!t0) return
      const t = e.touches[0]
      const dy = lastY - t.clientY
      const dt = Math.max(1, e.timeStamp - lastT)
      velocity = dy / dt
      lastY = t.clientY
      lastT = e.timeStamp
      if (e.cancelable) e.preventDefault()
      scrollBy(dy)
    }
    const onTouchEnd = (e: TouchEvent) => {
      if (!t0) return
      const t = e.changedTouches[0]
      const start = t0
      t0 = null
      if (isTap(t.clientX - start.x, t.clientY - start.y, Date.now() - start.time)) {
        focus()
        return
      }
      let v = velocity * 16 // px per frame
      const step = () => {
        if (Math.abs(v) < 0.5) return
        scrollBy(v)
        v *= 0.95
        raf = requestAnimationFrame(step)
      }
      if (Math.abs(velocity) > 0.1) raf = requestAnimationFrame(step)
    }
    container.addEventListener('touchstart', onTouchStart, { passive: true })
    container.addEventListener('touchmove', onTouchMove, { passive: false })
    container.addEventListener('touchend', onTouchEnd)

    // "Jump to latest" visibility.
    const updateJump = () => {
      const b = term.buffer.active
      setShowJump(b.viewportY < b.baseY)
    }
    const d1 = term.onScroll(updateJump)
    const updatePrompt = () => {
      const b = term.buffer.active
      for (let i = b.baseY + term.rows - 1; i >= b.baseY; i--) {
        const text = b.getLine(i)?.translateToString(true) ?? ''
        if (text.trim()) { setPromptLike(looksLikePrompt(text)); return }
      }
      setPromptLike(false)
    }
    const d2 = term.onWriteParsed(() => { updateJump(); updatePrompt() })
    scrollToBottomRef.current = () => { term.scrollToBottom(); setShowJump(false) }

    // Mobile: the on-screen keyboard shrinks the visual viewport without changing
    // the container's box, so ResizeObserver doesn't fire. Track visualViewport
    // directly and refit so the bottom rows stay above the keyboard.
    const vv = window.visualViewport
    vv?.addEventListener('resize', refit)

    return () => {
      closedByUs = true
      sendRef.current = () => {}
      focusRef.current = () => {}
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (keepAliveTimer) clearInterval(keepAliveTimer)
      ro.disconnect()
      cancelAnimationFrame(raf)
      container.removeEventListener('touchstart', onTouchStart)
      container.removeEventListener('touchmove', onTouchMove)
      container.removeEventListener('touchend', onTouchEnd)
      container.removeEventListener('click', focus)
      d1.dispose()
      d2.dispose()
      scrollToBottomRef.current = () => {}
      applyFontRef.current = () => {}
      vv?.removeEventListener('resize', refit)
      ws?.close()
      term.dispose()
    }
  }, [sessionId])

  useEffect(() => {
    fontSizeRef.current = fontSize
    applyFontRef.current(fontSize)
  }, [fontSize])

  // Key-bar press: write the sequence, then hand focus back to xterm so the
  // on-screen keyboard stays up.
  function pressKey(seq: string) {
    sendRef.current(seq)
    focusRef.current()
  }

  function toggleDirect() {
    directRef.current = !directRef.current
    setDirectMode(directRef.current)
    if (directRef.current) focusRef.current()
    else (document.activeElement as HTMLElement | null)?.blur?.()
  }

  function toggleCtrl() {
    ctrlRef.current = !ctrlRef.current
    setCtrlArmed(ctrlRef.current)
    focusRef.current()
  }

  // touch-manipulation avoids the 300ms tap delay / double-tap-zoom on the
  // terminal; the flex box gives it a real height for FitAddon to measure.
  return (
    <>
      <div className="relative flex-1 min-h-0 flex flex-col">
        <div ref={containerRef} className="flex-1 min-h-0 bg-surface-raised p-4 touch-none md:touch-manipulation" />
        {showJump && (
          <button
            type="button"
            onClick={() => scrollToBottomRef.current()}
            className="absolute bottom-3 right-3 px-3 py-1.5 rounded-full bg-accent text-on-accent text-xs shadow-lg"
          >
            Jump to latest ↓
          </button>
        )}
      </div>
      <MessageComposer
        onSend={(d) => sendRef.current(d)}
        directMode={directMode}
        onToggleDirect={toggleDirect}
      />
      <TerminalKeyBar onKey={pressKey} onToggleCtrl={toggleCtrl} ctrlArmed={ctrlArmed} promptLike={promptLike} />
    </>
  )
}
