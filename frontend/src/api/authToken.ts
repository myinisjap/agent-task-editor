// authToken.ts — single source of truth for the runtime API bearer token.
//
// The frontend has no user/session model: it's a single shared token (or a
// named token from API_TOKENS) stored in the browser's localStorage and sent
// as `Authorization: Bearer <token>` on every REST/WS-ticket request. This
// deliberately replaces the old build-time `VITE_API_TOKEN` approach (which
// can never be baked into the prebuilt Docker image) — see client.ts's
// authedFetch/authedRawFetch and ws.ts's connect() for the call sites, and
// components/shared/ApiTokenGate.tsx for the UI that prompts for a token on
// first 401.

const STORAGE_KEY = 'ate_api_token'

// getApiToken returns the token currently stored in localStorage, if any.
// If nothing has been stored yet, it seeds localStorage from the build-time
// VITE_API_TOKEN (if set) so existing .env.local dev setups keep working
// without needing to go through the token prompt — but only once: after
// that, whatever's in localStorage (including an intentionally-cleared/
// empty value from the prompt) wins.
export function getApiToken(): string | null {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored) return stored

  const buildTimeDefault = import.meta.env.VITE_API_TOKEN
  if (buildTimeDefault) {
    localStorage.setItem(STORAGE_KEY, buildTimeDefault)
    return buildTimeDefault
  }
  return null
}

export function setApiToken(token: string): void {
  localStorage.setItem(STORAGE_KEY, token)
}

export function clearApiToken(): void {
  localStorage.removeItem(STORAGE_KEY)
}

// authHeaders returns {} or { Authorization: 'Bearer <token>' } — spread
// into any fetch init.headers.
export function authHeaders(): Record<string, string> {
  const token = getApiToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

// Simple pub/sub so the app shell (ApiTokenGate) can react to a 401 by
// prompting for a new token, without every call site needing its own error
// UI. notifyUnauthorized() is called by the shared fetch helpers in
// client.ts/ws.ts whenever a request comes back 401.
type UnauthorizedListener = () => void
const listeners: UnauthorizedListener[] = []

export function onUnauthorized(fn: UnauthorizedListener): () => void {
  listeners.push(fn)
  return () => {
    const i = listeners.indexOf(fn)
    if (i >= 0) listeners.splice(i, 1)
  }
}

export function notifyUnauthorized(): void {
  clearApiToken()
  listeners.forEach((fn) => fn())
}
