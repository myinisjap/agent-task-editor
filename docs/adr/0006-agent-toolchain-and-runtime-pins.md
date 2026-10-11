# 6. Agent toolchains: image baseline plus per-repo runtime pins

Status: Accepted

## Context

Agent `Bash`/`run_bash` calls run inside the **backend** container against
bind-mounted repos, not on the host and not in `frontend/Dockerfile`. A
compiler or runtime that exists on the developer's machine is therefore
invisible to agents unless the backend image provides it.

## Decision

Two layers:

1. Image baseline in the *final* stage of `backend/Dockerfile`: Go (copied from
   the Dockerfile's own builder stage, so agents see the same version that
   builds the server), Node/npm from the base image, `build-essential` for cgo
   and native npm addons, and pinned `mise`/`uv` binaries.
2. Per-repo runtime pins (`go`/`node`/`python`/`rust`/`ruby`/`java`/`godot`) set
   on the Repos page and installed via `mise`/`uv` before the provider CLI
   runs (`internal/agent/runtime/`). With no pins, the spawn is byte-identical
   to before the feature.

## Consequences

- Adding a language for every agent means editing the final Dockerfile stage
  and rebuilding; one repo needing a version should use a pin instead.
- The builder stage only compiles this repo's binaries; editing it does not
  change what agents can run.
- Details: `docs/runtime.md`, `docs/getting-started.md#supported-languages--extending-the-toolchain`.
