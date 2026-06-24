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
RUN apk add --no-cache ca-certificates git

# Install Claude Code CLI globally so 'claude' is on PATH
RUN npm install -g @anthropic-ai/claude-code

RUN addgroup -S appuser && adduser -S appuser -G appuser && \
    mkdir -p /data && chown appuser:appuser /data

WORKDIR /app
COPY --from=builder /app/bin/server .
COPY --from=builder /app/bin/mcp-server .
RUN chown -R appuser:appuser /app

USER appuser

EXPOSE 8080
CMD ["./server"]
