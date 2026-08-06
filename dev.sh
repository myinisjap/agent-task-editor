#!/usr/bin/env bash
# Exit on error/unset var/failed pipeline so a failed build (e.g. `go build`
# in the `dev` branch below) can't silently fall through to launching a stale
# binary from a previous run.
set -euo pipefail

# Load .env if present (without overriding existing shell vars)
if [[ -f "$(dirname "$0")/.env" ]]; then
  set -o allexport
  source "$(dirname "$0")/.env"
  set +o allexport
fi

export LLM_API_KEY=${LLM_API_KEY:-"your_api_key_here"}
export LLM_BASE_URL=${LLM_BASE_URL:-"http://localhost:8081/v1"}
export LLM_MODEL=${LLM_MODEL:-"gemma-4-12B-it-qat-UD-Q4_K_XL"}

# Parse optional --repo-dir <path> / --all-cli flags before the command.
ALL_CLI=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir) REPO_BASE_DIR="$2"; shift 2 ;;
    --all-cli) ALL_CLI=true; shift ;;
    --raw-log-dir) AGENT_RAW_LOG_DIR="$2"; shift 2 ;;
    *) break ;;
  esac
done

# --all-cli builds the backend image with the Codex and Qwen CLIs installed
# alongside Claude (see backend/Dockerfile's INSTALL_*_CLI build args, wired
# through docker-compose.yml) instead of the default Claude-only image.
# Mirrors run.sh --all-cli, which selects the prebuilt `-all-cli` tag.
if [[ "$ALL_CLI" == "true" ]]; then
  export INSTALL_CODEX_CLI=true
  export INSTALL_QWEN_CLI=true
fi

if [[ -z "${REPO_BASE_DIR:-}" ]]; then
  REPO_BASE_DIR="/tmp/repos"
  echo "Warning: REPO_BASE_DIR not set — defaulting to $REPO_BASE_DIR (pass --repo-dir <path> or export REPO_BASE_DIR to override)"
fi

# Reject paths that would shadow critical system directories inside the container.
_UNSAFE_PREFIXES=("/" "/app" "/bin" "/boot" "/data" "/dev" "/etc" "/home" "/lib" "/lib64" "/proc" "/root" "/run" "/sbin" "/sys" "/tmp" "/usr" "/var")
for _prefix in "${_UNSAFE_PREFIXES[@]}"; do
  if [[ "$REPO_BASE_DIR" == "$_prefix" ]]; then
    echo "Error: REPO_BASE_DIR='$REPO_BASE_DIR' is a system path and cannot be used as a repo base"
    exit 1
  fi
done
unset _prefix _UNSAFE_PREFIXES

export REPO_BASE_DIR
# Dev-only raw agent-log capture. --raw-log-dir sets AGENT_RAW_LOG_DIR; the
# `dev` (local) path uses it as-is, while `start`/`restart` (docker) ignore the
# host value and write to /data/raw-logs on the db_data volume — see
# docker-compose.yml. Export so compose can gate the env line on its presence.
export AGENT_RAW_LOG_DIR="${AGENT_RAW_LOG_DIR:-}"
# Passed to the backend container, which remaps its runtime user to these so
# files agents write to bind-mounted repos are owned by the host user rather
# than root (see backend/entrypoint.sh).
export PUID=$(id -u) PGID=$(id -g)

# Compute SSL-bypass env vars here rather than in docker-compose.yml, because
# compose's ${VAR:+word} expansion fires on any non-empty string (including
# "false"), which would silently disable SSL when a user sets
# INSECURE_SKIP_SSL_VERIFY=false in their shell or .env file.
if [[ "${INSECURE_SKIP_SSL_VERIFY:-}" == "true" ]]; then
  export GIT_SSL_NO_VERIFY=true
  export NPM_CONFIG_STRICT_SSL=false
  export NODE_TLS_REJECT_UNAUTHORIZED=0
else
  export GIT_SSL_NO_VERIFY=
  export NPM_CONFIG_STRICT_SSL=
  export NODE_TLS_REJECT_UNAUTHORIZED=
fi

COMPOSE="docker compose"
if [[ -n "${TRAEFIK_HOST:-}" ]]; then
  COMPOSE="docker compose -f docker-compose.yml -f docker-compose.traefik.yml"
fi

# Extract GH token from gh CLI (keyring or hosts.yml) if not already set.
if [[ -z "${GH_TOKEN:-}" ]] && command -v gh &>/dev/null; then
  if GH_TOKEN=$(gh auth token 2>/dev/null); then
    export GH_TOKEN
  fi
fi

