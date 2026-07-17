#!/usr/bin/env bash
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

# --all-cli builds the backend image with the Gemini, Codex, and Qwen CLIs
# installed alongside Claude (see backend/Dockerfile's INSTALL_*_CLI build
# args, wired through docker-compose.yml) instead of the default Claude-only
# image. Mirrors run.sh --all-cli, which selects the prebuilt `-all-cli` tag.
if [[ "$ALL_CLI" == "true" ]]; then
  export INSTALL_GEMINI_CLI=true
  export INSTALL_CODEX_CLI=true
  export INSTALL_QWEN_CLI=true
fi

if [[ -z "$REPO_BASE_DIR" ]]; then
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
export AGENT_RAW_LOG_DIR
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
if [[ -n "$TRAEFIK_HOST" ]]; then
  COMPOSE="docker compose -f docker-compose.yml -f docker-compose.traefik.yml"
fi

# Extract GH token from gh CLI (keyring or hosts.yml) if not already set.
if [[ -z "$GH_TOKEN" ]] && command -v gh &>/dev/null; then
  GH_TOKEN=$(gh auth token 2>/dev/null) && export GH_TOKEN
fi

# On macOS, Claude Code stores OAuth credentials in the Keychain rather than a
# file, so the container can't read them. Sync to ~/.claude/.credentials.json
# (which is inside the already-mounted ~/.claude volume) before starting.
if [[ "$(uname)" == "Darwin" ]] && command -v security &>/dev/null; then
  if security find-generic-password -s "Claude Code-credentials" -w \
      > "$HOME/.claude/.credentials.json" 2>/dev/null; then
    echo "Claude credentials synced from macOS Keychain → ~/.claude/.credentials.json"
  fi
fi

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
    kill $(lsof -ti :8080 :5173 :5174 :5175 2>/dev/null) 2>/dev/null
    pkill -f 'agent-task-editor/backend/server' 2>/dev/null
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

    echo "Building backend..."
    (cd "$SCRIPT_DIR/backend" && go build -o server ./cmd/server)

    echo "Starting backend on :8080..."
    (cd "$SCRIPT_DIR/backend" && MCP_SERVER_PATH="$MCP_SERVER_PATH" LOG_LEVEL=DEBUG AGENT_RAW_LOG_DIR="$AGENT_RAW_LOG_DIR" ./server) &
    BACKEND_PID=$!

    echo "Starting frontend on :5173..."
    (cd "$SCRIPT_DIR/frontend" && npm install --silent && VITE_API_BASE_URL=http://localhost:8080 VITE_WS_BASE_URL=ws://localhost:8080 npm run dev) &
    FRONTEND_PID=$!

    echo ""
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    echo "Press Ctrl+C to stop both."
    wait $BACKEND_PID $FRONTEND_PID
    ;;
  *)
    echo "Usage: $0 [--repo-dir <path>] [--all-cli] [--raw-log-dir <path>] [start|stop|restart|logs|login|shell|dev]"
    exit 1
    ;;
esac
