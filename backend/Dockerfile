# bookworm (glibc), not alpine (musl): the runtime stage below is now
# node:26-bookworm-slim (glibc, needed for mise's prebuilt python/ruby/java
# toolchains). A CGO binary built here links against musl-libc's dynamic
# loader (mattn/go-sqlite3 pulls in CGO) and will NOT execute on a glibc
# host — confirmed by building on alpine and running the resulting binary
# under debian:bookworm-slim: "exec: no such file or directory" (the ELF
# interpreter path musl wrote into the binary doesn't exist on glibc). The
# builder's libc must match the runtime's libc.
FROM golang:1.26.6-bookworm AS builder
# gcc + libc6-dev (needed for mattn/go-sqlite3's CGO) already ship in the
# golang:*-bookworm image, unlike golang:*-alpine — no apk-style install step
# needed here.

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# VERSION is stamped into cmd/server's `main.Version` via -ldflags, so
# GET /healthz and the Health page's "Version" row report the running
# release tag instead of the default "dev" (see .github/workflows/release.yml,
# which passes VERSION=<git tag> as a build-arg). Left as "dev" for local
# `docker compose build` invocations that don't set it.
ARG VERSION=dev
# Build all binaries — mcp-server is the signal_complete/request_human sidecar;
# mcp-board is the standing board MCP server wired into chat sessions (and
# usable standalone from a chat client). See docs/board-mcp.md.
RUN go build -ldflags "-X main.Version=${VERSION}" -o bin/server ./cmd/server && \
    go build -o bin/mcp-server ./cmd/mcp-server && \
    go build -o bin/mcp-board ./cmd/mcp-board

