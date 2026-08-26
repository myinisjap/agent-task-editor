FROM golang:1.26.6-alpine AS builder
RUN apk add --no-cache gcc musl-dev

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
#
# mcp-server is built with CGO_ENABLED=0 (static binary): it's bind-mounted
# by absolute path into arbitrary per-repo runtime containers (see
# docs/runtime-containers.md), whose base image is not under this project's
# control. A cgo/glibc-dynamic binary runs fine on Debian/Ubuntu-based images
# but fails outright on an Alpine/musl one — a common devcontainer base.
# Static linking makes it base-image-agnostic. cmd/server keeps cgo (default)
# because it links mattn/go-sqlite3, which requires it; it always runs inside
# this Dockerfile's own image, never inside a runtime container, so the
# portability concern doesn't apply to it.
RUN go build -ldflags "-X main.Version=${VERSION}" -o bin/server ./cmd/server && \
    CGO_ENABLED=0 go build -o bin/mcp-server ./cmd/mcp-server && \
    go build -o bin/mcp-board ./cmd/mcp-board

# Use node:26-alpine as the runtime so the claude CLI is available
FROM node:26-alpine
# Set to "true" to disable SSL verification for git/npm/Node.js (corporate TLS proxies).
# Pass via: INSECURE_SKIP_SSL_VERIFY=true docker compose build
ARG INSECURE_SKIP_SSL_VERIFY=false
# su-exec drops privileges in the entrypoint; shadow provides usermod/groupmod
# for the runtime PUID/PGID remap (see entrypoint.sh).
# ripgrep (rg) backs the anthropic/llm providers' native `search` tool.
# docker-cli is the `docker` binary itself — needed by internal/agent/runtime.go
# to manage per-repo runtime containers (docker run/exec/ps/rm) when a repo sets
# runtime_image. It only works if /var/run/docker.sock is also bind-mounted in
# (see docker-compose.yml and docs/runtime-containers.md); without the socket,
# the binary is present but every call fails and the affected repo's runs
# escalate to waiting_human, same as if docker-cli weren't installed at all.
RUN apk add --no-cache ca-certificates git github-cli bash build-base su-exec shadow ripgrep docker-cli
RUN if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then npm config set strict-ssl false; fi
# Install Claude Code CLI globally so 'claude' is on PATH
#
# Pinned so two builds of the same git tag produce the same agent runtime and
# a breaking CLI release can't turn a green docker-build into a broken
# published image. Bump deliberately (Dependabot's docker ecosystem only
# tracks FROM lines).
ARG CLAUDE_CLI_VERSION=2.1.223
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CLI_VERSION}

# devcontainer CLI — builds a repo's runtime container from a generated
# devcontainer.json (see docs/runtime-containers.md and
# internal/agent/devcontainer.go). Installed unconditionally rather than
# behind a build arg like the codex/qwen CLIs below, because a repo
# configured for the devcontainer path (a repo-committed devcontainer.json,
# or repos.runtime_languages) fails at dispatch time (escalating the task to
# waiting_human) if the binary is missing, and that config lives in the
# database rather than in the image build — so there is no build-time signal
# that would tell an operator to opt in.
#
# Pinned for the same reproducibility reason as CLAUDE_CLI_VERSION above.
ARG DEVCONTAINER_CLI_VERSION=0.88.0
RUN npm install -g @devcontainers/cli@${DEVCONTAINER_CLI_VERSION}

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
# (golang:1.26-alpine builder stage above) so agents can build/test/vet Go
# repos they're assigned to work on. Copying from the builder stage (rather
# than `apk add go`) guarantees the agent-visible Go version matches exactly
# what compiled bin/server and bin/mcp-server.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH=/home/node/go
ENV GOCACHE=/home/node/.cache/go-build
ENV GOMODCACHE=/home/node/go/pkg/mod

# Node.js/npm/npx are already on PATH via the node:26-alpine base image, which
# covers Vite/React/TS projects out of the box (npm ci, npm run build/test,
# vitest, etc.). build-base (gcc, g++, make, musl-dev) above covers cgo builds
# (this project's own backend uses mattn/go-sqlite3) and native npm modules
# that require node-gyp compilation.

# The 'node' user ships at 1000:1000; entrypoint.sh remaps it to PUID/PGID at
# runtime (see below). Create the writable state dirs owned by node up front so
# a default (1000:1000) run needs no chown, and pre-seed node's git config.
RUN mkdir -p /data /home/node/go /home/node/.cache/go-build && \
    chown -R node: /data /home/node/go /home/node/.cache && \
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
# state dirs; it then drops to node via su-exec before running the server.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./server"]
