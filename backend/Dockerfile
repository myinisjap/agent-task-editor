FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build both binaries — mcp-server is the signal_complete/request_human sidecar
RUN go build -o bin/server ./cmd/server && \
    go build -o bin/mcp-server ./cmd/mcp-server

# Use node:22-alpine as the runtime so the claude CLI is available
FROM node:22-alpine
# Set to "true" to disable SSL verification for git/npm/Node.js (corporate TLS proxies).
# Pass via: INSECURE_SKIP_SSL_VERIFY=true docker compose build
ARG INSECURE_SKIP_SSL_VERIFY=false
RUN apk add --no-cache ca-certificates git github-cli bash build-base
RUN if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then npm config set strict-ssl false; fi
# Install Claude Code CLI globally so 'claude' is on PATH
RUN npm install -g @anthropic-ai/claude-code
ENV SHELL=/bin/sh

# Bring in the same Go toolchain used to build this image's own server binary
# (golang:1.24-alpine builder stage above) so agents can build/test/vet Go
# repos they're assigned to work on. Copying from the builder stage (rather
# than `apk add go`) guarantees the agent-visible Go version matches exactly
# what compiled bin/server and bin/mcp-server.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH=/home/node/go
ENV GOCACHE=/home/node/.cache/go-build
ENV GOMODCACHE=/home/node/go/pkg/mod

# Node.js/npm/npx are already on PATH via the node:22-alpine base image, which
# covers Vite/React/TS projects out of the box (npm ci, npm run build/test,
# vitest, etc.). build-base (gcc, g++, make, musl-dev) above covers cgo builds
# (this project's own backend uses mattn/go-sqlite3) and native npm modules
# that require node-gyp compilation.

# Remap the existing node user to match the host user's UID/GID so bind-mounted
# repo files are writable. Defaults to 1000:1000 if not passed at build time.
ARG HOST_UID=1000
ARG HOST_GID=1000
# Delete the user before the group — Alpine refuses to remove a group that still
# has members, so the old order caused "addgroup: group 'node' in use" when
# delgroup silently failed while node was still attached.
RUN EXISTING_GROUP=$(getent group "$HOST_GID" 2>/dev/null | cut -d: -f1) && \
    if [ "$HOST_GID" != "1000" ] || [ "$HOST_UID" != "1000" ]; then \
      deluser node 2>/dev/null || true; \
    fi && \
    if [ "$HOST_GID" != "1000" ]; then \
      delgroup node 2>/dev/null || true; \
      if [ -z "$EXISTING_GROUP" ]; then \
        addgroup -g "$HOST_GID" node; \
        EXISTING_GROUP=node; \
      fi; \
    else \
      EXISTING_GROUP=node; \
    fi && \
    if [ "$HOST_UID" != "1000" ]; then \
      adduser -D -u "$HOST_UID" -G "$EXISTING_GROUP" node; \
    elif [ "$HOST_GID" != "1000" ]; then \
      adduser -D -u 1000 -G "$EXISTING_GROUP" node; \
    fi && \
    mkdir -p /data && chown node: /data && \
    mkdir -p /home/node/go /home/node/.cache/go-build && \
    chown -R node: /home/node/go /home/node/.cache

WORKDIR /app
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
RUN chown -R node: /app

USER node
# bind-mounted repos are owned by the host UID, not node — allow git to read them
RUN git config --global safe.directory '*' && \
    if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then git config --global http.sslVerify false; fi

EXPOSE 8080
CMD ["./server"]
