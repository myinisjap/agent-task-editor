# 32. The frontend API token is a runtime value in localStorage

Status: Accepted

## Context

Vite build-time env vars are baked into the bundle, which cannot work for a
prebuilt, published Docker image.

## Decision

`src/api/authToken.ts` owns the token. `VITE_API_TOKEN` only seeds
`localStorage` once on first load for `.env.local` dev. On any 401 the stored
token is cleared and `ApiTokenGate` prompts; every request and WS-ticket mint
sends `Authorization: Bearer`. All requests go through `authedRawFetch` or the
internal `request()` wrappers. There is no user/session model, just one shared
or named token.

## Consequences

- Use `authHeaders()` for any new raw `fetch()`.
- Do not rely on `VITE_API_TOKEN` in production builds.
