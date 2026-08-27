# Per-Repo Agent Toolchains (mise)

By default, every agent task runs inside the backend container with whatever
language toolchain the image ships — Go (matching the server's own build)
and Node.js out of the box, plus Claude Code CLI (and optionally Codex/Qwen).
That's fine for repos that happen to match the image, but a repo pinned to
Go 1.21, or Python 3.11, or a Rust `nightly-2024-06-01` toolchain, would
silently run against whatever the image ships instead — which may not even
compile the repo.

Per-repo runtime configuration fixes that: a repo can pin exact language
versions, and agent runs on that repo use `mise` (and, for Python, `uv`) to
install and switch to those versions before invoking the provider CLI.

## Supported languages

Fixed allowlist: `go`, `node`, `python`, `rust`, `ruby`, `java`. Version
strings are validated against `^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$` (letters,
digits, `.`, `_`, `-`; no `@`, no spaces, no leading `-`) both client- and
server-side, since they end up as `mise install`/`mise x` argv elements.

## Configuring pins

Open a repo's edit form (Repos page) and scroll to the **Agent runtime**
section:

- Add a row per language: pick the language from the dropdown, type the
  version (e.g. `1.21`, `22`, `3.12`).
- **Detect from repo** scans the repo's root directory (not worktrees, and
  not recursively) for well-known manifest files and pre-fills suggested
  rows:
  - `go.mod` → `go` (from the `go` directive)
  - `.nvmrc` / `.node-version` → `node`
  - `.python-version` → `python`
  - `rust-toolchain` / `rust-toolchain.toml` (`channel = "..."`) → `rust`
  - `.ruby-version` → `ruby`
  - `.java-version` → `java`

  Detection only *suggests* — it never saves anything on its own. Review
  the pre-filled rows and click Save yourself. Symlinked manifest files are
  skipped (a repo can't point `.nvmrc` somewhere outside itself to smuggle
  in an unintended suggestion).
- Leave the section empty and nothing changes: a repo with no pins spawns
  agent runs exactly as before this feature existed — the dispatcher only
  touches `mise`/`uv` when the repo has at least one saved pin.
- Saving a repo with non-empty pins kicks off a background `mise install`
  immediately, so the *next* task on that repo doesn't pay the cold-install
  cost — see [Cold vs. warm installs](#cold-vs-warm-installs) below.

## What happens on a task run

For a repo with pins configured, before the provider CLI is invoked:

1. Runs `mise install <id>@<version> ...` for every non-Python pin (10-minute
   timeout).
2. If a `python` pin is present, resolves the installed interpreter's path
   via `mise where python@<version>` and creates a per-task-worktree
   virtualenv with `uv venv --python <path> <worktree>/.venv` — reused as-is
   on a later run against the same worktree *unless* the venv's recorded
   interpreter version (`pyvenv.cfg`) no longer matches the repo's current
   python pin, in which case the stale venv is removed and recreated from
   the new pin (so bumping the pin can never silently leave a re-run on the
   old interpreter). `.venv` is excluded from `git status`/`git add -A` for
   that worktree, so it never leaks into the task's branch even for a repo
   that doesn't already gitignore it. Python is handled outside `mise x`
   deliberately — `mise x`'s `PATH` prepend would shadow the venv, so
   instead the spawn prepends `<worktree>/.venv/bin` to the child process's
   `PATH`, and `python`/`pip` resolve to the venv's own pinned interpreter.
3. Spawns the provider CLI wrapped as `mise x go@1.21 node@22 -- <binary>
   <args...>` (non-Python pins only — Python is excluded from the `mise x`
   argument list per the previous step).

This prep step runs in the run's own background goroutine, not on the
dispatcher's sweep loop or an HTTP request — a cold install can take minutes,
and running it synchronously there would freeze dispatch for every other task
(or get killed by an HTTP request timeout). The dispatcher only *validates*
a repo's pins synchronously (cheap — no `mise`/`uv` involved); the actual
install/venv work always happens after the run has already been handed to a
worker.

### Fail-closed behavior

If `mise install` (or, for Python, `uv venv`) fails or times out, the task
**escalates straight to Waiting for human** — it never silently falls back
to spawning against the container's default toolchain. The run's error
includes the tail of `mise`'s stderr, so the actual failure (bad version,
network issue, disk full, etc.) is visible on the run without digging into
container logs. Fix the pin (or the underlying issue) and move the task
back to an agent-triggerable state to retry.

## Chat sessions

Interactive chat sessions (the Chat page) apply the same repo pins as task
runs, so a chat session and a task run against the same repo see the same
toolchain: non-Python pins wrap the session's CLI with `mise x`, and a
Python pin gets a venv on `PATH`. Two differences from a task run:

- The Python venv is **not** created inside the chat session's worktree —
  that worktree persists across reconnects and is a live checkout a human
  is actively looking at, so dropping a build artifact there (even one
  excluded from git) is a worse experience than for a short-lived task
  worktree. Instead it lives outside any repo checkout, keyed by repo id, and
  is reused across every chat session against that repo (recreated on a pin
  version change, same as a task worktree's venv).
- A `node` pin does **not** wrap the chat CLI's own launch. Whether pinning
  node should also run the provider CLI itself (a Node.js program) under
  that pinned node is a separate, still-open design question that applies to
  task runs too — until it's resolved, node pins are inert for chat.
- Prep (mise install / venv resolve) runs once per session start, not on
  every reconnect — reattaching to an already-running session never re-runs
  it.

## Cache volumes

Both compose files (`docker-compose.yml`, `docker-compose.release.yml`) mount
two named volumes on the backend service:

| Volume | Container path | Purpose |
|---|---|---|
| `mise-data` | `/home/node/.local/share/mise` | Installed language runtimes (`mise install` output) |
| `uv-cache` | `/home/node/.cache/uv` | uv's package/wheel cache |

Both are content/version-addressed, so sharing them across every repo and
every task is safe — installing `go@1.21` once for repo A also benefits
repo B if it later pins the same version. They persist across container
restarts and image upgrades the same way `db_data` does. The entrypoint's
PUID/PGID remap (see [getting-started.md](getting-started.md)) chowns both
paths at startup, so a non-default host UID still gets a writable cache.

## Cold vs. warm installs

- **Cold** (first install of a given language/version, nothing cached yet):
  proportional to a normal `mise install`/`pip install` for that
  toolchain — typically tens of seconds for a Go or Node version, more for
  larger toolchains (Java, or a Python project with a heavy
  `requirements.txt`).
- **Warm** (version already in `mise-data`, packages already in
  `uv-cache`): near-instant — `mise install` is a no-op once the version is
  present, and `uv`'s cache means repeat `pip install`-equivalent runs
  mostly skip network/build work.

Saving a repo's runtime config pre-warms the cache in the background (see
above), so in practice most tasks hit the warm path; only the very first
task after a new pin (or a version bump) pays the cold cost.

## Container image

The backend image's runtime stage moved from `node:26-alpine` to
`node:26-bookworm-slim` to support this feature — mise's prebuilt
python/ruby/java toolchains require glibc, which alpine's musl libc
doesn't provide. If you build the image yourself, expect a larger image
than before (Debian's base is bigger than Alpine's) and rebuild any
downstream derivative images that assumed an Alpine base (e.g. anything
using `apk` in a multi-stage build on top of this image).
