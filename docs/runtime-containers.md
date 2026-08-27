# Per-Repo Runtime Containers

By default, every agent CLI run executes *inside the backend container*, against
a bind-mounted copy of your repo (see
[Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain)).
That means every repo shares one toolchain — whatever's installed in
`backend/Dockerfile`'s final image — and two repos that need conflicting
versions of the same language can't both be served by one backend image.

A repo can opt out of that shared toolchain: its agent CLI runs inside its own
long-lived container instead, either built from an image you choose directly,
or generated from a picked list of languages.

## Resolution order

Each repo resolves to exactly one of these, checked in order (`dispatcher.go`'s
`startRun`):

1. **`repos.runtime_image`** — an explicit image ref → `docker run`. Wins
   outright over everything below; no devcontainer path is even considered.
2. **A repo-committed `.devcontainer/devcontainer.json`** (`ReadRepoDevcontainerFile`)
   → `devcontainer up`. Repo-committed content is code the agent already runs
   against — checked out and executed on every run the same as any other file
   in the repo — so it is treated as **not a new trust boundary**, unlike a
   UI-authored config would be. It is still never merged with anything the
   backend generates: the file is passed to `devcontainer up` completely
   unmodified, so a repo relying on this must declare its own mounts if it
   needs this codebase's own mounts (MCP sidecar, credential dirs).
3. **`repos.runtime_languages`** — the language picker's list → a
   backend-generated `devcontainer.json` → `devcontainer up`. See "The
   language picker" below.
4. **None** (the default) — run in-process in the backend container, exactly
   as before this feature existed.

A repo with a runtime source configured (2 or 3) but no `RuntimeManager`
wired up, or a Docker/`devcontainer` CLI failure while ensuring the
container, escalates the run to `waiting_human` — it never silently falls
back to in-process, since that would run the agent CLI against a toolchain
and credential set it was never meant to use. Same behavior `runtime_image`
already had (see "When the container can't start" below).

## The language picker

Instead of hand-rolling a `runtime_image`, a repo can select from a fixed,
backend-defined list of languages and versions. The backend generates the
entire `devcontainer.json` for you — there is no free-form JSON field.

