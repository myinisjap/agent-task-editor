import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, type ChatSession, type Repo } from '../api/client'
import { wsTicketParam } from '../api/ws'

// Provider keys mirror the backend AgentConfig.Provider values (see main.go's
// providerFactory) and the terminalCommand() switch. The `anthropic` API
// provider is intentionally absent — it has no interactive CLI to run in a PTY.
const PROVIDERS = [
  { value: 'claude', label: 'Claude' },
  { value: 'qwen_code', label: 'Qwen' },
  { value: 'gemini_cli', label: 'Gemini' },
  { value: 'codex_cli', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
]

export default function ChatPage() {
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  // New-session form
  const [newRepo, setNewRepo] = useState('')
  const [newProvider, setNewProvider] = useState('claude')

  // Initial load: sessions + repos (repos needed for the new-session picker).
  // Coerce to [] — the API marshals an empty list as JSON null (Go nil slice).
  useEffect(() => {
    api.chat.list().then((s) => setSessions(s ?? [])).catch(() => {})
    api.repos.list().then((r) => {
      r = r ?? []
      setRepos(r)
      if (r.length > 0) setNewRepo(r[0].id)
    }).catch(() => {})
  }, [])

  async function createSession() {
    if (!newRepo) return
    const sess = await api.chat.create({ repo_id: newRepo, provider: newProvider })
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
    <div className="h-full min-h-0 flex">
      {/* Left: session list + new-session form.
          Mobile: full width, hidden once a chat is open. Desktop: fixed rail. */}
      <div className={`${active ? 'hidden md:flex' : 'flex'} w-full md:w-64 shrink-0 border-r border-slate-800 flex-col min-h-0 bg-slate-900`}>
        <div className="p-3 border-b border-slate-800 space-y-2">
          <div className="text-slate-200 font-semibold text-sm">New terminal</div>
          <select
            value={newRepo}
            onChange={(e) => setNewRepo(e.target.value)}
            className="w-full text-base md:text-xs rounded bg-slate-800 border-slate-700 text-slate-200 px-2 py-2 md:py-1"
          >
            {repos.length === 0 && <option value="">No repos configured</option>}
            {repos.map((r) => (
              <option key={r.id} value={r.id}>{r.name}</option>
            ))}
          </select>
          <select
            value={newProvider}
            onChange={(e) => setNewProvider(e.target.value)}
            className="w-full text-base md:text-xs rounded bg-slate-800 border-slate-700 text-slate-200 px-2 py-2 md:py-1"
          >
            {PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
          <button
            onClick={createSession}
            disabled={!newRepo}
            className="w-full text-xs px-2 py-1.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
          >
            Start terminal
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {sessions.map((s) => {
            const repo = repos.find((r) => r.id === s.repo_id)
            return (
              <div
                key={s.id}
                onClick={() => setActiveId(s.id)}
                className={`group px-3 py-3 md:py-2 cursor-pointer border-b border-slate-800/50 flex items-start justify-between gap-2 ${
                  s.id === activeId ? 'bg-slate-800' : 'hover:bg-slate-800/50'
                }`}
              >
                <div className="min-w-0">
                  <div className="text-slate-200 text-xs truncate">{s.title || repo?.name || 'Chat'}</div>
                  <div className="text-slate-500 text-[11px] truncate">{s.provider}{repo ? ` · ${repo.name}` : ''}</div>
                </div>
                <button
                  onClick={(e) => { e.stopPropagation(); deleteSession(s.id) }}
                  aria-label="Delete chat"
                  className="opacity-100 md:opacity-0 md:group-hover:opacity-100 text-slate-500 hover:text-red-400 text-sm px-1"
                >
                  ✕
                </button>
              </div>
            )
          })}
          {sessions.length === 0 && (
            <p className="text-slate-600 text-xs px-3 py-3">No terminals yet</p>
          )}
        </div>
      </div>

      {/* Right: the live terminal. Inverse of the sidebar on mobile. */}
      <div className={`${active ? 'flex' : 'hidden md:flex'} flex-1 flex-col min-w-0 min-h-0`}>
        {active ? (
          <>
            <div className="px-4 py-2 border-b border-slate-800 text-slate-400 text-xs flex items-center gap-2">
              <button
                onClick={() => setActiveId(null)}
                aria-label="Back to chats"
                className="md:hidden text-slate-400 hover:text-slate-100 -ml-1 pr-1"
              >
                ‹ Chats
              </button>
              {active.provider}
              {repos.find((r) => r.id === active.repo_id) && ` · ${repos.find((r) => r.id === active.repo_id)!.name}`}
            </div>
            {/* keyed by session id so switching sessions remounts a fresh terminal */}
            <TerminalView key={active.id} sessionId={active.id} />
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center text-slate-600 text-sm">
            Select a terminal or start a new one.
          </div>
        )}
      </div>
    </div>
  )
}

// TerminalView mounts an xterm.js terminal bound to one chat session's PTY over
// a dedicated WebSocket. The backend keeps the process alive across disconnects
// and replays scrollback on reconnect, so remounting (session switch, refresh)
// reattaches to the same live CLI.
function TerminalView({ sessionId }: { sessionId: string }) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    // Smaller font on phones so a usable number of columns fits the width.
    const isNarrow = window.innerWidth < 640
    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: isNarrow ? 11 : 13,
      theme: { background: '#0f172a' }, // slate-900, matches the app chrome
      cursorBlink: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(container)

    let ws: WebSocket | null = null
    let closedByUs = false
    const encoder = new TextEncoder()

    function sendResize() {
      if (ws?.readyState === WebSocket.OPEN) {
        // Control frame the backend recognizes: NUL + "resize:<cols>,<rows>".
        ws.send(encoder.encode(`\x00resize:${term.cols},${term.rows}`))
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

    // Initial fit after layout settles. On mobile the container often measures
    // 0x0 synchronously on mount, so defer to the next frame (and once more, in
    // case fonts/flex settle a beat later) rather than fitting to a stale size.
    requestAnimationFrame(() => {
      refit()
      requestAnimationFrame(refit)
    })

    ;(async () => {
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const base = import.meta.env.BASE_URL.replace(/\/$/, '')
      const ticket = await wsTicketParam()
      const url = `${proto}//${window.location.host}${base}/api/v1/chat/sessions/${sessionId}/terminal${ticket}`
      ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'

      // Send the size once connected — by now the deferred fit has run, so the
      // PTY starts at the real terminal dimensions rather than the 80x24 default.
      ws.onopen = () => sendResize()
      ws.onmessage = (e) => {
        if (typeof e.data === 'string') term.write(e.data)
        else term.write(new Uint8Array(e.data))
      }
      ws.onclose = () => {
        if (!closedByUs) term.write('\r\n\x1b[90m[disconnected]\x1b[0m\r\n')
      }

      // Keystrokes -> PTY.
      term.onData((data) => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
      })
    })()

    // Refit on container resize (desktop pane drag, orientation change).
    const ro = new ResizeObserver(refit)
    ro.observe(container)

    // Mobile: tapping the terminal must focus xterm's hidden textarea, which is
    // what summons the on-screen keyboard — without this, touch users can't type.
    const focus = () => term.focus()
    container.addEventListener('touchend', focus)
    container.addEventListener('click', focus)

    // Mobile: the on-screen keyboard shrinks the visual viewport without changing
    // the container's box, so ResizeObserver doesn't fire. Track visualViewport
    // directly and refit so the bottom rows stay above the keyboard.
    const vv = window.visualViewport
    vv?.addEventListener('resize', refit)

    return () => {
      closedByUs = true
      ro.disconnect()
      container.removeEventListener('touchend', focus)
      container.removeEventListener('click', focus)
      vv?.removeEventListener('resize', refit)
      ws?.close()
      term.dispose()
    }
  }, [sessionId])

  // touch-manipulation avoids the 300ms tap delay / double-tap-zoom on the
  // terminal; the flex box gives it a real height for FitAddon to measure.
  return <div ref={containerRef} className="flex-1 min-h-0 bg-slate-900 p-2 touch-manipulation" />
}
