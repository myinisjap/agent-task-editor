# 5. `REPO_BASE_DIR` and API auth are optional, with startup warnings

Status: Accepted

## Context

The project is self-hosted and meant to run with zero config on a laptop.
Forcing `REPO_BASE_DIR` or an API token would make local development and demos
harder, while production deployments should set both.

## Decision

An empty `REPO_BASE_DIR` allows any repo path and logs a warning at startup. An
empty `API_TOKEN`/`API_TOKENS` means no authentication and logs a `SECURITY:`
warning (stronger when CORS is `*` or the server listens on all interfaces).
Compensating controls apply when no token is set: the Host allowlist
(`ALLOWED_HOSTS`, DNS-rebinding protection), `RequireTrustedOrigin`, and
`CORS_ORIGINS` defaulting to the local dev origins rather than `*`.
`/metrics` follows the API auth unless `METRICS_TOKEN` or `METRICS_PUBLIC`
says otherwise. When `REPO_BASE_DIR` is set, repo create and update both
resolve symlinks before checking containment.

## Consequences

- New config that gates safety should follow the same shape: permissive by
  default, loud warning, enforced when set.
- Do not make these hard requirements without a migration path; the
  quick-start flows depend on them being optional.