# node:26-bookworm-slim (glibc), not node:26-alpine (musl): mise's prebuilt
# python/ruby/java toolchains are glibc-only, so alpine can't host per-repo
# runtimes (see docs/runtime.md). Debian's default repos already ship
# usermod/groupmod (the `passwd` package, present by default) and bash, so
# neither needs a separate install like alpine's `shadow` package did.
FROM node:26-bookworm-slim
# Set to "true" to disable SSL verification for git/npm/Node.js (corporate TLS proxies).
# Pass via: INSECURE_SKIP_SSL_VERIFY=true docker compose build
ARG INSECURE_SKIP_SSL_VERIFY=false
# gosu (Debian's actively-maintained equivalent of alpine's su-exec) drops
# privileges in the entrypoint. build-essential covers cgo/node-gyp builds.
# ripgrep (rg) backs the anthropic/llm providers' native `search` tool.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git bash build-essential gosu ripgrep curl \
    && rm -rf /var/lib/apt/lists/*
RUN if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then npm config set strict-ssl false; fi

# gh CLI: installed from a pinned GitHub release binary rather than Debian's
# packaged `gh` (whose version tracks the Debian release, not a version we
# choose) or GitHub's official apt repo (an extra signing-key/repo-add step
# for one binary) — same reproducibility rationale as CLAUDE_CLI_VERSION
# below: two builds of the same tag must produce the same agent runtime.
ARG GH_CLI_VERSION=2.63.2
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_CLI_VERSION}/gh_${GH_CLI_VERSION}_linux_amd64.tar.gz" \
      | tar -xz -C /usr/local --strip-components=1 "gh_${GH_CLI_VERSION}_linux_amd64/bin/gh"

# mise (per-repo language runtime manager): pinned release tarball, same
# reproducibility reason as gh above. Uses the musl-static build rather than
# ghcr.io/jdx/mise's glibc binary — that image is rebuilt against whatever
# glibc its CI runner ships (2.38/2.39), newer than bookworm's 2.36, and
# fails with "GLIBC_2.38 not found" at runtime (confirmed by testing);
# musl-static has no libc dependency, so it runs unchanged on any base.
ARG MISE_VERSION=2026.8.14
RUN curl -fsSL "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-x64-musl.tar.gz" \
      | tar -xz -C /tmp && mv /tmp/mise/bin/mise /usr/local/bin/mise && rm -rf /tmp/mise

# uv (Python venv/package manager, used for the python pin — see
# docs/runtime.md): copied as a prebuilt binary from its own pinned image,
# same reproducibility reason as gh/mise above. uv's image binaries are
# static (no libc dependency), so no musl/glibc mismatch here.
COPY --from=ghcr.io/astral-sh/uv:0.9.9 /uv /usr/local/bin/uv
ENV MISE_DATA_DIR=/home/node/.local/share/mise
ENV UV_CACHE_DIR=/home/node/.cache/uv
# Skip mise's interactive "trust this config" prompts (agent runs are
# non-interactive) and its own self-update/telemetry checks.
ENV MISE_YES=1
# Install Claude Code CLI globally so 'claude' is on PATH
#
# Pinned so two builds of the same git tag produce the same agent runtime and
# a breaking CLI release can't turn a green docker-build into a broken
# published image. Bump deliberately (Dependabot's docker ecosystem only
# tracks FROM lines).
ARG CLAUDE_CLI_VERSION=2.1.223
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CLI_VERSION}

# Codex CLI (codex_cli provider) and Qwen CLI (qwen_code provider) are NOT
# installed by default — unlike claude above, both are gated behind build
# args (default false) so the default image doesn't grow for users who
# don't need them. Pass via:
#   INSTALL_CODEX_CLI=true docker compose build
#   INSTALL_QWEN_CLI=true docker compose build
ARG INSTALL_CODEX_CLI=false
ARG INSTALL_QWEN_CLI=false
# Pinned for the same reproducibility reason as CLAUDE_CLI_VERSION above.
ARG CODEX_CLI_VERSION=0.146.1
ARG QWEN_CLI_VERSION=0.21.6
RUN if [ "$INSTALL_CODEX_CLI" = "true" ]; then npm install -g @openai/codex@${CODEX_CLI_VERSION}; fi
RUN if [ "$INSTALL_QWEN_CLI" = "true" ]; then npm install -g @qwen-code/qwen-code@${QWEN_CLI_VERSION}; fi
ENV SHELL=/bin/sh

# Bring in the same Go toolchain used to build this image's own server binary
# (golang:1.26-bookworm builder stage above) so agents can build/test/vet Go
# repos they're assigned to work on. Copying from the builder stage (rather
# than an apt install) guarantees the agent-visible Go version matches exactly
# what compiled bin/server and bin/mcp-server.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH=/home/node/go
ENV GOCACHE=/home/node/.cache/go-build
ENV GOMODCACHE=/home/node/go/pkg/mod

# Node.js/npm/npx are already on PATH via the node:26-bookworm-slim base image,
# which covers Vite/React/TS projects out of the box (npm ci, npm run
# build/test, vitest, etc.). build-essential above covers cgo builds (this
# project's own backend uses mattn/go-sqlite3) and native npm modules that
# require node-gyp compilation.

# The 'node' user ships at 1000:1000; entrypoint.sh remaps it to PUID/PGID at
# runtime (see below). Create the writable state dirs owned by node up front so
# a default (1000:1000) run needs no chown, and pre-seed node's git config.
# mise/uv's cache dirs are included here (and in entrypoint.sh's runtime
# chown) so per-repo toolchain installs work at both default and remapped
# PUID/PGID — see docs/runtime.md.
RUN mkdir -p /data /home/node/go /home/node/.cache/go-build "$MISE_DATA_DIR" "$UV_CACHE_DIR" && \
    chown -R node: /data /home/node/go /home/node/.cache /home/node/.local && \
    # bind-mounted repos are owned by the host UID, not node — allow git to read them
    HOME=/home/node git config --global safe.directory '*' && \
    if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then HOME=/home/node git config --global http.sslVerify false; fi && \
    chown node: /home/node/.gitconfig

WORKDIR /app
# entrypoint rarely changes, so copy it before the binaries (which change every
# code edit) to keep this chmod layer cached across rebuilds.
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
COPY --from=builder /app/bin/mcp-board .
RUN chown -R node: /app

# Stays root so the entrypoint can remap the node user (PUID/PGID) and chown the
# state dirs; it then drops to node via gosu before running the server.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./server"]