# On macOS, Claude Code stores OAuth credentials in the Keychain rather than a
# file, so the container can't read them. Sync to ~/.claude/.credentials.json
# (which is inside the already-mounted ~/.claude volume) before starting.
# Written via a temp file + atomic move (rather than redirecting straight into
# the destination) so a missing/locked keychain entry doesn't truncate a
# previously-valid credentials file.
if [[ "$(uname)" == "Darwin" ]] && command -v security &>/dev/null; then
  mkdir -p "$HOME/.claude"
  _cred_tmp=$(mktemp)
  if security find-generic-password -s "Claude Code-credentials" -w > "$_cred_tmp" 2>/dev/null; then
    chmod 600 "$_cred_tmp"
    mv "$_cred_tmp" "$HOME/.claude/.credentials.json"
    echo "Claude credentials synced from macOS Keychain → ~/.claude/.credentials.json"
  else
    rm -f "$_cred_tmp"
    # Keychain entry missing/locked or the user denied access — leave any
    # existing ~/.claude/.credentials.json untouched rather than truncating it.
    echo "Note: could not read Claude credentials from the macOS Keychain; leaving ~/.claude/.credentials.json as-is"
  fi
  unset _cred_tmp
fi

# Docker creates a *directory* at the source path of a bind mount when it
# doesn't exist, so a user who has never run the Claude CLI locally would end
# up with ~/.claude.json as a directory (breaking `./dev.sh login` and
# host-side `claude`). Pre-create it as an empty file.
mkdir -p "$HOME/.claude"
[ -e "$HOME/.claude.json" ] || : > "$HOME/.claude.json"

CMD=${1:-start}

case "$CMD" in
  start)
    $COMPOSE up -d --build
    echo ""
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    ;;
  stop)
    $COMPOSE down
    ;;
  restart)
    $COMPOSE down
    $COMPOSE up -d --build
    ;;
  logs)
    $COMPOSE logs -f backend
    ;;
  login)
    # Authenticate Claude CLI inside the running backend container. Run as the
    # node user (the container's PID 1 drops to node via su-exec) so credentials
    # land in the mounted /home/node/.claude, not root's home.
    $COMPOSE exec --user node backend claude login
    ;;
  shell)
    $COMPOSE exec --user node backend sh
    ;;
  dev-stop)
    # Kill any orphaned dev processes by port.
    _dev_pids=$(lsof -ti :8080 :5173 :5174 :5175 2>/dev/null || true)
    if [[ -n "$_dev_pids" ]]; then
      kill $_dev_pids 2>/dev/null || true
    fi
    unset _dev_pids
    pkill -f 'agent-task-editor/backend/server' 2>/dev/null || true
    echo "dev processes stopped"
    ;;
  dev)
    # Start backend and frontend as local processes (no Docker).
    # Requires: Go, Node.js/npm installed locally.
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    trap 'kill 0' INT TERM EXIT

    echo "Building MCP server..."
    (cd "$SCRIPT_DIR/backend" && go build -o mcp-server ./cmd/mcp-server)
    MCP_SERVER_PATH="$SCRIPT_DIR/backend/mcp-server"

    echo "Building board MCP server..."
    (cd "$SCRIPT_DIR/backend" && go build -o mcp-board ./cmd/mcp-board)
    MCP_BOARD_PATH="$SCRIPT_DIR/backend/mcp-board"

    echo "Building backend..."
    (cd "$SCRIPT_DIR/backend" && go build -o server ./cmd/server)

    echo "Starting backend on :8080..."
    (cd "$SCRIPT_DIR/backend" && MCP_SERVER_PATH="$MCP_SERVER_PATH" MCP_BOARD_PATH="$MCP_BOARD_PATH" LOG_LEVEL=DEBUG AGENT_RAW_LOG_DIR="$AGENT_RAW_LOG_DIR" ./server) &
    BACKEND_PID=$!

    echo "Starting frontend on :5173..."
    (cd "$SCRIPT_DIR/frontend" && npm install --silent && VITE_API_BASE_URL=http://localhost:8080 VITE_WS_BASE_URL=ws://localhost:8080 npm run dev) &
    FRONTEND_PID=$!

    echo ""
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    echo "Press Ctrl+C to stop both."
    # `|| true`: a backgrounded process exiting (Ctrl+C, or one side dying)
    # makes `wait` return non-zero, which would otherwise trip `set -e` here
    # and turn a normal Ctrl+C into a spurious error path.
    wait "$BACKEND_PID" "$FRONTEND_PID" || true
    ;;
  *)
    echo "Usage: $0 [--repo-dir <path>] [--all-cli] [--raw-log-dir <path>] [start|stop|restart|logs|login|shell|dev]"
    exit 1
    ;;
esac
