# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

To cut a release, add a `## [x.y.z] - YYYY-MM-DD` section below with the changes,
then push the matching tag (`git tag vx.y.z && git push origin vx.y.z`). The
release workflow builds and pushes the images and creates a GitHub Release using
this file's section for that version as the release notes.

## [Unreleased]

### Security
- Fixed an authentication bypass where any request carrying an
  `Upgrade: websocket` header skipped bearer-token validation on **every** API
  route (not just `/ws`), letting a client read and write the full API without a
  token when `API_TOKEN` was set. The `/ws` route is now mounted outside the
  `BearerAuth` middleware (it does its own constant-time `?token=` check), and
  the header-based bypass has been removed.

### Added
- Cancel a running agent run: `POST /api/v1/tasks/{id}/runs/{run_id}/cancel` plus
  a **Stop run** button on the task detail page. The pool keeps a per-run cancel
  registry; cancelling cancels the run's context (killing CLI subprocesses via
  `exec.CommandContext` and aborting HTTP providers), then marks the run
  `cancelled` (not `failed`, and without consuming transient-retry budget),
  pauses the task so it isn't immediately re-dispatched, clears the active-run
  lock, and broadcasts `task.agent_done`. Fills the kill-switch gap where
  pausing only blocked *future* dispatch — a runaway agent no longer burns
  tokens until it times out.
- Provider health / onboarding status page (`Health` in the sidebar) backed by a
  new `GET /api/v1/health/providers` endpoint. Checks the claude CLI (present +
  authenticated), API keys for the anthropic/llm providers, qwen/opencode
  binaries (only for providers an enabled agent config uses), the MCP sidecar
  binary (`MCP_SERVER_PATH`), gh auth, and `REPO_BASE_DIR` — each rendered as a
  green/yellow/red row with a one-line fix hint. Turns the most common "why did
  my first run fail" support loop into a glance.

## [0.1.0] - 2026-07-04

First tagged release.

### Added
- Versioned multi-arch (amd64 + arm64) Docker images published to GHCR on every
  `v*` tag push, plus an automated GitHub Release.
- `docker-compose.release.yml` for running from the prebuilt
  `ghcr.io/myinisjap/agent-task-editor-{backend,frontend}` images instead of
  building from source.
- `run.sh` helper that injects the runtime env vars (repo mount, GitHub token,
  Claude auth, SSL bypass) and starts the stack from published images.
- Runtime `PUID`/`PGID` remap in the backend container: it steps down to the
  host user at startup (via an entrypoint + `su-exec`) so files agents write to
  bind-mounted repos are owned by you. Works for prebuilt images with no rebuild.
- Frontend unit tests (vitest) for the `src/lib` parsers — `parseAgentLog`,
  `parseWorkflowYaml`/`validateWorkflow`, `parseDiff`, `condensedBoard`, and
  `diffComments` — with real captured fixtures, wired into the frontend CI job.
- This changelog.

### Changed
- Backend image runs as the host user at runtime via `PUID`/`PGID` instead of a
  build-time `HOST_UID`/`HOST_GID` remap. `dev.sh`/`run.sh` set these from
  `id -u`/`id -g`; the build no longer bakes in a UID.
