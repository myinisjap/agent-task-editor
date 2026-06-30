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
RUN apk add --no-cache ca-certificates git github-cli bash

# Install Claude Code CLI globally so 'claude' is on PATH
RUN npm install -g @anthropic-ai/claude-code
ENV SHELL=/bin/sh

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
    mkdir -p /data && chown node:node /data

WORKDIR /app
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
RUN chown -R node:node /app

USER node
# bind-mounted repos are owned by the host UID, not node — allow git to read them
RUN git config --global safe.directory '*'

EXPOSE 8080
CMD ["./server"]
