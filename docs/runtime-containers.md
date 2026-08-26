# Per-Repo Runtime Containers

By default, every agent CLI run executes *inside the backend container*, against
a bind-mounted copy of your repo (see
[Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain)).
That means every repo shares one toolchain — whatever's installed in
`backend/Dockerfile`'s final image — and two repos that need conflicting
versions of the same language can't both be served by one backend image.

A repo can opt out of that shared toolchain: its agent CLI runs inside its own
container instead, built either from an image you choose directly, or from a
`devcontainer.json` (a repo-committed file, or one authored in the UI).

## Resolution order

Each repo resolves to exactly one of these, checked in order:

1. **`repos.runtime_image`** — an explicit image ref. No build. This is the
   escape hatch and it always wins, skipping the devcontainer path entirely.
2. **`.devcontainer/devcontainer.json` committed in the repo** — the standard
   location every devcontainer-CLI consumer (VS Code, Codespaces, JetBrains,
   Zed) already looks for.
3. **`repos.devcontainer_json`** — the same format, authored in this app's UI
   and stored in the DB, for repos that don't ship their own file.
4. **None of the above** — run in-process in the backend container, exactly
   as before this feature existed.

Sources 2 and 3 are the *same format through the same builder* — only where
the JSON lives differs. "Graduating from the UI to a committed file" is just
committing the file; there's no export step or dual maintenance.

**A committed repo file always beats the UI-authored config, deliberately.**
The checked-in `.devcontainer/devcontainer.json` is the source of truth for
that repo — it's versioned, reviewed, and shared with every other tool that
reads it (VS Code, Codespaces, ...). If a UI toggle could silently override
it, editing the file wouldn't reliably change what actually runs; the agent's
environment could keep drifting from what's checked in. Resolving to the
repo file, and warning the user in the UI when their saved
`devcontainer_json` is being ignored (see below), avoids that surprise
instead of hiding it.

