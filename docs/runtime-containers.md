# Per-Repo Runtime Containers

By default, every agent CLI run executes *inside the backend container*, against
a bind-mounted copy of your repo (see
[Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain)).
That means every repo shares one toolchain — whatever's installed in
`backend/Dockerfile`'s final image — and two repos that need conflicting
versions of the same language can't both be served by one backend image.

A repo can opt out of that shared toolchain: its agent CLI runs inside its own
long-lived container instead, built from an image you choose directly.

## Resolution order

Each repo resolves to exactly one of these, checked in order:

1. **`repos.runtime_image`** — an explicit image ref. No build. This is the
   escape hatch.
2. **None** (the default) — run in-process in the backend container, exactly
   as before this feature existed.

## Requirements

Two things must be true for `runtime_image` to actually work, on top of
setting the field itself:

1. **The `docker` CLI must be on the backend image's `PATH`.** `backend/Dockerfile`
   installs it via `apk add docker-cli`.
2. **The backend container must be able to reach a Docker daemon** — in
   practice, `/var/run/docker.sock` bind-mounted in. Both `docker-compose.yml`
   and `docker-compose.release.yml` mount it into the `backend` service by
   default.

If either is missing, every task on a repo with `runtime_image` set escalates
to `waiting_human` with an error like `exec: "docker": executable file not
found in $PATH` or a permission/connection error talking to the socket — see
"When the container can't start" below. Read the socket section before relying
on this in production: mounting it grants the backend host-root-equivalent
access (see "The Docker socket requirement, stated honestly" below).

### The socket permission problem, and how it's solved

The backend process runs as `node` (uid 1000 by default, remapped to
`PUID`/`PGID` at container start — see `backend/entrypoint.sh`), not root. A
bind-mounted `/var/run/docker.sock` is owned by `root:docker` on the host, and
the host's `docker` group's gid varies by distro/install method — there's no
single value that's correct to bake into the image or hardcode in a compose
file.

`entrypoint.sh` solves this at container startup, while it's still running as
root (before dropping to `node` via `su-exec`): if `/var/run/docker.sock` is
present, it reads the socket's actual gid with `stat`, creates (or reuses) a
group with that gid inside the container, and adds `node` to it. This adapts
automatically to whatever gid the socket has on the host — no `DOCKER_GID` env
var or manual group setup needed. If the socket isn't mounted at all (the
common case for anyone not using `runtime_image`), this step is a no-op.

This has been verified end-to-end: with the socket mounted per the compose
files above, `docker exec -u node <backend container> docker ps` succeeds and
lists the host's actual running containers.

## Setting an explicit image ref

`runtime_image` is a field on a repo, set via the repo update endpoint (or the
Repos page UI — a plain text field, empty by default):

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<repo_id> \
  -H "Content-Type: application/json" \
  -d '{"runtime_image": "mcr.microsoft.com/devcontainers/go:1.24"}'
