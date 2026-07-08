FROM golang:1.26.5-alpine AS builder
RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build both binaries — mcp-server is the signal_complete/request_human sidecar
RUN go build -o bin/server ./cmd/server && \
    go build -o bin/mcp-server ./cmd/mcp-server

# Use node:26-alpine as the runtime so the claude CLI is available
FROM node:26-alpine
# Set to "true" to disable SSL verification for git/npm/Node.js (corporate TLS proxies).
# Pass via: INSECURE_SKIP_SSL_VERIFY=true docker compose build
ARG INSECURE_SKIP_SSL_VERIFY=false
# su-exec drops privileges in the entrypoint; shadow provides usermod/groupmod
# for the runtime PUID/PGID remap (see entrypoint.sh).
RUN apk add --no-cache ca-certificates git github-cli bash build-base su-exec shadow
RUN if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then npm config set strict-ssl false; fi
# Install Claude Code CLI globally so 'claude' is on PATH
RUN npm install -g @anthropic-ai/claude-code

# Gemini CLI (gemini_cli provider) and Codex CLI (codex_cli provider) are
# NOT installed by default — unlike claude above, both are gated behind
# build args (default false) so the default image doesn't grow for users who
# don't need them. Pass via:
#   INSTALL_GEMINI_CLI=true docker compose build
#   INSTALL_CODEX_CLI=true docker compose build
ARG INSTALL_GEMINI_CLI=false
ARG INSTALL_CODEX_CLI=false
RUN if [ "$INSTALL_GEMINI_CLI" = "true" ]; then npm install -g @google/gemini-cli; fi
RUN if [ "$INSTALL_CODEX_CLI" = "true" ]; then npm install -g @openai/codex; fi
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
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chown -R node: /app && chmod +x /usr/local/bin/entrypoint.sh

# Stays root so the entrypoint can remap the node user (PUID/PGID) and chown the
# state dirs; it then drops to node via su-exec before running the server.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./server"]
