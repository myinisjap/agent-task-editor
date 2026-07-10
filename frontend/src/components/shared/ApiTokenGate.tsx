import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { setApiToken, onUnauthorized } from '../../api/authToken'
import { wsClient } from '../../api/ws'

// ApiTokenGate wraps the whole app. It renders `children` normally until any
// request comes back 401 (see authedFetch/authedRawFetch in client.ts and
// ws.ts's ticket fetch), at which point onUnauthorized() fires and this
// component swaps in a minimal "enter API token" screen instead. It never
// renders speculatively — with API_TOKEN unset on the backend nothing ever
// 401s, so the prompt never appears.
export default function ApiTokenGate({ children }: { children: ReactNode }) {
  const [needsToken, setNeedsToken] = useState(false)
  const [input, setInput] = useState('')

  useEffect(() => {
    return onUnauthorized(() => setNeedsToken(true))
  }, [])

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    setApiToken(input.trim())
    // Reload so every already-mounted page's initial fetch retries cleanly
    // with the new token, rather than needing bespoke retry plumbing in
    // every store/page. wsClient.connect() also gets a fresh attempt as part
    // of the reload's normal startup path (see main.tsx).
    wsClient.connect()
    window.location.reload()
  }

  if (!needsToken) return <>{children}</>

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950">
      <form
        onSubmit={handleSubmit}
        className="bg-slate-900 border border-slate-700 rounded-lg shadow-xl p-6 w-full max-w-sm flex flex-col gap-4"
      >
        <div>
          <h2 className="text-lg font-semibold text-slate-100">API token required</h2>
          <p className="text-sm text-slate-400 mt-1">
            This server requires an API token. Enter it below to continue.
          </p>
        </div>

        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400 font-medium">Token</label>
          <input
            autoFocus
            type="password"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            className="bg-slate-800 border border-slate-700 rounded px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            placeholder="API token"
          />
        </div>

        <div className="flex justify-end">
          <button
            type="submit"
            disabled={!input.trim()}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
          >
            Save
          </button>
        </div>
      </form>
    </div>
  )
}