Six languages are supported today (`runtimeLanguageAllowlist` in
`internal/agent/devcontainer.go`), each mapped to a
[devcontainer feature](https://containers.dev/features) reference:

| id | Feature ref |
|---|---|
| `go` | `ghcr.io/devcontainers/features/go:1` |
| `node` | `ghcr.io/devcontainers/features/node:2` |
| `python` | `ghcr.io/devcontainers/features/python:1` |
| `rust` | `ghcr.io/devcontainers/features/rust:1` |
| `java` | `ghcr.io/devcontainers/features/java:1` |
| `ruby` | `ghcr.io/devcontainers/features/ruby:2` |

It's a plain table, not a plugin system — adding a language is one entry plus
a matching addition to the frontend's `LANGUAGE_IDS` list.

**Version is free text**, not a dropdown of known-good versions: version
lists for a feature go stale, and a stale dropdown fails invisibly (it looks
like a valid choice right up until the feature install fails at container
build time). You type whatever version string the feature accepts (e.g.
`1.26`, `20`, `3.12`) — it's validated only for shape
(`^[A-Za-z0-9][A-Za-z0-9._-]*$`, max 32 chars), not against any list of
versions that actually exist.

### Setting it via the UI

On the Repos page, "Runtime environment" is a three-way choice for both
creating and editing a repo: **None**, **Image ref** (the plain-text
`runtime_image` field), or **Languages** (`RuntimeLanguagesEditor` — a
repeatable list of allowlisted-id dropdown + free-text-version rows, add/remove
per row). Switching between modes doesn't discard whatever you'd already
typed into another mode within the same edit session. When editing a repo
that ships a committed `.devcontainer/devcontainer.json`, the Languages panel
shows an inline warning that the repo file wins and the selections below are
ignored (via `GET /repos/{id}/devcontainer` — see below). There is
deliberately no raw devcontainer.json editor in this UI — see "The security
property" below for why.

### Setting it via the API

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<repo_id> \
  -H "Content-Type: application/json" \
  -d '{"runtime_languages": [{"id": "go", "version": "1.26"}, {"id": "node", "version": "20"}]}'
```

`runtime_languages` is a typed array of `{id, version}` objects on the `Repo`
schema (`openapi.yaml`), not a free-form string — the same pointer/omit
convention as `runtime_image` applies: omitting the field on a `PATCH`
preserves the stored value, `[]` clears it. An unknown `id` or a `version`
that fails the shape check is rejected with `400`, naming the offending
id/version, and nothing is persisted for that request.

`GET /repos/{id}/devcontainer` reports which of the four sources currently
governs the repo (`{"source": "image_ref" | "repo_file" | "languages" | "none",
"effective_json": "...", "repo_file_present": bool}`) — read-only, it never
starts a container. `effective_json` is always generated or repo-committed
output, safe to return as-is: it never contains a user-authored string beyond
the already-validated language ids/versions.

## The security property

**No user-authored string ever reaches a Docker `runArgs` flag or a `mounts`
entry.** This is the reason there is no raw devcontainer.json editor
anywhere in this feature, and it's worth stating plainly because an earlier
version of this UI got it wrong.

That earlier version accepted a user-supplied `devcontainer.json` directly —
a full JSON blob including its own `runArgs` and `mounts` arrays — and
**merged** those into the backend's own generated arrays (appending, to
preserve whatever the user had written) rather than replacing them. Because
the merge happened before this codebase's own hardening flags were appended,
a config containing `"runArgs": ["--privileged"]` survived right alongside
`--cap-drop=ALL` — Docker doesn't let a later `--cap-drop` retract an earlier
`--privileged`, so the container ran fully privileged regardless of flag
order. The practical effect: anyone who could `PATCH` a repo's config could
turn "can edit a repo's settings" into "can run as root on the Docker host."
A security review caught this and the whole path was deleted.

The current design closes that hole at the source, not by trying to sanitize
the JSON more carefully: **the backend generates the entire devcontainer.json
itself**, from a fixed allowlist, and there is no step anywhere that parses
or folds in a user-authored JSON document.

- Users select a language id from a **fixed allowlist** — an id not in the
  table is a `400`, never silently dropped or passed through.
- A version string is checked against a narrow regex
  (`^[A-Za-z0-9][A-Za-z0-9._-]*$`, max 32 chars) — anything else is a `400`.
- `GenerateDevcontainerJSON` builds `image`, `features`, `mounts`, `runArgs`,
  `workspaceMount`, and `workspaceFolder` from these validated inputs plus
  this codebase's own fixed mount/hardening contract. `mounts` and `runArgs`
  are **set**, not merged — there is no user input in scope at that point to
  merge with, so there is nothing an id or version string could inject into
  either array. The only place a version string ends up is as the `"version"`
  value inside a `features/<ref>` JSON object.
- There is no raw-JSON escape hatch in the UI or the API for the generated
  path. A repo that needs more control than "pick languages and versions"
  has two options, both of which stay outside this trust boundary
  intentionally: commit a `.devcontainer/devcontainer.json` to the repo
  itself (already code the agent runs against, not a new surface — see
  resolution order step 2 above), or set `runtime_image` to a prebuilt image.

If a future change reintroduces a user-supplied JSON blob anywhere in this
path, it must re-derive this analysis first — that repeat mistake is exactly
what this section exists to prevent.

## Detecting a repo's languages

Picking a repo's `runtime_languages` by hand means knowing offhand which
versions its manifests declare. `POST /repos/{id}/detect-languages` suggests
them instead — but **it only ever fills the picker; it never writes
`repos.runtime_languages` itself.** Nothing is persisted until the user
reviews the suggestions in the UI and hits Save on a subsequent
`PATCH /repos/{id}`. There is no auto-detection on repo registration or
anywhere else — the endpoint only runs when the user clicks "Detect from
repo" in the Languages mode of the runtime picker.

### The three layers

1. **Manifest scan** (`agent.DetectLanguages`, `internal/agent/detectlang.go`)
   — no LLM, no network, deterministic, and it runs on every detect request.
   For the common case (an exact `go.mod` version, a pinned `.nvmrc`) it's
   also more accurate than an LLM guess, since these files don't need
   interpreting.
2. **Claude fallback** (`agent.DetectLanguagesWithFallback`,
   `internal/agent/detectlang_llm.go`), invoked **only when the scan leaves a
   gap** — zero suggestions, or any suggestion flagged `Ambiguous`. A clean,
   fully-versioned scan result never reaches the model: `needsLLMFallback`
   short-circuits before any LLM call is made, so a repo with a normal
   `go.mod` and a pinned `.nvmrc` spends nothing and waits nothing.
3. **Human confirms**, always. The response is suggestions; a row is only
   ever written to the repo's `runtime_languages` when the user hits Save.

### What each language is detected from

The scanner checks the repo root plus one level of subdirectories (to catch
a monorepo layout, e.g. a `frontend/` dir), skipping `.git`, `node_modules`,
`vendor`, `target`, and `.ate-worktrees`. Per language, files are checked in
this priority order — first match wins, and presence without a parseable
version still counts as a suggestion (flagged `Ambiguous`, with an empty
version the user must fill in):

| id | files, in priority order | notes |
|---|---|---|
| go | `go.mod` (`^go (\d+\.\d+)`) | exact version only; no fallback file |
| node | `.nvmrc`, then `package.json`'s `engines.node` | a range (e.g. `>=18 <21`) or a non-version token (`lts/hydrogen`, `*`) is flagged ambiguous |
| python | `.python-version`, then `pyproject.toml`'s `requires-python`, then `runtime.txt` (Heroku-style `python-3.12.1`) | a range is flagged ambiguous |
| rust | `rust-toolchain.toml`'s `channel`, then `rust-toolchain`, then `Cargo.toml`'s `rust-version` | `stable`/`beta`/`nightly` count as ambiguous presence, not a version |
| ruby | `.ruby-version` (rbenv/chruby's `ruby-3.3.0` prefix is stripped), then `Gemfile`'s `ruby "..."` directive | |
| java | `pom.xml`'s `maven.compiler.release` or `java.version` property, then `.sdkmanrc`'s `java=` line | an unresolved `${java.version}` property, or an sdkman identifier like `17.0.2-tem`, degrades to ambiguous presence rather than a bogus exact version |

A file that exists but doesn't parse (malformed XML, an unreadable manifest)
is skipped for that one language — it never fails the rest of the scan. Each
suggestion's `source` field is the actual path found (e.g. `go.mod`, or
`frontend/.nvmrc` for a monorepo subdirectory), shown in the UI so the user
can judge it directly rather than trust a black box.

### Suggestions reflect what the repo declares, not everything it needs

This is the main way a suggestion can mislead if read too quickly: detection
only ever reports what a manifest *says*, not everything the repo actually
needs to build and run. **A Go backend with a React frontend but no
`.nvmrc`/`package.json` `engines.node` field will suggest only `go` — it
won't infer Node just because a `frontend/` directory with `.tsx` files
exists.** The scanner (and, above it, the LLM fallback) has no notion of
"this project probably also needs X"; it reports evidence, not inference
beyond what's written down. Review the suggestions against what the repo
actually requires before saving, not just against what's shown.

### Ambiguous and no-version suggestions

A suggestion is `ambiguous: true` when the scan found evidence of a language
but couldn't pin an exact version — a range (`>=18 <21`), a non-version
channel name (`stable`), an unresolvable placeholder (`${java.version}`), or
simply a manifest present with no version field at all (e.g. an empty
`Cargo.toml`). The UI (`RuntimeLanguagesEditor`) renders these rows with a
visible "— needs confirmation" note next to their source, and additionally
"— no version detected, pick one" when the version is empty. Nothing about
an ambiguous suggestion is auto-resolved; the user must type or confirm a
version themselves before Save will accept it.

### `POST /repos/{id}/detect-languages`

```bash
curl -X POST http://localhost:8080/api/v1/repos/<repo_id>/detect-languages
```

Response:

```json
{
  "suggestions": [
    { "id": "go", "version": "1.26", "source": "go.mod", "ambiguous": false },
    { "id": "node", "version": "", "source": "frontend/package.json", "ambiguous": true }
  ],
  "used_llm": false
}
```

`used_llm` is `true` only when the Claude fallback ran *and* produced at
least one guess that survived validation — a scan-only result, or a fallback
that ran but was discarded (see below), both report `false`. The "Detect
from repo" button surfaces `used_llm: true` as an inline "suggested by
Claude — please confirm" note, so the user knows when a value came from a
model rather than a manifest. Detecting an id the form already has replaces
that row rather than duplicating it; a detect that fails is a non-blocking
inline error, and the form is left exactly as the user had it.

### The security posture

Detection output is treated as **untrusted input**, whether it came from the
manifest scanner or from Claude:

- Every suggestion — scanner or LLM — must pass `ParseRuntimeLanguages`
  (the same validation any other `runtime_languages` write goes through)
  before it can be returned to the client. An unknown id or a version
  outside the allowed charset/length is rejected, never sanitized.
- The Claude fallback's output is JSON-only by prompt constraint, parsed
  strictly into a typed `{id, version}` struct, then run through
  `ParseRuntimeLanguages`. **A single invalid entry drops the whole batch** —
  a hostile or malformed guess (e.g. an id like `--privileged` that isn't in
  the six-value allowlist) doesn't get partially accepted by picking out the
  "good" entries; the caller just falls back to the scan's own result.
- **Symlinked manifests are skipped outright**, not followed. `os.Open`
  follows symlinks and `Stat` reports the target, so without this guard a
  repo committing `.nvmrc -> ~/.aws/credentials` would have that file's
  contents read in-process by the backend (which has broad filesystem access
  and no container isolation) and, on the ambiguous path, packed into the
  prompt sent to the model — a read-side exfiltration path distinct from the
  write-side "no user string becomes a Docker flag" property described
  below. `readManifestCapped` refuses any path whose `Lstat` reports a
  symlink before ever opening it.
- The Claude fallback is invoked one-shot (`--print --output-format json`,
  `--max-turns 1`, a 60s timeout), fed only the manifest contents the scanner
  already found (capped) plus a shallow directory listing — never the whole
  repo. If no provider is configured/authenticated, the call errors and the
  endpoint silently degrades to the scan result (`used_llm: false`); it never
  fails the request.

### Known limitation: no rate limit on the LLM-spending route

`POST /repos/{id}/detect-languages` is the only detection endpoint that can
invoke the `claude` CLI, at up to 60 seconds and one real Claude API call per
request when the scan is ambiguous. It has no rate limit of its own — it
requires the same bearer-token auth as every other `/api/v1` route, but
nothing caps how many times an authenticated caller can trigger it back to
back. In this project's single-user, self-hosted deployment model that's
cost exposure rather than a privilege boundary (anyone who can call this
route can already do anything else the API allows), but it is a real gap:
scripting repeated calls to this route runs up API spend with no backend
throttle to stop it.

## Caching and the rebuild trigger

Every generated (or repo-committed) `devcontainer.json` is hashed
(`HashDevcontainerJSON`, sha256) and passed to `devcontainer up` as
`--id-label ate.dcjson=<hash>`, alongside `--id-label ate.repo_id=<repo_id>`.
The `devcontainer` CLI uses those two labels for double duty: they both tag
the container it creates *and* are what it queries against to decide whether
an existing container can be reused.

- **Unchanged config** (same repo, same hash) — the CLI finds a container
  already carrying both labels and reuses it as-is. Verified ~1s warm.
- **Changed config** (a language added/removed, a version bumped, or the
  repo's committed file edited) — no container carries the new hash, so the
  CLI builds a fresh one. Verified ~2min cold for a single feature.

A now-stale container from a prior config is left running rather than
removed at build time — `worktreesweep`'s reaping pass (the same one that
already handles `runtime_image` staleness) is what cleans it up once the
repo goes idle; see "Reaping stale and orphaned containers" below.

The hash is a pure function of the language list (or the repo file's raw
bytes) alone — it does **not** depend on which provider credential
directories happen to exist on the host at generation time. All of
`credentialDirs` are mounted unconditionally into a generated config, unlike
the explicit-`runtime_image` path below which skips a mount for a
nonexistent source dir. This avoids a real bug from an earlier iteration: if
the hash depended on host `os.Stat` results, the same language list could
hash differently between the dispatcher and the sweeper (or across hosts),
causing spurious rebuilds and reap/keep disagreements.

## The `@devcontainers/cli` requirement

Resolution order steps 2 and 3 both shell out to the `devcontainer` CLI
(`devcontainer up ...`), so it must be installed in the backend image.
`backend/Dockerfile` installs `@devcontainers/cli` globally via npm, pinned
to a fixed version (`DEVCONTAINER_CLI_VERSION`, currently `0.88.0`) for the
same reproducibility reason the `claude` CLI version is pinned — two builds
of the same git tag should produce the same agent runtime. Unlike the
optional `codex`/`qwen` CLIs, it's installed **unconditionally**, not behind
a build arg: a repo configured for step 2 or 3 fails at dispatch time
(escalating to `waiting_human`) if the binary is missing, and that
configuration lives in the database rather than in the image build — there's
no build-time signal that would tell an operator to opt in, the way there is
for the optional provider CLIs.

## Requirements

Two things must be true for `runtime_image` (or the devcontainer paths above)
to actually work, on top of setting the relevant field:

1. **The `docker` CLI must be on the backend image's `PATH`.** `backend/Dockerfile`
   installs it via `apk add docker-cli`.
2. **The backend container must be able to reach a Docker daemon** — in
   practice, `/var/run/docker.sock` bind-mounted in. Both `docker-compose.yml`
   and `docker-compose.release.yml` mount it into the `backend` service by
   default.

If either is missing, every task on a repo with `runtime_image` set (or
resolving to a repo-committed devcontainer file or `runtime_languages`)
escalates to `waiting_human` with an error like `exec: "docker": executable
file not found in $PATH` or a permission/connection error talking to the
socket — see "When the container can't start" below. Read the socket section
before relying on this in production: mounting it grants the backend
host-root-equivalent access (see "The Docker socket requirement, stated
honestly" below).

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
common case for anyone not using a per-repo runtime container at all), this
step is a no-op.

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
- The same applies to a devcontainer-CLI-managed container's `ate.dcjson`
  label: the sweep recomputes each repo's currently-expected hash (from its
  repo-committed file or its `runtime_languages`, mirroring the dispatcher's
  own resolution order) and reaps any container whose label no longer
  matches — a language added/removed/changed, or the repo falling back to a
  different source entirely.
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
- `internal/agent/devcontainer.go` — the language allowlist, `ParseRuntimeLanguages`,
  `GenerateDevcontainerJSON`, `EnsureDevcontainerRunning`/`EnsureDevcontainerRunningFromFile`.
- `internal/agent/dispatcher.go` — resolves the four runtime sources before
  `startRun` and escalates to `waiting_human` on failure.
- `internal/worktreesweep/sweeper.go` — reaping stale/orphaned runtime
  containers (both `ate.image` and `ate.dcjson`), skipping repos with an
  in-flight run.
- `internal/api/handlers/repos.go` — `resolveRuntimeLanguages` validation,
  `GET /repos/{id}/devcontainer`, `DetectLanguages` (`POST
  /repos/{id}/detect-languages`).
- `internal/agent/detectlang.go` — the manifest scanner (`DetectLanguages`),
  per-language extractors, symlink skip.
- `internal/agent/detectlang_llm.go` — the Claude fallback gate
  (`needsLLMFallback`), `DetectLanguagesWithFallback`, LLM output validation
  and merge.
- `internal/agent/providers/claude_detectlang.go` — the one-shot `claude
  --print` detection call.
- `frontend/src/components/shared/RuntimeLanguagesEditor.tsx`,
  `frontend/src/pages/ReposPage.tsx` — the Repos page's three-way runtime
  picker and its "Detect from repo" button.
- `internal/agent/providers/cli.go` — `spawn()`, `containerEnvOverrides`.
- `backend/entrypoint.sh` — the Docker-socket-gid join described above.
- [Supported Languages & Extending the Toolchain](getting-started.md#supported-languages--extending-the-toolchain) —
  the backend-image toolchain this feature is an alternative to.