```

It's a plain string column (`repos.runtime_image`, migration `055`), empty by
default. See `openapi.yaml`'s `Repo` schema for the full field list.

**Empty `runtime_image` (the default) is unchanged behavior**: the agent CLI
runs in-process in the backend container, exactly as before this feature
existed. Setting it is strictly opt-in, per repo.

## What happens when `runtime_image` is set

The dispatcher resolves a running container for the repo before starting a
run (`internal/agent/runtime.go`'s `RuntimeManager.EnsureRunning`):

- It looks for an existing container named `ate-runtime-<repo_id>` already
  running the current `runtime_image`. If found, it's reused as-is.
- Otherwise (no container, or one running a stale image), it removes any
  existing container by that name and starts a fresh one with
  `docker run -d ... <image> sleep infinity` — a long-lived container kept
  alive by an idle `sleep`, not a fresh container per task.
- If `docker run` fails because a container by that name already exists
  (another `EnsureRunning` call for the same repo won a race — e.g. two
  dispatches landing close together, or the sweeper's removal not yet fully
  propagated), it re-inspects and reuses the winning container rather than
  failing the run outright.
- The agent CLI (`claude`, `codex`, etc.) is then run inside it via
  `docker exec -i -w <workdir> ... <container> <bin> <args>`
  (`internal/agent/providers/cli.go`'s `spawn`). Every tool the CLI invokes —
  `Bash`, `Edit`, `cargo test`, whatever — is a child of that process, so it
  inherits the container too.
- Concurrent dispatches for the same repo are serialized around container
  creation (a per-repo lock in `RuntimeManager`), so two tasks racing to
  create the container can't produce duplicates; once running, `docker exec`
  supports concurrent execs into the same container fine.

## The mount contract

Four things are bind-mounted into the container, and **all of them land at
the exact same absolute path they have on the host** — this is not a
convenience choice, it's mandatory:

| Mount | Mode | Why same-path is mandatory |
|---|---|---|
| Repo root | read-write | A git worktree's `.git` is not a directory — it's a **file** containing an absolute path back to `<repo>/.git/worktrees/<task_id>` on the *host*. Task runs happen inside worktrees at `<repo>/.ate-worktrees/<task_id>` (see `worktreesweep`). Mount the repo anywhere else inside the container and that pointer dangles — git fails outright, not just with a warning. |
| `/tmp` | read-write | The MCP sidecar handoff (`signal_complete`/`request_human`) writes its config and result files under `/tmp` on the host and reads them back after the container process exits. A different `/tmp` inside the container breaks that round-trip. |
| The `mcp-server` binary (`MCP_SERVER_PATH`) | read-only | The generated `--mcp-config` JSON references the sidecar by its absolute host path. If that path doesn't exist inside the container too, the CLI can't launch it. |
| Provider credential directories (`~/.claude`, `~/.claude.json`, `~/.codex`, `~/.qwen`) | **read-write** | See below. |

A source path that doesn't exist on the host (e.g. no `~/.codex` because this
deployment only uses `claude`) is silently skipped for that one directory,
rather than mounting an empty directory in its place — that keeps "provider
not configured" distinguishable from "empty credential dir" instead of
masking one as the other.

### Credentials are mounted read-write, not read-only

This is deliberate, not an oversight: the `claude` CLI **rotates its OAuth
refresh token on use**. A read-only mount would let the container read the
current token but silently fail to persist rotation, breaking auth on some
later run. Other CLIs may similarly update their own config/session state
mid-run. All four credential directories are mounted read-write for this
reason.

Each per-container run also applies `--security-opt no-new-privileges`,
`--cap-drop ALL`, and a `--pids-limit` (default 512) — hardening for what a
runaway or misbehaving process *inside* the container can do. See
`buildDockerRunArgs` in `internal/agent/runtime.go`.

## The `HOME=/home/vscode` assumption

Credentials are mounted from the backend's own home directory (e.g.
`~/.claude` on the host) to `/home/vscode/.claude` inside the container —
`agent.RuntimeContainerHome` is hardcoded to `/home/vscode`. The CLI's `HOME`
env var inside the container is also forced to `/home/vscode` for the same
run (`providers/cli.go`'s `containerEnvOverrides`), so the mounted credentials
and the CLI's own idea of `HOME` agree.

This matches the convention used by the standard devcontainer/VS Code Dev
Containers images (`mcr.microsoft.com/devcontainers/*`, and anything built on
top of them): a non-root `vscode` user with `HOME=/home/vscode`. **Images
following that convention work out of the box.**

**A plain upstream image does not.** `golang:1.26`, for example, runs as
`root` with `HOME=/root` — the credentials get mounted to
`/home/vscode/.claude`, a path the container's own `HOME` never points at,
and the CLI fails auth with a misleading "not logged in" rather than an
obvious "wrong path" error. There's no per-image detection here; the
container's actual user/home is never introspected.

This is a known, named limitation, not an accident — from the doc comment on
`RuntimeContainerHome` in `internal/agent/runtime.go`:

> `ponytail: fixed value, not introspected per image. Correct for
> devcontainer-convention images (the documented/supported case) but wrong
> for a plain upstream image like golang:1.26, which runs as root with
> HOME=/root — there the credential mounts land somewhere the CLI never
> reads and auth fails with a misleading "not logged in". Upgrade path when
> that matters: read the image's configured user/home once at
> EnsureRunning time (`docker image inspect -f '{{.Config.User}}'`, or
> `docker exec … sh -c 'echo $HOME'` against the started container) and
> store it on the container record alongside the ate.image label, instead
> of assuming one convention.`

Until that upgrade path is built, **use a devcontainer-convention image** (or
one you've built yourself with a `vscode` user and matching `HOME`) for
`runtime_image`.

One related consequence: `spawn()`'s `docker exec -e` layers the passed
environment *on top of* the container image's own env, so the backend's own
`PATH` and `HOME` would otherwise leak through and be wrong for a different
image. `containerEnvOverrides` handles this by dropping `PATH` entirely (so
the image's own `PATH` — the one that actually resolves its installed CLI —
wins) and forcing `HOME` to `/home/vscode` as described above.

## The Docker socket requirement, stated honestly

Ensuring a runtime container exists requires the backend process to talk to
the Docker daemon (`docker run`, `docker exec`, `docker ps`, `docker rm`) —
in practice, access to `/var/run/docker.sock` or an equivalent `DOCKER_HOST`.

**That socket is root-equivalent on the host.** Anything that can write to it
can start a container with `-v /:/host` and chroot into the host filesystem
as root. There is no permission model on the socket itself that narrows this.

This is not a new risk category introduced by this feature, but it is worth
stating plainly rather than glossing over: the backend already executes
agent-authored code with repo write access and API/provider credentials.
Docker socket access widens the blast radius of an existing trust
boundary (host root, instead of "just" the mounted repos and credentials) —
it does not create untrusted code execution where none existed before.

No sandboxing, socket proxy, or reduced-privilege runtime ships with this
feature. The per-container flags that are applied
(`--security-opt=no-new-privileges`, `--cap-drop=ALL`, `--pids-limit`) reduce
what a runaway or misbehaving process *inside* a runtime container can do —
they are not a boundary against the backend process itself, which already
has the keys to the socket. Treat `runtime_image` as an escape hatch you
point at images and repos you trust, on hosts where you're comfortable with
the backend having that level of access — the same posture this project
already asks for without this feature.

If your threat model requires isolating the backend from host root — e.g.
running untrusted third-party repos, or a multi-tenant deployment — this
feature does not solve that, and no configuration of it will. If you don't
want the backend to have this access at all, remove the
`/var/run/docker.sock` volume line from your compose file (or override it) —
`runtime_image` will then reliably fail closed to `waiting_human` for any
repo that sets it, rather than silently running in-process.

## When the container can't start

If a repo has `runtime_image` set but the container can't be created, found
running, or reached (Docker unreachable, socket not mounted, `docker`
binary missing, image pull failure, daemon error, or `runtime_image` set with
no `RuntimeManager` configured at all), the dispatcher does **not** fall back
to running the task in-process in the backend container. Silently falling
back would run the agent CLI against a toolchain and credential set it was
never meant to use.

Instead, the run is marked `waiting_human` immediately, before any provider
process starts, with a note describing what failed. The task stays locked to
that run (its dispatch lock is not cleared) so the 5-second dispatch sweep
doesn't immediately retry and hot-loop on the same failure — a human needs to
fix the underlying problem (or clear the repo's `runtime_image`) and reply
before the task can be picked up again.

## Reaping stale and orphaned containers

`worktreesweep`'s periodic sweep (`sweeper.go`) also reconciles runtime
containers, alongside its existing worktree-directory cleanup:

- A managed container (one carrying the `ate.repo_id` label) is removed if its
  repo no longer exists, or if its `ate.image` label no longer matches that
  repo's current `runtime_image` (including a repo that had `runtime_image`
  cleared back to empty — the sweep enforces "empty means in-process" even for
  containers left running from before the field was cleared).
- **A container is skipped this tick — never reaped — if its repo has a task
  with a non-terminal, in-flight run** (a non-nil `active_agent_run_id`).
  `EnsureRunning` resolves the container name well before the provider
  actually `docker exec`s into it (pool enqueue, prompt building, and MCP prep
  happen in between, which can take minutes under queue backpressure);
  reaping the container in that window would kill an in-flight run with a raw
  "No such container" error instead of a clean result. Such a container is
  reaped on a later tick, once the repo goes idle.

The sweeper shares the *same* `RuntimeManager` instance as the dispatcher
(wired in `cmd/server/main.go`), not a second one, so its per-repo container
lock actually serializes against `EnsureRunning` — a container isn't reaped
mid-creation by one path while the other is starting it.

## Related

- `internal/agent/runtime.go` — `RuntimeManager`, container lifecycle,
  `buildDockerRunArgs`.
- `internal/agent/dispatcher.go` — resolves `runtime_image` before `startRun`
  and escalates to `waiting_human` on failure.
- `internal/worktreesweep/sweeper.go` — reaping stale/orphaned runtime
  containers, skipping repos with an in-flight run.
- `internal/agent/providers/cli.go` — `spawn()`, `containerEnvOverrides`.
- `backend/entrypoint.sh` — the Docker-socket-gid join described above.
- [Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain) —
  the backend-image toolchain this feature is an alternative to.
