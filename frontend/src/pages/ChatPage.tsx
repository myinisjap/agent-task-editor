import { useEffect, useRef, useState } from 'react'
import { api, type ChatSession, type ChatMessage, type Repo, type AgentLog } from '../api/client'
import { wsClient } from '../api/ws'
import AgentLogEntry from '../components/board/AgentLogEntry'

// Provider keys mirror the backend AgentConfig.Provider values (see main.go's
// providerFactory). All support session resume, so any works for chat.
const PROVIDERS = [
  { value: 'claude', label: 'Claude' },
  { value: 'qwen_code', label: 'Qwen' },
  { value: 'gemini_cli', label: 'Gemini' },
  { value: 'codex_cli', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'anthropic', label: 'Anthropic API' },
]

// A ChatMessage renders through AgentLogEntry, which expects an AgentLog shape.
function toLog(m: ChatMessage): AgentLog {
  return { id: m.id, agent_run_id: m.session_id, timestamp: m.created_at, type: m.type, content: m.content }
}

export default function ChatPage() {
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [running, setRunning] = useState(false)
  // New-session form
  const [newRepo, setNewRepo] = useState('')
  const [newProvider, setNewProvider] = useState('claude')

  const scrollRef = useRef<HTMLDivElement>(null)

  // Initial load: sessions + repos (repos needed for the new-session picker).
  // Coerce to [] — the API marshals an empty list as JSON null (Go nil slice),
  // which would otherwise land in state and blow up .find()/.map() below.
  useEffect(() => {
    api.chat.list().then((s) => setSessions(s ?? [])).catch(() => {})
    api.repos.list().then((r) => {
      r = r ?? []
      setRepos(r)
      if (r.length > 0) setNewRepo(r[0].id)
    }).catch(() => {})
  }, [])

  // Load transcript when the active session changes.
  useEffect(() => {
    if (!activeId) {
      setMessages([])
      return
    }
    api.chat.get(activeId).then((res) => setMessages(res.messages ?? [])).catch(() => {})
  }, [activeId])

  // Live updates: chat events broadcast to all clients, so filter by session.
  useEffect(() => {
    const off = wsClient.on((event) => {
      if (event.type === 'chat.message' && event.payload.session_id === activeId) {
        const m = event.payload.message
        setMessages((prev) =>
          prev.some((p) => p.id === m.id)
            ? prev
            : [...prev, { ...m, session_id: activeId }],
        )
      } else if (event.type === 'chat.turn_done' && event.payload.session_id === activeId) {
        setRunning(false)
      }
    })
    return off
  }, [activeId])

  // Keep the transcript scrolled to the newest message.
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [messages])

  async function createSession() {
    if (!newRepo) return
    const sess = await api.chat.create({ repo_id: newRepo, provider: newProvider })
    setSessions((prev) => [sess, ...prev])
    setActiveId(sess.id)
  }

  async function send() {
    const text = input.trim()
    if (!text || !activeId || running) return
    setInput('')
    setRunning(true)
    // Optimistically append the user's message; the server also persists and
    // broadcasts it, and the dedup-by-id in the WS handler prevents a double.
    setMessages((prev) => [
      ...prev,
      { id: `local-${Date.now()}`, session_id: activeId, type: 'user', content: text, created_at: new Date().toISOString() },
    ])
    try {
      await api.chat.send(activeId, text)
    } catch (e) {
      setRunning(false)
      setMessages((prev) => [
        ...prev,
        { id: `err-${Date.now()}`, session_id: activeId, type: 'stderr', content: String(e), created_at: new Date().toISOString() },
      ])
    }
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
          Mobile: full width, and hidden once a chat is open (one pane at a
          time — see the right pane's inverse rule). Desktop: fixed 16rem rail. */}
      <div className={`${active ? 'hidden md:flex' : 'flex'} w-full md:w-64 shrink-0 border-r border-slate-800 flex-col min-h-0 bg-slate-900`}>
        <div className="p-3 border-b border-slate-800 space-y-2">
          <div className="text-slate-200 font-semibold text-sm">New chat</div>
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
            Start chat
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
                  // Always visible on touch (no hover); reveal-on-hover on desktop.
                  className="opacity-100 md:opacity-0 md:group-hover:opacity-100 text-slate-500 hover:text-red-400 text-sm px-1"
                >
                  ✕
                </button>
              </div>
            )
          })}
          {sessions.length === 0 && (
            <p className="text-slate-600 text-xs px-3 py-3">No chats yet</p>
          )}
        </div>
      </div>

      {/* Right: transcript + composer. Inverse of the sidebar on mobile —
          hidden until a chat is open, so the list gets the full screen. */}
      <div className={`${active ? 'flex' : 'hidden md:flex'} flex-1 flex-col min-w-0 min-h-0`}>
        {active ? (
          <>
            <div className="px-4 py-2 border-b border-slate-800 text-slate-400 text-xs flex items-center gap-2">
              {/* Mobile-only back arrow — returns to the session list. */}
              <button
                onClick={() => setActiveId(null)}
                aria-label="Back to chats"
                className="md:hidden text-slate-400 hover:text-slate-100 -ml-1 pr-1"
              >
                ‹ Chats
              </button>
              {running && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
              {active.provider}
              {repos.find((r) => r.id === active.repo_id) && ` · ${repos.find((r) => r.id === active.repo_id)!.name}`}
            </div>
            <div ref={scrollRef} className="flex-1 overflow-y-auto px-2 py-2">
              {messages.length === 0 && (
                <p className="text-slate-600 text-xs px-3 py-3">Send a message to start.</p>
              )}
              {messages.map((m) => (
                <AgentLogEntry key={m.id} log={toLog(m)} />
              ))}
            </div>
            <div className="border-t border-slate-800 p-3 flex gap-2">
              <textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    send()
                  }
                }}
                placeholder={running ? 'Waiting for the agent…' : 'Message…'}
                rows={2}
                // text-base on mobile (< 16px triggers iOS focus-zoom); smaller on desktop.
                className="flex-1 resize-none text-base md:text-sm rounded bg-slate-800 border-slate-700 text-slate-200 px-3 py-2 focus:ring-indigo-500"
              />
              <button
                onClick={send}
                disabled={running || !input.trim()}
                className="px-4 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm disabled:opacity-50"
              >
                Send
              </button>
            </div>
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center text-slate-600 text-sm">
            Select a chat or start a new one.
          </div>
        )}
      </div>
    </div>
  )
}