This resolution is implemented once, in `internal/agent/devcontainer.go`'s
`ResolveDevcontainerSource`, and used by both the dispatcher
(`dispatcher.go`'s `startRun`) and the `GET /repos/{id}/devcontainer` endpoint
that powers the UI's warning — so what the UI shows and what actually runs
can't drift apart.

## Setting an explicit image ref

`runtime_image` is a field on a repo, set via the repo update endpoint:

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<repo_id> \
  -H "Content-Type: application/json" \
  -d '{"runtime_image": "mcr.microsoft.com/devcontainers/go:1.24"}'
```

It's a plain string column (`repos.runtime_image`, migration `055`), empty by
default. See `openapi.yaml`'s `Repo` schema for the full field list.

**Empty `runtime_image` and empty `devcontainer_json` (the default) is
unchanged behavior**: the agent CLI runs in-process in the backend container,
exactly as before this feature existed. Setting either is strictly opt-in,
per repo.

## What happens when `runtime_image` is set

The dispatcher resolves a running container for the repo before starting a
run (`internal/agent/runtime.go`'s `RuntimeManager.EnsureRunning`):

- It looks for an existing container named `ate-runtime-<repo_id>` already
  running the current `runtime_image`. If found, it's reused as-is.
- Otherwise (no container, or one running a stale image), it removes any
  existing container by that name and starts a fresh one with
  `docker run -d ... <image> sleep infinity` — a long-lived container kept
  alive by an idle `sleep`, not a fresh container per task.
- The agent CLI (`claude`, `codex`, etc.) is then run inside it via
  `docker exec -i -w <workdir> ... <container> <bin> <args>`
  (`internal/agent/providers/cli.go`'s `spawn`). Every tool the CLI invokes —
  `Bash`, `Edit`, `cargo test`, whatever — is a child of that process, so it
  inherits the container too.
- Concurrent dispatches for the same repo are serialized around container
  creation (a per-repo lock in `RuntimeManager`), so two tasks racing to
  create the container can't produce duplicates; once running, `docker exec`
  supports concurrent execs into the same container fine.

## What happens when a devcontainer.json is resolved (sources 2 and 3)

When `runtime_image` is empty but a devcontainer.json is resolved — either
`.devcontainer/devcontainer.json` committed in the repo, or
`repos.devcontainer_json` from the UI — the dispatcher takes a different path
(`internal/agent/devcontainer.go`'s `RuntimeManager.EnsureDevcontainerRunning`),
using the [`@devcontainers/cli`](https://github.com/devcontainers/cli)
reference implementation instead of a hand-rolled `docker run`:

1. The winning source's raw JSON is parsed, and this codebase's mount/hardening
   contract is merged into a **copy** of it (see "The injected contract"
   below). The committed repo file itself, if that's the source, is never
   written to — only an in-memory/temp-file copy is modified.
2. That effective JSON is hashed (sha256) and written to a temp file
   (`ate-devcontainer-<repo_id>.json`).
3. `devcontainer up --workspace-folder <repoPath> --config <that file>
   --id-label ate.repo_id=<repo_id> --id-label ate.dcjson=<hash>` is run. The
   CLI parses its `mounts`/`features`/`runArgs` and starts (or reuses) a
   container accordingly, printing JSON to stdout:
   `{"outcome":"success","containerId":"...","remoteUser":"vscode"}`.
4. The agent CLI is then `docker exec`'d into the returned `containerId`,
   exactly as in the `runtime_image` path — `spawn()` in
   `providers/cli.go` doesn't know or care which path produced the container
   id it was handed.

Failure to build (bad JSON, missing Docker, `devcontainer` CLI not installed,
feature install failure, etc.) escalates the run to `waiting_human` — the same
"never silently fall back to running in-process" rule the `runtime_image` path
already follows (see "When the container can't start" below).

### The injected contract

`BuildEffectiveDevcontainerJSON` (`internal/agent/devcontainer.go`) injects,
into a copy of the resolved JSON, before every build:

- **Same-path `workspaceMount` + `workspaceFolder`**, pinned to the repo's
  absolute path on both sides of the mount — for the same git-worktree reason
  the `runtime_image` path's mount table (below) requires it.
- **The `/tmp` bind**, same-path, for the MCP sidecar's config/result-file
  handoff.
- **The MCP sidecar binary**, read-only, at its own absolute path.
- **Provider credential directories** (`credentialDirs`: `.claude`,
  `.claude.json`, `.codex`, `.qwen`), read-write, from the backend's own home
  directory to `RuntimeContainerHome` inside the container — a source
  directory absent on the host is silently skipped, same as the
  `runtime_image` path.
- **Hardening `runArgs`**: `--security-opt=no-new-privileges` and
  `--cap-drop=ALL`, overriding the devcontainer CLI's own defaults
  (`--cap-add SYS_PTRACE --security-opt seccomp=unconfined`).

Critically, **a user's own `mounts` and `runArgs` entries are appended to,
never replaced.** If a devcontainer.json already declares its own `mounts` or
`runArgs` (a repo's committed file legitimately might, for a
`postCreateCommand` dependency or a custom bind), those entries stay, and this
codebase's required entries are added alongside them — the merge never drops
anything the source JSON specified.

### Caching and the rebuild trigger

The effective JSON's sha256 hash is the cache key, carried as the
`ate.dcjson=<hash>` label passed to `devcontainer up --id-label`, alongside
`ate.repo_id=<repo_id>` (the same label `runtime_image` containers carry).
`--id-label` does double duty: it both **sets** the label on a newly-built
container and is what the CLI's own idempotency check **queries against** to
decide whether an existing container can be reused —

- **Unchanged config** → a container already carrying both labels exists →
  `devcontainer up` reuses it as-is. Measured warm-call latency: **~1s**,
  returning the same `containerId`.
- **Changed config** (any key in the effective JSON, including this
  codebase's own injected contract) → the hash differs → no container carries
  that label pair → the CLI builds a fresh one. Measured cold-build latency:
  **~2 minutes** for one feature (base image pull + toolchain install) in the
  spike that validated this design — see `runtime-images.md`.

The now-stale container from a prior effective JSON isn't removed at build
time; it's left running until `worktreesweep`'s reaping pass (below) finds it
carries a `ate.dcjson` label that no longer matches the repo's
currently-resolved hash and removes it. This mirrors how the `runtime_image`
path already leaves stale-image cleanup to the same sweep rather than doing
it inline.

### Reaping

`worktreesweep`'s existing container-reaping pass (`sweeper.go`) is extended
to cover devcontainer-built containers the same way it already covers
explicit-image ones: for each managed container, it recomputes the repo's
*currently expected* hash (`RuntimeManager.ExpectedDevcontainerHash`, which
runs the identical resolve-then-inject-then-hash logic used at build time) and
removes the container if its `ate.dcjson` label doesn't match — repo deleted,
repo's devcontainer config changed, or the repo no longer uses a devcontainer
source at all.

## The mount contract (explicit `runtime_image` path)

The table below describes the mounts `buildDockerRunArgs`
(`internal/agent/runtime.go`) constructs directly for the `runtime_image`
path's plain `docker run`. The devcontainer path achieves the same contract
by injecting the equivalent entries into the devcontainer.json before calling
`devcontainer up` — see "The injected contract" above — so the same mandatory
mounts apply either way, just expressed differently under the hood.

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

## The `HOME=/home/vscode` assumption

Credentials are mounted from the backend's own home directory (e.g.
`~/.claude` on the host) to `/home/vscode/.claude` inside the container —
`agent.RuntimeContainerHome` is hardcoded to `/home/vscode`. The CLI's `HOME`
env var inside the container is also forced to `/home/vscode` for the same
run (`providers/cli.go`'s `containerEnvOverrides` — see below), so the
mounted credentials and the CLI's own idea of `HOME` agree.

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

This is why the devcontainer path (sources 2 and 3 above) doesn't hit this
limitation in practice: the devcontainer CLI's own base images
(`mcr.microsoft.com/devcontainers/*`, which `ghcr.io/devcontainers/features/*`
are built to run on top of) already use a `vscode` user with
`HOME=/home/vscode` by convention — the same convention
`RuntimeContainerHome` assumes. A devcontainer.json built from the language
picker in the UI, or a typical repo-committed one, works out of the box for
this reason. The limitation only bites if a devcontainer.json's own `image`
field points at a plain, non-devcontainer-convention base (e.g. `image:
"golang:1.26"` with no `vscode` user set up by a feature or `postCreateCommand`)
— the same failure mode as pointing `runtime_image` at that image directly.

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
feature does not solve that, and no configuration of it will.

## When the container can't start

If a repo resolves to `runtime_image` or a devcontainer source but the
container can't be created, found running, or built (Docker unreachable,
image pull failure, daemon error, a malformed effective devcontainer.json,
the `devcontainer` CLI missing, a feature install failing, or a runtime
source resolved with no runtime manager configured at all), the dispatcher
does **not** fall back to running the task in-process in the backend
container. Silently falling back would run the agent CLI against a toolchain
and credential set it was never meant to use.

Instead, the run is marked `waiting_human` immediately, before any provider
process starts, with a note describing what failed. The task stays locked to
that run (its dispatch lock is not cleared) so the 5-second dispatch sweep
doesn't immediately retry and hot-loop on the same failure — a human needs to
fix the underlying problem (or clear the repo's runtime configuration) and
reply before the task can be picked up again.

## The `@devcontainers/cli` dependency

The devcontainer path (sources 2 and 3) shells out to the `devcontainer`
binary from [`@devcontainers/cli`](https://www.npmjs.com/package/@devcontainers/cli)
(npm, MIT-licensed reference implementation) — `runDevcontainerUp` in
`internal/agent/devcontainer.go` runs `devcontainer up` directly via
`exec.Command`, no wrapper script in between.

`backend/Dockerfile` installs it globally, pinned via the
`DEVCONTAINER_CLI_VERSION` build arg (0.88.0), for the same reproducibility
reason `CLAUDE_CLI_VERSION` is pinned: a breaking CLI release shouldn't turn a
green build into a broken published image.

Unlike the codex and qwen CLIs — which are gated behind `INSTALL_*_CLI` build
args so the default image stays small — the devcontainer CLI is installed
unconditionally. A repo's runtime configuration lives in the database, not in
the image build, so there is no build-time signal that would tell an operator
to opt in; a repo configured for the devcontainer path would simply fail at
dispatch time (escalating to `waiting_human`) on an image built without it.

**If you run the backend outside this Dockerfile** — a bare `go run`, or a
custom image — the `devcontainer` binary must be on `PATH` for sources 2 and 3
to work. Node alone is not sufficient. Without it, `runtime_image` (source 1)
still works, and a repo resolving to a devcontainer.json fails at build time,
correctly escalating to `waiting_human` per the section above rather than
silently running in-process.

## The UI

The Repos page's create/edit forms include a runtime environment picker
(`RuntimeEnvironmentEditor.tsx`), with three mutually exclusive modes mapping
directly onto the resolution order above:

- **None** — run in the backend container (the default; sends empty
  `runtime_image` and empty `devcontainer_json`).
- **Image ref** — a text field for an explicit image ref (source 1).
- **Dev container** — a devcontainer.json editor whose contents are saved as
  `repos.devcontainer_json` (source 3 — a UI edit can never produce or modify
  source 2, the repo-committed file), with two ways to edit it:
  - A **language picker**: rows for Go, Node, Python, Rust, Java, and Ruby,
    each a free-text version field (not a dropdown — a hardcoded version list
    goes stale invisibly, so it's left to the user to type what they need)
    that reads/writes that language's entry under the JSON's `features` key
    using the same `ghcr.io/devcontainers/features/*` refs documented in
    `runtime-images.md`.
  - A collapsible **raw JSON editor** ("Advanced: edit raw devcontainer.json")
    for anything the picker doesn't model — `mounts`, `postCreateCommand`,
    features the picker doesn't list, or hand-written comments-via-extra-keys.

**Round-trip behavior**: the raw JSON is the single source of truth kept in
component state. The language picker only ever reads and writes the entries
under `features` for the six languages it knows about
(`upsertFeature`/`removeFeature` in `RuntimeEnvironmentEditor.tsx`) — every
other top-level key, and every feature the picker doesn't model, survives
untouched when the picker adds, edits, or removes a language row.

**The "repo file wins" warning**: when editing an existing repo, the form
calls `GET /repos/{id}/devcontainer` and shows an inline warning ("This repo
ships `.devcontainer/devcontainer.json` — those settings win; these are
ignored.") if the repo has a committed file, regardless of what's saved in
`devcontainer_json` — the same accurate-not-misleading goal the resolution
order itself was designed around.

Note: the picker's languages menu shown above is a fixed list matching
`RuntimeEnvironmentEditor.tsx`'s `LANGUAGES` constant; the UI does not
currently surface build progress (building/ready/failed) after saving — a
save just sends the fields to the API, and whether the resulting devcontainer
actually builds successfully is only visible from a task's run outcome (an
immediate `waiting_human` escalation, per the section above) or by calling
`GET /repos/{id}/devcontainer` directly.

## `GET /repos/{id}/devcontainer`

Returns the devcontainer.json that would actually be used to build the
repo's runtime container, resolved with the exact same precedence and merge
logic the dispatcher itself uses (`ResolveDevcontainerSource` +
`EffectiveDevcontainerJSON` — not a re-derived copy of that logic, the same
function calls):

```json
{
  "source": "repo_file",
  "effective_json": "{...the fully-resolved JSON, contract injected...}",
  "repo_file_present": true
}
```

- **`source`** is one of `image_ref`, `repo_file`, `db`, or `none` — which of
  the four resolution-order entries won.
- **`effective_json`** is empty when `source` is `image_ref` (no devcontainer
  build happens at all — an explicit image ref never touches this codepath)
  or `none`. Otherwise it's the resolved JSON with the mount/hardening
  contract already merged in, i.e. what would actually be passed to
  `devcontainer up`.
- **`repo_file_present`** is `true` whenever the repo has its own committed
  `.devcontainer/devcontainer.json`, *regardless* of which source ultimately
  won — this is what lets the UI show its warning even when `source` is
  `image_ref` (repo file present but irrelevant because an explicit image ref
  always wins first).

This is the endpoint the UI's "repo file wins" warning (above) is built on.

## Related

- `internal/agent/runtime.go` — `RuntimeManager`, container lifecycle,
  `buildDockerRunArgs`.
- `internal/agent/devcontainer.go` — devcontainer.json resolution
  (`ResolveDevcontainerSource`), the injected mount/hardening contract
  (`BuildEffectiveDevcontainerJSON`), hashing, and `devcontainer up`
  invocation (`EnsureDevcontainerRunning`).
- `internal/api/handlers/repos.go` — the `runtime_image`/`devcontainer_json`
  repo fields, write-time validation (`validateDevcontainerJSON`), and
  `GET /repos/{id}/devcontainer`.
- `internal/worktreesweep/sweeper.go` — reaping stale runtime containers,
  both explicit-image and devcontainer-built.
- `frontend/src/components/shared/RuntimeEnvironmentEditor.tsx` — the Repos
  page's runtime environment picker.
- `internal/agent/providers/cli.go` — `spawn()`, `containerEnvOverrides`.
- [Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain) —
  the backend-image toolchain this feature is an alternative to.
