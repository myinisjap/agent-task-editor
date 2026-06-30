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
RUN apk add --no-cache ca-certificates git github-cli bash build-base

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
RUN if [ "$HOST_GID" != "1000" ]; then \
      delgroup node 2>/dev/null; addgroup -g "$HOST_GID" node; \
    fi && \
    if [ "$HOST_UID" != "1000" ]; then \
      deluser node 2>/dev/null; adduser -D -u "$HOST_UID" -G node node; \
    fi && \
    mkdir -p /data && chown node:node /data && \
    mkdir -p /home/node/go /home/node/.cache/go-build && \
    chown -R node:node /home/node/go /home/node/.cache

WORKDIR /app
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
RUN chown -R node:node /app

USER node
# bind-mounted repos are owned by the host UID, not node — allow git to read them
RUN git config --global safe.directory '*'

EXPOSE 8080
CMD ["./server"]
