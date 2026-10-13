# 19. Agent subprocesses get an allow-listed environment

Status: Accepted

## Context

The backend environment holds secrets an agent must never see (`API_TOKEN`,
`LLM_API_KEY`, `DATABASE_URL`/`DB_PASSWORD`). Inheriting `os.Environ()` leaks
them (issue #321). Allow-listing in turn caused a subtle failure: `GITHUB_TOKEN`
was missing from the list, so agents could not authenticate `git push`
(`could not read Username for 'https://github.com'`), while the backend's own
post-run `PushBranch` (full environment) worked, masking the gap as a
"non-fast-forward" divergence.

## Decision

`allowlistEnv(<provider>EnvAllowlist)` in `providers/cli.go` builds the base
environment: `commonBaseEnvKeys` (PATH, HOME, locale, proxy/TLS trust,
`GITHUB_TOKEN`/`GH_TOKEN`) plus per-provider keys (e.g. `ANTHROPIC_*`).
`mergeEnv` then layers operator-supplied `AgentConfig.Env` (from the
`ProviderConfig`) on top, blocking `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`,
`HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH` with a
logged warning. Guards: `TestAllowlistEnv_EveryProviderHasGitHubToken`,
`...HasPathAndHome`, `...NoProviderLeaksBackendSecrets`.

## Consequences

- A new env var an agent needs must be added to the allowlist, or it silently
  never arrives.
- Never add a real backend secret to the allowlist.
- Chat sessions and task runs both inherit the allowlist behavior, including
  `SSL_CA_CERT_PATH`-derived variables.


> Note (v0.17.0): `LLM_API_KEY` named above no longer exists (the `anthropic`/`llm` providers were removed); the allowlist rationale applies unchanged to `API_TOKEN`, DB credentials, etc.
